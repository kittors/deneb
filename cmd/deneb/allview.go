package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The "全部" view: every LAN at once, each in a region of its own around
// this computer (DESIGN §9 scene 3, and its part for the client).

// allLANs is what a.lan is while the canvas shows every LAN at once.
const allLANs model.LANID = "\x00all"

// all reports whether the canvas shows every LAN at once.
func (a *app) all() bool { return a.lan == allLANs }

// shownLANs are the LANs the "全部" view shows a region for, in the order I
// joined them: those I am in, not those I wait to be let into.
func (a *app) shownLANs() []*model.LAN {
	var out []*model.LAN
	for _, l := range a.snap.LANs {
		if !l.Waiting {
			out = append(out, l)
		}
	}
	return out
}

// allKey keys a device in a LAN's region: by the LAN and the device, so
// that it keeps its key whatever other LANs it is in, and one in two LANs
// shows twice, once in each.
func allKey(lan model.LANID, id model.DeviceID) string { return string(lan) + "/" + string(id) }

// allSpots are the devices the "全部" view shows: this computer, in no
// region, and every LAN's devices in its region.
func (a *app) allSpots() []spot {
	self := model.DeviceID(a.self)
	var out []spot
	if self != "" {
		out = append(out, spot{key: a.self, dev: self})
	}
	for _, l := range a.shownLANs() {
		for _, id := range onCanvas(l) {
			if id != self {
				out = append(out, spot{key: allKey(l.ID, id), dev: id, lan: l.ID})
			}
		}
	}
	return out
}

// allPlace is where a device first shows in the "全部" view: this computer
// in the middle; the first LAN's devices in an arc to its left, the
// second's to its right, both facing it, so that no link crosses another
// device; the third's under it, between the two. After that, they rest
// where they are put, remembered by LAN and device.
func (a *app) allPlace(d *device) ui.Point {
	cx, cy := a.cw/2, a.ch/2-8
	if d.id == a.self {
		return ui.Point{X: cx, Y: cy}
	}
	lans := a.shownLANs()
	at := slices.IndexFunc(lans, func(l *model.LAN) bool { return l.ID == d.lan })
	var mates []string
	for _, sp := range a.allSpots() {
		if sp.lan == d.lan {
			mates = append(mates, sp.key)
		}
	}
	n := len(mates)
	half := float32(n-1) / 2
	spot := func(i int) ui.Point {
		o := float32(i) - half
		switch {
		case at == 0 || at == 1:
			k := float32(0)
			if half > 0 {
				k = abs32(o) / half
			}
			x := 140 + 46*k
			if at == 1 {
				x = a.cw - 140 - 46*k
			}
			return ui.Point{X: x, Y: cy + o*124}
		case at >= 2:
			below := float32(len(lans) - 2)
			col := float32(at-2) - (below-1)/2
			return ui.Point{X: cx + col*320 + o*140, Y: a.ch - 120}
		}
		return ui.Point{X: cx, Y: cy}
	}
	// A LAN that comes in later finds its region a place clear of the
	// others' and of this computer (scene 3's 加入).
	var r ui.Rect
	for j := range n {
		f := footprint(spot(j))
		if j == 0 {
			r = f
			continue
		}
		x1, y1 := max(r.X+r.W, f.X+f.W), max(r.Y+r.H, f.Y+f.H)
		r.X, r.Y = min(r.X, f.X), min(r.Y, f.Y)
		r.W, r.H = x1-r.X, y1-r.Y
	}
	r = ui.Rect{X: r.X - regionSide, Y: r.Y - regionTop, W: r.W + 2*regionSide, H: r.H + regionTop + regionBottom}
	push := a.clearOfRegions(d.lan, r)
	p := spot(max(0, slices.Index(mates, d.id)))
	return ui.Point{X: p.X + push.X, Y: p.Y + push.Y}
}

// allBounds hold a device inside the canvas while the hand drags it in the
// "全部" view, its name too: above its tile for this computer.
func (a *app) allBounds(d *device, w, h float32) *ui.Rect {
	if d.id == a.self {
		return &ui.Rect{X: 70 - d.base.X, Y: 86 - d.base.Y, W: max(0, w-140), H: max(0, h-40-86)}
	}
	// Room for a region around it, too: its title above, its margins.
	ar := a.restArea(w, h)
	return &ui.Rect{X: ar.X - d.base.X, Y: ar.Y - d.base.Y, W: ar.W, H: ar.H}
}

// foot is the room d takes resting at at: its footprint, or, for this
// computer in the "全部" view, whose name is above its tile, the footprint
// turned upside down.
func (a *app) foot(d *device, at ui.Point) ui.Rect {
	if d != nil && d.id == a.self && a.all() {
		return ui.Rect{X: at.X - 64, Y: at.Y - 76, W: 128, H: 110}
	}
	return footprint(at)
}

// footOf is foot for d, as freeSpot and makeRoom take it.
func (a *app) footOf(d *device) func(ui.Point) ui.Rect {
	return func(at ui.Point) ui.Rect { return a.foot(d, at) }
}

// The room a region keeps around its devices' footprints: its title above
// them.
const (
	regionSide   = 28
	regionTop    = 44
	regionBottom = 18
	regionReach  = 72 // how far past its region another's device may be held out
)

// regionFit is the region a LAN's devices need: their footprints, with room
// for its title above. held counts a device of another's in the hand where
// the hand holds it, as the region follows it; else where it rested.
func (a *app) regionFit(lan model.LANID, held bool) (ui.Rect, bool) {
	var x0, y0, x1, y1 float32
	any := false
	for _, d := range a.devices {
		if d.lan != lan || d.id == a.self || !d.placed() {
			continue
		}
		at := d.at()
		if d.dragging {
			at = d.rest
			if held && !d.mine {
				ro := a.regionOff[lan]
				at = ui.Point{X: d.live.X - d.push.X - ro.X, Y: d.live.Y - d.push.Y - ro.Y}
			}
		}
		f := footprint(at)
		if !any {
			x0, y0, x1, y1, any = f.X, f.Y, f.X+f.W, f.Y+f.H, true
			continue
		}
		x0, y0, x1, y1 = min(x0, f.X), min(y0, f.Y), max(x1, f.X+f.W), max(y1, f.Y+f.H)
	}
	if !any {
		return ui.Rect{}, false
	}
	return ui.Rect{X: x0 - regionSide, Y: y0 - regionTop, W: x1 - x0 + 2*regionSide, H: y1 - y0 + regionTop + regionBottom}, true
}

// regionAt is the LAN whose region holds p, in the canvas, or none.
func (a *app) regionAt(p ui.Point) model.LANID {
	for _, l := range a.shownLANs() {
		r, ok := a.regionRects[l.ID]
		if !ok {
			continue
		}
		o := a.regionOff[l.ID]
		if p.X >= r.X+o.X && p.X <= r.X+o.X+r.W && p.Y >= r.Y+o.Y && p.Y <= r.Y+o.Y+r.H {
			return l.ID
		}
	}
	return ""
}

// regionPaint is how a region is painted in a frame, under the links: the
// fill goes on the canvas's painter, before them, so that the tracks run
// over it into its devices.
type regionPaint struct {
	r                 ui.Rect // in the canvas
	alpha, ring, lift float32
}

// regionIn is how long a region takes to come in.
const regionIn = 420 * time.Millisecond

// regions builds each LAN's region under its devices: a soft grey rounded
// area that wraps them, its title (the members' dots and its name) at its
// top left, "已暂停" at its top right while paused. It follows its devices
// on a Smooth spring; it rings blue while a device held would move into
// it; it dims while another LAN has the focus. Pressed on its own ground,
// the whole region moves, its devices with it, lifted on a shadow.
func (a *app) regions(c *ui.Context, p *palette) {
	if a.regionRects == nil {
		a.regionRects, a.regionRest = map[model.LANID]ui.Rect{}, map[model.LANID]ui.Rect{}
		a.regionOff, a.regionGen = map[model.LANID]ui.Point{}, map[model.LANID]int{}
		a.regionBorn, a.regionLeft = map[model.LANID]time.Time{}, map[model.LANID]time.Time{}
		a.regionLast = map[model.LANID]regionPaint{}
	}
	now := c.Now()
	a.regionPaints = a.regionPaints[:0]
	shown := map[model.LANID]bool{}
	for _, l := range a.shownLANs() {
		lan := l.ID
		r, ok := a.regionFit(lan, true)
		if !ok {
			continue
		}
		shown[lan] = true
		if _, seen := a.regionBorn[lan]; !seen {
			// A region that comes in after the view is laid out fades in
			// from below; those there as it opens are just there.
			born := time.Time{}
			if a.regionsLaid {
				born = now
			}
			a.regionBorn[lan] = born
		}
		delete(a.regionLeft, lan)
		a.regionRects[lan] = r
		if rest, ok := a.regionFit(lan, false); ok {
			a.regionRest[lan] = rest
		}
		off := a.regionOff[lan]
		gen := strconv.Itoa(a.regionGen[lan])
		el := ui.Box(c.Key("region-" + string(lan))).Absolute()
		x := el.AnimateTo("x"+gen, r.X, ui.Smooth)
		y := el.AnimateTo("y"+gen, r.Y, ui.Smooth)
		w := el.AnimateTo("w"+gen, r.W, ui.Smooth)
		h := el.AnimateTo("h"+gen, r.H, ui.Smooth)
		in := float32(1)
		if born := a.regionBorn[lan]; !born.IsZero() {
			if k := float32(now.Sub(born)) / float32(regionIn); k < 1 {
				in = ui.EaseOut(max(0, k))
				c.AnimationFrame()
			}
		}
		rise := (1 - in) * 24
		el.Left(x+off.X).Top(y+off.Y+rise).Size(w, h)
		focus := float32(1)
		if a.focus != "" && a.focus != lan {
			focus = 0.35
		}
		fk := el.AnimateTo("focus", focus, ui.Smooth)
		ring := float32(0)
		if a.dropOver == lan {
			ring = 1
		}
		rk := el.AnimateTo("ring", ring, ui.Animation{Duration: 160 * time.Millisecond})
		lift := float32(0)
		if a.regionHeld == lan {
			lift = 1
		}
		lk := el.AnimateTo("lift", lift, ui.Snappy)
		paint := regionPaint{r: ui.Rect{X: x + off.X, Y: y + off.Y + rise, W: w, H: h}, alpha: fk * in, ring: rk, lift: lk}
		a.regionPaints = append(a.regionPaints, paint)
		a.regionLast[lan] = paint
		if k := fk * in; k < 1 {
			el.Opacity(k)
		}
		el.Cursor(ui.CursorGrab)
		if a.regionHeld == lan {
			el.Cursor(ui.CursorGrabbing)
		}
		if el.Clicked() {
			a.selected, a.more = "", false
		}
		if dx, dy, held := el.Dragged(); held && (dx != 0 || dy != 0) {
			// Held in the canvas, its devices with it.
			o := ui.Point{X: off.X + dx, Y: off.Y + dy}
			o.X = min(max(o.X, -r.X), a.cw-r.X-r.W)
			o.Y = min(max(o.Y, -r.Y), a.ch-r.Y-r.H)
			a.regionOff[lan], a.regionHeld = o, lan
			c.AnimationFrame()
		}
		if _, _, ended := el.DragEnded(); ended {
			a.settleRegion(lan)
		}
		el.Children(func() {
			ui.Row(c).Absolute().Left(16).Top(14).Gap(8).AlignItems(ui.Center).PassThrough().Children(func() {
				ui.Row(c).Children(func() {
					for i, id := range l.Members[:min(3, len(l.Members))] {
						b := ball(c, a.people[string(id)], 12, false).Shadow(0, 0, 0, 1.5, p.well)
						if i > 0 {
							b.Margin(0, 0, 0, -4)
						}
					}
				})
				ui.Text(c, l.Name).FontSize(13).FontWeight(600).TextColor(p.ink2).SingleLine().MaxWidth(max(40, w-100))
			})
			if l.Paused {
				ui.Text(c.Key("paused"), "已暂停").FontSize(12).TextColor(p.ink3).Absolute().Right(16).Top(15).PassThrough().
					Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
			}
		})
	}
	// A region whose LAN went fades where it was.
	for lan, last := range a.regionLast {
		if shown[lan] {
			continue
		}
		left, ok := a.regionLeft[lan]
		if !ok {
			left = now
			a.regionLeft[lan] = left
		}
		k := float32(now.Sub(left)) / float32(vanishFor)
		if k >= 1 {
			delete(a.regionLast, lan)
			delete(a.regionLeft, lan)
			delete(a.regionBorn, lan)
			delete(a.regionRects, lan)
			delete(a.regionRest, lan)
			continue
		}
		last.alpha *= 1 - k
		a.regionPaints = append(a.regionPaints, last)
		c.AnimationFrame()
	}
	a.regionsLaid = a.cw > 0
	a.edgeNoteView(c, p)
}

// paintRegions paints the regions' ground, under the links: a lifted one's
// shadow, the fill, and the ring of one a device would move into.
func (a *app) paintRegions(pt *ui.Painter, r ui.Rect, p *palette) {
	for _, g := range a.regionPaints {
		q := ui.Rect{X: r.X + g.r.X, Y: r.Y + g.r.Y, W: g.r.W, H: g.r.H}
		if g.lift > 0 {
			pt.Shadow(q, 20, 0, 12*g.lift, 32, 0, p.shadow.Alpha(g.lift*g.alpha))
		}
		pt.Fill(q, p.well.Alpha(g.alpha), 20)
		if g.ring > 0 {
			pt.Stroke(q, p.primary.Alpha(g.ring*g.alpha), 20, 1.5)
		}
	}
}

// settleRegion ends a region's move: its devices rest where it took them,
// and it is where they are. Let go over another region or this computer,
// it then slides to the nearest place clear of them.
func (a *app) settleRegion(lan model.LANID) {
	off := a.regionOff[lan]
	for _, d := range a.devices {
		if d.lan == lan && d.id != a.self {
			d.base = ui.Point{X: d.base.X + off.X, Y: d.base.Y + off.Y}
			d.rest = ui.Point{X: d.rest.X + off.X, Y: d.rest.Y + off.Y}
		}
	}
	r := a.regionRects[lan]
	r.X, r.Y = r.X+off.X, r.Y+off.Y
	a.regionRects[lan] = r
	// A new generation of its springs, which start where it is now rather
	// than glide there from where it was.
	a.regionGen[lan]++
	a.regionOff[lan] = ui.Point{}
	a.regionHeld = ""
	if push := a.clearOfRegions(lan, r); push != (ui.Point{}) {
		for _, d := range a.devices {
			if d.lan == lan && d.id != a.self {
				d.off = ui.Point{X: d.off.X + push.X, Y: d.off.Y + push.Y}
				d.flying = true
			}
		}
	}
}

// clearOfRegions is the least a region at r has to move to keep clear of
// the other regions and this computer, inside the canvas.
func (a *app) clearOfRegions(lan model.LANID, r ui.Rect) ui.Point {
	const air = 16
	fits := func(push ui.Point) bool {
		q := ui.Rect{X: r.X + push.X, Y: r.Y + push.Y, W: r.W, H: r.H}
		if q.X < 0 || q.Y < 0 || q.X+q.W > a.cw || q.Y+q.H > a.ch {
			return false
		}
		q = ui.Rect{X: q.X - air, Y: q.Y - air, W: q.W + 2*air, H: q.H + 2*air}
		for other, o := range a.regionRects {
			if other != lan && overlaps(q, o) {
				return false
			}
		}
		if me := a.device(a.self); me != nil && overlaps(q, a.foot(me, me.at())) {
			return false
		}
		return true
	}
	if fits(ui.Point{}) {
		return ui.Point{}
	}
	for rad := float32(8); rad < 900; rad += 8 {
		for i := 0; i < 24; i++ {
			s, co := math.Sincos(float64(i) * math.Pi / 12)
			push := ui.Point{X: rad * float32(co), Y: rad * float32(s)}
			if fits(push) {
				return push
			}
		}
	}
	return ui.Point{}
}

// innerBounds hold another's device inside its region while the hand
// drags it in the "全部" view, its name and all, a little past the region
// as it rested: the region stretches that far with it, and no further.
func (a *app) innerBounds(d *device) (*ui.Rect, bool) {
	r, ok := a.regionRest[d.lan]
	if !ok {
		return nil, false
	}
	x0 := r.X + regionSide + 64 - regionReach
	x1 := r.X + r.W - regionSide - 64 + regionReach
	y0 := r.Y + regionTop + 34 - regionReach
	y1 := r.Y + r.H - regionBottom - 76 + regionReach
	ar := a.restArea(a.cw, a.ch)
	x0, y0 = max(x0, ar.X), max(y0, ar.Y)
	x1, y1 = min(x1, ar.X+ar.W), min(y1, ar.Y+ar.H)
	return &ui.Rect{X: x0 - d.base.X, Y: y0 - d.base.Y, W: max(0, x1-x0), H: max(0, y1-y0)}, true
}

// edgeNote says why another's device stops at its region's edge.
type edgeNote struct {
	lan  model.LANID
	text string
}

// edgeNoteView shows the note under the region of the device that stopped
// at its edge, or over it, near the canvas's bottom.
func (a *app) edgeNoteView(c *ui.Context, p *palette) {
	ui.Box(c.Key("edge-notes")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().Children(func() {
		n := a.edgeNote
		if n == nil {
			return
		}
		r, ok := a.regionRects[n.lan]
		if !ok {
			return
		}
		y := r.Y + r.H + 8
		if y+20 > a.ch {
			y = r.Y - 26
		}
		note := ui.Row(c.Key("note-" + string(n.lan))).Absolute().Left(r.X).Top(y).Width(r.W).Justify(ui.Center).PassThrough()
		note.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: -4}, Exit: &ui.Motion{Y: -4}})
		note.Children(func() { ui.Text(c, n.text).FontSize(12).TextColor(p.ink2).SingleLine() })
	})
}

// droppedInAll is what letting go of a device does in the "全部" view: one
// of mine let go in another LAN's region moves there, and out of every
// region leaves its LAN; anywhere else, and anyone's, it stays, at the
// nearest free spot; let go in a LAN it is in already, it goes back.
func (a *app) droppedInAll(c *ui.Context, d *device, w, h float32) {
	at := d.at()
	to := a.regionAt(at)
	spot := a.freeSpot(at, d, a.footOf(d), w, h)
	stay := func() {
		d.off = ui.Point{X: spot.X - d.base.X, Y: spot.Y - d.base.Y}
		d.flying = true
	}
	switch {
	case d.id == a.self || !d.removable() || to == d.lan:
		stay()
	case to == "":
		a.leaveHere(c, d)
	case a.moveTo(c, d, to, spot):
		stay()
	default:
		d.off = ui.Point{X: d.rest.X - d.base.X, Y: d.rest.Y - d.base.Y}
		d.flying = true
	}
}

// heldInAll follows a device in the hand in the "全部" view: the region one
// of mine would move into rings blue; another's that reaches its region's
// edge says why it stops there, for the rest of the drag.
func (a *app) heldInAll(d *device, live ui.Point, b *ui.Rect) {
	if !d.dragging {
		if a.edgeHit == d.id {
			a.edgeHit = ""
		}
		return
	}
	if d.removable() {
		// Not one it is in already: there, letting go sends it back.
		if to := a.regionAt(ui.Point{X: d.base.X + live.X, Y: d.base.Y + live.Y}); to != d.lan {
			if l := a.snap.LAN(to); l == nil || !l.Has(d.dev) && !l.HasPending(d.dev) {
				a.dropNext = to
			}
		}
		return
	}
	if d.mine || b == nil {
		return
	}
	const e = 0.5
	if live.X <= b.X+e || live.X >= b.X+b.W-e || live.Y <= b.Y+e || live.Y >= b.Y+b.H-e {
		a.edgeHit = d.id
	}
	if a.edgeHit == d.id {
		who := spaced(a.people[d.owner].name)
		a.edgeNext = &edgeNote{lan: d.lan, text: "这是" + who + "的设备，只有" + who + "能移出"}
	}
}

// rekey moves a device on the canvas to the region of lan: its key, and
// what is kept under it, go with it.
func (a *app) rekey(d *device, lan model.LANID) {
	id := allKey(lan, d.dev)
	if b, ok := a.beams[d.id]; ok {
		a.beams[id] = b
		delete(a.beams, d.id)
	}
	delete(a.geos, d.id)
	if a.selected == d.id {
		a.selected = id
	}
	d.elem = d.key()
	d.id, d.lan = id, lan
}

// moveTo moves one of my devices from its LAN to another, to rest at spot
// (DESIGN §6.4: a drag into another region is a move, never a second
// copy): into the new one first, then out of the old; undo does it the
// other way round, and sends it back to where it rested. It reports false,
// and does nothing, when the device is in the other LAN already.
func (a *app) moveTo(c *ui.Context, d *device, to model.LANID, spot ui.Point) bool {
	from, l, was := d.lan, a.snap.LAN(to), a.snap.LAN(d.lan)
	if l == nil || was == nil || l.Has(d.dev) || l.HasPending(d.dev) {
		return false
	}
	rest := d.rest
	// It rests where it lands now: the toast keeps clear of there.
	d.rest = spot
	a.rekey(d, to)
	a.sendAfter(c, model.Op{Kind: model.OpLANAddDevice, LAN: to, Device: d.dev},
		model.Op{Kind: model.OpLANRemoveDevice, LAN: from, Device: d.dev})
	a.notify(c, d, "已把"+short(d.name)+"挪到"+short(l.Name), func() {
		a.sendAfter(c, model.Op{Kind: model.OpLANAddDevice, LAN: from, Device: d.dev},
			model.Op{Kind: model.OpLANRemoveDevice, LAN: to, Device: d.dev})
		a.rekey(d, from)
		d.off, d.flying = ui.Point{X: rest.X - d.base.X, Y: rest.Y - d.base.Y}, true
	})
	return true
}

// leaveHere takes one of my devices out of its LAN, let go out of every
// region; undo brings it back where it was.
func (a *app) leaveHere(c *ui.Context, d *device) {
	l := a.snap.LAN(d.lan)
	if l == nil {
		return
	}
	key, lan, dev, rest := d.id, d.lan, d.dev, d.rest
	a.places.set(a.lan, key, rest)
	// It shrinks away where it was let go: the toast keeps clear of there.
	d.rest = d.at()
	a.send(c, model.Op{Kind: model.OpLANRemoveDevice, LAN: lan, Device: dev})
	a.notify(c, d, "已把"+short(d.name)+"移出"+short(l.Name), func() {
		a.places.set(a.lan, key, rest)
		if x := a.device(key); x != nil {
			// Still shrinking away where it was let go: it grows back, on
			// its way home.
			x.off, x.flying = ui.Point{X: rest.X - x.base.X, Y: rest.Y - x.base.Y}, true
		}
		a.send(c, model.Op{Kind: model.OpLANAddDevice, LAN: lan, Device: dev})
	})
}

// allSummary is the "全部" view's line under its title: how many LANs and
// devices, and which are paused.
func (a *app) allSummary(p *palette) (ui.Color, string) {
	lans := a.shownLANs()
	var paused []string
	for _, l := range lans {
		if l.Paused {
			paused = append(paused, short(l.Name))
		}
	}
	all, up := map[model.DeviceID]bool{}, map[model.DeviceID]bool{}
	for _, d := range a.devices {
		if !d.gone.IsZero() || d.pending {
			continue
		}
		all[d.dev] = true
		if d.path == "self" || linked(d.path) {
			up[d.dev] = true
		}
	}
	switch {
	case !a.snap.Engine:
		return p.ink3, "连不上这台电脑上的 Deneb，正在重试…"
	case len(paused) == len(lans) && len(lans) > 0:
		return p.ink3, "都暂停了，打开开关就恢复"
	case len(paused) > 0:
		return p.ink3, strings.Join(paused, "、") + "已暂停，其他照常"
	case len(up) == len(all):
		return p.ok, fmt.Sprintf("%d 个局域网，%d 台设备都连上了", len(lans), len(all))
	}
	return p.ink3, fmt.Sprintf("%d 个局域网，%d 台设备，%d 台连上了", len(lans), len(all), len(up))
}

// allHeader is the "全部" view's header: its title and line, and the
// engine's notice, if any; no balls and no invite, as no one LAN shows.
func (a *app) allHeader(c *ui.Context, p *palette) {
	ui.Row(c).Height(headerHeight).Padding(22, 24, 0, 28).AlignItems(ui.Start).Gap(16).DragWindow().Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "全部局域网").FontSize(20).FontWeight(600).SingleLine()
			ui.Row(c).Gap(6).Children(func() {
				dot, line := a.summary(p)
				ui.Box(c).Size(7, 7).Radius(999).Background(dot).Shadow(0, 0, 0, 3, dot.Alpha(0.18))
				ui.Text(c, line).FontSize(13).TextColor(p.ink2)
			})
		})
		ui.Spacer(c)
		a.noticePill(c, p)
	})
}

// updateFocus takes the focus from the sidebar's row under the pointer:
// hovering a LAN's row dims the other regions and their links; off the
// rows, everything comes back a moment later, so that crossing from row to
// row moves the focus at once (DESIGN §9 scene 3). Nothing has the focus
// while something is in the hand.
func (a *app) updateFocus(c *ui.Context, over model.LANID) {
	if !a.all() || a.busy() || a.regionHeld != "" {
		a.focus, a.unfocusAt = "", time.Time{}
		return
	}
	if over != "" {
		a.focus, a.unfocusAt = over, time.Time{}
		return
	}
	if a.focus == "" {
		return
	}
	switch {
	case a.unfocusAt.IsZero():
		a.unfocusAt = c.Now().Add(90 * time.Millisecond)
		c.After(90 * time.Millisecond)
	case !c.Now().Before(a.unfocusAt):
		a.focus, a.unfocusAt = "", time.Time{}
	default:
		c.After(a.unfocusAt.Sub(c.Now()))
	}
}

// miniToggle is a LAN's switch in the sidebar: the card's switch, a size
// smaller. It reports whether it was flipped.
func miniToggle(c *ui.Context, p *palette, key, label string, on bool) (flipped bool) {
	v := on
	sw := ui.SwitchBase(c.Key(key), &v).Size(28, 16).Radius(999).Padding(2).FocusRing(false).Label(label)
	k := sw.AnimateTo("knob", map[bool]float32{false: 0, true: 1}[v], ui.Snappy)
	sw.Background(p.line2.Mix(p.primary, k))
	sw.Children(func() {
		ui.Box(c).Size(12, 12).Radius(999).Background(white).Translate(12*k, 0).
			Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.2))
	})
	return v != on
}

// pause pauses a LAN on this computer (on), or resumes it: the engine does
// it, at once, and the sidebar's switch, the canvas and the summary follow.
func (a *app) pause(c *ui.Context, lan model.LANID, on bool) {
	a.send(c, model.Op{Kind: model.OpLANPause, LAN: lan, On: on})
}

// inLANs is how many of my LANs this computer is in.
func (a *app) inLANs() int {
	n := 0
	for _, l := range a.shownLANs() {
		if l.Has(model.DeviceID(a.self)) {
			n++
		}
	}
	return n
}

// cardOnLeft reports whether the card shows at the canvas's left: in the
// "全部" view, for a device in the right half, which the card would cover
// at the right.
func (a *app) cardOnLeft() bool {
	if !a.all() {
		return false
	}
	d := a.device(a.selected)
	return d != nil && d.at().X > a.cw/2+40
}

// inWhich says which LANs this computer is in, for its card in the "全部"
// view: "在周末联机和工作室里".
func (a *app) inWhich() string {
	var names []string
	for _, l := range a.shownLANs() {
		if l.Has(model.DeviceID(a.self)) {
			names = append(names, short(l.Name))
		}
	}
	switch n := len(names); {
	case n == 0:
		return "还没加进局域网"
	case n == 1:
		return "在" + names[0] + "里"
	case n <= 3:
		return "在" + strings.Join(names[:n-1], "、") + "和" + names[n-1] + "里"
	default:
		return "在" + strings.Join(names[:2], "、") + "等 " + strconv.Itoa(n) + " 个局域网里"
	}
}

// membersOf are the people who may use one of my devices, as its card
// lists them: the members of the LAN it shows in.
func (a *app) membersOf(d *device) []string {
	if !a.all() {
		return a.members
	}
	var out []string
	if l := a.lanOf(d); l != nil {
		for _, m := range l.Members {
			out = append(out, string(m))
		}
	}
	return out
}

// dropHint is what letting go of one of my devices held at live does in
// the "全部" view, in a few words, or "" where it just stays.
func (a *app) dropHint(d *device, live ui.Point) string {
	if !a.all() || !d.dragging || !d.removable() {
		return ""
	}
	from := a.snap.LAN(d.lan)
	switch to := a.regionAt(ui.Point{X: d.base.X + live.X, Y: d.base.Y + live.Y}); {
	case from == nil || to == d.lan:
		return ""
	case to == "":
		return "松手，移出" + short(from.Name)
	default:
		l := a.snap.LAN(to)
		if l == nil {
			return ""
		}
		if l.Has(d.dev) || l.HasPending(d.dev) {
			return short(l.Name) + "里已经有了"
		}
		return "松手，挪到" + short(l.Name)
	}
}

// selfInAll reports whether what the hand drags is this computer in the
// "全部" view: nothing steps aside for it, as it goes into no region.
func (a *app) selfInAll(who any) bool {
	me := a.device(a.self)
	return a.all() && me != nil && who == any(me)
}

// selfOverRegion reports whether d is this computer, held over a region in
// the "全部" view.
func (a *app) selfOverRegion(d *device) bool {
	if !a.all() || d.id != a.self || !d.dragging {
		return false
	}
	f := a.foot(d, d.live)
	for _, q := range a.regionRects {
		if overlaps(f, q) {
			return true
		}
	}
	return false
}
