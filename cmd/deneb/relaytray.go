package main

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// My relays in the tray, and the way in and out of it (DESIGN §6.12): the
// ones not in this LAN wait in the tray after my devices, a thin line
// between; dragged out onto the canvas, or clicked, the LAN uses one;
// dragged back, or "不用这个中转站", it does not. Last in the tray are the
// install commands that wait, breathing, and the "+" that asks for one.

const (
	relaySlotBall = 32 // a relay's ball in the tray
	relayBall     = 40 // and on the canvas
	relayFly      = 420 * time.Millisecond
)

// relayChips are the "谁在用" chips of a relay's card as it opened.
type relayChips struct {
	relay model.RelayID
	users []model.PersonID
}

// relayFlight is a relay on its way between the tray and the canvas: from
// where it was, to where it goes, since when. into: to the tray, where it
// goes out of sight as it arrives.
type relayFlight struct {
	from, to ui.Point
	at       time.Time
	into     bool
	held     bool      // it waited for the line through it to fade
	made     time.Time // when it set off, or was asked to
}

// relayTray reports whether the tray holds relays here: an M4 server's
// LAN, where the "+" always is.
func (a *app) relayTray() bool {
	if !a.snap.Can(model.FeatureRelays) || a.all() || a.shared() {
		return false
	}
	l := a.snap.LAN(a.lan)
	return l != nil && !l.Waiting
}

// trayRelays are my relays not in this LAN, by ID: the tray's.
func (a *app) trayRelays() []*model.Relay {
	l := a.snap.LAN(a.lan)
	if l == nil {
		return nil
	}
	var out []*model.Relay
	for _, rl := range a.snap.MyRelays() {
		if !l.Added(rl.ID) {
			out = append(out, rl)
		}
	}
	return out
}

// relayRemovable reports whether the hand can take r out of this LAN:
// mine, dragged in.
func (a *app) relayRemovable(r *relayStop) bool {
	rl, l := a.snap.Relays[r.id], a.snap.LAN(a.lan)
	return rl != nil && l != nil && a.snap.MineRelay(rl) && l.Added(r.id)
}

// relayRefusal is what the tray says of a relay it cannot take.
func (a *app) relayRefusal(r *relayStop) string {
	rl := a.snap.Relays[r.id]
	if rl == nil || rl.Public() || rl.Source == "" {
		return "公共中转站一直都在，拿不掉"
	}
	who := a.personName(rl.Owner)
	return say("这是", who, "的中转站，只有", who, "能拿走")
}

// trayRelayItems are the tray's relays, its install commands and its "+".
func (a *app) trayRelayItems(c *ui.Context, p *palette, w, h float32) {
	if !a.relayTray() {
		return
	}
	a.trayHintNext = ""
	defer func() {
		// What the pointer is on, for the tray's words in the next frame.
		if a.trayHintNext != a.trayHint {
			a.trayHint = a.trayHintNext
			c.AnimationFrame()
		}
	}()
	// The thin line between my devices and my relays.
	if len(a.tray) > 0 {
		ui.Box(c.Key("relay-rule")).Size(1, 26).Margin(0, 0, 0, slotGap+2).Background(p.line).Shrink(0)
	}
	for _, rl := range a.trayRelays() {
		a.trayRelayItem(c, p, rl, w, h)
	}
	for _, j := range a.snap.RelayJoins {
		a.trayJoinItem(c, p, j)
	}
	plus := ui.ButtonBase(c.Key("relay-plus")).Size(relaySlotBall+slotGap, slotTile).Padding(0, 0, 0, slotGap).Center().
		FocusRing(false).Cursor(ui.CursorPointer).Label("用我自己的服务器").Shrink(0)
	plus.Transition(ui.ElementTransition{Animation: &soft, Position: true})
	if b := plus.Bounds(); b.W > 0 {
		a.slots["relay:+"] = ui.Point{X: b.X - a.origin.X + slotGap + relaySlotBall/2, Y: b.Y - a.origin.Y + b.H/2}
	}
	plus.AnimatePose(pressPose(plus, 0.92), ui.Snappy)
	if plus.Hovered() {
		a.trayHintNext = "用我自己的服务器当中转站"
	}
	plus.Children(func() {
		ring := p.ink3.Alpha(0.55)
		if plus.Hovered() {
			ring = p.primary.Alpha(0.7)
		}
		dashedRing(c, p, relaySlotBall, ring, func() {
			ui.Icon(c, uiGlyphs["plus"]).Size(14, 14).TextColor(ring)
		})
	})
	if plus.Clicked() {
		a.askJoin(c)
	}
}

// trayRelayItem is a relay of mine in the tray: dragged out over the
// canvas it grows to a canvas relay; let go there, this LAN uses it, where
// it was let go; let go over the tray, it goes back. A click puts it where
// it helps most (relayHome).
func (a *app) trayRelayItem(c *ui.Context, p *palette, rl *model.Relay, w, h float32) {
	key := "relay:" + string(rl.ID)
	slot := ui.Box(c.Key("tray-"+key)).Size(relaySlotBall+slotGap, slotTile).Padding(0, 0, 0, slotGap).Shrink(0)
	fresh := a.newRelays[rl.ID]
	if fresh.IsZero() || a.now().Sub(fresh) > 900*time.Millisecond {
		slot.Transition(ui.ElementTransition{Animation: &soft, Position: true, Enter: &ui.Motion{Collapse: true, Scale: 0.6}})
	} else {
		// Just set up: it opens where its install command closes, and
		// springs in (Bouncy).
		slot.Transition(ui.ElementTransition{Animation: &ui.Bouncy, Position: true, Enter: &ui.Motion{Collapse: true, Scale: 0.4}})
	}
	home, known := a.slots[key]
	if b := slot.Bounds(); b.W > 0 {
		home, known = ui.Point{X: b.X - a.origin.X + slotGap + relaySlotBall/2, Y: b.Y - a.origin.Y + b.H/2}, true
		a.slots[key] = home
	}
	off := a.trayOff[key]
	if off == nil {
		off = &ui.Point{}
		a.trayOff[key] = off
	}
	slot.Children(func() {
		well := ui.Box(c).Size(relaySlotBall, relaySlotBall).Radius(999).Background(p.well).Margin((slotTile-relaySlotBall)/2, 0, 0, 0)
		well.Children(func() {
			item := ui.Box(c.Key("item")).Size(relaySlotBall, relaySlotBall).Label(rl.Label())
			var bounds *ui.Rect
			if known && item.Pressed() {
				bounds = &ui.Rect{X: 70 - home.X, Y: 44 - home.Y, W: max(0, w-140), H: max(0, h-36-44)}
			}
			item.Draggable(off, ui.DraggableOptions{Bounds: bounds, Rigid: true, Animation: ui.Snappy})
			if _, _, ok := item.DragEnded(); ok && known {
				at := ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
				*off = ui.Point{}
				if at.Y <= h-trayBand {
					a.relayIn(c, rl.ID, at, at, false)
					return
				}
			}
			if item.Clicked() && known {
				a.relayIn(c, rl.ID, home, a.relayHome(rl.ID), true)
				return
			}
			item.Cursor(ui.CursorGrab)
			out := item.Dragging() && home.Y+off.Y <= h-trayBand
			if item.Dragging() {
				item.Cursor(ui.CursorGrabbing)
				if out {
					// Out over the canvas: what is there makes room for it.
					a.heldRelay, a.heldRelayAt = rl.ID, ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
				}
			}
			if item.Hovered() && !item.Dragging() {
				if !fresh.IsZero() && a.now().Sub(fresh) < 5*time.Second {
					a.trayHintNext = say(a.relayCall(rl), "刚上线，拖进来就能用")
				} else {
					a.trayHintNext = a.relayCall(rl) + "，拖进来就用它中转"
				}
			}
			pose := ui.Pose{}
			switch {
			case out:
				pose = ui.Pose{Scale: float32(relayBall) / relaySlotBall * 1.04}
			case item.Dragging():
				pose = ui.Pose{Scale: 1.08, Y: -2}
			case item.Pressed():
				pose = ui.Pose{Scale: 0.92}
			case item.Hovered():
				pose = ui.Pose{Y: -2}
			}
			if st := a.stopOf(rl.ID); st != nil && st.flight != nil && st.flight.into {
				// On its way here from the canvas: the slot waits for it.
				return
			}
			item.Children(func() {
				relayOrb(c, p, relaySlotBall, rl.Down, 0).AnimatePose(pose, ui.Snappy)
			})
		})
	})
}

// trayJoinItem is an install command waiting to be run: a dashed relay
// that breathes. A click shows its card, the command again and the way to
// take it back.
func (a *app) trayJoinItem(c *ui.Context, p *palette, j model.RelayJoin) {
	key := "join:" + j.ID
	b := ui.ButtonBase(c.Key("tray-"+key)).Size(relaySlotBall+slotGap, slotTile).Padding(0, 0, 0, slotGap).Center().
		FocusRing(false).Cursor(ui.CursorPointer).Label("等待安装").Shrink(0)
	b.Transition(ui.ElementTransition{Animation: &soft, Position: true,
		Enter: &ui.Motion{Collapse: true, Scale: 0.6}, Exit: &ui.Motion{Collapse: true, Scale: 0.6}})
	b.AnimatePose(pressPose(b, 0.92), ui.Snappy)
	if bb := b.Bounds(); bb.W > 0 {
		a.slots[key] = ui.Point{X: bb.X - a.origin.X + slotGap + relaySlotBall/2, Y: bb.Y - a.origin.Y + bb.H/2}
	}
	if b.Hovered() {
		a.trayHintNext = "等待安装 · 命令还没跑"
	}
	// It breathes: 1 to 0.45 and back, every 2.4 s.
	t := float64(a.now().Sub(a.start)) / float64(2400*time.Millisecond)
	k := float32(0.5 - 0.5*cosTurn(t))
	c.AnimationFrame()
	b.Children(func() {
		dashedRing(c, p, relaySlotBall, p.primary.Alpha(0.75), func() {
			ui.Icon(c, deviceGlyphs["relay"]).Size(15, 15).TextColor(p.primary.Alpha(0.8))
		}).Opacity(1 - 0.55*k)
	})
	if b.Clicked() {
		a.selected, a.more = joinCard+j.ID, false
		a.joinAsked = ""
	}
}

// dashedRing is a dashed circle size across, in col, with inside in it.
func dashedRing(c *ui.Context, p *palette, size float32, col ui.Color, inside func()) ui.Element {
	b := ui.Box(c).Size(size, size).Center()
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-0.75)
		pt.FillPath(&path, p.bg)
		path.Dash(0, 3, 2.5)
		pt.StrokePath(&path, 1.5, col)
	})
	b.Children(inside)
	return b
}

// askJoin asks the server for an install command and shows it in its card;
// the tray has a breathing "等待安装" for it as the view has the join.
func (a *app) askJoin(c *ui.Context) {
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, model.Op{Kind: model.OpRelayJoin})
	}, func(c *ui.Context, r model.Result, err error) {
		if err != nil {
			a.problem(c, err)
			return
		}
		a.commands[r.Join] = r.Command
		a.selected, a.more, a.joinAsked = joinCard+r.Join, false, r.Join
	})
}

// revokeJoin takes back an install command that was not run: its slot
// shrinks away now, and the server hears of it once the toast goes, its
// undo the command as it was.
func (a *app) revokeJoin(c *ui.Context, id string) {
	a.askConfirm("收回安装命令", "中转站安装命令", "收回后此命令将失效。", func(c *ui.Context) { a.revokeJoinNow(c, id) })
}
func (a *app) revokeJoinNow(c *ui.Context, id string) {
	a.selected = ""
	a.holdFor(c, model.Op{Kind: model.OpRelayRevokeJoin, Join: id}, "已收回这行命令")
	a.toast.orb = true
}

// relayIn makes this LAN use the relay id, at spot: it comes from from,
// the hand's place (let go there) or the tray's slot (a click, flying).
func (a *app) relayIn(c *ui.Context, id model.RelayID, from, spot ui.Point, fly bool) {
	a.relayInSay(c, id, from, spot, fly, true)
}

// relayInSay is relayIn, saying so with a toast, or not: undo says nothing.
func (a *app) relayInSay(c *ui.Context, id model.RelayID, from, spot ui.Point, fly, toast bool) {
	rl := a.snap.Relays[id]
	l := a.snap.LAN(a.lan)
	if rl == nil || l == nil {
		return
	}
	a.send(c, model.Op{Kind: model.OpRelayAdd, LAN: a.lan, Relay: id})
	r := a.stopOf(id)
	if r == nil {
		a.syncStops()
		r = a.stopOf(id)
	}
	if r == nil {
		return
	}
	stand := &relayStop{side: "below"}
	at := a.freeSpot(spot, r, stand.footprint, a.cw, a.ch)
	r.base, r.off, r.unplaced, r.checked, r.byHand = at, ui.Point{}, false, true, true
	r.shown, r.leftAt, r.popFirst, r.poppedAt = true, time.Time{}, false, time.Time{}
	r.flight = &relayFlight{from: from, to: at, at: c.Now(), made: c.Now()}
	if !fly {
		// Let go there: it is where the hand left it, at once.
		r.flight = nil
	}
	a.places.set(a.lan, r.key(), at)
	a.places.set(a.lan, r.handKey(), ui.Point{X: 1, Y: 1})
	c.AnimationFrame()
	if !toast {
		return
	}
	name, lan := short(a.relayCall(rl)), a.lan
	a.notify(c, nil, say(short(l.Name), "用上了", name), func() {
		a.relayOutOf(c, lan, id, false)
	})
	a.toast.orb = true
	c.AnimationFrame()
}

// relayOut takes the relay id out of this LAN, as the card's "不用这个中转
// 站" does, or the hand dropping it on the tray: it flies into the tray.
func (a *app) relayOut(c *ui.Context, id model.RelayID) { a.relayOutOf(c, a.lan, id, true) }

func (a *app) relayOutOf(c *ui.Context, lan model.LANID, id model.RelayID, toast bool) {
	rl, l := a.snap.Relays[id], a.snap.LAN(lan)
	if rl == nil || l == nil {
		return
	}
	a.askConfirm("移除中转站", rl.Label(), "从“"+l.Name+"”移除，连接将改用其他路径。", func(c *ui.Context) { a.relayOutOfNow(c, lan, id, toast) })
}
func (a *app) relayOutOfNow(c *ui.Context, lan model.LANID, id model.RelayID, toast bool) {
	rl, l := a.snap.Relays[id], a.snap.LAN(lan)
	if rl == nil || l == nil || !l.Added(id) {
		return
	}
	if r := a.stopOf(id); r != nil && lan == a.lan {
		to := a.relaySlotNext()
		from := r.live
		if from == (ui.Point{}) {
			from = r.at()
		}
		r.flight = &relayFlight{from: from, to: to, at: c.Now(), into: true, made: c.Now()}
	}
	if a.selected == relayCard+string(id) {
		a.selected = ""
	}
	a.send(c, model.Op{Kind: model.OpRelayRemove, LAN: lan, Relay: id})
	if !toast {
		return
	}
	name := short(a.relayCall(rl))
	a.notify(c, nil, say(short(l.Name), "不用", name, "了"), func() {
		if r := a.stopOf(id); r != nil {
			r.flight = nil
		}
		home := a.relayHome(id)
		if r := a.stopOf(id); r != nil && r.base != (ui.Point{}) {
			home = r.base
		}
		from := a.relaySlotNext()
		if s, ok := a.slots["relay:"+string(id)]; ok {
			from = s
		}
		a.relayInSay(c, id, from, home, true, false)
	})
	a.toast.orb = true
}

// relaySlotNext is about where a relay that goes back into the tray will
// have its slot: where the first install command waiting is, or the "+",
// which make room for it.
func (a *app) relaySlotNext() ui.Point {
	for _, j := range a.snap.RelayJoins {
		if at, ok := a.slots["join:"+j.ID]; ok {
			return at
		}
	}
	if at, ok := a.slots["relay:+"]; ok {
		return at
	}
	z := a.trayZone(a.cw, a.ch)
	return ui.Point{X: z.X + z.W - 30, Y: z.Y + z.H/2}
}

// relayHome is where a relay of mine goes by itself as this LAN takes it
// (DESIGN §6.12): under the middle of a link that connects nowhere; else
// beside the public relay its links go through now; else under this
// computer.
func (a *app) relayHome(id model.RelayID) ui.Point {
	me := a.device(a.self)
	if me == nil {
		return ui.Point{X: a.cw / 2, Y: a.ch / 2}
	}
	for _, d := range a.devices {
		if d.path == "unreachable" && d.placed() {
			p, q := me.at(), d.at()
			return ui.Point{X: (p.X + q.X) / 2, Y: (p.Y+q.Y)/2 + 30}
		}
	}
	for _, r := range a.relays {
		if rl := a.snap.Relays[r.id]; rl != nil && rl.Public() && a.used(r) {
			return ui.Point{X: r.at().X + 90, Y: r.at().Y}
		}
	}
	return ui.Point{X: me.at().X, Y: me.at().Y + 150}
}

// relayLook is where a relay in flight is now, and how large: from the tray
// it grows to a canvas ball as it comes; into it, it shrinks and fades as
// it arrives. done: the flight is over.
func (f *relayFlight) look(now time.Time) (at ui.Point, scale, fade float32, done bool) {
	k := float32(now.Sub(f.at)) / float32(relayFly)
	if k < 0 {
		return f.from, 1, 0, false
	}
	if k >= 1 {
		return f.to, 1, 0, true
	}
	e := ui.EaseInOut(max(0, k))
	at = ui.Point{X: f.from.X + (f.to.X-f.from.X)*e, Y: f.from.Y + (f.to.Y-f.from.Y)*e}
	small := float32(relaySlotBall) / relayBall
	if f.into {
		return at, 1 + (small-1)*e, max(0, (e-0.7)/0.3), false
	}
	return at, small + (1-small)*e, 0, false
}

// relaysCame notes my relays that came up while the window is open: each
// pops into the tray, which says so for a few seconds, and takes the place
// of the card of the install command that set it up, if that is open.
func (a *app) relaysCame() {
	mine := a.snap.MyRelays()
	if a.knownRelays == nil {
		a.knownRelays = map[model.RelayID]bool{}
		for _, rl := range mine {
			a.knownRelays[rl.ID] = true
		}
		return
	}
	for _, rl := range mine {
		if a.knownRelays[rl.ID] {
			continue
		}
		a.knownRelays[rl.ID] = true
		if rl.Since.IsZero() || a.now().Sub(rl.Since) > time.Minute {
			// One that came up long ago, new to the window only.
			continue
		}
		a.newRelays[rl.ID] = a.now()
		a.cameHint, a.cameUntil = say(a.relayCall(rl), "刚上线，拖进来就能用"), a.now().Add(4*time.Second)
		if id, ok := strings.CutPrefix(a.selected, joinCard); ok {
			if !slices.ContainsFunc(a.snap.RelayJoins, func(j model.RelayJoin) bool { return j.ID == id }) {
				a.openRelay(rl.ID)
			}
		}
	}
}

// cosTurn is cos(2πt).
func cosTurn(t float64) float64 { return math.Cos(2 * math.Pi * t) }
