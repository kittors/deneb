package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// My Devices (DESIGN §6.15, design/plan/m7/mydevices.md): every device of
// mine as a card. Its face says only whether it is online; under it, the
// LANs it is in, a chip each, and its services with the LANs each is open
// to. 详情 has the numbers the engine reports. "+ 加一台" tells how to bring
// another computer in, and says so when it comes.

func (a *app) setMyDevices(open bool) {
	a.myDevicesOpen = open
	if store, ok := a.store.(interface{ MonitorDevices(bool) }); ok {
		store.MonitorDevices(open)
	}
	if open {
		a.selected = ""
		a.more = false
		a.invite = nil
	} else {
		a.svcEdit = nil
	}
}

func (a *app) myDevicesPage(c *ui.Context, p *palette) {
	ww, wh := c.Size()
	a.cw, a.ch = ww-sidebarWidth, wh-a.strip
	page := ui.Column(c.Key("my-devices-page")).Grow(1).FillWidth()
	if b := page.Bounds(); b.W > 0 {
		a.origin = ui.Point{X: b.X, Y: b.Y}
	}
	page.Children(func() { a.myDevicesContent(c, p); a.toastView(c, p, a.cw, a.ch, c.Now()) })
}

// myDevices are mine, in the order the snapshot keeps.
func (a *app) myDevices() []*model.Device {
	var out []*model.Device
	for _, id := range a.snap.Mine {
		if d := a.snap.Device(id); d != nil && d.Owner == a.snap.Me {
			out = append(out, d)
		}
	}
	return out
}

func (a *app) myDevicesContent(c *ui.Context, p *palette) {
	devs := a.myDevices()
	// As a LAN's header (view.go): the title where a LAN's name is, in its
	// size, so that going between them nothing jumps; 添加设备 on its middle
	// line, as 邀请朋友 is there.
	ui.Column(c.Key("devices-header")).Height(headerHeight).Shrink(0).Padding(headerTop, 24, 0, 28).Gap(4).DragWindow().Children(func() {
		ui.Row(c).Height(titleLine).Gap(16).Children(func() {
			ui.Text(c, "我的设备").FontSize(20).FontWeight(600).SingleLine()
			ui.Spacer(c)
			if a.addPill(c, p).Clicked() {
				a.openAddOne(c)
			}
		})
		online := 0
		for _, d := range devs {
			if d.Online {
				online++
			}
		}
		line := fmt.Sprintf("%d 台设备，%d 台在线", len(devs), online)
		switch {
		case len(devs) == 0:
			line = "暂无设备"
		case len(devs) == 1:
			line = "1 台设备"
		case online == len(devs):
			line = fmt.Sprintf("%d 台设备，全部在线", len(devs))
		}
		ui.Row(c).Height(statusLine).Children(func() { ui.Text(c, line).FontSize(13).TextColor(p.ink2) })
	})
	// The line under the header shows once the list goes under it.
	under := float32(0)
	if a.mineScroll.Y > 0.5 {
		under = 1
	}
	edge := ui.Box(c.Key("devices-edge")).Height(1).Shrink(0).Background(p.line)
	edge.Opacity(edge.AnimateTo("under", under, ui.Animation{Duration: 160 * time.Millisecond}))
	list := ui.Scroll(c.Key("device-page-scroll")).Grow(1).TrackScroll(&a.mineScroll)
	if b := list.Bounds(); b.H > 0 {
		a.mineView = b
	}
	list.Children(func() {
		ui.Column(c).Padding(24, 28).Gap(16).Children(func() {
			for _, d := range devs {
				a.deviceCard(c, p, d)
			}
			a.addCard(c, p, len(devs) <= 1)
		})
	})
}

// addPill is the header's "+ 添加设备", the page's one primary action
// (design/components.md): always in sight, however long the list.
func (a *app) addPill(c *ui.Context, p *palette) ui.Element {
	return primaryButton(c, p, "add-one", "添加设备", 32, 16, 13, "plus")
}

// addCard is the dashed card at the list's end. Alone (only this computer),
// it is the page's empty state: taller, with one line on how others come.
func (a *app) addCard(c *ui.Context, p *palette, alone bool) {
	h := float32(64)
	if alone {
		h = 112
	}
	b := ui.ButtonBase(c.Key("add-card")).Height(h).FillWidth().Radius(16).FocusRing(false).Center().Label("添加设备")
	b.Transition(ui.ElementTransition{Animation: &ui.Smooth, Position: true, Size: true})
	hover := b.Hovered()
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, 15.25)
		if hover {
			pt.FillPath(&path, p.primary.Alpha(0.05))
		}
		path.Dash(0, 5, 4)
		col := p.ink3.Alpha(0.7)
		if hover {
			col = p.primary
		}
		pt.StrokePath(&path, 1.5, col)
	})
	b.AnimatePose(pressPose(b, 0.985), ui.Snappy)
	ink := p.ink2
	if hover {
		ink = p.primary
	}
	b.Children(func() {
		ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, uiGlyphs["plus"]).Size(16, 16).TextColor(ink)
				ui.Text(c, "添加设备").FontSize(13).FontWeight(500).TextColor(ink)
			})
			if alone {
				ui.Text(c, "其他电脑安装 Deneb 并用同一邮箱登录后，显示在这里").FontSize(12).TextColor(p.ink3)
			}
		})
	})
	if b.Clicked() {
		a.openAddOne(c)
	}
}

// deviceStatus is a device's second line, in everyday words (§2): online,
// when it last was, this computer, its login run out.
func (a *app) deviceStatus(d *model.Device, now time.Time) (string, bool) {
	switch {
	case d.ID == a.snap.Self:
		return "这台电脑", true
	case d.Login.Expired:
		return "登录已过期", false
	case d.Online:
		return "在线", true
	case !d.LastSeen.IsZero():
		return ago(now.Sub(d.LastSeen)) + "在线", false
	}
	return "不在线", false
}

func (a *app) deviceCard(c *ui.Context, p *palette, d *model.Device) {
	now := c.Now()
	lans := a.lansOf(d)
	// Opening 详情 grows the card, its words clipped to it, and the cards
	// under it make room as it grows (DESIGN §5: Smooth).
	card := ui.Column(c.Key("host-" + string(d.ID))).Padding(16).Gap(16).Radius(16).Background(p.well).Clip().
		Transition(ui.ElementTransition{Animation: &ui.Smooth, Position: true, Size: true, Enter: &ui.Motion{Y: 8, Scale: 0.98}})
	if a.revealCard == d.ID {
		a.revealMine(c, card.Bounds())
	}
	if at, ok := a.cardFlash[d.ID]; ok {
		// Just come in: a soft blue, gone in 1.2 s.
		if k := min(1, 1-float32(now.Sub(at))/float32(1200*time.Millisecond)); k > 0 {
			card.Background(p.well.Mix(p.primary, 0.14*k))
			c.AnimationFrame()
		} else {
			delete(a.cardFlash, d.ID)
		}
	}
	card.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			avatar := ui.ButtonBase(c.Key("avatar")).Size(44, 44).Radius(13).Label("修改" + d.Name + "头像")
			avatar.Children(func() { a.profileTile(c, p, d.ID, glyphFor(d.Kind), ownerColors[0], 44, false, 0) })
			if avatar.Clicked() {
				a.openAvatar(d.ID)
			}
			ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
				ui.Text(c, d.Name).FontSize(15).FontWeight(600)
				status, on := a.deviceStatus(d, now)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					dot := p.ink3.Alpha(0.6)
					if on {
						dot = p.ok
					}
					ui.Box(c).Size(6, 6).Radius(3).Background(dot)
					ui.Text(c, status).FontSize(12).TextColor(p.ink3).SingleLine()
				})
			})
			if textButton(c, p, "rename", "重命名", false).Label("重命名" + d.Name).Clicked() {
				a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.ID}, d.Name)
			}
			details := "详情"
			if a.deviceDetails == d.ID {
				details = "收起"
			}
			// 详情 is a text button as 重命名 beside it: both are the card's
			// own small things, not what the page is for.
			if textButton(c, p, "details", details, false).Label(details + d.Name).Clicked() {
				if a.deviceDetails == d.ID {
					a.deviceDetails = ""
				} else {
					a.deviceDetails = d.ID
					a.revealCard, a.revealBy = d.ID, c.Now().Add(700*time.Millisecond)
				}
			}
		})
		a.lanChipsRow(c, p, d, lans)
		a.servicesBlock(c, p, d, lans)
		if a.deviceDetails == d.ID {
			more := ui.Column(c.Key("details")).Gap(16)
			more.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
			more.Children(func() { a.deviceDetailsView(c, p, d, now) })
		}
	})
}

// section is a card's row: a small label on the left, on the middle of
// the first line (first high) of what it is about, on the right.
func section(c *ui.Context, p *palette, label string, first float32, body func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Row(c).Width(44).Height(first).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(p.ink3).SingleLine()
		})
		ui.Column(c).Grow(1).Shrink(1).Gap(8).Children(body)
	})
}

// revealMine glides the list, while revealBy, so that the card in box b shows
// whole, 16 from its edges, but never past its top: as little as shows it,
// as the card grows. Scrolling by hand stops it.
func (a *app) revealMine(c *ui.Context, b ui.Rect) {
	v, s := a.mineView, &a.mineScroll
	if c.Now().After(a.revealBy) || a.revealTo != 0 && !s.Scrolling() && abs32(s.Y-a.revealTo) > 1 {
		a.revealCard, a.revealTo = "", 0
		return
	}
	c.AnimationFrame()
	if b.H == 0 || v.H == 0 {
		return
	}
	const room = 16
	top, bottom := b.Y-v.Y, b.Y-v.Y+b.H
	y := s.Y
	switch {
	case top < room:
		y -= room - top
	case bottom > v.H-room:
		y += min(bottom-(v.H-room), top-room)
	}
	y = max(0, min(y, s.MaxY))
	if abs32(y-s.Y) > 0.5 && abs32(y-a.revealTo) > 0.5 {
		s.ScrollTo(0, y, ui.Smooth)
		a.revealTo = y
	}
}

// lansOf are the LANs a device of mine can go in, in the sidebar's order:
// mine, and those where members bring their own (not a studio that hands
// out its own devices); a LAN I wait to be let into is not one yet.
func (a *app) lansOf(d *model.Device) []*model.LAN {
	var out []*model.LAN
	for _, l := range a.snap.LANs {
		if l.Waiting || l.Mode == model.ModeShare && l.Owner != a.snap.Me {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (a *app) lanChipsRow(c *ui.Context, p *palette, d *model.Device, lans []*model.LAN) {
	section(c, p, "局域网", 28, func() {
		if len(lans) == 0 {
			ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "暂无局域网").FontSize(13).TextColor(p.ink3)
			})
			return
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, l := range lans {
				a.lanChip(c, p, d, l)
			}
		})
	})
}

// lanChip is one LAN, a toggle chip as a service's below
// (design/components.md 多选胶囊): ticked when the device is in it, dashed
// when not. A click puts it in, with undo; taking it out asks once in place,
// as the others there lose it. Waiting for its owner, a click takes the
// asking back. This computer cannot leave from here (DESIGN §6.2).
func (a *app) lanChip(c *ui.Context, p *palette, d *model.Device, l *model.LAN) {
	in, waits := l.Has(d.ID), l.HasPending(d.ID)
	owner := a.personName(l.Owner)
	st, label, tip := chipOff, say("把", d.Name, "加入", l.Name), "加入"+l.Name
	switch {
	case waits:
		st, label, tip = chipWait, say("撤回", d.Name, "加入", l.Name, "的请求"), say("等待", owner, "同意，点按撤回")
	case in && d.ID == a.snap.Self:
		st, label, tip = chipFixed, say(d.Name, "在", l.Name), "正在使用的这台电脑不能移出"
	case in:
		st, label, tip = chipOn, say("把", d.Name, "移出", l.Name), "移出"+l.Name
	}
	extra := ""
	if waits {
		extra = "等待同意"
	}
	if !a.toggleChip(c, p, "lan-"+string(d.ID)+"-"+string(l.ID), l.Name, label, tip, "移出", st, extra) {
		return
	}
	lan, dev, name := l.ID, d.ID, short(d.Name)
	add := model.Op{Kind: model.OpLANAddDevice, LAN: lan, Device: dev}
	out := model.Op{Kind: model.OpLANRemoveDevice, LAN: lan, Device: dev}
	switch st {
	case chipOff:
		a.send(c, add)
		text := "已将" + name + "加入" + short(l.Name)
		if l.ApproveDevices && l.Owner != a.snap.Me {
			text = say(name, "正在等待", owner, "同意")
		}
		a.notify(c, nil, text, func() { a.send(c, out) })
	case chipOn:
		a.send(c, out)
		a.notify(c, nil, "已将"+name+"移出"+short(l.Name), func() { a.send(c, add) })
	case chipWait:
		a.send(c, out)
		a.notify(c, nil, "已撤回"+name+"加入"+short(l.Name)+"的请求", func() { a.send(c, add) })
	}
}

// chipState is a toggle chip's state.
type chipState int

const (
	chipOff   chipState = iota // dashed: not chosen
	chipOn                     // tinted and ticked: chosen
	chipWait                   // dashed in the primary color: asked, waiting
	chipFixed                  // ticked, neutral, and cannot be unchosen here
)

// armed reports whether the inline confirm asks on key now
// (design/components.md 原地确认).
func (a *app) armed(c *ui.Context, key string) bool {
	return a.armedKey == key && c.Now().Before(a.armedUntil)
}

// arm makes key ask: "再点一下，…", for 4 s or until the pointer leaves it.
func (a *app) arm(c *ui.Context, key string) {
	a.armedKey, a.armedUntil = key, c.Now().Add(4*time.Second)
	c.After(4 * time.Second)
}

// toggleChip is one choice of a row of toggle chips (design/components.md
// 多选胶囊): the LANs a device is in, the LANs a service is open to. It
// reports a click that is to be acted on: choosing at once; unchoosing
// once it asked in place, red, "再点一下，<off>" (原地确认).
func (a *app) toggleChip(c *ui.Context, p *palette, key, name, label, tip, off string, st chipState, extra string) bool {
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 12, 0, 8).Gap(4).Radius(999).FocusRing(false).Shrink(0).Label(label).Tooltip(tip)
	// Its words keep their width while it grows or shrinks, clipped to it
	// on the way: laid out at its size on the way, they were cut short,
	// "✓ 周末联…", and stayed so where the growing stopped a hair short.
	b.Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true, Colors: true}).Clip()
	hover := b.Hovered()
	asking := st == chipOn && a.armed(c, key)
	if asking && !hover && !b.Focused() {
		// Moved off it: the question goes.
		a.armedKey, asking = "", false
	}
	if st == chipOff || st == chipWait || asking {
		b.Padding(0, 12)
	}
	switch {
	case asking:
		b.Background(danger.Alpha(0.1))
	case st == chipFixed:
		// Chosen, but not to be changed here: neutral, no hover, no hand,
		// no press; the tooltip says why (AGENTS.md §1.3 禁用).
		b.Background(p.well2)
	case st == chipOn:
		bg := p.primary.Alpha(0.12)
		if hover {
			bg = p.primary.Alpha(0.18)
		}
		b.Background(bg)
	default:
		wait := st == chipWait
		b.Draw(func(pt *ui.Painter, r ui.Rect) {
			var path ui.Path
			roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, (r.H-1.5)/2)
			path.Dash(0, 4, 3)
			col := p.ink3.Alpha(0.7)
			switch {
			case hover:
				col = p.primary
			case wait:
				col = p.primary.Alpha(0.5)
			}
			pt.StrokePath(&path, 1.5, col)
		})
	}
	if st == chipFixed {
		b.Cursor(ui.CursorDefault)
	} else {
		b.AnimatePose(pressPose(b, 0.95), ui.Snappy)
	}
	b.Children(func() {
		switch {
		case asking:
			ui.Text(c.Key("ask"), "再点一下，"+off).FontSize(12).FontWeight(500).TextColor(danger).SingleLine().Shrink(0).
				Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
		case st == chipOn || st == chipFixed:
			ink := p.primary
			if st == chipFixed {
				ink = p.ink2
			}
			checkMark(c, ink, 12)
			ui.Text(c.Key("name"), name).FontSize(12).FontWeight(500).TextColor(ink).MaxWidth(220).SingleLine().Shrink(0)
		default:
			ink := p.ink2
			if hover {
				ink = p.primary
			}
			ui.Text(c.Key("name"), name).FontSize(12).FontWeight(500).TextColor(ink).MaxWidth(220).SingleLine().Shrink(0)
			if extra != "" {
				ui.Text(c, extra).FontSize(12).TextColor(p.ink3).SingleLine()
			}
		}
	})
	if !b.Clicked() || st == chipFixed {
		return false
	}
	if st == chipOn && !asking {
		a.arm(c, key)
		return false
	}
	a.armedKey = ""
	return true
}

// svcRow is one service of a device, and whether it is open in each LAN
// the device is in.
type svcRow struct {
	svc   model.Service
	on    map[model.LANID]bool
	local bool // this computer's, found listening, not set up yet
}

// deviceServices are a device's services: what it has set up, what a LAN
// lists of it besides, and this computer's found listening.
func (a *app) deviceServices(d *model.Device, in []*model.LAN) []svcRow {
	var rows []svcRow
	add := func(s model.Service, local bool) {
		if slices.ContainsFunc(rows, func(r svcRow) bool { return r.svc.Key == s.Key }) {
			return
		}
		r := svcRow{svc: s, on: map[model.LANID]bool{}, local: local}
		for _, l := range in {
			for _, v := range l.Services[d.ID] {
				if v.Key == s.Key && v.On {
					r.on[l.ID] = true
				}
			}
		}
		rows = append(rows, r)
	}
	for _, s := range d.Catalog {
		add(s, false)
	}
	for _, l := range in {
		for _, s := range l.Services[d.ID] {
			add(s, false)
		}
	}
	if d.ID == a.snap.Self {
		for _, s := range a.snap.Local {
			add(s, true)
		}
	}
	return rows
}

// svcName is the name to show: with its port when another has the same.
func svcName(rows []svcRow, s model.Service) string {
	name := s.Name
	if name == "" {
		name = model.KindName(s.Kind)
	}
	for _, r := range rows {
		other := r.svc.Name
		if other == "" {
			other = model.KindName(r.svc.Kind)
		}
		if other == name && r.svc.Key != s.Key {
			return name + " · " + s.Ports
		}
	}
	return name
}

func (a *app) servicesBlock(c *ui.Context, p *palette, d *model.Device, lans []*model.LAN) {
	var in []*model.LAN
	for _, l := range lans {
		if l.Has(d.ID) {
			in = append(in, l)
		}
	}
	// The label sits on the first line: a service or the words (28
	// high), or the editor's fields (32).
	first := float32(28)
	rows := a.deviceServices(d, in)
	editing := a.svcEdit != nil && a.svcEdit.dev == d.ID
	if len(in) > 0 && len(rows) == 0 && editing {
		first = 32
	}
	section(c, p, "服务", first, func() {
		if len(in) == 0 {
			ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "加入局域网后可开放服务").FontSize(13).TextColor(p.ink3)
			})
			return
		}
		if len(rows) == 0 && !editing {
			ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "暂无服务").FontSize(13).TextColor(p.ink3)
			})
		}
		for _, r := range rows {
			a.serviceRow(c, p, d, in, rows, r)
		}
		if h, ok := a.cardHints[d.ID]; ok {
			if c.Now().Before(h.until) {
				ui.Text(c.Key("hint-"+h.text), h.text).FontSize(12).TextColor(p.ink3).Transition(ui.ElementTransition{Animation: &ui.Smooth, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{}})
				c.After(h.until.Sub(c.Now()))
			} else {
				delete(a.cardHints, d.ID)
			}
		}
		if e := a.svcEdit; e != nil && e.dev == d.ID {
			a.serviceEditor(c, p, d, in, e)
		} else {
			a.addServiceChip(c, p, d)
		}
	})
}

// serviceRow is a service: its icon and name, then one toggle chip per LAN
// the device is in (ticked where it is open); on the pointer, × takes it off
// the device, after asking in place.
func (a *app) serviceRow(c *ui.Context, p *palette, d *model.Device, in []*model.LAN, rows []svcRow, r svcRow) {
	row := ui.Row(c.Key("svc-" + r.svc.Key)).Gap(12).AlignItems(ui.Start)
	row.Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true, Enter: &ui.Motion{Y: 4, Scale: 0.98}, Exit: &ui.Motion{Collapse: true}})
	hover := row.Hovered()
	name := svcName(rows, r.svc)
	row.Children(func() {
		// A long name takes two lines at most; the row grows for it.
		ui.Row(c).Width(176).Shrink(0).MinHeight(28).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, uiGlyphs[serviceIcon(r.svc.Kind)]).Size(16, 16).TextColor(p.ink2).Shrink(0)
			ui.Text(c, name).FontSize(13).FontWeight(500).Grow(1).Shrink(1).MaxLines(2)
		})
		ui.Row(c).Grow(1).Shrink(1).Gap(8).Wrap().Children(func() {
			for _, l := range in {
				a.serviceChip(c, p, d, r, name, l)
			}
		})
		if r.local || !a.snap.Can(model.FeatureServiceRemove) {
			ui.Box(c).Size(28, 28).Shrink(0)
			return
		}
		// ×, asking in place: "再点一下，删除" (design/components.md).
		key := "remove-" + string(d.ID) + "-" + r.svc.Key
		x := ui.ButtonBase(c.Key("remove")).Height(28).MinWidth(28).Radius(999).Center().FocusRing(false).Shrink(0).
			Label(say("删除", d.Name, "的", name)).Tooltip("删除" + name)
		x.Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true, Colors: true})
		asking := a.armed(c, key)
		if asking && !x.Hovered() && !x.Focused() {
			a.armedKey, asking = "", false
		}
		shown := float32(0)
		if hover || x.Focused() || asking {
			shown = 1
		}
		x.Opacity(x.AnimateTo("shown", shown, ui.Animation{Duration: 160 * time.Millisecond}))
		switch {
		case asking:
			x.Padding(0, 12).Background(danger.Alpha(0.1))
		case x.Hovered():
			x.Background(p.well2)
		}
		x.AnimatePose(pressPose(x, 0.95), ui.Snappy)
		x.Children(func() {
			if asking {
				ui.Text(c.Key("ask"), "再点一下，删除").FontSize(12).FontWeight(500).TextColor(danger).SingleLine().
					Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
				return
			}
			ui.Icon(c, uiGlyphs["close"]).Size(12, 12).TextColor(p.ink3)
		})
		if x.Clicked() {
			if !asking {
				a.arm(c, key)
				return
			}
			a.armedKey = ""
			a.send(c, model.Op{Kind: model.OpServiceRemove, Device: d.ID, Service: r.svc.Key})
			a.notify(c, nil, "已删除「"+name+"」", nil)
		}
	})
}

// serviceChip is a service in one LAN: a click opens it there; closing it
// asks in place first. The same switch as the LAN card's (§6.5).
func (a *app) serviceChip(c *ui.Context, p *palette, d *model.Device, r svcRow, name string, l *model.LAN) {
	on := r.on[l.ID]
	st, label, tip := chipOff, say("把", d.Name, "的", name, "开放给", l.Name), "开放给"+l.Name
	if on {
		st, label, tip = chipOn, say("在", l.Name, "关闭", d.Name, "的", name), "在"+l.Name+"关闭"
	}
	if !a.toggleChip(c, p, "on-"+string(d.ID)+"-"+r.svc.Key+"-"+string(l.ID), l.Name, label, tip, "关闭", st, "") {
		return
	}
	op := model.Op{Kind: model.OpServiceSet, LAN: l.ID, Device: d.ID, Service: r.svc.Key, On: !on}
	if !on && r.local {
		// Found listening on this computer, never set up: it goes in as
		// it is.
		svc := r.svc
		op.Def = &svc
	}
	a.send(c, op)
	if !on && r.svc.Kind == "shell" && !a.shellTold {
		a.shellTold = true
		a.hint(d.ID, "开放命令行后，对方可以经这台电脑访问其他地方，请只开放给信任的人。", 5*time.Second)
	}
}

// cardHint is a line under a card's services that goes by itself.
type cardHint struct {
	text  string
	until time.Time
}

func (a *app) hint(dev model.DeviceID, text string, d time.Duration) {
	if a.cardHints == nil {
		a.cardHints = map[model.DeviceID]cardHint{}
	}
	a.cardHints[dev] = cardHint{text: text, until: a.now().Add(d)}
}

// svcEdit is a service being set up by hand: a name and a port.
type svcEdit struct {
	dev        model.DeviceID
	name, port string
	err        string
	errPort    bool
	saving     bool
	work       busy // the save's, for 添加's ring (primaryButton)
}

func (a *app) addServiceChip(c *ui.Context, p *palette, d *model.Device) {
	b := ui.ButtonBase(c.Key("add-svc")).Height(28).Padding(0, 12, 0, 8).Gap(4).Radius(999).FocusRing(false).AlignSelf(ui.Start).Label(say("给", d.Name, "添加服务"))
	hover := b.Hovered()
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, (r.H-1.5)/2)
		path.Dash(0, 4, 3)
		col := p.ink3.Alpha(0.7)
		if hover {
			col = p.primary
		}
		pt.StrokePath(&path, 1.5, col)
	})
	b.AnimatePose(pressPose(b, 0.95), ui.Snappy)
	ink := p.ink2
	if hover {
		ink = p.primary
	}
	b.Children(func() {
		ui.Icon(c, uiGlyphs["plus"]).Size(12, 12).TextColor(ink)
		ui.Text(c, "添加服务").FontSize(12).FontWeight(500).TextColor(ink)
	})
	if b.Clicked() {
		a.svcEdit = &svcEdit{dev: d.ID}
	}
}

// serviceEditor is "添加服务" open: a name, a port, 添加. The kind is
// guessed from the port; TCP and UDP both open; it starts open to no one.
func (a *app) serviceEditor(c *ui.Context, p *palette, d *model.Device, in []*model.LAN, e *svcEdit) {
	box := ui.Column(c.Key("svc-editor")).Gap(8)
	box.Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Collapse: true}})
	box.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			field := func(key string, value *string, label string, w float32, bad bool) ui.Element {
				f := ui.TextInputBase(c.Key(key), value).Height(32).Width(w).Padding(0, 12).Radius(8).Background(p.bg).Border(1, p.line).FontSize(13).Label(label).Placeholder(label).Disabled(e.saving)
				switch {
				case bad:
					f.Border(1.5, danger)
				case f.Focused():
					f.Border(1.5, p.primary)
				}
				return f
			}
			name := field("svc-name", &e.name, "名称", 200, false)
			if e.name == "" && e.port == "" && !e.saving {
				name.AutoFocus()
			}
			port := field("svc-port", &e.port, "端口", 84, e.errPort)
			if digits := onlyDigits(e.port, 5); digits != e.port {
				e.port = digits
			}
			empty := strings.TrimSpace(e.port) == ""
			add := primaryButton(c, p, "svc-add", "添加", 32, 16, 13, "", &e.work).Disabled(empty)
			if empty {
				// Not ready: plainly so, as a disabled button is.
				add.Opacity(0.45)
			}
			cancel := textButton(c, p, "svc-cancel", "取消", false)
			if add.Clicked() || name.Submitted() || port.Submitted() {
				a.addService(c, d, in, e)
			}
			if cancel.Clicked() || (name.Focused() || port.Focused()) && c.Shortcut(0, ui.KeyEscape) {
				a.svcEdit = nil
			}
		})
		if e.err != "" {
			ui.Text(c.Key("err-"+e.err), e.err).FontSize(12).TextColor(danger).
				Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: -2}})
		}
	})
}

// addService sets the service up: in the device's first LAN, closed, as
// service.set does the first time it hears of one (open to no one yet).
func (a *app) addService(c *ui.Context, d *model.Device, in []*model.LAN, e *svcEdit) {
	if e.saving || e.work.working(c.Now()) || len(in) == 0 {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(e.port))
	if err != nil || n < 1 || n > 65535 {
		e.err, e.errPort = "端口应为 1–65535 之间的数字", true
		return
	}
	if t := model.PortTaken(d, uint16(n)); t != nil {
		e.err, e.errPort = "端口 "+strconv.Itoa(n)+" 已被「"+t.Name+"」占用", true
		return
	}
	def := model.HandService(e.name, uint16(n))
	op := model.Op{Kind: model.OpServiceSet, LAN: in[0].ID, Device: d.ID, Service: def.Key, Def: &def}
	e.saving, e.err, e.errPort = true, "", false
	e.work.begin(c.Now())
	dev := d.ID
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) }, func(c *ui.Context, _ model.Result, err error) {
		e.saving = false
		e.work.end()
		if a.svcEdit != e {
			return
		}
		if err != nil {
			e.err = message(err)
			e.errPort = strings.Contains(e.err, "端口")
			return
		}
		a.svcEdit = nil
		a.notify(c, nil, "已添加「"+def.Name+"」", func() {
			a.send(c, model.Op{Kind: model.OpServiceRemove, Device: dev, Service: def.Key})
		})
		a.sync(c)
	})
}

// deviceDetailsView is what 详情 opens: the numbers the engine reports, the
// system, the address, and deleting the device (Codex's, kept).
func (a *app) deviceDetailsView(c *ui.Context, p *palette, d *model.Device, now time.Time) {
	sample, has := a.snap.DeviceMetrics[d.ID]
	stale := !d.Online || !a.snap.Control && d.ID != a.snap.Self || has && now.Sub(sample.At) > 45*time.Second
	ui.Column(c.Key("host-details")).Gap(16).Transition(ui.ElementTransition{Animation: &ui.Smooth, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Y: 4}}).Children(func() {
		ui.Row(c).Gap(16).Children(func() {
			cpu, cpuSub, memory, memorySub, disk, diskSub := "—", "暂无数据", "—", "暂无数据", "—", "暂无数据"
			if has && sample.CPU != nil {
				cpu, cpuSub = fmt.Sprintf("%.0f%%", *sample.CPU*100), "使用率"
			}
			if has && sample.Memory != nil {
				memory = hostBytes(sample.Memory.Total - sample.Memory.Available)
				memorySub = "共 " + hostBytes(sample.Memory.Total)
			}
			if has && len(sample.Disks) > 0 {
				disk = hostBytes(sample.Disks[0].Available)
				diskSub = "共 " + hostBytes(sample.Disks[0].Total) + " · 可用"
			}
			deviceMetric(c, p, "CPU", cpu, cpuSub, stale)
			deviceMetric(c, p, "内存", memory, memorySub, stale)
			deviceMetric(c, p, "存储", disk, diskSub, stale)
			rtt, path := a.deviceLatency(d)
			deviceMetric(c, p, "延迟", rtt, path, false)
		})
		ui.Column(c).Gap(12).Children(func() {
			value := sample.OSVersion
			if !has || value == "" {
				value = d.OS
			}
			monitorFact(c, p, "系统", value)
			if has {
				monitorFact(c, p, "处理器", sample.CPUModel)
				monitorFact(c, p, "配置", fmt.Sprintf("%d 核 · %s", sample.CPUs, sample.Arch))
				if sample.Memory != nil {
					monitorFact(c, p, "可用内存", hostBytes(sample.Memory.Available)+" / "+hostBytes(sample.Memory.Total))
				}
				for _, disk := range sample.Disks {
					monitorFact(c, p, disk.Name, hostBytes(disk.Available)+" 可用 / "+hostBytes(disk.Total))
				}
				if sample.Uptime != nil {
					monitorFact(c, p, "已运行", hostUptime(*sample.Uptime))
				}
				if sample.EngineVersion != "" {
					monitorFact(c, p, "引擎", sample.EngineVersion)
				}
				if len(sample.Errors) > 0 {
					ui.Text(c, strings.Join(sample.Errors, " · ")).FontSize(11).TextColor(p.ink3)
				}
			}
			monitorFact(c, p, "Deneb 地址", d.Addr)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "上次采样").Width(88).Shrink(0).FontSize(12).TextColor(p.ink3)
				when := "暂无"
				switch {
				case !has && !d.Online:
					when = "设备不在线"
				case !has && a.snap.MetricsError != "":
					when = "需更新 Deneb 后才能上报"
				case has:
					when = ago(now.Sub(sample.At))
				}
				ui.Text(c, when).FontSize(12).TextColor(p.ink2)
				// Under way, past busyDelay, a ring takes the words' place; not
				// disabled meanwhile, which would fade the ring.
				a.metricsWork.follow(now, a.snap.MetricsLoading)
				r := textButton(c, p, "refresh", "刷新", true, &a.metricsWork).Label("刷新监控数据")
				if r.Clicked() && !a.metricsWork.working(now) {
					if store, ok := a.store.(interface{ RefreshDeviceMetrics() }); ok {
						store.RefreshDeviceMetrics()
					}
				}
			})
		})
		a.deleteRow(c, p, d.ID)
	})
}

func deviceMetric(c *ui.Context, p *palette, label, value, sub string, stale bool) {
	ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.ink3)
		ink := p.ink
		if stale {
			ink = p.ink3
		}
		ui.Text(c, value).FontSize(21).FontWeight(600).TextColor(ink).SingleLine()
		ui.Text(c, sub).FontSize(11).TextColor(p.ink3).SingleLine()
	})
}
func monitorFact(c *ui.Context, p *palette, label, value string) {
	if value == "" {
		value = "—"
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(88).Shrink(0).FontSize(12).TextColor(p.ink3)
		ui.Text(c, value).Grow(1).Shrink(1).FontSize(12).TextColor(p.ink2)
	})
}

// deviceLatency is the round trip and the way, in §2's words.
func (a *app) deviceLatency(d *model.Device) (string, string) {
	if d.ID == a.snap.Self {
		return "—", "这台电脑"
	}
	if !d.Online {
		return "—", "不在线"
	}
	path, ok := a.snap.Paths[d.ID]
	if !ok || path.Kind == model.PathApart {
		return "—", "不在同一局域网"
	}
	if !path.Kind.Connected() {
		return "—", "无法连接"
	}
	way := "直接连上"
	if path.Kind == model.PathRelay {
		way = "经中转"
		if r := a.snap.Relays[path.Relay]; r != nil && r.Name != "" {
			way = "经" + r.Name + "中转"
		}
	}
	if measured, ok := a.snap.DeviceLatencies[d.ID]; ok && time.Since(measured.At) <= 45*time.Second {
		if measured.Failed {
			return "—", "未测到"
		}
		return fmt.Sprintf("%.1f 毫秒", float64(measured.RTT)/float64(time.Millisecond)), way
	}
	if path.RTT <= 0 {
		return "—", way
	}
	return fmt.Sprintf("%.1f 毫秒", float64(path.RTT)/float64(time.Millisecond)), way
}
func hostBytes(n uint64) string {
	for _, u := range []struct {
		n uint64
		s string
	}{{1 << 40, "TB"}, {1 << 30, "GB"}, {1 << 20, "MB"}, {1 << 10, "KB"}} {
		if n >= u.n {
			return fmt.Sprintf("%.1f %s", float64(n)/float64(u.n), u.s)
		}
	}
	return fmt.Sprintf("%d B", n)
}
func hostUptime(s uint64) string {
	if s >= 86400 {
		return fmt.Sprintf("%d 天 %d 小时", s/86400, s%86400/3600)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", s/3600, s%3600/60)
}
