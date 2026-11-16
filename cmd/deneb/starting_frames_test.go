package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestStartingFrames records the starting page with no view to show, the
// control server out of reach (engine-restart-offline.md §2.3), both
// themes, when DENEB_STARTING names a folder: each scene's frames, a 60th of
// a second apart, in <theme>-<scene>/, and its stills beside them.
//
//   - wait: from the window opening on 正在连接 Deneb…, through the words
//     changing at 10 s, to the view coming;
//   - alone: the same with no device connected, the line under the words
//     not there;
//   - button: the button to the diagnostics under the pointer, pressed, the
//     package put together, and the toast saying where it went.
func TestStartingFrames(t *testing.T) {
	root := os.Getenv("DENEB_STARTING")
	if root == "" {
		t.Skip("set DENEB_STARTING to a folder to record the starting page")
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]

		r := startingRec(t, root, theme, dark, "wait", 0, "control-out")
		r.run(time.Second)
		r.shot("connecting")
		r.run(startingPatience - time.Second)
		r.run(600 * time.Millisecond)
		r.shot("out")
		r.run(2400 * time.Millisecond)
		r.d.Simulate("control-back")
		r.run(1500 * time.Millisecond)
		r.shot("back")

		r = startingRec(t, root, theme, dark, "alone", startingPatience-2*time.Second, "control-out-alone")
		r.run(2600 * time.Millisecond)
		r.shot("out")
		r.run(1400 * time.Millisecond)
		r.d.Simulate("control-back")
		r.run(1500 * time.Millisecond)

		r = startingRec(t, root, theme, dark, "button", startingPatience+600*time.Millisecond, "control-out")
		b, ok := r.tt.Find(startDiagLabel)
		if !ok {
			t.Fatalf("no %s: %q", startDiagLabel, r.tt.Texts())
		}
		bx, by := b.X+b.W/2, b.Y+b.H/2
		// The pointer comes to it from where it rested, as a hand does.
		r.run(400 * time.Millisecond)
		fx, fy := r.px, r.py
		for i := 1; i <= 18; i++ {
			k := ui.EaseInOut(float32(i) / 18)
			r.move(fx+(bx-fx)*k, fy+(by-fy)*k)
			r.run(16 * time.Millisecond)
		}
		r.run(400 * time.Millisecond)
		r.shot("hover")
		r.tt.Press(bx, by)
		r.run(160 * time.Millisecond)
		r.shot("press")
		r.tt.Release(bx, by)
		r.run(500 * time.Millisecond)
		r.shot("busy")
		r.run(1200 * time.Millisecond)
		r.shot("saved")
		for i := 1; i <= 18; i++ {
			k := ui.EaseInOut(float32(i) / 18)
			r.move(bx+(fx-bx)*k, by+(fy-by)*k)
			r.run(16 * time.Millisecond)
		}
		r.run(1500 * time.Millisecond)
	}
}

// startingRec is the demo's window at its usual size after sims, recording
// into <theme>-<key>/ from lead on: what comes before is not in the frames.
// The pointer rests at the bottom right, out of the way.
func startingRec(t *testing.T, root, theme string, dark bool, key string, lead time.Duration, sims ...string) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	for _, s := range sims {
		d.Simulate(s)
	}
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.tt.SetDark(dark)
	r.move(1000, 650)
	r.run(lead + 16*time.Millisecond)
	r.dir = filepath.Join(root, theme+"-"+key)
	r.still = r.dir
	reset(t, r.dir)
	return r
}
