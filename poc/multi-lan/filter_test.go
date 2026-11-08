package main

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

// The filter is the only thing keeping two LANs apart on a device that is in
// both, so its rules get table tests independent of any network.

var (
	self  = netip.MustParseAddr("100.86.0.1")
	lin   = netip.MustParseAddr("100.86.0.2") // shares "studio" with self
	jie   = netip.MustParseAddr("100.86.0.3") // shares "weekend" with self
	other = netip.MustParseAddr("100.86.0.9") // shares nothing
)

func testPolicy() *policy {
	return &policy{
		self: self,
		peers: map[netip.Addr]*peerPol{
			lin: {label: "box.lin", lans: []string{"studio"}},
			jie: {label: "box.jie", lans: []string{"weekend"}},
		},
		open: map[string][]svc{
			"studio":  {{proto: protoICMP}, {proto: protoUDP, port: 7}, {proto: protoTCP, port: 80}},
			"weekend": {{proto: protoICMP}, {proto: protoUDP, port: 7}},
		},
	}
}

func newTestFilter() *filterTUN {
	f := &filterTUN{flows: map[flowKey]time.Time{}, drops: map[string]uint64{}, seen: map[string]time.Time{}}
	f.pol.Store(testPolicy())
	return f
}

func pkt(proto uint8, src, dst netip.Addr, sport, dport uint16, syn bool) []byte {
	b := make([]byte, 40)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], 40)
	b[9] = proto
	s4, d4 := src.As4(), dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	switch proto {
	case protoTCP:
		binary.BigEndian.PutUint16(b[20:], sport)
		binary.BigEndian.PutUint16(b[22:], dport)
		if syn {
			b[33] = 0x02
		} else {
			b[33] = 0x10
		}
	case protoUDP:
		binary.BigEndian.PutUint16(b[20:], sport)
		binary.BigEndian.PutUint16(b[22:], dport)
	case protoICMP:
		b[20] = 8 // echo request
		binary.BigEndian.PutUint16(b[24:], sport)
	}
	return b
}

func TestInbound(t *testing.T) {
	cases := []struct {
		name string
		p    []byte
		ok   bool
		why  string
	}{
		{"studio member opens a service opened to studio", pkt(protoTCP, lin, self, 5000, 80, true), true, ""},
		{"weekend member, service only opened to studio", pkt(protoTCP, jie, self, 5000, 80, true), false, "service-not-open-to-this-lan"},
		{"weekend member pings", pkt(protoICMP, jie, self, 9, 9, false), true, ""},
		{"stranger", pkt(protoICMP, other, self, 9, 9, false), false, "not-in-an-active-shared-lan"},
		{"studio member aims at a weekend device through us", pkt(protoTCP, lin, jie, 5000, 80, true), false, "transit-in"},
		{"weekend member aims at a studio device through us", pkt(protoUDP, jie, lin, 5000, 7, false), false, "transit-in"},
		{"mid-stream TCP without a flow", pkt(protoTCP, lin, self, 5000, 80, false), false, "tcp-without-flow"},
	}
	for _, c := range cases {
		f := newTestFilter()
		ok, why := f.allowIn(c.p)
		if ok != c.ok || why != c.why {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, ok, why, c.ok, c.why)
		}
	}
}

func TestOutbound(t *testing.T) {
	f := newTestFilter()
	if ok, why := f.allowOut(pkt(protoTCP, self, jie, 6000, 22, true)); !ok {
		t.Fatalf("own connection to a visible peer refused: %s", why)
	}
	// A packet with someone else's source is being forwarded; it must not leave,
	// even if the OS has IP forwarding turned on.
	if ok, why := f.allowOut(pkt(protoTCP, lin, jie, 5000, 80, true)); ok || why != "transit-out" {
		t.Fatalf("forwarded packet left: (%v, %q)", ok, why)
	}
	if ok, why := f.allowOut(pkt(protoTCP, self, other, 6000, 80, true)); ok || why != "not-a-visible-peer" {
		t.Fatalf("sent to a non-peer: (%v, %q)", ok, why)
	}
}

func TestRepliesFollowFlows(t *testing.T) {
	f := newTestFilter()
	// We connect to jie:22, a port jie never opened *to us*: the reply must
	// still come back, because the flow was started by this device.
	if ok, _ := f.allowOut(pkt(protoTCP, self, jie, 6000, 22, true)); !ok {
		t.Fatal("outbound refused")
	}
	if ok, why := f.allowIn(pkt(protoTCP, jie, self, 22, 6000, false)); !ok {
		t.Fatalf("reply refused: %s", why)
	}
	// After the shared LAN is paused, forgetting the peer cuts the flow too.
	f.forgetPeer(jie)
	p := testPolicy()
	delete(p.peers, jie)
	f.pol.Store(p)
	if ok, _ := f.allowIn(pkt(protoTCP, jie, self, 22, 6000, false)); ok {
		t.Fatal("reply from a paused LAN still accepted")
	}
}

func TestPausedLANIsInvisible(t *testing.T) {
	f := newTestFilter()
	p := testPolicy()
	delete(p.peers, jie) // node.applyNetmap drops peers whose only shared LAN is paused
	delete(p.open, "weekend")
	f.pol.Store(p)
	if ok, _ := f.allowIn(pkt(protoICMP, jie, self, 9, 9, false)); ok {
		t.Fatal("paused LAN member got in")
	}
	if ok, _ := f.allowOut(pkt(protoUDP, self, jie, 6000, 7, false)); ok {
		t.Fatal("sent into a paused LAN")
	}
	if ok, _ := f.allowIn(pkt(protoTCP, lin, self, 5000, 80, true)); !ok {
		t.Fatal("pausing weekend affected studio")
	}
}
