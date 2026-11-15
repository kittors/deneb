package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSiteShots writes the website's own stills of the real window, twice
// as sharp, both themes, when DENEB_SITE names a folder
// (design/tools/site-shots.sh runs it with the scenes the docs use;
// design/tools/site-images.py crops them for web/site/img):
//
//   - 窗口: the LAN canvas, as the window opens;
//   - 拖出来: a device of mine coming out of the tray;
//   - 邀请朋友: what the click on 邀请朋友 says;
//   - 这台电脑, 别人的设备: their cards;
//   - 服务: My Devices, three services each open to its own LANs;
//   - 自己的服务器: my own server in use as the LAN's relay.
func TestSiteShots(t *testing.T) {
	root := os.Getenv("DENEB_SITE")
	if root == "" {
		t.Skip("set DENEB_SITE to a folder for the website's stills")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		shoot := func(r *m3rig, name string) { siteShoot(t, r.tt, filepath.Join(root, theme+"_"+name+".png")) }

		r := newM4Rig(t)
		r.tt.SetDark(dark)
		r.move(r.a.origin.X+700, r.a.origin.Y+560)
		r.run(1500 * time.Millisecond)
		shoot(r, "窗口")
		// A device of mine out of the tray, over the canvas.
		var ids []string
		for id := range r.a.slots {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			id := ids[0]
			r.glide(func() ui.Point { return r.traySlot(id) }, 24)
			r.press()
			r.glide(at(r.a.origin.X+620, r.a.origin.Y+430), 40)
			r.run(400 * time.Millisecond)
			shoot(r, "拖出来")
			r.glide(func() ui.Point { return r.traySlot(id) }, 30)
			r.tt.Release(r.px, r.py)
			r.run(1200 * time.Millisecond)
		}
		r.tap(r.text("邀请朋友"))
		r.run(900 * time.Millisecond)
		shoot(r, "邀请朋友")
		r.park()
		r.run(5 * time.Second)
		r.tap(r.device("mac"))
		r.run(1300 * time.Millisecond)
		shoot(r, "这台电脑")
		r.tap(r.text("关闭"))
		r.run(900 * time.Millisecond)
		r.tap(r.device("jdesk"))
		r.run(1300 * time.Millisecond)
		shoot(r, "别人的设备")
		r.tap(r.text("关闭"))
		r.run(900 * time.Millisecond)

		// My Devices: three services, each open to its own LANs, the
		// pointer off them.
		r = myRig(t, root, theme, dark, "", 1180, 740)
		me := r.a.snap.Device("mac").Name
		r.openMine()
		r.tap(r.text(say("把", me, "的", "共享文件夹", "开放给", "周末联机")))
		r.run(900 * time.Millisecond)
		r.tap(r.text(say("给", me, "添加服务")))
		r.run(600 * time.Millisecond)
		r.field("名称", "我的网站")
		r.field("端口", "8080")
		r.tt.Key(0, ui.KeyEnter)
		r.run(1100 * time.Millisecond)
		r.tap(r.text(say("把", me, "的", "我的网站", "开放给", "工作室")))
		r.run(900 * time.Millisecond)
		r.glide(at(r.a.origin.X+100, r.a.origin.Y+450), 20)
		r.run(6 * time.Second)
		shoot(r, "服务")

		// My own server, set up as M4's own-server scene does, in use; its
		// toast gone, the pointer away.
		r = newM4Rig(t, "m4-fresh")
		r.tt.SetDark(dark)
		r.run(1200 * time.Millisecond)
		plus := func() ui.Point {
			b, ok := r.tt.Find("用我自己的服务器")
			if !ok {
				t.Fatalf("no + in the tray: %q", r.tt.Texts())
			}
			return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
		}
		r.glide(plus, 24)
		r.run(900 * time.Millisecond)
		r.tt.Press(r.px, r.py)
		r.run(70 * time.Millisecond)
		r.tt.Release(r.px, r.py)
		r.run(1500 * time.Millisecond)
		r.park()
		r.run(2600 * time.Millisecond)
		r.d.Simulate("relay-joined")
		r.run(1500 * time.Millisecond)
		r.tap(r.text("关闭"))
		r.run(700 * time.Millisecond)
		nas := r.tile("jnas")
		r.drag(func() ui.Point { return r.traySlot("relay:srv") }, at(nas.X+190, nas.Y-150), 46)
		r.run(1600 * time.Millisecond)
		r.park()
		r.run(7 * time.Second)
		shoot(r, "自己的服务器")
	}
}

// siteShoot writes the window as it is, twice as sharp, to path.
func siteShoot(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	tt.SetScale(2)
	tt.Frame()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	tt.SetScale(1)
	tt.Frame()
}

// TestSiteHeroLoop writes the frames of the home page's moving window, when
// DENEB_SITE_HERO names a folder: the LAN canvas as the window opens, twice
// as sharp, 6.5 s at 30 a second, in <theme>/0000.png… The light runs on
// its own cycles, so design/tools/site-hero.sh makes the loop by fading the
// last half second into the first; frame 15 is where the loop starts, the
// picture under it (design/tools/site-images.py).
func TestSiteHeroLoop(t *testing.T) {
	root := os.Getenv("DENEB_SITE_HERO")
	if root == "" {
		t.Skip("set DENEB_SITE_HERO to a folder for the home page's loop")
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		dir := filepath.Join(root, theme)
		reset(t, dir)
		r := newM4Rig(t)
		r.tt.SetDark(dark)
		r.move(r.a.origin.X+700, r.a.origin.Y+560)
		r.run(1500 * time.Millisecond)
		r.tt.SetScale(2)
		for i := 0; i < 195; i++ {
			if i > 0 {
				r.tt.Advance(time.Second / 30)
			}
			r.tt.Frame()
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.png", i)))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, r.tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
