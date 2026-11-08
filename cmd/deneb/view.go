package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

const sidebarWidth = 232
const headerHeight = 76

// view builds the window: the sidebar, the LAN's header, and the canvas
// with its card, tray and toast (design/DESIGN.md §7).
func (a *app) view(c *ui.Context) {
	// First what came from elsewhere: work done off the main thread, and
	// the store's snapshot, which the canvas is brought to.
	a.drain(c)
	a.sync(c)
	p := colors(c)
	switch {
	case !a.snap.Engine && a.snap.Version == 0, a.snap.LoggedIn && !a.snap.Loaded:
		a.startingView(c, p)
		return
	case !a.snap.LoggedIn:
		a.loginView(c, p)
		return
	}
	bar := c.TitleBar()
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	for k, fn := range a.sims {
		if root.Shortcut(ui.Cmd|ui.Alt, k) {
			fn(c, a)
		}
	}
	root.Children(func() {
		a.sidebar(c, p, bar)
		ui.Column(c).Grow(1).Background(p.bg).Children(func() {
			a.header(c, p)
			a.canvas(c, p)
			a.inviteLayer(c, p)
		})
	})
}

// ball is a person: a glossy ball of their color with their initial.
func ball(c *ui.Context, who *person, size float32, off bool) ui.Element {
	k := float32(0)
	if off {
		k = 1
	}
	return ballOff(c, who, size, k)
}

// ballOff is a person's ball k of the way to refused: grey, its initial
// gone, and a slash across it, drawn out from its middle.
func ballOff(c *ui.Context, who *person, size, k float32) ui.Element {
	col := who.color.Mix(ui.Hex("#a1a1aa"), k)
	b := ui.Box(c).Size(size, size).Radius(999).Center().
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.34, Y: 0.28}, Oklab: true,
			Stops: []ui.GradientStop{{Color: col.Mix(white, 0.45), At: 0}, {Color: col, At: 0.52}, {Color: col.Mix(black, 0.18), At: 1}},
		}).
		InsetShadow(0, -1.5, 3, 0, ui.RGBA(0, 0, 0, 0.14)).
		Shadow(0, 2, 6, 0, col.Alpha(0.3))
	b.Children(func() {
		if who.ch != "" && size >= 20 && k < 1 {
			ui.Text(c, who.ch).FontSize(size * 0.4).FontWeight(600).TextColor(white.Alpha(1 - k)).SingleLine()
		}
		if k > 0 {
			l := size * 0.84 * k
			ui.Box(c).Absolute().Size(2, l).Top(size/2 - l/2).Left(size/2 - 1).Radius(1).
				Background(white).Transform(ui.Rotation(-45))
		}
	})
	return b
}

func (a *app) sidebar(c *ui.Context, p *palette, bar ui.TitleBar) {
	top := max(bar.Height, 16) + 6
	ui.Column(c).Width(sidebarWidth).Background(p.well).Padding(top, 12, 12, 12).Gap(2).DragWindow().Children(func() {
		ui.Row(c).Gap(8).Padding(6, 8, 18, 8).Children(func() {
			ui.Icon(c, starGlyph).Size(18, 18).TextColor(p.primary)
			ui.Text(c, "Deneb").FontSize(15).FontWeight(600)
		})
		group(c, p, "局域网")
		// Each of my LANs, with the balls of its first members; a click puts
		// it on the canvas.
		dots := func(ids []model.PersonID) func() {
			return func() {
				ui.Row(c).Width(18).Children(func() {
					for _, id := range ids[:min(3, len(ids))] {
						ball(c, a.people[string(id)], 10, false).Margin(0, -4, 0, 0).Shadow(0, 0, 0, 1.5, p.well)
					}
				})
			}
		}
		for _, l := range a.snap.LANs {
			if navRow(c, p, "lan-"+string(l.ID), l.Name, l.ID == a.lan, dots(l.Members), "", a.flash(c, l.ID)) {
				a.show(c, l.ID, false)
			}
		}
		a.newLANRow(c, p)
		ui.Box(c).Height(16)
		group(c, p, "设备")
		navRow(c, p, "我的设备", "我的设备", false, func() { ui.Icon(c, uiGlyphs["home"]).Size(18, 18).TextColor(p.ink3) }, fmt.Sprint(len(a.snap.Mine)), 0)
		shared := ""
		if n := a.snap.SharedWithMe; n > 0 {
			shared = fmt.Sprint(n)
		}
		navRow(c, p, "共享给我", "共享给我", false, func() { ui.Icon(c, uiGlyphs["inbox"]).Size(18, 18).TextColor(p.ink3) }, shared, 0)
		ui.Spacer(c)
		ui.Row(c).Gap(10).Padding(8, 6, 4, 8).Children(func() {
			ball(c, a.people[a.me], 32, false)
			name := ""
			if me := a.snap.Person(a.snap.Me); me != nil {
				name = me.Name
			}
			ui.Text(c, name).FontSize(13).FontWeight(500).Grow(1).SingleLine()
			iconButton(c, p, "gear", "设置")
		})
	})
}

func group(c *ui.Context, p *palette, s string) {
	ui.Text(c, s).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(0, 8, 6, 8)
}

// navRow is a row of the sidebar; the chosen one is a white pill. A row
// that just came in opens out and lights up, flash (1 to 0) of the way. It
// reports whether it was clicked.
func navRow(c *ui.Context, p *palette, key, label string, on bool, lead func(), count string, flash float32) bool {
	row := ui.ButtonBase(c.Key(key)).Height(36).Padding(0, 10).Gap(10).Radius(10).FocusRing(false)
	if flash > 0 {
		row.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Collapse: true}})
	}
	switch {
	case on && flash > 0:
		row.Background(p.bg.Mix(p.primary, 0.18*flash)).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	case on:
		row.Background(p.bg).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	case flash > 0:
		row.Background(p.well.Mix(p.primary, 0.18*flash))
	case row.Hovered():
		row.Background(p.well2)
	}
	color := p.ink2
	if on {
		color = p.ink
	}
	row.Children(func() {
		lead()
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(color).Grow(1).SingleLine()
		if count != "" {
			ui.Text(c, count).FontSize(12).TextColor(p.ink3)
		}
	})
	return row.Clicked()
}

func iconButton(c *ui.Context, p *palette, icon, label string) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Size(30, 30).Radius(999).Center().Label(label).FocusRing(false)
	if b.Hovered() {
		b.Background(p.well2)
	}
	b.AnimatePose(pressPose(b, 0.92), ui.Snappy)
	b.Children(func() { ui.Icon(c, uiGlyphs[icon]).Size(18, 18).TextColor(p.ink2) })
	return b
}

// pressPose shrinks what the pointer presses, so a press answers at once.
func pressPose(e ui.Element, scale float32) ui.Pose {
	if e.Pressed() {
		return ui.Pose{Scale: scale}
	}
	return ui.Pose{}
}

func (a *app) header(c *ui.Context, p *palette) {
	l := a.snap.LAN(a.lan)
	if l == nil {
		// No LAN yet: the header keeps its place, with nothing in it.
		ui.Row(c).Height(headerHeight).DragWindow()
		return
	}
	name := l.Name
	ui.Row(c).Height(headerHeight).Padding(22, 24, 0, 28).AlignItems(ui.Start).Gap(16).DragWindow().Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, name).FontSize(20).FontWeight(600).SingleLine().MaxWidth(lanNameWidth)
			ui.Row(c).Gap(6).Children(func() {
				dot, line := a.summary(p)
				ui.Box(c).Size(7, 7).Radius(999).Background(dot).Shadow(0, 0, 0, 3, dot.Alpha(0.18))
				ui.Text(c, line).FontSize(13).TextColor(p.ink2)
			})
		})
		ui.Spacer(c)
		ui.Row(c).Gap(14).Children(func() {
			ui.Row(c).Children(func() {
				for i, id := range a.members {
					b := ball(c, a.people[id], 30, false).Shadow(0, 0, 0, 2, p.bg)
					if i > 0 {
						b.Margin(0, 0, 0, -6)
					}
				}
			})
			btn := ui.ButtonBase(c.Key("invite")).Height(34).Padding(0, 14).Gap(6).Radius(999).Background(p.primary).FocusRing(false).
				Shadow(0, 1, 2, 0, p.primary.Alpha(0.3)).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18))
			if btn.Hovered() {
				btn.Background(p.primary2)
			}
			btn.AnimatePose(pressPose(btn, 0.96), ui.Snappy)
			btn.Children(func() {
				ui.Icon(c, uiGlyphs["plus"]).Size(16, 16).TextColor(white)
				ui.Text(c, "邀请朋友").FontSize(13).FontWeight(500).TextColor(white)
			})
			if btn.Clicked() {
				a.inviteFriends(c)
			}
		})
	})
}

// flash is how lit a LAN's row of the sidebar is, as it comes in: from 1,
// the frame it first shows in, to 0 in 1.4 seconds (as the design demo's
// k-flash).
func (a *app) flash(c *ui.Context, id model.LANID) float32 {
	t, ok := a.flashFrom[id]
	if !ok {
		return 0
	}
	now := c.Now()
	if t.IsZero() {
		t = now
		a.flashFrom[id] = t
	}
	k := float32(now.Sub(t)) / float32(1400*time.Millisecond)
	if k >= 1 {
		delete(a.flashFrom, id)
		return 0
	}
	c.AnimationFrame()
	return max(1-ui.EaseOut(k), 0.001)
}

// lanNameWidth is the widest a LAN's name shows in the header, clear of the
// members' balls and the invite button.
const lanNameWidth = 420

// summary is the line under the LAN's name and the color of its dot: how
// many people and devices, and whether all of them are connected.
func (a *app) summary(p *palette) (ui.Color, string) {
	n, up, waiting := 0, 0, 0
	for _, d := range a.devices {
		if !d.gone.IsZero() {
			continue
		}
		n++
		switch {
		case d.path == "self" || linked(d.path):
			up++
		case d.path == "connecting":
			waiting++
		}
	}
	people := len(a.members)
	l := a.snap.LAN(a.lan)
	switch {
	case !a.snap.Engine:
		return p.ink3, "连不上这台电脑上的 Deneb，正在重试…"
	case l != nil && a.self != "" && !l.Has(model.DeviceID(a.self)):
		// Made on another of my devices: this computer waits in the tray,
		// and nothing here connects until it is in.
		return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，这台电脑还没加进来", people, n)
	case up == n:
		return p.ok, fmt.Sprintf("%d 个人，%d 台设备都连上了", people, n)
	case up+waiting == n:
		return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，正在连接…", people, n)
	}
	return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，%d 台连上了", people, n, up)
}
