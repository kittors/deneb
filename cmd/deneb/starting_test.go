package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// startingRig is the demo's window at its usual size after sims, as the
// demo plays them: "control-out" has this computer's engine answering
// without the person's view, the control server out of reach
// (engine-restart-offline.md §2.3).
func startingRig(t *testing.T, sims ...string) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	for _, s := range sims {
		d.Simulate(s)
	}
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.run(100 * time.Millisecond)
	return r
}

// middle is the middle of b.
func middle(b ui.Rect) (float32, float32) { return b.X + b.W/2, b.Y + b.H/2 }

// TestStartingOutOfReach: the starting page with the control server out of
// reach and no view to show (engine-restart-offline.md §2.3). For 10 s it
// says "正在连接 Deneb…", alone. Then "连不上 Deneb 服务器，正在重试…"
// where that was, its middle where that one's was; under it
// "已经连上的设备照常能用" while devices are connected by the network map
// kept, and not without; and the button to the diagnostics, under the words.
func TestStartingOutOfReach(t *testing.T) {
	for _, tc := range []struct {
		sim       string
		connected bool
	}{{"control-out", true}, {"control-out-alone", false}} {
		t.Run(tc.sim, func(t *testing.T) {
			r := startingRig(t, tc.sim)
			was, ok := r.tt.Find("正在连接 Deneb…")
			if !ok {
				t.Fatalf("the starting page shows %q", r.tt.Texts())
			}
			r.run(9 * time.Second) // 9.1 s in
			if r.has(startNoControl) || r.has(startDiagLabel) || r.has("已经连上的设备照常能用") {
				t.Errorf("before 10 s: %q", r.tt.Texts())
			}
			if now, _ := r.tt.Find("正在连接 Deneb…"); now != was {
				t.Errorf("the line moved before 10 s: %v, then %v", was, now)
			}
			r.run(1500 * time.Millisecond) // 10.6 s in: the words faded across
			if !r.has(startNoControl) || r.has("正在连接 Deneb…") {
				t.Fatalf("past 10 s: %q", r.tt.Texts())
			}
			line, _ := r.tt.Find(startNoControl)
			wx, wy := middle(was)
			lx, ly := middle(line)
			if abs32(wx-lx) > 0.5 || abs32(wy-ly) > 0.5 {
				t.Errorf("the line's middle moved: (%.1f, %.1f), then (%.1f, %.1f)", wx, wy, lx, ly)
			}
			works, shows := r.tt.Find("已经连上的设备照常能用")
			if shows != tc.connected {
				t.Errorf("已经连上的设备照常能用 shows: %v, want %v (%q)", shows, tc.connected, r.tt.Texts())
			}
			button, ok := r.tt.Find(startDiagLabel)
			if !ok {
				t.Fatalf("no %s: %q", startDiagLabel, r.tt.Texts())
			}
			over := line
			if shows {
				if gap := works.Y - (line.Y + line.H); abs32(gap-4) > 0.5 {
					t.Errorf("已经连上的设备照常能用 is %.1f under the line, want 4", gap)
				}
				over = works
			}
			// The line is its ring and its words, centered as one
			// (waitingWords): the button under the middle of the two.
			if bx, _ := middle(button); abs32(bx-(lx-(waitRing+waitRoom)/2)) > 0.5 {
				t.Errorf("the button's middle is at %.1f, the line's (its ring and words) at %.1f", bx, lx-(waitRing+waitRoom)/2)
			}
			if button.Y <= over.Y+over.H {
				t.Errorf("the button %v is not under the words %v", button, over)
			}
		})
	}
}

// TestStartingNoNetwork: this computer without a network says so past 10 s,
// as the window does anywhere else (DESIGN §6.10), and never 连不上: the
// trouble is here, not over there. No button: there is nothing to look
// into.
func TestStartingNoNetwork(t *testing.T) {
	r := startingRig(t, "control-out", "nonet")
	r.run(10500 * time.Millisecond)
	if !r.has(startNoNetwork) || r.has("连不上") || r.has(startDiagLabel) || r.has("已经连上的设备") {
		t.Errorf("without a network: %q", r.tt.Texts())
	}
}

// TestStartingToTheView: the view coming ends the starting page, before
// 10 s or after, one thing at a time (DESIGN §5): the page fades out as it
// is, nothing new on it and no LAN yet; then the LAN's window comes up
// through the background, the curtain lifting off it, and nothing of the
// starting page is left.
func TestStartingToTheView(t *testing.T) {
	for _, wait := range []time.Duration{3 * time.Second, 12 * time.Second} {
		r := startingRig(t, "control-out")
		r.run(wait)
		was := r.tt.Texts()
		r.d.Simulate("control-back")
		r.tt.Advance(16 * time.Millisecond)
		r.tt.Frame()
		if r.has("周末联机") || r.has("正在连接 Deneb…") && wait > startingPatience {
			t.Errorf("after %v, the view come, fading out: %q, was %q", wait, r.tt.Texts(), was)
		}
		r.run(swapOut + 50*time.Millisecond)
		for _, s := range []string{"正在连接 Deneb…", startNoControl, startDiagLabel, "已经连上的设备照常能用"} {
			if r.has(s) {
				t.Errorf("after %v, the page gone: %q still shows", wait, s)
			}
		}
		if !r.has("周末联机") {
			t.Errorf("after %v, the page gone: no LAN: %q", wait, r.tt.Texts())
		}
		if r.a.curtain == nil {
			t.Errorf("after %v: the window came with no curtain lifting off it", wait)
		}
		r.run(curtainLift + 100*time.Millisecond)
		if r.a.curtain != nil {
			t.Errorf("after %v: the curtain still over the window", wait)
		}
	}
}

// TestStartingDiagnostics: the button has the diagnostics package put
// together, "正在收集…" turning in its place; then the toast says where it
// went, as from the card (DESIGN §6.14), at the bottom of the window, under
// the page's words. So too when the window drew its canvas before, this
// computer's card open: none of that is there to keep the toast from.
func TestStartingDiagnostics(t *testing.T) {
	for _, drawn := range []bool{false, true} {
		var r *m3rig
		if drawn {
			r = startingRig(t)
			r.run(1200 * time.Millisecond)
			r.d.Simulate("control-out")
		} else {
			r = startingRig(t, "control-out")
		}
		r.run(10500 * time.Millisecond)
		r.click(startDiagLabel)
		if !r.has("正在收集…") || r.has(startDiagLabel) {
			t.Errorf("canvas drawn before %v, while it is put together: %q", drawn, r.tt.Texts())
		}
		r.run(1500 * time.Millisecond)
		if !r.has("诊断包存到了「下载」") || !r.has(startDiagLabel) {
			t.Fatalf("canvas drawn before %v, once put together: %q", drawn, r.tt.Texts())
		}
		toast, _ := r.tt.Find("诊断包存到了「下载」")
		line, _ := r.tt.Find(startNoControl)
		if toast.Y < line.Y+line.H {
			t.Errorf("canvas drawn before %v: the toast %v is not under the page's words %v", drawn, toast, line)
		}
		// As low as on a canvas with no tray: the page has none to keep over.
		want := 740 - toastBottom(false) - toastHeight/2
		if _, ty := middle(toast); abs32(ty-want) > 1 {
			t.Errorf("canvas drawn before %v: the toast's middle at %.1f, want %.1f", drawn, ty, want)
		}
	}
}

// TestNoteWait: the wait counts from the store's own snapshot, from the
// first in which this computer's engine answers, logged in, without the
// person's view and without the control server; any other ends it.
func TestNoteWait(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 13, 23, 49, 0, time.UTC)
	a := &app{clock: t0}
	out := &model.Snapshot{Engine: true, LoggedIn: true, Version: 7}
	a.noteWait(out)
	a.clock = t0.Add(5 * time.Second)
	a.noteWait(out)
	if a.waitSince != t0 {
		t.Fatalf("waiting since %v, want %v", a.waitSince, t0)
	}
	for _, s := range []*model.Snapshot{
		nil,
		{Engine: false, LoggedIn: true},
		{Engine: true, LoggedIn: false},
		{Engine: true, LoggedIn: true, Control: true},
		{Engine: true, LoggedIn: true, Loaded: true},
	} {
		a.waitSince = t0
		a.noteWait(s)
		if !a.waitSince.IsZero() {
			t.Errorf("%+v: still waiting", s)
		}
	}
}

// TestStartingAfterAHeldSwap: the engine changed for the one shipped comes
// back without its view, the control server out of reach; the window holds
// its picture meanwhile (engineswap.go), and past 60 s shows what is. That
// says the server is out of reach at once: the 10 s counted meanwhile.
func TestStartingAfterAHeldSwap(t *testing.T) {
	r := startingRig(t)
	r.run(500 * time.Millisecond)
	r.swap(false)
	r.run(500 * time.Millisecond)
	r.d.Simulate("engine-away")
	r.run(time.Second)
	r.d.Simulate("control-out")
	r.run(30 * time.Second)
	if !r.has("周末联机") || r.has(startNoControl) {
		t.Fatalf("held, 30 s in: %q", r.tt.Texts())
	}
	for i := 0; i < 40*60 && r.has("周末联机"); i++ {
		r.tt.Advance(16 * time.Millisecond)
		r.tt.Frame()
		if r.has("正在连接 Deneb…") {
			t.Fatalf("the picture let go to 正在连接 Deneb…, not to what is: %q", r.tt.Texts())
		}
	}
	if !r.has(startNoControl) {
		t.Errorf("past the hold: %q", r.tt.Texts())
	}
}
