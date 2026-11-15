package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestTrayFrames records the tray's comings and goings in the demo, both
// themes, when DENEB_TRAY names a folder: each scene's frames, a 60th of a
// second apart, in <theme>-<scene>/, and its stills twice as sharp beside
// them. DENEB_TRAY_ONLY picks scenes.
//
//   - out: a device of mine dragged off the canvas, near the tray, onto it,
//     let go there;
//   - in: it dragged out of the tray onto the canvas again;
//   - back: one picked up from the tray, carried over the canvas, brought
//     back and let go there;
//   - refuse: someone else's dragged near the tray, and away;
//   - relay: my relay on the canvas dragged onto the tray;
//   - plus: every device of mine taken in, the tray with only its line,
//     添加设备 and "+"; the pointer over "+" and off it.
func TestTrayFrames(t *testing.T) {
	root := os.Getenv("DENEB_TRAY")
	if root == "" {
		t.Skip("set DENEB_TRAY to a folder to record the tray")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_TRAY_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key    string
		events []string
		play   func(r *m3rig)
	}{
		{"out", nil, playTrayOut},
		{"in", nil, playTrayIn},
		{"back", nil, playTrayBack},
		{"refuse", nil, playTrayRefuse},
		{"relay", []string{"m4-in"}, playTrayRelay},
		{"plus", nil, playTrayPlus},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			r := newM4Rig(t, sc.events...)
			r.tt.SetDark(dark)
			r.dir = filepath.Join(root, theme+"-"+sc.key)
			r.still = r.dir
			reset(t, r.dir)
			r.move(r.a.origin.X+700, r.a.origin.Y+80)
			r.run(600 * time.Millisecond)
			sc.play(r)
		}
	}
}

// overTray is the middle of the tray, in the window; near is a little above
// it, where the hand is near enough for the tray to offer.
func (r *m3rig) overTray(near bool) func() ui.Point {
	return func() ui.Point {
		b := r.a.trayAt
		y := b.Y + b.H/2
		if near {
			y = b.Y - 70
		}
		return ui.Point{X: r.a.origin.X + b.X + b.W/2, Y: r.a.origin.Y + y}
	}
}

// trayIDs are the tray's devices of mine, in a steady order.
func (r *m3rig) trayIDs() []string {
	var ids []string
	for _, d := range r.a.tray {
		ids = append(ids, d.id)
	}
	sort.Strings(ids)
	return ids
}

func playTrayOut(r *m3rig) {
	r.mark("pick")
	r.glide(r.device("game"), 20)
	r.press()
	r.mark("near")
	r.glide(r.overTray(true), 30)
	r.run(500 * time.Millisecond)
	r.shot("近了")
	r.mark("over")
	r.glide(r.overTray(false), 16)
	r.run(500 * time.Millisecond)
	r.shot("在托盘上")
	r.mark("let go")
	r.tt.Release(r.px, r.py)
	r.run(700 * time.Millisecond)
	r.shot("问一句")
	r.confirm()
	r.run(1300 * time.Millisecond)
	r.shot("回到托盘")
	r.park()
	r.run(800 * time.Millisecond)
}

// confirm answers the confirmation showing, if one is: yes.
func (r *m3rig) confirm() {
	if r.a.confirming == nil {
		return
	}
	r.mark("confirm")
	r.tap(r.text("确认" + r.a.confirming.title))
}

func playTrayIn(r *m3rig) {
	ids := r.trayIDs()
	if len(ids) == 0 {
		r.t.Fatal("no device of mine in the tray")
	}
	id := ids[0]
	r.mark("pick")
	r.glide(func() ui.Point { return r.traySlot(id) }, 20)
	r.press()
	r.glide(at(r.a.origin.X+640, r.a.origin.Y+380), 36)
	r.run(400 * time.Millisecond)
	r.shot("拖出来")
	r.mark("let go")
	r.tt.Release(r.px, r.py)
	r.run(1200 * time.Millisecond)
	r.shot("加进局域网")
	r.park()
	r.run(800 * time.Millisecond)
}

func playTrayBack(r *m3rig) {
	ids := r.trayIDs()
	if len(ids) == 0 {
		r.t.Fatal("no device of mine in the tray")
	}
	id := ids[len(ids)-1]
	home := r.traySlot(id)
	r.mark("pick")
	r.glide(at(home.X, home.Y), 20)
	r.press()
	r.glide(at(r.a.origin.X+600, r.a.origin.Y+420), 32)
	r.run(300 * time.Millisecond)
	r.shot("拿起来")
	r.mark("bring back")
	r.glide(at(home.X, home.Y), 30)
	r.run(300 * time.Millisecond)
	r.shot("拿回来")
	r.tt.Release(r.px, r.py)
	r.run(1000 * time.Millisecond)
	r.shot("放回去")
	r.park()
	r.run(600 * time.Millisecond)
}

func playTrayRefuse(r *m3rig) {
	from := r.tile("jdesk")
	r.mark("pick")
	r.glide(r.device("jdesk"), 20)
	r.press()
	r.glide(r.overTray(true), 30)
	r.run(500 * time.Millisecond)
	r.shot("别人的设备")
	r.mark("away")
	r.glide(at(from.X, from.Y), 30)
	r.tt.Release(r.px, r.py)
	r.run(1000 * time.Millisecond)
	r.park()
	r.run(600 * time.Millisecond)
}

func playTrayRelay(r *m3rig) {
	r.mark("pick")
	r.glide(r.relay("srv"), 20)
	r.press()
	r.glide(r.overTray(true), 30)
	r.run(500 * time.Millisecond)
	r.shot("中转站近了")
	r.glide(r.overTray(false), 16)
	r.run(500 * time.Millisecond)
	r.shot("中转站在托盘上")
	r.mark("let go")
	r.tt.Release(r.px, r.py)
	r.run(700 * time.Millisecond)
	r.confirm()
	r.run(1300 * time.Millisecond)
	r.shot("中转站回到托盘")
	r.park()
	r.run(800 * time.Millisecond)
}

func playTrayPlus(r *m3rig) {
	for _, id := range r.trayIDs() {
		r.tap(func() ui.Point { return r.traySlot(id) })
		r.run(1200 * time.Millisecond)
	}
	r.park()
	r.run(5 * time.Second) // the toasts gone
	r.shot("只有加号")
	r.mark("over +")
	b, ok := r.tt.Find("用我自己的服务器")
	if !ok {
		r.t.Fatalf("no + in the tray: %q", r.tt.Texts())
	}
	r.glide(at(b.X+b.W/2, b.Y+b.H/2), 24)
	r.run(600 * time.Millisecond)
	r.shot("指着加号")
	r.mark("off +")
	r.park()
	r.run(800 * time.Millisecond)
}
