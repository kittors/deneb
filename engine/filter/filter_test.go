package filter

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

// The filter is what keeps two scopes apart on a device that is in both, so
// its rules get table tests independent of any network. The cases start
// from the prototype's (poc/multi-lan/filter_test.go).

var (
	self  = netip.MustParseAddr("10.77.0.1")
	lin   = netip.MustParseAddr("10.77.0.2") // shares "studio" with self
	jie   = netip.MustParseAddr("10.77.0.3") // shares "weekend" with self
	other = netip.MustParseAddr("10.77.0.9") // shares nothing
)

func testPolicy() *Policy {
	return &Policy{
		Self: self,
		Peers: map[netip.Addr]*Peer{
			lin: {Label: "box.lin", Scopes: []string{"studio"}},
			jie: {Label: "box.jie", Scopes: []string{"weekend"}},
		},
		Open: map[string][]Rule{
			"studio":  {{ProtoUDP, 7, 7}, {ProtoTCP, 80, 80}, {ProtoUDP, 27015, 27030}},
			"weekend": {{ProtoUDP, 7, 7}},
		},
	}
}

func newTestFilter() *Filter {
	f := New(func(string, ...any) {})
	f.SetPolicy(testPolicy())
	return f
}

type pk struct {
	proto        uint8
	src, dst     netip.Addr
	sport, dport uint16
	flags        byte  // TCP flags
	icmpType     uint8 // ICMP
	id           uint16
	more         bool   // more fragments
	fragOff      uint16 // in bytes, multiple of 8
	payload      []byte // ICMP errors: the quoted packet
}

const (
	syn = 0x02
	ack = 0x10
	fin = 0x01
)

func (p pk) bytes() []byte {
	l4 := 20
	switch p.proto {
	case ProtoUDP, ProtoICMP:
		l4 = 8
	}
	if p.fragOff > 0 {
		l4 = 8 // a later fragment: arbitrary data
	}
	b := make([]byte, 20+l4+len(p.payload))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	binary.BigEndian.PutUint16(b[4:], p.id)
	frag := p.fragOff / 8
	if p.more {
		frag |= 0x2000
	}
	binary.BigEndian.PutUint16(b[6:], frag)
	b[8] = 64
	b[9] = p.proto
	s4, d4 := p.src.As4(), p.dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	if p.fragOff > 0 {
		return b
	}
	switch p.proto {
	case ProtoTCP:
		binary.BigEndian.PutUint16(b[20:], p.sport)
		binary.BigEndian.PutUint16(b[22:], p.dport)
		b[20+12] = 5 << 4
		b[20+13] = p.flags
	case ProtoUDP:
		binary.BigEndian.PutUint16(b[20:], p.sport)
		binary.BigEndian.PutUint16(b[22:], p.dport)
	case ProtoICMP:
		b[20] = p.icmpType
		binary.BigEndian.PutUint16(b[24:], p.sport)
	}
	copy(b[20+l4:], p.payload)
	return b
}

func tcp(src, dst netip.Addr, sport, dport uint16, flags byte) []byte {
	return pk{proto: ProtoTCP, src: src, dst: dst, sport: sport, dport: dport, flags: flags}.bytes()
}
func udp(src, dst netip.Addr, sport, dport uint16) []byte {
	return pk{proto: ProtoUDP, src: src, dst: dst, sport: sport, dport: dport}.bytes()
}
func echo(src, dst netip.Addr, typ uint8, id uint16) []byte {
	return pk{proto: ProtoICMP, src: src, dst: dst, icmpType: typ, sport: id}.bytes()
}

func TestInbound(t *testing.T) {
	cases := []struct {
		name string
		p    []byte
		ok   bool
		why  string
	}{
		{"studio member opens a service opened to studio", tcp(lin, self, 5000, 80, syn), true, ""},
		{"weekend member, service opened only to studio", tcp(jie, self, 5000, 80, syn), false, "service-not-open"},
		{"a port range opened to studio", udp(lin, self, 5000, 27020), true, ""},
		{"just outside the range", udp(lin, self, 5000, 27031), false, "service-not-open"},
		{"weekend member pings: ping is always open", echo(jie, self, icmpEchoRequest, 9), true, ""},
		{"an echo reply nobody asked for", echo(jie, self, icmpEchoReply, 9), false, "icmp-reply-without-flow"},
		{"other ICMP types", pk{proto: ProtoICMP, src: jie, dst: self, icmpType: 13}.bytes(), false, "icmp-type"},
		{"stranger", echo(other, self, icmpEchoRequest, 9), false, "not-a-visible-peer"},
		{"studio member aims at a weekend device through us", tcp(lin, jie, 5000, 80, syn), false, "transit-in"},
		{"weekend member aims at a studio device through us", udp(jie, lin, 5000, 7), false, "transit-in"},
		{"mid-stream TCP without a flow, from a peer that may connect: reset", tcp(lin, self, 5000, 80, ack), false, StaleTCP},
		{"SYN+ACK is not a new connection", tcp(lin, self, 5000, 80, syn|ack), false, StaleTCP},
		{"mid-stream TCP to a service not open to it: silence", tcp(jie, self, 5000, 80, ack), false, "tcp-without-flow"},
		{"a reset is never answered", tcp(lin, self, 5000, 80, 0x04|ack), false, "tcp-without-flow"},
		{"truncated", tcp(lin, self, 5000, 80, syn)[:30], false, "malformed"},
		{"IPv6", append([]byte{0x60}, make([]byte, 39)...), false, "malformed"},
	}
	for _, c := range cases {
		f := newTestFilter()
		ok, why := f.In(c.p)
		if ok != c.ok || why != c.why {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, ok, why, c.ok, c.why)
		}
	}
}

func TestOutbound(t *testing.T) {
	f := newTestFilter()
	if ok, why := f.Out(tcp(self, jie, 6000, 22, syn)); !ok {
		t.Fatalf("own connection to a visible peer refused: %s", why)
	}
	// Someone else's source: the OS is forwarding. It must not leave, even
	// with IP forwarding on.
	if ok, why := f.Out(tcp(lin, jie, 5000, 80, syn)); ok || why != "transit-out" {
		t.Fatalf("forwarded packet left: (%v, %q)", ok, why)
	}
	if ok, why := f.Out(tcp(self, other, 6000, 80, syn)); ok || why != "not-a-visible-peer" {
		t.Fatalf("sent to a non-peer: (%v, %q)", ok, why)
	}
}

func TestRepliesFollowFlows(t *testing.T) {
	f := newTestFilter()
	// We connect to jie:22, which jie never opened to us: the reply must
	// still come back, because this device started the flow.
	if ok, _ := f.Out(tcp(self, jie, 6000, 22, syn)); !ok {
		t.Fatal("outbound refused")
	}
	if ok, why := f.In(tcp(jie, self, 22, 6000, syn|ack)); !ok {
		t.Fatalf("reply refused: %s", why)
	}
	// Our ping's reply comes back; theirs goes out.
	if ok, _ := f.Out(echo(self, jie, icmpEchoRequest, 41)); !ok {
		t.Fatal("ping refused")
	}
	if ok, why := f.In(echo(jie, self, icmpEchoReply, 41)); !ok {
		t.Fatalf("ping reply refused: %s", why)
	}
	if ok, _ := f.In(echo(lin, self, icmpEchoRequest, 7)); !ok {
		t.Fatal("inbound ping refused")
	}
	if ok, why := f.Out(echo(self, lin, icmpEchoReply, 7)); !ok {
		t.Fatalf("our ping reply refused: %s", why)
	}
	if ok, why := f.Out(echo(self, lin, icmpEchoReply, 8)); ok || why != "icmp-reply-without-flow" {
		t.Fatalf("unasked echo reply left: (%v, %q)", ok, why)
	}
}

func TestICMPErrorsNeedAFlow(t *testing.T) {
	f := newTestFilter()
	// self -> jie udp 6000->9999, then jie says port unreachable.
	sent := udp(self, jie, 6000, 9999)
	if ok, _ := f.Out(sent); !ok {
		t.Fatal("udp refused")
	}
	unreach := pk{proto: ProtoICMP, src: jie, dst: self, icmpType: icmpUnreachable, payload: sent[:28]}.bytes()
	if ok, why := f.In(unreach); !ok {
		t.Fatalf("unreachable for our flow refused: %s", why)
	}
	// An error quoting a flow we never had is a probe; drop it.
	forged := pk{proto: ProtoICMP, src: jie, dst: self, icmpType: icmpUnreachable, payload: udp(self, jie, 6001, 9999)[:28]}.bytes()
	if ok, why := f.In(forged); ok || why != "icmp-error-without-flow" {
		t.Fatalf("forged unreachable: (%v, %q)", ok, why)
	}
	// An error from lin about our flow with jie: not lin's business.
	cross := pk{proto: ProtoICMP, src: lin, dst: self, icmpType: icmpUnreachable, payload: sent[:28]}.bytes()
	if ok, _ := f.In(cross); ok {
		t.Fatal("a third device's error about our flow got in")
	}
	// Our stack answering jie's packet to a closed (but open) port.
	in := udp(lin, self, 5000, 7)
	if ok, _ := f.In(in); !ok {
		t.Fatal("udp/7 from lin refused")
	}
	reply := pk{proto: ProtoICMP, src: self, dst: lin, icmpType: icmpUnreachable, payload: in[:28]}.bytes()
	if ok, why := f.Out(reply); !ok {
		t.Fatalf("our unreachable for lin's flow refused: %s", why)
	}
}

func TestPausedScopeIsInvisible(t *testing.T) {
	f := newTestFilter()
	p := testPolicy()
	delete(p.Peers, jie) // the engine drops peers whose only shared scope is paused
	delete(p.Open, "weekend")
	f.SetPolicy(p)
	if ok, _ := f.In(echo(jie, self, icmpEchoRequest, 9)); ok {
		t.Fatal("paused scope's member got in")
	}
	if ok, _ := f.Out(udp(self, jie, 6000, 7)); ok {
		t.Fatal("sent into a paused scope")
	}
	if ok, _ := f.In(tcp(lin, self, 5000, 80, syn)); !ok {
		t.Fatal("pausing weekend affected studio")
	}
}

// A policy change cuts open flows it no longer allows: removal, a service
// switched off and refusals take effect on connections already open.
func TestSetPolicyCutsFlows(t *testing.T) {
	f := newTestFilter()
	f.In(tcp(lin, self, 5000, 80, syn))  // lin uses our website
	f.Out(tcp(self, jie, 6000, 22, syn)) // we use jie's ssh
	f.In(echo(lin, self, icmpEchoRequest, 3))

	p := testPolicy()
	p.Open["studio"] = []Rule{{ProtoUDP, 7, 7}} // website switched off
	f.SetPolicy(p)
	if ok, _ := f.In(tcp(lin, self, 5000, 80, ack)); ok {
		t.Fatal("website flow survived switching the service off")
	}
	if ok, _ := f.In(tcp(jie, self, 22, 6000, ack)); !ok {
		t.Fatal("our own flow to jie was cut by an unrelated change")
	}

	p = testPolicy()
	p.Peers[jie] = &Peer{Label: "box.jie", Scopes: []string{"weekend"}, RefusedByPeer: true}
	f.SetPolicy(p)
	if ok, _ := f.In(tcp(jie, self, 22, 6000, ack)); ok {
		t.Fatal("flow to a peer that refused us survived")
	}
	if ok, why := f.Out(tcp(self, jie, 6001, 22, syn)); ok || why != "refused-by-peer" {
		t.Fatalf("dialed a peer that refused us: (%v, %q)", ok, why)
	}

	p = testPolicy()
	p.Peers[lin] = &Peer{Label: "box.lin", Scopes: []string{"studio"}, RefusedByMe: true}
	f.SetPolicy(p)
	if ok, why := f.In(echo(lin, self, icmpEchoRequest, 4)); ok || why != "refused" {
		t.Fatalf("refused peer pinged us: (%v, %q)", ok, why)
	}
	if ok, why := f.In(tcp(lin, self, 5001, 80, syn)); ok || why != "refused" {
		t.Fatalf("refused peer connected: (%v, %q)", ok, why)
	}
	// A refused peer can still be used by us: refusal is one-way.
	if ok, _ := f.Out(tcp(self, lin, 6002, 80, syn)); !ok {
		t.Fatal("refusing lin stopped us from using lin")
	}
	if ok, _ := f.In(tcp(lin, self, 80, 6002, syn|ack)); !ok {
		t.Fatal("reply from a peer we refused was dropped")
	}

	p = testPolicy()
	delete(p.Peers, lin) // lin left the scope
	f.SetPolicy(p)
	if ok, _ := f.In(tcp(lin, self, 80, 6002, ack)); ok {
		t.Fatal("flow with a peer that left survived")
	}
}

func TestFragments(t *testing.T) {
	f := newTestFilter()
	first := pk{proto: ProtoUDP, src: lin, dst: self, sport: 5000, dport: 7, id: 77, more: true}.bytes()
	rest := pk{proto: ProtoUDP, src: lin, dst: self, id: 77, fragOff: 1480}.bytes()
	stray := pk{proto: ProtoUDP, src: lin, dst: self, id: 78, fragOff: 1480}.bytes()
	if ok, why := f.In(rest); ok || why != "fragment-without-head" {
		t.Fatalf("a fragment before its head: (%v, %q)", ok, why)
	}
	if ok, _ := f.In(first); !ok {
		t.Fatal("first fragment refused")
	}
	if ok, why := f.In(rest); !ok {
		t.Fatalf("rest of an accepted datagram refused: %s", why)
	}
	if ok, _ := f.In(stray); ok {
		t.Fatal("fragment of another datagram got in")
	}
	// A head the filter refuses lets no tail in.
	closed := pk{proto: ProtoUDP, src: jie, dst: self, sport: 5000, dport: 27020, id: 90, more: true}.bytes()
	tail := pk{proto: ProtoUDP, src: jie, dst: self, id: 90, fragOff: 1480}.bytes()
	f.In(closed)
	if ok, _ := f.In(tail); ok {
		t.Fatal("tail of a refused datagram got in")
	}
	tiny := pk{proto: ProtoTCP, src: lin, dst: self, id: 5, fragOff: 8}.bytes()
	if ok, why := f.In(tiny); ok || why != "tiny-fragment" {
		t.Fatalf("tiny TCP fragment: (%v, %q)", ok, why)
	}
	short := pk{proto: ProtoTCP, src: lin, dst: self, sport: 5000, dport: 80, flags: syn, more: true}.bytes()[:20+12]
	binaryFix(short)
	if ok, why := f.In(short); ok || why != "malformed" {
		t.Fatalf("first fragment without the whole TCP header: (%v, %q)", ok, why)
	}
}

// TestFragmentedPing: a ping larger than the MTU goes out in pieces and
// its reply comes back in pieces, both ways. Each piece after the first is
// let through by the first: the request's by the request, and the reply's
// by the reply (it once wasn't, so large pings got no answer).
func TestFragmentedPing(t *testing.T) {
	f := newTestFilter()
	piece := func(src, dst netip.Addr, typ uint8, ipID uint16, off uint16, more bool) []byte {
		return pk{proto: ProtoICMP, src: src, dst: dst, icmpType: typ, sport: 0x4242, id: ipID, fragOff: off, more: more}.bytes()
	}
	for _, c := range []struct {
		name string
		send func([]byte) (bool, string) // the request's way
		back func([]byte) (bool, string) // the reply's way
		from netip.Addr                  // who pings
		to   netip.Addr
	}{
		{"this device pings", f.Out, f.In, self, lin},
		{"a peer pings this device", f.In, f.Out, lin, self},
	} {
		for i, step := range []struct {
			judge func([]byte) (bool, string)
			pkt   []byte
		}{
			{c.send, piece(c.from, c.to, icmpEchoRequest, 100, 0, true)},
			{c.send, piece(c.from, c.to, icmpEchoRequest, 100, 1256, true)},
			{c.send, piece(c.from, c.to, icmpEchoRequest, 100, 2512, false)},
			{c.back, piece(c.to, c.from, icmpEchoReply, 200, 0, true)},
			{c.back, piece(c.to, c.from, icmpEchoReply, 200, 1256, true)},
			{c.back, piece(c.to, c.from, icmpEchoReply, 200, 2512, false)},
		} {
			if ok, why := step.judge(step.pkt); !ok {
				t.Errorf("%s: piece %d refused: %s", c.name, i, why)
			}
		}
	}
	// A reply nobody asked for lets no piece in.
	if ok, _ := f.In(piece(jie, self, icmpEchoReply, 300, 0, true)); ok {
		t.Fatal("unasked-for reply got in")
	}
	if ok, _ := f.In(piece(jie, self, icmpEchoReply, 300, 1256, false)); ok {
		t.Fatal("a piece of an unasked-for reply got in")
	}
}

// binaryFix sets the total length after a test truncates a packet.
func binaryFix(b []byte) { binary.BigEndian.PutUint16(b[2:], uint16(len(b))) }

func TestFlowsExpire(t *testing.T) {
	f := newTestFilter()
	now := time.Now()
	f.now = func() time.Time { return now }
	f.Out(udp(self, jie, 6000, 53))
	now = now.Add(udpIdle + time.Second)
	if ok, _ := f.In(udp(jie, self, 53, 6000)); ok {
		t.Fatal("reply after the flow went idle got in")
	}
	f.GC()
	if f.Flows() != 0 {
		t.Fatalf("GC left %d flows", f.Flows())
	}
}

func TestOffAndExtraOut(t *testing.T) {
	f := newTestFilter()
	p := testPolicy()
	p.ExtraOut = []netip.Prefix{netip.MustParsePrefix("10.77.0.9/32")}
	f.SetPolicy(p)
	if ok, _ := f.Out(tcp(self, other, 1, 2, syn)); !ok {
		t.Fatal("ExtraOut not honoured")
	}
	p = testPolicy()
	p.Off = true
	f.SetPolicy(p)
	if ok, _ := f.In(tcp(lin, jie, 1, 2, syn)); !ok {
		t.Fatal("Off not honoured")
	}
}

// TestUsageAndCuts: who uses which service of this device, and which uses a
// policy change cuts (scene 5's red light): only connections peers started
// to a service, not our own, not pings.
func TestUsageAndCuts(t *testing.T) {
	f := newTestFilter()
	f.In(tcp(lin, self, 5000, 80, syn)) // lin opens our website twice
	f.In(tcp(lin, self, 5001, 80, syn))
	f.Out(tcp(self, jie, 6000, 22, syn)) // our own use of jie: not a use of ours
	f.In(echo(lin, self, icmpEchoRequest, 3))
	u := f.Usage()
	if len(u) != 1 || u[0].Peer != lin || u[0].Proto != ProtoTCP || u[0].Port != 80 || u[0].Conns != 2 {
		t.Fatalf("usage %+v, want lin on tcp/80 with 2 connections", u)
	}
	if u[0].Since.IsZero() || u[0].LastSeen.Before(u[0].Since) {
		t.Fatalf("times %+v", u[0])
	}

	p := testPolicy()
	p.Open["studio"] = []Rule{{ProtoUDP, 7, 7}} // the website switched off
	cut := f.SetPolicy(p)
	if len(cut) != 1 || cut[0].Peer != lin || cut[0].Port != 80 || cut[0].Conns != 2 {
		t.Fatalf("cut %+v, want lin's two connections to tcp/80", cut)
	}
	if u := f.Usage(); len(u) != 0 {
		t.Fatalf("usage after the cut: %+v", u)
	}
	if cut := f.SetPolicy(testPolicy()); len(cut) != 0 {
		t.Fatalf("turning it back on cut %+v", cut)
	}
}

// TestUsing: what this device uses of its peers' services, for the
// "已连接" timer on the line to the peer: connections it started, one entry
// per peer and port. Pings and closed connections are not uses, nor are a
// peer's connections to us.
func TestUsing(t *testing.T) {
	f := newTestFilter()
	f.Out(tcp(self, jie, 6000, 22, syn)) // two sessions to jie's ssh
	f.Out(tcp(self, jie, 6001, 22, syn))
	f.Out(tcp(self, lin, 6002, 80, syn))
	f.Out(echo(self, jie, icmpEchoRequest, 4))
	f.In(tcp(lin, self, 5000, 80, syn)) // lin using us: Usage, not Using
	u := f.Using()
	if len(u) != 2 {
		t.Fatalf("using %+v, want jie's tcp/22 and lin's tcp/80", u)
	}
	byPeer := map[netip.Addr]Use{u[0].Peer: u[0], u[1].Peer: u[1]}
	if j := byPeer[jie]; j.Proto != ProtoTCP || j.Port != 22 || j.Conns != 2 || j.Since.IsZero() {
		t.Fatalf("jie: %+v", j)
	}
	if l := byPeer[lin]; l.Port != 80 || l.Conns != 1 {
		t.Fatalf("lin: %+v", l)
	}
	f.Out(tcp(self, lin, 6002, 80, fin)) // closed: no longer a use
	if u := f.Using(); len(u) != 1 || u[0].Peer != jie {
		t.Fatalf("using after lin's connection closed: %+v", u)
	}
}

// TestNoInbound: the receiving side of a share (M3) only connects out. A
// peer met only through the share starts nothing here, not even a ping,
// whatever the scope's Open says; what this device starts gets its replies;
// and a peer that also shares a LAN with us still has what the LAN opens.
func TestNoInbound(t *testing.T) {
	f := New(func(string, ...any) {})
	share := &Policy{
		Self: self,
		Peers: map[netip.Addr]*Peer{
			lin: {Label: "box.lin", Scopes: []string{"share/s1"}},            // only the share
			jie: {Label: "box.jie", Scopes: []string{"share/s2", "weekend"}}, // the share and a LAN
		},
		Open: map[string][]Rule{
			"share/s1": {{ProtoTCP, 22, 22}}, // ignored: NoInbound wins
			"weekend":  {{ProtoTCP, 80, 80}},
		},
		NoInbound: map[string]bool{"share/s1": true, "share/s2": true},
	}
	f.SetPolicy(share)
	if ok, why := f.In(echo(lin, self, icmpEchoRequest, 1)); ok || why != "no-inbound" {
		t.Fatalf("ping from a share-only peer: %v %q, want dropped as no-inbound", ok, why)
	}
	if ok, why := f.In(tcp(lin, self, 5000, 22, syn)); ok || why != "no-inbound" {
		t.Fatalf("connection from a share-only peer to a port its Open lists: %v %q", ok, why)
	}
	// What we start, and its replies.
	if ok, _ := f.Out(tcp(self, lin, 6000, 80, syn)); !ok {
		t.Fatal("our connection to the shared device was dropped")
	}
	if ok, why := f.In(tcp(lin, self, 80, 6000, syn|ack)); !ok {
		t.Fatalf("the shared device's reply was dropped: %s", why)
	}
	if ok, _ := f.Out(echo(self, lin, icmpEchoRequest, 2)); !ok {
		t.Fatal("our ping to the shared device was dropped")
	}
	if ok, why := f.In(echo(lin, self, icmpEchoReply, 2)); !ok {
		t.Fatalf("the reply to our ping was dropped: %s", why)
	}
	// The peer also in a LAN with us: the LAN still lets it in.
	if ok, why := f.In(echo(jie, self, icmpEchoRequest, 3)); !ok {
		t.Fatalf("ping from a peer in a LAN with us too: dropped as %s", why)
	}
	if ok, why := f.In(tcp(jie, self, 5001, 80, syn)); !ok {
		t.Fatalf("the LAN's open service, from a peer also in a share: dropped as %s", why)
	}
	if ok, _ := f.In(tcp(jie, self, 5002, 22, syn)); ok {
		t.Fatal("a port no scope opens came in")
	}
}

// The reset answering a stale segment: back to where it came from, the
// sequence numbers RFC 9293 asks for, and checksums a stack accepts.
func TestResetFor(t *testing.T) {
	seg := tcp(lin, self, 5000, 80, ack)
	binary.BigEndian.PutUint32(seg[24:], 1000)   // SEQ
	binary.BigEndian.PutUint32(seg[28:], 777777) // ACK
	r := ResetFor(seg)
	if r == nil {
		t.Fatal("no reset")
	}
	h, ok := Parse(r)
	if !ok || h.Src != self || h.Dst != lin || h.SPort != 80 || h.DPort != 5000 || !h.TCPRst {
		t.Fatalf("reset %+v", h)
	}
	if seq := binary.BigEndian.Uint32(r[24:]); seq != 777777 || r[33] != 0x04 {
		t.Fatalf("reset to an ACK: seq %d flags %#x, want seq 777777 and RST", seq, r[33])
	}
	if checksum(r[:20], 0) != 0 {
		t.Fatal("bad IP checksum")
	}
	var pseudo uint32 = ProtoTCP + 20
	for i := 12; i < 20; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(r[i:]))
	}
	if checksum(r[20:], pseudo) != 0 {
		t.Fatal("bad TCP checksum")
	}

	// No ACK (a FIN alone, with two bytes): SEQ 0, ACK = SEG.SEQ + len + FIN.
	fin := pk{proto: ProtoTCP, src: lin, dst: self, sport: 5000, dport: 80, flags: 0x01, payload: []byte("hi")}.bytes()
	binary.BigEndian.PutUint32(fin[24:], 50)
	r = ResetFor(fin)
	if r == nil || binary.BigEndian.Uint32(r[24:]) != 0 || binary.BigEndian.Uint32(r[28:]) != 53 || r[33] != 0x14 {
		t.Fatalf("reset to a FIN: %x", r)
	}
	if ResetFor(tcp(lin, self, 5000, 80, 0x04)) != nil {
		t.Fatal("answered a reset")
	}
}

// seg is a TCP segment with sequence number seq and data.
func seg(src, dst netip.Addr, sport, dport uint16, flags byte, seq uint32, data string) []byte {
	b := pk{proto: ProtoTCP, src: src, dst: dst, sport: sport, dport: dport, flags: flags, payload: []byte(data)}.bytes()
	binary.BigEndian.PutUint32(b[24:], seq)
	return b
}

// A policy change that cuts a live TCP connection (here the service
// closed) resets both ends at once, each with the sequence number it
// expects next: the stack here and the peer. Later segments of it, either
// way, are answered with resets too, even once the peer is gone.
func TestCutConnectionIsReset(t *testing.T) {
	f := newTestFilter()
	var toStack, toPeers [][]byte
	f.OnReset = func(s, p [][]byte) { toStack, toPeers = append(toStack, s...), append(toPeers, p...) }
	steps := []struct {
		in  bool
		pkt []byte
	}{
		{true, seg(lin, self, 5000, 80, syn, 100, "")},      // the peer opens
		{false, seg(self, lin, 80, 5000, syn|ack, 500, "")}, // we answer
		{true, seg(lin, self, 5000, 80, ack, 101, "hello")}, // its data: we expect 106 next
		{false, seg(self, lin, 80, 5000, ack, 501, "abc")},  // ours: it expects 504
		{true, seg(lin, self, 5000, 80, ack, 101, "hello")}, // a repeat moves nothing back
	}
	for i, s := range steps {
		var ok bool
		var why string
		if s.in {
			ok, why = f.In(s.pkt)
		} else {
			ok, why = f.Out(s.pkt)
		}
		if !ok {
			t.Fatalf("step %d dropped: %s", i, why)
		}
	}
	p := testPolicy()
	p.Open["studio"] = []Rule{{ProtoUDP, 7, 7}} // the web service closes
	if cut := f.SetPolicy(p); len(cut) != 1 {
		t.Fatalf("cut %+v, want the one connection", cut)
	}
	if len(toStack) != 1 || len(toPeers) != 1 {
		t.Fatalf("resets: %d to the stack, %d to the peer; want one each", len(toStack), len(toPeers))
	}
	check := func(r []byte, src, dst netip.Addr, sport, dport uint16, seq uint32) {
		t.Helper()
		h, ok := Parse(r)
		if !ok || !h.TCPRst || h.Src != src || h.Dst != dst || h.SPort != sport || h.DPort != dport {
			t.Fatalf("reset %+v", h)
		}
		if got := binary.BigEndian.Uint32(r[24:]); got != seq {
			t.Fatalf("reset seq %d, want %d", got, seq)
		}
		var pseudo uint32 = ProtoTCP + 20
		for i := 12; i < 20; i += 2 {
			pseudo += uint32(binary.BigEndian.Uint16(r[i:]))
		}
		if checksum(r[:20], 0) != 0 || checksum(r[20:], pseudo) != 0 {
			t.Fatal("bad checksum")
		}
	}
	check(toStack[0], lin, self, 5000, 80, 106)
	check(toPeers[0], self, lin, 80, 5000, 504)

	delete(p.Peers, lin) // and the peer leaves the map altogether
	f.SetPolicy(p)
	if ok, why := f.In(seg(lin, self, 5000, 80, ack, 106, "more")); ok || why != CutTCP {
		t.Fatalf("the peer going on with it: %v %q, want %q", ok, why, CutTCP)
	}
	if ok, why := f.Out(seg(self, lin, 80, 5000, ack, 504, "late")); ok || why != CutTCPOut {
		t.Fatalf("our stack going on with it: %v %q, want %q", ok, why, CutTCPOut)
	}
	if ok, why := f.In(seg(lin, self, 5000, 80, 0x04|ack, 106, "")); ok || why == CutTCP {
		t.Fatalf("a reset on it: %v %q; never answered", ok, why)
	}
	if ok, why := f.In(seg(lin, self, 5001, 80, ack, 1, "x")); ok || why == CutTCP {
		t.Fatalf("another connection: %v %q", ok, why)
	}
}

// TestPeerCannotFillTheFlowTable: a peer that may ping this device could
// otherwise fill the flow table with pings of new identifiers, and every
// new connection, this device's own included, would be refused. What one
// peer starts is capped; the others, and this device, are not affected.
func TestPeerCannotFillTheFlowTable(t *testing.T) {
	f := newTestFilter()
	now := time.Now()
	f.now = func() time.Time { return now }
	for id := range maxPeerFlows {
		if ok, why := f.In(echo(lin, self, icmpEchoRequest, uint16(id))); !ok {
			t.Fatalf("ping %d refused: %s", id, why)
		}
	}
	if ok, why := f.In(echo(lin, self, icmpEchoRequest, maxPeerFlows)); ok || why != "peer-flows-full" {
		t.Fatalf("one ping too many: (%v, %q)", ok, why)
	}
	if ok, _ := f.In(echo(lin, self, icmpEchoRequest, 7)); !ok {
		t.Error("a ping of an identifier already in the table refused")
	}
	if ok, why := f.In(echo(jie, self, icmpEchoRequest, 1)); !ok {
		t.Errorf("another peer's ping refused: %s", why)
	}
	if ok, why := f.Out(udp(self, lin, 40000, 7)); !ok {
		t.Errorf("this device's own new flow refused: %s", why)
	}
	now = now.Add(icmpIdle + time.Second) // the pings' flows expire
	if ok, why := f.In(echo(lin, self, icmpEchoRequest, maxPeerFlows)); !ok {
		t.Errorf("after the flows expired: %s", why)
	}
}

// TestPeerCannotFillTheFragmentTable: one peer's first fragments fill at
// most a quarter of the table, and a head admits its datagram's other
// pieces for fragIdle only.
func TestPeerCannotFillTheFragmentTable(t *testing.T) {
	f := newTestFilter()
	now := time.Now()
	f.now = func() time.Time { return now }
	head := func(src netip.Addr, id uint16) []byte {
		return pk{proto: ProtoUDP, src: src, dst: self, sport: 5000, dport: 7, id: id, more: true}.bytes()
	}
	tail := func(src netip.Addr, id uint16) []byte {
		return pk{proto: ProtoUDP, src: src, dst: self, id: id, fragOff: 1480}.bytes()
	}
	for id := range maxPeerFrags {
		if ok, why := f.In(head(lin, uint16(id))); !ok {
			t.Fatalf("head %d refused: %s", id, why)
		}
	}
	f.In(head(lin, maxPeerFrags)) // the flow passes; its head is not noted
	if ok, why := f.In(tail(lin, maxPeerFrags)); ok || why != "fragment-without-head" {
		t.Fatalf("the rest of a datagram past the peer's share: (%v, %q)", ok, why)
	}
	if ok, why := f.In(tail(lin, 3)); !ok {
		t.Errorf("the rest of a noted datagram refused: %s", why)
	}
	f.In(head(jie, 9))
	if ok, why := f.In(tail(jie, 9)); !ok {
		t.Errorf("another peer's datagram refused: %s", why)
	}
	now = now.Add(fragIdle + time.Second)
	if ok, _ := f.In(tail(lin, 3)); ok {
		t.Error("a piece let in after its head expired")
	}
	f.In(head(lin, maxPeerFrags+1))
	if ok, why := f.In(tail(lin, maxPeerFrags+1)); !ok {
		t.Errorf("after the heads expired: %s", why)
	}
}
