package main

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTrayIcons: the bar's marks as each system gets them (systray.go,
// DESIGN §7.1, §7.2): macOS a template of two images (a TIFF); Windows the
// colour mark of the taskbar's mode, at the pixels of the display's scale
// or the next larger there are; Linux the dark taskbar's at 32 pixels. In
// every colour mark the eyes are cut out, so the taskbar shows through
// them, and the three states differ.
func TestTrayIcons(t *testing.T) {
	for _, st := range trayStates {
		tif := trayIconFor("darwin", st, false, 2)
		if !bytes.HasPrefix(tif, []byte("MM\x00*")) && !bytes.HasPrefix(tif, []byte("II*\x00")) {
			t.Errorf("%s: macOS's mark is not a TIFF", st)
		}
		for _, tc := range []struct {
			scale float64
			px    int
		}{{0, 16}, {1, 16}, {1.25, 20}, {1.5, 24}, {1.75, 32}, {2, 32}, {3, 32}} {
			for _, dark := range []bool{false, true} {
				b := trayIconFor("windows", st, dark, tc.scale)
				img, err := png.Decode(bytes.NewReader(b))
				if err != nil {
					t.Fatalf("%s %v dark=%v: %v", st, tc.scale, dark, err)
				}
				if r := img.Bounds(); r.Dx() != tc.px || r.Dy() != tc.px {
					t.Fatalf("%s at %v: %v, want %d px", st, tc.scale, r, tc.px)
				}
				if st != "dot" {
					// The left eye's middle, a whole pixel of each size's
					// grid (design/brand/c.py's GRID): clear when open, and
					// the star around it not.
					eye := map[int][2]int{16: {6, 8}, 20: {7, 10}, 24: {9, 12}, 32: {11, 16}}[tc.px]
					x, y := eye[0], eye[1]
					if a := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA).A; st == "solid" && a != 0 {
						t.Errorf("%s %d px dark=%v: the eye (%d,%d) shows alpha %d", st, tc.px, dark, x, y, a)
					}
					if a := color.NRGBAModel.Convert(img.At(tc.px/2, tc.px/4)).(color.NRGBA).A; a < 200 {
						t.Errorf("%s %d px dark=%v: the star's upper ray shows alpha %d", st, tc.px, dark, a)
					}
				}
			}
			if bytes.Equal(trayIconFor("windows", st, true, tc.scale), trayIconFor("windows", st, false, tc.scale)) {
				t.Errorf("%s at %v: the dark taskbar's mark is the light one's", st, tc.scale)
			}
		}
		if !bytes.Equal(trayIconFor("linux", st, false, 1), trayIconFor("windows", st, true, 2)) {
			t.Errorf("%s: Linux's mark is not the dark taskbar's at 32 pixels", st)
		}
	}
	for _, goos := range []string{"darwin", "windows"} {
		if bytes.Equal(trayIconFor(goos, "solid", false, 1), trayIconFor(goos, "hollow", false, 1)) ||
			bytes.Equal(trayIconFor(goos, "solid", false, 1), trayIconFor(goos, "dot", false, 1)) {
			t.Errorf("%s: two states share a mark", goos)
		}
	}
}

// TestTrayGlyphs draws the marks in one colour as the app's views do (the
// tray tip's star), 18 points at 2x, black on clear, into the folder
// DENEB_TRAY_GLYPHS names, to look at beside design/brand/final/menubar.
func TestTrayGlyphs(t *testing.T) {
	dir := os.Getenv("DENEB_TRAY_GLYPHS")
	if dir == "" {
		t.Skip("set DENEB_TRAY_GLYPHS to a folder to draw the marks into")
	}
	for _, st := range trayStates {
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Center().Children(func() { ui.Icon(c, map[string]*ui.SVG{"solid": starGlyph, "dot": trayStarDot, "hollow": trayStarHollow}[st]).Size(18, 18).TextColor(black) })
		}, 18, 18)
		tt.SetScale(2)
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, st+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
