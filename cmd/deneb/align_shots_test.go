package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAlignShots writes the stills of the layout audit (DESIGN §3.9,
// design/plan/m7/layout-audit.md), when DENEB_ALIGN names a folder: every
// state the layout checks look at, at the window's smallest, usual and a large size, both
// themes, twice as sharp, as <theme>_<width>x<height>_<state>.png.
func TestAlignShots(t *testing.T) {
	root := os.Getenv("DENEB_ALIGN")
	if root == "" {
		t.Skip("set DENEB_ALIGN to a folder for the stills")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// DENEB_ALIGN_ONLY names the states to take, as "设置,托盘空着"; all by
	// default.
	want := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_ALIGN_ONLY"), ",") {
		if k != "" {
			want[k] = true
		}
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, size := range [][2]float32{{980, 640}, {1180, 740}, {1440, 900}} {
			w, h := size[0], size[1]
			for _, st := range alignStates {
				if len(want) > 0 && !want[st.name] {
					continue
				}
				r := alignRig(t, st.first, w, h)
				r.tt.SetDark(dark)
				r.run(400 * time.Millisecond)
				if !st.open(r, h) {
					t.Fatalf("%s: could not get there: %q", st.name, r.tt.Texts())
				}
				r.park()
				r.run(800 * time.Millisecond)
				r.tt.SetScale(2)
				r.tt.Frame()
				f, err := os.Create(filepath.Join(root, fmt.Sprintf("%s_%.0fx%.0f_%s.png", theme, w, h, st.name)))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, r.tt.Image()); err != nil {
					t.Fatal(err)
				}
				f.Close()
				r.tt.SetScale(1)
				r.tt.Frame()
			}
		}
	}
}
