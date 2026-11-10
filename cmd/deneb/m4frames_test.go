package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestM4Frames records M4's relays in the demo (DESIGN §6.12), both
// themes, for review, when DENEB_M4 names a folder: each scene's frames, a
// 60th of a second apart, in <theme>-<scene>/, and its stills beside them.
func TestM4Frames(t *testing.T) {
	root := os.Getenv("DENEB_M4")
	if root == "" {
		t.Skip("set DENEB_M4 to a folder to record M4's demo")
	}
	scenes := []struct {
		key    string
		events []string
		play   func(r *m3rig)
	}{
		{"public-card", nil, playPublicCard},
		{"my-server", []string{"m4-in"}, playMyServer},
		{"friend", nil, playFriend},
		{"fallback", []string{"m4-in"}, playFallback},
		{"own-server", []string{"m4-fresh"}, playOwnServer},
	}
	// DENEB_M4_ONLY names the scenes to record again, as "friend,fallback".
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_M4_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
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
			r.still = filepath.Join(root, theme+"-"+sc.key)
			os.RemoveAll(r.dir)
			if err := os.MkdirAll(r.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			r.run(600 * time.Millisecond)
			sc.play(r)
		}
	}
}

// newM4Rig is the demo as an M4 server would have it, then events, the
// card put away.
func newM4Rig(t *testing.T, events ...string) *m3rig {
	t.Helper()
	r := newM3Rig(t)
	r.d.Simulate("m4")
	for _, e := range events {
		r.d.Simulate(e)
	}
	r.a.selected, r.a.more = "", false
	r.run(1200 * time.Millisecond)
	return r
}

// relay is where the relay id shows on the canvas, in the window.
func (r *m3rig) relay(id string) func() ui.Point {
	return func() ui.Point {
		s := r.a.stopOf(model.RelayID(id))
		if s == nil {
			r.t.Fatalf("no relay %s on the canvas: %q", id, r.tt.Texts())
		}
		p := s.at()
		return ui.Point{X: r.a.origin.X + p.X, Y: r.a.origin.Y + p.Y}
	}
}

// device is where the device id shows on the canvas, in the window.
func (r *m3rig) device(id string) func() ui.Point { return func() ui.Point { return r.tile(id) } }

// drag takes what is at from to where to says, as a hand does, and lets go.
func (r *m3rig) drag(from, to func() ui.Point, steps int) {
	r.glide(from, 18)
	r.press()
	r.glide(to, steps)
	r.run(250 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(700 * time.Millisecond)
}

// mark logs the frame a moment of the scene starts at, to find it.
func (r *m3rig) mark(what string) { r.t.Logf("%s: frame %d", what, r.n) }

// park moves the pointer out of the way, to the canvas's empty top.
func (r *m3rig) park() { r.glide(at(700, 30), 16) }

// Scene M4-01: 阿杰's NAS goes through 香港, the public relay. Its card
// says it is always there, how near, that it sees nothing; "更多" says
// where, how fast, and the limit of each connection. The relay and the
// NAS move about, the tracks following.
func playPublicCard(r *m3rig) {
	r.run(700 * time.Millisecond)
	r.tap(r.relay("hk"))
	r.run(1400 * time.Millisecond)
	r.shot("公共中转站")
	r.tap(r.text("更多"))
	r.run(1500 * time.Millisecond)
	r.shot("更多")
	r.tap(r.text("关闭"))
	r.run(600 * time.Millisecond)
	// It moves where the hand puts it; the NAS too, the tracks through the
	// relay following like ropes.
	hk := r.relay("hk")()
	r.drag(r.relay("hk"), at(hk.X-150, hk.Y+40), 40)
	r.run(500 * time.Millisecond)
	nas := r.tile("jnas")
	r.drag(r.device("jnas"), at(nas.X+120, nas.Y-60), 40)
	r.run(600 * time.Millisecond)
	r.shot("挪一挪")
	r.tap(r.device("jnas"))
	r.run(1200 * time.Millisecond)
	r.shot("设备卡片")
	r.tap(r.text("看看这个中转站"))
	r.run(1300 * time.Millisecond)
	r.park()
	r.run(800 * time.Millisecond)
}

// Scene M4-02: my server, 198.51.100.4, carries 阿杰's NAS. Its card: how
// it is doing, what it carried this month for each LAN, who is on it. It
// takes a name in place; 阿杰 is kept off it, and let back; "不用这个中转
// 站" sends it into the tray, and undo brings it back.
func playMyServer(r *m3rig) {
	r.run(600 * time.Millisecond)
	r.tap(r.relay("srv"))
	r.run(1400 * time.Millisecond)
	r.shot("我的服务器")
	r.tap(r.text("改名字"))
	r.run(500 * time.Millisecond)
	for _, ch := range []string{"公", "司", "机", "房"} {
		r.tt.Type(ch)
		r.run(160 * time.Millisecond)
	}
	r.run(300 * time.Millisecond)
	r.tt.Key(0, ui.KeyEnter)
	r.run(1200 * time.Millisecond)
	r.shot("改名")
	r.mark("kick")
	r.tap(r.text("不让阿杰用"))
	r.run(1600 * time.Millisecond)
	r.shot("不让阿杰用")
	r.mark("unkick")
	r.tap(r.text("撤销"))
	r.run(1800 * time.Millisecond)
	r.mark("out")
	r.tap(r.text("不用这个中转站"))
	r.run(1600 * time.Millisecond)
	r.shot("不用这个中转站")
	r.mark("back")
	r.tap(r.text("撤销"))
	r.run(1800 * time.Millisecond)
	r.shot("撤销")
	r.park()
	r.run(800 * time.Millisecond)
}

// Scene M4-03: 阿杰's server in 东京, which he put into 周末联机, carries
// Mia's MacBook in a hotel. Its card says what 阿杰 can see, and has
// nothing to switch. Taken to the tray, it stays: only 阿杰 can take it
// out; nor does the public one go.
func playFriend(r *m3rig) {
	r.run(600 * time.Millisecond)
	r.tap(r.relay("jiesrv"))
	r.run(1500 * time.Millisecond)
	r.shot("朋友的中转站")
	r.tap(r.text("更多"))
	r.run(1200 * time.Millisecond)
	r.tap(r.text("关闭"))
	r.run(600 * time.Millisecond)
	tray := func() ui.Point {
		z := r.a.trayZone(r.a.cw, r.a.ch)
		return ui.Point{X: r.a.origin.X + z.X + z.W/2, Y: r.a.origin.Y + z.Y + z.H/2}
	}
	r.glide(r.relay("jiesrv"), 18)
	r.press()
	r.glide(tray, 46)
	r.run(900 * time.Millisecond)
	r.shot("拿不走")
	r.tt.Release(r.px, r.py)
	r.run(900 * time.Millisecond)
	r.glide(r.relay("hk"), 18)
	r.press()
	r.glide(tray, 40)
	r.run(900 * time.Millisecond)
	r.shot("公共的拿不掉")
	r.tt.Release(r.px, r.py)
	r.run(900 * time.Millisecond)
	// Mia's MacBook moves, its track through her NAS following.
	mia := r.tile("mia")
	r.drag(r.device("mia"), at(mia.X+40, mia.Y+150), 40)
	r.run(800 * time.Millisecond)
	r.park()
	r.run(600 * time.Millisecond)
}

// Scene M4-04: my server, named 公司机房, carries 阿杰's NAS; it stops. The
// NAS goes through the public relay at once, the light never broken, and
// says "改走公共中转站"; the server greys, "连不上". Back, all goes back.
func playFallback(r *m3rig) {
	r.d.Do(context.Background(), model.Op{Kind: model.OpRelayRename, Relay: "srv", Name: "公司机房"})
	r.run(1500 * time.Millisecond)
	r.shot("经我的服务器")
	r.mark("down")
	r.d.Simulate("relay-down")
	r.run(2200 * time.Millisecond)
	r.shot("改走公共中转站")
	r.tap(r.device("jnas"))
	r.run(1500 * time.Millisecond)
	r.shot("设备卡片")
	r.tap(r.relay("srv"))
	r.run(1500 * time.Millisecond)
	r.shot("连不上")
	r.tap(r.text("关闭"))
	r.run(800 * time.Millisecond)
	r.park()
	r.mark("up")
	r.d.Simulate("relay-down")
	r.run(2400 * time.Millisecond)
	r.shot("换回来")
	r.run(600 * time.Millisecond)
}

// Scene M4-05: no server of my own yet. The tray's "+" gives a command to
// run on one, and a breathing "等待安装"; copied, run, my server comes up in
// the tray, popping, and its card takes the command's place. Dragged onto
// the canvas, 周末联机 uses it: 阿杰's NAS leaves the public relay for it.
// Another command, not wanted, is taken back.
func playOwnServer(r *m3rig) {
	plus := func() ui.Point {
		b, ok := r.tt.Find("用我自己的服务器")
		if !ok {
			r.t.Fatalf("no + in the tray: %q", r.tt.Texts())
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
	r.run(600 * time.Millisecond)
	r.glide(plus, 24)
	r.run(900 * time.Millisecond)
	r.shot("加号")
	r.tt.Press(r.px, r.py)
	r.run(70 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(1500 * time.Millisecond)
	r.shot("一行命令")
	r.tap(r.text("复制"))
	r.run(1500 * time.Millisecond)
	r.shot("已复制")
	r.park()
	r.run(2600 * time.Millisecond)
	r.mark("joined")
	r.d.Simulate("relay-joined")
	r.run(1500 * time.Millisecond)
	r.shot("刚上线")
	r.tap(r.text("关闭"))
	r.run(700 * time.Millisecond)
	slot := func() ui.Point { return r.traySlot("relay:srv") }
	nas := r.tile("jnas")
	r.mark("drag-in")
	// Into the open, right of the NAS: the NAS's track leaves 香港 for it.
	r.drag(slot, at(nas.X+190, nas.Y-150), 46)
	r.run(1600 * time.Millisecond)
	r.shot("拖进来")
	r.run(1500 * time.Millisecond)
	// One more command, not wanted: taken back.
	r.glide(plus, 24)
	r.tt.Press(r.px, r.py)
	r.run(70 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(1200 * time.Millisecond)
	r.tap(r.text("关闭"))
	r.run(800 * time.Millisecond)
	r.tap(r.text("等待安装"))
	r.run(1200 * time.Millisecond)
	r.shot("等待安装")
	r.mark("revoke")
	r.tap(r.text("收回这行命令"))
	r.run(1500 * time.Millisecond)
	r.shot("收回命令")
	r.park()
	r.run(4500 * time.Millisecond)
}
