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
		{"mid-stream TCP without a flow", tcp(lin, self, 5000, 80, ack), false, "tcp-without-flow"},
		{"SYN+ACK is not a new connection", tcp(lin, self, 5000, 80, syn|ack), false, "tcp-without-flow"},
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
