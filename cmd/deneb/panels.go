package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// ---- Joining and leaving -------------------------------------------------------------

// notify shows a toast about d (or nothing in particular); undo, when
// given, is its button. It shows above the tray, or at the top of the
// canvas when it would cover the device it is about there (§6.3).
func (a *app) notify(c *ui.Context, d *device, text string, undo func()) {
	a.toasts++
	n := &notice{id: a.toasts, text: text, dev: d, undo: undo, until: c.Now().Add(4 * time.Second)}
	if d != nil && slices.Contains(a.devices, d) && a.landing != d {
		right := float32(0)
		if a.selected != "" {
			right = cardWidth + 20
		}
		tw := toastWidth(text, undo != nil)
		box := ui.Rect{X: (a.cw-right)/2 - tw/2, Y: a.ch - toastBottom(true) - 44, W: tw, H: 44}
		n.top = overlaps(box, footprint(d.rest))
	}
	a.toast = n
}

// toastWidth is about how wide a toast with text is: enough to tell
// whether it would cover a device.
func toastWidth(text string, undo bool) float32 {
	w := 8 + 28 + 10 + 13*float32(utf8.RuneCountInString(text)) + 16
	if undo {
		w += 10 + 56 - 10
	}
	return w
}

// toastBottom is how far above the canvas's bottom edge the toast sits:
// above the tray when it shows.
func toastBottom(trayShows bool) float32 {
	if trayShows {
		return 22 + 56 + 12
	}
	return 22
}

// startLeave sends my device d into the tray: it flies into the slot the
// tray opens for it, its track drawn back in, and leaves the canvas as it
// lands (landed). byHand tells a drop over the tray from a button's doing.
// It sends nothing: takeOut sends the op, and reconcile calls it for what
// happened elsewhere.
func (a *app) startLeave(c *ui.Context, d *device, toast, byHand bool) {
	if l := a.landing; l != nil && l != d {
		a.landed(c, l)
	}
	a.landing, a.landingByHand = d, byHand
	d.leaving, d.aimed, d.flying = c.Now(), false, false
	if a.selected == d.id {
		a.selected, a.more = "", false
	}
	if toast {
		a.notify(c, d, "已把"+short(d.name)+"移出局域网", func() { a.undoLeave(c, d) })
	}
	c.AnimationFrame()
}

// landed finishes a device's flight into the tray: it leaves the LAN and
// takes the slot the tray opened for it, where it already shows.
func (a *app) landed(c *ui.Context, d *device) {
	if a.landing == d {
		a.landing = nil
		if a.slotOK {
			a.slots[d.id] = a.slot
		}
	}
	a.devices = slices.DeleteFunc(a.devices, func(x *device) bool { return x == d })
	d.leaving, d.aimed, d.flying, d.arriving = time.Time{}, false, false, false
	a.tray = append(a.tray, d)
	a.trayOff[d.id] = &ui.Point{}
	delete(a.beams, d.id)
	c.AnimationFrame()
}

// takeOut takes my device d out of the LAN: it flies into the tray as the
// op goes out (startLeave).
func (a *app) takeOut(c *ui.Context, d *device, toast, byHand bool) {
	a.startLeave(c, d, toast, byHand)
	a.send(c, model.Op{Kind: model.OpLANRemoveDevice, LAN: a.lan, Device: model.DeviceID(d.id)})
}

// bringIn puts my device d from the tray into the LAN at spot as the op goes
// out (joinFrom).
func (a *app) bringIn(c *ui.Context, d *device, from, spot ui.Point, pose ui.Pose) {
	a.joinFrom(c, d, from, spot, pose, true)
	a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: a.lan, Device: model.DeviceID(d.id)})
}

// undoLeave puts a device that went into the tray back where it rested: the
// undo of taking it out is bringing it in again (§2.6.2).
func (a *app) undoLeave(c *ui.Context, d *device) {
	if a.landing == d {
		// Still on its way: it turns back.
		a.turnBack(d)
		a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: a.lan, Device: model.DeviceID(d.id)})
		return
	}
	if !slices.Contains(a.tray, d) {
		return
	}
	from, ok := a.slots[d.id]
	if !ok {
		from = ui.Point{X: a.cw / 2, Y: a.ch - 50}
	}
	a.joinFrom(c, d, from, a.freeSpot(d.rest, d, footprint, a.cw, a.ch), ui.Pose{Scale: float32(slotTile) / tileSize}, false)
	a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: a.lan, Device: model.DeviceID(d.id)})
}

// joinFrom puts my device d from the tray onto the canvas at spot. It shows
// first at from, as it looked there (pose), and glides to spot, its track
// drawn out from both ends as it goes. It sends nothing: bringIn and
// undoLeave send the op, and reconcile calls it for what happened elsewhere.
func (a *app) joinFrom(c *ui.Context, d *device, from, spot ui.Point, pose ui.Pose, announce bool) {
	a.tray = slices.DeleteFunc(a.tray, func(x *device) bool { return x == d })
	delete(a.slots, d.id)
	d.base, d.rest, d.live = spot, spot, from
	d.off = ui.Point{X: from.X - spot.X, Y: from.Y - spot.Y}
	d.joined, d.leaving = c.Now(), time.Time{}
	d.arriving, d.arrive, d.flying, d.aimed = true, pose, true, false
	d.dragging, d.way, d.push = false, ui.Point{}, ui.Point{}
	delete(a.beams, d.id)
	a.devices = append(a.devices, d)
	if announce {
		a.notify(c, d, "已把"+short(d.name)+"加进局域网", func() { a.takeOut(c, d, false, false) })
	} else if a.toast != nil && a.toast.dev == d {
		a.toast = nil
	}
	c.AnimationFrame()
}

// ---- Tray ------------------------------------------------------------------------------

// trayMode is what the tray says, which follows what the hand holds
// (§6.1): the tray is where my devices out of the LAN wait, and where one
// of mine goes to leave it.
type trayMode int

const (
	trayIdle   trayMode = iota // its devices can be dragged out, into the LAN
	trayOffer                  // one of mine is dragged near: it can go in here
	trayArmed                  // it is over the tray, or on its way in
	trayRefuse                 // someone else's, or this computer, is dragged near: it cannot
)

func (a *app) trayMode() (trayMode, string) {
	dr := a.drag
	switch {
	case a.landing != nil && a.landingByHand, dr.d != nil && dr.removable && dr.over:
		return trayArmed, "松手，移出局域网"
	case dr.d != nil && dr.removable && dr.near:
		return trayOffer, "拖到这里，移出局域网"
	case dr.d != nil && dr.near && dr.d.path == "self":
		return trayRefuse, "正在用的这台电脑不能移出"
	case dr.d != nil && dr.near:
		who := spaced(a.people[dr.d.owner].name)
		return trayRefuse, "这是" + who + "的设备，只有" + who + "能移出"
	}
	return trayIdle, "拖出来，加进局域网"
}

// trayShows reports whether the tray is up: it holds devices, one is on
// its way into it, or the hand brings one near.
func (a *app) trayShows() bool {
	mode, _ := a.trayMode()
	return len(a.tray) > 0 || a.landing != nil || mode != trayIdle
}

// trayView is the pill at the bottom of the canvas: my devices that are
// not in the LAN, to drag out into it, and the place one of mine is
// dragged to, to leave it. It opens a slot for one coming in, and the
// pill, its words and its slots make room in step.
func (a *app) trayView(c *ui.Context, p *palette, w, h float32, now time.Time) {
	mode, text := a.trayMode()
	show := a.trayShows()
	// Coming up, it starts lower and clear; going away, it sinks and
	// fades, and is gone once it has.
	entering := show && !a.trayUp
	switch {
	case show:
		a.trayUp, a.trayOut = true, time.Time{}
	case a.trayUp && a.trayOut.IsZero():
		a.trayOut = now.Add(300 * time.Millisecond)
		c.After(300 * time.Millisecond)
	case a.trayUp && !now.Before(a.trayOut):
		a.trayUp, a.trayOut = false, time.Time{}
	}
	if entering {
		c.AnimationFrame()
	}
	a.slotOK = false
	a.held = nil
	right := float32(0)
	if a.selected != "" {
		right = cardWidth + 20
	}
	holder := ui.Row(c.Key("tray-holder")).Absolute().Left(0).Right(right).Bottom(22).Justify(ui.Center).PassThrough()
	holder.Children(func() {
		if !a.trayUp {
			return
		}
		ring := p.bg
		if p.dark {
			ring = p.line
		}
		bg, ink := p.bg, p.ink2
		switch mode {
		case trayOffer:
			ring, ink = p.primary.Alpha(0.4), p.primary
		case trayArmed:
			bg, ring, ink = p.bg.Mix(p.primary, 0.07), p.primary, p.primary
		}
		pill := ui.Row(c.Key("tray")).Height(56).Padding(0, 10, 0, 18).AlignItems(ui.Center).Radius(999).
			Background(bg).Border(1.5, ring).
			Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05))
		pill.Transition(ui.ElementTransition{Animation: &soft})
		if b := pill.Bounds(); b.W > 0 {
			a.trayAt = ui.Rect{X: b.X - a.origin.X, Y: b.Y - a.origin.Y, W: b.W, H: b.H}
		}
		// With AnimateTo, not AnimatePose: the view builds in every frame
		// the tray rises or sinks in, so that where its slots are (which
		// drags and flights go by) is where they show.
		lower, clear := float32(0), float32(1)
		if entering || !show {
			lower, clear = 24, 0
		}
		pill.Translate(0, pill.AnimateTo("lower", lower, soft)).Opacity(pill.AnimateTo("clear", clear, soft))
		pill.Children(func() {
			label := ui.Text(c.Key("label-"+text), text).FontSize(13).TextColor(ink).SingleLine().Shrink(0)
			if mode != trayIdle && mode != trayRefuse {
				label.FontWeight(500)
			}
			label.Transition(ui.ElementTransition{Animation: &soft, Position: true, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
			for _, d := range slices.Clone(a.tray) {
				a.trayItem(c, p, d, w, h)
			}
			if mode == trayArmed || a.landing != nil {
				// The slot for the one coming in, opening as the others
				// make room.
				s := ui.Box(c.Key("slot")).Size(slotTile+slotGap, slotTile).Padding(0, 0, 0, slotGap).Shrink(0)
				s.Transition(ui.ElementTransition{Animation: &soft, Position: true, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}})
				if b := s.Bounds(); b.W > 0 {
					a.slot = ui.Point{X: b.X - a.origin.X + slotGap + slotTile/2, Y: b.Y - a.origin.Y + b.H/2}
					a.slotOK = true
				}
				s.Children(func() {
					ui.Box(c).Size(slotTile, slotTile).Radius(slotTile*0.3).Background(p.primary.Alpha(0.08)).Border(1.5, p.primary.Alpha(0.3))
				})
			}
		})
	})
}

// trayItem is one of my devices in the tray. Dragged out of it, it grows
// to a tile of the canvas; let go on the canvas, it joins the LAN there (or
// at the nearest free spot); let go over the tray, it goes back. A click
// adds it too, at a free spot right above.
func (a *app) trayItem(c *ui.Context, p *palette, d *device, w, h float32) {
	slot := ui.Box(c.Key("tray-"+d.id)).Size(slotTile+slotGap, slotTile).Padding(0, 0, 0, slotGap).Shrink(0)
	slot.Transition(ui.ElementTransition{Animation: &soft, Position: true})
	// Where the slot's tile sits in the canvas: the slot itself never
	// moves with the drag, so this is home however far the tile is.
	home, known := a.slots[d.id]
	if b := slot.Bounds(); b.W > 0 {
		home, known = ui.Point{X: b.X - a.origin.X + slotGap + slotTile/2, Y: b.Y - a.origin.Y + b.H/2}, true
		a.slots[d.id] = home
	}
	off := a.trayOff[d.id]
	slot.Children(func() {
		// The slot shows faintly while its device is out of it (§6.1): the
		// well the tile sits in, which the tile covers at home.
		ui.Box(c).Size(slotTile, slotTile).Radius(slotTile * 0.3).Background(p.well).Children(func() {
			a.trayTile(c, p, d, off, home, known, w, h)
		})
	})
}

// trayTile is the tile of a tray item, the part the hand drags.
func (a *app) trayTile(c *ui.Context, p *palette, d *device, off *ui.Point, home ui.Point, known bool, w, h float32) {
	item := ui.Box(c.Key("item")).Size(slotTile, slotTile).Label(d.name)
	// Held inside the canvas while the hand has it, and only then: at rest
	// it is wherever the tray is, rising or sinking.
	var bounds *ui.Rect
	if known && item.Pressed() {
		bounds = &ui.Rect{X: 70 - home.X, Y: 44 - home.Y, W: max(0, w-140), H: max(0, h-36-44)}
	}
	item.Draggable(off, ui.DraggableOptions{Bounds: bounds, Rigid: true, Animation: ui.Snappy})
	if _, _, ok := item.DragEnded(); ok && known {
		at := ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
		if at.Y > h-trayBand {
			*off = ui.Point{} // let go over the tray: it goes back to its place
		} else {
			from := at
			*off = ui.Point{}
			a.bringIn(c, d, from, a.freeSpot(from, d, footprint, w, h), liftPose)
			return
		}
	}
	if item.Clicked() && known {
		from := home
		ar := a.restArea(w, h)
		spot := a.freeSpot(ui.Point{X: from.X, Y: ar.Y + ar.H}, d, footprint, w, h)
		a.bringIn(c, d, from, spot, ui.Pose{Scale: float32(slotTile) / tileSize})
		return
	}
	item.Cursor(ui.CursorGrab)
	if item.Dragging() {
		item.Cursor(ui.CursorGrabbing)
		if home.Y+off.Y <= h-trayBand {
			// Out over the canvas: the devices there make room for it.
			a.held, a.heldAt = d, ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
		}
	}
	// Out of the tray it is a tile of the canvas in the hand, as large and
	// as turned as one, so that it lands as one.
	pose := ui.Pose{}
	switch {
	case item.Dragging() && home.Y+off.Y <= h-trayBand:
		pose = ui.Pose{Scale: float32(tileSize) / slotTile * liftPose.Scale, Rotate: liftPose.Rotate, Y: liftPose.Y}
	case item.Dragging():
		pose = ui.Pose{Scale: 1.06, Y: -2}
	case item.Pressed():
		pose = ui.Pose{Scale: 0.94}
	case item.Hovered():
		pose = ui.Pose{Y: -2}
	}
	item.Children(func() {
		deviceTile(c, p, d.kind, a.tileColor(d), slotTile, false).AnimatePose(pose, ui.Snappy)
	})
}

// ---- Toast ------------------------------------------------------------------------------

// toastView shows what just happened, with its undo: a light pill, as the
// tray and the card are, with the tile of the device it is about. It rises
// in, and sinks and fades out; a new one takes the place of the one
// showing, its words fading across.
func (a *app) toastView(c *ui.Context, p *palette, w, h float32, now time.Time) {
	tn := a.toast
	if tn != nil && !now.Before(tn.until) {
		a.toast, tn = nil, nil
	}
	right := float32(0)
	if a.selected != "" {
		right = cardWidth + 20
	}
	// Both places are built in every frame, so that a toast comes in and
	// goes out where it shows: Enter and Exit play in a parent that stays.
	above := a.trayShows() || a.trayUp
	for _, top := range []bool{false, true} {
		holder := ui.Row(c.Key(fmt.Sprint("toast-holder-", top))).Absolute().Left(0).Right(right).Justify(ui.Center).PassThrough()
		if top {
			holder.Top(12)
		} else {
			holder.Bottom(toastBottom(above)).Transition(ui.ElementTransition{Animation: &soft, Position: true})
			if above {
				// Over the tray, the place keeps the toast's height: one
				// coming in does not grow it up from the tray's edge, which
				// would bring the toast up through the tray.
				holder.Height(44)
			}
		}
		holder.Children(func() {
			if tn == nil || tn.top != top {
				return
			}
			c.After(tn.until.Sub(now))
			right := float32(16)
			if tn.undo != nil {
				right = 6
			}
			// It rises in from below: over the tray, by less than the room
			// above the tray, so that it never touches it.
			rise := float32(14)
			if !top && above {
				rise = 10
			}
			t := ui.Row(c.Key("toast")).Height(44).Padding(0, right, 0, 8).AlignItems(ui.Center).Radius(999).
				Background(p.bg).Border(1, p.line).
				Shadow(0, 12, 32, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.06))
			t.Transition(ui.ElementTransition{Animation: &ui.Snappy, Size: true,
				Enter: &ui.Motion{Y: rise, Scale: 0.96}, Exit: &ui.Motion{Y: 8, Scale: 0.98}})
			if t.Hovered() {
				// The pointer on it holds it, on its way to the undo. A
				// pointer at rest sends nothing that builds the view anew,
				// so it looks again before the hold runs out: otherwise
				// the build at its end would let it go from under it.
				if hold := now.Add(1500 * time.Millisecond); tn.until.Before(hold) {
					tn.until = hold
				}
				c.After(time.Second)
			}
			t.Children(func() {
				body := ui.Row(c.Key(tn.id)).Gap(10).AlignItems(ui.Center)
				body.Transition(ui.ElementTransition{Duration: 150 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
				body.Children(func() {
					if tn.dev != nil {
						deviceTile(c, p, tn.dev.kind, a.tileColor(tn.dev), 28, false)
					}
					ui.Text(c, tn.text).FontSize(13).FontWeight(500).SingleLine()
					if tn.undo != nil {
						u := ui.ButtonBase(c.Key("undo")).Height(32).Padding(0, 12).Radius(999).FocusRing(false)
						if u.Hovered() {
							u.Background(p.primary.Alpha(0.1))
						}
						u.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
						u.AnimatePose(pressPose(u, 0.94), ui.Snappy)
						u.Children(func() { ui.Text(c, "撤销").FontSize(13).FontWeight(600).TextColor(p.primary) })
						if u.Clicked() {
							undo := tn.undo
							a.toast = nil
							undo()
						}
					}
				})
			})
		})
	}
}

// ---- Card -------------------------------------------------------------------------------

// cardLayer holds the card in a layer of its own, over the devices: going
// away, it fades out over them, never under them. It steps out of the way
// while the hand holds something, or something is on its way somewhere,
// and comes back once all is still, so that nothing is ever dragged over
// it or under it.
func (a *app) cardLayer(c *ui.Context, p *palette) {
	layer := ui.Column(c.Key("card-layer")).Absolute().Right(20).Top(12).Width(cardWidth).PassThrough()
	layer.Children(func() {
		if a.selected != "" && !a.busy() {
			a.card(c, p)
		}
	})
}

// busy reports whether the hand holds something, or something moves
// where the hand did not take it.
func (a *app) busy() bool {
	if a.held != nil || a.relay.dragging || a.landing != nil {
		return true
	}
	for _, d := range a.devices {
		if d.dragging || d.flying || d.arriving {
			return true
		}
	}
	return false
}

// card describes the chosen device in plain words: how it is connected,
// what it offers, and (for my own) who may use it (§3.8). It grows out of
// its corner as it opens and shrinks back into it as it closes; choosing
// another device fades its words across while its height follows.
func (a *app) card(c *ui.Context, p *palette) {
	d := a.device(a.selected)
	if d == nil || a.landing == d || !d.gone.IsZero() {
		a.selected = ""
		return
	}
	owner := a.people[d.owner]
	mine := d.mine
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
		// Another device's words take the place of these at once and fade
		// in: two sets of words fading across each other read as a muddle.
		body := ui.Column(c.Key("body-" + d.id)).Padding(16)
		body.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
		body.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				deviceTile(c, p, d.kind, a.tileColor(d), 40, false)
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, d.name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
					who := "我的设备"
					if !mine {
						who = owner.name + "的设备"
					}
					ui.Text(c, who).FontSize(12).TextColor(p.ink2)
				})
				if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
					a.selected, a.more = "", false
				}
			})
			// How it is connected, in one sentence.
			ui.Row(c).Gap(10).Padding(12).Radius(14).Background(p.well).Margin(14, 0, 0, 0).Children(func() {
				icon, title, sub, col := a.howConnected(d, owner, p)
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
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(13).FontWeight(500)
					ui.Text(c, sub).FontSize(12).TextColor(p.ink2)
				})
			})
			label := "你可以用"
			if mine {
				label = "开放给大家的"
			}
			ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
			// The list as this frame began: an op sent from it brings a new
			// one, which the next frame shows.
			svcs := d.services
			ui.Column(c).Radius(14).Background(p.well).Padding(0, 12).Dividers(1, p.line).Children(func() {
				for i := range svcs {
					s := svcs[i]
					ui.Row(c.Key(s.name)).Height(44).Gap(10).Children(func() {
						muted := !mine && !s.on
						ink := p.ink
						if muted {
							ink = p.ink3
						}
						ui.Icon(c, uiGlyphs[s.icon]).Size(18, 18).TextColor(p.ink2)
						ui.Text(c, s.name).FontSize(13).FontWeight(500).TextColor(ink).Grow(1)
						switch {
						case mine:
							if toggle(c, p, s.on) {
								a.send(c, model.Op{Kind: model.OpServiceSet, LAN: a.lan, Device: model.DeviceID(d.id), Service: s.svc.Key, On: !s.on})
							}
						case s.on:
							b := ui.ButtonBase(c.Key("open")).Height(28).Padding(0, 12).Radius(999).Background(p.bg).FocusRing(false).
								Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06))
							b.AnimatePose(pressPose(b, 0.94), ui.Snappy)
							b.Children(func() { ui.Text(c, "打开").FontSize(12).FontWeight(500) })
							if b.Clicked() {
								a.openService(c, d, s)
							}
						default:
							ui.Text(c, "没开放").FontSize(12).TextColor(p.ink3)
						}
					})
				}
			})
			if mine {
				ui.Text(c, "谁能用").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for _, id := range a.members {
						if id == d.owner {
							continue
						}
						off := d.refused[id]
						chip := ui.ButtonBase(c.Key("who-"+id)).Height(34).Padding(0, 12, 0, 4).Gap(8).Radius(999).Background(p.well).FocusRing(false).
							Cursor(ui.CursorPointer)
						if chip.Hovered() {
							chip.Background(p.well2)
						}
						chip.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
						chip.AnimatePose(pressPose(chip, 0.95), ui.Snappy)
						if chip.Clicked() {
							off = !off
							a.send(c, model.Op{Kind: model.OpDeviceRefuse, Device: model.DeviceID(d.id), Person: model.PersonID(id), Refuse: off})
						}
						// Refused, the ball greys and a slash draws across it,
						// and the name greys with it.
						k := chip.AnimateTo("off", map[bool]float32{false: 0, true: 1}[off], ui.Animation{Duration: 200 * time.Millisecond})
						chip.Children(func() {
							ballOff(c, a.people[id], 26, k)
							ui.Text(c, a.people[id].name).FontSize(13).FontWeight(500).TextColor(p.ink.Mix(p.ink3, k))
						})
					}
				})
				ui.Text(c, "点一下头像，就不让他用了").FontSize(12).TextColor(p.ink3).Padding(8, 4, 0, 4)
			}
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
				if d.removable() {
					// The click for the drag into the tray (§6.4).
					out := ui.ButtonBase(c.Key("leave")).Height(28).Padding(0, 10).Radius(999).FocusRing(false)
					if out.Hovered() {
						out.Background(p.well)
					}
					out.AnimatePose(pressPose(out, 0.96), ui.Snappy)
					out.Children(func() { ui.Text(c, "移出这个局域网").FontSize(12).TextColor(p.ink2) })
					if out.Clicked() {
						a.takeOut(c, d, true, false)
					}
				}
			})
			if a.more {
				detail := ui.Column(c.Key("detail"))
				detail.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
				detail.Children(func() {
					for i, line := range a.details(d) {
						top := float32(2)
						if i == 0 {
							top = 8
						}
						ui.Text(c, line).FontSize(12).TextColor(p.ink2).Padding(top, 4, 0, 4).SingleLine()
					}
				})
			}
		})
	})
}

// cardNameWidth is the widest a device's name shows in the card, beside its
// tile and the close button.
const cardNameWidth = 186

// howConnected says in one sentence how the card's device is connected: the
// icon and color of its badge, and two lines.
func (a *app) howConnected(d *device, owner *person, p *palette) (icon, title, sub string, col ui.Color) {
	grey := ui.Hex("#a1a1aa")
	switch d.path {
	case "self":
		return "home", "这就是你正在用的电脑", "别人能用你开放的服务", owner.color
	case "relay":
		return "", "经" + a.relay.name + "中转", "慢一点，但能正常用", grey
	case "connecting":
		return "bolt", "正在连接…", "一般几秒就好", grey
	case "unreachable":
		return "bolt", "连不上", "对方在线，但现在连不上", grey
	case "offline":
		return "bolt", "不在线", "对方开机联网后会自动连上", grey
	case "paused":
		return "bolt", "已暂停", "这个局域网在这台电脑上暂停了", grey
	case "refused":
		return "lock", "主人不让你用", "要用的话，问问" + spaced(owner.name), grey
	case "apart":
		return "bolt", "这台电脑还没加进来", "把托盘里的这台电脑拖进来", grey
	}
	return "bolt", "直接连上", "很快", p.ok
}

// details are the card's "更多": what only someone who asks needs (DESIGN
// §2), the address, the full name, the ports and how long a round trip takes.
func (a *app) details(d *device) []string {
	var out []string
	if d.addr != "" {
		out = append(out, "地址 "+d.addr)
	}
	if d.fqdn != "" {
		out = append(out, "名字 "+d.fqdn)
	}
	var ports []string
	for _, s := range d.services {
		if s.svc.Ports != "" && (d.mine || s.on) {
			ports = append(ports, s.name+" "+s.svc.Ports)
		}
	}
	if len(ports) > 0 {
		out = append(out, strings.Join(ports, " · "))
	}
	if linked(d.path) && d.rtt > 0 {
		out = append(out, fmt.Sprintf("延迟 %d 毫秒", d.rtt.Milliseconds()))
	}
	return out
}

// toggle is the design's switch: a pill in the primary color when on, a
// white knob that springs across. It reports whether it was flipped in this
// frame, and shows the flip at once: the caller sends the op that makes it
// so, and passes in what the snapshot says from then on.
func toggle(c *ui.Context, p *palette, on bool) (flipped bool) {
	v := on
	sw := ui.SwitchBase(c, &v).Size(38, 22).Radius(999).Padding(2).FocusRing(false)
	k := sw.AnimateTo("knob", map[bool]float32{false: 0, true: 1}[v], ui.Snappy)
	sw.Background(p.line2.Mix(p.primary, k))
	sw.Children(func() {
		ui.Box(c).Size(18, 18).Radius(999).Background(white).Translate(16*k, 0).
			Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.08))
	})
	return v != on
}
