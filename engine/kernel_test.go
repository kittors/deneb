package engine

import (
	"encoding/binary"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	wgtun "golang.zx2c4.com/wireguard/tun"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

// fakeTUN stands in for the utun: the test is the operating system on the
// other side, sending what programs send and reading what they would get.
type fakeTUN struct {
	toEngine   chan []byte
	fromEngine chan []byte
	events     chan wgtun.Event
	closed     chan struct{}
	once       sync.Once
}

func newFakeTUN() *fakeTUN {
	f := &fakeTUN{toEngine: make(chan []byte, 16), fromEngine: make(chan []byte, 64),
		events: make(chan wgtun.Event, 1), closed: make(chan struct{})}
	f.events <- wgtun.EventUp
	return f
}

func (f *fakeTUN) File() *os.File             { return nil }
func (f *fakeTUN) MTU() (int, error)          { return 1280, nil }
func (f *fakeTUN) Name() (string, error)      { return "fake0", nil }
func (f *fakeTUN) Events() <-chan wgtun.Event { return f.events }
func (f *fakeTUN) BatchSize() int             { return 1 }
func (f *fakeTUN) Close() error               { f.once.Do(func() { close(f.closed) }); return nil }

func (f *fakeTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-f.toEngine:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	case <-f.closed:
		return 0, os.ErrClosed
	}
}

func (f *fakeTUN) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		select {
		case f.fromEngine <- append([]byte(nil), b[offset:]...):
		default:
		}
	}
	return len(bufs), nil
}

// await returns the first packet the engine hands the system that match
// accepts, or nil after d.
func (f *fakeTUN) await(d time.Duration, match func(p []byte) bool) []byte {
	deadline := time.After(d)
	for {
		select {
		case p := <-f.fromEngine:
			if match(p) {
				return p
			}
		case <-deadline:
			return nil
		}
	}
}

func csum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}

// ip4 builds an IPv4 packet around a transport payload, with checksums.
func ip4(proto uint8, src, dst netip.Addr, l4 []byte) []byte {
	p := make([]byte, 20+len(l4))
	p[0], p[8], p[9] = 0x45, 64, proto
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	s, d := src.As4(), dst.As4()
	copy(p[12:], s[:])
	copy(p[16:], d[:])
	binary.BigEndian.PutUint16(p[10:], csum(p[:20], 0))
	copy(p[20:], l4)
	if proto == 6 || proto == 17 {
		var ph uint32
		for i := 12; i < 20; i += 2 {
			ph += uint32(p[i])<<8 | uint32(p[i+1])
		}
		ph += uint32(proto) + uint32(len(l4))
		off := 16 // TCP
		if proto == 17 {
			off = 6
		}
		binary.BigEndian.PutUint16(p[20+off:], csum(p[20:], ph))
	}
	return p
}

func from(p []byte) netip.Addr { return netip.AddrFrom4([4]byte(p[12:16])) }

// TestKernelModeDataPath runs the kernel-mode data path with a fake utun:
// what programs send goes through the filter and WireGuard to a user-space
// peer and back, and the engine answers deneb.internal names itself. It is
// as far as kernel mode goes without root (the real utun, route and
// resolver file are in test/topo/kernel-round.sh).
func TestKernelModeDataPath(t *testing.T) {
	if testing.Short() {
		t.Skip("runs two engines")
	}
	fake := newFakeTUN()
	old := openKernel
	openKernel = func(cfg tun.Config, logf func(string, ...any)) (*tun.Kernel, error) {
		if !cfg.DNS.IsValid() || cfg.Block.String() != "10.77.0.0/16" {
			t.Errorf("kernel config %+v", cfg)
		}
		return tun.Fake(fake, "fake0"), nil
	}
	defer func() { openKernel = old }()

	relay, pool := testRelay(t)
	w := &world{relay: relay, members: map[proto.ScopeID][]*tnode{}, open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name, owner string, mode apitype.Mode) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: mode, BindInterface: "none", RelayRoots: pool, Logf: quiet, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: owner, person: proto.PersonID("p-" + owner),
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac", "kittors", apitype.ModeKernel)
	build := mk(2, "build", "kittors", apitype.ModeUserspace)
	w.members["lan/studio"] = []*tnode{mac, build}
	web := proto.Service{Kind: proto.ServiceWeb, Name: "网页", Ports: []proto.PortRange{{Proto: proto.TCP, First: 8080, Last: 8080}}}
	w.open["lan/studio"] = map[*tnode][]proto.Service{build: {web}}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	ln, err := build.e.stack.ListenTCP(8080)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dnsAddr := netip.MustParseAddr("10.77.0.53")

	// A name, answered by the engine without leaving this machine.
	var q dnsmessage.Builder
	q = dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	q.StartQuestions()
	q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("build.kittors.deneb.internal."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	msg, _ := q.Finish()
	udp := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint16(udp[0:], 53530)
	binary.BigEndian.PutUint16(udp[2:], 53)
	binary.BigEndian.PutUint16(udp[4:], uint16(len(udp)))
	copy(udp[8:], msg)
	fake.toEngine <- ip4(17, mac.addr, dnsAddr, udp)
	ans := fake.await(3*time.Second, func(p []byte) bool { return p[9] == 17 && from(p) == dnsAddr })
	if ans == nil {
		t.Fatal("no DNS answer from the engine")
	}
	var m dnsmessage.Message
	if err := m.Unpack(ans[28:]); err != nil || len(m.Answers) != 1 {
		t.Fatalf("DNS answer: %v %+v", err, m)
	}
	if a, ok := m.Answers[0].Body.(*dnsmessage.AResource); !ok || netip.AddrFrom4(a.A) != build.addr {
		t.Fatalf("build.kittors.deneb.internal answered %v", m.Answers[0].Body)
	}

	// An ordinary ping, through WireGuard to the peer and back.
	echo := []byte{8, 0, 0, 0, 0x12, 0x34, 0, 1, 'h', 'i'}
	binary.BigEndian.PutUint16(echo[2:], csum(echo, 0))
	deadline := time.Now().Add(10 * time.Second)
	var reply []byte
	for reply == nil && time.Now().Before(deadline) {
		fake.toEngine <- ip4(1, mac.addr, build.addr, echo)
		reply = fake.await(time.Second, func(p []byte) bool { return p[9] == 1 && from(p) == build.addr && p[20] == 0 })
	}
	if reply == nil {
		t.Fatal("no echo reply from build through kernel mode")
	}

	// A connection to a service build opens to the LAN: the SYN gets a SYN-ACK.
	syn := make([]byte, 20)
	binary.BigEndian.PutUint16(syn[0:], 40000)
	binary.BigEndian.PutUint16(syn[2:], 8080)
	binary.BigEndian.PutUint32(syn[4:], 1000)
	syn[12], syn[13] = 5<<4, 0x02 // header length, SYN
	binary.BigEndian.PutUint16(syn[14:], 65535)
	fake.toEngine <- ip4(6, mac.addr, build.addr, syn)
	if p := fake.await(5*time.Second, func(p []byte) bool { return p[9] == 6 && from(p) == build.addr && p[33]&0x12 == 0x12 }); p == nil {
		t.Fatal("no SYN-ACK from build's open service")
	}

	// A program cannot send as another address: no forwarding (layer 2).
	before := mac.e.f.Drops()["out:transit-out"]
	fake.toEngine <- ip4(1, netip.MustParseAddr("10.77.0.9"), build.addr, echo)
	if p := fake.await(time.Second, func(p []byte) bool { return p[9] == 1 && from(p) == build.addr }); p != nil {
		t.Fatal("a packet with another source got an answer")
	}
	if after := mac.e.f.Drops()["out:transit-out"]; after != before+1 {
		t.Errorf("transit-out drops %d -> %d", before, after)
	}
}

// lookup asks the engine's DNS (kernel mode, through the fake utun) for an
// A record, as the system resolver would, and returns the answer.
func lookup(t *testing.T, fake *fakeTUN, self, dnsAddr netip.Addr, name string, id uint16) dnsmessage.Message {
	t.Helper()
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	q.StartQuestions()
	q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	msg, _ := q.Finish()
	udp := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint16(udp[0:], 53530+id)
	binary.BigEndian.PutUint16(udp[2:], 53)
	binary.BigEndian.PutUint16(udp[4:], uint16(len(udp)))
	copy(udp[8:], msg)
	fake.toEngine <- ip4(17, self, dnsAddr, udp)
	ans := fake.await(3*time.Second, func(p []byte) bool {
		return p[9] == 17 && from(p) == dnsAddr && binary.BigEndian.Uint16(p[28:]) == id
	})
	if ans == nil {
		t.Fatalf("no DNS answer for %s", name)
	}
	var m dnsmessage.Message
	if err := m.Unpack(ans[28:]); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestKernelModeNamesFollowMembership is acceptance item 7 in kernel mode,
// on the engine's side of the system resolver: a device taken out of the
// only LAN it shares is "no such name" at once, with a 1 s SOA so the
// resolver forgets that within a second, and it resolves again the moment
// it is put back.
func TestKernelModeNamesFollowMembership(t *testing.T) {
	if testing.Short() {
		t.Skip("runs two engines")
	}
	fake := newFakeTUN()
	old := openKernel
	openKernel = func(tun.Config, func(string, ...any)) (*tun.Kernel, error) { return tun.Fake(fake, "fake0"), nil }
	defer func() { openKernel = old }()

	relay, pool := testRelay(t)
	w := &world{relay: relay, members: map[proto.ScopeID][]*tnode{}, open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name, owner string, mode apitype.Mode) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: mode, BindInterface: "none", RelayRoots: pool, Logf: quiet, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: owner, person: proto.PersonID("p-" + owner),
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac", "kittors", apitype.ModeKernel)
	nas := mk(3, "nas", "jie", apitype.ModeUserspace)
	w.members["lan/weekend"] = []*tnode{mac, nas}
	w.open["lan/weekend"] = map[*tnode][]proto.Service{}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	dnsAddr := netip.MustParseAddr("10.77.0.53")
	const name = "nas.jie.deneb.internal."

	m := lookup(t, fake, mac.addr, dnsAddr, name, 1)
	if len(m.Answers) != 1 || netip.AddrFrom4(m.Answers[0].Body.(*dnsmessage.AResource).A) != nas.addr {
		t.Fatalf("before: %+v", m)
	}

	w.members["lan/weekend"] = []*tnode{mac} // nas taken out
	w.push()
	m = lookup(t, fake, mac.addr, dnsAddr, name, 2)
	if m.RCode != dnsmessage.RCodeNameError || len(m.Answers) != 0 {
		t.Fatalf("taken out: %v %+v", m.RCode, m.Answers)
	}
	if len(m.Authorities) != 1 || m.Authorities[0].Body.(*dnsmessage.SOAResource).MinTTL != 1 || m.Authorities[0].Header.TTL != 1 {
		t.Fatalf("taken out: the negative answer needs a 1 s SOA, got %+v", m.Authorities)
	}

	flushed := make(chan struct{}, 4)
	oldFlush := flushDNS
	flushDNS = func() error { flushed <- struct{}{}; return nil }
	defer func() { flushDNS = oldFlush }()
	w.members["lan/weekend"] = []*tnode{mac, nas} // put back
	w.push()
	m = lookup(t, fake, mac.addr, dnsAddr, name, 3)
	if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 1 || netip.AddrFrom4(m.Answers[0].Body.(*dnsmessage.AResource).A) != nas.addr {
		t.Fatalf("put back: %v %+v", m.RCode, m.Answers)
	}
	// The name it said did not exist is back: the system's caches, which
	// would keep "no such name" longer, get flushed once.
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("the system's DNS caches were not flushed when nas came back")
	}
	w.push() // nothing new comes back: no second flush
	select {
	case <-flushed:
		t.Fatal("flushed again with nothing new")
	case <-time.After(300 * time.Millisecond):
	}
}
