package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// panelRig is the menu bar's panel in a flyout that follows its height, as
// systray.go's show and resizePanel have it: first as tall as the panel
// last was (360 the first time), then as tall as the panel asks.
type panelRig struct {
	t      *testing.T
	a      *app
	tt     *ui.Tester
	h      int
	dir    string
	n      int
	dark   bool
	sizes  []string // the flyout's height in each frame, for the frame check
	resize []string // the frames the panel asked for another height in
}

func newPanelRig(t *testing.T, a *app, dark bool, dir string) *panelRig {
	r := &panelRig{t: t, a: a, dark: dark, dir: dir}
	r.h = a.panelHeight
	if r.h == 0 {
		r.h = 360
	}
	a.resizePanel = func(n int) {
		r.resize = append(r.resize, fmt.Sprintf("%d %d", r.n, n))
		r.h = n
		if r.tt != nil {
			r.tt.SetSize(panelWidth, n)
		}
	}
	r.tt = ui.NewTester(a.panel, panelWidth, r.h)
	// Its first frame may have asked for another height already: show sets
	// it once the flyout is made.
	r.tt.SetSize(panelWidth, r.h)
	r.tt.SetDark(dark)
	return r
}

// run advances the flyout's clock by d, a 60th of a second a frame, each
// frame drawn hanging from the top of a grey desktop as tall as the panel
// can be, so that its height shows.
func (r *panelRig) run(d time.Duration) {
	for end := r.tt.Now().Add(d); r.tt.Now().Before(end); {
		r.tt.Advance(time.Second / 60)
		r.tt.Frame()
		r.sizes = append(r.sizes, fmt.Sprintf("%d %d", r.n, r.h))
		if r.dir != "" {
			r.write()
		}
		r.n++
	}
}

func (r *panelRig) write() {
	img := r.tt.Image()
	desk := image.NewRGBA(image.Rect(0, 0, panelWidth+48, panelMaxHeight+48))
	bg := color.RGBA{0xc8, 0xcc, 0xd4, 0xff}
	if r.dark {
		bg = color.RGBA{0x2a, 0x2c, 0x33, 0xff}
	}
	draw.Draw(desk, desk.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	draw.Draw(desk, img.Bounds().Add(image.Pt(24, 24)), img, image.Point{}, draw.Over)
	f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%04d.jpg", r.n)))
	if err != nil {
		r.t.Fatal(err)
	}
	jpeg.Encode(f, desk, &jpeg.Options{Quality: 90})
	f.Close()
}

// done writes the flyout's heights beside the frames.
func (r *panelRig) done() {
	if r.dir == "" {
		return
	}
	os.WriteFile(filepath.Join(r.dir, "heights.txt"), []byte(strings.Join(r.sizes, "\n")+"\n"), 0o644)
	os.WriteFile(filepath.Join(r.dir, "resizes.txt"), []byte(strings.Join(r.resize, "\n")+"\n"), 0o644)
}

// TestPanelKeepsItsRoomWhileWaiting: the menu bar's panel opened while
// this computer's engine does not answer yet (a window just started, the
// engine being changed) keeps the room its groups take, a ring in it,
// rather than folding to its head (the user, 11-17: "变得非常扁平"), and
// says it is connecting; the engine answering, they come in its place.
// Still nothing past startingPatience, it is the engine not running: the
// panel says so, as tall as that is.
func TestPanelKeepsItsRoomWhileWaiting(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	open := newPanelRig(t, a, false, "")
	open.run(500 * time.Millisecond)
	whole := open.h
	// Put away; the engine goes; opened again.
	d.Simulate("engine-away")
	r := newPanelRig(t, a, false, "")
	r.run(500 * time.Millisecond)
	if r.h < whole-8 {
		t.Errorf("waiting for the engine, the panel is %d tall; %d with all in", r.h, whole)
	}
	if !r.tt.HasText("正在连接…") || r.tt.HasText("Deneb 没在运行") {
		t.Errorf("waiting for the engine, the panel says %q", r.tt.Texts())
	}
	d.Simulate("engine-up")
	r.run(600 * time.Millisecond)
	if r.h < whole-1 || r.h > whole+1 || !r.tt.HasText("周末联机") {
		t.Errorf("the engine back, the panel is %d tall, not %d: %q", r.h, whole, r.tt.Texts())
	}

	// A window just started, the panel never shown: room for two LANs.
	a2, d2 := newDemoApp()
	d2.Manual()
	d2.Simulate("engine-away")
	r2 := newPanelRig(t, a2, false, "")
	r2.run(500 * time.Millisecond)
	if r2.h < whole-24 {
		t.Errorf("a panel never shown before, waiting: %d tall; %d with all in", r2.h, whole)
	}
	r2.run(startingPatience)
	if !r2.tt.HasText("Deneb 没在运行") || r2.h > whole/2+16 {
		t.Errorf("past startingPatience without the engine: %d tall, %q", r2.h, r2.tt.Texts())
	}
}

// TestPanelHeightFrames records the menu bar's panel in its flyout, both
// themes, when DENEB_PANEL names a folder: <theme>-<scene>/ frames, a 60th
// of a second apart, heights.txt the flyout's height in each.
//   - coming: opened as the window has just started, this computer's
//     engine not answering yet (after an update, it is being changed),
//     then answering with all it has;
//   - long: the same, the engine not answering past startingPatience;
//   - gone: open with all in, the engine goes, and comes back;
//   - reopen: put away and opened again, all in.
func TestPanelHeightFrames(t *testing.T) {
	root := os.Getenv("DENEB_PANEL")
	if root == "" {
		t.Skip("set DENEB_PANEL to a folder to record the panel's height")
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		scene := func(key string) string {
			dir := filepath.Join(root, theme+"-"+key)
			reset(t, dir)
			return dir
		}
		a, d := newDemoApp()
		d.Manual()
		d.Simulate("engine-away")
		r := newPanelRig(t, a, dark, scene("coming"))
		r.run(1500 * time.Millisecond)
		d.Simulate("engine-up")
		r.run(1500 * time.Millisecond)
		r.done()

		r = newPanelRig(t, a, dark, scene("gone"))
		r.run(800 * time.Millisecond)
		d.Simulate("engine-away")
		r.run(1500 * time.Millisecond)
		d.Simulate("engine-up")
		r.run(1500 * time.Millisecond)
		r.done()

		r = newPanelRig(t, a, dark, scene("reopen"))
		r.run(1000 * time.Millisecond)
		r.done()

		if dark {
			continue
		}
		a, d = newDemoApp()
		d.Manual()
		d.Simulate("engine-away")
		r = newPanelRig(t, a, dark, scene("long"))
		r.run(startingPatience + 1500*time.Millisecond)
		d.Simulate("engine-up")
		r.run(1500 * time.Millisecond)
		r.done()
	}
}
