package main

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// modalPanel keeps focus, shortcuts and pointer events inside the dialog.
func modalPanel(c *ui.Context, p *palette, key, title string, width float32, open bool, close func(), body func()) {
	ui.Overlay(c.Key(key), func() {
		viewport, _ := c.Size()
		// Keep the host alive between openings: Enter needs an existing parent,
		// and Exit needs that parent to outlive the panel. Removing the entire
		// backdrop used to skip both of these animation lifecycle requirements.
		host := ui.Box(c.Key(key + "-host")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Background(black.Alpha(.28))
		target := float32(0)
		if open {
			target = 1
			host.Modal()
		} else {
			host.PassThrough()
		}
		fade := ui.Animation{Duration: 160 * time.Millisecond, Ease: ui.EaseOut}
		if !open {
			// EaseOut removes most opacity in the first few frames. Give the
			// Smooth retreat time to read, then ease the final fade to rest.
			fade = ui.Animation{Duration: 220 * time.Millisecond, Ease: ui.EaseInOut}
		}
		host.AnimatePose(ui.Pose{Fade: 1 - target}, fade)
		if open && host.OverlayShortcut(0, ui.KeyEscape) {
			close()
		}
		host.Children(func() {
			if !open {
				return
			}
			box := ui.Column(c.Key("dialog")).Width(width).MaxWidth(viewport-40).Padding(22).Gap(16).Radius(20).Background(p.bg).Shadow(0, 12, 40, 0, p.shadow).TransformOrigin(.5, .5)
			box.Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true, Enter: &ui.Motion{Scale: .96, Y: 8, Opacity: 1}, Exit: &ui.Motion{Scale: .96, Y: 8}})
			box.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).FontSize(17).FontWeight(600).Grow(1)
					if iconButton(c, p, "close", "关闭"+title).Clicked() {
						close()
					}
				})
				body()
			})
		})
	})
}
func dialogButton(c *ui.Context, p *palette, key, label string, primary, destructive, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(38).MinWidth(88).Padding(0, 18).Radius(999).Center().Label(label).Disabled(disabled)
	bg, ink := p.well, p.ink
	if primary {
		bg, ink = p.primary, white
	}
	if destructive {
		bg, ink = danger, white
	}
	if disabled {
		bg, ink = p.well2, p.ink3
	}
	b.Background(bg)
	if b.Hovered() && !disabled {
		b.Background(bg.Mix(p.ink, .06))
	}
	b.AnimatePose(pressPose(b, .97), ui.Snappy)
	b.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(ink).SingleLine() })
	return b
}

type renaming struct {
	id, name, original string
	op                 model.Op
	selected, saving   bool
	err                string
}

func (a *app) beginRename(op model.Op, name string) {
	a.renaming = &renaming{id: string(op.Device) + string(op.LAN) + string(op.Relay), name: name, original: name, op: op}
}
func renameError(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "请输入名称"
	}
	if utf8.RuneCountInString(name) > 40 {
		return "名称最多 40 个字"
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "名称包含无效字符"
		}
	}
	return ""
}
func (a *app) renameLayer(c *ui.Context, p *palette) {
	r := a.renaming
	close := func() {
		if r != nil && !r.saving {
			a.renaming = nil
		}
	}
	modalPanel(c, p, "rename-modal", "重命名", 420, r != nil, close, func() {
		ui.Text(c, r.original).FontSize(12).TextColor(p.ink3).SingleLine()
		field := ui.TextInputBase(c.Key("rename-input-"+r.id), &r.name).Height(44).FillWidth().Padding(0, 14).Radius(12).Background(p.well).Border(1, p.line).FontSize(14).Label("名称").Placeholder("输入名称").AutoFocus().Disabled(r.saving)
		if !r.selected {
			field.SetTextSelection(0, utf8.RuneCountInString(r.name))
			r.selected = true
		}
		if field.Focused() {
			field.Border(1.5, p.primary)
		}
		invalid := renameError(r.name)
		ui.Row(c).Height(18).AlignItems(ui.Center).Children(func() {
			msg := r.err
			if invalid != "" {
				msg = invalid
			}
			if msg != "" {
				ui.Text(c, msg).FontSize(12).TextColor(danger)
			}
			ui.Spacer(c)
			ui.Text(c, fmtCount(r.name)).FontSize(11).TextColor(p.ink3)
		})
		save := func() {
			if r.saving || invalid != "" || strings.TrimSpace(r.name) == r.original {
				return
			}
			r.saving = true
			r.err = ""
			op := r.op
			op.Name = strings.TrimSpace(r.name)
			run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) }, func(c *ui.Context, _ model.Result, err error) {
				r.saving = false
				if a.renaming != r {
					return
				}
				if err != nil {
					r.err = message(err)
					return
				}
				a.renaming = nil
				a.sync(c)
			})
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if dialogButton(c, p, "cancel", "取消", false, false, r.saving).Clicked() {
				close()
			}
			label := "确认"
			if r.saving {
				label = "保存中…"
			}
			if dialogButton(c, p, "save", label, true, false, r.saving || invalid != "" || strings.TrimSpace(r.name) == r.original).Clicked() {
				save()
			}
		})
		if field.Submitted() {
			save()
		}
	})
}
func fmtCount(s string) string {
	return strconv.Itoa(utf8.RuneCountInString(strings.TrimSpace(s))) + " / 40"
}

type confirmation struct {
	title, target, detail string
	action                func(*ui.Context)
}

func (a *app) askConfirm(title, target, detail string, action func(*ui.Context)) {
	if a.confirming != nil {
		return
	}
	a.confirming = &confirmation{title: title, target: target, detail: detail, action: action}
}
func (a *app) confirmLayer(c *ui.Context, p *palette) {
	q := a.confirming
	title := ""
	if q != nil {
		title = q.title
	}
	close := func() { a.confirming = nil }
	modalPanel(c, p, "confirm-action", title, 420, q != nil, close, func() {
		ui.Text(c, q.target).FontSize(15).FontWeight(500).LineHeight(1.4)
		if q.detail != "" {
			ui.Text(c, q.detail).FontSize(13).TextColor(p.ink2).LineHeight(1.5)
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if dialogButton(c, p, "cancel", "取消", false, false, false).AutoFocus().Clicked() {
				close()
			}
			if dialogButton(c, p, "confirm", "确认"+q.title, false, true, false).Clicked() {
				a.confirming = nil
				q.action(c)
			}
		})
	})
}
