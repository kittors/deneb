package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTrayIcons draws the menu bar's three icons into trayicons/, when
// DENEB_TRAY_ICONS is set: the 16-point mark in the middle of the 18 points
// MyGo makes a menu bar icon, so that none of it is scaled, at twice the
// size, black on clear, for macOS to tint as a template (DESIGN §7.1).
func TestTrayIcons(t *testing.T) {
	if os.Getenv("DENEB_TRAY_ICONS") == "" {
		t.Skip("set DENEB_TRAY_ICONS to draw the menu bar's icons")
	}
	for _, state := range []string{"solid", "dot", "hollow"} {
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Center().Children(func() { trayGlyph(c, state, black) })
		}, 18, 18)
		tt.SetScale(2)
		tt.Frame()
		// The tester draws on white: the glyph's darkness is its cover.
		src := tt.Image()
		img := image.NewNRGBA(src.Bounds())
		for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
			for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
				r, g, b, _ := src.At(x, y).RGBA()
				gray := (r + g + b) / 3 >> 8
				img.SetNRGBA(x, y, color.NRGBA{A: uint8(255 - gray)})
			}
		}
		f, err := os.Create(filepath.Join("trayicons", state+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
