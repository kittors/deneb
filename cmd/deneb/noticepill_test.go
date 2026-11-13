package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestNoticePillAligned: the engine's notice, a pill in the header, sits on
// the same middle line as the members' balls and "邀请朋友" (the user's
// report, 2026-11-13: it sat lower, centered on the whole header), with
// the seats' line under them or not, in a narrow, the usual and a wide
// window; and in 全部's header, where it alone shows, on the same line as in
// a LAN's. With DENEB_FRAMES naming a folder, each case's header is written
// there too, light and dark, for review.
func TestNoticePillAligned(t *testing.T) {
	dir := os.Getenv("DENEB_FRAMES")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"narrow", 900, 620}, {"usual", 1180, 740}, {"wide", 1500, 900}} {
		for _, dark := range []bool{false, true} {
			theme := map[bool]string{false: "light", true: "dark"}[dark]
			a, d := newDemoApp()
			d.Manual()
			d.Simulate("m3") // one seat left: "还能加 1 人" under 邀请朋友
			d.Simulate("local-network")
			tt := ui.NewTester(a.view, size.w, size.h)
			tt.SetDark(dark)
			if dir != "" {
				tt.SetScale(2)
			}
			run := func() {
				for i := 0; i < 60; i++ {
					tt.Advance(16 * time.Millisecond)
					tt.Frame()
				}
			}
			mid := func(what string) float32 {
				r, ok := tt.Find(what)
				if !ok {
					t.Fatalf("%s %s: no %q; the window shows %q", size.name, theme, what, tt.Texts())
				}
				return r.Y + r.H/2
			}
			shot := func(state string) {
				if dir == "" {
					return
				}
				img := tt.Image()
				// The header's band, the window's width: what the report is about.
				band := image.Rect(0, 0, img.Bounds().Dx(), int(2*headerHeight)+24)
				out := image.NewRGBA(image.Rect(0, 0, band.Dx(), band.Dy()))
				draw.Draw(out, out.Bounds(), img, band.Min, draw.Src)
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("pill_%s_%s_%s.png", theme, size.name, state)))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, out); err != nil {
					t.Fatal(err)
				}
			}
			run()
			check := func(state string) float32 {
				pill, invite := mid("本地网络权限没拿到"), mid("邀请朋友")
				if d := pill - invite; d > 0.5 || d < -0.5 {
					t.Errorf("%s %s, %s: the pill's middle is %.1f, 邀请朋友's %.1f", size.name, theme, state, pill, invite)
				}
				shot(state)
				return pill
			}
			if !tt.HasText("还能加 1 人") {
				t.Fatalf("%s %s: no seats' line: %q", size.name, theme, tt.Texts())
			}
			lan := check("seats-line")
			d.Simulate("seats") // seven seats: "还能加 2 人"
			d.Simulate("seats") // ten: nothing said
			run()
			if tt.HasText("还能加") {
				t.Fatalf("%s %s: the seats' line still shows: %q", size.name, theme, tt.Texts())
			}
			check("no-seats-line")
			// 全部: no balls and no invite; the pill where it is in a LAN's.
			if err := tt.Click("全部"); err != nil {
				t.Fatal(err)
			}
			run()
			if all := mid("本地网络权限没拿到"); all-lan > 0.5 || all-lan < -0.5 {
				t.Errorf("%s %s: in 全部 the pill's middle is %.1f, in a LAN's %.1f", size.name, theme, all, lan)
			}
			shot("all")
		}
	}
}
