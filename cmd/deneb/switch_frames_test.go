package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// TestSwitchFrames records the window as the sidebar goes from one page to
// another (design/plan/m7/switch-motion.md), both themes, when DENEB_SWITCH
// names a folder: each scene's frames, a 60th of a second apart, in
// <theme>-<scene>/, and its stills beside them. DENEB_SWITCH_ONLY picks
// scenes; DENEB_SWITCH_SIZE (narrow, wide) another window size.
//
// The world is the demo's, with my game console in 工作室 as well as in
// 周末联机: two LANs that share this computer and one more device, at
// different places, as the user's do (11-17: the same DESKTOP in both).
func TestSwitchFrames(t *testing.T) {
	root := os.Getenv("DENEB_SWITCH")
	if root == "" {
		t.Skip("set DENEB_SWITCH to a folder to record the sidebar's switching")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_SWITCH_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	w, h := float32(1180), float32(740)
	switch os.Getenv("DENEB_SWITCH_SIZE") {
	case "narrow":
		w, h = 980, 640
	case "wide":
		w, h = 1440, 900
	}
	scenes := []struct {
		key  string
		play func(r *m3rig)
	}{
		{"lan", playSwitchLAN},
		{"all", playSwitchAll},
		{"pages", playSwitchPages},
		{"rapid", playSwitchRapid},
		{"toggle", playSwitchToggle},
		{"rows", playSwitchRows},
		{"boards", playSwitchBoards},
	}
	themes := []bool{false, true}
	if os.Getenv("DENEB_SWITCH_THEME") == "light" {
		themes = []bool{false}
	} else if os.Getenv("DENEB_SWITCH_THEME") == "dark" {
		themes = []bool{true}
	}
	for _, dark := range themes {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			sc.play(switchRig(t, root, theme, dark, sc.key, w, h))
		}
	}
}

// switchWorld is the demo's world with M3's devices given to me, and my
// game console in 工作室 too.
func switchWorld(d *client.Demo) {
	d.Simulate("m3")
	d.Do(context.Background(), model.Op{Kind: model.OpLANAddDevice, LAN: "work", Device: "game"})
}

// switchRig is the window at w×h, as on the Mac, showing 周末联机, recording
// into <theme>-<key>/, the pointer over the canvas's empty top right.
func switchRig(t *testing.T, root, theme string, dark bool, key string, w, h float32) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, int(w), int(h))}
	r.tt.SetTitleBar(ui.TitleBar{Height: macTitleBar, Left: 70})
	r.tt.SetDark(dark)
	r.run(800 * time.Millisecond)
	switchWorld(d)
	r.run(1200 * time.Millisecond)
	// 工作室 once, so that the console has its place there, as it would
	// have after a first look; then back to 周末联机, where every scene
	// starts.
	a.post(func(c *ui.Context) { a.show(c, "work", false) })
	r.run(600 * time.Millisecond)
	a.post(func(c *ui.Context) { a.show(c, "weekend", false) })
	r.run(1500 * time.Millisecond)
	r.dir = filepath.Join(root, theme+"-"+key)
	r.still = r.dir
	reset(t, r.dir)
	r.move(w-120, 140)
	r.run(400 * time.Millisecond)
	return r
}

// row is where the sidebar's row reading label is: its text, in the
// sidebar (the header's title reads the same).
func (r *m3rig) row(label string) func() ui.Point {
	return func() ui.Point {
		b, ok := r.tt.Find(label)
		if !ok || b.X > sidebarWidth {
			r.t.Fatalf("no row %q in the sidebar: %q", label, r.tt.Texts())
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
}

// pick goes to a row and clicks it, as a hand does: there, a moment, down,
// up; then the pointer rests.
func (r *m3rig) pick(label string, rest time.Duration) {
	r.glide(r.row(label), 22)
	r.run(180 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	r.run(80 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(rest)
}

// Scene: 周末联机 to 工作室 and back, at a hand's pace.
func playSwitchLAN(r *m3rig) {
	r.shot("周末联机")
	r.pick("工作室", 1400*time.Millisecond)
	r.shot("工作室")
	r.pick("周末联机", 1400*time.Millisecond)
	r.shot("回到周末联机")
}

// Scene: 全部, then one LAN, then 全部 again.
func playSwitchAll(r *m3rig) {
	r.pick("全部", 1400*time.Millisecond)
	r.shot("全部")
	r.pick("工作室", 1400*time.Millisecond)
	r.shot("工作室")
	r.pick("全部", 1400*time.Millisecond)
	r.shot("回到全部")
}

// Scene: from a LAN's canvas to My Devices, to the devices given to me,
// and back to a LAN.
func playSwitchPages(r *m3rig) {
	r.pick("我的设备", 1400*time.Millisecond)
	r.shot("我的设备")
	r.pick("共享给我", 1400*time.Millisecond)
	r.shot("共享给我")
	r.pick("工作室", 1400*time.Millisecond)
	r.shot("工作室")
}

// Scene: four rows clicked one after another, faster than any switch
// takes: nothing may shake, pile up or stall.
func playSwitchRapid(r *m3rig) {
	for i, label := range []string{"工作室", "全部", "周末联机", "工作室"} {
		r.glide(r.row(label), 6)
		r.run(30 * time.Millisecond)
		r.tt.Press(r.px, r.py)
		r.run(50 * time.Millisecond)
		r.tt.Release(r.px, r.py)
		if i == 1 {
			r.shot("连点中")
		}
		r.run(40 * time.Millisecond)
	}
	r.run(1500 * time.Millisecond)
	r.shot("停下")
}

// Scene: the switch at the end of a row: 工作室's off and on, while
// 周末联机 shows; then 周末联机's own, off and on.
func playSwitchToggle(r *m3rig) {
	for _, name := range []string{"工作室", "周末联机"} {
		at := r.text("暂停" + name)
		r.glide(at, 22)
		r.run(200 * time.Millisecond)
		r.shot("指着" + name + "的开关")
		r.tt.Press(r.px, r.py)
		r.run(120 * time.Millisecond)
		r.shot("按着" + name + "的开关")
		r.tt.Release(r.px, r.py)
		r.run(1300 * time.Millisecond)
		r.shot(name + "已暂停")
		r.tt.Press(r.px, r.py)
		r.run(80 * time.Millisecond)
		r.tt.Release(r.px, r.py)
		r.run(1300 * time.Millisecond)
	}
	r.shot("都恢复了")
}

// Scene: the rows under the pointer one by one, top to bottom; one held
// down and let go off it, which does nothing; another clicked.
func playSwitchRows(r *m3rig) {
	for _, label := range []string{"全部", "周末联机", "工作室", "我的设备", "共享给我"} {
		r.glide(r.row(label), 14)
		r.run(260 * time.Millisecond)
		if label == "工作室" {
			r.shot("悬停")
		}
	}
	r.glide(r.row("工作室"), 14)
	r.run(200 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	r.run(250 * time.Millisecond)
	r.shot("按下")
	r.glide(func() ui.Point { return ui.Point{X: r.px + 300, Y: r.py + 40} }, 14)
	r.tt.Release(r.px, r.py)
	r.run(500 * time.Millisecond)
	r.shot("移开松手")
	r.pick("工作室", 1200*time.Millisecond)
}

// Scene: into a LAN I wait to be let into and out of it; then 工作室,
// shown, gone elsewhere; then the last LAN gone too: words instead of
// devices, taking over as any page does.
func playSwitchBoards(r *m3rig) {
	r.d.Simulate("waiting")
	r.run(600 * time.Millisecond)
	r.pick("读书会", 1400*time.Millisecond)
	r.shot("等同意的局域网")
	r.pick("周末联机", 1400*time.Millisecond)
	r.pick("工作室", 1200*time.Millisecond)
	r.d.Simulate("lan-gone") // 读书会, the last in the list
	r.run(900 * time.Millisecond)
	r.d.Simulate("lan-gone") // 工作室, on the canvas
	r.run(1400 * time.Millisecond)
	r.shot("工作室没了")
	r.d.Simulate("lan-gone") // 周末联机, the last one
	r.run(1400 * time.Millisecond)
	r.shot("一个局域网都没有了")
}
