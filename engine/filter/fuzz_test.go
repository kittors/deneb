package filter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"
)

// The filter under fuzzing (M7: implementation.md asks for IP fragments,
// IPv6 extension headers and ICMP errors). Each target checks the verdicts
// against a reference written apart from the filter's own parser, not only
// that nothing panics or reads past the end. `go test` runs the seeds and
// the inputs in testdata/fuzz (those that once found a problem); CI never
// fuzzes for long. By hand, one target at a time:
//
//	go test ./engine/filter -run '^$' -fuzz '^FuzzFragments$' -fuzztime 5m -parallel 2

// attackers are peers in "weekend", where ping is open: each can have the
// first pieces of its fragmented pings noted, and later pieces held.
var attackers = []netip.Addr{
	netip.MustParseAddr("10.77.0.20"),
	netip.MustParseAddr("10.77.0.21"),
	netip.MustParseAddr("10.77.0.22"),
}

// far is no device at all: a packet to it through this one is transit.
var far = netip.MustParseAddr("10.77.0.50")

func fuzzPolicy() *Policy {
	p := testPolicy()
	for i, a := range attackers {
		p.Peers[a] = &Peer{Label: fmt.Sprintf("attacker%d", i), Scopes: []string{"weekend"}}
	}
	return p
}

var fuzzEpoch = time.Unix(1_800_000_000, 0)

// refPacket is what the reference reads of an IPv4 packet, by itself.
type refPacket struct {
	v6       bool
	ok       bool // IPv4 the filter could judge: sound lengths, TCP, UDP or ICMP
	proto    uint8
	src, dst netip.Addr
	id       uint16
	more     bool
	off      int    // fragment offset, in bytes
	end      int    // where it ends in its datagram, header included
	l4       []byte // what follows the header, up to the total length
}

func readRef(b []byte) refPacket {
	var r refPacket
	if len(b) > 0 && b[0]>>4 == 6 {
		r.v6 = true
		return r
	}
	if len(b) < 20 || b[0]>>4 != 4 {
		return r
	}
	ihl := int(b[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if ihl < 20 || total < ihl || total > len(b) {
		return r
	}
	r.proto = b[9]
	r.id = binary.BigEndian.Uint16(b[4:6])
	r.src = netip.AddrFrom4([4]byte(b[12:16]))
	r.dst = netip.AddrFrom4([4]byte(b[16:20]))
	fl := binary.BigEndian.Uint16(b[6:8])
	r.more = fl&0x2000 != 0
	r.off = int(fl&0x1fff) * 8
	r.end = r.off + total
	r.l4 = b[ihl:total]
	r.ok = r.proto == ProtoTCP || r.proto == ProtoUDP || r.proto == ProtoICMP
	return r
}

func (r refPacket) key() fragKey { return fragKey{r.src, r.dst, r.proto, r.id} }

// judged runs packets through a filter the way the TUN does, checks every
// verdict against what holds for any packet in any state, and remembers
// the first pieces let through, which a later piece needs.
type judged struct {
	t      *testing.T
	f      *Filter
	now    *time.Time
	firsts map[fragKey]time.Time
}

func newJudged(t *testing.T, now *time.Time) *judged {
	f := New(func(string, ...any) {})
	f.now = func() time.Time { return *now }
	f.SetPolicy(fuzzPolicy())
	return &judged{t: t, f: f, now: now, firsts: map[fragKey]time.Time{}}
}

// in judges b from WireGuard. As in the TUN, a first piece let in brings
// back the pieces held for it, which are judged again; those let in are
// returned.
func (j *judged) in(b []byte) (bool, string, [][]byte) {
	ok, why := j.f.In(b)
	j.check("in", b, ok, why)
	var after [][]byte
	if ok {
		for _, p := range j.f.TakeEarly(b) {
			pok, pwhy := j.f.In(p)
			j.check("in", p, pok, pwhy)
			if pok {
				after = append(after, p)
			}
		}
	}
	return ok, why, after
}

func (j *judged) out(b []byte) (bool, string) {
	ok, why := j.f.Out(b)
	j.check("out", b, ok, why)
	return ok, why
}

// check fails the test if the filter let through, or held, what the
// reference finds it never may.
func (j *judged) check(dir string, b []byte, ok bool, why string) {
	t := j.t
	t.Helper()
	pol := j.f.Policy()
	r := readRef(b)
	if r.v6 {
		if ok || why != "ipv6" {
			t.Fatalf("%s: IPv6 judged (%v, %q): % x", dir, ok, why, b)
		}
		return
	}
	if why == FragmentHeld {
		if dir != "in" || ok || !r.ok || r.off == 0 || r.dst != pol.Self || pol.Peers[r.src] == nil || len(b) > maxEarlySize {
			t.Fatalf("%s: held what may not wait for its first piece: % x", dir, b)
		}
		return
	}
	if !ok {
		return
	}
	if !r.ok {
		t.Fatalf("%s: let through what the reference cannot read: % x", dir, b)
	}
	switch dir {
	case "in":
		if r.dst != pol.Self || pol.Peers[r.src] == nil {
			t.Fatalf("in: let through %s -> %s, not from a peer to this device", r.src, r.dst)
		}
	case "out":
		if r.src != pol.Self || pol.Peers[r.dst] == nil {
			t.Fatalf("out: let through %s -> %s, not from this device to a peer", r.src, r.dst)
		}
	}
	if r.end > 0xffff {
		t.Fatalf("%s: let through a piece ending at byte %d of its datagram, past 65535", dir, r.end)
	}
	if r.off > 0 {
		if r.proto == ProtoTCP && r.off < 24 {
			t.Fatalf("%s: let through a TCP piece at offset %d, inside the TCP header", dir, r.off)
		}
		at, seen := j.firsts[r.key()]
		if !seen || j.now.Sub(at) > fragIdle {
			t.Fatalf("%s: let through a later piece (id %d, offset %d) whose first piece it did not let through within fragIdle", dir, r.id, r.off)
		}
		return
	}
	need := 8
	if r.proto == ProtoTCP {
		need = 20
	}
	if len(r.l4) < need {
		t.Fatalf("%s: let through a packet whose L4 header (%d bytes) is cut to %d", dir, need, len(r.l4))
	}
	if r.proto == ProtoICMP && !slices.Contains([]uint8{icmpEchoReply, icmpEchoRequest, icmpUnreachable, icmpTimeExceeded, icmpParamProblem}, r.l4[0]) {
		t.Fatalf("%s: let through ICMP type %d", dir, r.l4[0])
	}
	if r.more {
		j.firsts[r.key()] = *j.now
	}
}

// datagram is a whole IPv4 datagram with l4len bytes after its header.
func datagram(proto uint8, src, dst netip.Addr, sport, dport uint16, tcpFlags, icmpType uint8, ipID uint16, l4len int) []byte {
	b := make([]byte, 20+l4len)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	binary.BigEndian.PutUint16(b[4:], ipID)
	b[8] = 64
	b[9] = proto
	s4, d4 := src.As4(), dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	l4 := b[20:]
	for i := range l4 {
		l4[i] = byte(i * 7)
	}
	switch proto {
	case ProtoTCP:
		binary.BigEndian.PutUint16(l4[0:], sport)
		binary.BigEndian.PutUint16(l4[2:], dport)
		l4[12], l4[13] = 5<<4, tcpFlags
	case ProtoUDP:
		binary.BigEndian.PutUint16(l4[0:], sport)
		binary.BigEndian.PutUint16(l4[2:], dport)
		binary.BigEndian.PutUint16(l4[4:], uint16(l4len))
	case ProtoICMP:
		l4[0], l4[1] = icmpType, 0
		binary.BigEndian.PutUint16(l4[4:], sport) // the echo identifier
	}
	return b
}

// fragment is one piece of datagram whole: data at offset off.
func fragment(whole, data []byte, off int, more bool) []byte {
	p := make([]byte, 20+len(data))
	copy(p, whole[:20])
	copy(p[20:], data)
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	fl := uint16(off/8) & 0x1fff
	if more {
		fl |= 0x2000
	}
	binary.BigEndian.PutUint16(p[6:], fl)
	return p
}

// split cuts a whole datagram the way a stack does: unit bytes of its
// payload (a multiple of 8) in each piece, all but the last saying more
// follow.
func split(whole []byte, unit int) [][]byte {
	payload := whole[20:]
	var out [][]byte
	for off := 0; off < len(payload); off += unit {
		end := min(off+unit, len(payload))
		out = append(out, fragment(whole, payload[off:end], off, end < len(payload)))
	}
	return out
}

// The kinds of datagram FuzzFragments sends. A reply first gets the
// packet that makes its flow, the other way.
const (
	fragUDP       = iota // a new UDP flow
	fragTCPSyn           // a new TCP connection
	fragPing             // an echo request
	fragPingReply        // an echo reply
	fragUDPReply         // a UDP reply
	fragTCPAck           // more of a TCP connection
	fragKinds
)

// FuzzFragments sends one datagram whole to one filter and in pieces to
// another, in the order, with the repeats and overlaps the input asks for.
// The pieces never get more through than the whole does: nothing of a
// datagram the whole of which is refused (another device's, a closed
// port's, a reply nobody asked for), no TCP piece starting inside its
// header, no piece ending past 65535 bytes (check). Split the way a stack
// splits, its first piece holding the L4 header and the rest within
// fragHold, in any order inbound and in order outbound, every piece gets
// through exactly when the whole does.
func FuzzFragments(f *testing.F) {
	// The unit tests' datagrams: TestFragments (lin's UDP to port 7 in
	// 1480-byte pieces, jie's to a port shut to it, a TCP piece inside its
	// header), TestFragmentedPing (1256-byte pieces, both ways, and a
	// reply nobody asked for), TestEarlyFragments (the head last).
	f.Add(uint8(0), uint8(fragUDP), uint16(7), uint16(2960), uint8(184), []byte{}, uint8(0))
	f.Add(uint8(1), uint8(fragUDP), uint16(27020), uint16(2960), uint8(184), []byte{1, 0}, uint8(0))
	f.Add(uint8(0), uint8(fragTCPSyn), uint16(80), uint16(1480), uint8(0), []byte{}, uint8(0))
	f.Add(uint8(0), uint8(fragTCPSyn), uint16(80), uint16(1480), uint8(2), []byte{1, 0}, uint8(0))
	f.Add(uint8(0), uint8(fragPing), uint16(0x4242), uint16(3000), uint8(156), []byte{}, uint8(0))
	f.Add(uint8(0), uint8(fragPing), uint16(0x4242), uint16(3000), uint8(156), []byte{}, uint8(2))
	f.Add(uint8(0), uint8(fragPingReply), uint16(0x4242), uint16(3000), uint8(156), []byte{}, uint8(0))
	f.Add(uint8(1), uint8(fragPingReply), uint16(300), uint16(3000), uint8(156), []byte{}, uint8(2))
	f.Add(uint8(0), uint8(fragUDP), uint16(7), uint16(3000), uint8(156), []byte{2, 1, 0}, uint8(0))
	f.Add(uint8(0), uint8(fragUDPReply), uint16(53), uint16(3000), uint8(156), []byte{0, 2, 2, 1}, uint8(0))
	f.Add(uint8(0), uint8(fragTCPAck), uint16(22), uint16(3000), uint8(156), []byte{0, 1, 0x82}, uint8(0))
	f.Add(uint8(0), uint8(fragUDP), uint16(7), uint16(3000), uint8(156), []byte{2, 1, 0}, uint8(8))
	f.Add(uint8(2), uint8(fragPing), uint16(1), uint16(3000), uint8(184), []byte{}, uint8(0))
	f.Add(uint8(0), uint8(fragUDP), uint16(7), uint16(3000), uint8(184), []byte{}, uint8(1))
	// A piece past 65535 bytes, both ways (what FuzzFragments found).
	f.Add(uint8(0), uint8(fragUDP), uint16(7), uint16(3000), uint8(156), []byte{}, uint8(4))
	f.Add(uint8(0), uint8(fragUDP), uint16(22), uint16(3051), uint8(156), []byte{0}, uint8(6))
	f.Fuzz(func(t *testing.T, from, kind uint8, port, size uint16, unit uint8, order []byte, opts uint8) {
		peers := []netip.Addr{lin, jie, other, attackers[0]}
		peer := peers[int(from)%len(peers)]
		kind %= fragKinds
		transit := opts&1 != 0 // to (or from) an address not this device's
		out := opts&2 != 0     // this device sends it
		huge := opts&4 != 0    // and a piece ending past 65535
		late := opts&8 != 0    // the head comes fragHold after the rest
		pieceLen := (int(unit)%200 + 1) * 8
		l4len := int(size)%9000 + 20

		var proto, flags, icmpType uint8
		sport, dport := uint16(5000), port
		var request []byte // what makes a reply's flow, the other way
		src, dst := peer, self
		if out {
			src, dst = self, peer
		}
		switch kind {
		case fragUDP:
			proto = ProtoUDP
		case fragTCPSyn:
			proto, flags = ProtoTCP, syn
		case fragPing:
			proto, icmpType, sport = ProtoICMP, icmpEchoRequest, port
		case fragPingReply:
			proto, icmpType, sport = ProtoICMP, icmpEchoReply, port
			request = echo(dst, src, icmpEchoRequest, port)
		case fragUDPReply:
			proto = ProtoUDP
			request = udp(dst, src, dport, sport)
		case fragTCPAck:
			proto, flags = ProtoTCP, ack
			request = tcp(dst, src, dport, sport, syn)
		}
		if transit {
			if out {
				src = far
			} else {
				dst = far
			}
		}
		whole := datagram(proto, src, dst, sport, dport, flags, icmpType, 0x1234, l4len)
		now := fuzzEpoch
		send := func(j *judged, b []byte) (bool, [][]byte) {
			if out {
				ok, _ := j.out(b)
				return ok, nil
			}
			ok, _, after := j.in(b)
			return ok, after
		}
		start := func() *judged {
			j := newJudged(t, &now)
			if request != nil { // its own way: inbound for an outbound reply
				if out {
					j.in(request)
				} else {
					j.out(request)
				}
			}
			return j
		}
		okWhole, _ := send(start(), whole)

		j := start()
		pieces := split(whole, pieceLen)
		type step struct {
			b   []byte
			idx int // the piece of pieces it is, -1 if none
		}
		var steps []step
		overlap := false
		if len(order) == 0 {
			for i, p := range pieces {
				steps = append(steps, step{p, i})
			}
		}
		for _, o := range order[:min(len(order), 64)] {
			i := int(o&0x7f) % len(pieces)
			if o&0x80 == 0 || i == 0 {
				steps = append(steps, step{pieces[i], i})
				continue
			}
			// The same piece, from 8 bytes earlier: over the end of the one
			// before.
			overlap = true
			r := readRef(pieces[i])
			off := r.off - 8
			steps = append(steps, step{fragment(whole, whole[20+off:20+r.off+len(r.l4)], off, r.more), -1})
		}
		if huge {
			steps = append(steps, step{fragment(whole, bytes.Repeat([]byte{0xee}, 16), 0xfff8, false), -1})
		}
		if late { // every head to the end, after fragHold
			var heads []step
			steps = slices.DeleteFunc(steps, func(s step) bool {
				if s.idx == 0 {
					heads = append(heads, s)
				}
				return s.idx == 0
			})
			steps = append(steps, heads...)
		}

		got := make([]bool, len(pieces))
		var through, heldEarly, early int
		sentHead := false
		for _, s := range steps {
			if late && s.idx == 0 && !sentHead {
				now = now.Add(fragHold + time.Second)
			}
			if s.idx == 0 {
				sentHead = true
			} else if !sentHead {
				early++
				if len(s.b) <= maxEarlySize {
					heldEarly++
				}
			}
			ok, after := send(j, s.b)
			if ok {
				through++
				if s.idx >= 0 {
					got[s.idx] = true
				}
			}
			for _, a := range after {
				through++
				for i, p := range pieces {
					if bytes.Equal(a, p) {
						got[i] = true
					}
				}
			}
		}
		if !okWhole && through > 0 {
			t.Fatalf("whole it is refused, yet %d of its pieces got through", through)
		}
		// As a stack splits: the L4 header in the first piece, nothing
		// overlapping, every piece sent, the head within fragHold; inbound,
		// what comes before the head small enough to be held, and no more
		// of it than may be; outbound, the head first.
		stackLike := len(pieces) > 1 && !huge && !late && !overlap &&
			(proto != ProtoTCP || pieceLen >= 24) &&
			(out && early == 0 || !out && heldEarly == early && early <= maxPeerEarly)
		for i := range pieces {
			if !slices.ContainsFunc(steps, func(s step) bool { return s.idx == i }) {
				stackLike = false
			}
		}
		if okWhole && stackLike {
			for i, g := range got {
				if !g {
					t.Fatalf("whole it gets through, yet piece %d of %d (offset %d) did not", i, len(pieces), i*pieceLen)
				}
			}
		}
	})
}

// FuzzFragmentTable: up to three peers fill the fragment table with the
// first pieces of pings (ping is open to every peer), each its own
// datagram, and the store of early pieces with later pieces of datagrams
// that never start. Then lin sends one datagram in two pieces. However the
// others went at it, none of them holds more than a quarter of the table,
// or more than maxPeerEarly pieces, and lin's datagram gets through: one
// peer cannot fill another's table.
func FuzzFragmentTable(f *testing.F) {
	f.Add(uint16(1100), uint16(0), uint16(0), uint16(70), uint16(0), uint8(0))
	f.Add(uint16(1100), uint16(1100), uint16(1100), uint16(70), uint16(30000), uint8(1))
	f.Add(uint16(2000), uint16(5), uint16(1500), uint16(0), uint16(65000), uint8(1))
	f.Fuzz(func(t *testing.T, n0, n1, n2, early, idBase uint16, opts uint8) {
		now := fuzzEpoch
		j := newJudged(t, &now)
		for i, n := range []uint16{n0, n1, n2} {
			a := attackers[i]
			for k := range int(n) % (2 * maxPeerFrags) {
				ping := datagram(ProtoICMP, a, self, 1, 1, 0, icmpEchoRequest, idBase+uint16(k), 1200)
				j.in(fragment(ping, ping[20:20+56], 0, true))
			}
			for k := range int(early) % (2 * maxPeerEarly) {
				never := datagram(ProtoICMP, a, self, 1, 1, 0, icmpEchoRequest, idBase+0x8000+uint16(k), 1200)
				j.in(fragment(never, never[20+1176:], 1176, false))
			}
		}
		d := datagram(ProtoUDP, lin, self, 5000, 7, 0, 0, idBase^0x5555, 2000)
		pieces := split(d, 1480)
		if opts&1 != 0 {
			pieces[0], pieces[1] = pieces[1], pieces[0]
		}
		through := 0
		for _, p := range pieces {
			ok, _, after := j.in(p)
			if ok {
				through++
			}
			through += len(after)
		}
		if through != 2 {
			t.Fatalf("lin's datagram: %d of its 2 pieces got through", through)
		}
		fl := j.f
		fl.mu.Lock()
		defer fl.mu.Unlock()
		if len(fl.frags) > maxFrags || fl.nEarly > maxEarly {
			t.Fatalf("%d fragments noted, %d pieces held", len(fl.frags), fl.nEarly)
		}
		for _, a := range attackers {
			if fl.peerFrags[a] > maxPeerFrags || fl.peerEarly[a] > maxPeerEarly {
				t.Fatalf("%s has %d fragments noted, %d pieces held", a, fl.peerFrags[a], fl.peerEarly[a])
			}
		}
	})
}

// ipv6Packet is an IPv6 packet from a peer's ULA to this device's, with
// the extension headers chain names: pairs of a header type (hop-by-hop 0,
// routing 43, fragment 44, destination options 60, or any other) and its
// length byte. last's header ends it: TCP 6, UDP 17, ICMPv6 58, or
// whatever it names. The payload length says plen unless mode&1 (then the
// true one); mode&2 cuts the packet anywhere, a header's middle included.
func ipv6Packet(chain []byte, last uint8, plen, cut uint16, mode uint8) []byte {
	if len(chain) > 128 {
		chain = chain[:128] // a chain of 64 headers
	}
	b := make([]byte, 40)
	b[0] = 0x60
	b[6] = last
	if len(chain) >= 2 {
		b[6] = chain[0]
	}
	b[7] = 64
	copy(b[8:24], netip.MustParseAddr("fd00::2").AsSlice())
	copy(b[24:40], netip.MustParseAddr("fd00::1").AsSlice())
	for i := 0; i+1 < len(chain); i += 2 {
		typ, n := chain[i], chain[i+1]
		next := last
		if i+3 < len(chain) {
			next = chain[i+2]
		}
		var h []byte
		if typ == 44 { // fragment: next, reserved, offset and M, identification
			h = make([]byte, 8)
			h[0] = next
			binary.BigEndian.PutUint16(h[2:], uint16(n)<<3|1)
			binary.BigEndian.PutUint32(h[4:], 0x01020304)
		} else {
			h = make([]byte, (int(n)+1)*8)
			h[0], h[1] = next, n
			for k := 2; k < len(h); k++ {
				h[k] = byte(k)
			}
			if typ == 43 { // routing: type 0, one segment left
				h[2], h[3] = 0, 1
			}
		}
		b = append(b, h...)
	}
	switch last {
	case ProtoTCP:
		l4 := make([]byte, 20)
		binary.BigEndian.PutUint16(l4[0:], 5000)
		binary.BigEndian.PutUint16(l4[2:], 80)
		l4[12], l4[13] = 5<<4, syn
		b = append(b, l4...)
	case ProtoUDP:
		b = append(b, 0x13, 0x88, 0, 7, 0, 8, 0, 0)
	case 58: // ICMPv6 echo request
		b = append(b, 128, 0, 0, 0, 0x42, 0x42, 0, 1)
	}
	pl := plen
	if mode&1 != 0 {
		pl = uint16(len(b) - 40)
	}
	binary.BigEndian.PutUint16(b[4:], pl)
	if mode&2 != 0 {
		b = b[:int(cut)%len(b)+1] // never empty: the version stays
	}
	return b
}

// FuzzIPv6ExtensionHeaders: only IPv4 travels inside the tunnel, so the
// filter drops every IPv6 packet, both ways, whatever its extension
// headers: a long chain of hop-by-hop, routing, fragment and destination
// options headers, lengths that point past the end, a chain cut in the
// middle of a header. None gets through, nothing reads past the end, and
// nothing of it is remembered: no flow, no fragment, no piece held, and no
// reset made for it.
func FuzzIPv6ExtensionHeaders(f *testing.F) {
	f.Add([]byte{0, 0, 43, 0, 44, 0, 60, 0}, uint8(ProtoUDP), uint16(0), uint16(0), uint8(1))
	f.Add([]byte{60, 255}, uint8(58), uint16(0xffff), uint16(0), uint8(0))
	f.Add([]byte{0, 0, 0, 0, 0, 0}, uint8(ProtoTCP), uint16(0), uint16(45), uint8(3))
	f.Add(bytes.Repeat([]byte{44, 0}, 64), uint8(ProtoUDP), uint16(0), uint16(0), uint8(1))
	f.Add([]byte{43, 2, 44, 255}, uint8(ProtoTCP), uint16(12), uint16(70), uint8(2))
	f.Fuzz(func(t *testing.T, chain []byte, last uint8, plen, cut uint16, mode uint8) {
		b := ipv6Packet(chain, last, plen, cut, mode)
		now := fuzzEpoch
		j := newJudged(t, &now)
		setupFlows(j)
		state := func() string {
			j.f.mu.Lock()
			defer j.f.mu.Unlock()
			return fmt.Sprint(len(j.f.flows), len(j.f.frags), j.f.nEarly, len(j.f.cut))
		}
		before := state()
		for _, out := range []bool{false, true} {
			var ok bool
			var why string
			if out {
				ok, why = j.out(b)
			} else {
				ok, why, _ = j.in(b)
			}
			if ok || why != "ipv6" {
				t.Fatalf("out %v: (%v, %q), want dropped as ipv6", out, ok, why)
			}
		}
		if j.f.TakeEarly(b) != nil || ResetFor(b) != nil {
			t.Fatal("an IPv6 packet handed back pieces, or drew a reset")
		}
		if after := state(); after != before {
			t.Fatalf("the filter's tables went from %s to %s", before, after)
		}
	})
}

// refFlow is a flow as the reference keeps it: this device's port, the
// peer and its port.
type refFlow struct {
	proto    uint8
	selfPort uint16
	peer     netip.Addr
	peerPort uint16
}

// setupFlows makes a few flows both ways, and returns them as the
// reference keeps them.
func setupFlows(j *judged) map[refFlow]bool {
	j.t.Helper()
	steps := []struct {
		out  bool
		b    []byte
		flow refFlow
	}{
		{true, udp(self, jie, 6000, 9999), refFlow{ProtoUDP, 6000, jie, 9999}},
		{false, udp(lin, self, 5000, 7), refFlow{ProtoUDP, 7, lin, 5000}},
		{true, tcp(self, jie, 40000, 80, syn), refFlow{ProtoTCP, 40000, jie, 80}},
		{true, echo(self, lin, icmpEchoRequest, 0x4242), refFlow{ProtoICMP, 0x4242, lin, 0x4242}},
		{false, echo(jie, self, icmpEchoRequest, 7), refFlow{ProtoICMP, 7, jie, 7}},
		{false, echo(attackers[0], self, icmpEchoRequest, 9), refFlow{ProtoICMP, 9, attackers[0], 9}},
	}
	live := map[refFlow]bool{}
	for _, s := range steps {
		var ok bool
		if s.out {
			ok, _ = j.out(s.b)
		} else {
			ok, _, _ = j.in(s.b)
		}
		if !ok {
			j.t.Fatalf("setup refused % x", s.b)
		}
		live[s.flow] = true
	}
	return live
}

// icmpError is an ICMP error from src to dst of type typ quoting q.
func icmpError(src, dst netip.Addr, typ, code uint8, q []byte) []byte {
	b := make([]byte, 28+len(q))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	b[8] = 64
	b[9] = ProtoICMP
	s4, d4 := src.As4(), dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	b[20], b[21] = typ, code
	copy(b[28:], q)
	return b
}

// refICMPError is the reference's verdict on an ICMP error from src to
// dst, of type typ, quoting q: through only when it is about a live flow
// this device has with the very peer at the other end. Inbound, the
// quoted packet went from this device to the peer that sends the error;
// outbound, from the peer the error goes to, to this device.
func refICMPError(out bool, src, dst netip.Addr, typ uint8, q []byte, live map[refFlow]bool) bool {
	peer := src
	if out {
		peer = dst
	}
	if out && src != self || !out && dst != self || fuzzPolicy().Peers[peer] == nil {
		return false
	}
	if typ != icmpUnreachable && typ != icmpTimeExceeded && typ != icmpParamProblem {
		return false
	}
	if len(q) < 20 || q[0]>>4 != 4 {
		return false
	}
	ihl := int(q[0]&0x0f) * 4
	if ihl < 20 || len(q) < ihl+8 {
		return false
	}
	if binary.BigEndian.Uint16(q[6:8])&0x1fff != 0 {
		return false // a later piece: no ports to tell the flow by
	}
	qs, qd := netip.AddrFrom4([4]byte(q[12:16])), netip.AddrFrom4([4]byte(q[16:20]))
	l4 := q[ihl:]
	var sp, dp uint16
	switch q[9] {
	case ProtoTCP, ProtoUDP:
		sp, dp = binary.BigEndian.Uint16(l4[0:2]), binary.BigEndian.Uint16(l4[2:4])
	case ProtoICMP:
		switch l4[0] {
		case icmpEchoRequest, icmpEchoReply:
			sp = binary.BigEndian.Uint16(l4[4:6])
			dp = sp
		case icmpUnreachable, icmpTimeExceeded, icmpParamProblem:
			return false // an error about an error
		}
	default:
		return false
	}
	if out {
		return qd == self && qs == peer && live[refFlow{q[9], dp, peer, sp}]
	}
	return qs == self && qd == peer && live[refFlow{q[9], sp, peer, dp}]
}

// FuzzICMPErrors: ICMP errors (and other ICMP types) quoting a packet, both
// ways, after a few flows were made. The filter's verdict must be the
// reference's, every time: an error about a live flow with the peer at the
// other end gets through; one whose quoted packet is cut short, is a later
// piece, is itself an error, belongs to another peer's flow or to none, or
// comes from or goes to anyone but a peer, does not.
func FuzzICMPErrors(f *testing.F) {
	// TestICMPErrorsNeedAFlow's: jie's unreachable for this device's UDP
	// (through), a forged one about another port, lin's about the flow
	// with jie, and this device's unreachable for lin's datagram.
	f.Add(false, uint8(0), uint8(3), uint8(2), uint8(0), uint8(2), uint8(1), uint16(6000), uint16(9999), uint8(28), uint16(0), uint8(0), uint8(0))
	f.Add(false, uint8(0), uint8(3), uint8(2), uint8(0), uint8(2), uint8(1), uint16(6001), uint16(9999), uint8(28), uint16(0), uint8(0), uint8(0))
	f.Add(false, uint8(0), uint8(3), uint8(1), uint8(0), uint8(2), uint8(1), uint16(6000), uint16(9999), uint8(28), uint16(0), uint8(0), uint8(0))
	f.Add(true, uint8(0), uint8(3), uint8(1), uint8(1), uint8(0), uint8(1), uint16(5000), uint16(7), uint8(28), uint16(0), uint8(0), uint8(0))
	// Time exceeded for this device's ping to lin, and for its TCP to jie
	// with the quoted packet cut short; parameter problem about an error.
	f.Add(false, uint8(1), uint8(0), uint8(1), uint8(0), uint8(1), uint8(2), uint16(0x4242), uint16(0x4242), uint8(28), uint16(0), uint8(8), uint8(0))
	f.Add(false, uint8(1), uint8(0), uint8(2), uint8(0), uint8(2), uint8(0), uint16(40000), uint16(80), uint8(24), uint16(0), uint8(0), uint8(0))
	f.Add(false, uint8(2), uint8(0), uint8(1), uint8(0), uint8(1), uint8(2), uint16(0), uint16(0), uint8(28), uint16(0), uint8(3), uint8(0))
	// A quoted later piece; a quoted header with options.
	f.Add(false, uint8(0), uint8(3), uint8(2), uint8(0), uint8(2), uint8(1), uint16(6000), uint16(9999), uint8(28), uint16(185), uint8(0), uint8(0))
	f.Add(false, uint8(0), uint8(3), uint8(2), uint8(0), uint8(2), uint8(1), uint16(6000), uint16(9999), uint8(40), uint16(0), uint8(0), uint8(1))
	f.Fuzz(func(t *testing.T, out bool, typ, code, sender, qsrc, qdst, qproto uint8, qsport, qdport uint16, qlen uint8, qfrag uint16, qtype, qopts uint8) {
		addrs := []netip.Addr{self, lin, jie, other, attackers[0], far}
		pick := func(i uint8) netip.Addr { return addrs[int(i)%len(addrs)] }
		now := fuzzEpoch
		j := newJudged(t, &now)
		live := setupFlows(j)

		// The quoted packet: a header (with up to two words of options),
		// then the start of its TCP, UDP, ICMP or other payload, cut to qlen.
		ihl := 20 + 4*(int(qopts)%3)
		q := make([]byte, ihl+32)
		q[0] = 0x40 | byte(ihl/4)
		binary.BigEndian.PutUint16(q[2:], 1500)
		binary.BigEndian.PutUint16(q[4:], 0x7777)
		binary.BigEndian.PutUint16(q[6:], qfrag)
		q[8] = 64
		q[9] = []uint8{ProtoTCP, ProtoUDP, ProtoICMP, 2}[int(qproto)%4]
		s4, d4 := pick(qsrc).As4(), pick(qdst).As4()
		copy(q[12:16], s4[:])
		copy(q[16:20], d4[:])
		l4 := q[ihl:]
		switch q[9] {
		case ProtoICMP:
			l4[0] = []uint8{icmpEchoRequest, icmpEchoReply, 13, icmpUnreachable, icmpTimeExceeded, icmpParamProblem, 5, 17}[int(qtype)%8]
			binary.BigEndian.PutUint16(l4[4:], qsport)
		default:
			binary.BigEndian.PutUint16(l4[0:], qsport)
			binary.BigEndian.PutUint16(l4[2:], qdport)
		}
		q = q[:min(len(q), int(qlen)%(len(q)+1))]

		et := []uint8{icmpUnreachable, icmpTimeExceeded, icmpParamProblem, 5, 13}[int(typ)%5]
		src, dst := pick(sender), self
		if out {
			src, dst = self, pick(sender)
		}
		b := icmpError(src, dst, et, code, q)
		var ok bool
		var why string
		if out {
			ok, why = j.out(b)
		} else {
			ok, why, _ = j.in(b)
		}
		if want := refICMPError(out, src, dst, et, q, live); ok != want {
			t.Fatalf("out %v, type %d from %s to %s quoting % x: (%v, %q), the reference says %v", out, et, src, dst, q, ok, why, want)
		}
	})
}

// FuzzPackets: any bytes at all, both ways, to a filter with a few flows
// and one datagram started (lin's, IP ID 77): whatever gets through holds
// up by the reference (check), nothing reads past the end, and a reset
// made for it answers it.
func FuzzPackets(f *testing.F) {
	for _, b := range [][]byte{
		udp(lin, self, 5000, 7),
		tcp(lin, self, 5000, 80, syn),
		tcp(jie, self, 9999, 6000, ack),
		echo(jie, self, icmpEchoRequest, 7),
		echo(lin, self, icmpEchoReply, 0x4242),
		pk{proto: ProtoICMP, src: jie, dst: self, icmpType: icmpUnreachable, payload: udp(self, jie, 6000, 9999)[:28]}.bytes(),
		pk{proto: ProtoICMP, src: lin, dst: self, icmpType: icmpUnreachable, payload: udp(self, jie, 6000, 9999)[:28]}.bytes(),
		pk{proto: ProtoUDP, src: lin, dst: self, id: 77, fragOff: 1480}.bytes(),
		pk{proto: ProtoUDP, src: lin, dst: self, id: 78, fragOff: 1480}.bytes(),
		pk{proto: ProtoTCP, src: lin, dst: self, id: 5, fragOff: 8}.bytes(),
		pk{proto: ProtoICMP, src: lin, dst: self, icmpType: icmpEchoReply, sport: 0x4242, id: 200, more: true}.bytes(),
		udp(self, lin, 7, 5000),
		ipv6Packet([]byte{0, 0, 44, 0}, ProtoUDP, 0, 0, 1),
		// What FuzzPackets found: a later piece of a protocol the filter
		// never lets in, which it held for its first piece. And a piece of
		// lin's datagram reaching past 65535 bytes.
		pk{proto: 48, src: lin, dst: self, id: 77, fragOff: 1480}.bytes(),
		pk{proto: ProtoUDP, src: lin, dst: self, id: 77, fragOff: 65528, payload: make([]byte, 8)}.bytes(),
	} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		now := fuzzEpoch
		j := newJudged(t, &now)
		setupFlows(j)
		head := pk{proto: ProtoUDP, src: lin, dst: self, sport: 5000, dport: 7, id: 77, more: true}.bytes()
		if ok, _, _ := j.in(head); !ok {
			t.Fatal("setup: lin's first piece refused")
		}
		j.in(b)
		j.out(b)
		j.f.TakeEarly(b)
		if r := ResetFor(b); r != nil {
			if len(r) != 40 || r[9] != ProtoTCP || r[33]&0x04 == 0 || !bytes.Equal(r[12:16], b[16:20]) || !bytes.Equal(r[16:20], b[12:16]) {
				t.Fatalf("a reset that does not answer it: % x", r)
			}
		}
	})
}
