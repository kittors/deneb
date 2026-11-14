package proto

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDeviceAvatarBounds(t *testing.T) {
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	img.Set(100, 80, color.NRGBA{R: 255, A: 255})
	png.Encode(&b, img)
	normalized, err := NormalizeDeviceAvatar(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(normalized))
	if err != nil || format != "png" || cfg.Width != 128 || cfg.Height != 128 {
		t.Fatalf("bad normalized picture: %+v %s %v", cfg, format, err)
	}
	for _, input := range [][]byte{[]byte("<svg/>"), make([]byte, (8<<20)+1)} {
		if _, err := NormalizeDeviceAvatar(input); err == nil {
			t.Fatal("accepted invalid or oversized image")
		}
	}
	if got, err := NormalizeDeviceAvatar(nil); got != nil || err != nil {
		t.Fatal("cannot restore glyph")
	}
	var large bytes.Buffer
	png.Encode(&large, image.NewGray(image.Rect(0, 0, 4097, 1)))
	if _, err := NormalizeDeviceAvatar(large.Bytes()); err == nil {
		t.Fatal("accepted oversized dimensions")
	}
}
