// Package magicsock carries WireGuard packets for every scope the device is
// in over one UDP socket and one connection per relay, and finds direct
// paths between devices (multi-lan-tech §3.6). It grew out of the
// prototype's smaller cousin of Tailscale's magicsock (poc/multi-lan); the
// reasons for not using Tailscale's own are in design/plan/m1/engine.md.
//
// Path selection mirrors Tailscale: send via the peer's home relay until a
// direct UDP path answers a ping, then switch; when the direct path stops
// answering for Trust, fall back to the relay and keep probing. Adding a
// scope adds peers, never sockets, keys or relay connections.
package magicsock

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go4.org/mem"
	"golang.org/x/crypto/nacl/box"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/net/stun"
	"tailscale.com/types/key"

	"github.com/kittors/deneb/engine/bind"
	"github.com/kittors/deneb/proto"
)

// Options configure a Conn.
type Options struct {
	Port uint16 // UDP port; 0 picks one
	// BindInterface pins the UDP socket and relay connections to one
	// interface; "" leaves them to the routing table.
	BindInterface string
	Heartbeat     time.Duration // how often to ping the direct path (M1: 1 s)
	Trust         time.Duration // how long a direct path stays trusted without a pong (M1: 2.5 s)
	// Idle is how long a peer may go without WireGuard traffic before its
	// heartbeat stops (M2: no traffic, no pings). Traffic wakes it at once.
	Idle time.Duration
	// RelayIdle is how long a connection to a relay other than the home
	// one stays open unused (default 60 s).
	RelayIdle time.Duration
	// PathByLoss lets loss and delay move a working direct path to the
	// relay (M2's "bad line"). Off by default until it has proven itself on
	// real networks.
	PathByLoss bool
	// Overlay are this deployment's own blocks: never offered as an
	// endpoint, since a peer cannot reach us through our own tunnel.
	Overlay []netip.Prefix
	// StaticEndpoints are offered besides the discovered ones, for a server
	// whose public address STUN cannot see.
	StaticEndpoints []netip.AddrPort
	// RelayRoots, if set, are trusted for relays' TLS besides the system's:
	// the colima topology's relay has a self-signed certificate.
	RelayRoots *x509.CertPool
	Logf       func(format string, args ...any)
	// OnEndpoints is called when the endpoints or the home relay change.
	OnEndpoints func()
	// OnPath is called when a peer's path kind, relay or endpoint changes.
	OnPath func(proto.DeviceID)
	// OnPeerRestart is called when a peer's engine has restarted: its
	// WireGuard sessions are gone, so ours with it are stale.
	OnPeerRestart func(proto.NodePublic)
	// OnTimeJump is called when the wall clock moved much further than the
	// heartbeat between two ticks: the machine slept (or the process was
	// frozen) for about that long.
	OnTimeJump func(time.Duration)
}

// PeerConfig is one peer as the network map describes it. Only peers that
// share an active scope are handed to SetPeers.
type PeerConfig struct {
	ID        proto.DeviceID
	Label     string // "nas.jie", for logs
	NodeKey   proto.NodePublic
	DiscoKey  proto.DiscoPublic
	Endpoints []netip.AddrPort // as the peer last reported them
	HomeRelay proto.RelayID
}

// Conn is safe for concurrent use.
type Conn struct {
	opts      Options
	nodePriv  key.NodePrivate
	discoPriv [32]byte
	discoPub  [32]byte
	instance  string // random per start: lets peers notice that this engine restarted
	logf      func(format string, args ...any)

	udp      atomic.Pointer[net.UDPConn] // replaced by Rebind
	rebindMu sync.Mutex
	port     uint16
	boundTo  string // interface the UDP socket is pinned to, if any (under mu after New)
	bindErr  error  // why pinning failed, if it did
	closed   chan struct{}
	closeOne sync.Once

	mu       sync.Mutex
	relayCfg map[proto.RelayID]proto.Relay // every relay the network map names
	relays   map[proto.RelayID]*relayConn  // open connections: home, and others while in use
	home     proto.RelayID
	peers    map[proto.NodePublic]*peerState
	byDisco  map[proto.DiscoPublic]*peerState
	byAddr   map[netip.AddrPort]*peerState
	stunTx   map[stun.TxID]proto.RelayID
	stunAddr map[proto.RelayID]netip.AddrPort
	pongSelf netip.AddrPort
	lastEps  []netip.AddrPort

	rx chan rxPacket
}

type rxPacket struct {
	data []byte
	ep   wgEndpoint
}

// relayConn is one DERP connection. DERP addresses clients by node key and
// knows nothing about scopes, so one connection serves all of them, and an
// unmodified Tailscale derper works as Deneb's relay.
type relayConn struct {
	relay    proto.Relay
	client   *derphttp.Client
	up       bool
	since    time.Time
	stop     chan struct{}
	onDemand bool      // not the home relay: closed when unused
	lastUsed time.Time // last packet sent or received through it
}

// New opens the UDP socket. Call SetRelays and SetPeers, then Start.
func New(nodePriv key.NodePrivate, discoPriv [32]byte, opts Options) (*Conn, error) {
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = time.Second
	}
	if opts.Trust <= 0 {
		opts.Trust = 2500 * time.Millisecond
	}
	if opts.Idle <= 0 {
		opts.Idle = 45 * time.Second
	}
	if opts.RelayIdle <= 0 {
		opts.RelayIdle = time.Minute
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	c := &Conn{
		opts:      opts,
		nodePriv:  nodePriv,
		discoPriv: discoPriv,
		logf:      logf,
		closed:    make(chan struct{}),
		relays:    map[proto.RelayID]*relayConn{},
		relayCfg:  map[proto.RelayID]proto.Relay{},
		peers:     map[proto.NodePublic]*peerState{},
		byDisco:   map[proto.DiscoPublic]*peerState{},
		byAddr:    map[netip.AddrPort]*peerState{},
		stunTx:    map[stun.TxID]proto.RelayID{},
		stunAddr:  map[proto.RelayID]netip.AddrPort{},
		rx:        make(chan rxPacket, 1024),
	}
	c.discoPub = DiscoPublic(discoPriv)
	c.instance = newTx()[:16]

	pc, err := c.listen(opts.Port, opts.BindInterface)
	if err != nil && opts.Port != 0 {
		// The usual port is taken (another engine, or something else):
		// any port works, STUN and the endpoint report tell peers where.
		logf("[magicsock] udp port %d: %v; picking another", opts.Port, err)
		pc, err = c.listen(0, opts.BindInterface)
	}
	if err != nil {
		return nil, err
	}
	c.udp.Store(pc)
	c.port = uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	return c, nil
}

func (c *Conn) listen(port uint16, ifname string) (*net.UDPConn, error) {
	pc, bound, bindErr, err := c.listenOn(port, ifname)
	if err == nil {
		c.boundTo, c.bindErr = bound, bindErr
	}
	return pc, err
}

// listenOn opens a UDP socket pinned to ifname if it can; bound is the
// interface it got pinned to ("" for none) and bindErr why not.
func (c *Conn) listenOn(port uint16, ifname string) (pc *net.UDPConn, bound string, bindErr, err error) {
	lc := net.ListenConfig{}
	if ifname != "" {
		ctl, cerr := bind.Control(ifname)
		if cerr == nil {
			lc.Control = ctl
			p, lerr := lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(int(port)))
			if lerr == nil {
				return p.(*net.UDPConn), ifname, nil, nil
			}
			if !isBindError(lerr) {
				return nil, "", nil, lerr
			}
			bindErr = lerr
		} else {
			bindErr = cerr
		}
		// Pinning failed (no permission on an old Linux kernel, say):
		// unpinned still works, it only risks a proxy in the way.
		c.logf("[magicsock] cannot pin the UDP socket to %s: %v; using the routing table", ifname, bindErr)
		lc.Control = nil
	}
	p, lerr := lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(int(port)))
	if lerr != nil {
		return nil, "", bindErr, lerr
	}
	return p.(*net.UDPConn), "", bindErr, nil
}

// Rebind moves the UDP socket to another interface, on the same port: the
// default route went from Wi-Fi to Ethernet, say, and a socket pinned to
// the old interface would go on sending into it.
func (c *Conn) Rebind(ifname string) error {
	c.rebindMu.Lock()
	defer c.rebindMu.Unlock()
	old := c.sock()
	old.Close() // frees the port for the new socket
	pc, bound, bindErr, err := c.listenOn(c.port, ifname)
	if err != nil {
		c.logf("[magicsock] udp port %d on %s: %v; picking another", c.port, ifname, err)
		pc, bound, bindErr, err = c.listenOn(0, ifname)
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.boundTo, c.bindErr = bound, bindErr
	c.mu.Unlock()
	c.udp.Store(pc)
	c.port = uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	go c.udpLoop(pc)
	c.logf("[magicsock] UDP port %d now on %q", c.port, bound)
	return nil
}

// ResetPeer forgets what is known about the path to k, as after a pause:
// the peer may have dropped us meanwhile, so nothing counts as confirmed
// until it answers again, and a handshake sent now is kept and sent again
// the moment it does (pendingInit).
func (c *Conn) ResetPeer(k proto.NodePublic) {
	now := time.Now()
	c.mu.Lock()
	p := c.peers[k]
	if p == nil || !p.active {
		c.mu.Unlock()
		return
	}
	p.lastPong, p.lastRelayPong = time.Time{}, time.Time{}
	p.activatedAt, p.lastActive, p.idle, p.wokeAt = now, now, false, time.Time{}
	p.degraded, p.reason = false, ""
	c.mu.Unlock()
	c.updatePath(p)
	go c.probe(p)
}

// ResetPaths starts over after the network changed or the machine woke:
// addresses and NAT mappings may all be gone. Every peer shows
// "connecting" (not an error) until it answers again; the relays are
// redialed, STUN asked again, and every peer probed at once.
func (c *Conn) ResetPaths(reason string) {
	now := time.Now()
	c.mu.Lock()
	var peers []*peerState
	for _, p := range c.peers {
		if !p.active {
			continue
		}
		p.lastPong, p.lastRelayPong = time.Time{}, time.Time{}
		p.activatedAt, p.lastActive, p.idle, p.wokeAt = now, now, false, time.Time{}
		p.degraded, p.reason, p.srtt = false, "", 0
		p.probes, p.pending = nil, map[string]pendingPing{}
		peers = append(peers, p)
	}
	var start []*relayConn
	for id, rc := range c.relays {
		c.closeRelayLocked(id, rc)
	}
	if r, ok := c.relayCfg[c.home]; ok {
		if rc, err := c.newRelayConn(r, true); err == nil {
			c.relays[c.home] = rc
			start = append(start, rc)
		}
	}
	c.pongSelf = netip.AddrPort{}
	clear(c.stunAddr)
	c.mu.Unlock()
	c.logf("[net] %s: redialing the relay and probing %d peers", reason, len(peers))
	for _, rc := range start {
		go c.relayLoop(rc)
	}
	for _, p := range peers {
		c.updatePath(p)
	}
	go c.stunOnce()
	c.noteEndpoints()
	for _, p := range peers {
		go c.probe(p)
	}
}

func isBindError(err error) bool {
	return strings.Contains(err.Error(), "bind to ")
}

// DiscoPublic derives the public half of a disco key.
func DiscoPublic(priv [32]byte) [32]byte {
	return key.NodePrivateFromRaw32(mem.B(priv[:])).Public().Raw32()
}

// Port is the UDP port in use.
func (c *Conn) Port() uint16 { return c.port }

// BoundInterface is the interface the UDP socket is pinned to ("" if none),
// and why pinning failed if it was asked for and did not work.
func (c *Conn) BoundInterface() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundTo, c.bindErr
}

// Start runs the receive, heartbeat and STUN loops.
func (c *Conn) Start() {
	go c.udpLoop(c.sock())
	go c.heartbeatLoop()
	go c.stunLoop()
}

// Close stops everything. Safe to call more than once.
func (c *Conn) Close() error {
	c.closeOne.Do(func() {
		close(c.closed)
		c.sock().Close()
		c.mu.Lock()
		for _, r := range c.relays {
			close(r.stop)
			r.client.Close()
		}
		c.relays = map[proto.RelayID]*relayConn{}
		c.mu.Unlock()
	})
	return nil
}

// SetRelays replaces the relay table. home is where peers reach this device:
// its connection is held open. A connection to another relay is opened when
// a peer homed there is sent to, and closed when it has been unused for
// RelayIdle, so a device need not hold one to every relay (M2, for M4's many
// relays). Connections to relays that stay are kept; gone ones are closed.
func (c *Conn) SetRelays(relays []proto.Relay, home proto.RelayID) {
	want := map[proto.RelayID]proto.Relay{}
	for _, r := range relays {
		want[r.ID] = r
	}
	now := time.Now()
	c.mu.Lock()
	changedHome := c.home != home
	c.home = home
	c.relayCfg = want
	var start []*relayConn
	for id, rc := range c.relays {
		r, ok := want[id]
		if !ok || !sameRelay(r, rc.relay) {
			c.closeRelayLocked(id, rc)
		}
	}
	if r, ok := want[home]; ok {
		if _, open := c.relays[home]; !open {
			rc, err := c.newRelayConn(r, true)
			if err != nil {
				c.logf("[relay] %s: %v", r.Name, err)
			} else {
				c.relays[home] = rc
				start = append(start, rc)
			}
		}
	}
	for id, rc := range c.relays {
		rc.client.NotePreferred(id == home)
		rc.onDemand = id != home
		if rc.lastUsed.IsZero() {
			rc.lastUsed = now
		}
	}
	c.mu.Unlock()
	for _, rc := range start {
		go c.relayLoop(rc)
	}
	if changedHome || len(start) > 0 {
		go c.stunOnce()
		c.noteEndpoints()
	}
}

func (c *Conn) closeRelayLocked(id proto.RelayID, rc *relayConn) {
	close(rc.stop)
	rc.client.Close()
	delete(c.relays, id)
	delete(c.stunAddr, id)
}

// relayLocked returns the open connection to relay id, opening it if the
// network map knows the relay; started says the caller must start its
// receive loop.
func (c *Conn) relayLocked(id proto.RelayID, now time.Time) (rc *relayConn, started bool) {
	if rc = c.relays[id]; rc != nil {
		rc.lastUsed = now
		return rc, false
	}
	r, ok := c.relayCfg[id]
	if !ok {
		return nil, false
	}
	rc, err := c.newRelayConn(r, false)
	if err != nil {
		c.logf("[relay] %s: %v", r.Name, err)
		return nil, false
	}
	rc.onDemand, rc.lastUsed = true, now
	c.relays[id] = rc
	return rc, true
}

// closeIdleRelaysLocked closes connections to relays other than home that
// carried nothing for RelayIdle.
func (c *Conn) closeIdleRelaysLocked(now time.Time) []string {
	var closed []string
	for id, rc := range c.relays {
		if rc.onDemand && now.Sub(rc.lastUsed) > c.opts.RelayIdle {
			c.closeRelayLocked(id, rc)
			closed = append(closed, rc.relay.Name)
		}
	}
	return closed
}

func sameRelay(a, b proto.Relay) bool {
	return a.HostName == b.HostName && a.IPv4 == b.IPv4 && a.DERPPort == b.DERPPort && a.STUNPort == b.STUNPort
}

func (c *Conn) newRelayConn(r proto.Relay, home bool) (*relayConn, error) {
	if r.HostName == "" || r.DERPPort == 0 {
		return nil, errors.New("relay has no host name or port")
	}
	url := "https://" + net.JoinHostPort(r.HostName, strconv.Itoa(int(r.DERPPort))) + "/derp"
	dc, err := derphttp.NewClient(c.nodePriv, url, c.derpLogf(r.Name), netmon.NewStatic())
	if err != nil {
		return nil, err
	}
	if c.opts.RelayRoots != nil {
		dc.TLSConfig = &tls.Config{RootCAs: c.opts.RelayRoots}
	}
	dc.SetURLDialer(c.relayDialer(r))
	dc.NotePreferred(home)
	return &relayConn{relay: r, client: dc, stop: make(chan struct{})}, nil
}

// relayDialer dials the relay's address as the network map gives it,
// ignoring DNS on purpose: with a proxy in fake-ip mode the relay's name
// resolves to a fake address (multi-lan-tech §6.5). TLS still checks the
// name. The connection is pinned like the UDP socket.
func (c *Conn) relayDialer(r proto.Relay) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		target := addr
		if r.IPv4.IsValid() {
			target = net.JoinHostPort(r.IPv4.String(), strconv.Itoa(int(r.DERPPort)))
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		c.mu.Lock()
		boundTo := c.boundTo
		c.mu.Unlock()
		if boundTo != "" {
			if ctl, err := bind.Control(boundTo); err == nil {
				d.Control = ctl
				conn, err := d.DialContext(ctx, "tcp4", target)
				if err == nil {
					return conn, nil
				}
				// §3.7 item 2: if the pinned dial fails, go through the
				// routing table, so at least the relay works.
				c.logf("[relay] %s: pinned dial failed (%v); trying unpinned", r.Name, err)
				d.Control = nil
			}
		}
		return d.DialContext(ctx, "tcp4", target)
	}
}

func (c *Conn) derpLogf(name string) func(format string, args ...any) {
	return func(format string, args ...any) {
		// derphttp is chatty; keep connection changes and errors.
		s := fmt.Sprintf(format, args...)
		if strings.Contains(s, "error") || strings.Contains(s, "closed") {
			c.logf("[relay %s] %s", name, s)
		}
	}
}

func (c *Conn) relayLoop(rc *relayConn) {
	for {
		select {
		case <-rc.stop:
			return
		default:
		}
		m, err := rc.client.Recv()
		if err != nil {
			select {
			case <-rc.stop: // closed on purpose: not a lost relay
				return
			default:
			}
			c.mu.Lock()
			was := rc.up
			rc.up = false
			c.mu.Unlock()
			if was {
				c.logf("[relay] lost %s: %v", rc.relay.Name, err)
				c.relayChanged(rc.relay.ID)
			}
			select {
			case <-rc.stop:
				return
			case <-time.After(time.Second):
			}
			continue
		}
		c.mu.Lock()
		was := rc.up
		if !was {
			rc.up, rc.since = true, time.Now()
		}
		if _, ok := m.(derp.ReceivedPacket); ok {
			rc.lastUsed = time.Now()
		}
		c.mu.Unlock()
		if !was {
			c.logf("[relay] connected to %s (%s)", rc.relay.Name, rc.relay.HostName)
			c.relayChanged(rc.relay.ID)
		}
		switch m := m.(type) {
		case derp.ReceivedPacket:
			c.handleRelayPacket(rc.relay.ID, m)
		case derp.PeerGoneMessage:
			c.handlePeerGone(rc.relay.ID, m)
		}
	}
}

// relayChanged re-probes every peer homed on a relay that came or went, so
// their paths settle at once instead of at the next heartbeat.
func (c *Conn) relayChanged(id proto.RelayID) {
	c.mu.Lock()
	var ps []*peerState
	for _, p := range c.peers {
		if p.active && !p.idle && c.relayForLocked(p) == id {
			ps = append(ps, p)
		}
	}
	c.mu.Unlock()
	for _, p := range ps {
		c.sendRelayPing(p)
		c.updatePath(p)
	}
}

func (c *Conn) handleRelayPacket(relay proto.RelayID, m derp.ReceivedPacket) {
	src := proto.NodePublic(m.Source.Raw32())
	if isDisco(m.Data) {
		c.handleDisco(append([]byte(nil), m.Data...), netip.AddrPort{}, relay, src)
		return
	}
	c.mu.Lock()
	p := c.peers[src]
	if p == nil || !p.active {
		c.mu.Unlock()
		return
	}
	p.rxRelay++
	noteHandshakeResponseLocked(p, m.Data)
	woke := !isKeepalive(m.Data) && c.noteTrafficLocked(p, time.Now())
	c.mu.Unlock()
	if woke {
		go c.wake(p)
	}
	c.deliver(rxPacket{data: append([]byte(nil), m.Data...), ep: p.ep})
}

// handlePeerGone: the relay says the peer is not connected to it. Mark the
// relay path dead now instead of waiting for pings to time out.
func (c *Conn) handlePeerGone(relay proto.RelayID, m derp.PeerGoneMessage) {
	k := proto.NodePublic(m.Peer.Raw32())
	c.mu.Lock()
	p := c.peers[k]
	if p != nil && c.relayForLocked(p) == relay {
		p.lastRelayPong = time.Time{}
	}
	c.mu.Unlock()
	if p != nil {
		c.updatePath(p)
	}
}

// deliver hands a packet to WireGuard. When WireGuard falls behind, packets
// are dropped rather than stalling disco and STUN handling; TCP recovers.
func (c *Conn) deliver(p rxPacket) {
	select {
	case c.rx <- p:
	default:
	}
}

// sock is the current UDP socket.
func (c *Conn) sock() *net.UDPConn { return c.udp.Load() }

func (c *Conn) udpLoop(pc *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, src, err := pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || c.sock() != pc {
				return // closed, or replaced by Rebind
			}
			select {
			case <-c.closed:
				return
			default:
			}
			continue
		}
		src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		b := buf[:n]
		if stun.Is(b) {
			c.handleSTUN(b)
			continue
		}
		if isDisco(b) {
			c.handleDisco(append([]byte(nil), b...), src, "", proto.NodePublic{})
			continue
		}
		c.mu.Lock()
		p := c.byAddr[src]
		if p != nil && (!p.active || p.blocked || p.lossRx > 0 && mrand.Float64() < p.lossRx) {
			c.mu.Unlock()
			continue
		}
		if p == nil {
			// Unknown source, such as a peer that restarted elsewhere and
			// still trusts an old path to us. WireGuard authenticates every
			// packet itself and roaming is off, so handing it over cannot
			// hijack a peer's endpoint; dropping it would stall the
			// handshake for 5 seconds (multi-lan-tech §6.7, item 2).
			c.mu.Unlock()
			c.deliver(rxPacket{data: append([]byte(nil), b...), ep: &addrEndpoint{addr: src}})
			continue
		}
		p.rxDirect++
		noteHandshakeResponseLocked(p, b)
		woke := !isKeepalive(b) && c.noteTrafficLocked(p, time.Now())
		c.mu.Unlock()
		if woke {
			go c.wake(p)
		}
		c.deliver(rxPacket{data: append([]byte(nil), b...), ep: p.ep})
	}
}

// relayForLocked is the relay used to reach p: its home relay if the network
// map names it (the connection is opened on demand), else our own home.
func (c *Conn) relayForLocked(p *peerState) proto.RelayID {
	if _, ok := c.relayCfg[p.cfg.HomeRelay]; ok {
		return p.cfg.HomeRelay
	}
	return c.home
}

func (c *Conn) sendRelay(p *peerState, b []byte) error {
	c.mu.Lock()
	rc, started := c.relayLocked(c.relayForLocked(p), time.Now())
	c.mu.Unlock()
	if rc == nil {
		return errors.New("no relay to reach " + p.cfg.Label)
	}
	if started {
		c.logf("[relay] connecting to %s to reach %s", rc.relay.Name, p.cfg.Label)
		go c.relayLoop(rc)
	}
	return rc.client.Send(key.NodePublicFromRaw32(mem.B(p.cfg.NodeKey[:])), b)
}

// SetPeers replaces the peer table with the peers that share an active
// scope. A peer that stays keeps its path; a new one is probed at once (a
// relay ping and a call-me-maybe), so it answers via the relay within a
// round trip and goes direct shortly after (multi-lan-tech §6.7, item 3).
func (c *Conn) SetPeers(cfgs []PeerConfig) {
	want := map[proto.NodePublic]PeerConfig{}
	for _, pc := range cfgs {
		want[pc.NodeKey] = pc
	}
	var probe []*peerState
	var changed []*peerState
	c.mu.Lock()
	for k, p := range c.peers {
		pc, ok := want[k]
		if !ok || pc.DiscoKey != p.cfg.DiscoKey {
			if p.active {
				p.active = false
				p.best, p.lastPong = netip.AddrPort{}, time.Time{}
				changed = append(changed, p)
			}
			if ok { // the peer has new disco keys: start over
				c.forgetLocked(p)
			}
			continue
		}
	}
	for k, pc := range want {
		p := c.peers[k]
		if p == nil {
			p = c.newPeerLocked(pc)
		}
		old := p.cfg
		p.cfg = pc
		for _, h := range pc.Endpoints {
			if _, seen := p.cands[h]; !seen {
				p.cands[h] = time.Now()
			}
			c.byAddr[h] = p
		}
		if !p.active {
			p.active = true
			p.activatedAt = time.Now()
			p.lastRelayPong = time.Time{}
			// A new peer gets a heartbeat for a while, so its path is known
			// before anyone uses it; then it idles like any other.
			p.lastActive, p.idle = p.activatedAt, false
			probe = append(probe, p)
		} else if old.HomeRelay != pc.HomeRelay {
			probe = append(probe, p)
		}
	}
	c.mu.Unlock()
	for _, p := range changed {
		c.updatePath(p)
	}
	for _, p := range probe {
		go c.probe(p)
	}
}

func (c *Conn) newPeerLocked(pc PeerConfig) *peerState {
	p := &peerState{
		cfg:     pc,
		cands:   map[netip.AddrPort]time.Time{},
		pending: map[string]pendingPing{},
		kind:    kindConnecting,
		since:   time.Now(),
	}
	peerPub, myPriv := [32]byte(pc.DiscoKey), c.discoPriv
	box.Precompute(&p.shared, &peerPub, &myPriv)
	p.ep = &peerEndpoint{key: pc.NodeKey}
	c.peers[pc.NodeKey] = p
	c.byDisco[pc.DiscoKey] = p
	return p
}

func (c *Conn) forgetLocked(p *peerState) {
	delete(c.peers, p.cfg.NodeKey)
	delete(c.byDisco, p.cfg.DiscoKey)
	for a, q := range c.byAddr {
		if q == p {
			delete(c.byAddr, a)
		}
	}
}

// probe tells the peer where we are (via its relay) and pings every address
// we already know for it, so a direct path can form within one round trip.
func (c *Conn) probe(p *peerState) {
	c.sendRelayPing(p)
	c.sendCMM(p)
	c.mu.Lock()
	cands := make([]netip.AddrPort, 0, len(p.cands))
	for a := range p.cands {
		cands = append(cands, a)
	}
	c.mu.Unlock()
	for _, a := range cands {
		c.sendPing(p, a)
	}
}

func (c *Conn) stunLoop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	c.stunOnce()
	// A lost first request would leave us without a public address for 20
	// seconds; ask again soon.
	select {
	case <-c.closed:
		return
	case <-time.After(1500 * time.Millisecond):
		c.stunOnce()
	}
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			c.stunOnce()
			c.noteEndpoints() // interfaces may have changed too
		}
	}
}

func (c *Conn) stunOnce() {
	c.mu.Lock()
	type target struct {
		id   proto.RelayID
		addr netip.AddrPort
	}
	var ts []target
	for id, rc := range c.relays {
		if rc.relay.STUNPort != 0 && rc.relay.IPv4.IsValid() {
			ts = append(ts, target{id, netip.AddrPortFrom(rc.relay.IPv4, rc.relay.STUNPort)})
		}
	}
	if len(c.stunTx) > 64 {
		// Answers that never came: forget them rather than grow forever.
		clear(c.stunTx)
	}
	reqs := make([][]byte, len(ts))
	for i, t := range ts {
		tx := stun.NewTxID()
		c.stunTx[tx] = t.id
		reqs[i] = stun.Request(tx)
	}
	c.mu.Unlock()
	for i, t := range ts {
		c.sock().WriteToUDPAddrPort(reqs[i], t.addr)
	}
}

func (c *Conn) handleSTUN(b []byte) {
	tx, addr, err := stun.ParseResponse(b)
	if err != nil {
		return
	}
	c.mu.Lock()
	id, ok := c.stunTx[tx]
	if !ok {
		c.mu.Unlock()
		return
	}
	delete(c.stunTx, tx)
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	changed := c.stunAddr[id] != addr
	c.stunAddr[id] = addr
	c.mu.Unlock()
	if changed {
		c.logf("[stun] public address as the relay sees it: %s", addr)
		c.noteEndpoints()
	}
}

// Endpoints lists where peers might reach this device: what STUN saw, what
// peers saw in their pongs, the local interfaces' addresses, and any static
// ones. Addresses of other tunnels (Tailscale's 100.64/10, a proxy's
// 198.18/15) and of container bridges are left out: offering them only makes
// peers ping into someone else's network.
func (c *Conn) Endpoints() []netip.AddrPort {
	var out []netip.AddrPort
	seen := map[netip.AddrPort]bool{}
	add := func(ap netip.AddrPort) {
		if ap.IsValid() && !seen[ap] {
			seen[ap] = true
			out = append(out, ap)
		}
	}
	c.mu.Lock()
	ids := make([]proto.RelayID, 0, len(c.stunAddr))
	for id := range c.stunAddr {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		add(c.stunAddr[id])
	}
	add(c.pongSelf)
	c.mu.Unlock()
	for _, ap := range c.opts.StaticEndpoints {
		add(ap)
	}
	for _, ip := range localAddrs(c.opts.Overlay) {
		add(netip.AddrPortFrom(ip, c.port))
	}
	return out
}

var (
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	bench = netip.MustParsePrefix("198.18.0.0/15")
)

func localAddrs(overlay []netip.Prefix) []netip.Addr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 || bind.IsTunnel(ifi.Name) || isBridge(ifi.Name) {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := pfx.Addr()
			if !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) || bench.Contains(ip) {
				continue
			}
			if slices.ContainsFunc(overlay, func(p netip.Prefix) bool { return p.Contains(ip) }) {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

// isBridge: Docker's and libvirt's bridges on a server running the engine
// with host networking.
func isBridge(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "vmnet", "bridge"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// noteEndpoints reports a change in endpoints or home relay.
func (c *Conn) noteEndpoints() {
	eps := c.Endpoints()
	c.mu.Lock()
	same := slices.Equal(eps, c.lastEps)
	c.lastEps = eps
	var ps []*peerState
	if !same {
		for _, p := range c.peers {
			if p.active {
				ps = append(ps, p)
			}
		}
	}
	c.mu.Unlock()
	if same {
		return
	}
	// Tell every peer at once, so a changed public address does not wait
	// for the next call-me-maybe. In the background: a send through a
	// relay still connecting can block, and callers may hold locks.
	go func() {
		for _, p := range ps {
			c.sendCMM(p)
		}
	}()
	if c.opts.OnEndpoints != nil {
		c.opts.OnEndpoints()
	}
}

// HomeRelay is the relay peers reach this device through.
func (c *Conn) HomeRelay() proto.RelayID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.home
}

// RelayState is one relay connection, for status.
type RelayState struct {
	ID       proto.RelayID
	Name     string
	Up       bool
	Home     bool
	OnDemand bool // opened to reach a peer homed there; closed when unused
	Since    time.Time
}

// Relays lists the relay connections.
func (c *Conn) Relays() []RelayState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RelayState, 0, len(c.relays))
	for id, rc := range c.relays {
		out = append(out, RelayState{ID: id, Name: rc.relay.Name, Up: rc.up, Home: id == c.home, OnDemand: rc.onDemand, Since: rc.since})
	}
	slices.SortFunc(out, func(a, b RelayState) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return out
}
