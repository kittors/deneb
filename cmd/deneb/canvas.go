package main

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The canvas: devices as glossy tiles colored by their owner, joined to
// this computer by faint tracks with light flowing in them (design/
// DESIGN.md §3.4–§3.6). Everything on it can be dragged; nothing ever sits
// on top of anything else, even while it moves (§4, §6).

const (
	nameWidth = 128 // the widest a device's name shows: its footprint's width
	tileSize  = 60
	cardWidth = 300
	trayBand  = 104 // the band at the bottom where the tray floats and takes drops
	slotTile  = 40  // a device's tile in the tray
	slotGap   = 10  // the room before each of the tray's slots
)

// The springs. glide carries a device where the hand did not, into the
// tray or out of it: without bounce, so that it lands where it goes. soft
// moves what makes room, the tray's slots and the tray around them, all in
// step. A device steps aside quickly enough to keep up with the hand, and
// waits a moment before stepping back, so that one dropped right on top of
// it is out of the way first.
var (
	glide     = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond}}
	soft      = ui.Animation{Spring: ui.SpringOptions{Duration: 360 * time.Millisecond}}
	stepAside = ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond}}
	stepBack  = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond}, Delay: 140 * time.Millisecond}
	// liftPose is a device in the hand: a little larger, turned a little.
	liftPose = ui.Pose{Scale: 1.06, Rotate: -3, Y: -4}
)

const (
	trackIn  = 480 * time.Millisecond // a joining device's track, drawn out
	trackOut = 280 * time.Millisecond // a leaving one's, drawn back in
)

// footprint is the room a device takes on the canvas: its tile and the two
// lines of its name, so that tiles and names never overlap.
func footprint(at ui.Point) ui.Rect {
	return ui.Rect{X: at.X - 64, Y: at.Y - 34, W: 128, H: 110}
}

// footprint is the room the relay takes with its name, on whichever side
// its name is.
func (r *relayStop) footprint(at ui.Point) ui.Rect {
	// Room for its name: as M1's short ones, or as long as M4's are.
	side := max(104, 32+r.nameW+6)
	across := max(80, r.nameW+12)
	switch r.side {
	case "right":
		return ui.Rect{X: at.X - 26, Y: at.Y - 26, W: 26 + side, H: 52}
	case "above":
		return ui.Rect{X: at.X - across/2, Y: at.Y - 64, W: across, H: 88}
	case "below":
		return ui.Rect{X: at.X - across/2, Y: at.Y - 24, W: across, H: 88}
	}
	return ui.Rect{X: at.X - side, Y: at.Y - 26, W: 26 + side, H: 52}
}

func overlaps(a, b ui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// apart reports whether two footprints keep a little air between them, so
// that one device's name never sits against the next one's tile.
func apart(a, b ui.Rect) bool {
	const air = 12
	return !overlaps(ui.Rect{X: a.X - air, Y: a.Y - air, W: a.W + 2*air, H: a.H + 2*air}, b)
}

func dist(a, b ui.Point) float32 { return float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))) }

func (r *relayStop) at() ui.Point { return ui.Point{X: r.base.X + r.off.X, Y: r.base.Y + r.off.Y} }

// restArea is where things can come to rest: inside the canvas, above
// the tray's band (§6.3). The card is kept clear of where it shows
// (freeSpot), not by the area.
func (a *app) restArea(w, h float32) ui.Rect {
	if a.all() {
		// No tray in the "全部" view: down to the canvas's bottom, with
		// room for a region around what rests at its edges.
		return ui.Rect{X: 96, Y: 80, W: max(0, w-192), H: max(0, h-102-80)}
	}
	// Never a negative size, however small the window: an empty area just
	// holds things where they are.
	return ui.Rect{X: 70, Y: 44, W: max(0, w-140), H: max(0, h-trayBand-70-44)}
}

// dragBounds holds a device inside the canvas as it shows, while the hand
// drags it: above the tray's band, or, for one I can take out of the LAN,
// into it. Under the card is allowed on the way; a drop there lands beside
// it. Every resting device is inside its bounds, which would move it
// otherwise.
func (a *app) dragBounds(base ui.Point, intoTray bool, w, h float32) *ui.Rect {
	return a.dragBoundsFrom(base, intoTray, 44, w, h)
}

// dragBoundsFrom is dragBounds with the tile's middle let up to top.
func (a *app) dragBoundsFrom(base ui.Point, intoTray bool, top, w, h float32) *ui.Rect {
	left, right := float32(70), w-70
	bottom := h - trayBand - 70
	if intoTray {
		bottom = h - 36
	}
	return &ui.Rect{X: left - base.X, Y: top - base.Y, W: max(0, right-left), H: max(0, bottom-top)}
}

// freeSpot returns the point nearest to want where something of the given
// footprint fits: inside the rest area, clear of every other device and the
// relay, by a little air. It searches rings around want, so a drop lands as
// close as it can to where the hand let go.
func (a *app) freeSpot(want ui.Point, self any, fp func(ui.Point) ui.Rect, w, h float32) ui.Point {
	ar := a.restArea(w, h)
	clamp := func(p ui.Point) ui.Point {
		return ui.Point{X: min(max(p.X, ar.X), ar.X+ar.W), Y: min(max(p.Y, ar.Y), ar.Y+ar.H)}
	}
	free := func(p ui.Point) bool {
		r := fp(p)
		// A device that stepped aside for the dragged one counts where it
		// rests and where it stepped to: the drop lands clear of both, so
		// nothing crosses paths as it goes back.
		for _, d := range a.devices {
			if any(d) == self || a.landing == d || !d.placed() {
				continue
			}
			at := d.at()
			if !apart(r, a.foot(d, at)) || !apart(r, a.foot(d, ui.Point{X: at.X + d.push.X, Y: at.Y + d.push.Y})) {
				return false
			}
		}
		if me := a.device(a.self); a.all() && me != nil && self == any(me) {
			// This computer keeps out of every region, by a little air
			// (scene 3).
			for _, q := range a.regionRects {
				if overlaps(ui.Rect{X: r.X - 16, Y: r.Y - 16, W: r.W + 32, H: r.H + 32}, q) {
					return false
				}
			}
		}
		for _, rl := range a.relays {
			if any(rl) == self || !a.counts(rl) {
				continue
			}
			at := rl.at()
			if !apart(r, rl.footprint(at)) || !apart(r, rl.footprint(ui.Point{X: at.X + rl.push.X, Y: at.Y + rl.push.Y})) {
				return false
			}
		}
		// Clear of the card, which comes back when the hand lets go.
		return a.selected == "" || a.cardAt.W == 0 || apart(r, a.cardAt)
	}
	start := clamp(want)
	if free(start) {
		return start
	}
	for radius := float32(16); radius < 520; radius += 16 {
		best, bestD := ui.Point{}, float32(math.MaxFloat32)
		steps := int(radius / 4)
		for i := 0; i < steps; i++ {
			ang := 2 * math.Pi * float64(i) / float64(steps)
			p := clamp(ui.Point{X: start.X + radius*float32(math.Cos(ang)), Y: start.Y + radius*float32(math.Sin(ang))})
			if free(p) {
				if d := dist(p, want); d < bestD {
					best, bestD = p, d
				}
			}
		}
		if bestD < math.MaxFloat32 {
			return best
		}
	}
	return start
}

// makeRoom is how far a resting device steps aside for the one being
// dragged over it, so that two tiles or two names never sit on top of each
// other even for a moment (§4). way is the direction it stepped aside the
// frame before: it keeps it while they overlap, so a device passed over
// slides one way and back, never jumping to the other side as the dragged
// one crosses its middle. The first way out is the shortest one where
// fits finds room: a device is not pushed off the canvas, into the tray,
// under the card, or onto another device.
func makeRoom(still, moving ui.Rect, way ui.Point, fits func(push ui.Point) bool) (push, newWay ui.Point) {
	const air = 12
	scx, scy := still.X+still.W/2, still.Y+still.H/2
	mcx, mcy := moving.X+moving.W/2, moving.Y+moving.H/2
	dx, dy := scx-mcx, scy-mcy
	if !overlaps(ui.Rect{X: still.X - air, Y: still.Y - air, W: still.W + 2*air, H: still.H + 2*air}, moving) {
		return ui.Point{}, ui.Point{}
	}
	clearX := (still.W+moving.W)/2 + air
	clearY := (still.H+moving.H)/2 + air
	// How far along w it must go to clear the moving one.
	along := func(w ui.Point) ui.Point {
		if w.X != 0 {
			return ui.Point{X: w.X * max(0, clearX-w.X*dx)}
		}
		return ui.Point{Y: w.Y * max(0, clearY-w.Y*dy)}
	}
	if way != (ui.Point{}) && fits(along(way)) {
		return along(way), way
	}
	shortest := func(inside bool) (ui.Point, ui.Point, bool) {
		best, bestWay, bestN := ui.Point{}, ui.Point{}, float32(math.MaxFloat32)
		for _, w := range []ui.Point{{X: -1}, {X: 1}, {Y: -1}, {Y: 1}} {
			p := along(w)
			if n := abs32(p.X) + abs32(p.Y); n < bestN && (!inside || fits(p)) {
				best, bestWay, bestN = p, w, n
			}
		}
		return best, bestWay, bestN < math.MaxFloat32
	}
	if p, w, ok := shortest(true); ok {
		return p, w
	}
	p, w, _ := shortest(false) // nowhere inside: the shortest way, all the same
	return p, w
}

func abs32(x float32) float32 { return float32(math.Abs(float64(x))) }

// roomAt returns whether self, resting at `at` with the footprint fp, has
// room when pushed by push: inside the rest area, and off every other
// device and the relay as they rest (the one in the hand aside).
func (a *app) roomAt(self any, at ui.Point, fp func(ui.Point) ui.Rect, w, h float32) func(ui.Point) bool {
	ar := a.restArea(w, h)
	return func(push ui.Point) bool {
		q := ui.Point{X: at.X + push.X, Y: at.Y + push.Y}
		if q.X < ar.X-1 || q.X > ar.X+ar.W+1 || q.Y < ar.Y-1 || q.Y > ar.Y+ar.H+1 {
			return false
		}
		r := fp(q)
		for _, d := range a.devices {
			if any(d) != self && !d.dragging && a.landing != d && d.placed() && overlaps(r, a.foot(d, d.at())) {
				return false
			}
		}
		for _, rl := range a.relays {
			if any(rl) != self && !rl.dragging && a.counts(rl) && overlaps(r, rl.footprint(rl.at())) {
				return false
			}
		}
		return true
	}
}

func stepAnim(push ui.Point) ui.Animation {
	if push == (ui.Point{}) {
		return stepBack
	}
	return stepAside
}

// dragNow is the device the hand drags on the canvas, from the frame
// before, as the tray sees it.
type dragNow struct {
	d         *device
	relay     *relayStop // or a relay (M4)
	removable bool
	near      bool // close to the tray's band
	over      bool // in it
}

func (a *app) dragState(w, h float32) dragNow {
	for _, d := range a.devices {
		if d.dragging {
			return dragNow{d: d, removable: d.removable(), near: a.nearTray(d.live, w, h), over: d.removable() && a.overTray(d.live, w, h)}
		}
	}
	for _, r := range a.relays {
		if r.dragging && a.snap.Can(model.FeatureRelays) {
			out := a.relayRemovable(r)
			return dragNow{relay: r, removable: out, near: a.nearTray(r.live, w, h), over: out && a.overTray(r.live, w, h)}
		}
	}
	return dragNow{}
}

// trayZone is where the tray is in the canvas: where it shows, or, while
// it is down, where it comes up.
func (a *app) trayZone(w, h float32) ui.Rect {
	if a.trayUp && a.trayAt.W > 0 {
		return a.trayAt
	}
	right := a.bottomRight(h)
	return ui.Rect{X: (w-right)/2 - 140, Y: h - 22 - 56, W: 280, H: 56}
}

// overTray reports whether a tile centered at q is over the tray: on it,
// or within a few DIPs of it. Only there does letting go put it in.
func (a *app) overTray(q ui.Point, w, h float32) bool {
	if a.all() {
		return false
	}
	z := a.trayZone(w, h)
	const m = 22
	return q.X >= z.X-m && q.X <= z.X+z.W+m && q.Y >= z.Y-m && q.Y <= z.Y+z.H+m
}

// nearTray reports whether a tile centered at q comes close to the tray.
func (a *app) nearTray(q ui.Point, w, h float32) bool {
	if a.all() {
		return false
	}
	z := a.trayZone(w, h)
	dx := max(z.X-q.X, 0, q.X-(z.X+z.W))
	dy := max(z.Y-q.Y, 0, q.Y-(z.Y+z.H))
	return dx*dx+dy*dy < 170*170 // about when its name comes within reach of the tray
}

// dragged returns the footprint of whatever the hand drags now, from the
// frame before; ok is false when it drags nothing.
func (a *app) dragged() (ui.Rect, any, bool) {
	for _, d := range a.devices {
		if d.dragging {
			return a.foot(d, d.live), d, true
		}
	}
	for _, rl := range a.relays {
		if rl.dragging {
			return rl.footprint(rl.live), rl, true
		}
	}
	if a.held != nil {
		// A device of the tray in the hand, out over the canvas.
		return footprint(a.heldAt), a.held, true
	}
	if a.heldRelay != "" {
		// A relay of the tray, likewise (M4).
		return (&relayStop{side: "below"}).footprint(a.heldRelayAt), a.heldRelay, true
	}
	return ui.Rect{}, nil, false
}

// lifted reports whether a device floats over everything else, the tray,
// the card and the toast too (§6.1): in the hand, or on its way somewhere.
func (a *app) lifted(d *device) bool { return d.dragging || d.flying || d.arriving || a.landing == d }

func (a *app) canvas(c *ui.Context, p *palette) {
	ww, wh := c.Size()
	w, h := ww-sidebarWidth, wh-headerHeight-a.strip
	a.cw, a.ch = w, h
	now := c.Now()
	if a.start.IsZero() {
		a.start = now
	}
	if !a.all() && a.snap.LAN(a.lan) == nil {
		a.emptyCanvas(c, p)
		return
	}
	if l := a.snap.LAN(a.lan); l != nil && l.Waiting {
		a.waitingCanvas(c, p, l)
		return
	}
	a.place()
	a.sweep(c, now)
	box := ui.Box(c).Grow(1)
	back := now.Before(a.unclipUntil)
	if a.aim.d == nil && !back {
		// Clipped, but while one of my devices is held up to the header's
		// balls, or on its way back from there (DESIGN §6.11).
		box.Clip()
	} else if back {
		c.After(a.unclipUntil.Sub(now))
	}
	if b := box.Bounds(); b.W > 0 {
		a.origin = ui.Point{X: b.X, Y: b.Y}
	}
	a.drag = a.dragState(w, h)
	px, py, _ := box.PointerPosition()
	a.takeAim(px, py)
	grow := a.trackGrowth(c, now)
	box.Draw(func(pt *ui.Painter, r ui.Rect) {
		a.paintRegions(pt, r, p)
		a.paintLinks(pt, r, p, grow)
	})
	box.Children(func() {
		// The canvas itself, under everything: a click on it puts the card
		// away.
		if ui.Box(c.Key("floor")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Clicked() {
			a.selected, a.more = "", false
		}
		if a.all() {
			a.regions(c, p)
		}
		// The relays under the devices, but the one in the hand, as the frame
		// began: one the hand picks up in this frame floats from the next.
		shows, up := map[*relayStop]bool{}, map[*relayStop]bool{}
		for _, rl := range slices.Clone(a.relays) {
			// One in flight floats over everything too, as a device does.
			up[rl] = rl.dragging || rl.flight != nil
			if shows[rl] = a.relayShows(c, rl); shows[rl] && !up[rl] {
				a.relayView(c, p, rl, w, h)
			}
		}
		// Which devices float is settled before any is built: one the hand
		// picks up in this frame floats from the next.
		built := map[*device]bool{}
		for _, d := range slices.Clone(a.devices) {
			if !a.lifted(d) {
				built[d] = true
				a.deviceView(c, p, d, w, h)
			}
		}
		a.cardLayer(c, p)
		a.toastView(c, p, w, h, now)
		if !a.all() {
			a.trayView(c, p, w, h, now)
		}
		// Built last, so that they float over everything: the ones in the
		// hand or on their way, and any that came out of the tray just now.
		for _, d := range slices.Clone(a.devices) {
			if !built[d] {
				a.deviceView(c, p, d, w, h)
			}
		}
		for _, rl := range slices.Clone(a.relays) {
			if shows[rl] && up[rl] {
				a.relayView(c, p, rl, w, h)
			}
		}
	})
	// What the devices said for the regions, which show it in the next frame.
	if a.dropOver != a.dropNext || (a.edgeNote == nil) != (a.edgeNext == nil) {
		c.AnimationFrame()
	}
	a.dropOver, a.dropNext = a.dropNext, ""
	a.edgeNote, a.edgeNext = a.edgeNext, nil
	// Where things rest is kept, for the next time the window opens (§6.2).
	a.keepPlaces()
	a.places.save(now)
}

// sweep takes off the canvas the devices that left the LAN elsewhere and
// have shrunk away.
func (a *app) sweep(c *ui.Context, now time.Time) {
	done := func(d *device) bool { return !d.gone.IsZero() && now.Sub(d.gone) >= vanishFor }
	if !slices.ContainsFunc(a.devices, done) {
		return
	}
	for _, d := range a.devices {
		if done(d) {
			delete(a.beams, d.id)
			delete(a.geos, d.id)
		}
	}
	a.devices = slices.DeleteFunc(slices.Clone(a.devices), done)
	c.AnimationFrame()
}

// placed reports whether d rests somewhere on the canvas, for others to
// keep clear of: it has a place, and it is not on its way off the canvas.
func (d *device) placed() bool { return !d.unplaced && d.gone.IsZero() }

// trackGrowth is how much of each link's track shows: all of it, or, as
// its device joins, drawn out from both ends, and as it leaves, drawn back
// in to them.
func (a *app) trackGrowth(c *ui.Context, now time.Time) map[string]float32 {
	grow := map[string]float32{}
	for _, d := range a.devices {
		g := float32(1)
		if !d.joined.IsZero() {
			if k := float32(now.Sub(d.joined)) / float32(trackIn); k < 1 {
				g = ui.EaseInOut(max(0, k))
				c.AnimationFrame()
			}
		}
		if !d.leaving.IsZero() {
			k := float32(now.Sub(d.leaving)) / float32(trackOut)
			g = min(g, 1-ui.EaseInOut(min(1, max(0, k))))
			if k < 1 {
				c.AnimationFrame()
			}
		}
		grow[d.id] = g
	}
	return grow
}

// deviceView builds one device's tile and name. Its key keeps its state
// wherever among its siblings it is built.
func (a *app) deviceView(c *ui.Context, p *palette, d *device, w, h float32) {
	landing := a.landing == d
	first := d.arriving
	// Joined or gone elsewhere, it pops in where it is, or shrinks away
	// there; the hand cannot take one that is going.
	// Until it pops in it stays small and clear, then springs up.
	pop := d.popFirst || !d.popped.IsZero() && c.Now().Before(d.popped)
	d.popFirst = false
	if !d.popped.IsZero() && c.Now().Before(d.popped) {
		c.After(d.popped.Sub(c.Now()))
	}
	going := !d.gone.IsZero()
	col := ui.Column(c.Key(d.key())).Absolute().Left(d.base.X - 70).Top(d.base.Y - tileSize/2).Width(140).AlignItems(ui.Center).Gap(10)
	// No Snap and no throw: a device lands where the hand lets go, as a
	// thing set down on a table; what happens next depends on that point.
	bounds := a.dragBoundsFrom(d.base, d.removable(), a.shareTop(d), w, h)
	if a.all() {
		// In the "全部" view: anywhere in the canvas, or, for another's,
		// inside its region (scene 3).
		bounds = a.allBounds(d, w, h)
		if !d.mine {
			if b, ok := a.innerBounds(d); ok {
				bounds = b
			}
		}
	}
	opts := ui.DraggableOptions{Bounds: bounds, Rigid: true, Animation: ui.Snappy}
	if landing {
		// On its way into the tray, to the slot the tray opened for it,
		// wherever that is now. The hand cannot catch it on the way.
		if a.slotOK {
			d.off = ui.Point{X: a.slot.X - d.base.X, Y: a.slot.Y - d.base.Y}
			d.aimed = true
		}
		opts = ui.DraggableOptions{Animation: glide}
		col.PassThrough()
	}
	if going {
		col.PassThrough()
		if left := vanishFor - c.Now().Sub(d.gone); left > 0 {
			c.After(left)
		}
	}
	col.Draggable(&d.off, opts)
	if _, _, ok := col.DragEnded(); ok && !landing && a.all() {
		a.droppedInAll(c, d, w, h)
	} else if ok && !landing {
		if a.dropOnBall(c, d) {
			// Held up to the header's balls: given to someone, or not, it
			// flies back to where it rested (DESIGN §6.11).
			d.off = ui.Point{X: d.rest.X - d.base.X, Y: d.rest.Y - d.base.Y}
			d.flying = true
			a.unclipUntil = c.Now().Add(800 * time.Millisecond)
		} else if at := d.at(); d.removable() && a.overTray(at, w, h) {
			// Mine, let go over the tray: it flies into the slot the tray
			// opened for it, and leaves the LAN as it lands there.
			a.takeOut(c, d, true, true)
			landing = true
		} else {
			// Anywhere else, the tray's band beside the tray too: it stays,
			// at the nearest free spot. (Someone else's device never
			// reaches the band: its bounds end above it.)
			spot := a.freeSpot(at, d, footprint, w, h)
			d.off = ui.Point{X: spot.X - d.base.X, Y: spot.Y - d.base.Y}
			d.flying = true
		}
	}
	d.dragging = col.Dragging()
	live := col.DragOffset()
	if a.all() {
		a.heldInAll(d, live, bounds)
	}
	switch {
	case first:
		// Its first frame showed it where it came from, as it looked
		// there; from there it glides to its place.
		d.arriving = false
		d.off = ui.Point{}
		c.AnimationFrame()
	case d.dragging:
		d.flying = false
	case d.flying && dist(live, d.off) < 0.75:
		d.flying = false
	}
	if landing && (d.aimed && dist(live, d.off) < 0.75 || c.Now().Sub(d.leaving) > 1200*time.Millisecond) {
		// In its slot, as large as the tray's tiles: the tray takes it.
		a.landed(c, d)
	}
	if !d.dragging && !d.flying && !landing && !first {
		d.rest = d.at()
	}
	// Step aside while something else is dragged over this device.
	push := ui.Point{}
	if fp, who, ok := a.dragged(); ok && who != any(d) && !d.dragging && !d.flying && !landing && !a.selfInAll(who) {
		push, d.way = makeRoom(a.foot(d, d.at()), fp, d.way, a.roomAt(d, d.at(), a.footOf(d), w, h))
	} else {
		d.way = ui.Point{}
	}
	d.push = push
	px := col.AnimateTo("push-x", push.X, stepAnim(push))
	py := col.AnimateTo("push-y", push.Y, stepAnim(push))
	// Its region in the hand takes it along (scene 3).
	ro := ui.Point{}
	if a.all() && d.lan != "" {
		ro = a.regionOff[d.lan]
	}
	// Up over a ball, the tile goes into the ball, wherever in the tile the
	// hand holds it, and back to the hand off it (DESIGN §6.11).
	snap := ui.Point{}
	if d.dragging && a.upHigh(d) {
		snap = a.snapTo(ui.Point{X: a.origin.X + d.base.X + live.X, Y: a.origin.Y + d.base.Y + live.Y})
	}
	sx := col.AnimateTo("snap-x", snap.X, ui.Snappy)
	sy := col.AnimateTo("snap-y", snap.Y, ui.Snappy)
	col.Translate(px+ro.X+sx, py+ro.Y+sy)
	d.live = ui.Point{X: d.base.X + live.X + px + ro.X, Y: d.base.Y + live.Y + py + ro.Y}
	// Dimmed with its region while another LAN has the focus.
	faded := float32(0)
	if a.all() && a.focus != "" && d.lan != a.focus && d.id != a.self {
		faded = 1
	}
	d.faded = col.AnimateTo("faded", faded, ui.Smooth)
	if d.faded > 0 {
		col.Opacity(1 - 0.65*d.faded)
	}
	// Its link is cut while I refuse its owner on this computer.
	cut := float32(0)
	if a.cutOff(d) {
		cut = 1
	}
	d.cut = col.AnimateTo("cut", cut, ui.Animation{Duration: 320 * time.Millisecond})
	if col.Clicked() {
		if d.pending && !d.mine {
			// Waiting for me: a click lets it in (DESIGN §6.7).
			a.approve(c, d, true)
		} else {
			a.selected, a.more = d.id, false
		}
	}
	col.Cursor(ui.CursorGrab)
	if d.dragging {
		col.Cursor(ui.CursorGrabbing)
	}
	// Over the tray, one of mine already looks as it will there: tray
	// sized, upright, its name gone, so that the slot shows under it.
	armed := d.dragging && d.removable() && a.overTray(d.live, w, h)
	// Its link fades as it is held over the tray: let go, and it is cut
	// (§9, scene 5).
	dim := float32(0)
	up := d.dragging && a.upHigh(d)
	// Up in the header, out of the canvas, its link all but goes: letting
	// go there cuts nothing, and a link to the header would cross it all.
	d.up = col.AnimateTo("up", map[bool]float32{false: 0, true: 1}[up], ui.Animation{Duration: 160 * time.Millisecond})
	if armed || landing || a.all() && d.dragging && d.removable() && a.regionAt(d.live) == "" {
		// In the "全部" view, out of every region: letting go takes it out
		// of its LAN.
		dim = 1
	}
	d.dim = col.AnimateTo("dim", dim, ui.Animation{Duration: 160 * time.Millisecond})
	pose := ui.Pose{}
	tileAnim := ui.Snappy
	switch {
	case pop:
		pose = ui.Pose{Scale: 0.5, Fade: 1}
	case going:
		pose, tileAnim = ui.Pose{Scale: 0.6, Fade: 1}, ui.Animation{Duration: vanishFor}
	case first:
		pose = d.arrive
	case landing, armed:
		pose = ui.Pose{Scale: float32(slotTile) / tileSize}
	case up:
		// Up in the header: small, and smaller still over a ball, which
		// shows round it, lit (DESIGN §6.1, §6.11).
		pose = ui.Pose{Scale: a.upScale(), Rotate: liftPose.Rotate}
	case d.dragging:
		pose = liftPose
	case col.Pressed():
		pose = ui.Pose{Scale: 0.96}
	case col.Hovered():
		pose = ui.Pose{Y: -2}
	}
	if !d.approved.IsZero() && c.Now().Sub(d.approved) < 90*time.Millisecond {
		// Let in: it dips, and springs up full (DESIGN §6.7).
		pose, tileAnim = ui.Pose{Scale: 0.9}, ui.Animation{Duration: 90 * time.Millisecond}
		c.After(90*time.Millisecond - c.Now().Sub(d.approved))
	} else if !d.approved.IsZero() && c.Now().Sub(d.approved) < 700*time.Millisecond {
		tileAnim = ui.Bouncy
	}
	col.Children(func() {
		if !d.popped.IsZero() && c.Now().Sub(d.popped) < 700*time.Millisecond && !pop {
			// It springs up as it pops in (DESIGN §6.1: joining lands Bouncy).
			tileAnim = ui.Bouncy
		}
		// Offline, it greys over 300 ms rather than at once.
		gone := float32(0)
		if d.path == "offline" || d.path == "paused" || d.path == "nonet" {
			// Paused, it greys as well (scene 3).
			gone = 1
		}
		off := col.AnimateTo("offline", gone, ui.Animation{Duration: 300 * time.Millisecond})
		waits := float32(0)
		if d.pending || d.turnedDown {
			waits = 1
		}
		wait := col.AnimateTo("pending", waits, ui.Animation{Duration: 220 * time.Millisecond})
		owner := a.people[d.owner].color
		tile := ui.Box(c).Size(tileSize, tileSize)
		tile.AnimatePose(pose, tileAnim)
		tile.Children(func() {
			solid := deviceTileOff(c, p, d.kind, owner, tileSize, a.selected == d.id, off)
			if wait > 0 {
				solid.Opacity(1 - wait)
				pendingTile(c, p, d.kind, owner, tileSize).Absolute().Left(0).Top(0).Opacity(wait).PassThrough()
			}
			a.tileBadges(c, p, d, col.Hovered())
		})
		name := ui.Column(c).AlignItems(ui.Center).Gap(1)
		if a.all() && d.id == a.self {
			// Over its tile in the "全部" view, clear of the links that go
			// down to the regions under it (scene 3).
			name.Absolute().Left(0).Right(0).Bottom(tileSize + 10)
		}
		if first || landing || armed || up || pop || going || a.selfOverRegion(d) {
			// (This computer held over a region in the "全部" view: its name
			// goes, rather than sit on the region's names.)
			name.AnimatePose(ui.Pose{Fade: 1}, ui.Animation{Duration: 120 * time.Millisecond})
		} else {
			name.AnimatePose(ui.Pose{}, ui.Animation{Duration: 220 * time.Millisecond, Delay: 60 * time.Millisecond})
		}
		name.Children(func() {
			// A long name ends in "…" within the device's footprint, so that
			// it never runs under the next device's. A second of its name
			// shows a small grey 2 after it (DESIGN §6.8).
			if d.twin > 1 {
				ui.Row(c).AlignItems(ui.Baseline).Gap(3).MaxWidth(nameWidth).Children(func() {
					ui.Text(c, d.name).FontSize(13).FontWeight(500).SingleLine().Shrink(1)
					ui.Text(c, strconv.Itoa(d.twin)).FontSize(11).FontWeight(500).TextColor(p.ink3).FontFeatures("tnum")
				})
			} else {
				ui.Text(c, d.name).FontSize(13).FontWeight(500).SingleLine().MaxWidth(nameWidth)
			}
			status, ink := a.status(d), p.ink2
			if hint := a.dropHint(d, live); hint != "" {
				// In the hand in the "全部" view, its second line says what
				// letting go does (scene 3).
				status, ink = hint, p.primary
			}
			st := ui.Text(c, status).FontSize(12).TextColor(ink).SingleLine().MaxWidth(nameWidth)
			if !d.using.IsZero() && linked(d.path) {
				// The count goes up every second, its digits of one width.
				st.FontFeatures("tnum")
				c.After(time.Second - a.now().Sub(d.using)%time.Second)
			}
		})
	})
}

// deviceTile is a device's glossy tile in its owner's color: lighter at the
// top, a soft highlight inside its top edge, a spot of light at its top
// left, and a shadow in its own color (§3.4).
func deviceTile(c *ui.Context, p *palette, kind string, col ui.Color, size float32, selected bool) ui.Element {
	return deviceTileOff(c, p, kind, col, size, selected, 0)
}

// deviceTileOff is a device's tile off of the way to how it shows offline:
// grey, its glyph see-through (DESIGN §3.4).
func deviceTileOff(c *ui.Context, p *palette, kind string, col ui.Color, size float32, selected bool, off float32) ui.Element {
	if off > 0 {
		col = col.Mix(offlineGrey, off)
	}
	radius := size * 0.3
	t := ui.Box(c).Size(size, size).Radius(radius).Center().
		LinearGradient(ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{
			{Color: col.Mix(white, 0.2), At: 0}, {Color: col, At: 0.58}, {Color: col.Mix(black, 0.1), At: 1},
		}}).
		InsetShadow(0, 1, 0.5, 0, ui.RGBA(255, 255, 255, 0.45)).
		InsetShadow(0, -2, 6, 0, ui.RGBA(0, 0, 0, 0.12))
	if selected {
		t.Shadow(0, 0, 0, 4, p.bg).Shadow(0, 0, 0, 6, p.primary)
	}
	t.Shadow(0, size*0.13, size*0.3, 0, col.Alpha(0.3)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.1))
	t.Children(func() {
		// A soft spot of light near the top left, fading out well before
		// its box's edge so that it never shows a shape of its own.
		ui.Box(c).Absolute().Left(size*0.16).Top(size*0.06).Size(size*0.46, size*0.2).PassThrough().
			RadialGradient(ui.RadialGradient{Radius: size * 0.2, Stops: []ui.GradientStop{{Color: white.Alpha(0.42), At: 0}, {Color: white.Alpha(0), At: 1}}})
		glyph := white
		if off > 0 {
			glyph = white.Alpha(1 - 0.45*off)
		}
		// Told to be another kind, the new shape fades in over the old one
		// as it fades out (DESIGN §6.9).
		g := ui.Box(c.Key("glyph-" + kind)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().PassThrough()
		g.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
		g.Children(func() { ui.Icon(c, deviceGlyphs[kind]).Size(size/2, size/2).TextColor(glyph) })
	})
	return t
}

func (a *app) relayView(c *ui.Context, p *palette, r *relayStop, w, h float32) {
	r.side = a.relayLabelSide(r)
	if r.flight != nil && a.relayFlying(c, p, r) {
		return
	}
	// Mine, dragged in: the hand can take it into the tray (M4).
	out := a.relayRemovable(r)
	el := ui.Box(c.Key("relay\x00"+string(r.id))).Absolute().Left(r.base.X-20).Top(r.base.Y-20).Size(40, 40)
	el.Draggable(&r.off, ui.DraggableOptions{Bounds: a.dragBounds(r.base, out, w, h), Rigid: true, Animation: ui.Snappy})
	if _, _, ok := el.DragEnded(); ok && out && a.overTray(r.at(), w, h) {
		// Let go over the tray: this LAN no longer uses it (DESIGN §6.12).
		r.live = r.at()
		a.relayOut(c, r.id)
		return
	} else if ok {
		spot := a.freeSpot(r.at(), r, r.footprint, w, h)
		r.off = ui.Point{X: spot.X - r.base.X, Y: spot.Y - r.base.Y}
		// Put there by hand: it comes back there, whatever its tracks cross.
		r.byHand = true
		a.places.set(a.lan, r.handKey(), ui.Point{X: 1, Y: 1})
	}
	live := el.DragOffset()
	r.dragging = el.Dragging()
	push := ui.Point{}
	if fp, who, ok := a.dragged(); ok && who != any(r) && !el.Dragging() {
		push, r.way = makeRoom(r.footprint(r.at()), fp, r.way, a.roomAt(r, r.at(), r.footprint, w, h))
	} else {
		r.way = ui.Point{}
	}
	r.push = push
	px := el.AnimateTo("push-x", push.X, stepAnim(push))
	py := el.AnimateTo("push-y", push.Y, stepAnim(push))
	el.Translate(px, py)
	r.live = ui.Point{X: r.base.X + live.X + px, Y: r.base.Y + live.Y + py}
	el.Cursor(ui.CursorGrab)
	if el.Clicked() && a.snap.Can(model.FeatureRelays) {
		// Its card (M4).
		a.openRelay(r.id)
	}
	// Down, it greys over 300 ms, as an offline device does (M4).
	off := el.AnimateTo("down", map[bool]float32{false: 0, true: 1}[r.down], ui.Animation{Duration: 300 * time.Millisecond})
	// It pops in as a link comes to go through it, and shrinks away as the
	// last one goes (DESIGN scene 1); the hand cannot take one going.
	pop, going := r.popFirst, !r.leftAt.IsZero()
	r.popFirst = false
	popping := !r.poppedAt.IsZero() && c.Now().Sub(r.poppedAt) < 700*time.Millisecond
	anim := ui.Snappy
	pose := ui.Pose{}
	switch {
	case pop:
		pose = ui.Pose{Scale: 0.5, Fade: 1}
	case going:
		pose, anim = ui.Pose{Scale: 0.6, Fade: 1}, ui.Animation{Duration: 240 * time.Millisecond}
		el.PassThrough()
	case el.Dragging():
		pose = ui.Pose{Scale: 1.08}
	case el.Hovered():
		pose = ui.Pose{Y: -2}
	}
	if popping && !pop && !going {
		anim = ui.Bouncy
	}
	el.Children(func() {
		orb := ui.Box(c).Size(40, 40).Radius(999).Center().
			RadialGradient(ui.RadialGradient{Center: ui.Point{X: 0.34, Y: 0.28}, Stops: []ui.GradientStop{
				{Color: p.ballHi, At: 0}, {Color: p.ballMid, At: 0.52}, {Color: p.ballLo, At: 1},
			}}).
			InsetShadow(0, -1.5, 3, 0, ui.RGBA(0, 0, 0, 0.08)).
			Shadow(0, 6, 14, 0, ui.RGBA(9, 9, 11, 0.12)).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.1))
		orb.AnimatePose(pose, anim)
		if off > 0 {
			orb.Opacity(1 - 0.4*off)
		}
		orb.Children(func() { ui.Icon(c, deviceGlyphs["relay"]).Size(18, 18).TextColor(p.ink2.Alpha(1 - 0.5*off)) })
		name := a.relayName(c, p, r, off)
		switch {
		case pop, going:
			name.AnimatePose(ui.Pose{Fade: 1}, ui.Animation{Duration: 120 * time.Millisecond})
		case popping:
			name.AnimatePose(ui.Pose{}, ui.Animation{Duration: 220 * time.Millisecond, Delay: 60 * time.Millisecond})
		}
	})
}

// relayName is a relay's two lines beside its ball: on the side no track
// leaves from (§4); moving to another side, they fade out there and in
// here. off greys them, the relay down.
func (a *app) relayName(c *ui.Context, p *palette, r *relayStop, off float32) ui.Element {
	name := ui.Column(c.Key("name-" + r.side)).Absolute().
		Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
	if r.nameW > 44 {
		// A name longer than M1's "香港" (M4's): as wide as it is.
		name.Width(r.nameW + 6)
	}
	switch r.side {
	case "left":
		name.Right(52).Top(3).AlignItems(ui.End)
	case "right":
		name.Left(52).Top(3).AlignItems(ui.Start)
	case "above":
		name.Attach(ui.AnchorTop, ui.AnchorBottom).Top(-8).AlignItems(ui.Center)
	default:
		name.Attach(ui.AnchorBottom, ui.AnchorTop).Top(8).AlignItems(ui.Center)
	}
	name.Children(func() {
		ui.Text(c, r.name).FontSize(12).FontWeight(500).SingleLine().TextColor(p.ink.Mix(p.ink3, off))
		ui.Text(c, r.sub).FontSize(12).TextColor(p.ink2).SingleLine()
	})
	return name
}

// relayFlying shows r on its way between the tray and the canvas, and
// reports whether it is: it grows to a canvas ball as it comes out, or
// shrinks and fades as it goes in, its name out of sight meanwhile.
func (a *app) relayFlying(c *ui.Context, p *palette, r *relayStop) bool {
	if f := r.flight; f.into && !f.held {
		// Going into the tray: it waits for the line that went through it
		// to fade, so that the line never bends at nothing (scene 1).
		f.held = true
		if hold := a.relayFading(r).Add(-relayFade / 2); hold.After(f.at) {
			f.at = hold
		}
	}
	at, scale, fade, done := r.flight.look(c.Now())
	if done {
		into := r.flight.into
		r.flight = nil
		if into {
			r.shown, r.leftAt = false, time.Time{}
		}
		return into
	}
	c.AnimationFrame()
	r.live = at
	el := ui.Box(c.Key("relay\x00"+string(r.id))).Absolute().Left(r.base.X-20).Top(r.base.Y-20).Size(40, 40).PassThrough()
	el.Translate(at.X-r.base.X, at.Y-r.base.Y)
	el.Children(func() {
		relayOrb(c, p, 40, r.down, 0).Scale(scale, scale).Opacity(1 - fade)
		// Its name fades out as it sets off, and in as it lands.
		k := min(1, float32(c.Now().Sub(r.flight.made))/float32(150*time.Millisecond))
		if !r.flight.into {
			k = 1 - min(1, max(0, float32(c.Now().Sub(r.flight.at)-relayFly+150*time.Millisecond)/float32(150*time.Millisecond)))
		}
		if k < 1 {
			a.relayName(c, p, r, map[bool]float32{false: 0, true: 1}[r.down]).Opacity(1 - k)
		}
	})
	return true
}

// relayFade is how long the relay takes to shrink away once the last link
// through it goes.
const relayFade = 280 * time.Millisecond

// relayShows reports whether the relay is on the canvas: while a link goes
// through it, and as it shrinks away once the last one has gone, or is
// going (DESIGN scene 1: 中转站缩小淡出). One that a link comes back to
// pops in.
func (a *app) relayShows(c *ui.Context, r *relayStop) bool {
	if a.all() {
		return false
	}
	now := c.Now()
	if r.flight != nil {
		// On its way between the tray and the canvas (M4).
		r.shown, r.leftAt = true, time.Time{}
		return true
	}
	if a.relayLinked(r) || a.kept(r) {
		if !r.shown {
			r.popFirst, r.poppedAt = true, now
		}
		r.shown, r.leftAt = true, time.Time{}
		return true
	}
	if !r.shown {
		return false
	}
	// A link that left the relay for a direct path fades out the way it
	// went, through the relay: the relay stays, and shrinks away with the
	// last of that line, so that the line never bends at nothing. Its
	// shrink is quick to show, the line's fade slow to end: it sets off
	// when the line is about a fifth there.
	if hold := a.relayFading(r).Add(-relayFade / 2); now.Before(hold) {
		r.leftAt = time.Time{}
		c.After(hold.Sub(now))
		return true
	}
	if r.leftAt.IsZero() {
		r.leftAt = now
	}
	if left := relayFade - now.Sub(r.leftAt); left > 0 {
		c.After(left)
		return true
	}
	r.shown, r.leftAt = false, time.Time{}
	return false
}

// relayFading is when the last old line through the relay, of a link that
// went direct, has faded out (links.go), or zero if none is fading.
func (a *app) relayFading(r *relayStop) time.Time {
	var end time.Time
	for _, d := range a.devices {
		if d.oldGeo != nil && d.fromPath == "relay" && d.gone.IsZero() && a.stopOf(d.fromRelay) == r {
			if e := d.pathAt.Add(trackIn); e.After(end) {
				end = e
			}
		}
	}
	return end
}

// relayLinked reports whether a link goes through the relay, and stays: one
// on its way off the canvas, or into the tray, takes the relay along.
func (a *app) relayLinked(r *relayStop) bool {
	for _, d := range a.devices {
		if a.via(d, r) && d.gone.IsZero() && a.landing != d && !d.unplaced && !d.pending {
			return true
		}
	}
	return false
}

// relayLabelSide picks the side of the relay farthest from every track
// that leaves it: the one whose direction is least like any track's. It
// keeps the side it has unless another is clearly better, so that the name
// does not flick from side to side while the relay is dragged.
func (a *app) relayLabelSide(stop *relayStop) string {
	r := stop.live
	if stop.live == (ui.Point{}) {
		r = stop.at()
	}
	var ways []ui.Point
	add := func(q ui.Point) {
		dx, dy := q.X-r.X, q.Y-r.Y
		if l := float32(math.Hypot(float64(dx), float64(dy))); l > 1 {
			ways = append(ways, ui.Point{X: dx / l, Y: dy / l})
		}
	}
	// Where things show, or, before the first frame laid them out, rest.
	pos := func(d *device) ui.Point {
		if d.live == (ui.Point{}) {
			return d.at()
		}
		return d.live
	}
	if me := a.device(a.self); me != nil {
		for _, d := range a.devices {
			if a.via(d, stop) {
				add(pos(me))
				add(pos(d))
			}
		}
	}
	sides := []struct {
		name string
		dir  ui.Point
	}{{"left", ui.Point{X: -1}}, {"right", ui.Point{X: 1}}, {"below", ui.Point{Y: 1}}, {"above", ui.Point{Y: -1}}}
	// Where the name would sit on each side, about as large as it is.
	label := map[string]ui.Rect{
		"left":  {X: r.X - 32 - 44, Y: r.Y - 17, W: 44, H: 34},
		"right": {X: r.X + 32, Y: r.Y - 17, W: 44, H: 34},
		"above": {X: r.X - 22, Y: r.Y - 28 - 34, W: 44, H: 34},
		"below": {X: r.X - 22, Y: r.Y + 28, W: 44, H: 34},
	}
	score := map[string]float32{}
	best, bestScore := "left", float32(math.MaxFloat32)
	for _, s := range sides {
		worst := float32(-1)
		for _, w := range ways {
			worst = max(worst, s.dir.X*w.X+s.dir.Y*w.Y)
		}
		// A side where the name would sit on a device or its name is
		// worse than any track.
		for _, d := range a.devices {
			// Where it rests (or where the hand has it): not where it
			// passes on its way in from the tray.
			if a.landing != d && d.placed() && overlaps(label[s.name], footprint(d.at())) {
				worst += 3
				break
			}
		}
		score[s.name] = worst
		if worst < bestScore {
			best, bestScore = s.name, worst
		}
	}
	if cur := stop.side; cur != "" && score[cur] <= bestScore+0.25 {
		return cur
	}
	return best
}

// pendingTile is a device that waits to be let into the LAN: its tile's
// shape in a dashed line of its owner's color, its glyph faint, nothing
// filled (DESIGN §6.7).
func pendingTile(c *ui.Context, p *palette, kind string, owner ui.Color, size float32) ui.Element {
	radius := size * 0.3
	t := ui.Box(c).Size(size, size).Center()
	t.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, radius)
		// Filled with the canvas first, so that it covers the end of a
		// track as a tile does: one drawn back in as it waits again.
		pt.FillPath(&path, p.bg)
		pt.FillPath(&path, owner.Alpha(0.06))
		path.Dash(0, 5, 4)
		pt.StrokePath(&path, 1.5, owner.Alpha(0.75))
	})
	t.Children(func() {
		ui.Icon(c, deviceGlyphs[kind]).Size(size/2, size/2).TextColor(owner.Alpha(0.55))
	})
	return t
}

// roundRect adds a rectangle with round corners to path.
func roundRect(path *ui.Path, x, y, w, h, r float32) {
	const k = 0.5523 // a cubic's reach to draw a quarter circle
	path.MoveTo(x+r, y).LineTo(x+w-r, y).
		CubeTo(x+w-r+r*k, y, x+w, y+r-r*k, x+w, y+r).LineTo(x+w, y+h-r).
		CubeTo(x+w, y+h-r+r*k, x+w-r+r*k, y+h, x+w-r, y+h).LineTo(x+r, y+h).
		CubeTo(x+r-r*k, y+h, x, y+h-r+r*k, x, y+h-r).LineTo(x, y+r).
		CubeTo(x, y+r-r*k, x+r-r*k, y, x+r, y).Close()
}

// tileBadges are the small round buttons at a tile's top right: the
// amber dot of a login of mine that asks to be renewed, which renews it,
// and the "×" of a device that waits, shown as the pointer is on it, which
// turns it away, or takes mine back (DESIGN §6.7, §6.8).
func (a *app) tileBadges(c *ui.Context, p *palette, d *device, hovered bool) {
	if d.mine && d.login.Due(a.now()) && !d.pending && a.snap.Can(model.FeatureRenew) {
		dot := ui.ButtonBase(c.Key("renew")).Absolute().Right(-3).Top(-3).Size(14, 14).Radius(999).
			Background(ui.Hex("#F59E0B")).Border(2, p.bg).FocusRing(false)
		dot.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.4}, Exit: &ui.Motion{Scale: 0.4}})
		dot.AnimatePose(pressPose(dot, 0.85), ui.Snappy)
		if dot.Clicked() {
			a.renew(c, d)
		}
	}
	if d.pending && hovered {
		x := ui.ButtonBase(c.Key("turn-away")).Absolute().Right(-7).Top(-7).Size(20, 20).Radius(999).Center().
			Background(p.bg).Border(1, p.line).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.12)).FocusRing(false)
		x.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.6}, Exit: &ui.Motion{Scale: 0.6}})
		x.AnimatePose(pressPose(x, 0.88), ui.Snappy)
		x.Children(func() { ui.Icon(c, uiGlyphs["close"]).Size(12, 12).TextColor(p.ink2) })
		if x.Clicked() {
			a.approve(c, d, false)
		}
	}
}
