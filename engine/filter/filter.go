// Package filter keeps scopes apart on a device that is in several of them
// (multi-lan-tech §3.5, layers 2 and 3). It sits between WireGuard and the
// local stack and judges every packet:
//
//   - never forward: inbound packets must be for this device, outbound ones
//     must come from it;
//   - a new inbound flow is let in only if this device opened that port to a
//     scope it shares with the sender (ping is always open, unless refused);
//   - replies to what this device started always come back.
//
// It fails closed: what it cannot parse, it drops.
package filter

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Rule lets in one port range of one protocol.
type Rule struct {
	Proto       uint8
	First, Last uint16
}

// Peer is what the filter knows about one visible peer.
type Peer struct {
	Label string // "nas.jie", for logs
	// Scopes are the scopes shared with the peer that are not paused here.
	// Those the peer paused on itself stay: it keeps to its pause on its
	// own, and its first packet after resuming gets through at once (M2,
	// engine/datapath.go).
	Scopes []string
	// RefusedByMe: this device's owner does not let the peer's owner use
	// it, so nothing the peer starts gets in (not even ping).
	RefusedByMe bool
	// RefusedByPeer: the peer's owner does not let this owner use it, so
	// this device starts nothing toward it. Replies still flow, so the peer
	// can still use this device if this owner allows it.
	RefusedByPeer bool
}

// Policy is one version of the three tables, as the filter needs them. It
// is never changed after SetPolicy; the engine builds a new one instead.
type Policy struct {
	Self  netip.Addr
	Peers map[netip.Addr]*Peer // every visible peer, by overlay address
	Open  map[string][]Rule    // scope -> what this device opens to it
	// NoInbound are the scopes this device only connects out of (M3: the
	// receiving side of a share): nothing a peer starts comes in through
	// them, not even a ping, whatever Open says. Replies to what this
	// device started pass, and a peer's other scopes still count.
	NoInbound map[string]bool

	// Test builds only (engine/testhooks): let this device send to
	// addresses that are not peers, or turn the filter off, to stage the
	// attacks of multi-lan-tech §6.4.
	ExtraOut []netip.Prefix
	Off      bool
}

func (p *Policy) allows(peer *Peer, proto uint8, port uint16) bool {
	for _, s := range peer.Scopes {
		if p.NoInbound[s] {
			continue
		}
		for _, r := range p.Open[s] {
			if r.Proto == proto && r.First <= port && port <= r.Last {
				return true
			}
		}
	}
	return false
}

// pingable: a peer may ping this device through any scope they share that
// is not NoInbound (DESIGN §12: every peer of a scope may ping).
func (p *Policy) pingable(peer *Peer) bool {
	for _, s := range peer.Scopes {
		if !p.NoInbound[s] {
			return true
		}
	}
	return false
}

// Idle timeouts. An established TCP connection may sit quiet for a long
// time (an SSH session), so it gets an hour; once FIN or RST is seen it
// only needs to drain.
const (
	tcpIdle    = time.Hour
	tcpClosing = 30 * time.Second
	udpIdle    = 2 * time.Minute
	icmpIdle   = 30 * time.Second
	// A datagram's pieces travel together: its head admits the rest for a
	// few seconds, which also keeps the table small under a steady stream
	// of large datagrams (30 s filled it from about 140 a second).
	fragIdle = 5 * time.Second
	maxFlows = 1 << 16
	maxFrags = 1 << 12
	// One peer cannot fill either table, which would refuse every new
	// connection, this device's own included (a ping with a new identifier
	// is a new flow): what a peer starts is capped well below the total.
	maxPeerFlows = maxFlows / 16
	maxPeerFrags = maxFrags / 4

	// fragHold: a later piece of a datagram that comes in before the first
	// piece waits this long for it. The engine sends a small piece through
	// the relay as well as direct just after waking (magicsock's
	// wakeRelay), while the large first piece goes direct only, and the
	// relay can be the faster way: the small piece comes first, and the
	// peer's WireGuard then drops its direct copy as a replay, so the one
	// held here is the only one there is (Windows, 11-10: the first large
	// ping of each round lost).
	fragHold = 2 * time.Second
	// What is held at once: pieces, of them from one peer, and the size of
	// one (a piece of a datagram split for our MTU; larger ones are not
	// held, so a peer cannot pin much memory here).
	maxEarly     = 256
	maxPeerEarly = 64
	maxEarlySize = 1500
)

// FragmentHeld is In's answer for a later piece of a datagram whose first
// piece has not come in yet: not dropped, but kept (TUN.Write leaves it to
// the filter) until the first piece is let in, when TakeEarly hands it
// back, or for fragHold, after which it counts as fragment-without-head.
const FragmentHeld = "fragment-held"

// flowKey is written from this device's point of view, so both directions
// of a flow map to the same key.
type flowKey struct {
	proto         uint8
	local, remote netip.AddrPort
}

type flow struct {
	expires  int64 // unix nanoseconds
	since    int64 // when it started
	lastSeen int64 // last packet either way
	inbound  bool  // the peer started it
	port     uint16
	closing  bool
	// TCP: the sequence number each end expects next, as far as the
	// segments seen tell (inEnd: this device's stack, from the peer's
	// segments; outEnd: the peer, from ours). A reset of a cut connection
	// carries them (cutResetsLocked).
	inEnd, outEnd uint32
	inSet, outSet bool
}

// noteSeq records the sequence number after h, a segment in or out.
func (fl *flow) noteSeq(h Packet, out bool) {
	if h.Proto != ProtoTCP || h.TCPRst || h.Fragment || h.FirstFragment {
		return
	}
	if out {
		if !fl.outSet || int32(h.TCPEnd-fl.outEnd) > 0 {
			fl.outEnd, fl.outSet = h.TCPEnd, true
		}
	} else if !fl.inSet || int32(h.TCPEnd-fl.inEnd) > 0 {
		fl.inEnd, fl.inSet = h.TCPEnd, true
	}
}

// cutMemory is how long a connection cut by a policy change is remembered:
// a segment of it, either way, draws a reset (CutTCP, CutTCPOut).
const cutMemory = 30 * time.Second

// Use is a service of this device in use by a peer right now: the
// connections a peer started to one local port (M2, scene 5: closing a
// service sends a short red light down the lines of those using it).
type Use struct {
	Peer     netip.Addr
	Proto    uint8
	Port     uint16
	Conns    int
	Since    time.Time // the oldest connection
	LastSeen time.Time // the latest packet
}

type fragKey struct {
	src, dst netip.Addr
	proto    uint8
	id       uint16
}

// Filter is safe for concurrent use.
type Filter struct {
	pol atomic.Pointer[Policy]
	now func() time.Time

	mu    sync.Mutex
	flows map[flowKey]*flow
	frags map[fragKey]int64
	// Of flows and frags, how many each peer started (by its address):
	// what maxPeerFlows and maxPeerFrags count.
	peerFlows map[netip.Addr]int
	peerFrags map[netip.Addr]int
	// early holds later pieces that came before their first (FragmentHeld),
	// by datagram; nEarly counts them all, peerEarly by peer, and held is
	// nEarly for In's callers to read without the lock.
	early     map[fragKey][]earlyPiece
	nEarly    int
	peerEarly map[netip.Addr]int
	held      atomic.Int32
	cut       map[flowKey]int64 // TCP connections a policy change cut, until when they are remembered
	// cutUntil is the latest expiry ever put in cut: past it, nothing in
	// cut is live, and wasCut answers without the lock (every TCP segment
	// asks it; most of the time nothing was cut lately).
	cutUntil atomic.Int64
	drops    map[string]uint64
	seen     map[string]time.Time // rate-limits drop logging
	logf     func(format string, args ...any)

	// OnReset gets the TCP resets of the connections a policy change cut:
	// toStack for this device's own stack, toPeers for WireGuard. Set
	// once, before the first SetPolicy (NewTUN does).
	OnReset func(toStack, toPeers [][]byte)
}

// New returns a filter that drops everything until SetPolicy.
func New(logf func(format string, args ...any)) *Filter {
	if logf == nil {
		logf = log.Printf
	}
	f := &Filter{
		now:   time.Now,
		flows: map[flowKey]*flow{},
		frags: map[fragKey]int64{},

		peerFlows: map[netip.Addr]int{},
		peerFrags: map[netip.Addr]int{},
		early:     map[fragKey][]earlyPiece{},
		peerEarly: map[netip.Addr]int{},
		cut:       map[flowKey]int64{},
		drops:     map[string]uint64{},
		seen:      map[string]time.Time{},
		logf:      logf,
	}
	f.pol.Store(&Policy{Peers: map[netip.Addr]*Peer{}})
	return f
}

// Policy returns the policy in force.
func (f *Filter) Policy() *Policy { return f.pol.Load() }

// SetPolicy puts p in force and cuts every tracked flow p no longer allows:
// a peer that left, a paused scope, a service switched off, a refusal. So a
// change takes effect on open connections too, not only on new ones.
//
// It returns the uses it cut: connections peers had open to a service of
// this device that the new policy no longer allows.
func (f *Filter) SetPolicy(p *Policy) []Use {
	if p.Peers == nil {
		p.Peers = map[netip.Addr]*Peer{}
	}
	f.pol.Store(p)
	f.mu.Lock()
	now := f.now().UnixNano()
	var toStack, toPeers [][]byte
	defer func() {
		f.mu.Unlock()
		if f.OnReset != nil && (len(toStack) > 0 || len(toPeers) > 0) {
			f.OnReset(toStack, toPeers)
		}
	}()
	var cut []flowEntry
	for k, fl := range f.flows {
		if p.Off {
			break
		}
		peer := p.Peers[k.remote.Addr()]
		drop := false
		switch {
		case k.local.Addr() != p.Self, peer == nil:
			drop = true
		case fl.inbound && peer.RefusedByMe:
			drop = true
		case fl.inbound && k.proto != ProtoICMP && !p.allows(peer, k.proto, fl.port):
			drop = true
		case !fl.inbound && peer.RefusedByPeer:
			drop = true
		}
		if drop {
			if fl.inbound && k.proto != ProtoICMP && !fl.closing && fl.expires >= now {
				cut = append(cut, flowEntry{k, *fl})
			}
			// A live TCP connection, either way: both ends are told at
			// once, rather than hanging until their own timeouts (an SSH
			// session that froze instead of closing, 11-09).
			if k.proto == ProtoTCP && !fl.closing && fl.expires >= now {
				if fl.inSet {
					if r := resetWith(k.remote, k.local, fl.inEnd); r != nil {
						toStack = append(toStack, r)
					}
				}
				if fl.outSet {
					if r := resetWith(k.local, k.remote, fl.outEnd); r != nil {
						toPeers = append(toPeers, r)
					}
				}
				if len(f.cut) < maxFlows {
					f.cut[k] = now + int64(cutMemory)
					f.noteCutUntil(now + int64(cutMemory))
				}
			}
			f.dropFlowLocked(k, fl)
		}
	}
	return uses(cut)
}

// wasCut: key is a TCP connection a policy change cut lately.
func (f *Filter) wasCut(key flowKey, now time.Time) bool {
	if now.UnixNano() > f.cutUntil.Load() {
		return false // nothing cut is still remembered: no lock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	exp, ok := f.cut[key]
	return ok && exp >= now.UnixNano()
}

// noteCutUntil raises cutUntil to exp.
func (f *Filter) noteCutUntil(exp int64) {
	for {
		cur := f.cutUntil.Load()
		if exp <= cur || f.cutUntil.CompareAndSwap(cur, exp) {
			return
		}
	}
}

type flowEntry struct {
	k  flowKey
	fl flow
}

// Usage lists the services of this device in use: live connections peers
// started (pings are not a service), one entry per peer and port.
func (f *Filter) Usage() []Use {
	f.mu.Lock()
	now := f.now().UnixNano()
	var live []flowEntry
	for k, fl := range f.flows {
		if fl.inbound && k.proto != ProtoICMP && !fl.closing && fl.expires >= now {
			live = append(live, flowEntry{k, *fl})
		}
	}
	f.mu.Unlock()
	return uses(live)
}

// Using lists the peers' services this device is using: live connections
// this device started to a peer (pings are not a service), one entry per
// peer and remote port (M2: "已连接 2:14" on the line to that device).
func (f *Filter) Using() []Use {
	f.mu.Lock()
	now := f.now().UnixNano()
	var live []flowEntry
	for k, fl := range f.flows {
		if !fl.inbound && k.proto != ProtoICMP && !fl.closing && fl.expires >= now {
			e := flowEntry{k, *fl}
			e.fl.port = k.remote.Port()
			live = append(live, e)
		}
	}
	f.mu.Unlock()
	return uses(live)
}

func uses(es []flowEntry) []Use {
	type key struct {
		peer  netip.Addr
		proto uint8
		port  uint16
	}
	agg := map[key]*Use{}
	var order []key
	for _, e := range es {
		k := key{e.k.remote.Addr(), e.k.proto, e.fl.port}
		u := agg[k]
		if u == nil {
			u = &Use{Peer: k.peer, Proto: k.proto, Port: k.port}
			agg[k] = u
			order = append(order, k)
		}
		u.Conns++
		since, last := time.Unix(0, e.fl.since), time.Unix(0, e.fl.lastSeen)
		if u.Since.IsZero() || since.Before(u.Since) {
			u.Since = since
		}
		if last.After(u.LastSeen) {
			u.LastSeen = last
		}
	}
	out := make([]Use, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	slices.SortFunc(out, func(a, b Use) int {
		if c := a.Peer.Compare(b.Peer); c != 0 {
			return c
		}
		if a.Proto != b.Proto {
			return int(a.Proto) - int(b.Proto)
		}
		return int(a.Port) - int(b.Port)
	})
	return out
}

// StaleTCP is In's reason for a segment of a connection this side does not
// know, from a peer that may open one now: the TUN answers it with a
// reset.
const StaleTCP = "tcp-stale"

// CutTCP and CutTCPOut are In's and Out's reasons for a segment of a
// connection a policy change cut (the share taken back, the service
// closed, the person refused, the LAN paused): the TUN answers the peer,
// or this device's stack, with a reset.
const (
	CutTCP    = "tcp-cut"
	CutTCPOut = "tcp-cut-out"
)

// In judges a packet from WireGuard on its way to the local stack.
// WireGuard has already checked that the source address belongs to the
// peer that sent it (layer 4).
func (f *Filter) In(b []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := Parse(b)
	if !ok {
		return f.drop("in", unparsed(b), h)
	}
	if pol.Off {
		return true, ""
	}
	// Never act as a router: a packet not for this device is someone trying
	// to go through it, for instance into another scope.
	if h.Dst != pol.Self {
		return f.drop("in", "transit-in", h)
	}
	now := f.now()
	if h.Proto == ProtoTCP && !h.TCPSYN && !h.TCPRst && !h.Fragment &&
		f.wasCut(flowKey{h.Proto, netip.AddrPortFrom(h.Dst, h.DPort), netip.AddrPortFrom(h.Src, h.SPort)}, now) {
		// More of a connection a policy change cut, the peer visible or
		// not: answered with a reset carrying the ACK it gave, which it
		// takes for certain (the one sent at the cut may have been off).
		return f.drop("in", CutTCP, h)
	}
	peer := pol.Peers[h.Src]
	if peer == nil {
		return f.drop("in", "not-a-visible-peer", h)
	}
	if h.Fragment {
		return f.fragment("in", h, b)
	}
	key := flowKey{h.Proto, netip.AddrPortFrom(h.Dst, h.DPort), netip.AddrPortFrom(h.Src, h.SPort)}
	if h.Proto == ProtoICMP {
		switch h.ICMPType {
		case icmpEchoRequest:
			if peer.RefusedByMe {
				return f.drop("in", "refused", h)
			}
			if !pol.pingable(peer) {
				return f.drop("in", "no-inbound", h)
			}
			return f.accept(key, &flow{inbound: true}, h, now, false)
		case icmpEchoReply:
			if f.touch(key, h, now, false) {
				f.noteFrag(h, now) // a reply to a large ping comes in pieces
				return true, ""
			}
			return f.drop("in", "icmp-reply-without-flow", h)
		case icmpUnreachable, icmpTimeExceeded, icmpParamProblem:
			// An error about a packet this device sent to the same peer.
			in := h.Inner
			if in.Src == pol.Self && in.Dst == h.Src && f.has(flowKey{in.Proto, netip.AddrPortFrom(in.Src, in.SPort), netip.AddrPortFrom(in.Dst, in.DPort)}, now) {
				return true, ""
			}
			return f.drop("in", "icmp-error-without-flow", h)
		}
		return f.drop("in", "icmp-type", h)
	}
	if f.touch(key, h, now, false) {
		f.noteFrag(h, now)
		return true, "" // a reply, or more of an accepted flow
	}
	if h.Proto == ProtoTCP && !h.TCPSYN {
		// A connection this side does not know: its flow was cut when the
		// service closed or the share was taken back, or it expired. If
		// the peer may open it anew now, it is told with a reset (the TUN
		// sends ResetFor): its end gives the old connection up at once and
		// connects again, instead of sending into silence until its own
		// timeout (a share given back after it was taken: about 12 s,
		// 11-09). Anyone else, and any reset, still meets silence.
		if !h.TCPRst && !peer.RefusedByMe && pol.pingable(peer) && pol.allows(peer, h.Proto, h.DPort) {
			return f.drop("in", StaleTCP, h)
		}
		return f.drop("in", "tcp-without-flow", h)
	}
	if peer.RefusedByMe {
		return f.drop("in", "refused", h)
	}
	if !pol.pingable(peer) { // every scope with this peer is NoInbound
		return f.drop("in", "no-inbound", h)
	}
	if !pol.allows(peer, h.Proto, h.DPort) {
		return f.drop("in", "service-not-open", h)
	}
	return f.accept(key, &flow{inbound: true, port: h.DPort}, h, now, false)
}

// Out judges a packet from the local stack (or the engine itself) on its
// way to WireGuard.
func (f *Filter) Out(b []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := Parse(b)
	if !ok {
		return f.drop("out", unparsed(b), h)
	}
	if pol.Off {
		return true, ""
	}
	// The outbound half of "never forward": only what this device made may
	// leave. It is what stops transit when the OS forwards a packet back out
	// (IP forwarding is on by default on any Linux host running Docker).
	if h.Src != pol.Self {
		return f.drop("out", "transit-out", h)
	}
	if h.Fragment {
		return f.fragment("out", h, nil)
	}
	now := f.now()
	peer := pol.Peers[h.Dst]
	key := flowKey{h.Proto, netip.AddrPortFrom(h.Src, h.SPort), netip.AddrPortFrom(h.Dst, h.DPort)}
	if h.Proto == ProtoTCP && !h.TCPSYN && !h.TCPRst && f.wasCut(key, now) {
		// This device's stack goes on with a connection a policy change
		// cut (the reset at the cut did not reach it, or was off): it gets
		// one now, with the ACK it just sent, and the segment goes nowhere.
		return f.drop("out", CutTCPOut, h)
	}
	if peer == nil {
		for _, p := range pol.ExtraOut {
			if p.Contains(h.Dst) {
				return true, ""
			}
		}
		return f.drop("out", "not-a-visible-peer", h)
	}
	if h.Proto == ProtoICMP {
		switch h.ICMPType {
		case icmpEchoRequest:
			if peer.RefusedByPeer {
				return f.drop("out", "refused-by-peer", h)
			}
			return f.accept(key, &flow{}, h, now, true)
		case icmpEchoReply:
			if f.touch(key, h, now, true) {
				f.noteFrag(h, now)
				return true, ""
			}
			return f.drop("out", "icmp-reply-without-flow", h)
		case icmpUnreachable, icmpTimeExceeded, icmpParamProblem:
			in := h.Inner
			if in.Dst == pol.Self && in.Src == h.Dst && f.has(flowKey{in.Proto, netip.AddrPortFrom(in.Dst, in.DPort), netip.AddrPortFrom(in.Src, in.SPort)}, now) {
				return true, ""
			}
			return f.drop("out", "icmp-error-without-flow", h)
		}
		return f.drop("out", "icmp-type", h)
	}
	if f.touch(key, h, now, true) {
		f.noteFrag(h, now)
		return true, ""
	}
	if peer.RefusedByPeer {
		return f.drop("out", "refused-by-peer", h)
	}
	return f.accept(key, &flow{}, h, now, true)
}

func (f *Filter) accept(key flowKey, fl *flow, h Packet, now time.Time, out bool) (bool, string) {
	f.mu.Lock()
	peer := key.remote.Addr()
	if len(f.flows) >= maxFlows || fl.inbound && f.peerFlows[peer] >= maxPeerFlows {
		f.gcLocked(now)
	}
	if len(f.flows) >= maxFlows {
		f.mu.Unlock()
		return f.drop("both", "flow-table-full", h)
	}
	if old := f.flows[key]; old != nil {
		f.dropFlowLocked(key, old) // the same ports again: a new connection
	}
	if fl.inbound {
		if f.peerFlows[peer] >= maxPeerFlows {
			f.mu.Unlock()
			return f.drop("in", "peer-flows-full", h)
		}
		f.peerFlows[peer]++
	}
	fl.expires = now.Add(idleFor(h.Proto, h.TCPFinRst)).UnixNano()
	fl.since, fl.lastSeen = now.UnixNano(), now.UnixNano()
	fl.closing = h.TCPFinRst
	fl.noteSeq(h, out)
	f.flows[key] = fl
	delete(f.cut, key) // a new connection on the same ports
	f.mu.Unlock()
	f.noteFrag(h, now)
	return true, ""
}

func idleFor(proto uint8, closing bool) time.Duration {
	switch proto {
	case ProtoTCP:
		if closing {
			return tcpClosing
		}
		return tcpIdle
	case ProtoUDP:
		return udpIdle
	}
	return icmpIdle
}

// touch reports whether key is a live flow and extends it.
func (f *Filter) touch(key flowKey, h Packet, now time.Time, out bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flows[key]
	if fl == nil || fl.expires < now.UnixNano() {
		return false
	}
	if h.TCPFinRst {
		fl.closing = true
	}
	fl.noteSeq(h, out)
	fl.expires = now.Add(idleFor(key.proto, fl.closing)).UnixNano()
	fl.lastSeen = now.UnixNano()
	return true
}

func (f *Filter) has(key flowKey, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flows[key]
	return fl != nil && fl.expires >= now.UnixNano()
}

// noteFrag remembers an accepted first fragment, so the rest of the
// datagram may follow. Later fragments carry no ports, so they can only be
// judged by the first one.
func (f *Filter) noteFrag(h Packet, now time.Time) {
	if !h.FirstFragment {
		return
	}
	self := f.pol.Load().Self
	k := fragKey{h.Src, h.Dst, h.Proto, h.ID}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.frags[k]; ok {
		f.frags[k] = now.Add(fragIdle).UnixNano()
		return
	}
	full := func() bool {
		return len(f.frags) >= maxFrags || h.Src != self && f.peerFrags[h.Src] >= maxPeerFrags
	}
	if full() {
		for k, exp := range f.frags {
			if exp < now.UnixNano() {
				f.dropFragLocked(k)
			}
		}
	}
	if full() {
		return // its pieces are dropped: fragment-without-head
	}
	f.frags[k] = now.Add(fragIdle).UnixNano()
	f.peerFrags[h.Src]++
}

func (f *Filter) fragment(dir string, h Packet, b []byte) (bool, string) {
	// A TCP fragment that starts inside the TCP header could rewrite the
	// flags or ports the first fragment was judged by (RFC 1858).
	if h.Proto == ProtoTCP && h.FragOffset < 24 {
		return f.drop(dir, "tiny-fragment", h)
	}
	now := f.now()
	k := fragKey{h.Src, h.Dst, h.Proto, h.ID}
	f.mu.Lock()
	exp, ok := f.frags[k]
	if ok && exp >= now.UnixNano() {
		f.mu.Unlock()
		return true, ""
	}
	// Inbound, it may have overtaken its first piece (fragHold): kept.
	if b != nil && f.holdLocked(k, b, now) {
		f.mu.Unlock()
		return false, FragmentHeld
	}
	f.mu.Unlock()
	return f.drop(dir, "fragment-without-head", h)
}

type earlyPiece struct {
	b  []byte
	at int64 // unix nanoseconds
}

// holdLocked keeps a copy of b, a later piece of datagram k, for its first
// piece, within maxEarly, maxPeerEarly and maxEarlySize.
func (f *Filter) holdLocked(k fragKey, b []byte, now time.Time) bool {
	if len(b) > maxEarlySize {
		return false
	}
	full := func() bool { return f.nEarly >= maxEarly || f.peerEarly[k.src] >= maxPeerEarly }
	if full() {
		f.expireEarlyLocked(now)
		if full() {
			return false
		}
	}
	f.early[k] = append(f.early[k], earlyPiece{append([]byte(nil), b...), now.UnixNano()})
	f.nEarly++
	f.peerEarly[k.src]++
	f.held.Store(int32(f.nEarly))
	return true
}

// TakeEarly hands back the pieces held for b's datagram, if b is a first
// piece In let in: they go to the stack right after it, and In lets them
// in now. Nothing when nothing is held, which it tells without the lock.
func (f *Filter) TakeEarly(b []byte) [][]byte {
	if f.held.Load() == 0 {
		return nil
	}
	h, ok := Parse(b)
	if !ok || !h.FirstFragment {
		return nil
	}
	k := fragKey{h.Src, h.Dst, h.Proto, h.ID}
	now := f.now().UnixNano()
	var out [][]byte
	f.mu.Lock()
	// Unless the first piece went in unnoted (maxPeerFrags): then In would
	// refuse them, and they are dropped here.
	noted := f.frags[k] >= now
	for _, p := range f.early[k] {
		if noted && now-p.at <= int64(fragHold) {
			out = append(out, p.b)
		} else {
			f.drops["in:fragment-without-head"]++
		}
	}
	f.dropEarlyLocked(k)
	f.mu.Unlock()
	return out
}

func (f *Filter) dropEarlyLocked(k fragKey) {
	n := len(f.early[k])
	if n == 0 {
		return
	}
	delete(f.early, k)
	f.nEarly -= n
	if f.peerEarly[k.src] <= n {
		delete(f.peerEarly, k.src)
	} else {
		f.peerEarly[k.src] -= n
	}
	f.held.Store(int32(f.nEarly))
}

// expireEarlyLocked drops the pieces that waited fragHold for their first:
// it was lost on the way, or let through before they came without being
// noted (maxPeerFrags).
func (f *Filter) expireEarlyLocked(now time.Time) {
	for k, ps := range f.early {
		if now.UnixNano()-ps[0].at > int64(fragHold) {
			f.drops["in:fragment-without-head"] += uint64(len(ps))
			f.dropEarlyLocked(k)
		}
	}
}

// unparsed says why Parse refused b: IPv6, which the systems send on the
// interface (router solicitations and the like; there is only IPv4 inside
// the tunnel), an IPv4 protocol the filter does not know (IGMP's
// membership reports), or a malformed packet.
func unparsed(b []byte) string {
	switch {
	case len(b) > 0 && b[0]>>4 == 6:
		return "ipv6"
	case len(b) >= 20 && b[0]>>4 == 4 && b[9] != ProtoTCP && b[9] != ProtoUDP && b[9] != ProtoICMP:
		return "protocol"
	}
	return "malformed"
}

// GC forgets expired flows and fragments. The engine calls it periodically.
func (f *Filter) GC() {
	f.mu.Lock()
	f.gcLocked(f.now())
	f.mu.Unlock()
}

func (f *Filter) gcLocked(now time.Time) {
	for k, exp := range f.cut {
		if exp < now.UnixNano() {
			delete(f.cut, k)
		}
	}
	n := now.UnixNano()
	for k, fl := range f.flows {
		if fl.expires < n {
			f.dropFlowLocked(k, fl)
		}
	}
	for k, exp := range f.frags {
		if exp < n {
			f.dropFragLocked(k)
		}
	}
	f.expireEarlyLocked(now)
}

// dropFlowLocked forgets a flow, and its count against the peer that
// started it.
func (f *Filter) dropFlowLocked(k flowKey, fl *flow) {
	delete(f.flows, k)
	if fl.inbound {
		countDown(f.peerFlows, k.remote.Addr())
	}
}

func (f *Filter) dropFragLocked(k fragKey) {
	delete(f.frags, k)
	countDown(f.peerFrags, k.src)
}

func countDown(m map[netip.Addr]int, a netip.Addr) {
	if m[a] <= 1 {
		delete(m, a)
	} else {
		m[a]--
	}
}

// Flows is the number of tracked flows, for diagnostics.
func (f *Filter) Flows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.flows)
}

func (f *Filter) drop(dir, why string, h Packet) (bool, string) {
	f.mu.Lock()
	f.drops[dir+":"+why]++
	k := fmt.Sprintf("%s|%s|%s|%s", dir, why, h.Src, h.Dst)
	now := f.now()
	logIt := now.Sub(f.seen[k]) > 5*time.Second
	if logIt {
		f.seen[k] = now
		if len(f.seen) > 4096 {
			f.seen = map[string]time.Time{}
		}
	}
	f.mu.Unlock()
	if logIt {
		f.logf("[filter] drop %s %s: %s -> %s proto=%d dport=%d", dir, why, h.Src, h.Dst, h.Proto, h.DPort)
	}
	return false, why
}

// Drops counts dropped packets by "direction:reason".
func (f *Filter) Drops() map[string]uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := make(map[string]uint64, len(f.drops))
	for k, v := range f.drops {
		m[k] = v
	}
	return m
}
