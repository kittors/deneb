package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestM3Frames records M3 in the demo (DESIGN §6.11), both themes, for
// review, when DENEB_M3 names a folder: each scene's frames, a 60th of a
// second apart, in <theme>-<scene>/, and its stills beside them.
func TestM3Frames(t *testing.T) {
	root := os.Getenv("DENEB_M3")
	if root == "" {
		t.Skip("set DENEB_M3 to a folder to record M3's demo")
	}
	scenes := []struct {
		key  string
		play func(r *m3rig)
	}{
		{"share-drag", playShareDrag},
		{"share-cannot", playShareCannot},
		{"share-card", playShareCard},
		{"shared-with-me", playSharedWithMe},
		{"shared-in", playSharedIn},
		{"seats", playSeats},
		{"seats-blocked", playSeatsBlocked},
		{"invite-full", playInviteFull},
		{"share-mode", playShareMode},
	}
	// DENEB_M3_ONLY names the scenes to record again, as "seats,panel".
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_M3_ONLY"), ",") {
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
			r := newM3Rig(t)
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
		if len(only) == 0 || only["panel"] {
			recordPanelShared(t, root, theme, dark)
		}
	}
}

// pickTray presses 我的 NAS's slot in the tray.
func (r *m3rig) traySlot(id string) ui.Point {
	slot, ok := r.a.slots[id]
	if !ok {
		r.t.Fatalf("%s is not in the tray", id)
	}
	return ui.Point{X: r.a.origin.X + slot.X, Y: r.a.origin.Y + slot.Y}
}

// glide moves the pointer, held or not, to where to says, as a hand does.
func (r *m3rig) glide(to func() ui.Point, steps int) {
	from := ui.Point{X: r.px, Y: r.py}
	for i := 1; i <= steps; i++ {
		p := to()
		k := ui.EaseInOut(float32(i) / float32(steps))
		r.move(from.X+(p.X-from.X)*k, from.Y+(p.Y-from.Y)*k)
		r.run(time.Second / 60)
	}
}

func at(x, y float32) func() ui.Point { return func() ui.Point { return ui.Point{X: x, Y: y} } }

// press presses where the pointer is, a moment after it got there.
func (r *m3rig) press() {
	r.run(120 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	r.run(80 * time.Millisecond)
}

// tap clicks where to says, the pointer gliding there first.
func (r *m3rig) tap(to func() ui.Point) {
	r.glide(to, 20)
	r.run(150 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	r.run(70 * time.Millisecond)
	r.tt.Release(r.px, r.py)
}

// text is where the element showing s is, for tap.
func (r *m3rig) text(s string) func() ui.Point {
	return func() ui.Point {
		b, ok := r.tt.Find(s)
		if !ok {
			r.t.Fatalf("nothing shows %q: %q", s, r.tt.Texts())
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
}

// Scene: 我的 NAS out of the tray, up to the header: the balls spread, the
// line says what to do; over Mia it says what letting go does; let go, it
// goes back to its slot, her ball springs, the toast says so. Her ball's
// click shows what I gave her, and what it opens to her, a switch each.
func playShareDrag(r *m3rig) {
	r.move(r.a.origin.X+500, r.a.origin.Y+500)
	r.run(300 * time.Millisecond)
	r.glide(func() ui.Point { return r.traySlot("mynas") }, 30)
	r.press()
	r.glide(func() ui.Point { return ui.Point{X: r.ball("leo").X - 40, Y: r.a.origin.Y + 60} }, 50)
	r.run(300 * time.Millisecond)
	r.shot("拿起来")
	r.glide(func() ui.Point { return r.ball("jie") }, 24)
	r.run(250 * time.Millisecond)
	r.glide(func() ui.Point { return r.ball("mia") }, 18)
	r.run(500 * time.Millisecond)
	r.shot("指到Mia")
	r.tt.Release(r.px, r.py)
	r.run(900 * time.Millisecond)
	r.shot("分享了")
	r.run(1500 * time.Millisecond)
	r.tap(func() ui.Point { return r.ball("mia") })
	r.run(800 * time.Millisecond)
	r.shot("Mia的卡片")
	r.tap(r.text("分享了我的 NAS"))
	r.run(700 * time.Millisecond)
	r.shot("开放给Mia的")
	r.tap(func() ui.Point {
		b, _ := r.tt.Find("共享文件夹")
		return ui.Point{X: r.a.origin.X + r.a.cardAt.X + r.a.cardAt.W - 40, Y: b.Y + b.H/2}
	})
	r.run(900 * time.Millisecond)
	r.shot("关掉一项")
	r.tap(r.text("关闭"))
	r.run(800 * time.Millisecond)
}

// Scene: what cannot go says so as it is held, and goes back: my game
// console, in 周末联机, over Mia, who uses it already; 我的 NAS, given to
// 阿杰 already, over him.
func playShareCannot(r *m3rig) {
	r.shareNAS("jie")
	r.run(4500 * time.Millisecond)
	r.move(r.a.origin.X+500, r.a.origin.Y+500)
	r.glide(func() ui.Point { return r.tile("game") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("mia") }, 60)
	r.run(700 * time.Millisecond)
	r.shot("已经能用了")
	r.tt.Release(r.px, r.py)
	r.run(1000 * time.Millisecond)
	r.glide(func() ui.Point { return r.traySlot("mynas") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("jie") }, 60)
	r.run(700 * time.Millisecond)
	r.shot("已经有了")
	r.tt.Release(r.px, r.py)
	r.run(1000 * time.Millisecond)
}

// Scene: this computer given to 阿明 on the canvas of the devices given to
// me; its card says to whom and which way; × takes it back, undo gives it
// back; × again, and the toast goes.
func playShareCard(r *m3rig) {
	r.tap(r.text("共享给我"))
	r.run(1200 * time.Millisecond)
	r.glide(func() ui.Point { return r.tile("mac") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("ming") }, 70)
	r.run(600 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(1200 * time.Millisecond)
	r.tap(func() ui.Point { return r.tile("mac") })
	r.run(900 * time.Millisecond)
	r.shot("卡片写清方向")
	r.glide(r.text("分享给阿明"), 20)
	r.run(500 * time.Millisecond)
	r.tap(r.text("收回"))
	r.run(900 * time.Millisecond)
	r.shot("收回")
	r.tap(r.text("撤销"))
	r.run(1000 * time.Millisecond)
	r.shot("撤销")
	r.glide(r.text("分享给阿明"), 20)
	r.run(500 * time.Millisecond)
	r.tap(r.text("收回"))
	r.move(r.a.origin.X+400, r.a.origin.Y+500)
	r.run(5 * time.Second)
	r.shot("提示条走了")
}

// Scene: "共享给我": the canvas of the devices given to me, where each
// comes from; a card, one way, and "不要了" with undo.
func playSharedWithMe(r *m3rig) {
	r.tap(r.text("共享给我"))
	r.run(1500 * time.Millisecond)
	r.shot("共享给我")
	r.tap(func() ui.Point { return r.tile("gpu") })
	r.run(900 * time.Millisecond)
	r.shot("卡片")
	r.tap(r.text("不要了"))
	r.run(1200 * time.Millisecond)
	r.shot("不要了")
	r.tap(r.text("撤销"))
	r.run(1500 * time.Millisecond)
	r.shot("撤销")
}

// Scene: 小美 gives me her printer: the sidebar's row lights up, it comes in
// on the canvas, connects; she takes it back, and it says so.
func playSharedIn(r *m3rig) {
	r.tap(r.text("共享给我"))
	r.run(1500 * time.Millisecond)
	r.d.Simulate("share-in")
	r.run(500 * time.Millisecond)
	r.shot("分来了")
	r.d.Simulate("share-in-up")
	r.run(2000 * time.Millisecond)
	r.shot("连上了")
	r.d.Simulate("share-in")
	r.run(1200 * time.Millisecond)
	r.shot("收回了")
	r.run(3000 * time.Millisecond)
}

// Scene: the seats. One left: "还能加 1 人"; ⌥⌘S: two left, then enough,
// nothing said, then one left again; this computer given to 阿明 takes the
// last; "邀请朋友" goes dashed; its click, the seats; freeing Leo's, undo.
func playSeats(r *m3rig) {
	r.run(800 * time.Millisecond)
	r.shot("还能加1人")
	r.d.Simulate("seats")
	r.run(900 * time.Millisecond)
	r.shot("还能加2人")
	r.d.Simulate("seats")
	r.run(900 * time.Millisecond)
	r.shot("够用不写")
	r.d.Simulate("seats")
	r.run(900 * time.Millisecond)
	r.tap(r.text("共享给我"))
	r.run(1200 * time.Millisecond)
	r.glide(func() ui.Point { return r.tile("mac") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("ming") }, 70)
	r.run(500 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(1500 * time.Millisecond)
	r.tap(r.text("周末联机"))
	r.run(1500 * time.Millisecond)
	r.shot("满了")
	r.tap(r.text("邀请朋友"))
	r.run(900 * time.Millisecond)
	r.shot("位置卡片")
	r.tap(r.text("腾一个位置"))
	r.run(800 * time.Millisecond)
	r.shot("腾一个位置")
	r.tap(r.text("移出Leo"))
	r.run(1000 * time.Millisecond)
	r.shot("移出Leo")
	r.tap(r.text("撤销"))
	r.run(1500 * time.Millisecond)
	r.shot("撤销")
}

// Scene: the seats full. This computer held up to 小美, new to them: her
// ball does not light, the line says why, and it goes back; her ball over
// 周末联机's row: the row does not light, the chip says why.
func playSeatsBlocked(r *m3rig) {
	r.d.Simulate("share-in")
	r.d.Simulate("share-in-up")
	r.tap(r.text("共享给我"))
	r.run(1200 * time.Millisecond)
	r.glide(func() ui.Point { return r.tile("mac") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("ming") }, 60)
	r.run(400 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	r.run(4800 * time.Millisecond)
	r.glide(func() ui.Point { return r.tile("mac") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("mei") }, 70)
	r.run(800 * time.Millisecond)
	r.shot("位置满了")
	r.tt.Release(r.px, r.py)
	r.run(1200 * time.Millisecond)
	r.glide(func() ui.Point { return r.ball("mei") }, 30)
	r.press()
	r.glide(func() ui.Point {
		row := r.a.rows["weekend"]
		return ui.Point{X: row.X + row.W*0.6, Y: row.Y + row.H/2}
	}, 80)
	r.run(800 * time.Millisecond)
	r.shot("拖人也进不去")
	r.tt.Release(r.px, r.py)
	r.run(1200 * time.Millisecond)
}

// Scene: Leo invites me, his seats full: the card says so.
func playInviteFull(r *m3rig) {
	r.d.Simulate("invitation-full")
	r.run(1500 * time.Millisecond)
	r.shot("对方满了")
}

// Scene: 工作室, made to hand out devices: 小林's go; then 工作室主机 held
// up to her ball is hers.
func playShareMode(r *m3rig) {
	r.tap(r.text("工作室"))
	r.run(1500 * time.Millisecond)
	// The LAN's name in the header (the sidebar's row shows it first).
	r.tap(func() ui.Point { return ui.Point{X: r.a.origin.X + 60, Y: 36} })
	r.run(900 * time.Millisecond)
	r.shot("局域网卡片")
	r.tap(func() ui.Point {
		b, _ := r.tt.Find("成员只用分到的设备")
		return ui.Point{X: r.a.origin.X + r.a.cardAt.X + r.a.cardAt.W - 40, Y: b.Y + b.H/2}
	})
	r.run(1200 * time.Millisecond)
	r.shot("改成只用分到的")
	r.tap(r.text("关闭"))
	r.run(4500 * time.Millisecond)
	r.glide(func() ui.Point { return r.tile("studio") }, 30)
	r.press()
	r.glide(func() ui.Point { return r.ball("lin") }, 70)
	r.run(600 * time.Millisecond)
	r.shot("分给小林")
	r.tt.Release(r.px, r.py)
	r.run(1500 * time.Millisecond)
}

// recordPanelShared records the menu bar's panel with the devices given to
// me in it.
func recordPanelShared(t *testing.T, root, theme string, dark bool) {
	a, d := newDemoApp()
	d.Manual()
	d.Simulate("m3")
	m := &menubarScreen{a: a, open: true}
	a.panelShown = true
	tt := ui.NewTester(m.view, screenW, screenH)
	tt.SetDark(dark)
	dir := filepath.Join(root, theme+"-panel-shared")
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
	n := 0
	rec := func(dt time.Duration) {
		for end := tt.Now().Add(dt); tt.Now().Before(end); n++ {
			tt.Advance(time.Second / 60)
			tt.Frame()
			f, _ := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.jpg", n)))
			jpeg.Encode(f, tt.Image(), &jpeg.Options{Quality: 88})
			f.Close()
		}
	}
	rec(1200 * time.Millisecond)
	d.Simulate("share-in")
	rec(1600 * time.Millisecond)
	d.Simulate("share-in-up")
	rec(1500 * time.Millisecond)
	tt.SetScale(2)
	tt.Frame()
	f, _ := os.Create(filepath.Join(root, theme+"-panel-shared_面板里的共享给我.png"))
	png.Encode(f, tt.Image())
	f.Close()
	tt.SetScale(1)
	tt.Frame()
	rec(500 * time.Millisecond)
}
