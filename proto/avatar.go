package proto

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// NormalizeDeviceAvatar accepts bounded raster pictures, strips metadata and
// stores one small square PNG. An empty picture restores the device glyph.
func NormalizeDeviceAvatar(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > 8<<20 {
		return nil, errors.New("图片不能超过 8 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, errors.New("请选择 PNG 或 JPEG 图片")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		return nil, errors.New("图片尺寸不能超过 4096 × 4096")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("图片无法读取，请换一张")
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2, b.Min.X+(b.Dx()+side)/2, b.Min.Y+(b.Dy()+side)/2)
	dst := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
