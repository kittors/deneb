package main

import (
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
)

// TestMotionFrames records the motion the live data brings, at 60 frames a
// second, when DENEB_MOTION names a folder: a folder of numbered frames for
// each scene and theme, as design/tools/sheet.py reads them, for review
// frame by frame (DESIGN §11). The demo plays the outside events, by hand,
// so that every run records the same frames.
func TestMotionFrames(t *testing.T) {
	root := os.Getenv("DENEB_MOTION")
	if root == "" {
		t.Skip("set DENEB_MOTION to record the motion of the live data")
	}
	const frame = time.Second / 60
	scenes := []struct {
		name string
		play func(a *app, d *client.Demo, tt *ui.Tester, rec func(ms int))
	}{
		// 阿杰's NAS leaves 周末联机 on his computer, and comes back: it
		// shrinks away as its track is drawn in and the relay goes with it,
		// then pops in as both grow back (acceptance step 5).
		{"leave", func(a *app, d *client.Demo, tt *ui.Tester, rec func(int)) {
			rec(200)
			d.Simulate("leave")
			rec(900)
			d.Simulate("leave")
			rec(1300)
		}},
		// 阿杰's desktop is connected to anew: the flowing dots, then the
		// track drawn out from both ends with fresh light.
		{"connect", func(a *app, d *client.Demo, tt *ui.Tester, rec func(int)) {
			rec(200)
			d.Simulate("connect")
			rec(1000)
			d.Simulate("connected")
			rec(1300)
		}},
		// It goes offline, and comes back.
		{"offline", func(a *app, d *client.Demo, tt *ui.Tester, rec func(int)) {
			rec(200)
			d.Simulate("offline")
			rec(800)
			d.Simulate("offline")
			rec(1300)
		}},
		// Mia's MacBook loses its direct path and goes through the relay,
		// then gets it back (scene 1's 网络变了).
		{"network", func(a *app, d *client.Demo, tt *ui.Tester, rec func(int)) {
			rec(200)
			d.Simulate("network")
			rec(1300)
			d.Simulate("network")
			rec(1300)
		}},
		// An invite comes, and is accepted: the card comes in and goes, the
		// sidebar's row opens out and lights up, and the LAN's devices pop
		// in one after the other (scene 3).
		{"invite", func(a *app, d *client.Demo, tt *ui.Tester, rec func(int)) {
			rec(200)
			a.post(func(c *ui.Context) { a.openLink(c, client.DemoInvite) })
			rec(700)
			if err := tt.Click("加入"); err != nil {
				t.Fatal(err)
			}
			rec(1800)
		}},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			dir := filepath.Join(root, theme+"-"+sc.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			a, d := newDemoApp()
			d.Manual()
			// As the window does: what the store or other goroutines bring
			// asks for a frame built anew.
			var woken atomic.Bool
			a.setWake(func() { woken.Store(true) })
			tt := ui.NewTester(a.view, 1180, 740)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(1200 * time.Millisecond)
			n := 0
			rec := func(ms int) {
				for end := n + ms*60/1000; n < end; n++ {
					if woken.Swap(false) {
						tt.Frame()
					}
					tt.Advance(frame)
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.jpg", n)))
					if err != nil {
						t.Fatal(err)
					}
					jpeg.Encode(f, tt.Image(), &jpeg.Options{Quality: 88})
					f.Close()
				}
			}
			sc.play(a, d, tt, rec)
		}
	}
}
