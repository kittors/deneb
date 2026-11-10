package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The relays' cards (DESIGN §6.12): the public one, which says it is always
// there and how fast; my own server, which says how it is doing and lets me
// keep people off it; a friend's, which says what its owner can see; and
// the card of an install command for a server of my own.

const (
	relayCard = "\x00relay:" // a.selected for a relay's card: this, then its id
	joinCard  = "\x00join:"  // a.selected for an install command's: this, then its id
)

// openRelay shows the card of the relay id.
func (a *app) openRelay(id model.RelayID) {
	a.selected, a.more, a.renaming, a.picking = relayCard+string(id), false, nil, false
}

// relayCardView is the card of the relay id.
func (a *app) relayCardView(c *ui.Context, p *palette, id model.RelayID) {
	rl := a.snap.Relays[id]
	if rl == nil || !a.snap.Can(model.FeatureRelays) {
		a.selected = ""
		return
	}
	mine := a.snap.MineRelay(rl)
	a.cardFrame(c, p, "relay-"+string(id), func() {
		a.relayHead(c, p, rl, mine)
		a.relayStatus(c, p, rl, mine)
		switch {
		case rl.Public():
			note(c, p, "自动兜底，不用管。直接连不上，或者局域网自己的中转站用不了时，自动经它中转。", 14)
			note(c, p, "内容全程加密，中转站看不到。", 6)
		case mine:
			a.relayNow(c, p, rl)
			a.relayMonth(c, p, rl)
			a.relayUsers(c, p, rl)
			note(c, p, "内容全程加密，你也看不到；你能看到谁在用、用了多少。", 14)
		default:
			who := a.personName(rl.Owner)
			// (No space after a full stop of its own: "，Mia".)
			note(c, p, "内容全程加密，"+say(who, "看不到；")+say(who, "能看到谁在经它连、用了多少。"), 14)
		}
		a.relayFoot(c, p, rl, mine)
	})
}

// relayHead is the card's top: the relay's ball, its name, which I rename
// in place when it is mine (DESIGN §6.9), and whose it is.
func (a *app) relayHead(c *ui.Context, p *palette, rl *model.Relay, mine bool) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		relayOrb(c, p, 40, rl.Down, 0)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			key := "relay:" + string(rl.ID)
			if r := a.renaming; r != nil && r.id == key {
				a.nameField(c, p, r, 15, func(name string) {
					if name != rl.Label() {
						a.send(c, model.Op{Kind: model.OpRelayRename, Relay: rl.ID, Name: name})
					}
				})
			} else if mine {
				name := ui.ButtonBase(c.Key("name")).FocusRing(false).Cursor(ui.CursorText).AlignSelf(ui.Start).Label("改名字")
				name.Children(func() {
					ui.Text(c.Key("name-"+rl.Label()), rl.Label()).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth).
						Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
				})
				if name.Clicked() {
					a.renaming = &renaming{id: key, name: rl.Label()}
				}
			} else {
				ui.Text(c, rl.Label()).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
			}
			from := a.relayFrom(rl)
			if rl.Public() {
				from = "公共中转站"
			}
			ui.Text(c, from).FontSize(12).TextColor(p.ink2)
		})
		if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
			a.selected, a.more, a.renaming = "", false, nil
		}
	})
}

// relayStatus is the card's line of how the relay is: online, and how near
// or how long up; or down, and since when.
func (a *app) relayStatus(c *ui.Context, p *palette, rl *model.Relay, mine bool) {
	title, sub, col := "在线", "离你很快", p.ok
	switch {
	case rl.Down:
		title, sub, col = "连不上", "刚停，经它的连线先走公共中转站", ui.Hex("#a1a1aa")
		if since := a.now().Sub(rl.LastSeen); !rl.LastSeen.IsZero() && since >= time.Minute {
			sub = say(strings.TrimSuffix(ago(since), "前"), "没报到，经它的连线先走公共中转站")
		}
	case mine && !rl.Since.IsZero() && a.now().Sub(rl.Since) < 10*time.Minute:
		sub = "刚上线"
	case mine:
		sub = "已经运行 " + days(rl.Load.Uptime)
	case rl.RTT == 0:
		// Nothing goes through it now to tell how near it is.
		sub = "随时能用"
	case rl.RTT > 80*time.Millisecond:
		sub = "离你有点慢"
	}
	statusRow(c, p, "", title, sub, col)
}

// days says how long, in days, or hours under one.
func days(d time.Duration) string {
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时", max(1, int(d.Hours())))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

// statusRow is a card's one sentence of how something is, on the well: a
// ball of col with the relay's glyph (or icon's), a title and a line under
// it, which fade in anew as they change.
func statusRow(c *ui.Context, p *palette, icon, title, sub string, col ui.Color) {
	ui.Row(c).Gap(10).Padding(12).Radius(14).Background(p.well).Margin(14, 0, 0, 0).Children(func() {
		badge := ui.Box(c).Size(30, 30).Radius(999).Center().
			RadialGradient(ui.RadialGradient{Center: ui.Point{X: 0.34, Y: 0.28}, Stops: []ui.GradientStop{
				{Color: col.Mix(white, 0.45), At: 0}, {Color: col, At: 0.55}, {Color: col.Mix(black, 0.15), At: 1}}})
		badge.Children(func() {
			if icon == "" {
				ui.Icon(c, deviceGlyphs["relay"]).Size(16, 16).TextColor(white)
			} else {
				ui.Icon(c, uiGlyphs[icon]).Size(16, 16).TextColor(white)
			}
		})
		words := ui.Column(c.Key("how-" + title + sub)).Gap(2).Grow(1)
		words.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
		words.Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(500)
			ui.Text(c, sub).FontSize(12).TextColor(p.ink2)
		})
	})
}

// note is a card's sentence in small grey words, top above it.
func note(c *ui.Context, p *palette, text string, top float32) {
	ui.Text(c, text).FontSize(12).TextColor(p.ink3).Padding(top, 4, 0, 4)
}

// heading is a card's small heading over a section.
func heading(c *ui.Context, p *palette, text string) {
	ui.Text(c, text).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
}

// wellRows are a card's rows of a name and a value on the well, each with
// what leads it.
func wellRows(c *ui.Context, p *palette, rows []wellRow) {
	ui.Column(c).Radius(14).Background(p.well).Padding(0, 12).Dividers(1, p.line).Children(func() {
		for _, r := range rows {
			ui.Row(c.Key("row-" + r.name)).Height(40).Gap(10).AlignItems(ui.Center).Children(func() {
				if r.lead != nil {
					r.lead()
				}
				ui.Text(c, r.name).FontSize(13).FontWeight(500).Grow(1).SingleLine()
				ui.Text(c, r.value).FontSize(13).TextColor(p.ink2).SingleLine()
			})
		}
	})
}

type wellRow struct {
	lead        func()
	name, value string
}

// relayNow is how my relay does now: who is on it, and how busy it is.
func (a *app) relayNow(c *ui.Context, p *palette, rl *model.Relay) {
	heading(c, p, "现在")
	on := "还没有"
	if rl.Load.People > 0 {
		on = fmt.Sprintf("%d 个人 · %d 台设备", rl.Load.People, rl.Load.Devices)
	}
	busy := map[model.Busy]string{model.BusyNo: "不忙", model.BusySome: "有点忙", model.BusyFull: "满了，新连上来的先走别的"}[rl.Load.Busy]
	if rl.Down {
		on, busy = "—", "—"
	}
	wellRows(c, p, []wellRow{
		{lead: func() { ui.Icon(c, uiGlyphs["people"]).Size(16, 16).TextColor(p.ink2) }, name: "在用", value: on},
		{lead: func() { ui.Icon(c, uiGlyphs["bolt"]).Size(16, 16).TextColor(p.ink2) }, name: "忙不忙", value: busy},
	})
}

// relayMonth is what my relay carried this month, for each LAN.
func (a *app) relayMonth(c *ui.Context, p *palette, rl *model.Relay) {
	if len(rl.Month) == 0 {
		return
	}
	heading(c, p, "这个月替你转了")
	var rows []wellRow
	for _, m := range rl.Month {
		name, lead := "分享", func() { ui.Icon(c, uiGlyphs["people"]).Size(16, 16).TextColor(p.ink2) }
		if !m.Shares {
			l := a.snap.LAN(m.LAN)
			if l == nil {
				continue
			}
			name = short(l.Name)
			lan := l
			lead = func() { lanDot(c, a, lan) }
		}
		rows = append(rows, wellRow{lead: lead, name: name, value: bytesWords(m.Bytes)})
	}
	wellRows(c, p, rows)
}

// bytesWords says an amount the way people do: 12.4 GB, 380 MB.
func bytesWords(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%d MB", n/1_000_000)
	}
	return fmt.Sprintf("%d KB", max(1, n/1000))
}

// relayUsers are who is on my relay now, each a chip that keeps them off it
// (grey, a slash across the ball, as "谁能用" does in scene 5), and those I
// kept off in the last 30 days, each a chip that lets them back.
func (a *app) relayUsers(c *ui.Context, p *palette, rl *model.Relay) {
	if a.relayChips == nil || a.relayChips.relay != rl.ID {
		// The chips as the card opened: one kept off stays where it was,
		// grey, while the card is open, to let back at once.
		a.relayChips = &relayChips{relay: rl.ID, users: append([]model.PersonID(nil), rl.Using...)}
	}
	users := a.relayChips.users
	if rl.Down {
		// Stopped, no one is on it.
		users = nil
	}
	if len(users) > 0 {
		heading(c, p, "谁在用")
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, who := range users {
				a.kickChip(c, p, rl, who, "")
			}
		})
		note(c, p, "点一下，就不让他用这个中转站", 8)
	}
	var kept []model.RelayKick
	for _, k := range rl.Kicks {
		if k.Lifted.IsZero() && !containsPerson(users, k.Person) && a.now().Sub(k.At) < 30*24*time.Hour {
			kept = append(kept, k)
		}
	}
	if len(kept) == 0 {
		return
	}
	heading(c, p, "不让用的")
	ui.Row(c).Gap(8).Wrap().Children(func() {
		for _, k := range kept {
			a.kickChip(c, p, rl, k.Person, dayWords(a.now(), k.At))
		}
	})
}

func containsPerson(list []model.PersonID, who model.PersonID) bool {
	for _, p := range list {
		if p == who {
			return true
		}
	}
	return false
}

// dayWords is which day at was: 今天, 昨天, or the date.
func dayWords(now, at time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := at.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "今天"
	case now.Sub(at) < 48*time.Hour && now.AddDate(0, 0, -1).Day() == d2:
		return "昨天"
	}
	return fmt.Sprintf("%d月%d日", m2, d2)
}

// kickChip is who on my relay: a click keeps them off it, or lets them
// back, the toast offering the other way.
func (a *app) kickChip(c *ui.Context, p *palette, rl *model.Relay, who model.PersonID, when string) {
	id := string(who)
	person := a.people[id]
	if person == nil {
		return
	}
	off := rl.Kick(who) != nil
	chip := ui.ButtonBase(c.Key("kick-"+id)).Height(34).Padding(0, 12, 0, 4).Gap(8).Radius(999).Background(p.well).FocusRing(false).
		Cursor(ui.CursorPointer).Label(map[bool]string{false: say("不让", person.name, "用"), true: say("让", person.name, "用")}[off])
	if chip.Hovered() {
		chip.Background(p.well2)
	}
	chip.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
	chip.AnimatePose(pressPose(chip, 0.95), ui.Snappy)
	if chip.Clicked() {
		a.kick(c, rl, who, !off)
		off = !off
	}
	k := chip.AnimateTo("off", map[bool]float32{false: 0, true: 1}[off], ui.Animation{Duration: 200 * time.Millisecond})
	chip.Children(func() {
		ballOff(c, person, 26, k)
		ui.Text(c, person.name).FontSize(13).FontWeight(500).TextColor(p.ink.Mix(p.ink3, k))
		if when != "" {
			ui.Text(c, when).FontSize(12).TextColor(p.ink3)
		}
	})
}

// kick keeps who off my relay, or lets them back (off false), and says so
// with the other way as its undo.
func (a *app) kick(c *ui.Context, rl *model.Relay, who model.PersonID, off bool) {
	kind, undo := model.OpRelayKick, model.OpRelayUnkick
	text := say("已不让", a.personName(who), "用", short(a.relayCall(rl)))
	if !off {
		kind, undo = undo, kind
		text = say("又让", a.personName(who), "用", short(a.relayCall(rl)), "了")
	}
	a.send(c, model.Op{Kind: kind, Relay: rl.ID, Person: who})
	id := rl.ID
	a.notify(c, nil, text, func() {
		a.send(c, model.Op{Kind: undo, Relay: id, Person: who})
	})
	a.toast.orb = true
}

// relayFoot is the card's bottom: "更多", what only someone who asks needs,
// and, on my relay in this LAN, the click for dragging it back to the tray.
func (a *app) relayFoot(c *ui.Context, p *palette, rl *model.Relay, mine bool) {
	ui.Row(c).Margin(12, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		more := ui.ButtonBase(c.Key("more")).Height(28).Padding(0, 4).Gap(2).Radius(8).FocusRing(false)
		if more.Clicked() {
			a.more = !a.more
		}
		turn := float32(0)
		if a.more {
			turn = 90
		}
		more.Children(func() {
			ui.Text(c, "更多").FontSize(13).TextColor(p.ink2)
			ui.Icon(c, uiGlyphs["chevron"]).Size(14, 14).TextColor(p.ink2).Rotate(more.AnimateTo("turn", turn, ui.Snappy))
		})
		ui.Spacer(c)
		l := a.snap.LAN(a.lan)
		if mine && l != nil && l.Added(rl.ID) {
			out := ui.ButtonBase(c.Key("relay-out")).Height(28).Padding(0, 10).Radius(999).FocusRing(false)
			if out.Hovered() {
				out.Background(p.well)
			}
			out.AnimatePose(pressPose(out, 0.96), ui.Snappy)
			out.Children(func() { ui.Text(c, "不用这个中转站").FontSize(12).TextColor(p.ink2) })
			if out.Clicked() {
				a.relayOut(c, rl.ID)
			}
		}
	})
	if !a.more {
		return
	}
	var lines []string
	where := rl.Addr
	if rl.Public() && rl.Name != "" {
		where = rl.Name + " · " + rl.Addr
		lines = append(lines, "现在经 "+where)
	} else if rl.Addr != "" {
		lines = append(lines, "地址 "+rl.Addr)
	}
	if rl.RTT > 0 && !rl.Down {
		lines = append(lines, fmt.Sprintf("延迟 %d 毫秒", rl.RTT.Milliseconds()))
	}
	if rl.RateLimit > 0 {
		lines = append(lines, "每条连接限速 "+rateWords(rl.RateLimit))
	} else {
		lines = append(lines, "不限速")
	}
	detail := ui.Column(c.Key("detail"))
	detail.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
	detail.Children(func() {
		for i, line := range lines {
			top := float32(2)
			if i == 0 {
				top = 8
			}
			ui.Text(c, line).FontSize(12).TextColor(p.ink2).Padding(top, 4, 0, 4).SingleLine()
		}
	})
}

// rateWords says a rate in bytes a second: 2 MB/s.
func rateWords(n int64) string {
	if n >= 1_000_000 {
		v := float64(n) / 1e6
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d MB/s", int64(v))
		}
		return fmt.Sprintf("%.1f MB/s", v)
	}
	return fmt.Sprintf("%d KB/s", max(1, n/1000))
}

// lanDot is a LAN's small mark, its owner's color, as the sidebar's.
func lanDot(c *ui.Context, a *app, l *model.LAN) {
	col := ui.Hex("#a1a1aa")
	if o := a.people[string(l.Owner)]; o != nil {
		col = o.color
	}
	ui.Box(c).Size(10, 10).Radius(999).Background(col)
}

// relayOrb is a relay's ball: white and glossy (§3.6), size across; grey
// off of the way to down; its rim dashed while it waits to be set up.
func relayOrb(c *ui.Context, p *palette, size float32, down bool, off float32) ui.Element {
	if down {
		off = 1
	}
	orb := ui.Box(c).Size(size, size).Radius(999).Center().
		RadialGradient(ui.RadialGradient{Center: ui.Point{X: 0.34, Y: 0.28}, Stops: []ui.GradientStop{
			{Color: p.ballHi, At: 0}, {Color: p.ballMid, At: 0.52}, {Color: p.ballLo, At: 1},
		}}).
		InsetShadow(0, -1.5, 3, 0, ui.RGBA(0, 0, 0, 0.08)).
		Shadow(0, size*0.15, size*0.35, 0, ui.RGBA(9, 9, 11, 0.12)).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.1))
	orb.Opacity(1 - 0.4*off)
	orb.Children(func() {
		ui.Icon(c, deviceGlyphs["relay"]).Size(size*0.45, size*0.45).TextColor(p.ink2.Alpha(1 - 0.5*off))
	})
	return orb
}

// joinCardView is the card of an install command for a server of my own:
// the one just asked for, or one waiting in the tray (DESIGN §6.12, the web
// admin's M3-15 as well).
func (a *app) joinCardView(c *ui.Context, p *palette, id string) {
	if !a.snap.Can(model.FeatureRelays) {
		a.selected = ""
		return
	}
	var join *model.RelayJoin
	for i := range a.snap.RelayJoins {
		if a.snap.RelayJoins[i].ID == id {
			join = &a.snap.RelayJoins[i]
		}
	}
	command := a.commands[id]
	if join == nil && command == "" {
		// Run, or taken back: the card goes with it, unless what it set up
		// takes its place (relayArrived).
		a.selected = ""
		return
	}
	waiting := a.joinAsked != id
	a.cardFrame(c, p, "join-"+id, func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			dashedOrb(c, p, 40)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "用我自己的服务器").FontSize(15).FontWeight(600).SingleLine()
				ui.Text(c, "装好后，它自己出现在托盘里").FontSize(12).TextColor(p.ink2).SingleLine()
			})
			if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
				a.selected = ""
			}
		})
		heading(c, p, "在服务器上运行这一行")
		if command == "" {
			note(c, p, "命令只显示一次。还没装的话，收回它，再要一行。", 0)
		} else {
			// Broken at its spaces only, never inside "--restart": it is
			// copied whole with the button, so the hand need not select a
			// part of it, and leave a piece behind.
			code := ui.Column(c).Radius(12).Background(p.well).Padding(10, 12)
			code.Children(func() {
				ui.Text(c, wrapAt(command, 34)).Font("SF Mono, Menlo, monospace").FontSize(11.5).LineHeight(1.45).TextColor(p.ink)
			})
		}
		ui.Row(c).Gap(10).Margin(12, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			left := "1 小时内有效，只能用一次，要先装好 Docker。"
			if join != nil && waiting {
				left = say("还有", minutes(join.Expires.Sub(a.now())), "有效，只能用一次。")
			}
			ui.Text(c, left).FontSize(12).TextColor(p.ink3).Grow(1).Shrink(1)
			if command != "" {
				copied := a.copiedAt[id]
				label := "复制"
				if !copied.IsZero() {
					label = "✓ 已复制"
				}
				if primaryButton(c, p, "copy", label, 30, 12, 12).Shrink(0).Clicked() {
					if a.copyText != nil {
						a.copyText(command)
					}
					a.copiedAt[id] = a.now()
				}
			}
		})
		if waiting && join != nil {
			ui.Row(c).Margin(10, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				out := ui.ButtonBase(c.Key("revoke")).Height(28).Padding(0, 10).Radius(999).FocusRing(false)
				if out.Hovered() {
					out.Background(p.well)
				}
				out.AnimatePose(pressPose(out, 0.96), ui.Snappy)
				out.Children(func() { ui.Text(c, "收回这行命令").FontSize(12).TextColor(p.ink2) })
				if out.Clicked() {
					a.revokeJoin(c, id)
				}
			})
			return
		}
		note(c, p, "不装也能用：直接连不上时，自动经公共中转站。", 12)
	})
}

// wrapAt breaks s at its spaces into lines of at most n characters (a
// word longer than that keeps a line to itself).
func wrapAt(s string, n int) string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) > n:
			lines, cur = append(lines, cur), w
		default:
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// minutes says how long is left, in minutes.
func minutes(d time.Duration) string {
	return fmt.Sprintf("%d 分钟", max(1, int(d.Minutes()+0.5)))
}

// dashedOrb is a relay that is not there yet: its ball's place, a dashed
// ring with the relay's glyph faint inside.
func dashedOrb(c *ui.Context, p *palette, size float32) ui.Element {
	return dashedRing(c, p, size, p.ink3.Alpha(0.6), func() {
		ui.Icon(c, deviceGlyphs["relay"]).Size(size*0.45, size*0.45).TextColor(p.ink3)
	})
}
