package engine

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// TestIdleStopsHeartbeatAndWakesFast is M2's "heartbeat only with traffic"
// with its guard: an idle peer gets no pings and keeps the path it showed,
// and the first packet after idle is answered within 0.2 s.
func TestIdleStopsHeartbeatAndWakesFast(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond })
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey

	// Idle after 1.5 s, and past the 2.5 s trust of the last pong.
	time.Sleep(5500 * time.Millisecond)
	st, _ := mac.e.ms.Status(k)
	if !st.Idle || st.Kind != "direct" {
		t.Fatalf("after 5.5 s without traffic: idle=%v kind=%s, want an idle peer still shown direct", st.Idle, st.Kind)
	}
	before := st.PingsTx
	time.Sleep(2 * time.Second)
	if st, _ = mac.e.ms.Status(k); st.PingsTx != before {
		t.Fatalf("an idle peer got %d pings in 2 s", st.PingsTx-before)
	}

	start := time.Now()
	sum, err := mac.e.Ping(context.Background(), "build", 1, 0, time.Second, nil)
	first := time.Since(start)
	if err != nil || sum.Received != 1 {
		t.Fatalf("first ping after idle: %v %+v", err, sum)
	}
	if first > 200*time.Millisecond {
		t.Fatalf("first answer after idle took %s, want within 0.2 s", first)
	}
	t.Logf("first answer after idle in %s", first.Round(100*time.Microsecond))
	waitPath(t, mac, build, "direct", 3*time.Second)
	if st, _ = mac.e.ms.Status(k); st.Idle {
		t.Fatal("still idle after traffic")
	}
}

// lineStream has build write a numbered line every 100 ms to mac over the
// tunnel, and records when each arrives, as a long connection would.
type lineStream struct {
	mu      sync.Mutex
	arrived []time.Time
	broken  error
}

func startLineStream(t *testing.T, from, to *tnode, port uint16) (*lineStream, func()) {
	t.Helper()
	ln, err := to.e.stack.ListenTCP(port)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; ; i++ {
			if _, err := fmt.Fprintf(c, "%d\n", i); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	c, err := from.e.dialPeer(ctx, netip.AddrPortFrom(to.addr, port))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	s := &lineStream{}
	go func() {
		r := bufio.NewReader(c)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				s.mu.Lock()
				s.broken = err
				s.mu.Unlock()
				return
			}
			s.mu.Lock()
			s.arrived = append(s.arrived, time.Now())
			s.mu.Unlock()
		}
	}()
	return s, func() { c.Close(); ln.Close() }
}

// since reports how many lines arrived after t and the longest gap then.
func (s *lineStream) since(t time.Time) (n int, gap time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := t
	for _, a := range s.arrived {
		if a.Before(t) {
			continue
		}
		n++
		if a.Sub(last) > gap {
			gap = a.Sub(last)
		}
		last = a
	}
	return n, gap
}

// TestLossyDirectGivesWayToRelay is M2's bad line: a direct path that drops
// 20% of UDP still answers most pings, but TCP over it stalls. The engine
// must move to the relay within 10 s, keep the connection going, and come
// back to direct once the line is clean.
func TestLossyDirectGivesWayToRelay(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	// Every packet of the long connection, kept in memory: if it stalls,
	// the test writes what both sides did, and every goroutine's stack.
	var traceMu sync.Mutex
	var trace []string
	for _, n := range []*tnode{mac, build} {
		name := n.name
		n.e.mu.Lock()
		n.e.tunw.SetTrace(7100, func(f string, a ...any) {
			traceMu.Lock()
			trace = append(trace, time.Now().Format("15:04:05.000")+" "+name+" "+fmt.Sprintf(f, a...))
			if len(trace) > 4000 {
				trace = trace[1000:]
			}
			traceMu.Unlock()
		})
		n.e.mu.Unlock()
	}
	dumpStall := func(why string) {
		f, err := os.CreateTemp("", "deneb-stall-*.txt")
		if err != nil {
			t.Fatal(why)
		}
		traceMu.Lock()
		fmt.Fprintln(f, strings.Join(trace[max(0, len(trace)-400):], "\n"))
		traceMu.Unlock()
		fmt.Fprintln(f, "\n=== goroutines")
		pprof.Lookup("goroutine").WriteTo(f, 2)
		f.Close()
		t.Fatalf("%s (trace and goroutines in %s)", why, f.Name())
	}
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(time.Second)

	t0 := time.Now()
	mac.e.ms.SetLoss(k, 0.2, 0.2)
	deadline := t0.Add(10 * time.Second)
	for {
		st, _ := mac.e.ms.Status(k)
		if st.Kind == "relay" && st.Reason == "lossy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("10 s at 20%% loss and still %s (%s)", st.Kind, st.Reason)
		}
		time.Sleep(100 * time.Millisecond)
	}
	onRelay := time.Now()
	t.Logf("on the relay %s after the line went bad", onRelay.Sub(t0).Round(time.Millisecond))
	// TCP may sit out a retransmission timeout it backed off to on the
	// lossy line; it must resume on the relay and then flow normally.
	var resumed time.Time
	for resumed.IsZero() {
		if n, _ := stream.since(onRelay); n > 0 {
			resumed = time.Now()
		} else if time.Since(onRelay) > 8*time.Second {
			dumpStall("the long connection did not resume on the relay within 8 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("the long connection resumed %s after the switch", resumed.Sub(onRelay).Round(time.Millisecond))
	time.Sleep(500 * time.Millisecond)
	mark := time.Now()
	time.Sleep(2 * time.Second)
	// Lines every 100 ms: flowing, with room for a loaded machine.
	if n, gap := stream.since(mark); n < 15 || gap > time.Second {
		t.Fatalf("on the relay the stream got %d lines in 2 s, longest gap %s", n, gap)
	}

	mac.e.ms.SetLoss(k, 0, 0)
	t1 := time.Now()
	waitPath(t, mac, build, "direct", 35*time.Second) // at least 20 s on the relay, then a clean 10 s
	t.Logf("direct again %s after the line was clean", time.Since(t1).Round(time.Millisecond))
	stream.mu.Lock()
	broken := stream.broken
	stream.mu.Unlock()
	if broken != nil {
		t.Fatalf("the long connection broke: %v", broken)
	}
	_, worst := stream.since(t0)
	t.Logf("longest stall of the long connection: %s", worst.Round(time.Millisecond))
}

// TestPeersOnDifferentRelays: two devices homed on different relays reach
// each other with UDP blocked between them, each sending through the
// other's home; a device keeps only its home relay open, opens another when
// a peer homed there is sent to, and closes it again when it goes unused.
func TestPeersOnDifferentRelays(t *testing.T) {
	relayA, pool := testRelay(t)
	relayB := testRelayInto(t, "relay-b", "乙", pool)
	w := &world{relay: relayA, relays: []proto.Relay{relayA, relayB}, members: map[proto.ScopeID][]*tnode{},
		open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name, owner string, home proto.RelayID) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool,
			Logf: quiet, Version: "test", HomeRelay: home, Idle: 1500 * time.Millisecond, RelayIdle: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: owner, person: proto.PersonID("p-" + owner),
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac", "kittors", "relay-local")
	build := mk(2, "build", "kittors", "relay-b")
	nas := mk(3, "nas", "jie", "relay-local")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.members["lan/weekend"] = []*tnode{mac, nas}
	w.open["lan/studio"] = map[*tnode][]proto.Service{}
	w.open["lan/weekend"] = map[*tnode][]proto.Service{}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	w.push() // now with each device's home relay as it reports it
	if build.home() != "relay-b" || mac.home() != "relay-local" {
		t.Fatalf("homes: mac %s, build %s", mac.home(), build.home())
	}
	mac.e.ms.SetBlocked(build.node().NodeKey, true)
	build.e.ms.SetBlocked(mac.node().NodeKey, true)

	pingOK(t, mac, "build", 10*time.Second)
	waitPath(t, mac, build, "relay", 5*time.Second)
	waitPath(t, build, mac, "relay", 5*time.Second)
	if r := pathOf(mac, build).Relay; r != "relay-b" {
		t.Errorf("mac reaches build through %s, want build's home relay-b", r)
	}
	if r := pathOf(build, mac).Relay; r != "relay-local" {
		t.Errorf("build reaches mac through %s, want mac's home relay-local", r)
	}
	open := func(n *tnode) string {
		var ids []string
		for _, r := range n.e.ms.Relays() {
			ids = append(ids, string(r.ID))
		}
		return fmt.Sprint(ids)
	}
	if got := open(mac); got != "[relay-b relay-local]" {
		t.Errorf("mac's relay connections %s", got)
	}
	if got := open(nas); got != "[relay-local]" {
		t.Errorf("nas, whose peers are all on its home relay, holds %s", got)
	}

	// No traffic: the peers idle, then the extra connections close.
	deadline := time.Now().Add(10 * time.Second)
	for open(mac) != "[relay-local]" || open(build) != "[relay-b]" {
		if time.Now().After(deadline) {
			t.Fatalf("after idle: mac holds %s, build %s", open(mac), open(build))
		}
		time.Sleep(200 * time.Millisecond)
	}
	start := time.Now()
	pingOK(t, mac, "build", 5*time.Second)
	t.Logf("first answer through the reopened relay in %s", time.Since(start).Round(time.Millisecond))
}

// TestNetworkChangeRecovers: after the network changes (here: as if it had)
// every path comes back, and no line shows an error meanwhile, only
// "connecting" and then the path it has.
func TestNetworkChangeRecovers(t *testing.T) {
	_, mac, build, nas := newWorld(t)
	pingOK(t, mac, "build", 5*time.Second)
	pingOK(t, mac, "nas", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	waitPath(t, mac, nas, "direct", 5*time.Second)

	var mu sync.Mutex
	shown := map[string]bool{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			mu.Lock()
			shown[string(pathOf(mac, build).Kind)] = true
			shown[string(pathOf(mac, nas).Kind)] = true
			mu.Unlock()
		}
	}()
	t0 := time.Now()
	mac.e.netChanged("test")
	waitPath(t, mac, build, "direct", 10*time.Second)
	waitPath(t, mac, nas, "direct", 10*time.Second)
	t.Logf("both direct again %s after the change", time.Since(t0).Round(time.Millisecond))
	close(stop)
	<-done
	pingOK(t, mac, "build", 2*time.Second)
	mu.Lock()
	defer mu.Unlock()
	for k := range shown {
		if k != "direct" && k != "connecting" && k != "relay" {
			t.Errorf("a line showed %q during the recovery", k)
		}
	}
	t.Logf("shown meanwhile: %v", shown)
}

// TestPauseLeavesOtherLANAloneAndResumesFast is M2's pause: pausing
// 周末联机 (weekend) does nothing to a long connection in 工作室 (studio),
// cuts the weekend peer off completely, and on resume the peer answers
// within 0.5 s, its WireGuard keys and paths having been kept.
func TestPauseLeavesOtherLANAloneAndResumesFast(t *testing.T) {
	w, mac, build, nas := newWorld(t)
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	pingOK(t, mac, "nas", 5*time.Second)
	waitPath(t, mac, nas, "direct", 5*time.Second)
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(time.Second)

	ctx := context.Background()
	t0 := time.Now()
	if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: true}); err != nil {
		t.Fatal(err)
	}
	w.paused = map[*tnode]map[proto.ScopeID]bool{mac: {"lan/weekend": true}}
	w.push() // the server takes the Mac out of nas's map
	if err := pingFails(t, mac, "nas"); err == nil {
		t.Fatal("the Mac still reaches nas with 周末联机 paused")
	}
	if sum, err := nas.e.Ping(ctx, "mac", 2, 100*time.Millisecond, 400*time.Millisecond, nil); err == nil && sum.Received != 0 {
		t.Fatalf("nas reached the Mac through a paused LAN: %+v", sum)
	}
	if k := pathOf(mac, nas).Kind; k != apitype.PathPaused {
		t.Errorf("nas shows %s on the Mac while paused", k)
	}
	time.Sleep(3 * time.Second)
	if n, gap := stream.since(t0); n < 30 || gap > 300*time.Millisecond {
		t.Fatalf("the studio connection got %d lines in the pause, longest gap %s", n, gap)
	}

	t1 := time.Now()
	if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: false}); err != nil {
		t.Fatal(err)
	}
	go func() { // the server puts the Mac back into nas's map a little later
		time.Sleep(50 * time.Millisecond)
		w.paused = nil
		w.push()
	}()
	pingOK(t, mac, "nas", 2*time.Second)
	if d := time.Since(t1); d > 500*time.Millisecond {
		t.Fatalf("nas answered %s after the resume, want within 0.5 s", d)
	} else {
		t.Logf("nas answered %s after the resume", d.Round(100*time.Microsecond))
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.broken != nil {
		t.Fatalf("the studio connection broke: %v", stream.broken)
	}
}

// TestServiceUsageAndCut: build knows the Mac is using its service, and
// when build closes it, the use comes back as cut: the line where scene 5
// runs its red light. The path also says what it was before.
func TestServiceUsageAndCut(t *testing.T) {
	w, mac, build, _ := newWorld(t)
	tick := proto.Service{Kind: proto.ServiceOther, Name: "计时流", Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}
	w.open["lan/studio"][build] = []proto.Service{tick}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	if from := pathOf(mac, build).From; from != apitype.PathRelay && from != apitype.PathConnecting {
		t.Errorf("direct path came from %q", from)
	}
	_, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(500 * time.Millisecond)

	usage := build.e.State().Usage
	if len(usage) != 1 || usage[0].DeviceID != apitype.DeviceID(mac.id) || usage[0].Port != 7100 ||
		usage[0].Name != "计时流" || len(usage[0].Scopes) != 1 || usage[0].Scopes[0] != "lan/studio" {
		t.Fatalf("build's usage %+v, want the Mac on 计时流 in studio", usage)
	}

	cuts := make(chan []apitype.ServiceUse, 1)
	unsub := build.e.Subscribe(func(ev apitype.Event) {
		if ev.Type == apitype.EventCut {
			cuts <- ev.Cut
		}
	})
	defer unsub()
	w.open["lan/studio"][build] = nil // build closes it
	w.push()
	select {
	case c := <-cuts:
		if len(c) != 1 || c[0].DeviceID != apitype.DeviceID(mac.id) || c[0].Name != "计时流" {
			t.Fatalf("cut %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no cut event when the service in use was closed")
	}
	if u := build.e.State().Usage; len(u) != 0 {
		t.Fatalf("usage after closing: %+v", u)
	}
}

// TestNormalLineDoesNotFlap: on an ordinary line, a few lost pings (3% at
// random) or one short hiccup (a second of nothing) must not move a direct
// path to the relay. The 11-08 kernel round saw a first version flap on
// late pongs every 20 s.
func TestNormalLineDoesNotFlap(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	_, stop := startLineStream(t, mac, build, 7100) // traffic: pings four times a second
	defer stop()

	var mu sync.Mutex
	var kinds []string
	done := make(chan struct{})
	watch := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-watch:
				return
			case <-time.After(50 * time.Millisecond):
			}
			st, _ := mac.e.ms.Status(k)
			mu.Lock()
			if len(kinds) == 0 || kinds[len(kinds)-1] != st.Kind {
				kinds = append(kinds, st.Kind)
			}
			mu.Unlock()
		}
	}()
	mac.e.ms.SetLoss(k, 0.015, 0.015) // an ordinary Wi-Fi: a few pings lost at random
	time.Sleep(12 * time.Second)
	mac.e.ms.SetLoss(k, 0, 0)
	time.Sleep(8 * time.Second)  // out of the window
	mac.e.ms.SetBlocked(k, true) // one hiccup: a second of nothing
	time.Sleep(time.Second)
	mac.e.ms.SetBlocked(k, false)
	time.Sleep(8 * time.Second)
	close(watch)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(kinds) != 1 || kinds[0] != "direct" {
		t.Fatalf("the path went %v on an ordinary line, want direct throughout", kinds)
	}
}
