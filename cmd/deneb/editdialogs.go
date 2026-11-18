package main

import (
	"context"
	"fmt"
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
			// The same room all round (DESIGN §3.9): 24 from every edge.
			box := ui.Column(c.Key("dialog")).Width(width).MaxWidth(viewport-40).Padding(24).Gap(16).Radius(20).Background(p.bg).Shadow(0, 12, 40, 0, p.shadow).TransformOrigin(.5, .5)
			// Its place too: a panel that grows (加一台's help opening) stays
			// centered as it grows, not jumping up at once.
			box.Transition(ui.ElementTransition{Animation: &ui.Smooth, Position: true, Size: true, Enter: &ui.Motion{Scale: .96, Y: 8, Opacity: 1}, Exit: &ui.Motion{Scale: .96, Y: 8}})
			box.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).FontSize(17).FontWeight(600).Grow(1)
					// The ×'s strokes end where all else in the panel does, its
					// button standing out into the room beside it, as a menu
					// row's light does (menuItem).
					if iconButton(c, p, "close", "关闭"+title).Margin(0, -(iconInset + glyphRoom), 0, 0).Clicked() {
						close()
					}
				})
				body()
			})
		})
	})
}

// dialogActions is a panel's buttons, at its foot, at its right end.
func dialogActions(c *ui.Context, buttons func()) {
	ui.Row(c).Gap(12).Justify(ui.End).Children(buttons)
}

// dialogButton is a dialog's button; a disabled one greys. Given work, the
// work it starts: while that is working it keeps its look, and past
// busyDelay its words give way to a ring (busyLabel). It is not disabled
// meanwhile, which would fade it: the caller takes no second click
// (busy.working).
func dialogButton(c *ui.Context, p *palette, key, label string, primary, destructive, disabled bool, work ...*busy) ui.Element {
	ring, working := underWay(c, work)
	b := ui.ButtonBase(c.Key(key)).Height(40).MinWidth(88).Padding(0, 16).Radius(999).Center().Label(label).Disabled(disabled && !working)
	bg, ink := p.well, p.ink
	if primary {
		bg, ink = p.primary, white
	}
	if destructive {
		bg, ink = danger, white
	}
	if disabled && !working {
		bg, ink = p.well2, p.ink3
	}
	b.Background(bg)
	if b.Hovered() && (!disabled || working) {
		b.Background(bg.Mix(p.ink, .06))
	}
	b.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	if !working {
		b.AnimatePose(pressPose(b, pressGive), ui.Snappy)
	}
	b.Children(func() {
		busyLabel(c, ring, ink, 16, func() { ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(ink).SingleLine() })
	})
	return b
}

type renaming struct {
	id, name, original string
	op                 model.Op
	selected, saving   bool
	work               busy // the save's, for 确认's ring (dialogButton)
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
		field := ui.TextInputBase(c.Key("rename-input-"+r.id), &r.name).Height(44).FillWidth().Padding(0, 16).Radius(12).Background(p.well).Border(1, p.line).FontSize(14).Label("名称").Placeholder("输入名称").AutoFocus().Disabled(r.saving)
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
			// What is wrong, as it comes, changes or goes: one at a time
			// (words.go).
			// This dialog's own: another one opened starts with none.
			swapWords(c, a.words, fmt.Sprintf("rename-error-%p", r), msg, 12, 400, danger, false)
			ui.Spacer(c)
			ui.Text(c, fmtCount(r.name)).FontSize(11).TextColor(p.ink3)
		})
		save := func() {
			if r.saving || r.work.working(c.Now()) || invalid != "" || strings.TrimSpace(r.name) == r.original {
				return
			}
			r.saving = true
			r.work.begin(c.Now())
			r.err = ""
			op := r.op
			op.Name = strings.TrimSpace(r.name)
			run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) }, func(c *ui.Context, _ model.Result, err error) {
				r.saving = false
				r.work.end()
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
		dialogActions(c, func() {
			if dialogButton(c, p, "cancel", "取消", false, false, r.saving).Clicked() {
				close()
			}
			if dialogButton(c, p, "save", "确认", true, false, r.saving || invalid != "" || strings.TrimSpace(r.name) == r.original, &r.work).Clicked() {
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
		dialogActions(c, func() {
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
