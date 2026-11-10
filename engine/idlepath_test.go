package engine

import (
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// onRelayThenIdle: UDP between n and peer blocked on n's side, used until n
// shows the relay, then left alone until it idles that way.
func onRelayThenIdle(t *testing.T, n, peer *tnode) {
	t.Helper()
	k := peer.node().NodeKey
	n.e.ms.SetBlocked(k, true)
	deadline := time.Now().Add(15 * time.Second)
	for {
		n.e.Ping(context.Background(), peer.name, 1, 0, 300*time.Millisecond, nil)
		if st, _ := n.e.ms.Status(k); st.Kind == "relay" {
			break
		}
		if time.Now().After(deadline) {
			st, _ := n.e.ms.Status(k)
			t.Fatalf("%s -> %s: never went to the relay with UDP blocked: %+v", n.name, peer.name, st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for {
		st, _ := n.e.ms.Status(k)
		if st.Idle && st.Kind == "relay" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s -> %s: never idled on the relay: %+v", n.name, peer.name, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitIdlePath waits for n to show kind for peer while the line stays idle:
// an answer changed what it shows, not traffic.
func waitIdlePath(t *testing.T, n, peer *tnode, kind string, within time.Duration) {
	t.Helper()
	k := peer.node().NodeKey
	deadline := time.Now().Add(within)
	for {
		st, _ := n.e.ms.Status(k)
		if st.Kind == kind {
			if !st.Idle {
				t.Fatalf("%s -> %s: %s, but the line woke: traffic changed it, not an answer", n.name, peer.name, kind)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s -> %s: shows %q while idle, want %q: %+v", n.name, peer.name, st.Kind, kind, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A peer that comes back while the line is idle shows it. 11-09 19:29 this
// Mac woke while the test Mac still slept, and showed it "unreachable"; the
// test Mac woke and called at 19:30:38, its direct path answered, and the
// screen went on saying "unreachable" until someone pinged it at 19:34.
func TestIdlePeerThatCallsShowsItsPath(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.IdleReprobe = time.Hour })
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	onRelayThenIdle(t, mac, build)
	mac.e.ms.SetBlocked(build.node().NodeKey, false)
	time.Sleep(time.Second)
	if st, _ := mac.e.ms.Status(build.node().NodeKey); st.Kind != "relay" {
		t.Fatalf("nothing answered yet, and the line shows %q", st.Kind)
	}
	// build wakes: it probes everyone, and its call-me-maybe draws mac's pings.
	build.e.ms.ResetPaths("test: build woke")
	waitIdlePath(t, mac, build, "direct", 5*time.Second)
}

// An idle line shown on the relay finds its direct path again by itself,
// with a few pings and nobody using it. 11-09 19:29 the desktop's direct
// path dropped right after this Mac woke; the line idled on the relay and
// said "via the relay" until someone used it.
func TestIdleLineOnTheRelayFindsDirectAgain(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.Idle = 1500 * time.Millisecond; o.IdleReprobe = time.Second })
	kb := build.node().NodeKey
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	onRelayThenIdle(t, mac, build)
	// Still blocked: asked again 1 s after it idled, then 2 s later, then
	// 4 s later. A few pings, and still the relay.
	st0, _ := mac.e.ms.Status(kb)
	time.Sleep(5 * time.Second)
	st1, _ := mac.e.ms.Status(kb)
	pings := st1.PingsTx - st0.PingsTx
	t.Logf("idle on the relay for 5 s: %d pings", pings)
	if st1.Kind != "relay" || !st1.Idle {
		t.Fatalf("blocked and idle: %+v", st1)
	}
	if pings == 0 || pings > 20 {
		t.Fatalf("%d pings in 5 s of idle, want a few", pings)
	}
	mac.e.ms.SetBlocked(kb, false)
	t0 := time.Now()
	waitIdlePath(t, mac, build, "direct", 10*time.Second)
	t.Logf("direct again %s after the line came back, still idle", time.Since(t0).Round(10*time.Millisecond))
}

func hasProblem(n *tnode, code string) bool {
	return slices.ContainsFunc(n.e.State().Engine.Problems, func(p apitype.Problem) bool { return p.Code == code })
}

// The system refusing packets to the local network shows as a problem, and
// goes once a device there answers. 11-09: macOS's local network privacy
// refused both test Macs' pings to 192.168.1.x ("no route to host", which
// the engine dropped), and the only sign was a hairpin through the home's
// public address that held a few seconds at a time.
func TestLocalNetworkRefusedIsReported(t *testing.T) {
	_, mac, build, _ := newWorld(t)
	kb := build.node().NodeKey
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	st, _ := mac.e.ms.Status(kb)
	pfx := netip.PrefixFrom(st.Endpoint.Addr(), 32) // build's address counts as the LAN
	mac.e.ms.RefuseLocalForTest(pfx, true)
	mac.e.ms.ResetPaths("test: probe again")
	waitFor(t, "the problem", 5*time.Second, func() bool { return hasProblem(mac, apitype.ProblemLocalNetwork) })
	if st, _ := mac.e.ms.Status(kb); !st.LocalRefused {
		t.Fatalf("build's path does not say its local address was refused: %+v", st)
	}
	pingOK(t, mac, "build", 10*time.Second) // the long way: through the relay
	mac.e.ms.RefuseLocalForTest(pfx, false)
	mac.e.ms.ResetPaths("test: probe again")
	waitFor(t, "the problem to go", 10*time.Second, func() bool { return !hasProblem(mac, apitype.ProblemLocalNetwork) })
	waitPath(t, mac, build, "direct", 10*time.Second)
}

func waitFor(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s after %s", what, within)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A direct path that answers a moment and then drops, again and again,
// is left alone for a while after its second drop: the line stays on the
// relay instead of going direct, relay, direct (11-09: the hairpin through
// the home's public address answered a few seconds at a time). Once the
// backoff runs out it is taken up again. A path that dropped once comes
// back at once (TestDirectLostFallsBackToRelay, M2 item 2).
func TestFlakyDirectPathBacksOff(t *testing.T) {
	_, mac, build, _ := newWorld(t, func(o *Options) { o.FlakyBackoff = 5 * time.Second })
	kb := build.node().NodeKey
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { // traffic all along: the line never idles
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
			mac.e.Ping(context.Background(), "build", 1, 0, 300*time.Millisecond, nil)
		}
	}()
	defer func() { close(stop); <-done }()
	flap := func() {
		t.Helper()
		waitPath(t, mac, build, "direct", 10*time.Second)
		time.Sleep(time.Second) // up a moment,
		mac.e.ms.SetBlocked(kb, true)
		waitPath(t, mac, build, "relay", 10*time.Second) // then nothing until Hold runs out,
		mac.e.ms.SetBlocked(kb, false)                   // and answering again at once
	}
	flap() // the first drop: back at once
	flap() // the second: backed off
	t0 := time.Now()
	for time.Since(t0) < 4*time.Second {
		st, _ := mac.e.ms.Status(kb)
		if st.Kind != "relay" {
			t.Fatalf("%s again %s after the second drop, although backed off", st.Kind, time.Since(t0).Round(100*time.Millisecond))
		}
		if len(st.BackedOff) != 1 {
			t.Fatalf("backed off: %v, want build's address", st.BackedOff)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitPath(t, mac, build, "direct", 6*time.Second) // the backoff ran out
	t.Logf("direct again %s after the second drop", time.Since(t0).Round(100*time.Millisecond))
}
