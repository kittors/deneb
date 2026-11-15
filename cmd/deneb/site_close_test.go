package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kittors/deneb/client/model"
)

// TestSiteCloseups writes the stills the home page's close-ups are cut from
// (design/tools/site-images.py), four times as sharp, when DENEB_SITE_CLOSE
// names a folder: the LAN's lines, My Devices' services and my own server
// in use (named 新加坡), both themes; my NAS held over Mia's ball, dark.
func TestSiteCloseups(t *testing.T) {
	root := os.Getenv("DENEB_SITE_CLOSE")
	if root == "" {
		t.Skip("set DENEB_SITE_CLOSE to a folder for the home page's close-ups")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	shoot := func(tt *ui.Tester, name string) {
		t.Helper()
		tt.SetScale(4)
		tt.Frame()
		f, err := os.Create(filepath.Join(root, name+".png"))
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
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]

		r := newM4Rig(t)
		r.tt.SetDark(dark)
		r.move(r.a.origin.X+700, r.a.origin.Y+560)
		r.run(1500 * time.Millisecond)
		shoot(r.tt, theme+"_lines")

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
		shoot(r.tt, theme+"_services")

		// My own server in use (as TestSiteShots has it).
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
		// Named, as its owner may name it, so that the picture shows a place
		// and not an address (the home page's words have none, DESIGN
		// §14.16): one the page has nowhere else, not 香港, the public
		// relay's on the first screen. The demo's own server keeps none.
		r.d.Do(context.Background(), model.Op{Kind: model.OpRelayRename, Relay: "srv", Name: "新加坡"})
		r.park()
		r.run(7 * time.Second)
		shoot(r.tt, theme+"_own")

		if !dark {
			continue
		}
		// My NAS out of the tray, held over Mia's ball (M3's share-drag).
		r = newM3Rig(t)
		r.tt.SetDark(true)
		r.run(600 * time.Millisecond)
		r.move(r.a.origin.X+500, r.a.origin.Y+500)
		r.run(300 * time.Millisecond)
		r.glide(func() ui.Point { return r.traySlot("mynas") }, 30)
		r.press()
		r.glide(func() ui.Point { return ui.Point{X: r.ball("leo").X - 40, Y: r.a.origin.Y + 60} }, 50)
		r.run(300 * time.Millisecond)
		r.glide(func() ui.Point { return r.ball("jie") }, 24)
		r.run(250 * time.Millisecond)
		r.glide(func() ui.Point { return r.ball("mia") }, 18)
		r.run(500 * time.Millisecond)
		shoot(r.tt, theme+"_share")
		r.tt.Release(r.px, r.py)
		r.run(900 * time.Millisecond)
	}
}
