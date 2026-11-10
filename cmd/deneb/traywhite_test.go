package main

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestTrayIconsWhite: Linux's marks are the template marks in white, cut
// to the 16 points the star is drawn in, the same shapes: each pixel keeps
// its alpha, and every pixel that shows is white.
func TestTrayIconsWhite(t *testing.T) {
	for _, st := range []string{"solid", "dot", "hollow"} {
		tmpl, err := png.Decode(bytes.NewReader(trayIcons[st]))
		if err != nil {
			t.Fatal(err)
		}
		white, err := png.Decode(bytes.NewReader(trayIconsWhite[st]))
		if err != nil {
			t.Fatal(err)
		}
		if in := tmpl.Bounds().Inset(trayInset); white.Bounds().Dx() != in.Dx() || white.Bounds().Dy() != in.Dy() {
			t.Fatalf("%s: %v, want %v", st, white.Bounds(), in)
		}
		shown := 0
		for y := white.Bounds().Min.Y; y < white.Bounds().Max.Y; y++ {
			for x := white.Bounds().Min.X; x < white.Bounds().Max.X; x++ {
				a := color.NRGBAModel.Convert(tmpl.At(x+trayInset, y+trayInset)).(color.NRGBA)
				w := color.NRGBAModel.Convert(white.At(x, y)).(color.NRGBA)
				if w.A != a.A {
					t.Fatalf("%s (%d,%d): alpha %d, want %d", st, x, y, w.A, a.A)
				}
				if w.A > 0 {
					shown++
					if w.R != 255 || w.G != 255 || w.B != 255 {
						t.Fatalf("%s (%d,%d): %v, want white", st, x, y, w)
					}
				}
			}
		}
		if shown == 0 {
			t.Fatalf("%s: nothing shows", st)
		}
		// DENEB_TRAY_WHITE names a folder to look at them in.
		if dir := os.Getenv("DENEB_TRAY_WHITE"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, st+".png"), trayIconsWhite[st], 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
