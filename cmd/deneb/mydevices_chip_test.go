package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestToggleChipNameWholeWhileHovered: a chip just chosen, the pointer
// still on it, shows its whole name, as it does once the pointer has left
// and come back. (It grew on the way, and its name stayed cut short, "✓ 周末
// 联…", until the pointer left.) Both themes.
func TestToggleChipNameWholeWhileHovered(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		r := myRig(t, "", theme, dark, "", 1180, 740)
		me := r.a.snap.Device("mac").Name
		r.openMine()
		r.tap(r.text(say("把", me, "的", "共享文件夹", "开放给", "周末联机")))
		r.run(900 * time.Millisecond)
		chip := say("在", "周末联机", "关闭", me, "的", "共享文件夹")
		b, ok := r.tt.Find(chip)
		if !ok {
			t.Fatalf("%s: no chip %q: %q", theme, chip, r.tt.Texts())
		}
		chosen := chipPixels(r.tt, b)
		// Away, and back on it: laid out anew, its name whole.
		r.glide(at(r.a.origin.X+100, r.a.origin.Y+450), 12)
		r.run(500 * time.Millisecond)
		r.glide(at(b.X+b.W/2, b.Y+b.H/2), 12)
		r.run(900 * time.Millisecond)
		if n := pixelsApart(chosen, chipPixels(r.tt, b)); n > 6 {
			t.Errorf("%s: the chip just chosen, hovered, differs from itself hovered again in %d pixels: its name cut short", theme, n)
		}
	}
}

// chipPixels copies the window's pixels in b.
func chipPixels(tt *ui.Tester, b ui.Rect) *image.RGBA {
	img := tt.Image()
	r := image.Rect(int(b.X), int(b.Y), int(b.X+b.W+0.5), int(b.Y+b.H+0.5)).Intersect(img.Bounds())
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			out.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out
}

// pixelsApart counts the pixels of a and b, of one size, more than a little
// apart in any channel.
func pixelsApart(a, b *image.RGBA) int {
	if a.Bounds() != b.Bounds() {
		return a.Bounds().Dx() * a.Bounds().Dy()
	}
	n := 0
	for i := 0; i+3 < len(a.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			d := int(a.Pix[i+c]) - int(b.Pix[i+c])
			if d > 24 || d < -24 {
				n++
				break
			}
		}
	}
	return n
}
