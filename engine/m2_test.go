package engine

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/proto"
	"golang.zx2c4.com/wireguard/device"
)

// TestIdleStopsHeartbeatAndWakesFast is M2's "heartbeat only with traffic"
// with its guard: an idle peer gets no pings and keeps the path it showed,
// and the first packet after idle is answered within 0.2 s.
func TestIdleStopsHeartbeatAndWakesFast(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.PathByLoss = true })
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
	// The paths show the relay once the hold runs out: 6.5 s after the last
	// pong (the relay copies have carried the traffic since 2.5 s).
	waitPath(t, mac, build, "relay", 9*time.Second)
	waitPath(t, build, mac, "relay", 9*time.Second)
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
	w.push() // nas's map now has the Mac with 周末联机 under PausedScopes
	if err := pingFails(t, mac, "nas"); err == nil {
		t.Fatal("the Mac still reaches nas with 周末联机 paused")
	}
	if sum, err := nas.e.Ping(ctx, "mac", 2, 100*time.Millisecond, 400*time.Millisecond, nil); err == nil && sum.Received != 0 {
		t.Fatalf("nas reached the Mac through a paused LAN: %+v", sum)
	}
	if k := pathOf(mac, nas).Kind; k != apitype.PathPaused {
		t.Errorf("the Mac shows %s on nas while paused", k)
	}
	// nas keeps the Mac warm (keys and path) but shows it as paused.
	if k := pathOf(nas, mac).Kind; k != apitype.PathPaused {
		t.Errorf("nas shows %s on the Mac while the Mac paused", k)
	}
	if _, ok := nas.e.ms.Status(mac.node().NodeKey); !ok {
		t.Error("nas forgot the Mac while it paused: its resume would need a new handshake")
	}
	for _, p := range nas.e.State().Peers {
		if p.ID == apitype.DeviceID(mac.id) && (len(p.Scopes) != 0 || len(p.PausedScopes) != 1) {
			t.Errorf("nas lists the Mac with scopes %v, paused %v", p.Scopes, p.PausedScopes)
		}
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
	// And the Mac's side: it is using build's tcp/7100 (the "已连接" timer).
	using := mac.e.State().Using
	if len(using) != 1 || using[0].DeviceID != apitype.DeviceID(build.id) || using[0].Port != 7100 ||
		using[0].Proto != "tcp" || using[0].Since.IsZero() || len(using[0].Scopes) != 1 || using[0].Scopes[0] != "lan/studio" {
		t.Fatalf("the Mac's using %+v, want build's tcp/7100 in studio", using)
	}
	if u := build.e.State().Using; len(u) != 0 {
		t.Fatalf("build uses %+v of the Mac's, want nothing", u)
	}

	cuts := make(chan []apitype.ServiceUse, 1)
	unsub := build.e.Subscribe(func(ev apitype.Event) {
		if ev.Type == apitype.EventCut {
			cuts <- ev.Cut
		}
	})
	defer unsub()
	w.open["lan/studio"][build] = nil // build closes it
	closedAt := time.Now()
	w.push()
	select {
	case c := <-cuts:
		if len(c) != 1 || c[0].DeviceID != apitype.DeviceID(mac.id) || c[0].Name != "计时流" {
			t.Fatalf("cut %+v", c)
		}
		t.Logf("cut event %s after the service was closed", time.Since(closedAt).Round(100*time.Microsecond))
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

// TestHiccupKeepsDirectPath: UDP between two devices goes silent for longer
// than Trust but less than Hold, which a long line does about once a minute
// (two pings in a row lost: kernel round 3 saw the paths jump for it, and
// M1 engines on the real machines jump the same way). The path shown stays
// direct throughout, a long connection carries on through the relay copies
// sent meanwhile, and once UDP answers again the copies stop.
func TestHiccupKeepsDirectPath(t *testing.T) {
	w, mac, build, _ := newWorld(t)
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(time.Second)

	var mu sync.Mutex
	var kinds []string
	held := false
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
			held = held || st.Held
			mu.Unlock()
		}
	}()
	t0 := time.Now()
	mac.e.ms.SetBlocked(k, true) // 4 s of nothing: past Trust (2.5 s), short of Hold (6.5 s)
	time.Sleep(4 * time.Second)
	mac.e.ms.SetBlocked(k, false)
	t1 := time.Now()
	deadline := t1.Add(2 * time.Second)
	for {
		if st, _ := mac.e.ms.Status(k); !st.Held && st.Kind == "direct" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still held 2 s after UDP came back")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("direct confirmed again %s after UDP came back", time.Since(t1).Round(time.Millisecond))
	// Not counting the small packets copied just after the peer called
	// (cmm, sendWG): those stop on their own within wakeDual.
	st, _ := mac.e.ms.Status(k)
	relayed := st.TxRelay - st.TxWake
	time.Sleep(2 * time.Second)
	close(watch)
	<-done
	if st, _ = mac.e.ms.Status(k); st.TxRelay-st.TxWake != relayed {
		t.Errorf("%d packets still went through the relay after the direct path answered", st.TxRelay-st.TxWake-relayed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(kinds) != 1 || kinds[0] != "direct" || !held {
		t.Fatalf("the path went %v (held=%v) through a 4 s hiccup, want direct throughout and held", kinds, held)
	}
	n, gap := stream.since(t0)
	stream.mu.Lock()
	broken := stream.broken
	stream.mu.Unlock()
	t.Logf("long connection: %d lines since the hiccup began, longest gap %s", n, gap.Round(time.Millisecond))
	if broken != nil || n < 50 || gap > 3500*time.Millisecond {
		t.Fatalf("long connection: %d lines, longest gap %s, broken %v", n, gap, broken)
	}
}

// TestPauseSparesPeerInOtherLAN: build is in both LANs, as on the real
// machines on 11-08. Pausing 周末联机 and resuming it must not touch the
// Mac's session or path with build, which 工作室 still carries: the long
// connection to build through 工作室 does not stall at all, and build's
// path is never reset (kernel round on 11-08 14:18 saw a 1.5 s stall on
// that connection 1.1 s after a resume; this says it was not the engine).
func TestPauseSparesPeerInOtherLAN(t *testing.T) {
	w, mac, build, nas := newWorld(t)
	w.members["lan/weekend"] = []*tnode{mac, nas, build}
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	pingOK(t, mac, "nas", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	waitPath(t, mac, nas, "direct", 5*time.Second)
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(time.Second)
	before, _ := mac.e.ms.Status(build.node().NodeKey)

	ctx := context.Background()
	t0 := time.Now()
	for i := 0; i < 3; i++ { // a few times over, as someone flicking the switch would
		if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: true}); err != nil {
			t.Fatal(err)
		}
		w.paused = map[*tnode]map[proto.ScopeID]bool{mac: {"lan/weekend": true}}
		w.push()
		time.Sleep(700 * time.Millisecond)
		if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: false}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond) // the server's map comes a moment later
		w.paused = nil
		w.push()
		pingOK(t, mac, "nas", 2*time.Second)
		time.Sleep(700 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	n, gap := stream.since(t0)
	stream.mu.Lock()
	broken := stream.broken
	stream.mu.Unlock()
	t.Logf("工作室's connection to build through three pauses of 周末联机: %d lines, longest gap %s", n, gap.Round(time.Millisecond))
	if broken != nil || gap > 300*time.Millisecond {
		t.Fatalf("工作室's connection to build: %d lines, longest gap %s, broken %v", n, gap, broken)
	}
	after, _ := mac.e.ms.Status(build.node().NodeKey)
	if after.Kind != "direct" || !after.Since.Equal(before.Since) {
		t.Fatalf("build's path was touched: %s since %s, was %s since %s", after.Kind, after.Since, before.Kind, before.Since)
	}
}

// TestSmallPacketsTwiceOnLossyLine: a direct line losing 20% of packets at
// random both ways (the home line to Hong Kong loses 14%, TCP alike, so
// the relay does not help). Sent twice, small packets are lost only when
// both copies are, and a long connection of small lines stalls on a
// retransmission much less often.
func TestSmallPacketsTwiceOnLossyLine(t *testing.T) {
	if testing.Short() {
		t.Skip("half a minute of a lossy line")
	}
	w, mac, build, _ := newWorld(t)
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k, kb := build.node().NodeKey, mac.node().NodeKey
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(time.Second)

	// lost is the time the stream stood still beyond its 100 ms rhythm.
	stalls := func(from, to time.Time) (n int, worst, lost time.Duration) {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		last := from
		for _, a := range stream.arrived {
			if a.Before(from) || a.After(to) {
				continue
			}
			g := a.Sub(last)
			if g > 300*time.Millisecond {
				n++
			}
			if g > 150*time.Millisecond {
				lost += g - 100*time.Millisecond
			}
			worst = max(worst, g)
			last = a
		}
		return n, worst, lost
	}
	run := func(mode string) (int, time.Duration, time.Duration) {
		mac.e.ms.SetDupSmall(mode)
		build.e.ms.SetDupSmall(mode)
		mac.e.ms.SetLoss(k, 0.2, 0.2) // both directions, as the line does
		t0 := time.Now()
		time.Sleep(15 * time.Second)
		mac.e.ms.SetLoss(k, 0, 0)
		t1 := time.Now()
		time.Sleep(3 * time.Second) // let it catch up before the next run
		return stalls(t0, t1)
	}
	offN, offWorst, offLost := run("off")
	onN, onWorst, onLost := run("on")
	st, _ := mac.e.ms.Status(k)
	sb, _ := build.e.ms.Status(kb)
	t.Logf("20%% loss for 15 s: sent once, %d stalls over 0.3 s, worst %s, %s stood still; twice, %d, worst %s, %s; sent twice: Mac %d, build %d",
		offN, offWorst.Round(time.Millisecond), offLost.Round(time.Millisecond), onN, onWorst.Round(time.Millisecond), onLost.Round(time.Millisecond), st.TxDup, sb.TxDup)
	if st.TxDup == 0 || sb.TxDup == 0 {
		t.Fatal("nothing was sent twice")
	}
	if offLost > 2*time.Second && onLost*2 > offLost {
		t.Fatalf("sending twice did not help: stood still %s against %s", onLost, offLost)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.broken != nil {
		t.Fatalf("the long connection broke: %v", stream.broken)
	}
}

// TestBothPathsLossyStaysDirect: the direct path loses 20% of its UDP, and
// the relay's answers come late a quarter of the time, as on the home line
// to Hong Kong where TCP is lost too. The relay is no cleaner, so the path
// stays direct: moving would only add the detour (and TCP over TCP).
func TestBothPathsLossyStaysDirect(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	_, stop := startLineStream(t, mac, build, 7100) // traffic: the line is judged closely
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
	mac.e.ms.SetRelayLate(k, 0.25, 400*time.Millisecond)
	mac.e.ms.SetLoss(k, 0.2, 0.2)
	time.Sleep(15 * time.Second)
	mac.e.ms.SetLoss(k, 0, 0)
	mac.e.ms.SetRelayLate(k, 0, 0)
	close(watch)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(kinds, "relay") {
		t.Fatalf("the path went %v with both the direct path and the relay bad, want direct throughout", kinds)
	}
}

// TestBusyPathFailsOverFast is M2 acceptance item 2 on the data plane:
// UDP to a peer stops while a long connection runs over it (traffic, so
// the path is pinged four times a second). The relay copies start once
// four pings in a row are missed, so the connection stalls well under M1's
// 2.6 s; the path is shown as the relay after 3 s of silence. A 1.5 s
// hiccup on the same busy line is held: shown direct throughout.
func TestBusyPathFailsOverFast(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k, kb := build.node().NodeKey, mac.node().NodeKey
	stream, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	time.Sleep(2 * time.Second)

	block := func(on bool) {
		mac.e.ms.SetBlocked(k, on)
		build.e.ms.SetBlocked(kb, on)
	}
	// A hiccup: held, not switched.
	t0 := time.Now()
	block(true)
	time.Sleep(1500 * time.Millisecond)
	block(false)
	time.Sleep(time.Second)
	if st, _ := mac.e.ms.Status(k); st.Kind != "direct" || st.Prev == "relay" && st.Since.After(t0) {
		t.Fatalf("a 1.5 s hiccup on a busy line: %s (from %s)", st.Kind, st.Prev)
	}
	time.Sleep(2 * time.Second)

	// The path dies.
	t1 := time.Now()
	block(true)
	var shown time.Duration
	for shown == 0 && time.Since(t1) < 8*time.Second {
		if st, _ := mac.e.ms.Status(k); st.Kind == "relay" {
			shown = time.Since(t1)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	block(false)
	_, gap := stream.since(t1)
	t.Logf("UDP gone under a long connection: shown as the relay after %s; the connection stood still at most %s",
		shown.Round(10*time.Millisecond), gap.Round(10*time.Millisecond))
	if shown == 0 || shown > 4*time.Second {
		t.Errorf("shown as the relay after %s, want within 4 s", shown)
	}
	if gap > 2*time.Second {
		t.Errorf("the long connection stood still %s, want under 2 s (M1: 2.6 s)", gap)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.broken != nil {
		t.Fatalf("the long connection broke: %v", stream.broken)
	}
}

// TestFirstPacketAfterDeepIdle: idle long enough that the last answers are
// minutes old and WireGuard's session has run out (19:34 on the real Mac:
// the first ping to nas took 2.4 s). The first packet must go through the
// relay at once, its handshake included, and be answered within 0.2 s
// (M2: "首包 0.2 s 内应答"); the direct path comes back in the background.
func TestFirstPacketAfterDeepIdle(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.PathByLoss = true })
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	deepIdle(t, mac, build)
	start := time.Now()
	sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 3*time.Second, nil)
	first := time.Since(start)
	if err != nil || sum.Received != 1 {
		t.Fatalf("first ping after a deep idle: %v %+v", err, sum)
	}
	t.Logf("first answer after a deep idle (session gone, answers 4 min old): %s via %s", first.Round(100*time.Microsecond), sum.Results[0].Path)
	if first > 200*time.Millisecond {
		t.Fatalf("first answer after a deep idle took %s, want within 0.2 s", first)
	}
}

// deepIdle lets mac and build go idle and makes it a long idle: their last
// answers 4 minutes old, the WireGuard session run out.
func deepIdle(t *testing.T, a, b *tnode) {
	t.Helper()
	ka, kb := a.node().NodeKey, b.node().NodeKey
	time.Sleep(3 * time.Second) // both idle
	for _, x := range []struct {
		n *tnode
		k proto.NodePublic
	}{{a, kb}, {b, ka}} {
		x.n.e.ms.AgeForTest(x.k, 4*time.Minute)
		if p := x.n.e.dev.LookupPeer(device.NoisePublicKey(x.k)); p != nil {
			p.ExpireCurrentKeypairs() // the session ran out while idle
		}
	}
}

// After a long idle the handshake takes a round trip, and by its end the
// direct path has answered the wake's ping once: the first ping went
// direct only. On the line to Hong Kong both its copies were lost (19:34,
// the NAS): answered only on the retry, 2.4 s later. Just after waking,
// small packets go through the relay too.
func TestFirstExchangeAfterWakeOnLossyLine(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.PathByLoss = true })
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	deepIdle(t, mac, build)
	kb, km := build.node().NodeKey, mac.node().NodeKey
	// The line loses the first data packets each way, copies included;
	// pings and the handshake get through.
	mac.e.ms.DropDirectForTest(kb, 4)
	build.e.ms.DropDirectForTest(km, 4)
	start := time.Now()
	sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 3*time.Second, nil)
	first := time.Since(start)
	sm, _ := mac.e.ms.Status(kb)
	sb, _ := build.e.ms.Status(km)
	t.Logf("first answer %s; sent through the relay just after waking: mac %d, build %d", first.Round(100*time.Microsecond), sm.TxWake, sb.TxWake)
	if err != nil || sum.Received != 1 {
		t.Fatalf("the first ping after a deep idle, its direct copies lost, got no answer: %v %+v", err, sum)
	}
	if first > 200*time.Millisecond {
		t.Fatalf("first answer after a deep idle took %s, want within 0.2 s", first)
	}
	// Kept busy past the wake window (Idle is 1.5 s here), the direct path
	// carries them alone again.
	for end := start.Add(magicsock.WakeDual + 200*time.Millisecond); time.Now().Before(end); {
		pingOK(t, mac, "build", 2*time.Second)
		time.Sleep(400 * time.Millisecond)
	}
	before, _ := mac.e.ms.Status(kb)
	for range 3 {
		pingOK(t, mac, "build", 2*time.Second)
	}
	after, _ := mac.e.ms.Status(kb)
	if after.TxWake != before.TxWake {
		t.Fatalf("relay copies %d → %d after the wake window", before.TxWake, after.TxWake)
	}
}

// The client says "this computer has no network" from a problem the
// engine reports the moment the system says so, not after the peers time
// out one by one (about 7 s, and the lines would read "unreachable"
// first); it goes once the network is back.
func TestNoNetworkReportedAtOnce(t *testing.T) {
	_, mac, _, _ := newWorld(t)
	var off atomic.Bool
	mac.e.mu.Lock()
	mac.e.test.offline = func(string) bool { return off.Load() }
	mac.e.mu.Unlock()
	events := make(chan apitype.Event, 64)
	cancel := mac.e.Subscribe(func(ev apitype.Event) {
		select {
		case events <- ev:
		default:
		}
	})
	defer cancel()
	has := func() bool {
		return slices.ContainsFunc(mac.e.State().Engine.Problems, func(p apitype.Problem) bool { return p.Code == apitype.ProblemNoNetwork })
	}
	waitState := func(want bool) {
		t.Helper()
		deadline := time.After(time.Second)
		for {
			select {
			case ev := <-events:
				if ev.Type == apitype.EventState && slices.ContainsFunc(ev.State.Engine.Problems, func(p apitype.Problem) bool { return p.Code == apitype.ProblemNoNetwork }) == want {
					return
				}
			case <-deadline:
				t.Fatalf("no state event with no-network=%v within 1 s", want)
			}
		}
	}
	mac.e.checkOnline()
	if has() {
		t.Fatal("no-network reported with the network up")
	}
	off.Store(true)
	mac.e.checkOnline() // what the network monitor calls on each notice
	waitState(true)
	off.Store(false)
	mac.e.checkOnline()
	waitState(false)
	if has() {
		t.Fatal("no-network still reported with the network back")
	}
}

// After a resume the direct path has answered once, if at all, when the
// handshake completes: the first packets go through the relay as well,
// as after waking. Real 20:36: the first ping to the desktop after a
// resume got no answer, the next one 4 ms.
func TestFirstPacketAfterResumeOnLossyLine(t *testing.T) {
	w, mac, _, nas := newWorld(t)
	pingOK(t, mac, "nas", 5*time.Second)
	waitPath(t, mac, nas, "direct", 5*time.Second)
	ctx := context.Background()
	if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: true}); err != nil {
		t.Fatal(err)
	}
	w.paused = map[*tnode]map[proto.ScopeID]bool{mac: {"lan/weekend": true}}
	w.push()
	time.Sleep(time.Second)
	t1 := time.Now()
	if err := mac.e.Pause(ctx, apitype.PauseRequest{Scope: "lan/weekend", Paused: false}); err != nil {
		t.Fatal(err)
	}
	kn, km := nas.node().NodeKey, mac.node().NodeKey
	mac.e.ms.DropDirectForTest(kn, 2)
	nas.e.ms.DropDirectForTest(km, 2)
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.paused = nil
		w.push()
	}()
	sum, err := mac.e.Ping(ctx, "nas", 1, 0, 2*time.Second, nil)
	d := time.Since(t1)
	sm, _ := mac.e.ms.Status(kn)
	sn, _ := nas.e.ms.Status(km)
	t.Logf("first answer %s after the resume; relay copies: mac %d, nas %d", d.Round(100*time.Microsecond), sm.TxWake, sn.TxWake)
	if err != nil || sum.Received != 1 {
		t.Fatalf("the first ping after the resume, its direct copies lost, got no answer: %v %+v", err, sum)
	}
	if d > 500*time.Millisecond {
		t.Fatalf("nas answered %s after the resume, want within 0.5 s", d)
	}
}

// forgetSession makes n drop its WireGuard session with k, as a peer does
// once a sleeping machine's session gets older than WireGuard allows: the
// peer taken out of WireGuard and put back, no handshake started.
func forgetSession(t *testing.T, n *tnode, k proto.NodePublic) {
	t.Helper()
	n.e.mu.Lock()
	wp, ok := n.e.wgPeers[k]
	dev := n.e.dev
	n.e.mu.Unlock()
	if !ok {
		t.Fatalf("no WireGuard peer %x", k[:4])
	}
	cfg := fmt.Sprintf("public_key=%x\nremove=true\npublic_key=%x\nendpoint=%s\nallowed_ip=%s/32\n", k[:], k[:], magicsock.EndpointString(k), wp.addr)
	if err := dev.IpcSet(cfg); err != nil {
		t.Fatal(err)
	}
}

// After a sleep the machine's WireGuard still counts its sessions young
// (the monotonic clock stood still) while the peers, counting real time,
// dropped theirs. Test Mac 20:30, ten minutes asleep: the paths showed
// direct at once, pings went unanswered for 13-16 s. Waking drops the
// sessions, so the next packet starts a handshake; netmon's notice of the
// same sleep, seconds later, changes nothing.
func TestWakeStartsNewSessions(t *testing.T) {
	_, mac, build, _ := newWorld(t)
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	forgetSession(t, build, mac.node().NodeKey) // build dropped it during the sleep
	t0 := time.Now()
	mac.e.woke(10 * time.Minute) // what the heartbeat calls on the jump
	sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 3*time.Second, nil)
	d := time.Since(t0)
	if err != nil || sum.Received != 1 {
		t.Fatalf("no answer from build after waking: %v %+v", err, sum)
	}
	t.Logf("build answered %s after waking", d.Round(100*time.Microsecond))
	if d > 2*time.Second {
		t.Fatalf("build answered %s after waking, want within 2 s", d)
	}
	waitPath(t, mac, build, "direct", 5*time.Second)
	mac.e.woke(10*time.Minute + 6*time.Second) // netmon, about the same sleep
	time.Sleep(50 * time.Millisecond)
	if k := pathOf(mac, build).Kind; k != apitype.PathDirect {
		t.Fatalf("the second notice of the same sleep reset the path: %s", k)
	}
}

// A line moved to the relay for loss that then goes idle comes back to the
// direct path once the line is clean again, without anyone using it: the
// NAS, 13:16, showed build on the relay 16 minutes after the loss stopped,
// because an idle line has no pings to judge by. Its direct path is asked
// now and then while idle.
func TestLossyLineComesBackDirectWhileIdle(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true; o.Idle = 1500 * time.Millisecond })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	_, stop := startLineStream(t, mac, build, 7100)
	time.Sleep(time.Second)
	mac.e.ms.SetLoss(k, 0.4, 0.4) // plainly lossy: the move comes within seconds
	deadline := time.Now().Add(15 * time.Second)
	for {
		if st, _ := mac.e.ms.Status(k); st.Kind == "relay" && st.Reason == "lossy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("15 s at 40% loss and still not on the relay")
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop() // nobody uses the line now
	deadline = time.Now().Add(10 * time.Second)
	for {
		if st, _ := mac.e.ms.Status(k); st.Idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the line did not go idle")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st, _ := mac.e.ms.Status(k); st.Kind != "relay" {
		t.Fatalf("idle with the loss still on, the line shows %s, want the relay", st.Kind)
	}
	mac.e.ms.SetLoss(k, 0, 0)
	t1 := time.Now()
	deadline = t1.Add(30 * time.Second)
	for {
		st, _ := mac.e.ms.Status(k)
		if st.Kind == "direct" {
			if !st.Idle {
				t.Fatal("the line woke to come back direct: idle pings must not count as traffic")
			}
			t.Logf("direct again %s after the line was clean, still idle", time.Since(t1).Round(100*time.Millisecond))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("30 s after the loss stopped the idle line still shows %s (%s)", st.Kind, st.Reason)
		}
		time.Sleep(200 * time.Millisecond)
	}
	pingOK(t, mac, "build", 2*time.Second) // and it carries traffic direct
	if st, _ := mac.e.ms.Status(k); st.Kind != "direct" {
		t.Fatalf("traffic again moved it to %s (%s)", st.Kind, st.Reason)
	}
}

// The relay's record looks back 120 answers. After a stretch on which it
// was clean, it still says "clean" for a while once the relay goes bad,
// and with the direct path losing past the threshold the line would move
// to a relay no better than it (colima lossy-both, 11-09: after lossy-line,
// moved with the relay 3 of 25 late). The relay must be clean right now as
// well.
func TestBothPathsLossyAfterCleanRelayStaysDirect(t *testing.T) {
	w, mac, build, _ := newWorld(t, func(o *Options) { o.PathByLoss = true })
	w.open["lan/studio"][build] = []proto.Service{{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 7100, Last: 7100}}}}
	w.push()
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	k := build.node().NodeKey
	_, stop := startLineStream(t, mac, build, 7100)
	defer stop()
	// A little loss, under the threshold: the relay is asked often and its
	// record fills with clean answers.
	mac.e.ms.SetLoss(k, 0.04, 0.04)
	time.Sleep(8 * time.Second)
	if st, _ := mac.e.ms.Status(k); st.Kind != "direct" {
		t.Fatalf("a little loss moved the line to %s", st.Kind)
	}
	var mu sync.Mutex
	var kinds []string
	watch := make(chan struct{})
	done := make(chan struct{})
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
	mac.e.ms.SetRelayLate(k, 0.25, 400*time.Millisecond)
	mac.e.ms.SetLoss(k, 0.2, 0.2)
	time.Sleep(15 * time.Second)
	mac.e.ms.SetLoss(k, 0, 0)
	mac.e.ms.SetRelayLate(k, 0, 0)
	close(watch)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(kinds, "relay") {
		t.Fatalf("the path went %v with both the direct path and the relay bad, after the relay had been clean; want direct throughout", kinds)
	}
}

// After a long idle the handshake runs before the direct path is proven,
// and its messages went through the relay only: on a lossy line the relay
// waits out a lost TCP segment (20:08: the response 0.7 s late). Between
// engines that drop repeated handshake messages (proto.CapHandshakeDedupe)
// each one also goes direct, and the first packet waits for neither. An
// engine from before the capability gets them one way only, as before.
func TestHandshakeBothWaysAfterDeepIdle(t *testing.T) {
	cases := []struct {
		name     string
		old      []string // the nodes running an engine from before the capability
		fast     bool     // the relay as fast as the direct path: the two copies land together
		fastest  time.Duration
		slowest  time.Duration
		dupesMin uint64 // repeats dropped on build
	}{
		{"both claim it", nil, false, 0, 300 * time.Millisecond, 1},
		// The two copies of each message a moment apart, the case that
		// splits a session if WireGuard works on both: one is dropped.
		{"both claim it, the relay as fast", nil, true, 0, 300 * time.Millisecond, 1},
		// The first answer waits for a resend or a new handshake, two
		// rounds of 3 round trips: about 0.2 s here, where a round trip is
		// next to nothing; 0.4-0.9 s on the line to Hong Kong.
		{"neither (as before)", []string{"mac", "build"}, false, 150 * time.Millisecond, 3 * time.Second, 0},
		{"build from before", []string{"build"}, false, 0, 3 * time.Second, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.PathByLoss = true })
			for _, n := range []*tnode{mac, build} {
				if slices.Contains(c.old, n.name) {
					n.makeOld()
				}
			}
			w.push()
			pingOK(t, mac, "build", 5*time.Second)
			waitPath(t, mac, build, "direct", 5*time.Second)
			deepIdle(t, mac, build)
			kb, km := build.node().NodeKey, mac.node().NodeKey
			if !c.fast {
				// The relay leg of every handshake message waits 0.7 s, both ways.
				mac.e.ms.DelayRelayHandshakesForTest(kb, 700*time.Millisecond)
				build.e.ms.DelayRelayHandshakesForTest(km, 700*time.Millisecond)
			}
			dupes0 := build.e.ms.HandshakeDupes()
			start := time.Now()
			sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 3*time.Second, nil)
			first := time.Since(start)
			if err != nil || sum.Received != 1 {
				t.Fatalf("no answer after a deep idle: %v %+v", err, sum)
			}
			time.Sleep(time.Second) // the relay copies arrive, to be dropped
			dupes := build.e.ms.HandshakeDupes() - dupes0
			t.Logf("first answer %s; repeats dropped on build: %d", first.Round(100*time.Microsecond), dupes)
			if first < c.fastest || first > c.slowest {
				t.Fatalf("first answer after %s, want between %s and %s", first, c.fastest, c.slowest)
			}
			if dupes < c.dupesMin {
				t.Fatalf("build dropped %d repeats, want at least %d: the handshake did not come both ways", dupes, c.dupesMin)
			}
			// What debug paths shows: on only when both claim it.
			both := len(c.old) == 0
			ms, _ := mac.e.ms.Status(kb)
			bs, _ := build.e.ms.Status(km)
			if ms.HSBoth != both || (ms.TxHSBoth > 0) != both || (bs.RxHSDupes > 0) != both {
				t.Fatalf("mac to build: hsBoth %v, %d handshake messages both ways; build dropped %d from mac; want all on: %v",
					ms.HSBoth, ms.TxHSBoth, bs.RxHSDupes, both)
			}
			pingOK(t, mac, "build", 2*time.Second) // and the session holds
		})
	}
}

// The first handshake response after a long idle lost, both ways: the
// initiator starts a new handshake soon, judged by the line as it is now
// and not by the round trip it had before the idle. 11-09 build to the
// NAS: the stored 108-150 ms (13:42) on a line at 4 ms now; the new
// handshake came 0.9 s on, and the first ping took 918 ms.
func TestFirstResponseLostAfterIdleRecoversFast(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.PathByLoss = true })
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	deepIdle(t, mac, build)
	kb, km := build.node().NodeKey, mac.node().NodeKey
	mac.e.ms.SetRTTForTest(kb, 150*time.Millisecond)
	build.e.ms.DropHandshakesForTest(km, 1) // build's response to mac's first initiation
	start := time.Now()
	sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 3*time.Second, nil)
	first := time.Since(start)
	if err != nil || sum.Received != 1 {
		t.Fatalf("no answer: %v %+v", err, sum)
	}
	t.Logf("first answer after %s with the first response lost", first.Round(time.Millisecond))
	if first > 500*time.Millisecond {
		t.Fatalf("first answer after %s, want within 0.5 s: a new handshake judged by the round trip before the idle", first)
	}
}
