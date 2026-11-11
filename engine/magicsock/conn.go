// Package magicsock carries WireGuard packets for every scope the device is
// in over one UDP socket and one connection per relay, and finds direct
// paths between devices (multi-lan-tech §3.6). It grew out of the
// prototype's smaller cousin of Tailscale's magicsock (poc/multi-lan); the
// reasons for not using Tailscale's own are in design/plan/m1/engine.md.
//
// Path selection mirrors Tailscale: send via the peer's home relay until a
// direct UDP path answers a ping, then switch; when the direct path stops
// answering for Trust, send both direct and through the relay, and fall
// back to the relay alone if it is still silent at Hold. Adding a
// scope adds peers, never sockets, keys or relay connections.
package magicsock

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"hash/maphash"
	"log"
	"maps"
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
	// RelayOnly opens no UDP port on the network. The socket is on the
	// loopback address; there is no STUN, no endpoint is offered and no
	// direct path is tried, so peers are reached through relays only. It is
	// for the Windows window's own engine: Windows asks the person whether
	// to let in a program that opens a port, and a program in the person's
	// own folder has no firewall rule (the service's engine has one).
	RelayOnly bool
	// Hold is how long a direct path that stopped answering keeps its
	// place. From Trust to Hold after its last pong, packets go both direct
	// and through the relay, and the path shown stays direct; only after
	// Hold is it shown as the relay. Default: Trust plus four heartbeats.
	Hold time.Duration
	// Idle is how long a peer may go without WireGuard traffic before its
	// heartbeat stops (M2: no traffic, no pings). Traffic wakes it at once.
	Idle time.Duration
	// IdleReprobe is how soon an idle peer shown on the relay or as
	// unreachable is asked again, then twice as long each time up to
	// idleReprobeMax: a line that came back while nobody used it shows it.
	// Default 30 s.
	IdleReprobe time.Duration
	// OnLocalNetwork is called when the system starts refusing packets to
	// local-network addresses (macOS's local network privacy: a program
	// without the permission gets "no route to host" for 192.168.x.x and
	// the like), and when a device there answers again.
	OnLocalNetwork func(refused bool)
	// FlakyBackoff is how long a direct address that dropped soon after
	// being taken up, twice, is left alone first (default 2 minutes; then
	// twice as long each time, up to 30 minutes).
	FlakyBackoff time.Duration
	// RelayRetry is how long a relay that failed is skipped at first, then
	// twice as long each time up to 5 minutes (default 15 s; tests).
	RelayRetry time.Duration
	// RelayIdle is how long a connection to a relay other than the home
	// one stays open unused (default 60 s).
	RelayIdle time.Duration
	// PathByLoss lets loss and delay move a working direct path to the
	// relay (M2's "bad line"), when the relay is clearly cleaner: at most
	// half as many of its pings lost or late. On a line that loses TCP as
	// well (the home line to Hong Kong) both are bad, and nothing moves.
	// denebd turns it on by default.
	PathByLoss bool
	// DupSmall sends small WireGuard packets twice on a direct path that
	// loses packets: "auto" (the default) from 5% measured loss on, "on"
	// always, "off" never. The second copy is dropped by WireGuard as a
	// replay, so peers of any version are fine with it. On the home line
	// to Hong Kong (11-08: 14% of packets lost at random, TCP alike, so the
	// relay is no better) a pair is lost only 2.5% of the time, and a long
	// connection stalls on a retransmission that much less often.
	DupSmall string
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
	// OnStaleHandshake is called when a handshake initiation to the peer
	// is still unanswered as a relay comes up, or after it was sent again
	// just after the path came back: its response may have been lost on
	// the way (through a relay we were not on). The engine starts a new
	// handshake; the same initiation again would only be a replay.
	OnStaleHandshake func(proto.NodePublic)
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
	// Relays are where the peer can be reached through a relay, best
	// first (proto.Peer.Relays, M4): each lets both devices in. Empty from
	// a server before M4, which gives HomeRelay only.
	Relays []proto.RelayID
	// DualHandshake: the peer claimed proto.CapHandshakeDedupe, so its
	// engine drops a repeated handshake message before WireGuard, and ours
	// may send each one both direct and through the relay (sendWG). A peer
	// that did not claim it gets every handshake message one way only.
	DualHandshake bool
}

// RelayList is one scope's relays, best first, as the server orders them:
// the scope's own relays, then the fallback (the deployment's default and
// the public ones). (M4)
type RelayList struct {
	Scope  proto.ScopeID
	Relays []proto.RelayID
}

// relayFailure is a relay that turned this device away or could not be
// reached: the lists skip it until a backoff runs out.
type relayFailure struct {
	until  time.Time
	n      int
	reason string // "refused: notListed", "unreachable: ..."
}

// Conn is safe for concurrent use.
type Conn struct {
	opts      Options
	dupMode   atomic.Pointer[string] // Options.DupSmall, changeable at run time (SetDupSmall)
	nodePriv  key.NodePrivate
	discoPriv [32]byte
	discoPub  [32]byte
	instance  string // random per start: lets peers notice that this engine restarted
	logf      func(format string, args ...any)

	udp      atomic.Pointer[net.UDPConn] // replaced by Rebind
	rebindMu sync.Mutex
	port     uint16
	boundTo  string // interface the UDP socket is pinned to, if any (under mu after New)
	// boundIf is boundTo for the relay dialer, which must not take mu: it
	// runs inside the relay client's connect, which holds the client's
	// lock, while code under mu calls into the client (a lock-order
	// deadlock froze an engine on 11-08 after two network changes).
	boundIf atomic.Pointer[string]
	// Handshake messages handed to WireGuard lately (seenHandshake).
	hsMu       sync.Mutex
	hsSeen     map[uint64]int64
	hsSeed     maphash.Seed
	hsDupes    atomic.Uint64
	noHSDedupe atomic.Bool // tests: an engine from before proto.CapHandshakeDedupe

	localRefused atomic.Bool               // the system refuses packets to the local network (noteSendErr)
	testLocal    atomic.Pointer[testLocal] // RefuseLocalForTest

	bindErr  error // why pinning failed, if it did
	closed   chan struct{}
	closeOne sync.Once

	mu       sync.Mutex
	relayCfg map[proto.RelayID]proto.Relay // every relay the network map names
	relays   map[proto.RelayID]*relayConn  // open connections: resident ones, and others while in use
	home     proto.RelayID
	// M4: a device keeps, for each scope it is in, the first relay of the
	// scope's list it can connect to (an own relay lets in only the devices
	// of its scopes, so one home relay is not enough). resident is the set
	// chosen, home the first scope's (or prefer); relayFail the ones
	// skipped for now.
	relayOrder []proto.RelayID
	relayLists []RelayList
	prefer     proto.RelayID
	chosen     map[proto.ScopeID]proto.RelayID
	resident   map[proto.RelayID]bool
	relayFail  map[proto.RelayID]*relayFailure
	peers      map[proto.NodePublic]*peerState
	byDisco    map[proto.DiscoPublic]*peerState
	byAddr     map[netip.AddrPort]*peerState
	stunTx     map[stun.TxID]proto.RelayID
	stunAddr   map[proto.RelayID]netip.AddrPort
	pongSelf   netip.AddrPort
	lastEps    []netip.AddrPort

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
	// trial: a relay back from a failure, tried on the side; its scopes
	// move back to it once it answers (chooseRelaysLocked).
	trial bool
}

// New opens the UDP socket. Call SetRelays and SetPeers, then Start.
func New(nodePriv key.NodePrivate, discoPriv [32]byte, opts Options) (*Conn, error) {
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = time.Second
	}
	if opts.Trust <= 0 {
		opts.Trust = 2500 * time.Millisecond
	}
	if opts.Hold <= opts.Trust {
		opts.Hold = opts.Trust + 4*opts.Heartbeat
	}
	if opts.Idle <= 0 {
		opts.Idle = 45 * time.Second
	}
	if opts.IdleReprobe <= 0 {
		opts.IdleReprobe = 30 * time.Second
	}
	if opts.FlakyBackoff <= 0 {
		opts.FlakyBackoff = 2 * time.Minute
	}
	if opts.RelayRetry <= 0 {
		opts.RelayRetry = 15 * time.Second
	}
	if opts.RelayIdle <= 0 {
		opts.RelayIdle = time.Minute
	}
	if opts.DupSmall == "" {
		opts.DupSmall = "auto"
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
	c.hsSeed = maphash.MakeSeed()
	return c, nil
}

func (c *Conn) listen(port uint16, ifname string) (*net.UDPConn, error) {
	pc, bound, bindErr, err := c.listenOn(port, ifname)
	if err == nil {
		c.boundTo, c.bindErr = bound, bindErr
		c.boundIf.Store(&bound)
	}
	return pc, err
}

// listenOn opens a UDP socket pinned to ifname if it can; bound is the
// interface it got pinned to ("" for none) and bindErr why not.
func (c *Conn) listenOn(port uint16, ifname string) (pc *net.UDPConn, bound string, bindErr, err error) {
	if c.opts.RelayOnly {
		// Windows does not ask about the loopback address. The socket is
		// there so that nothing else has to check for one.
		p, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, "", nil, err
		}
		return p.(*net.UDPConn), "", nil, nil
	}
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
	c.boundIf.Store(&bound)
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
	p.freshAt = now
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
		p.freshAt = now
		p.degraded, p.reason, p.srtt = false, "", 0
		p.probes, p.pending = nil, map[string]pendingPing{}
		p.relayHistN, p.relayHistI = 0, 0 // a new network: the relay is judged afresh
		p.flaky = nil                     // and the direct addresses
		peers = append(peers, p)
	}
	for id, rc := range c.relays {
		c.closeRelayLocked(id, rc)
	}
	clear(c.relayFail) // a new network: every relay may be reachable again
	c.pongSelf = netip.AddrPort{}
	clear(c.stunAddr)
	start, prefs, changed := c.chooseRelaysLocked(now)
	c.mu.Unlock()
	c.logf("[net] %s: redialing the relays and probing %d peers", reason, len(peers))
	c.startRelays(start, prefs, changed)
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
		var clients []*relayConn
		for _, r := range c.relays {
			close(r.stop)
			clients = append(clients, r)
		}
		c.relays = map[proto.RelayID]*relayConn{}
		c.mu.Unlock()
		for _, r := range clients { // outside mu (see boundIf)
			r.client.Close()
		}
	})
	return nil
}

// SetRelays replaces the relay table and the scopes' relay lists. For each
// list the first relay that can be connected to is kept open (resident),
// several scopes choosing the same one share the connection; the first
// list's choice is home, unless prefer names a relay of the table (a test
// knob: denebd -home-relay). A relay that turns the device away, or cannot
// be reached, is skipped for a backoff and the next on the list is used:
// an own relay first, the public one when it fails ("改走公共中转站").
// Connections to other relays are opened when a peer reached through one
// is sent to, and closed after RelayIdle unused (M2, for M4's many
// relays). Connections to relays that stay are kept; gone ones are closed.
func (c *Conn) SetRelays(relays []proto.Relay, lists []RelayList, prefer proto.RelayID) {
	want := map[proto.RelayID]proto.Relay{}
	order := make([]proto.RelayID, 0, len(relays))
	for _, r := range relays {
		want[r.ID] = r
		order = append(order, r.ID)
	}
	now := time.Now()
	c.mu.Lock()
	listsChanged := !slices.EqualFunc(lists, c.relayLists, func(a, b RelayList) bool {
		return a.Scope == b.Scope && slices.Equal(a.Relays, b.Relays)
	})
	c.relayCfg, c.relayOrder, c.relayLists, c.prefer = want, order, lists, prefer
	for id, rc := range c.relays {
		r, ok := want[id]
		if !ok || !sameRelay(r, rc.relay) {
			c.closeRelayLocked(id, rc)
			delete(c.relayFail, id) // a new relay at that name: judged afresh
		}
	}
	if listsChanged {
		// A list changed (a relay dragged in, a share given): who lets
		// this device in may have changed too.
		for id, f := range c.relayFail {
			if strings.HasPrefix(f.reason, "refused") {
				delete(c.relayFail, id)
			}
		}
	}
	start, prefs, changed := c.chooseRelaysLocked(now)
	c.mu.Unlock()
	c.startRelays(start, prefs, changed)
}

// chooseRelaysLocked picks each list's relay, opens the resident ones not
// open yet, and marks the rest on demand. The caller passes the results to
// startRelays once mu is released.
//
// A relay that failed comes back only once it answers. When its backoff
// runs out, a connection to it is opened on the side (a trial) and its
// scopes stay where they are; when the trial connects, the relay's failure
// is forgotten and the next choice moves the scopes back. Taking it back on
// the backoff alone made the line go to a relay still down for as long as
// the dial took (r2, 11-09 21:21: "my server" stopped, the public relay
// after 4.5 s, then 3 s back on the stopped one, 2 pings of 30 lost). A
// list with nothing else uses it at once, as before.
func (c *Conn) chooseRelaysLocked(now time.Time) (start []*relayConn, prefs []relayPref, changed bool) {
	known := func(id proto.RelayID) bool {
		_, ok := c.relayCfg[id]
		return ok
	}
	usable := func(id proto.RelayID) bool { return known(id) && c.relayFail[id] == nil }
	retry := func(id proto.RelayID) bool {
		f := c.relayFail[id]
		return known(id) && f != nil && !now.Before(f.until)
	}
	trials := map[proto.RelayID]bool{}
	chosen := map[proto.ScopeID]proto.RelayID{}
	resident := map[proto.RelayID]bool{}
	var home proto.RelayID
	if c.prefer != "" && (usable(c.prefer) || retry(c.prefer)) {
		// The test knob (denebd -home-relay): this device lives on that one
		// relay alone, whatever the lists say, as before M4; peers on other
		// relays reach it there (the cross-relay checks).
		home, resident[c.prefer] = c.prefer, true
		for _, l := range c.relayLists {
			chosen[l.Scope] = c.prefer
		}
	}
	for _, l := range c.relayLists {
		if home == c.prefer && c.prefer != "" {
			break
		}
		pick := proto.RelayID("")
		var back []proto.RelayID // before pick on the list: tried on the side
		for _, id := range l.Relays {
			if usable(id) {
				pick = id
				break
			}
			if retry(id) {
				back = append(back, id)
			}
		}
		if pick == "" && len(back) > 0 {
			pick, back = back[0], back[1:] // nothing else: the first one back, at once
		}
		for _, id := range back {
			trials[id] = true
		}
		if pick != "" {
			chosen[l.Scope], resident[pick] = pick, true
			if home == "" {
				home = pick
			}
		}
	}
	if home == "" { // in no scope yet (just logged in): any relay, for STUN and to be found
		for _, id := range c.relayOrder {
			if usable(id) || retry(id) {
				home, resident[id] = id, true
				break
			}
		}
	}
	changed = home != c.home || !maps.Equal(resident, c.resident)
	c.home, c.chosen, c.resident = home, chosen, resident
	for _, id := range c.relayOrder {
		if !resident[id] && !trials[id] {
			continue
		}
		if rc, open := c.relays[id]; open {
			rc.trial = trials[id] && !rc.up
			continue
		}
		rc, err := c.newRelayConn(c.relayCfg[id], id == home)
		if err != nil {
			c.logf("[relay] %s: %v", c.relayCfg[id].Name, err)
			continue
		}
		rc.trial = trials[id]
		c.relays[id] = rc
		start = append(start, rc)
	}
	for id, rc := range c.relays {
		rc.onDemand = !resident[id]
		if rc.lastUsed.IsZero() {
			rc.lastUsed = now
		}
		if changed { // NotePreferred takes the client's lock: only when home moved
			prefs = append(prefs, relayPref{rc, id == home})
		}
	}
	return start, prefs, changed
}

type relayPref struct {
	rc   *relayConn
	home bool
}

// startRelays finishes chooseRelaysLocked outside mu.
func (c *Conn) startRelays(start []*relayConn, prefs []relayPref, changed bool) {
	for _, rc := range start {
		go c.relayLoop(rc)
	}
	// Each on its own: NotePreferred takes the client's lock, which a dial
	// in progress holds until it gives up. One relay that hangs must not
	// hold up the others (r2: the public relay waited behind a stopped one).
	for _, p := range prefs {
		go p.rc.client.NotePreferred(p.home)
	}
	if changed || len(start) > 0 {
		go c.stunOnce()
		c.noteEndpoints()
	}
	if changed && c.opts.OnEndpoints != nil {
		// The relays to report changed (Report.Relays, HomeRelay), even if
		// the endpoints did not: peers learn where to find this device
		// from the next map (M4).
		c.opts.OnEndpoints()
	}
}

// rechooseRelays runs the choice again: a relay failed, or a backoff ran
// out and a better one on a list may be back.
func (c *Conn) rechooseRelays() {
	c.mu.Lock()
	start, prefs, changed := c.chooseRelaysLocked(time.Now())
	c.mu.Unlock()
	c.startRelays(start, prefs, changed)
}

// hasAlternativeLocked: a list relay id is on has another relay to use.
// Without one, a dropped relay is redialed at once, as before M4.
func (c *Conn) hasAlternativeLocked(id proto.RelayID, now time.Time) bool {
	for _, l := range c.relayLists {
		if !slices.Contains(l.Relays, id) {
			continue
		}
		for _, other := range l.Relays {
			if other == id {
				continue
			}
			if _, ok := c.relayCfg[other]; !ok {
				continue
			}
			if f := c.relayFail[other]; f == nil || !now.Before(f.until) {
				return true
			}
		}
	}
	return false
}

// noteRelayFailLocked skips relay id for a while: RelayRetry (15 s),
// doubling up to 5 minutes while it keeps failing.
func (c *Conn) noteRelayFailLocked(id proto.RelayID, reason string, now time.Time) {
	c.noteRelayFailForLocked(id, reason, now, c.opts.RelayRetry)
}

// noteRelayFailForLocked is noteRelayFailLocked with another first wait:
// a relay that was up and dropped is tried again after a few seconds.
func (c *Conn) noteRelayFailForLocked(id proto.RelayID, reason string, now time.Time, first time.Duration) {
	if c.relayFail == nil {
		c.relayFail = map[proto.RelayID]*relayFailure{}
	}
	f := c.relayFail[id]
	if f == nil {
		f = &relayFailure{}
		c.relayFail[id] = f
	}
	f.n++
	f.until = now.Add(min(5*time.Minute, first<<min(f.n-1, 6)))
	f.reason = reason
}

func (c *Conn) closeRelayLocked(id proto.RelayID, rc *relayConn) {
	close(rc.stop)
	// Not under mu: Close waits for a connect in progress, which may take
	// the dial's whole timeout.
	go rc.client.Close()
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
	hostport := net.JoinHostPort(r.HostName, strconv.Itoa(int(r.DERPPort)))
	if r.Pin != "" {
		// A Deneb relay's self-signed certificate, trusted by its key (M4).
		// derphttp's own TLS refuses a config that skips the chain check
		// (it panics), so the dialer does TLS itself and derphttp is told
		// plain http: it then speaks DERP over the pinned connection.
		cfg, err := proto.PinnedTLSConfig(r.Pin)
		if err != nil {
			return nil, err
		}
		dc, err := derphttp.NewClient(c.nodePriv, "http://"+hostport+"/derp", c.derpLogf(r.Name), netmon.NewStatic())
		if err != nil {
			return nil, err
		}
		dc.SetURLDialer(c.pinnedDialer(r, cfg))
		dc.NotePreferred(home)
		return &relayConn{relay: r, client: dc, stop: make(chan struct{})}, nil
	}
	url := "https://" + hostport + "/derp"
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

// pinnedDialer dials like relayDialer, then does TLS checking the relay's
// key against its pin: a key that is not the pinned one fails the dial,
// told apart from a refusal (which comes after) and from no answer.
func (c *Conn) pinnedDialer(r proto.Relay, cfg *tls.Config) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dial := c.relayDialer(r)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tcfg := cfg.Clone()
		if _, err := netip.ParseAddr(r.HostName); err != nil && r.HostName != "" {
			tcfg.ServerName = r.HostName
		}
		tc := tls.Client(conn, tcfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("pinned TLS: %w", err)
		}
		return tc, nil
	}
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
		d := net.Dialer{Timeout: 3 * time.Second}
		boundTo := ""
		if b := c.boundIf.Load(); b != nil {
			boundTo = *b
		}
		if boundTo != "" {
			if ctl, err := bind.Control(boundTo); err == nil {
				d.Control = ctl
				conn, err := d.DialContext(ctx, "tcp4", target)
				if err == nil {
					return conn, nil
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					return nil, err // the relay is not answering: the routing table would not help
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
			gaveUp, resident := false, c.resident[rc.relay.ID]
			switch {
			case !was && c.relays[rc.relay.ID] == rc:
				// Never came up: no dial, a TLS key that is not the pinned
				// one, or turned away without a word. The lists move on.
				c.noteRelayFailLocked(rc.relay.ID, "unreachable: "+err.Error(), time.Now())
				c.closeRelayLocked(rc.relay.ID, rc)
				gaveUp = true
			case was && resident && c.relays[rc.relay.ID] == rc && c.hasAlternativeLocked(rc.relay.ID, time.Now()):
				// A resident relay dropped (stopped, the line to it cut):
				// its scopes take the next one on their lists now, so peers
				// can find this device while it is gone, and come back to
				// it once it answers again (a trial, chooseRelaysLocked),
				// tried a few seconds later first.
				c.noteRelayFailForLocked(rc.relay.ID, "lost: "+err.Error(), time.Now(), min(5*time.Second, c.opts.RelayRetry))
				c.closeRelayLocked(rc.relay.ID, rc)
				gaveUp = true
			}
			c.mu.Unlock()
			if gaveUp {
				if was {
					c.logf("[relay] lost %s (%v); its scopes take the next on their lists for now", rc.relay.Name, err)
					c.relayChanged(rc.relay.ID)
				} else {
					c.logf("[relay] cannot connect to %s (%v); trying the next on its lists", rc.relay.Name, err)
				}
				c.rechooseRelays()
				return
			}
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
		if hm, ok := m.(derp.HealthMessage); ok && strings.HasPrefix(hm.Problem, proto.RelayRefusedPrefix) {
			// A Deneb relay turning this device away (M4): it is someone's
			// own relay and the device is in none of its scopes, or the
			// login ran out.
			reason := strings.TrimPrefix(hm.Problem, proto.RelayRefusedPrefix)
			c.mu.Lock()
			c.noteRelayFailLocked(rc.relay.ID, "refused: "+reason, time.Now())
			if c.relays[rc.relay.ID] == rc {
				c.closeRelayLocked(rc.relay.ID, rc)
			}
			c.mu.Unlock()
			c.logf("[relay] %s turned this device away (%s); trying the next on its lists", rc.relay.Name, reason)
			c.rechooseRelays()
			return
		}
		c.mu.Lock()
		was := rc.up
		backNow := false
		if !was {
			rc.up, rc.since = true, time.Now()
			_, failed := c.relayFail[rc.relay.ID]
			delete(c.relayFail, rc.relay.ID)
			backNow = failed || rc.trial
			rc.trial = false
		}
		if _, ok := m.(derp.ReceivedPacket); ok {
			rc.lastUsed = time.Now()
		}
		c.mu.Unlock()
		if !was {
			c.logf("[relay] connected to %s (%s)", rc.relay.Name, rc.relay.HostName)
			c.relayChanged(rc.relay.ID)
			c.restartUnansweredHandshakes()
			if backNow {
				c.rechooseRelays() // it answers again: its scopes move back to it
			}
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
// restartUnansweredHandshakes runs when a relay comes up. After a network
// change or a sleep the peers answer through a relay until they hear us
// direct again, and while we are not yet on it the relay has nowhere to
// deliver: a handshake response sent then is gone. Sending the same
// initiation again only draws a replay, and WireGuard tries a new one 5 s
// after the last. So every handshake still unanswered after staleInit is
// started over now (test Mac, 20:10: the sessions back 6 s after the
// network instead of under one).
func (c *Conn) restartUnansweredHandshakes() {
	if c.opts.OnStaleHandshake == nil {
		return
	}
	now := time.Now()
	var ks []proto.NodePublic
	c.mu.Lock()
	for k, p := range c.peers {
		if p.active && p.pendingInit != nil && now.Sub(p.pendingInitAt) > staleInit && now.Sub(p.pendingInitAt) < 5*time.Second {
			p.pendingInit = nil // a new one comes
			ks = append(ks, k)
		}
	}
	c.mu.Unlock()
	for _, k := range ks {
		c.opts.OnStaleHandshake(k)
	}
}

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
	hs := isHandshakeMsg(p.data) && !c.noHSDedupe.Load()
	if hs && c.seenHandshake(p.data) {
		c.hsDupes.Add(1)
		if pe, ok := p.ep.(*peerEndpoint); ok {
			c.mu.Lock()
			if q := c.peers[pe.key]; q != nil {
				q.rxHSDupes++
			}
			c.mu.Unlock()
		}
		return // the copy that came the other way was handed over already
	}
	select {
	case c.rx <- p:
	default:
		if hs {
			c.forgetHandshake(p.data) // dropped: the other copy may still go in
		}
	}
}

// SetHandshakeDedupe turns the dropping of repeated handshake messages,
// and sending them both ways to peers that claim it, on or off (tests: an
// engine from before proto.CapHandshakeDedupe has neither).
func (c *Conn) SetHandshakeDedupe(on bool) { c.noHSDedupe.Store(!on) }

// HandshakeDupes is how many repeated handshake messages were dropped.
func (c *Conn) HandshakeDupes() uint64 { return c.hsDupes.Load() }

// hsDedupeFor is how long a handshake message handed to WireGuard is
// remembered, its copies through the other path dropped (deliver). Longer
// than the paths ever differ; a resend is the same bytes and WireGuard
// would refuse it as a replay anyway, and a new handshake has new bytes.
const hsDedupeFor = 5 * time.Second

// seenHandshake reports whether the same handshake message was handed to
// WireGuard lately, and remembers it. The two copies of one message (a
// peer claiming proto.CapHandshakeDedupe sends both direct and through
// the relay) come in on two goroutines: one gets here first, the other is
// dropped, so WireGuard never works on both at once. Two copies of one
// initiation at once split the session; of one response, they make two
// sessions with the same keys.
func (c *Conn) seenHandshake(b []byte) bool {
	h := maphash.Bytes(c.hsSeed, b)
	now := time.Now().UnixNano()
	c.hsMu.Lock()
	defer c.hsMu.Unlock()
	if at, ok := c.hsSeen[h]; ok && now-at < int64(hsDedupeFor) {
		return true
	}
	if len(c.hsSeen) >= 1024 {
		for k, at := range c.hsSeen {
			if now-at >= int64(hsDedupeFor) {
				delete(c.hsSeen, k)
			}
		}
	}
	if c.hsSeen == nil {
		c.hsSeen = map[uint64]int64{}
	}
	c.hsSeen[h] = now
	return false
}

// forgetHandshake undoes seenHandshake for a message WireGuard never got
// (deliver found its queue full).
func (c *Conn) forgetHandshake(b []byte) {
	h := maphash.Bytes(c.hsSeed, b)
	c.hsMu.Lock()
	delete(c.hsSeen, h)
	c.hsMu.Unlock()
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
// relayForLocked is the relay to reach p through: the first of the peer's
// relays this device can use (M4: each lets both in), else its home relay
// (servers before M4), else this device's home.
func (c *Conn) relayForLocked(p *peerState) proto.RelayID {
	now := time.Now()
	cands := p.cfg.Relays
	if len(cands) == 0 && p.cfg.HomeRelay != "" {
		cands = []proto.RelayID{p.cfg.HomeRelay}
	}
	first, back := proto.RelayID(""), proto.RelayID("")
	for _, id := range cands {
		if _, ok := c.relayCfg[id]; !ok {
			continue
		}
		if first == "" {
			first = id
		}
		f := c.relayFail[id]
		if f == nil {
			return id
		}
		// Its backoff ran out, but it has not answered yet (a trial is
		// under way): not before one that never failed.
		if back == "" && !now.Before(f.until) {
			back = id
		}
	}
	if back != "" {
		return back
	}
	if first != "" {
		// All of the peer's relays failing here: it has most likely moved
		// to the same next one on the lists as this device (the map has
		// not said so yet). Through this device's home, unless that is
		// failing too (r2: the peer's relay stopped, and packets went on
		// to it for as long as a dial there hung).
		if f := c.relayFail[c.home]; c.home != "" && f == nil {
			return c.home
		}
		return first // all failing: the best one may be back
	}
	return c.home
}

func (c *Conn) sendRelay(p *peerState, b []byte) error {
	c.mu.Lock()
	// The peer's config is read under mu: SetPeers replaces it (a race the
	// race detector found, 11-12).
	to, label := key.NodePublicFromRaw32(mem.B(p.cfg.NodeKey[:])), p.cfg.Label
	if d := p.relayHSDelay; d > 0 && isHandshakeMsg(b) {
		// DelayRelayHandshakesForTest: the relay's TCP waiting out a lost
		// segment on a lossy line.
		c.mu.Unlock()
		cp := append([]byte(nil), b...)
		time.AfterFunc(d, func() {
			c.mu.Lock()
			rc, started := c.relayLocked(c.relayForLocked(p), time.Now())
			c.mu.Unlock()
			if rc != nil {
				if started {
					go c.relayLoop(rc)
				}
				rc.client.Send(to, cp)
			}
		})
		return nil
	}
	rc, started := c.relayLocked(c.relayForLocked(p), time.Now())
	c.mu.Unlock()
	if rc == nil {
		return errors.New("no relay to reach " + label)
	}
	if started {
		c.logf("[relay] connecting to %s to reach %s", rc.relay.Name, label)
		go c.relayLoop(rc)
	}
	return rc.client.Send(to, b)
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
	if c.opts.RelayOnly {
		return // a public address is for direct paths
	}
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
	if c.opts.RelayOnly {
		return nil // nowhere: the relays only
	}
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

// ResidentRelays are the relays kept open, home first, then in the table's
// order: what the device reports it can be reached through (M4).
func (c *Conn) ResidentRelays() []proto.RelayID {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []proto.RelayID
	if c.home != "" {
		out = append(out, c.home)
	}
	for _, id := range c.relayOrder {
		if c.resident[id] && id != c.home {
			out = append(out, id)
		}
	}
	return out
}

// ChosenRelays is, for each scope, the relay its list came down to: not
// the list's first when that one turned the device away or cannot be
// reached (M4: the client shows "改走公共中转站").
func (c *Conn) ChosenRelays() map[proto.ScopeID]proto.RelayID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.chosen)
}

// RelayFailures are the relays skipped for now, and why.
func (c *Conn) RelayFailures() map[proto.RelayID]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[proto.RelayID]string{}
	now := time.Now()
	for id, f := range c.relayFail {
		if now.Before(f.until) {
			out[id] = f.reason
		}
	}
	return out
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
