package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

type avatarChoice struct {
	data  []byte
	image ui.ImageSource
}
type avatarEditor struct {
	device             model.DeviceID
	kind, originalKind string
	original, draft    []byte
	picture            ui.ImageSource
	palette, pattern   int
	choices            []avatarChoice
	busy, saving       bool
	err                string
}

func (a *app) openAvatar(id model.DeviceID) {
	d := a.snap.Device(id)
	if d == nil || d.Owner != a.snap.Me || !a.snap.Can("deviceAvatar") {
		return
	}
	e := &avatarEditor{device: id, kind: d.Kind, originalKind: d.Kind, original: bytes.Clone(d.Avatar), draft: bytes.Clone(d.Avatar), pattern: -1, picture: a.avatar(id)}
	e.loadChoices()
	a.avatarEdit = e
}

var decodedAvatarChoices [8][]avatarChoice

func (e *avatarEditor) loadChoices() {
	if cached := decodedAvatarChoices[e.palette]; cached != nil {
		e.choices = cached
		return
	}
	e.choices = make([]avatarChoice, len(avatarPatterns))
	for i := range avatarPatterns {
		b := presetAvatar(i, e.palette)
		pic, err := ui.DecodeImage(b)
		if err != nil {
			e.err = "内置头像无法读取"
			return
		}
		e.choices[i] = avatarChoice{b, pic}
	}
	decodedAvatarChoices[e.palette] = e.choices
}
func (a *app) pickAvatarDraft(e *avatarEditor) {
	if a.pickAvatar == nil || e.busy || e.saving {
		return
	}
	e.busy = true
	e.err = ""
	go func() {
		b, err := a.pickAvatar()
		a.post(func(*ui.Context) {
			e.busy = false
			if a.avatarEdit != e {
				return
			}
			if err != nil {
				e.err = err.Error()
				return
			}
			if len(b) == 0 {
				return
			}
			pic, err := ui.DecodeImage(b)
			if err != nil {
				e.err = "图片无法读取"
				return
			}
			e.draft, e.picture, e.pattern = b, pic, -1
		})
	}()
}
func (a *app) avatarLayer(c *ui.Context, p *palette) {
	e := a.avatarEdit
	close := func() {
		if e != nil && !e.saving && !e.busy {
			a.avatarEdit = nil
		}
	}
	modalPanel(c, p, "avatar-editor", "设备头像", 500, e != nil, close, func() {
		d := a.snap.Device(e.device)
		if d == nil {
			ui.Text(c, "设备已不可用").TextColor(p.ink3)
			return
		}
		ui.Row(c).Gap(18).AlignItems(ui.Center).Children(func() {
			deviceTilePicture(c, p, glyphFor(e.kind), ownerColors[0], 76, false, 0, e.picture)
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, d.Name).FontSize(14).FontWeight(600).SingleLine().MaxWidth(300)
				label := "上传图片"
				if e.busy {
					label = "读取中…"
				}
				if dialogButton(c, p, "upload", label, false, false, e.busy || e.saving).Clicked() {
					a.pickAvatarDraft(e)
				}
				ui.Text(c, "PNG / JPEG · 最大 8 MB").FontSize(11).TextColor(p.ink3)
			})
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			for i, pal := range avatarPalettes {
				col := ui.RGBA(pal.top.R, pal.top.G, pal.top.B, 1)
				b := ui.ButtonBase(c.Key("palette-"+pal.name)).Size(28, 28).Radius(999).Center().Background(col).Label(pal.name).Disabled(e.busy || e.saving)
				if e.palette == i {
					b.Border(2, p.bg).Shadow(0, 0, 0, 1.5, p.primary)
				}
				if b.Clicked() {
					e.palette = i
					e.loadChoices()
					if e.pattern >= 0 {
						ch := e.choices[e.pattern]
						e.draft, e.picture = ch.data, ch.image
					}
				}
			}
			ui.Spacer(c)
			b := ui.ButtonBase(c.Key("default")).Height(28).Padding(0, 8).Radius(8).Background(p.well).Label("恢复默认").Disabled(e.busy || e.saving)
			b.Children(func() { ui.Text(c, "恢复默认").FontSize(12).TextColor(p.ink2) })
			if b.Clicked() {
				a.askConfirm("恢复图标", d.Name, "移除自定义头像，保存后生效。", func(*ui.Context) { e.draft = nil; e.picture = nil; e.pattern = -1 })
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			for _, k := range deviceKinds {
				b := ui.ButtonBase(c.Key("type-"+k.kind)).Column().Size(58, 50).Gap(3).Radius(10).Center().Label(k.name).Background(p.well).Disabled(e.busy || e.saving)
				if e.kind == k.kind && len(e.draft) == 0 {
					b.Border(1.5, p.primary)
				}
				b.Children(func() {
					ui.Icon(c, deviceGlyphs[k.glyph]).Size(20, 20).TextColor(p.ink2)
					ui.Text(c, k.name).FontSize(10).TextColor(p.ink2).SingleLine()
				})
				if b.Clicked() {
					e.kind = k.kind
					e.draft = nil
					e.picture = nil
					e.pattern = -1
				}
			}
		})
		ui.Column(c).Gap(8).Children(func() {
			for row := 0; row < 2; row++ {
				ui.Row(c).Gap(8).Children(func() {
					for col := 0; col < 6; col++ {
						i := row*6 + col
						ch := e.choices[i]
						b := ui.ButtonBase(c.Key(fmt.Sprint("preset-", i))).Size(68, 68).Radius(16).Padding(4).Center().Label(avatarPatterns[i]).Disabled(e.busy || e.saving)
						if e.pattern == i {
							b.Background(p.primary.Alpha(.1)).Border(1.5, p.primary)
						} else if b.Hovered() {
							b.Background(p.well)
						}
						b.Children(func() {
							ui.Box(c).Size(56, 56).Radius(14).Clip().Children(func() { ui.Image(c, ch.image).Size(56, 56) })
						})
						if b.Clicked() {
							e.pattern = i
							e.draft, e.picture = ch.data, ch.image
							e.err = ""
						}
					}
				})
			}
		})
		if e.err != "" {
			ui.Text(c, e.err).FontSize(12).TextColor(danger).LineHeight(1.4)
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if dialogButton(c, p, "cancel", "取消", false, false, e.busy || e.saving).Clicked() {
				close()
			}
			label := "保存"
			if e.saving {
				label = "保存中…"
			}
			if dialogButton(c, p, "save", label, true, false, e.busy || e.saving || (bytes.Equal(e.original, e.draft) && e.kind == e.originalKind)).Clicked() {
				e.saving = true
				e.err = ""
				data := bytes.Clone(e.draft)
				run(a, c, func(ctx context.Context) (model.Result, error) {
					op := model.Op{Kind: model.OpDeviceSetAvatar, Device: e.device, Avatar: data}
					if len(data) == 0 && e.kind != e.originalKind {
						op = model.Op{Kind: model.OpDeviceSetKind, Device: e.device, DevKind: e.kind}
					}
					return a.store.Do(ctx, op)
				}, func(c *ui.Context, _ model.Result, err error) {
					e.saving = false
					if a.avatarEdit != e {
						return
					}
					if err != nil {
						e.err = message(err)
						return
					}
					a.avatarEdit = nil
					a.sync(c)
				})
			}
		})
	})
}
