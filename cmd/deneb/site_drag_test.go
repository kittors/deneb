package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSiteDragLoop writes the frames of the home page's second block, when
// DENEB_SITE_DRAG names a folder: dark, twice as sharp, 30 a second, in
// 0000.png…, and pointer.txt, where the pointer is in each (x, y in the
// window's points, and 1 while pressed). A device of mine comes out of the
// tray onto the LAN and joins it; design/tools/site-loop.py draws the
// pointer, crops it and makes the loop.
func TestSiteDragLoop(t *testing.T) {
	root := os.Getenv("DENEB_SITE_DRAG")
	if root == "" {
		t.Skip("set DENEB_SITE_DRAG to a folder for the home page's drag")
	}
	reset(t, root)
	r := newM4Rig(t)
	r.tt.SetDark(true)
	rest := ui.Point{X: r.a.origin.X + 760, Y: r.a.origin.Y + 520}
	r.move(rest.X, rest.Y)
	r.run(1500 * time.Millisecond)

	var ids []string
	for id := range r.a.slots {
		if !strings.HasPrefix(id, "relay:") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		t.Fatalf("no device of mine in the tray: %q", r.tt.Texts())
	}
	id := ids[0]

	r.tt.SetScale(2)
	var track strings.Builder
	n, k, pressed := 0, 0, false
	tick := func(d time.Duration) {
		for end := r.tt.Now().Add(d); r.tt.Now().Before(end); {
			r.tt.Advance(time.Second / 60)
			r.tt.Frame()
			if k++; k%2 == 0 {
				continue
			}
			f, err := os.Create(filepath.Join(root, fmt.Sprintf("%04d.png", n)))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, r.tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
			fmt.Fprintf(&track, "%.1f %.1f %d\n", r.px, r.py, map[bool]int{false: 0, true: 1}[pressed])
			n++
		}
	}
	glide := func(to func() ui.Point, steps int) {
		from := ui.Point{X: r.px, Y: r.py}
		for i := 1; i <= steps; i++ {
			p := to()
			k := ui.EaseInOut(float32(i) / float32(steps))
			r.move(from.X+(p.X-from.X)*k, from.Y+(p.Y-from.Y)*k)
			tick(time.Second / 60)
		}
	}

	tick(600 * time.Millisecond)
	glide(func() ui.Point { return r.traySlot(id) }, 40)
	tick(120 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	pressed = true
	tick(100 * time.Millisecond)
	glide(at(r.a.origin.X+660, r.a.origin.Y+250), 70)
	tick(160 * time.Millisecond)
	r.tt.Release(r.px, r.py)
	pressed = false
	tick(1800 * time.Millisecond)
	glide(func() ui.Point { return rest }, 40)
	tick(1400 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "pointer.txt"), []byte(track.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d frames; %s joined: %v", n, id, r.a.device(id) != nil)
}
