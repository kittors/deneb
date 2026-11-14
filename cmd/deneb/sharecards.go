package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The cards of M3 (DESIGN §6.11): my device's "分享给", a person's card, the
// seats' card, and what the card of a device given to me says.

// shareSection is my device's "分享给": a chip for each person it is given
// to, then "+"; a click on a chip shows what it opens to them, its × takes
// it back; under them, which way it goes.
func (a *app) shareSection(c *ui.Context, p *palette, d *device) {
	m := a.snap.Device(d.dev)
	if m == nil || !a.snap.Can(model.FeatureShare) {
		return
	}
	ui.Text(c, "分享给").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
	ui.Row(c).Gap(8).Wrap().Children(func() {
		for _, sh := range m.Shares {
			who := a.people[string(sh.Person)]
			a.shareChip(c, p, d.dev, sh.Person, func() { ballOff(c, who, 26, a.refusedK(c, m, sh.Person)) },
				who.name, say("分享给", who.name))
		}
		a.sharePlus(c, p, d, len(m.Shares) == 0)
	})
	a.shareServices(c, p, m)
	if a.sharePick == d.id {
		a.sharePicker(c, p, d, m)
	}
	if line := oneWay(a, m); line != "" {
		ui.Text(c, line).FontSize(12).TextColor(p.ink3).Padding(8, 4, 0, 4)
	}
}

// oneWay says which way a device of mine given to people goes: "只有 Mia 能
// 连你，反过来不行" (DESIGN §12 item 12).
func oneWay(a *app, m *model.Device) string {
	switch len(m.Shares) {
	case 0:
		return ""
	case 1:
		return say("仅允许", a.people[string(m.Shares[0].Person)].name, "访问")
	}
	return "仅允许已分享成员访问"
}

// refusedK is how far a person's ball on a share chip is greyed: refused on
// the device, refusal wins over the share (DESIGN §6.11).
func (a *app) refusedK(c *ui.Context, m *model.Device, who model.PersonID) float32 {
	if m.Refused[who] {
		return 1
	}
	return 0
}

// shareChip is one share of dev: lead (a ball, or a device's tile) and a
// name. A click shows what it opens, below; on hover, × takes it back.
func (a *app) shareChip(c *ui.Context, p *palette, dev model.DeviceID, who model.PersonID, lead func(), name, label string) {
	key := string(dev) + "/" + string(who)
	chip := ui.Box(c.Key("share-"+key)).Height(34).Padding(0, 12, 0, 4).Radius(999).Background(p.well).Cursor(ui.CursorPointer).Label(label)
	chip.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.6}, Exit: &ui.Motion{Scale: 0.6}})
	open := a.shareOpen == key
	if chip.Hovered() || open {
		chip.Background(p.well2)
	}
	if open {
		chip.Border(1.5, p.primary.Alpha(0.5))
	}
	chip.AnimatePose(pressPose(chip, 0.95), ui.Snappy)
	hovered := chip.Hovered()
	chip.Children(func() {
		ui.Row(c).Height(34).Gap(8).AlignItems(ui.Center).Children(func() {
			lead()
			ui.Text(c, name).FontSize(13).FontWeight(500).SingleLine().MaxWidth(150)
		})
		if hovered {
			x := ui.ButtonBase(c.Key("take-back")).Absolute().Right(-6).Top(-6).Size(16, 16).Radius(999).Center().Label("收回").
				Background(p.bg).Border(1, p.line).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.12)).FocusRing(false)
			x.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.6}, Exit: &ui.Motion{Scale: 0.6}})
			x.Children(func() { ui.Icon(c, uiGlyphs["close"]).Size(10, 10).TextColor(p.ink2) })
			if x.Clicked() {
				a.revokeShare(c, dev, string(who))
				return
			}
		}
	})
	if chip.Clicked() {
		if open {
			a.shareOpen = ""
		} else {
			a.shareOpen, a.sharePick = key, ""
		}
	}
}

// sharePlus is the last chip of "分享给": "+", or, the device given to no
// one yet, "+ 分享给别人". A click shows whom it can go to.
func (a *app) sharePlus(c *ui.Context, p *palette, d *device, alone bool) {
	b := ui.ButtonBase(c.Key("share-plus")).Height(34).Radius(999).FocusRing(false).Gap(6).Label("分享给别人")
	if alone {
		b.Padding(0, 14, 0, 10)
	} else {
		b.Width(34).Center()
	}
	on := a.sharePick == d.id
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, (r.H-1.5)/2)
		if on {
			pt.FillPath(&path, p.primary.Alpha(0.08))
		}
		path.Dash(0, 4, 3)
		col := p.ink3
		if on {
			col = p.primary
		}
		pt.StrokePath(&path, 1.5, col)
	})
	b.AnimatePose(pressPose(b, 0.94), ui.Snappy)
	ink := p.ink2
	if b.Hovered() || on {
		ink = p.primary
	}
	b.Children(func() {
		ui.Icon(c, uiGlyphs["plus"]).Size(14, 14).TextColor(ink)
		if alone {
			ui.Text(c, "分享给别人").FontSize(13).FontWeight(500).TextColor(ink)
		}
	})
	if b.Clicked() {
		if on {
			a.sharePick = ""
		} else {
			a.sharePick, a.shareOpen = d.id, ""
		}
	}
}

// sharePicker is the row "+" opens: the people the device can go to, a
// click each (the click for the drag onto their ball). Someone new to my
// seats, the seats full, is faint, and says "位置满了" when clicked.
func (a *app) sharePicker(c *ui.Context, p *palette, d *device, m *model.Device) {
	now := c.Now()
	row := ui.Row(c.Key("share-picker")).Gap(8).Wrap().Padding(10, 0, 0, 0)
	row.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{}})
	row.Children(func() {
		for _, who := range a.shareCandidates(m) {
			full := a.seatsFull() && !a.seats().Holds(who)
			b := ui.ButtonBase(c.Key("pick-"+string(who))).Height(34).Padding(0, 12, 0, 4).Gap(8).Radius(999).Background(p.well).FocusRing(false)
			if b.Hovered() && !full {
				b.Background(p.well2)
			}
			if full {
				b.Opacity(0.4)
			}
			b.AnimatePose(pressPose(b, 0.95), ui.Snappy)
			note := a.pickNote == string(who) && now.Before(a.pickNoteUntil)
			if note {
				c.After(a.pickNoteUntil.Sub(now))
			}
			b.Children(func() {
				ball(c, a.people[string(who)], 26, false)
				label := a.people[string(who)].name
				if note {
					label = "位置满了"
				}
				t := ui.Text(c.Key("pick-name-"+label), label).FontSize(13).FontWeight(500).SingleLine()
				t.Transition(ui.ElementTransition{Duration: 140 * time.Millisecond, Enter: &ui.Motion{}})
			})
			if b.Clicked() {
				if full {
					a.pickNote, a.pickNoteUntil = string(who), now.Add(1600*time.Millisecond)
					continue
				}
				a.sharePick = ""
				a.shareTo(c, d, string(who))
			}
		}
	})
}

// shareCandidates are the people a device of mine can go to: everyone I
// see but me and those who have it, this LAN's members first.
func (a *app) shareCandidates(m *model.Device) []model.PersonID {
	var out []model.PersonID
	add := func(id model.PersonID) {
		if id == a.snap.Me || id == m.Owner || m.ShareTo(id) != nil || a.usesAlready(m, id) || slices.Contains(out, id) {
			return
		}
		if _, ok := a.snap.People[id]; ok {
			out = append(out, id)
		}
	}
	for _, id := range a.members {
		add(model.PersonID(id))
	}
	var rest []model.PersonID
	for id := range a.snap.People {
		rest = append(rest, id)
	}
	slices.SortFunc(rest, func(x, y model.PersonID) int {
		return strings.Compare(a.snap.People[x].Name, a.snap.People[y].Name)
	})
	for _, id := range rest {
		add(id)
	}
	return out
}

// shareServices is what the open share chip opens to its person, a switch
// each, as "开放给大家的" is (DESIGN §6.11).
func (a *app) shareServices(c *ui.Context, p *palette, m *model.Device) {
	dev, who, ok := strings.Cut(a.shareOpen, "/")
	if !ok || model.DeviceID(dev) != m.ID {
		return
	}
	sh := m.ShareTo(model.PersonID(who))
	if sh == nil {
		return
	}
	box := ui.Column(c.Key("share-services-" + who))
	box.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{}})
	box.Children(func() {
		ui.Text(c, say("开放给", a.people[who].name, "的")).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(12, 4, 8, 4)
		ui.Column(c).Radius(14).Background(p.well).Padding(0, 12).Dividers(1, p.line).Children(func() {
			if len(sh.Services) == 0 {
				ui.Row(c).Height(44).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "暂无服务").FontSize(13).TextColor(p.ink3)
				})
			}
			for _, v := range sh.Services {
				s := serviceOf(v)
				ui.Row(c.Key(s.name)).Height(44).Gap(10).Children(func() {
					ui.Icon(c, uiGlyphs[s.icon]).Size(18, 18).TextColor(p.ink2)
					ui.Text(c, s.name).FontSize(13).FontWeight(500).Grow(1)
					if toggle(c, p, s.on) {
						op := model.Op{Kind: model.OpServiceSet, Device: m.ID, Share: sh.ID, Person: sh.Person, Service: v.Key, On: !s.on}
						if s.on {
							a.askConfirm("关闭共享服务", s.name, a.people[who].name, func(c *ui.Context) { a.send(c, op) })
						} else {
							a.send(c, op)
						}
					}
				})
			}
		})
	})
}

// personCardView is a person's card, a click on their ball in the header:
// what I gave them, and what they gave me (DESIGN §6.11).
func (a *app) personCardView(c *ui.Context, p *palette, id string) {
	who := a.people[id]
	if who == nil || id == a.me || !a.snap.Can(model.FeatureShare) {
		a.selected = ""
		return
	}
	pid := model.PersonID(id)
	a.cardFrame(c, p, "person-"+id, func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ball(c, who, 40, false)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, who.name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
				ui.Text(c, a.roleOf(pid)).FontSize(12).TextColor(p.ink2).SingleLine().MaxWidth(cardNameWidth)
			})
			if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
				a.selected = ""
			}
		})
		ui.Text(c, say("你分享给", who.name, "的")).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
		var mine []*model.Device
		for _, dev := range a.snap.Mine {
			if m := a.snap.Device(dev); m != nil && m.ShareTo(pid) != nil {
				mine = append(mine, m)
			}
		}
		if len(mine) == 0 {
			ui.Text(c, say("拖入设备以共享给", who.name)).FontSize(12).TextColor(p.ink3).Padding(0, 4, 0, 4)
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, m := range mine {
				m := m
				a.shareChip(c, p, m.ID, pid, func() {
					a.profileTile(c, p, m.ID, glyphFor(m.Kind), a.people[string(m.Owner)].color, 26, false, 0)
				}, a.nameOf(m), say("分享了", a.nameOf(m)))
			}
		})
		for _, m := range mine {
			a.shareServices(c, p, m)
		}
		if len(mine) > 0 {
			ui.Text(c, say("仅允许", who.name, "访问")).FontSize(12).TextColor(p.ink3).Padding(8, 4, 0, 4)
		}
		var theirs []model.SharedDevice
		for _, sh := range a.snap.Shared {
			if sh.From == pid {
				theirs = append(theirs, sh)
			}
		}
		if len(theirs) == 0 {
			return
		}
		ui.Text(c, say(who.name, "分享给你的")).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, sh := range theirs {
				m := a.snap.Device(sh.Device)
				if m == nil {
					continue
				}
				b := ui.ButtonBase(c.Key("given-"+string(sh.Device))).Height(34).Padding(0, 12, 0, 4).Gap(8).Radius(999).Background(p.well).FocusRing(false)
				if b.Hovered() {
					b.Background(p.well2)
				}
				b.AnimatePose(pressPose(b, 0.95), ui.Snappy)
				b.Children(func() {
					a.profileTile(c, p, m.ID, glyphFor(m.Kind), who.color, 26, false, 0)
					ui.Text(c, short(m.Name)).FontSize(13).FontWeight(500).SingleLine().MaxWidth(150)
				})
				if b.Clicked() {
					// To the canvas of those, its card open.
					dev := sh.Device
					a.show(c, model.SharedLANID, false)
					a.selected = string(dev)
				}
			}
		})
	})
}

// roleOf is the second line of a person's card: who they are here.
func (a *app) roleOf(who model.PersonID) string {
	if a.shared() {
		n := 0
		for _, sh := range a.snap.Shared {
			if sh.From == who {
				n++
			}
		}
		return fmt.Sprintf("分享了 %d 台设备给你", n)
	}
	l := a.snap.LAN(a.lan)
	if l == nil {
		return ""
	}
	if l.Owner == who {
		return say(short(l.Name), "的拥有者")
	}
	return say(short(l.Name), "的成员")
}

// seatCardView is the card of the seats, a click on the dashed "邀请朋友"
// or, the seats full, on someone waiting to be let in: who takes them, and
// a way to free one (pricing-and-relays §1.5 item 2).
func (a *app) seatCardView(c *ui.Context, p *palette) {
	if !a.snap.Can(model.FeatureSeats) {
		a.selected = ""
		return
	}
	s := a.seats()
	full := s.Left <= 0
	a.cardFrame(c, p, "seats", func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			title := fmt.Sprintf("%d 个位置都用上了", s.Limit)
			if !full {
				title = fmt.Sprintf("还能加 %d 人", s.Left)
			}
			t := ui.Text(c.Key("seat-title-"+title), title).FontSize(15).FontWeight(600).Grow(1)
			t.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
			if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
				a.selected, a.seatFreeing, a.seatFor = "", false, ""
			}
		})
		if w := a.people[a.seatFor]; w != nil && full {
			ui.Text(c, say("腾出一个位置，", w.name, "就能加入")).FontSize(12).TextColor(p.ink2).Padding(4, 0, 0, 0)
		}
		ui.Row(c).Gap(10).Wrap().Padding(16, 0, 0, 0).Children(func() {
			for _, id := range s.People {
				id := string(id)
				el := ui.Box(c.Key("seat-"+id)).Size(34, 34)
				el.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.5}, Exit: &ui.Motion{Scale: 0.5}})
				el.Children(func() {
					ball(c, a.people[id], 34, false)
					if !a.seatFreeing || !full || id == a.me {
						return
					}
					x := ui.ButtonBase(c.Key("free")).Absolute().Right(-5).Top(-5).Size(18, 18).Radius(999).Center().Label("移出"+a.people[id].name).
						Background(p.bg).Border(1, p.line).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.12)).FocusRing(false)
					x.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.6}, Exit: &ui.Motion{Scale: 0.6}})
					x.AnimatePose(pressPose(x, 0.9), ui.Snappy)
					x.Children(func() { ui.Icon(c, uiGlyphs["close"]).Size(10, 10).TextColor(p.ink2) })
					if x.Clicked() {
						a.freeSeat(c, id)
					}
				})
			}
		})
		if a.seatFreeing && full {
			ui.Text(c, "移出成员并撤销其设备共享").FontSize(12).TextColor(p.ink3).Padding(12, 0, 0, 0)
		}
		ui.Row(c).Gap(8).Margin(16, 0, 0, 0).Children(func() {
			ui.Spacer(c)
			switch {
			case !full:
				l := a.snap.LAN(a.lan)
				if l != nil && !a.shared() && primaryButton(c, p, "seat-invite", "邀请朋友", 34, 14, 13).Clicked() {
					a.selected, a.seatFreeing, a.seatFor = "", false, ""
					a.inviteFriends(c)
				}
			case a.seatFreeing:
				if ghostButton(c, p, "seat-done", "好了").Clicked() {
					a.seatFreeing = false
				}
			default:
				if primaryButton(c, p, "seat-free", "腾一个位置", 34, 14, 13).Clicked() {
					a.seatFreeing = true
				}
			}
		})
	})
}

// cardFrame is a card of the card layer, as the LAN's: its look and how it
// comes in and goes, and its body under key.
func (a *app) cardFrame(c *ui.Context, p *palette, key string, body func()) {
	card := ui.Column(c.Key("card")).Width(cardWidth).Radius(20).Background(p.bg).Clip().
		Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05)).TransformOrigin(1, 0)
	if p.dark {
		card.Border(1, p.line)
	}
	if b := card.Bounds(); b.W > 0 {
		a.cardAt = ui.Rect{X: b.X - a.origin.X, Y: b.Y - a.origin.Y, W: b.W, H: b.H}
	}
	card.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Size: true,
		Enter: &ui.Motion{Scale: 0.96, Y: -8}, Exit: &ui.Motion{Scale: 0.97, Y: -6}})
	card.Children(func() {
		b := ui.Column(c.Key("body-" + key)).Padding(16)
		b.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
		b.Children(body)
	})
}

// givenWho is the second line of the card of a device given to me, or of
// a LAN's own device: whose it is to me ("阿明分享给你的", "设计部的设备");
// "" for any other.
func (a *app) givenWho(d *device) string {
	if sh := a.snap.SharedOf(d.dev); sh != nil {
		if sh.FromLAN != "" {
			return short(sh.FromLAN) + "的设备"
		}
		return say(a.people[string(sh.From)].name, "分享给你的")
	}
	if m := a.snap.Device(d.dev); m != nil && m.PublicLAN != "" {
		if l := a.snap.LAN(m.PublicLAN); l != nil {
			return short(l.Name) + "的设备"
		}
	}
	return ""
}

// serviceOf is a service as the cards list it.
func serviceOf(v model.Service) service {
	return service{icon: serviceIcon(v.Kind), name: v.Name, on: v.On, svc: v}
}
