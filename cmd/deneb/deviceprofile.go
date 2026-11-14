package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

type cachedAvatar struct {
	data  []byte
	image ui.ImageSource
}

func (a *app) avatar(id model.DeviceID) ui.ImageSource {
	d := a.snap.Device(id)
	if d == nil || len(d.Avatar) == 0 {
		delete(a.avatars, id)
		return nil
	}
	if a.avatars == nil {
		a.avatars = map[model.DeviceID]cachedAvatar{}
	}
	have, ok := a.avatars[id]
	if ok && bytes.Equal(have.data, d.Avatar) {
		return have.image
	}
	// Invalid pictures cannot be published by the server. Decoding still fails
	// visibly to the standard glyph if an old cache is corrupt.
	pic, err := ui.DecodeImage(d.Avatar)
	if err != nil {
		return nil
	}
	a.avatars[id] = cachedAvatar{data: bytes.Clone(d.Avatar), image: pic}
	return pic
}
func (a *app) profileTile(c *ui.Context, p *palette, id model.DeviceID, kind string, col ui.Color, size float32, selected bool, off float32) ui.Element {
	return deviceTilePicture(c, p, kind, col, size, selected, off, a.avatar(id))
}

func pickDeviceAvatar() ([]byte, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择设备头像", ButtonLabel: "使用这张图片", Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg"}}}})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	f, err := os.Open(paths[0])
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("请选择普通图片文件")
	}
	b, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	return proto.NormalizeDeviceAvatar(b)
}

// profileActions also supplies explicit labels for keyboard and screen readers,
// so editing this computer is discoverable without guessing a clickable title.
func (a *app) profileActions(c *ui.Context, p *palette, d *device) {
	if !d.mine {
		return
	}
	ui.Row(c).Gap(8).Margin(10, 0, 0, 0).Children(func() {
		action := func(key, label string, fn func()) {
			b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 10).Radius(9).Background(p.well).FocusRing(false).Label(label)
			if b.Hovered() {
				b.Background(p.well2)
			}
			b.AnimatePose(pressPose(b, .96), ui.Snappy)
			b.Children(func() { ui.Text(c, label).FontSize(12).TextColor(p.ink2).SingleLine() })
			if b.Clicked() {
				fn()
			}
		}
		action("rename-device", "重命名", func() {
			if m := a.snap.Device(d.dev); m != nil {
				a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.dev}, m.Name)
			}
		})
		if a.snap.Can("deviceAvatar") {
			action("edit-avatar", "修改头像", func() { a.openAvatar(d.dev) })
		}
	})
}
