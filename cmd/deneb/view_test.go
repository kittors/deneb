package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The view builds without a window, so a layout that panics or drops what
// the design shows fails here before anyone has to open the app.
func TestViewShowsTheLAN(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	for _, s := range []string{"Deneb", "周末联机", "这台电脑", "阿杰的 NAS", "拖出来，加进局域网", "开放给大家的"} {
		if !tt.HasText(s) {
			t.Errorf("%q missing, texts %q", s, tt.Texts())
		}
	}
}

// Dropping a device on another never leaves them on top of each other.
func TestDropMakesRoom(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	game := a.device("game")
	mac := a.device("mac")
	from := ui.Point{X: sidebarWidth + game.base.X, Y: headerHeight + game.base.Y}
	to := ui.Point{X: sidebarWidth + mac.base.X, Y: headerHeight + mac.base.Y}
	drag(tt, from, to)
	tt.Advance(2 * time.Second)
	if overlaps(footprint(game.at()), footprint(mac.at())) {
		t.Errorf("game at %v overlaps this computer at %v", game.at(), mac.at())
	}
}

// drag presses at from, moves to `to` in steps as a hand would, and lets go.
func drag(tt *ui.Tester, from, to ui.Point) {
	tt.Press(from.X, from.Y)
	for i := 1; i <= 12; i++ {
		k := float32(i) / 12
		tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Release(to.X, to.Y)
}

// TestFrames writes frames of the main interactions for review, at twice
// the window's size, when DENEB_FRAMES names a folder. Each step is one
// file, so the frames read in order like a recording.
//
// With DENEB_BASELINE naming a folder of frames written before a change, it
// also checks that the change left every frame as it was. The renderer's
// caches make a frame differ from itself by a few pixels a little from one
// run to the next (at most 14 of 3.5 million, each by 1 of 255, over four
// runs of the same code), so that much is let through; anything that moves,
// recolors or resizes what is drawn changes hundreds of pixels by more.
// changedOnPurpose are the frames a fix after the split changed, against
// the baseline taken before it: the toast that comes in or goes out over
// the tray rose up through it, and goes over it now (found in the self-check
// of the M1 recordings, design/plan/m1/client.md). The others stay as they
// were.
var changedOnPurpose = map[string]bool{"tray-landing": true, "undo-returning": true, "tray-joining": true}

func TestFrames(t *testing.T) {
	dir := os.Getenv("DENEB_FRAMES")
	if dir == "" {
		t.Skip("set DENEB_FRAMES to write review frames")
	}
	baseline := os.Getenv("DENEB_BASELINE")
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		a, _ := newDemoApp()
		tt := ui.NewTester(a.view, 1180, 740)
		tt.SetScale(2)
		tt.SetDark(dark)
		n := 0
		shot := func(name string) {
			n++
			file := fmt.Sprintf("%s_%02d_%s.png", theme, n, name)
			f, err := os.Create(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			img := tt.Image()
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			if baseline != "" && !changedOnPurpose[name] {
				if px, most := differ(t, filepath.Join(baseline, file), img); px > 64 || most > 1 {
					t.Errorf("%s: %d pixels differ from the baseline's, by up to %d of 255", file, px, most)
				}
			}
		}
		at := func(d *device) ui.Point { p := d.at(); return ui.Point{X: sidebarWidth + p.X, Y: headerHeight + p.Y} }
		// hold presses at from and moves to `to` as a hand would, holding on.
		hold := func(from, to ui.Point) {
			tt.Press(from.X, from.Y)
			for i := 1; i <= 10; i++ {
				k := float32(i) / 10
				tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
				tt.Advance(16 * time.Millisecond)
			}
		}
		click := func(text string) {
			if r, ok := tt.Find(text); ok {
				tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
			} else {
				t.Errorf("%s: no %q", theme, text)
			}
		}
		tt.Frame()
		tt.Advance(1200 * time.Millisecond)
		shot("main")
		// Hover, then open someone else's device: the card grows out of
		// its corner; another device fades its words across; a click on the
		// canvas puts it away.
		tt.Move(at(a.device("mia")).X, at(a.device("mia")).Y)
		tt.Advance(300 * time.Millisecond)
		shot("hover")
		tt.ClickAt(at(a.device("jnas")).X, at(a.device("jnas")).Y)
		tt.Advance(60 * time.Millisecond)
		shot("card-switching")
		tt.Advance(600 * time.Millisecond)
		shot("card-others")
		tt.ClickAt(sidebarWidth+300, headerHeight+40)
		tt.Advance(60 * time.Millisecond)
		shot("card-closing")
		tt.Advance(600 * time.Millisecond)
		shot("card-closed")
		tt.ClickAt(at(a.device("mia")).X, at(a.device("mia")).Y)
		tt.Advance(60 * time.Millisecond)
		shot("card-opening")
		tt.Advance(600 * time.Millisecond)
		shot("card-open")
		// Drag my console onto this computer: it makes room.
		game := a.device("game")
		hold(at(game), at(a.device("mac")))
		shot("dragging")
		tt.Release(at(a.device("mac")).X, at(a.device("mac")).Y)
		tt.Advance(120 * time.Millisecond)
		shot("drop-settling")
		tt.Advance(900 * time.Millisecond)
		shot("drop-made-room")
		// Bring someone else's device to the tray: it stops above it, and
		// the tray says why.
		jd := a.device("jdesk")
		hold(at(jd), ui.Point{X: at(jd).X, Y: 700})
		tt.Advance(300 * time.Millisecond)
		shot("refuse-hint")
		tt.Release(at(jd).X, 700)
		tt.Advance(900 * time.Millisecond)
		shot("refuse-rests")
		// Bring my console to the tray: it offers itself, then opens a slot;
		// let go, and the console flies into it.
		from := at(game)
		near := ui.Point{X: 560, Y: 740 - trayBand - 110}
		hold(from, near)
		tt.Advance(300 * time.Millisecond)
		shot("tray-offer")
		hold2 := func(from, to ui.Point) {
			for i := 1; i <= 8; i++ {
				k := float32(i) / 8
				tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
				tt.Advance(16 * time.Millisecond)
			}
		}
		hold2(near, ui.Point{X: 560, Y: 700})
		tt.Advance(400 * time.Millisecond)
		shot("tray-armed")
		tt.Release(560, 700)
		tt.Advance(100 * time.Millisecond)
		shot("tray-landing")
		tt.Advance(900 * time.Millisecond)
		shot("tray-landed")
		click("撤销")
		tt.Advance(140 * time.Millisecond)
		shot("undo-returning")
		tt.Advance(900 * time.Millisecond)
		shot("undo-back")
		// Let go in the tray's band, far from the tray: the tray never
		// lit up, and the console stays in the LAN, above the band.
		hold(at(game), ui.Point{X: 1180 - 90, Y: 700})
		tt.Advance(300 * time.Millisecond)
		shot("band-not-tray")
		tt.Release(1180-90, 700)
		tt.Advance(1200 * time.Millisecond)
		shot("band-rests-above")
		// Drag a device in from the tray.
		if r, ok := tt.Find("拖出来，加进局域网"); ok {
			x, y := r.X+r.W+34, r.Y+r.H/2
			hold(ui.Point{X: x, Y: y}, ui.Point{X: 360, Y: 330})
			shot("tray-dragging")
			tt.Release(360, 330)
			tt.Advance(160 * time.Millisecond)
			shot("tray-joining")
			tt.Advance(1200 * time.Millisecond)
			shot("tray-joined")
		} else {
			t.Errorf("%s: no tray", theme)
		}
		tt.Advance(4500 * time.Millisecond)
		shot("toast-gone")
	}
}

// differ counts the pixels of img that differ from those of the PNG in
// file, and by how much at most in any channel.
func differ(t *testing.T, file string, img *image.RGBA) (px, most int) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if want.Bounds() != img.Bounds() {
		t.Fatalf("%s is %v, the frame %v", file, want.Bounds(), img.Bounds())
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
			g := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			d := max(absDiff(w.R, g.R), absDiff(w.G, g.G), absDiff(w.B, g.B), absDiff(w.A, g.A))
			if d > 0 {
				px++
				most = max(most, d)
			}
		}
	}
	return px, most
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// A window far smaller than the design still builds: the canvas's area
// shrinks to nothing instead of going negative.
func TestTinyWindow(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 480, 360)
	tt.Advance(300 * time.Millisecond)
}
