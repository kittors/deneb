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
	// Scopes are the active scopes shared with the peer: neither paused
	// here nor by the peer.
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

	// Test builds only (engine/testhooks): let this device send to
	// addresses that are not peers, or turn the filter off, to stage the
	// attacks of multi-lan-tech §6.4.
	ExtraOut []netip.Prefix
	Off      bool
}

func (p *Policy) allows(peer *Peer, proto uint8, port uint16) bool {
	for _, s := range peer.Scopes {
		for _, r := range p.Open[s] {
			if r.Proto == proto && r.First <= port && port <= r.Last {
				return true
			}
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
	fragIdle   = 30 * time.Second
	maxFlows   = 1 << 16
	maxFrags   = 1 << 12
)

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
}

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
	drops map[string]uint64
	seen  map[string]time.Time // rate-limits drop logging
	logf  func(format string, args ...any)
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
		drops: map[string]uint64{},
		seen:  map[string]time.Time{},
		logf:  logf,
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
	defer f.mu.Unlock()
	now := f.now().UnixNano()
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
			delete(f.flows, k)
		}
	}
	return uses(cut)
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

// In judges a packet from WireGuard on its way to the local stack.
// WireGuard has already checked that the source address belongs to the
// peer that sent it (layer 4).
func (f *Filter) In(b []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := Parse(b)
	if !ok {
		return f.drop("in", "malformed", h)
	}
	if pol.Off {
		return true, ""
	}
	// Never act as a router: a packet not for this device is someone trying
	// to go through it, for instance into another scope.
	if h.Dst != pol.Self {
		return f.drop("in", "transit-in", h)
	}
	peer := pol.Peers[h.Src]
	if peer == nil {
		return f.drop("in", "not-a-visible-peer", h)
	}
	if h.Fragment {
		return f.fragment("in", h)
	}
	now := f.now()
	key := flowKey{h.Proto, netip.AddrPortFrom(h.Dst, h.DPort), netip.AddrPortFrom(h.Src, h.SPort)}
	if h.Proto == ProtoICMP {
		switch h.ICMPType {
		case icmpEchoRequest:
			if peer.RefusedByMe {
				return f.drop("in", "refused", h)
			}
			return f.accept(key, &flow{inbound: true}, h, now)
		case icmpEchoReply:
			if f.touch(key, h, now) {
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
	if f.touch(key, h, now) {
		f.noteFrag(h, now)
		return true, "" // a reply, or more of an accepted flow
	}
	if h.Proto == ProtoTCP && !h.TCPSYN {
		return f.drop("in", "tcp-without-flow", h)
	}
	if peer.RefusedByMe {
		return f.drop("in", "refused", h)
	}
	if !pol.allows(peer, h.Proto, h.DPort) {
		return f.drop("in", "service-not-open", h)
	}
	return f.accept(key, &flow{inbound: true, port: h.DPort}, h, now)
}

// Out judges a packet from the local stack (or the engine itself) on its
// way to WireGuard.
func (f *Filter) Out(b []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := Parse(b)
	if !ok {
		return f.drop("out", "malformed", h)
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
		return f.fragment("out", h)
	}
	now := f.now()
	peer := pol.Peers[h.Dst]
	key := flowKey{h.Proto, netip.AddrPortFrom(h.Src, h.SPort), netip.AddrPortFrom(h.Dst, h.DPort)}
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
			return f.accept(key, &flow{}, h, now)
		case icmpEchoReply:
			if f.touch(key, h, now) {
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
	if f.touch(key, h, now) {
		f.noteFrag(h, now)
		return true, ""
	}
	if peer.RefusedByPeer {
		return f.drop("out", "refused-by-peer", h)
	}
	return f.accept(key, &flow{}, h, now)
}

func (f *Filter) accept(key flowKey, fl *flow, h Packet, now time.Time) (bool, string) {
	f.mu.Lock()
	if len(f.flows) >= maxFlows {
		f.gcLocked(now)
	}
	if len(f.flows) >= maxFlows {
		f.mu.Unlock()
		return f.drop("both", "flow-table-full", h)
	}
	fl.expires = now.Add(idleFor(h.Proto, h.TCPFinRst)).UnixNano()
	fl.since, fl.lastSeen = now.UnixNano(), now.UnixNano()
	fl.closing = h.TCPFinRst
	f.flows[key] = fl
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
func (f *Filter) touch(key flowKey, h Packet, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flows[key]
	if fl == nil || fl.expires < now.UnixNano() {
		return false
	}
	if h.TCPFinRst {
		fl.closing = true
	}
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
	f.mu.Lock()
	if len(f.frags) >= maxFrags {
		for k, exp := range f.frags {
			if exp < now.UnixNano() {
				delete(f.frags, k)
			}
		}
	}
	if len(f.frags) < maxFrags {
		f.frags[fragKey{h.Src, h.Dst, h.Proto, h.ID}] = now.Add(fragIdle).UnixNano()
	}
	f.mu.Unlock()
}

func (f *Filter) fragment(dir string, h Packet) (bool, string) {
	// A TCP fragment that starts inside the TCP header could rewrite the
	// flags or ports the first fragment was judged by (RFC 1858).
	if h.Proto == ProtoTCP && h.FragOffset < 24 {
		return f.drop(dir, "tiny-fragment", h)
	}
	now := f.now()
	f.mu.Lock()
	exp, ok := f.frags[fragKey{h.Src, h.Dst, h.Proto, h.ID}]
	f.mu.Unlock()
	if ok && exp >= now.UnixNano() {
		return true, ""
	}
	return f.drop(dir, "fragment-without-head", h)
}

// GC forgets expired flows and fragments. The engine calls it periodically.
func (f *Filter) GC() {
	f.mu.Lock()
	f.gcLocked(f.now())
	f.mu.Unlock()
}

func (f *Filter) gcLocked(now time.Time) {
	n := now.UnixNano()
	for k, fl := range f.flows {
		if fl.expires < n {
			delete(f.flows, k)
		}
	}
	for k, exp := range f.frags {
		if exp < n {
			delete(f.frags, k)
		}
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
