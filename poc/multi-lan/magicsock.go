package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/mem"
	"golang.org/x/crypto/nacl/box"
	"golang.zx2c4.com/wireguard/conn"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/net/stun"
	"tailscale.com/types/key"
)

// magicSock is a much smaller cousin of Tailscale's magicsock. One instance
// serves every LAN the device is in: one UDP socket, one relay connection, one
// path state per peer. That is the point of the experiment: adding a second
// LAN adds peers, not sockets, keys or relay connections.
//
// Path selection mirrors Tailscale: send via the relay until a direct UDP path
// answers a ping, then switch; if the direct path stops answering for `trust`,
// fall back to the relay and keep probing.

const discoMagic = "DNBDSC"

type sockOpts struct {
	port      int
	bindIf    string        // bind underlay sockets to this interface (bypasses a proxy's TUN)
	heartbeat time.Duration // how often to ping the current direct path
	trust     time.Duration // how long a direct path stays trusted without a pong
	overlay   netip.Prefix  // our own address block: never offer it as an underlay endpoint
}

type rxPacket struct {
	data []byte
	ep   conn.Endpoint
}

type magicSock struct {
	priv, pub [32]byte
	opts      sockOpts
	relay     RelayConf
	events    *eventLog

	udp  *net.UDPConn
	port uint16
	derp *derphttp.Client

	mu       sync.Mutex
	peers    map[[32]byte]*peerState
	byAddr   map[netip.AddrPort]*peerState
	stunTx   map[stun.TxID]time.Time
	stunAddr netip.AddrPort
	pongSelf netip.AddrPort // our address as reported back in pongs
	derpUp   bool

	rx chan rxPacket
}

type peerState struct {
	key    [32]byte
	label  string
	shared [32]byte // precomputed NaCl box key for disco messages
	ep     *peerEndpoint
	active bool // false when every LAN shared with it is paused

	cands    map[netip.AddrPort]time.Time
	best     netip.AddrPort
	bestRTT  time.Duration
	lastPong time.Time
	pending  map[string]pendingPing
	blocked  bool   // test knob: behave as if UDP between us were blocked
	path     string // what the last WireGuard packet used: "relay" or "direct"
	lastCMM  time.Time
	lastFull time.Time

	txDirect, txRelay, rxDirect, rxRelay uint64
}

type pendingPing struct {
	to netip.AddrPort
	at time.Time
}

type discoMsg struct {
	T   string   `json:"t"`             // ping | pong | cmm (call me maybe)
	Tx  string   `json:"tx,omitempty"`  // ping/pong transaction id
	Src string   `json:"src,omitempty"` // pong: where the ping came from, as seen by the responder
	Eps []string `json:"eps,omitempty"` // cmm: sender's candidate endpoints
}

func newMagicSock(priv [32]byte, relay RelayConf, opts sockOpts, events *eventLog) (*magicSock, error) {
	ms := &magicSock{
		priv:   priv,
		opts:   opts,
		relay:  relay,
		events: events,
		peers:  map[[32]byte]*peerState{},
		byAddr: map[netip.AddrPort]*peerState{},
		stunTx: map[stun.TxID]time.Time{},
		rx:     make(chan rxPacket, 1024),
	}
	pubKey := key.NodePrivateFromRaw32(mem.B(priv[:])).Public()
	ms.pub = pubKey.Raw32()

	lc := net.ListenConfig{}
	if opts.bindIf != "" {
		ctl, err := bindControl(opts.bindIf)
		if err != nil {
			return nil, err
		}
		lc.Control = ctl
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(opts.port))
	if err != nil {
		return nil, err
	}
	ms.udp = pc.(*net.UDPConn)
	ms.port = uint16(ms.udp.LocalAddr().(*net.UDPAddr).Port)

	// The DERP protocol addresses clients by public key and knows nothing about
	// LANs, so one connection with the device key serves all of them. An
	// unmodified Tailscale derper works as Deneb's relay.
	url := fmt.Sprintf("https://%s:%d/derp", relay.Host, relay.Port)
	dc, err := derphttp.NewClient(key.NodePrivateFromRaw32(mem.B(priv[:])), url, quietLogf, netmon.NewStatic())
	if err != nil {
		return nil, err
	}
	dc.SetURLDialer(ms.dialRelay)
	dc.NotePreferred(true)
	ms.derp = dc
	return ms, nil
}

// dialRelay ignores DNS on purpose: with a proxy in fake-ip mode the relay's
// name resolves to a fake address. The TLS name is still checked.
func (ms *magicSock) dialRelay(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	if ms.opts.bindIf != "" {
		ctl, err := bindControl(ms.opts.bindIf)
		if err != nil {
			return nil, err
		}
		d.Control = ctl
	}
	return d.DialContext(ctx, "tcp4", net.JoinHostPort(ms.relay.IP, strconv.Itoa(ms.relay.Port)))
}

func quietLogf(format string, args ...any) {
	// derphttp is chatty; keep only connection state changes.
	s := fmt.Sprintf(format, args...)
	if strings.Contains(s, "connected") || strings.Contains(s, "error") || strings.Contains(s, "closed") {
		log.Printf("[derp] %s", s)
	}
}

func (ms *magicSock) addPeer(k [32]byte, label string, hints []netip.AddrPort) *peerState {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if p := ms.peers[k]; p != nil {
		return p
	}
	p := &peerState{key: k, label: label, cands: map[netip.AddrPort]time.Time{}, pending: map[string]pendingPing{}, path: "relay"}
	peerPub, myPriv := k, ms.priv
	box.Precompute(&p.shared, &peerPub, &myPriv)
	p.ep = &peerEndpoint{peer: p}
	for _, h := range hints {
		p.cands[h] = time.Now()
		ms.byAddr[h] = p
	}
	ms.peers[k] = p
	return p
}

func (ms *magicSock) setActive(p *peerState, on bool) {
	ms.mu.Lock()
	p.active = on
	if !on {
		p.best, p.lastPong = netip.AddrPort{}, time.Time{}
	}
	ms.mu.Unlock()
	if on {
		go ms.probe(p)
	}
}

// probe tells the peer where we are (via the relay) and pings every address
// we already know for it, so a direct path can form within one round trip.
func (ms *magicSock) probe(p *peerState) {
	ms.sendCMM(p)
	ms.mu.Lock()
	var cands []netip.AddrPort
	for c := range p.cands {
		cands = append(cands, c)
	}
	ms.mu.Unlock()
	for _, c := range cands {
		ms.sendPing(p, c)
	}
}

func (ms *magicSock) start() {
	go ms.udpLoop()
	go ms.relayLoop()
	go ms.heartbeatLoop()
	go ms.stunLoop()
}

func (ms *magicSock) relayLoop() {
	for {
		m, err := ms.derp.Recv()
		if err != nil {
			ms.mu.Lock()
			was := ms.derpUp
			ms.derpUp = false
			ms.mu.Unlock()
			if was {
				ms.events.add("relay", "", "relay connection lost: "+err.Error())
			}
			time.Sleep(time.Second)
			continue
		}
		ms.mu.Lock()
		was := ms.derpUp
		ms.derpUp = true
		ms.mu.Unlock()
		if !was {
			ms.events.add("relay", "", "connected to relay "+ms.relay.Host+" ("+ms.relay.IP+")")
		}
		pkt, ok := m.(derp.ReceivedPacket)
		if !ok {
			continue
		}
		src := pkt.Source.Raw32()
		if isDisco(pkt.Data) {
			ms.handleDisco(append([]byte(nil), pkt.Data...), netip.AddrPort{}, true, src)
			continue
		}
		ms.mu.Lock()
		p := ms.peers[src]
		if p == nil || !p.active {
			ms.mu.Unlock()
			continue
		}
		p.rxRelay++
		ms.mu.Unlock()
		ms.rx <- rxPacket{data: append([]byte(nil), pkt.Data...), ep: p.ep}
	}
}

func (ms *magicSock) udpLoop() {
	buf := make([]byte, 65535)
	for {
		n, src, err := ms.udp.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		b := buf[:n]
		if stun.Is(b) {
			ms.handleSTUN(b)
			continue
		}
		if isDisco(b) {
			ms.handleDisco(append([]byte(nil), b...), src, false, [32]byte{})
			continue
		}
		ms.mu.Lock()
		p := ms.byAddr[src]
		if p != nil && (!p.active || p.blocked) {
			ms.mu.Unlock()
			continue
		}
		if p == nil {
			// Unknown source, e.g. a peer that restarted elsewhere and still
			// trusts an old path to us. WireGuard authenticates every packet by
			// itself, and roaming is disabled, so handing it over cannot hijack
			// a peer's endpoint; dropping it would only stall the handshake.
			ms.mu.Unlock()
			ms.rx <- rxPacket{data: append([]byte(nil), b...), ep: &addrEndpoint{ms: ms, addr: src}}
			continue
		}
		p.rxDirect++
		ms.mu.Unlock()
		ms.rx <- rxPacket{data: append([]byte(nil), b...), ep: p.ep}
	}
}

func isDisco(b []byte) bool {
	return len(b) > len(discoMagic) && string(b[:len(discoMagic)]) == discoMagic
}

func (ms *magicSock) sealDisco(p *peerState, m discoMsg) []byte {
	plain, _ := json.Marshal(m)
	var nonce [24]byte
	rand.Read(nonce[:])
	out := make([]byte, 0, len(discoMagic)+32+24+len(plain)+box.Overhead)
	out = append(out, discoMagic...)
	out = append(out, ms.pub[:]...)
	out = append(out, nonce[:]...)
	return box.SealAfterPrecomputation(out, plain, &nonce, &p.shared)
}

// sendDisco sends over UDP when `to` is set, otherwise over the relay.
func (ms *magicSock) sendDisco(p *peerState, to netip.AddrPort, m discoMsg) {
	pkt := ms.sealDisco(p, m)
	if to.IsValid() {
		ms.mu.Lock()
		blocked := p.blocked
		ms.mu.Unlock()
		if blocked {
			return
		}
		ms.udp.WriteToUDPAddrPort(pkt, to)
		return
	}
	ms.derp.Send(key.NodePublicFromRaw32(mem.B(p.key[:])), pkt)
}

func (ms *magicSock) sendPing(p *peerState, to netip.AddrPort) {
	var tx [12]byte
	rand.Read(tx[:])
	id := hex.EncodeToString(tx[:])
	ms.mu.Lock()
	p.pending[id] = pendingPing{to: to, at: time.Now()}
	ms.mu.Unlock()
	ms.sendDisco(p, to, discoMsg{T: "ping", Tx: id})
}

func (ms *magicSock) sendCMM(p *peerState) {
	eps := ms.endpoints()
	ms.mu.Lock()
	p.lastCMM = time.Now()
	ms.mu.Unlock()
	ms.sendDisco(p, netip.AddrPort{}, discoMsg{T: "cmm", Eps: eps})
}

func (ms *magicSock) handleDisco(b []byte, src netip.AddrPort, viaRelay bool, relaySrc [32]byte) {
	const hdr = len(discoMagic) + 32 + 24
	if len(b) < hdr+box.Overhead {
		return
	}
	var sender [32]byte
	copy(sender[:], b[len(discoMagic):len(discoMagic)+32])
	if viaRelay && sender != relaySrc {
		return // the relay vouches for the source key; refuse a mismatch
	}
	ms.mu.Lock()
	p := ms.peers[sender]
	if p == nil || !p.active || (!viaRelay && p.blocked) {
		// Devices that share no active LAN with us get no answer at all: they
		// cannot even learn which addresses this device can be reached on.
		ms.mu.Unlock()
		return
	}
	shared := p.shared
	ms.mu.Unlock()
	var nonce [24]byte
	copy(nonce[:], b[len(discoMagic)+32:hdr])
	plain, ok := box.OpenAfterPrecomputation(nil, b[hdr:], &nonce, &shared)
	if !ok {
		return
	}
	var m discoMsg
	if json.Unmarshal(plain, &m) != nil {
		return
	}
	switch m.T {
	case "ping":
		if viaRelay {
			return
		}
		ms.learn(p, src)
		ms.sendDisco(p, src, discoMsg{T: "pong", Tx: m.Tx, Src: src.String()})
		if !ms.directTrusted(p) {
			ms.sendPing(p, src) // they can reach us from src; punch back right away
		}
	case "pong":
		if viaRelay {
			return
		}
		ms.handlePong(p, m, src)
	case "cmm":
		for _, s := range m.Eps {
			if ap, err := netip.ParseAddrPort(s); err == nil {
				ms.learn(p, ap)
				ms.sendPing(p, ap)
			}
		}
	}
}

func (ms *magicSock) learn(p *peerState, ap netip.AddrPort) {
	ms.mu.Lock()
	p.cands[ap] = time.Now()
	ms.byAddr[ap] = p
	ms.mu.Unlock()
}

func (ms *magicSock) handlePong(p *peerState, m discoMsg, src netip.AddrPort) {
	now := time.Now()
	ms.mu.Lock()
	pp, ok := p.pending[m.Tx]
	if !ok || pp.to != src {
		ms.mu.Unlock()
		return
	}
	delete(p.pending, m.Tx)
	rtt := now.Sub(pp.at)
	p.cands[src] = now
	ms.byAddr[src] = p
	trusted := p.best.IsValid() && now.Sub(p.lastPong) < ms.opts.trust
	var ev string
	switch {
	case !trusted:
		if p.best != src {
			ev = fmt.Sprintf("direct path found: %s rtt=%s", src, rtt.Round(100*time.Microsecond))
		}
		p.best, p.bestRTT, p.lastPong = src, rtt, now
	case src == p.best:
		p.bestRTT, p.lastPong = rtt, now
	case rtt < p.bestRTT*8/10:
		// Clearly better path (e.g. both devices turn out to be on the same Wi-Fi).
		ev = fmt.Sprintf("better direct path: %s rtt=%s (was %s)", src, rtt.Round(100*time.Microsecond), p.best)
		p.best, p.bestRTT, p.lastPong = src, rtt, now
	}
	if self, err := netip.ParseAddrPort(m.Src); err == nil {
		ms.pongSelf = self
	}
	label := p.label
	ms.mu.Unlock()
	if ev != "" {
		ms.events.add("path", label, ev)
	}
	ms.updatePath(p)
}

func (ms *magicSock) directTrusted(p *peerState) bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.directTrustedLocked(p)
}

// directTrustedLocked deliberately ignores the test-only `blocked` flag: when
// a network starts dropping UDP nobody is told, so the switch to the relay
// must come from missing pongs, exactly as it would in the field.
func (ms *magicSock) directTrustedLocked(p *peerState) bool {
	return p.best.IsValid() && time.Since(p.lastPong) < ms.opts.trust
}

// updatePath records a path change the moment it happens, so measurements can
// line packet loss up with the switch.
func (ms *magicSock) updatePath(p *peerState) {
	ms.mu.Lock()
	now := "relay"
	if ms.directTrustedLocked(p) {
		now = "direct"
	}
	old := p.path
	p.path = now
	label, best := p.label, p.best
	ms.mu.Unlock()
	if old != now {
		detail := "now via relay " + ms.relay.Host
		if now == "direct" {
			detail = "now direct via " + best.String()
		}
		ms.events.add("switch", label, old+" -> "+now+": "+detail)
	}
}

// sendWG is the WireGuard send path (conn.Bind.Send).
func (ms *magicSock) sendWG(bufs [][]byte, ep *peerEndpoint) error {
	p := ep.peer
	ms.updatePath(p)
	ms.mu.Lock()
	direct := ms.directTrustedLocked(p)
	to := p.best
	blackhole := direct && p.blocked // simulated UDP block: the packet is lost on the wire
	if direct {
		p.txDirect += uint64(len(bufs))
	} else {
		p.txRelay += uint64(len(bufs))
	}
	ms.mu.Unlock()
	var err error
	if blackhole {
		return nil
	}
	for _, b := range bufs {
		if direct {
			_, err = ms.udp.WriteToUDPAddrPort(b, to)
		} else {
			err = ms.derp.Send(key.NodePublicFromRaw32(mem.B(p.key[:])), b)
		}
	}
	return err
}

func (ms *magicSock) heartbeatLoop() {
	t := time.NewTicker(ms.opts.heartbeat)
	for range t.C {
		ms.mu.Lock()
		var ps []*peerState
		for _, p := range ms.peers {
			if p.active {
				ps = append(ps, p)
			}
			for id, pp := range p.pending {
				if time.Since(pp.at) > 5*time.Second {
					delete(p.pending, id)
				}
			}
		}
		ms.mu.Unlock()
		for _, p := range ps {
			ms.updatePath(p)
			ms.mu.Lock()
			trusted := ms.directTrustedLocked(p)
			best := p.best
			full := !trusted || time.Since(p.lastFull) > 30*time.Second
			var cands []netip.AddrPort
			if full {
				p.lastFull = time.Now()
				for c, seen := range p.cands {
					if time.Since(seen) < 2*time.Minute || c == best {
						cands = append(cands, c)
					}
				}
			}
			needCMM := !trusted && time.Since(p.lastCMM) > 5*time.Second
			ms.mu.Unlock()
			if trusted {
				ms.sendPing(p, best) // keeps NAT mappings open and the path trusted
			}
			for _, c := range cands {
				if c != best || !trusted {
					ms.sendPing(p, c)
				}
			}
			if needCMM {
				ms.sendCMM(p)
			}
		}
	}
}

func (ms *magicSock) stunLoop() {
	stunAddr := netip.AddrPortFrom(netip.MustParseAddr(ms.relay.IP), uint16(ms.relay.STUNPort))
	for {
		tx := stun.NewTxID()
		ms.mu.Lock()
		ms.stunTx[tx] = time.Now()
		ms.mu.Unlock()
		ms.udp.WriteToUDPAddrPort(stun.Request(tx), stunAddr)
		time.Sleep(20 * time.Second)
	}
}

func (ms *magicSock) handleSTUN(b []byte) {
	tx, addr, err := stun.ParseResponse(b)
	if err != nil {
		return
	}
	ms.mu.Lock()
	if _, ok := ms.stunTx[tx]; !ok {
		ms.mu.Unlock()
		return
	}
	delete(ms.stunTx, tx)
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	changed := addr != ms.stunAddr
	ms.stunAddr = addr
	var ps []*peerState
	for _, p := range ms.peers {
		if p.active {
			ps = append(ps, p)
		}
	}
	ms.mu.Unlock()
	if changed {
		ms.events.add("stun", "", "public address as seen by the relay's STUN: "+addr.String())
		for _, p := range ps {
			ms.sendCMM(p)
		}
	}
}

// endpoints lists where peers might reach this socket. Addresses that belong
// to other tunnels (Tailscale's 100.64/10, a proxy's 198.18/15) are skipped:
// offering them would only make peers ping into someone else's VPN.
func (ms *magicSock) endpoints() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ap netip.AddrPort) {
		if ap.IsValid() && !seen[ap.String()] {
			seen[ap.String()] = true
			out = append(out, ap.String())
		}
	}
	ms.mu.Lock()
	add(ms.stunAddr)
	add(ms.pongSelf)
	ms.mu.Unlock()
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	bench := netip.MustParsePrefix("198.18.0.0/15")
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := pfx.Addr()
		if !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) || bench.Contains(ip) || ms.opts.overlay.Contains(ip) {
			continue
		}
		add(netip.AddrPortFrom(ip, ms.port))
	}
	return out
}

type peerStatus struct {
	Label     string   `json:"label"`
	Active    bool     `json:"active"`
	Path      string   `json:"path"`
	Best      string   `json:"best,omitempty"`
	DirectRTT string   `json:"directRTT,omitempty"`
	PongAgo   string   `json:"lastPongAgo,omitempty"`
	Blocked   bool     `json:"udpBlockedForTest"`
	Cands     []string `json:"candidates"`
	TxDirect  uint64   `json:"txDirect"`
	TxRelay   uint64   `json:"txRelay"`
	RxDirect  uint64   `json:"rxDirect"`
	RxRelay   uint64   `json:"rxRelay"`
}

func (ms *magicSock) status(p *peerState) peerStatus {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	s := peerStatus{Label: p.label, Active: p.active, Path: "relay", Blocked: p.blocked,
		TxDirect: p.txDirect, TxRelay: p.txRelay, RxDirect: p.rxDirect, RxRelay: p.rxRelay}
	if ms.directTrustedLocked(p) {
		s.Path = "direct"
	}
	if p.best.IsValid() {
		s.Best = p.best.String()
		s.DirectRTT = p.bestRTT.Round(100 * time.Microsecond).String()
		s.PongAgo = time.Since(p.lastPong).Round(time.Millisecond).String()
	}
	for c := range p.cands {
		s.Cands = append(s.Cands, c.String())
	}
	return s
}

func (ms *magicSock) pathOf(p *peerState) string {
	if ms.directTrusted(p) {
		return "direct"
	}
	return "relay"
}

func (ms *magicSock) setBlocked(p *peerState, on bool) {
	ms.mu.Lock()
	p.blocked = on
	ms.mu.Unlock()
	if on {
		ms.events.add("test", p.label, "UDP to/from this peer now dropped (simulating a network that blocks UDP)")
	} else {
		ms.events.add("test", p.label, "UDP block lifted")
	}
	ms.updatePath(p)
}

// wgBind adapts magicSock to wireguard-go's conn.Bind. The sockets live in
// magicSock for the whole process; Open/Close only gate the receive function,
// because wireguard-go re-opens the bind on some configuration changes.
type wgBind struct {
	ms     *magicSock
	mu     sync.Mutex
	closed chan struct{}
}

func (b *wgBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	closed := make(chan struct{})
	b.closed = closed
	b.mu.Unlock()
	recv := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		var rx rxPacket
		select {
		case <-closed:
			return 0, net.ErrClosed
		case rx = <-b.ms.rx:
		}
		sizes[0] = copy(packets[0], rx.data)
		eps[0] = rx.ep
		n := 1
		for n < len(packets) {
			select {
			case rx = <-b.ms.rx:
				sizes[n] = copy(packets[n], rx.data)
				eps[n] = rx.ep
				n++
			default:
				return n, nil
			}
		}
		return n, nil
	}
	return []conn.ReceiveFunc{recv}, b.ms.port, nil
}

func (b *wgBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != nil {
		select {
		case <-b.closed:
		default:
			close(b.closed)
		}
	}
	return nil
}

func (b *wgBind) SetMark(uint32) error { return nil }
func (b *wgBind) BatchSize() int       { return 8 }

func (b *wgBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	switch e := ep.(type) {
	case *peerEndpoint:
		return b.ms.sendWG(bufs, e)
	case *addrEndpoint:
		// Only cookie replies under load are addressed to a packet's source.
		for _, buf := range bufs {
			b.ms.udp.WriteToUDPAddrPort(buf, e.addr)
		}
		return nil
	}
	return errors.New("unexpected endpoint type")
}

// ParseEndpoint receives the peer's public key (hex) that we put in the UAPI
// "endpoint=" line: the endpoint is the peer, and magicSock picks the path.
func (b *wgBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	k, err := parseHexKey(s)
	if err != nil {
		return nil, err
	}
	b.ms.mu.Lock()
	p := b.ms.peers[k]
	b.ms.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown peer %s", s)
	}
	return p.ep, nil
}

type peerEndpoint struct{ peer *peerState }

// addrEndpoint marks a packet from a UDP address not yet tied to a peer.
type addrEndpoint struct {
	ms   *magicSock
	addr netip.AddrPort
}

func (e *addrEndpoint) ClearSrc()           {}
func (e *addrEndpoint) SrcToString() string { return "" }
func (e *addrEndpoint) DstToString() string { return e.addr.String() }
func (e *addrEndpoint) DstToBytes() []byte  { b, _ := e.addr.MarshalBinary(); return b }
func (e *addrEndpoint) DstIP() netip.Addr   { return e.addr.Addr() }
func (e *addrEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }

func (e *peerEndpoint) ClearSrc()           {}
func (e *peerEndpoint) SrcToString() string { return "" }
func (e *peerEndpoint) DstToString() string { return hex.EncodeToString(e.peer.key[:]) }
func (e *peerEndpoint) DstToBytes() []byte  { return e.peer.key[:] }
func (e *peerEndpoint) DstIP() netip.Addr   { return netip.Addr{} }
func (e *peerEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }
