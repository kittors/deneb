package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
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
	if t := a.toast; t != nil && t.commit != nil {
		// A new toast takes the place of one that held something back,
		// which goes now.
		t.commit(c)
	}
	a.toasts++
	n := &notice{id: a.toasts, text: text, dev: d, undo: undo, until: c.Now().Add(4 * time.Second)}
	if d != nil && slices.Contains(a.devices, d) && a.landing != d {
		right := a.bottomRight(a.ch)
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

// bottomRight is how far the tray and the toast keep from the canvas's
// right edge. They sit centered under the whole canvas, card open or not:
// a tray that moved left of center whenever a card was open looked off
// (user, 2026-11-08), and while a device is dragged the card is away
// anyway. Only a card that is showing and reaches down into their band,
// the bottom 146 DIPs (a toast above the tray tops out at 134), pushes
// them left, so that they never sit under it.
func (a *app) bottomRight(h float32) float32 {
	if a.selected == "" || a.busy() || a.cardAt.W == 0 || a.cardAt.Y+a.cardAt.H <= h-146 {
		return 0
	}
	return cardWidth + 20
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
	d.leaving, d.aimed, d.flying, d.arriving, d.turnedDown = time.Time{}, false, false, false, false
	a.tray = append(a.tray, d)
	a.trayOff[d.id] = &ui.Point{}
	delete(a.beams, d.id)
	c.AnimationFrame()
}

// takeOut takes my device d out of the LAN: it flies into the tray as the
// op goes out (startLeave).
func (a *app) takeOut(c *ui.Context, d *device, toast, byHand bool) {
	lan := d.lan
	if byHand {
		d.off = ui.Point{X: d.rest.X - d.base.X, Y: d.rest.Y - d.base.Y}
		d.flying = true
	}
	a.askConfirm("移出局域网", a.snap.Device(d.dev).Name, "设备将断开此局域网。", func(c *ui.Context) {
		if d.lan == lan {
			a.takeOutNow(c, d, toast, false)
		}
	})
}
func (a *app) takeOutNow(c *ui.Context, d *device, toast, byHand bool) {
	a.startLeave(c, d, toast, byHand)
	a.send(c, model.Op{Kind: model.OpLANRemoveDevice, LAN: d.lan, Device: d.dev})
}

// bringIn puts my device d from the tray into the LAN at spot as the op goes
// out (joinFrom).
func (a *app) bringIn(c *ui.Context, d *device, from, spot ui.Point, pose ui.Pose) {
	a.joinFrom(c, d, from, spot, pose, true)
	a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: d.lan, Device: d.dev})
}

// undoLeave puts a device that went into the tray back where it rested: the
// undo of taking it out is bringing it in again (§2.6.2).
func (a *app) undoLeave(c *ui.Context, d *device) {
	if a.landing == d {
		// Still on its way: it turns back.
		a.turnBack(d)
		a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: d.lan, Device: d.dev})
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
	a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: d.lan, Device: d.dev})
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
	d.turnedDown = false
	if announce {
		text := "已把" + short(d.name) + "加进局域网"
		if l := a.snap.LAN(d.lan); l != nil && (l.Waiting || l.ApproveDevices && l.Owner != model.PersonID(d.owner)) {
			// It waits for the owner (DESIGN §6.7): the toast says so, not
			// that it is in. Its undo takes the asking back.
			text = say(short(d.name), "等", a.personName(l.Owner), "同意")
		}
		a.notify(c, d, text, func() { a.takeOut(c, d, false, false) })
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
	if r := dr.relay; r != nil && dr.near {
		// A relay in the hand (DESIGN §6.12): mine goes back in; the public
		// one and a friend's stay.
		if !a.relayRemovable(r) {
			return trayRefuse, a.relayRefusal(r)
		}
		lan := ""
		if l := a.snap.LAN(a.lan); l != nil {
			lan = short(l.Name)
		}
		if dr.over {
			return trayArmed, say("松手，", lan, "就不用它了")
		}
		return trayOffer, say("拖到这里，", lan, "就不用它了")
	}
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
	if a.trayHint != "" && dr.d == nil {
		return trayIdle, a.trayHint
	}
	if a.cameHint != "" && dr.d == nil && a.now().Before(a.cameUntil) {
		// A relay of mine came up (M4).
		return trayIdle, a.cameHint
	}
	return trayIdle, "拖出来，加进局域网"
}

// trayShows reports whether the tray is up: it holds devices, one is on
// its way into it, or the hand brings one near.
func (a *app) trayShows() bool {
	mode, _ := a.trayMode()
	return len(a.tray) > 0 || a.landing != nil || mode != trayIdle || a.relayTray()
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
	a.held, a.trayDrag, a.heldRelay = nil, nil, ""
	if a.now().Before(a.cameUntil) {
		c.After(a.cameUntil.Sub(a.now()))
	}
	right := a.bottomRight(h)
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
			// My relays, the install commands waiting, and the "+" (M4).
			a.trayRelayItems(c, p, w, h)
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
		// Up into the header too, to the balls there (DESIGN §6.11).
		top := a.shareTop(d)
		bounds = &ui.Rect{X: 70 - home.X, Y: top - home.Y, W: max(0, w-140), H: max(0, h-36-top)}
	}
	item.Draggable(off, ui.DraggableOptions{Bounds: bounds, Rigid: true, Animation: ui.Snappy})
	if _, _, ok := item.DragEnded(); ok && known {
		if a.dropOnBall(c, d) {
			// Given to someone, or not, it goes back to its place.
			*off = ui.Point{}
			a.unclipUntil = c.Now().Add(800 * time.Millisecond)
			return
		}
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
		a.trayDrag, a.trayDragAt = d, ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
		if home.Y+off.Y <= h-trayBand && !a.upHigh(d) {
			// Out over the canvas: the devices there make room for it.
			a.held, a.heldAt = d, ui.Point{X: home.X + off.X, Y: home.Y + off.Y}
		}
	}
	// Out of the tray it is a tile of the canvas in the hand, as large and
	// as turned as one, so that it lands as one.
	pose := ui.Pose{}
	switch {
	case item.Dragging() && a.upHigh(d):
		// Up in the header: as a tile of the canvas would be there, in the
		// ball the hand is over.
		pose = ui.Pose{Scale: a.upScale() * float32(tileSize) / slotTile, Rotate: liftPose.Rotate}
		snap := a.snapTo(ui.Point{X: a.origin.X + home.X + off.X, Y: a.origin.Y + home.Y + off.Y})
		pose.X, pose.Y = snap.X, snap.Y
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
		a.profileTile(c, p, d.dev, d.kind, a.tileColor(d), slotTile, false, 0).AnimatePose(pose, ui.Snappy)
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
		// The commit may synchronously replace the toast with a refusal.
		// Retire this toast first so its cleanup cannot erase the new one.
		a.toast = nil
		if tn.commit != nil {
			tn.commit(c)
		}
		tn = a.toast
	}
	right := a.bottomRight(h)
	// Both places are built in every frame, so that a toast comes in and
	// goes out where it shows: Enter and Exit play in a parent that stays.
	above := !a.myDevicesOpen && (a.trayShows() || a.trayUp)
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
						a.profileTile(c, p, tn.dev.dev, tn.dev.kind, a.tileColor(tn.dev), 28, false, 0)
					}
					if tn.orb {
						relayOrb(c, p, 28, false, 0)
					}
					ui.Text(c, tn.text).FontSize(13).FontWeight(500).SingleLine()
					if tn.undo != nil {
						u := ui.ButtonBase(c.Key("undo")).Height(32).Padding(0, 12).Radius(999).FocusRing(false)
						if u.Hovered() {
							u.Background(p.primary.Alpha(0.1))
						}
						u.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
						u.AnimatePose(pressPose(u, 0.94), ui.Snappy)
						label := "撤销"
						if tn.undoLabel != "" {
							// Not an undo: what to do next (§6.14).
							label = tn.undoLabel
						}
						u.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(p.primary) })
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
	// In the "全部" view, a device in the right half has its card at the
	// left (cardOnLeft). Both layers stay, so that a card leaving one for
	// the other plays its way out.
	left := a.cardOnLeft()
	for _, side := range [2]bool{false, true} {
		key := "card-layer"
		if side {
			key = "card-layer-left"
		}
		layer := ui.Column(c.Key(key)).Absolute().Top(12).Width(cardWidth).PassThrough()
		if side {
			layer.Left(20)
		} else {
			layer.Right(20)
		}
		layer.Children(func() {
			if side == left && a.selected != "" && !a.busy() {
				a.cardLeft = side
				a.card(c, p)
			}
		})
	}
}

// busy reports whether the hand holds something, or something moves
// where the hand did not take it.
func (a *app) busy() bool {
	// (A tile let go up in the header flies back until unclipUntil.)
	if a.held != nil || a.trayDrag != nil && a.upHigh(a.trayDrag) || a.relayDragging() || a.landing != nil || a.clock.Before(a.unclipUntil) {
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
	if a.selected == lanCard {
		a.lanCard(c, p)
		return
	}
	if a.selected == noticeCard {
		a.noticeCard(c, p)
		return
	}
	if a.selected == seatCard {
		a.seatCardView(c, p)
		return
	}
	if id, ok := strings.CutPrefix(a.selected, personCard); ok {
		a.personCardView(c, p, id)
		return
	}
	if id, ok := strings.CutPrefix(a.selected, relayCard); ok {
		a.relayCardView(c, p, model.RelayID(id))
		return
	}
	if id, ok := strings.CutPrefix(a.selected, joinCard); ok {
		a.joinCardView(c, p, id)
		return
	}
	d := a.device(a.selected)
	if d == nil || a.landing == d || !d.gone.IsZero() {
		a.selected = ""
		return
	}
	owner := a.people[d.owner]
	mine := d.mine
	card := ui.Column(c.Key("card")).Width(cardWidth).Radius(20).Background(p.bg).Clip().
		Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05)).TransformOrigin(1, 0)
	if a.cardLeft {
		card.TransformOrigin(0, 0)
	}
	if p.dark {
		card.Border(1, p.line)
	}
	if b := card.Bounds(); b.W > 0 {
		a.cardAt = ui.Rect{X: b.X - a.origin.X, Y: b.Y - a.origin.Y, W: b.W, H: b.H}
	}
	card.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Size: true,
		Enter: &ui.Motion{Scale: 0.96, Y: -8}, Exit: &ui.Motion{Scale: 0.97, Y: -6}})
	if a.ch > 0 {
		// Taller than the canvas, as this computer's with "更多" open in a
		// short window: it scrolls in itself (M7, DESIGN §6.15).
		card.MaxHeight(a.ch - 24)
	}
	card.Children(func() {
		ui.Scroll(c.Key("card-scroll")).Shrink(1).Children(func() {
			// Another device's words take the place of these at once and fade
			// in: two sets of words fading across each other read as a muddle.
			body := ui.Column(c.Key("body-" + d.key())).Padding(16)
			body.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
			body.Children(func() {
				// My devices take a new name and kind in place (DESIGN §6.9):
				// a click on the name edits it, one on the tile shows the kinds.
				named := mine
				ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
					if mine {
						kind := ui.ButtonBase(c.Key("kind")).Radius(12).FocusRing(false).Cursor(ui.CursorPointer)
						kind.AnimatePose(pressPose(kind, 0.94), ui.Snappy)
						kind.Children(func() { a.profileTile(c, p, d.dev, d.kind, a.tileColor(d), 40, false, 0) })
						if kind.Clicked() {
							a.openAvatar(d.dev)
						}
					} else {
						a.profileTile(c, p, d.dev, d.kind, a.tileColor(d), 40, false, 0)
					}
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						if named {
							name := ui.ButtonBase(c.Key("name")).FocusRing(false).Cursor(ui.CursorText).AlignSelf(ui.Start)
							name.Children(func() {
								ui.Text(c, a.snap.Device(d.dev).Name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
							})
							if name.Clicked() {
								a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.dev}, a.snap.Device(d.dev).Name)
							}
						} else {
							ui.Text(c, d.name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
						}
						who := "我的设备"
						if !mine {
							who = strings.TrimLeft(spaced(owner.name), " ") + "的设备"
						}
						if given := a.givenWho(d); given != "" {
							// Given to me, or a LAN's own (DESIGN §6.11).
							who = given
						}
						ui.Text(c, who).FontSize(12).TextColor(p.ink2)
						if a.sameName(d) {
							// Two of one name on the canvas: a word on which, and a
							// way to tell them apart (DESIGN §6.8).
							ui.Row(c).Gap(4).Children(func() {
								ui.Text(c, "和另一台同名").FontSize(12).TextColor(p.ink3)
								if named && a.renaming == nil {
									ui.Text(c, "·").FontSize(12).TextColor(p.ink3)
									fix := ui.ButtonBase(c.Key("rename")).FocusRing(false).Cursor(ui.CursorPointer)
									fix.Children(func() { ui.Text(c, "改个名字").FontSize(12).TextColor(p.primary) })
									if fix.Clicked() {
										a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.dev}, a.snap.Device(d.dev).Name)
									}
								}
							})
						}
					})
					if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
						a.selected, a.more, a.picking, a.renaming = "", false, false, nil
					}
				})
				a.profileActions(c, p, d)
				if mine {
					a.kinds(c, p, d)
				}
				// How it is connected, in one sentence. Through a relay of M4, a
				// click shows the relay's card (DESIGN §6.12).
				how := ui.Row(c.Key("how-row")).Gap(10).Padding(12).Radius(14).Background(p.well).Margin(14, 0, 0, 0)
				if d.path == "relay" && a.snap.Can(model.FeatureRelays) && a.snap.Relays[a.relayOf(d)] != nil {
					how.Cursor(ui.CursorPointer).Label("看看这个中转站")
					if how.Clicked() {
						a.openRelay(a.relayOf(d))
					}
				}
				how.Children(func() {
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
					// New words fade in as the state changes, as another device's
					// do; what can be done about it folds in and out under them,
					// the card's height following.
					ui.Column(c.Key("how")).Grow(1).Children(func() {
						words := ui.Column(c.Key("how-" + title)).Gap(2)
						words.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
						words.Children(func() {
							ui.Text(c, title).FontSize(13).FontWeight(500)
							if sub != "" {
								ui.Text(c, sub).FontSize(12).TextColor(p.ink2)
							}
						})
						// What can be done about it, in a click (DESIGN §6.8). It
						// folds in and out, its height drawn out frame by frame;
						// folding shut, it keeps its last words.
						label, do := a.remedy(d, owner)
						if a.remedyWas == nil {
							a.remedyWas = map[string]string{}
						}
						if label != "" {
							a.remedyWas[d.id] = label
						}
						holder := ui.Column(c.Key("remedy-holder")).Clip()
						shown := float32(0)
						if label != "" {
							shown = 1
						}
						k := holder.AnimateTo("shown", shown, ui.Animation{Duration: 180 * time.Millisecond})
						holder.Height((8 + 28) * k)
						if was := a.remedyWas[d.id]; k > 0 && was != "" {
							holder.Children(func() {
								b := ui.ButtonBase(c.Key("remedy")).Height(28).Padding(0, 12).Radius(999).Background(p.bg).FocusRing(false).
									Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).AlignSelf(ui.Start).Margin(8, 0, 0, 0).Opacity(k)
								b.AnimatePose(pressPose(b, 0.94), ui.Snappy)
								b.Children(func() { ui.Text(c, was).FontSize(12).FontWeight(500).SingleLine() })
								if b.Clicked() && label != "" {
									do(c)
								}
							})
						}
					})
				})
				// This computer in the "全部" view is in no one LAN: what it opens,
				// and to whom, is each LAN's, on its own canvas.
				perLAN := !((a.all() || a.shared()) && d.id == a.self)
				label := "可用服务"
				if mine {
					label = "公开服务"
				}
				if perLAN {
					ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
				}
				// The list as this frame began: an op sent from it brings a new
				// one, which the next frame shows.
				svcs := d.services
				if !perLAN {
					// Nothing to list: the list takes no room.
					svcs = nil
				}
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
							ui.Text(c, serviceLabel(d.services, s)).FontSize(13).FontWeight(500).TextColor(ink).Grow(1)
							if s.on && d.dev != a.snap.Self {
								b := ui.ButtonBase(c.Key("open")).Height(28).Padding(0, 12).Radius(999).Background(p.bg).FocusRing(false).
									Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06))
								b.AnimatePose(pressPose(b, 0.94), ui.Snappy)
								b.Children(func() { ui.Text(c, "打开").FontSize(12).FontWeight(500) })
								if b.Clicked() {
									a.openService(c, d, s)
								}
							}
							if mine {
								if toggle(c, p, s.on) {
									op := model.Op{Kind: model.OpServiceSet, LAN: d.lan, Device: d.dev, Service: s.svc.Key, On: !s.on}
									if s.on {
										a.askConfirm("关闭服务", s.name, d.name, func(c *ui.Context) { a.send(c, op) })
									} else {
										a.send(c, op)
									}
								}
							} else if !s.on {
								ui.Text(c, "没开放").FontSize(12).TextColor(p.ink3)
							}
						})
					}
				})
				if mine && perLAN {
					ui.Text(c, "访问权限").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
					ui.Row(c).Gap(8).Wrap().Children(func() {
						for _, id := range a.membersOf(d) {
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
								op := model.Op{Kind: model.OpDeviceRefuse, Device: d.dev, Person: model.PersonID(id), Refuse: off}
								if off {
									a.askConfirm("撤销访问权限", a.people[id].name, "将无法访问此设备。", func(c *ui.Context) { a.send(c, op) })
								} else {
									a.send(c, op)
								}
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

				}
				if mine {
					// Given to people one by one, and which way (DESIGN §6.11).
					a.shareSection(c, p, d)
				}
				given := a.snap.SharedOf(d.dev) != nil
				if given {
					ui.Text(c, "仅允许你访问此设备").FontSize(12).TextColor(p.ink3).Padding(8, 4, 0, 4)
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
						out.Children(func() { ui.Text(c, "移出局域网").FontSize(12).TextColor(p.ink2) })
						if out.Clicked() {
							a.takeOut(c, d, true, false)
						}
					}
					if given && a.shared() && a.snap.Can(model.FeatureShareMode) {
						// Given to me: I can give it back (DESIGN §6.11).
						out := ui.ButtonBase(c.Key("give-back")).Height(28).Padding(0, 10).Radius(999).FocusRing(false)
						if out.Hovered() {
							out.Background(p.well)
						}
						out.AnimatePose(pressPose(out, 0.96), ui.Snappy)
						out.Children(func() { ui.Text(c, "取消共享").FontSize(12).TextColor(p.ink2) })
						if out.Clicked() {
							a.leaveShare(c, d)
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
						if d.id == a.self && a.snap.Can(model.FeatureDiagnostics) {
							a.diagnosticsRow(c, p)
						}
					})
				}
			})
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
	switch {
	case d.pending && d.mine:
		return "bolt", "等" + spaced(a.ownerName()) + "同意", strings.TrimLeft(spaced(a.ownerName()), " ") + "同意以后，它才连得进来", grey
	case d.login.Expired && d.mine:
		return "bolt", "登录过期了", "续上就能连", grey
	case d.login.Expired:
		return "bolt", "登录过期了", "要等" + spaced(owner.name) + "在那台设备上续上", grey
	case d.mine && d.login.Due(a.now()):
		return "bolt", "需要重新登录", "还有 " + strconv.Itoa(max(1, int(math.Ceil(d.login.Expires.Sub(a.now()).Hours()/24)))) + " 天到期", grey
	case d.stale:
		return "bolt", "旧的那台", ago(a.now().Sub(d.lastSeen)) + "上线过，后来就没了", grey
	case d.path == "offline" && !d.lastSeen.IsZero():
		return "bolt", "不在线", "上次在线：" + ago(a.now().Sub(d.lastSeen)), grey
	case d.path == "unreachable":
		return "bolt", "连不上", "两边的路由器都不让直连", grey
	}
	switch d.path {
	case "self":
		if a.all() {
			return "home", "本机", a.inWhich(), owner.color
		}
		return "home", "本机", "", owner.color
	case "relay":
		return "", a.viaWords(d), a.viaSub(d), grey
	case "connecting":
		return "bolt", "正在连接…", "一般几秒就好", grey
	case "unreachable":
		return "bolt", "连不上", "对方在线，但现在连不上", grey
	case "offline":
		return "bolt", "不在线", "对方开机联网后会自动连上", grey
	case "nonet":
		return "bolt", "等这台电脑联网", "这台电脑没联网，联网后会自动连上", grey
	case "paused":
		return "bolt", "已暂停", "这个局域网在这台电脑上暂停了", grey
	case "refused":
		return "lock", "主人不让你用", "要用的话，问问" + spaced(owner.name), grey
	case "apart":
		return "bolt", "这台电脑还没加进来", "把托盘里的这台电脑拖进来", grey
	}
	return "bolt", "直接连上", "", p.ok
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

// approve lets a device that waits into the LAN, or turns it away; one of
// mine that waits is taken back instead (DESIGN §6.7). It shows at once and
// goes to the server as its toast goes, so that undo needs no server.
func (a *app) approve(c *ui.Context, d *device, yes bool) {
	if !yes {
		a.askConfirm("撤回或拒绝加入", d.name, "设备将不会加入此局域网。", func(c *ui.Context) { a.approveNow(c, d, false) })
		return
	}
	a.approveNow(c, d, yes)
}
func (a *app) approveNow(c *ui.Context, d *device, yes bool) {
	if !d.pending {
		return
	}
	op := model.Op{Kind: model.OpDeviceReject, LAN: d.lan, Device: d.dev}
	text := "已拒绝" + short(d.name)
	switch {
	case d.mine:
		op.Kind, text = model.OpLANRemoveDevice, "已撤回"+short(d.name)
	case yes:
		op.Kind, text = model.OpDeviceApprove, "已同意"+short(d.name)+"加进来"
	}
	a.hold(c, op, d, text)
}

// renew renews the login of one of my devices (DESIGN §6.8).
func (a *app) renew(c *ui.Context, d *device) {
	a.send(c, model.Op{Kind: model.OpDeviceRenew, Device: d.dev})
	a.notify(c, d, "已续上", nil)
}

// deviceKinds are the kinds a device of mine can be told to be, in the
// order the card shows them (DESIGN §6.9).
var deviceKinds = []struct{ kind, glyph, name string }{
	{"laptop", "laptop", "笔记本"}, {"desktop", "desktop", "台式机"}, {"server", "server", "服务器"},
	{"nas", "nas", "NAS"}, {"phone", "phone", "手机"}, {"console", "console", "游戏主机"}, {"other", "mini", "其他"},
}

// kinds is the card's row of kinds: the one d is, ringed in blue; a click
// on another makes d that. It folds open under the name and folds shut,
// its height drawn out frame by frame, so that what is under it moves with
// it rather than jumping.
func (a *app) kinds(c *ui.Context, p *palette, d *device) {
	holder := ui.Column(c.Key("kinds-holder")).Clip()
	open := float32(0)
	if a.picking {
		open = 1
	}
	k := holder.AnimateTo("open", open, ui.Animation{Duration: 220 * time.Millisecond})
	holder.Height((12 + 34) * k)
	if k <= 0 {
		return
	}
	// Made inside the holder, to be its child: what is made outside is
	// another child of the card.
	holder.Children(func() {
		row := ui.Row(c.Key("kinds")).Gap(6).Margin(12, 0, 0, 0).Opacity(k)
		a.kindRow(c, p, d, row)
	})
}

func (a *app) kindRow(c *ui.Context, p *palette, d *device, row ui.Element) {
	row.Children(func() {
		for _, k := range deviceKinds {
			b := ui.ButtonBase(c.Key("kind-"+k.kind)).Size(34, 34).Radius(10).Center().Background(p.well).FocusRing(false).
				Cursor(ui.CursorPointer).Tooltip(k.name).Label(k.name)
			if k.glyph == d.kind {
				b.Border(1.5, p.primary)
			} else if b.Hovered() {
				b.Background(p.well2)
			}
			b.AnimatePose(pressPose(b, 0.92), ui.Snappy)
			b.Children(func() { ui.Icon(c, deviceGlyphs[k.glyph]).Size(18, 18).TextColor(p.ink2) })
			if b.Clicked() {
				a.picking = false
				if k.glyph != d.kind || len(a.snap.Device(d.dev).Avatar) > 0 {
					a.send(c, model.Op{Kind: model.OpDeviceSetKind, Device: d.dev, DevKind: k.kind})
				}
			}
		}
	})
}

// sameName reports whether another device on the canvas has d's name.
func (a *app) sameName(d *device) bool {
	if d.id == a.self {
		return false
	}
	for _, o := range a.devices {
		if o.dev != d.dev && o.id != a.self && o.name == d.name && o.gone.IsZero() {
			return true
		}
	}
	return false
}

// remedy is what the card's device's state lets me do about it, in a click:
// the button's words, and what it does (DESIGN §6.8). None: "".
func (a *app) remedy(d *device, owner *person) (string, func(c *ui.Context)) {
	who := strings.TrimLeft(spaced(owner.name), " ")
	tell := func(text, ask string) func(c *ui.Context) {
		return func(c *ui.Context) {
			if a.copyText != nil {
				a.copyText(text)
			}
			a.notify(c, nil, "已复制，发给"+who+"就行", nil)
		}
	}
	switch {
	case d.pending && d.mine:
		return "不加了", func(c *ui.Context) { a.approve(c, d, false) }
	case (d.login.Expired || d.login.Due(a.now())) && d.mine && a.snap.Can(model.FeatureRenew):
		return "续上", func(c *ui.Context) { a.renew(c, d) }
	case d.login.Expired:
		return "提醒" + who, tell("你的"+d.name+"在 Deneb 里登录过期了，打开 Deneb 点一下续上就好", "")
	case d.stale && d.removable():
		return "移出", func(c *ui.Context) { a.takeOut(c, d, true, false) }
	case d.path == "offline":
		if a.watching[string(d.dev)] {
			return "上线时会告诉你", func(*ui.Context) { delete(a.watching, string(d.dev)) }
		}
		return "上线时告诉我", func(*ui.Context) {
			if a.watching == nil {
				a.watching = map[string]bool{}
			}
			a.watching[string(d.dev)] = true
		}
	case d.path == "refused":
		return "问问" + who, tell("能让我用一下你的"+d.name+"吗？在 Deneb 里点一下就行", "")
	case d.path == "unreachable" && a.snap.Can(model.FeatureAddRelay):
		return "加一个中转站", func(c *ui.Context) { a.send(c, model.Op{Kind: model.OpLANAddRelay, LAN: d.lan}) }
	}
	return "", nil
}

// ago is how long ago, in a few words: "3 小时前", "昨天", "40 天前".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " 分钟前"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " 小时前"
	case d < 48*time.Hour:
		return "昨天"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " 天前"
}

// lanCard is what a.selected is while the LAN's card shows.
const lanCard = "\x00lan"

// lanCard describes the LAN on the canvas: its name, its owner, its members
// and, for the owner, the two switches of who waits to be let in (DESIGN
// §6.7). It takes the device card's place, in the same way.
func (a *app) lanCard(c *ui.Context, p *palette) {
	l := a.snap.LAN(a.lan)
	if l == nil {
		a.selected = ""
		return
	}
	mine := l.Owner == a.snap.Me
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
		body := ui.Column(c.Key("body-lan-" + string(l.ID))).Padding(16)
		body.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
		body.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				// The LAN's mark: its first members' balls, as its row in the
				// sidebar shows them.
				ui.Box(c).Size(40, 40).Radius(12).Background(p.well).Center().Children(func() {
					ui.Row(c).Children(func() {
						for i, id := range l.Members[:min(3, len(l.Members))] {
							b := ball(c, a.people[string(id)], 16, false).Shadow(0, 0, 0, 1.5, p.well)
							if i > 0 {
								b.Margin(0, 0, 0, -5)
							}
						}
					})
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					if mine {
						name := ui.ButtonBase(c.Key("lan-name")).FocusRing(false).Cursor(ui.CursorText).AlignSelf(ui.Start)
						name.Children(func() {
							ui.Text(c, l.Name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
						})
						if name.Clicked() {
							a.beginRename(model.Op{Kind: model.OpLANRename, LAN: l.ID}, l.Name)
						}
					} else {
						ui.Text(c, l.Name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(cardNameWidth)
					}
					owner := "我"
					if !mine {
						owner = a.people[string(l.Owner)].name
					}
					ui.Text(c, "拥有者 · "+owner).FontSize(12).TextColor(p.ink2)
				})
				if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
					a.selected, a.renaming = "", nil
				}
			})
			ui.Text(c, "成员").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, id := range l.Members {
					ui.Row(c.Key("member-"+string(id))).Height(34).Padding(0, 12, 0, 4).Gap(8).Radius(999).Background(p.well).Children(func() {
						ball(c, a.people[string(id)], 26, false)
						ui.Text(c, a.people[string(id)].name).FontSize(13).FontWeight(500)
					})
				}
			})
			if !mine || !a.snap.Can(model.FeatureApprove) {
				// Not mine, or a server that cannot ask me: no switches.
				return
			}
			ui.Text(c, "加入审批").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
			ui.Column(c).Radius(14).Background(p.well).Padding(0, 12).Dividers(1, p.line).Children(func() {
				for _, sw := range []struct {
					key, label string
					on         bool
					kind       model.OpKind
				}{
					{"approve-devices", "审批新设备", l.ApproveDevices, model.OpLANSetApproveDevices},
					{"approve-members", "审批新成员", l.ApproveMembers, model.OpLANSetApproveMembers},
				} {
					ui.Row(c.Key(sw.key)).Height(44).Gap(10).Children(func() {
						ui.Text(c, sw.label).FontSize(13).FontWeight(500).Grow(1)
						if toggle(c, p, sw.on) {
							a.send(c, model.Op{Kind: sw.kind, LAN: l.ID, On: !sw.on})
						}
					})
				}
			})

			if !a.snap.Can(model.FeatureShareMode) {
				return
			}
			// Members use only what the owner gives each of them (DESIGN
			// §6.11, scene 4). Their devices go at once; the server hears
			// of it as the toast goes, so undo is nothing done.
			ui.Text(c, "设备共享").FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(18, 4, 8, 4)
			ui.Column(c).Radius(14).Background(p.well).Padding(0, 12).Children(func() {
				ui.Row(c.Key("share-mode")).Height(44).Gap(10).Children(func() {
					ui.Text(c, "按成员分配设备").FontSize(13).FontWeight(500).Grow(1)
					on := l.Mode == model.ModeShare
					if toggle(c, p, on) {
						mode, text := model.ModeShare, say("「"+short(l.Name)+"」", "改成成员只用分到的设备")
						if on {
							mode, text = model.ModeLAN, say("「"+short(l.Name)+"」", "改回大家的设备互相看得见")
						}
						a.askConfirm("更改共享模式", l.Name, "现有设备连接与访问范围将改变。", func(c *ui.Context) { a.holdFor(c, model.Op{Kind: model.OpLANSetMode, LAN: l.ID, Mode: mode}, text) })
					}
				})
			})
			line := "仅能访问分配的设备"
			if l.Mode == model.ModeShare {
				line = "关闭后恢复设备互联"
			}
			ui.Text(c, line).FontSize(12).TextColor(p.ink3).Padding(8, 4, 0, 4)
		})
	})
}

// dashedBall is someone who waits: to be let in by me, or to answer my
// invite. Their ball's shape in a dashed line of their color, their
// initial faint (DESIGN §6.7).
func dashedBall(c *ui.Context, p *palette, who *person, size float32) ui.Element {
	b := ui.Box(c).Size(size, size).Center()
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-0.75)
		pt.FillPath(&path, p.bg)
		pt.FillPath(&path, who.color.Alpha(0.08))
		path.Dash(0, 3, 2.5)
		pt.StrokePath(&path, 1.5, who.color.Alpha(0.8))
	})
	b.Children(func() {
		ui.Text(c, who.ch).FontSize(size * 0.42).FontWeight(600).TextColor(who.color.Alpha(0.75))
	})
	return b
}

// How someone is in a LAN's stack of balls.
const (
	member      = iota
	waitsForMe  // waits for me, the owner, to let them in
	invitedByMe // invited by name, and not answered yet
)

// stackBall is someone's ball in the header's stack, one that stays as they
// go from waiting to in (DESIGN §6.7): a member's is solid, and can be
// dragged onto a LAN's row to invite them there; one who waits shows
// dashed, a click lets them in, and on the pointer a small "×" turns them
// away, or takes my invite back. Let in, the dashed line fills to the ball.
func (a *app) stackBall(c *ui.Context, p *palette, l *model.LAN, id string, how int, overlap bool, invited model.Invited) {
	who := a.people[id]
	el := ui.Box(c.Key("stack-"+id)).Size(30, 30)
	// One of my devices in the hand, the stack spreads out, so that each
	// ball it can be given to shows whole; the others fade (DESIGN §6.11).
	spread := a.aim.d != nil
	target := how == member && id != a.me && a.snap.Can(model.FeatureShare)
	gap := el.AnimateTo("spread", map[bool]float32{false: -6, true: 8}[spread], ui.Snappy)
	if overlap {
		el.Margin(0, 0, 0, gap)
	}
	if fade := el.AnimateTo("fade", map[bool]float32{false: 0, true: 1}[spread && !target], ui.Animation{Duration: 160 * time.Millisecond}); fade > 0 {
		el.Opacity(1 - 0.6*fade)
	}
	if target {
		if b := el.Bounds(); b.W > 0 && a.ballRects != nil {
			a.ballRects[id] = b
		}
	}
	aimed := spread && a.aim.who == id && a.aim.ok
	el.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.5}, Exit: &ui.Motion{Scale: 0.5}})
	waits := float32(0)
	if how != member {
		waits = 1
	}
	k := el.AnimateTo("waits", waits, ui.Animation{Duration: 220 * time.Millisecond})
	if a.stackWas == nil {
		a.stackWas, a.stackIn = map[string]int{}, map[string]time.Time{}
	}
	if was, ok := a.stackWas[id]; ok && was == waitsForMe && how == member {
		a.stackIn[id] = c.Now()
	}
	a.stackWas[id] = how
	pose, anim := ui.Pose{}, ui.Snappy
	popped := c.Now().Sub(a.popAt[id])
	switch since := c.Now().Sub(a.stackIn[id]); {
	case popped < 110*time.Millisecond:
		// A device given to them just now: the ball takes it, and springs.
		pose, anim = ui.Pose{Scale: 1.22}, ui.Animation{Duration: 110 * time.Millisecond}
		c.After(110*time.Millisecond - popped)
	case popped < 800*time.Millisecond:
		anim = ui.Bouncy
	case aimed:
		pose = ui.Pose{Scale: 1.15}
	case since < 90*time.Millisecond:
		// Let in: it dips, and springs up full.
		pose, anim = ui.Pose{Scale: 0.88}, ui.Animation{Duration: 90 * time.Millisecond}
		c.After(90*time.Millisecond - since)
	case since < 700*time.Millisecond:
		anim = ui.Bouncy
	case how != member && el.Pressed():
		pose = ui.Pose{Scale: 0.92}
	case how != member && el.Hovered():
		pose = ui.Pose{Y: -2}
	}
	el.AnimatePose(pose, anim)
	hovered := el.Hovered()
	ring := el.AnimateTo("ring", map[bool]float32{false: 0, true: 1}[aimed], ui.Animation{Duration: 120 * time.Millisecond})
	el.Children(func() {
		solid := ball(c, who, 30, false).Shadow(0, 0, 0, 2, p.bg)
		if ring > 0 {
			solid.Shadow(0, 0, 0, 2+1.5*ring, p.primary.Alpha(ring))
		}
		if k > 0 {
			solid.Opacity(1 - k)
			dashedBall(c, p, who, 30).Absolute().Left(0).Top(0).Opacity(k).PassThrough()
		}
		if how != member && hovered {
			x := ui.ButtonBase(c.Key("away")).Absolute().Right(-6).Top(-6).Size(16, 16).Radius(999).Center().
				Background(p.bg).Border(1, p.line).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.12)).FocusRing(false)
			x.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.6}, Exit: &ui.Motion{Scale: 0.6}})
			x.Children(func() { ui.Icon(c, uiGlyphs["close"]).Size(10, 10).TextColor(p.ink2) })
			if x.Clicked() {
				switch how {
				case waitsForMe:
					a.askConfirm("拒绝加入", who.name, l.Name, func(c *ui.Context) {
						a.holdFor(c, model.Op{Kind: model.OpMemberReject, LAN: l.ID, Person: model.PersonID(id)}, "已拒绝"+spaced(who.name))
					})
				case invitedByMe:
					a.askConfirm("撤回邀请", who.name, l.Name, func(c *ui.Context) {
						a.send(c, model.Op{Kind: model.OpInviteRevoke, LAN: l.ID, Person: model.PersonID(id), Invite: invited.Invite})
						a.notify(c, nil, "已收回给"+spaced(who.name)+"的邀请", nil)
					})
				}
			}
		}
	})
	switch how {
	case member:
		if id != a.me {
			if a.snap.Can(model.FeatureInvitePerson) {
				a.personHandle(c, el, id)
			}
			if a.snap.Can(model.FeatureShare) {
				// A click shows what I gave them, and they me.
				if !a.snap.Can(model.FeatureInvitePerson) {
					el.Cursor(ui.CursorPointer)
				}
				if el.Clicked() {
					if a.selected == personCard+id {
						a.selected = ""
					} else {
						a.selected, a.more, a.shareOpen, a.sharePick = personCard+id, false, "", ""
					}
				}
			}
		}
	case waitsForMe:
		el.Cursor(ui.CursorPointer).Tooltip(strings.TrimLeft(spaced(who.name), " ") + "想加入，点一下同意")
		if el.Clicked() {
			if a.fullFor(l.ID, model.PersonID(id)) {
				// No seat for them: first a seat to free (DESIGN §6.11).
				a.selected, a.seatFor, a.seatFreeing, a.more = seatCard, id, false, false
				return
			}
			a.holdFor(c, model.Op{Kind: model.OpMemberApprove, LAN: l.ID, Person: model.PersonID(id)}, "已同意"+spaced(who.name)+"加入")
		}
	case invitedByMe:
		el.Tooltip("已邀请" + spaced(who.name) + "，等他接受")
	}
}
