package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSwapFrames records the window while this computer's engine is changed
// for the one shipped (design/plan/m7/engine-self-update.md §5), both
// themes, when DENEB_SWAP names a folder: each scene's frames, a 60th of a
// second apart, in <theme>-<scene>/, and its stills beside them.
// DENEB_SWAP_ONLY picks scenes. "before" is the window as it was, without
// holding its picture: what the change looked like.
func TestSwapFrames(t *testing.T) {
	root := os.Getenv("DENEB_SWAP")
	if root == "" {
		t.Skip("set DENEB_SWAP to a folder to record the engine change")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_SWAP_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key  string
		play func(r *m3rig)
		// opening: the window just opened, the engine's first picture not
		// in yet (engine-restart-offline.md §3).
		opening bool
	}{
		{"quick", playSwapQuick, false},
		{"slow", playSwapSlow, false},
		{"rollback", playSwapRollback, false},
		{"password", playSwapPassword, false},
		{"badsig", playSwapBadSig, false},
		{"before", playSwapBefore, false},
		{"fresh", playSwapFresh, true},
		{"nocontrol", playSwapNoControl, false},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			sc.play(swapRig(t, root, theme, dark, sc.key, sc.opening))
		}
	}
}

// swapRig is the demo at the window's usual size, recording into
// <theme>-<key>/, the pointer out of the way; opening: as a window just
// opened, the engine's first picture not in yet.
func swapRig(t *testing.T, root, theme string, dark bool, key string, opening bool) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	if opening {
		d.Simulate("engine-loading")
	}
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.tt.SetDark(dark)
	r.run(1200 * time.Millisecond)
	r.dir = filepath.Join(root, theme+"-"+key)
	r.still = r.dir
	reset(t, r.dir)
	r.move(1120, 700)
	r.run(400 * time.Millisecond)
	return r
}

// swap begins holding the picture, as followEngineOnce does before it asks
// the engine to change; asked: the system's window asks for the password.
func (r *m3rig) swap(asked bool) {
	r.a.beginSwap(r.tt.Now())
	r.a.swap.asked = asked
}

// change plays the engine's change: gone for away, then back.
func (r *m3rig) change(away time.Duration) {
	r.d.Simulate("engine-away")
	r.run(away / 2)
	r.shot("away")
	r.run(away - away/2)
	r.back()
}

// back is the new engine answering: without its LANs a moment, then with
// them, every device connecting, until they are as they were.
func (r *m3rig) back() {
	r.d.Simulate("engine-loading")
	r.run(500 * time.Millisecond)
	r.d.Simulate("engine-new")
	r.run(1500 * time.Millisecond)
	r.shot("back-connecting")
	r.d.Simulate("nonet-back")
}

// The new engine is there in two seconds: nothing in the window changes.
func playSwapQuick(r *m3rig) {
	r.shot("start")
	r.swap(false)
	r.run(500 * time.Millisecond)
	r.change(2 * time.Second)
	r.run(3 * time.Second)
	r.shot("done")
}

// It takes 12 seconds: past 10, the LAN's line says 正在更新网卡….
func playSwapSlow(r *m3rig) {
	r.swap(false)
	r.run(500 * time.Millisecond)
	r.d.Simulate("engine-away")
	r.run(9 * time.Second)
	r.shot("away-9s")
	r.run(2 * time.Second)
	r.shot("away-11s")
	r.run(time.Second)
	r.back()
	r.run(3 * time.Second)
	r.shot("done")
}

// The new engine does not get going: the old one is put back, and a toast
// says so.
func playSwapRollback(r *m3rig) {
	r.swap(false)
	r.run(500 * time.Millisecond)
	r.change(2 * time.Second)
	r.run(1500 * time.Millisecond)
	r.a.post(func(c *ui.Context) {
		r.a.endSwap()
		r.a.notify(c, nil, "网卡没更新成功，用回了原来的版本", nil)
	})
	r.run(800 * time.Millisecond)
	r.shot("toast")
	r.run(4500 * time.Millisecond)
	r.shot("done")
}

// The system's window asks for the password (a development build): the
// line from the start; the engine changes once it is given.
func playSwapPassword(r *m3rig) {
	r.swap(true)
	r.run(800 * time.Millisecond)
	r.shot("asking")
	r.run(2500 * time.Millisecond)
	r.change(2 * time.Second)
	r.run(3 * time.Second)
	r.shot("done")
}

// The engine in the package is not signed with Deneb's key: a toast, and
// nothing else changes.
func playSwapBadSig(r *m3rig) {
	r.run(300 * time.Millisecond)
	r.a.post(func(c *ui.Context) {
		r.a.notify(c, nil, "安装包里的系统网卡不对，请重新下载 Deneb", nil)
	})
	r.run(800 * time.Millisecond)
	r.shot("toast")
	r.run(4500 * time.Millisecond)
	r.shot("done")
}

// A window just opened asks for the change at once, before the engine's
// first picture is in (11-16, the real one); the new engine then comes back
// without its LANs a while, the control server out of reach. From the first
// picture on, the window stays as it is.
func playSwapFresh(r *m3rig) {
	r.shot("opening")
	r.swap(false)
	r.run(150 * time.Millisecond)
	r.d.Simulate("engine-up")
	r.run(1500 * time.Millisecond)
	r.shot("first")
	r.d.Simulate("engine-away")
	r.run(time.Second)
	r.shot("away")
	r.d.Simulate("engine-loading")
	r.run(4 * time.Second)
	r.shot("no-lans")
	r.d.Simulate("engine-new")
	r.run(1500 * time.Millisecond)
	r.shot("back-connecting")
	r.d.Simulate("nonet-back")
	r.run(3 * time.Second)
	r.shot("done")
}

// The new engine comes back without its LANs, the control server out of
// reach (11-16): the picture stays; past 10 s from the old engine going
// away, the line says 正在更新网卡…; then the LANs come, and each device
// keeps its line until it is connected again.
func playSwapNoControl(r *m3rig) {
	r.shot("start")
	r.swap(false)
	r.run(500 * time.Millisecond)
	r.d.Simulate("engine-away")
	r.run(time.Second)
	r.d.Simulate("engine-loading")
	r.run(8 * time.Second)
	r.shot("no-lans-9s")
	r.run(2 * time.Second)
	r.shot("no-lans-11s")
	r.run(2 * time.Second)
	r.d.Simulate("engine-new")
	r.run(1500 * time.Millisecond)
	r.shot("back-connecting")
	r.d.Simulate("nonet-back")
	r.run(3 * time.Second)
	r.shot("done")
}

// As it was: the window not told of the change.
func playSwapBefore(r *m3rig) {
	r.run(300 * time.Millisecond)
	r.d.Simulate("engine-away")
	r.run(2 * time.Second)
	r.shot("away")
	r.d.Simulate("engine-loading")
	r.run(300 * time.Millisecond)
	r.shot("loading")
	r.run(200 * time.Millisecond)
	r.d.Simulate("engine-new")
	r.run(1500 * time.Millisecond)
	r.d.Simulate("nonet-back")
	r.run(2 * time.Second)
	r.shot("done")
}
