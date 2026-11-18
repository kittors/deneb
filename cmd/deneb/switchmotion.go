package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The window as the sidebar goes from one page to another
// (design/plan/m7/switch-motion.md). What both pages show stays and moves
// to its new place; what only the old one shows fades out where it is, and
// what only the new one shows fades in, a moment later: one thing at a
// time (DESIGN §6.5). The sidebar's white ground slides to the row chosen.
// Another click on the way takes over from wherever everything is.

// The timing, from the design's tokens (DESIGN §5): out with swapOut, in
// with swapIn once the old is out of the way; what stays moves with the
// Smooth spring.
var (
	// switchOut is how what only the old page has fades out.
	switchOut = ui.Animation{Duration: swapOut}
	// switchIn is how what only the new page has fades in.
	switchIn = ui.Animation{Duration: swapIn, Delay: swapOut}
	// switchBack is how something on its way out comes back, clicked back
	// to before it is gone: from where it got, at once.
	switchBack = ui.Animation{Duration: swapIn}
)

// The waits around those fades, from the same tokens.
const (
	// outFor is how long what fades out with the page before stays (the
	// tray, and the devices and relays only that page had): its fade, and
	// a frame to draw it clear in before it is let go.
	outFor = swapOut + aFrame
	// switchFor is how long a switch goes on, for what comes and goes with
	// the page to fade as the page does: the old page's fade out, the new
	// one's fade in, and a frame for the one that fade ends in.
	switchFor = swapOut + swapIn + aFrame
)

// switchMotion is what the window keeps of a page change on its way.
type switchMotion struct {
	// The sidebar's white ground: navKey is the row it was under in the
	// last frame, navRects where each row was; while pillOn, it travels
	// on its own, under the rows, from one to the other.
	navKey   string
	navRects map[string]ui.Rect
	// rowOn is whether each row was chosen in the frame before, rowAt when
	// that changed: in the frame it does, however many times the frame is
	// built, the row's ground changes at once, the pill taking it over.
	rowOn    map[string]bool
	rowAt    map[string]time.Time
	pillOn   bool
	pillNew  bool // it sets off in this frame, from pillFrom
	pillFrom ui.Rect

	// The header's words and balls, one at a time.
	title  swapper
	stack  swapper
	invite swapper // whether "邀请朋友" shows: "1" or "0"
	// stacks are the balls each view showed, for the one fading out.
	stacks map[string]stackSrc
	// lineShown is the line under the title as it shows, lineBefore the
	// one before it: coming from no words, the dot fades in with them.
	lineShown, lineBefore string

	// The page: the canvas, or My Devices; one fades out, then the other
	// fades in.
	page swapper

	// carry is the canvas as it was before the switch, until the new one
	// is laid out and takes over from it (carryOver).
	carry *canvasWas
	// ghosts are what the old canvas had and the new one has not, fading
	// out where they were.
	ghosts      []*device
	ghostRelays []*relayStop
	// trayGone are the tray's devices of the page before, which fade out
	// in their slots as they close.
	trayGone map[string]*device
	// keys counts the element keys made for devices whose own was taken.
	keys int
	// The canvas's board: "" while it shows devices, else the words it
	// shows instead (boardKind); boardFrom the one before, boardAt when it
	// changed, boardSeen once the first is known.
	board, boardFrom string
	boardAt          time.Time
	boardSeen        bool
	// at is when the canvas last took over from another page's.
	at time.Time
	// regionsIn: the regions come in as the "全部" view shows, by fading
	// where they are, without rising (regionFade, until they have).
	regionsIn  bool
	regionFade map[model.LANID]bool
}

// stackSrc is what the header's balls are built from.
type stackSrc struct {
	lan     *model.LAN
	members []string
	shared  bool
}

// canvasWas is the canvas as the switch found it.
type canvasWas struct {
	devices []*device
	relays  []*relayStop
	geos    map[string]*linkGeo
	beams   map[string]*beamRun
	tray    []*device
	regions map[model.LANID]regionPaint
}

// navSelected is the key of the sidebar's row that is chosen.
func (a *app) navSelected() string {
	switch {
	case a.myDevicesOpen:
		return "我的设备"
	case a.all():
		return "all"
	case a.shared():
		return "共享给我"
	case a.lan != "":
		return "lan-" + string(a.lan)
	}
	return ""
}

// navPill is the sidebar's white ground on its way from the row chosen
// before to the one chosen now, under the rows. At rest, the row chosen
// has the ground itself; the pill is there only on the way, from the
// frame the choice changes until it has arrived.
func (a *app) navPill(c *ui.Context, p *palette, col ui.Element) {
	m := &a.sw
	sel := a.navSelected()
	if m.navRects == nil {
		m.navRects = map[string]ui.Rect{}
	}
	if sel != m.navKey {
		from, okFrom := m.navRects[m.navKey]
		_, okTo := m.navRects[sel]
		switch {
		case m.pillOn && okTo:
			// On its way already: it turns to the new row from where it is.
		case okFrom && okTo && m.navKey != "":
			m.pillOn, m.pillNew, m.pillFrom = true, true, from
		default:
			m.pillOn = false
		}
		m.navKey = sel
	}
	// Under the rows: MyGo paints a parent's absolute children over its
	// children in the flow, so the pill sits in a holder of no height, the
	// first in the flow, and paints with it, before the rows. The column's
	// gap after it is taken back.
	holder := ui.Box(c.Key("nav-pill-holder")).Height(0).Margin(0, 0, -4, 0).PassThrough()
	if !m.pillOn {
		return
	}
	to, ok := m.navRects[sel]
	origin := holder.Bounds()
	if !ok || origin.W == 0 {
		m.pillOn = false
		return
	}
	holder.Children(func() {
		el := ui.Box(c.Key("nav-pill")).Absolute().PassThrough().Radius(10).Background(p.bg).
			Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
		if m.pillNew {
			m.pillNew = false
			el.Jump("x", m.pillFrom.X-origin.X)
			el.Jump("y", m.pillFrom.Y-origin.Y)
			el.Jump("w", m.pillFrom.W)
			el.Jump("h", m.pillFrom.H)
		}
		tx, ty := to.X-origin.X, to.Y-origin.Y
		x := el.AnimateTo("x", tx, ui.Smooth)
		y := el.AnimateTo("y", ty, ui.Smooth)
		w := el.AnimateTo("w", to.W, ui.Smooth)
		h := el.AnimateTo("h", to.H, ui.Smooth)
		el.Left(x).Top(y).Size(w, h)
		if abs32(x-tx) < 0.25 && abs32(y-ty) < 0.25 && abs32(w-to.W) < 0.25 && abs32(h-to.H) < 0.25 {
			// Arrived, to a quarter of a point: the row takes its ground
			// back from the next frame, the same to the pixel.
			m.pillOn = false
			c.AnimationFrame()
		}
	})
}

// rowSeen notes where a row of the sidebar is, and tells whether its
// ground is the pill's now, and whether it was chosen or let go in this
// frame.
func (m *switchMotion) rowSeen(key string, at ui.Rect, on bool, now time.Time) (pillHas, changed bool) {
	if m.navRects == nil {
		m.navRects = map[string]ui.Rect{}
	}
	if m.rowOn == nil {
		m.rowOn, m.rowAt = map[string]bool{}, map[string]time.Time{}
	}
	if at.W > 0 {
		m.navRects[key] = at
	}
	if was, seen := m.rowOn[key]; seen && was != on {
		m.rowAt[key] = now
	}
	m.rowOn[key] = on
	return on && m.pillOn, m.rowAt[key].Equal(now)
}

// pageKey is the page the main column shows.
func (a *app) pageKey() string {
	if a.myDevicesOpen {
		return "mine"
	}
	return "canvas"
}

// pageStep is the page to build in this frame, and how opaque: the one
// shown fades out before the next fades in. As a page comes in, its
// header's words are there at once, with it.
func (a *app) pageStep(c *ui.Context) (string, float32) {
	before := a.sw.page.shown
	shown, alpha, busy := a.sw.page.step(a.pageKey(), c.Now())
	if busy {
		c.AnimationFrame()
	}
	if before != "" && shown != before {
		a.sw.title, a.sw.stack, a.sw.invite, a.lineWords = swapper{}, swapper{}, swapper{}, swapper{}
		a.sw.carry = nil
	}
	return shown, alpha
}

// captureCanvas keeps the canvas as it is, for the switch to take over
// from, if the canvas shows.
func (a *app) captureCanvas() *canvasWas {
	if a.sw.page.shown != "canvas" || a.myDevicesOpen || a.cw == 0 {
		return nil
	}
	w := &canvasWas{devices: a.devices, relays: a.relays, geos: a.geos, beams: a.beams, tray: a.tray, regions: map[model.LANID]regionPaint{}}
	if a.all() {
		for lan, r := range a.regionLast {
			w.regions[lan] = r
		}
	}
	return w
}

// carryOver takes the new canvas over from the old, once it is laid out:
// each device on both moves from where it showed to its place; the old
// one's others fade out where they are; the new one's others fade in.
func (a *app) carryOver(c *ui.Context, w *canvasWas) {
	now := c.Now()
	a.sw.at = now
	type cand struct {
		d     *device
		ghost bool
		used  bool
	}
	var cands []*cand
	for _, d := range w.devices {
		if d.unplaced {
			continue
		}
		cands = append(cands, &cand{d: d})
	}
	for _, g := range a.sw.ghosts {
		cands = append(cands, &cand{d: g, ghost: true})
	}
	// The same device in the same LAN first, everywhere; then the same
	// device from another LAN: so that, in the "全部" view, each region's
	// tile takes over its own LAN's.
	match := func(nd *device, sameLAN bool) *cand {
		for _, k := range cands {
			if !k.used && k.d.dev == nd.dev && (!sameLAN || k.d.lan == nd.lan) {
				return k
			}
		}
		return nil
	}
	picked := map[*device]*cand{}
	for _, same := range []bool{true, false} {
		for _, nd := range a.devices {
			if picked[nd] != nil {
				continue
			}
			if k := match(nd, same); k != nil {
				k.used, picked[nd] = true, k
			}
		}
	}
	taken := map[string]bool{}
	for _, nd := range a.devices {
		if k := picked[nd]; k != nil {
			taken[k.d.key()] = true
		}
	}
	for _, nd := range a.devices {
		k := picked[nd]
		if k == nil {
			nd.fresh = true
			if taken[nd.key()] {
				// Its own key is another's element now: one of its own.
				a.sw.keys++
				nd.elem = nd.id + "\x00" + strconv.Itoa(a.sw.keys)
			}
			continue
		}
		od := k.d
		nd.elem = od.key()
		target := nd.at()
		nd.base = od.base
		nd.off = ui.Point{X: target.X - od.base.X, Y: target.Y - od.base.Y}
		nd.rest = target
		nd.live = od.live
		nd.shifting = dist(od.live, target) > 0.5
		nd.nameUp, nd.nameSet, nd.nameWas = od.nameUp, od.nameSet, od.nameWas
		nd.nameOut, nd.nameIn = od.nameOut, od.nameIn
		nd.line, nd.lineText = od.line, od.lineText
		// Its light runs on as it was.
		run := w.beams[od.id]
		og := w.geos[od.id]
		if k.ghost {
			run, og = od.ghostBeam, od.ghostGeo
		}
		if run != nil {
			a.beams[nd.id] = run
		}
		// Its link drawn another way on this page (through the relay or
		// straight): the old shape fades out with its light, then the new
		// fades in (paintLinks; the 19th batch).
		if og != nil && og.relay != a.viaRelay(nd) {
			nd.wasGeo, nd.reshaped = og, now
			if run != nil {
				copied := *run
				nd.wasBeams = &copied
			}
		}
	}
	// What is left of the old fades out where it is, its link with it.
	var ghosts []*device
	keys := map[string]bool{}
	for _, d := range a.devices {
		keys[d.key()] = true
	}
	for _, k := range cands {
		if k.used || keys[k.d.key()] {
			continue
		}
		g := k.d
		if !k.ghost {
			g.ghost, g.ghostAt = true, now
			g.ghostGeo, g.ghostBeam = w.geos[g.id], w.beams[g.id]
		}
		ghosts = append(ghosts, g)
	}
	a.sw.ghosts = ghosts
	// The relays: on both, each moves to its new place; the old one's
	// others fade out, the new one's others fade in.
	var gr []*relayStop
	used := map[*relayStop]bool{}
	for _, r := range a.relays {
		if !r.shown {
			continue
		}
		for _, o := range append(append([]*relayStop{}, w.relays...), a.sw.ghostRelays...) {
			if o.id == r.id && !used[o] && (o.shown || o.ghost) {
				used[o] = true
				target := r.at()
				r.base = o.base
				r.off = ui.Point{X: target.X - o.base.X, Y: target.Y - o.base.Y}
				r.live = o.live
				r.shifting = dist(o.live, target) > 0.5
				r.matched = true
				break
			}
		}
		if !r.matched {
			r.fresh = true
		}
	}
	for _, o := range append(append([]*relayStop{}, w.relays...), a.sw.ghostRelays...) {
		if used[o] || !(o.shown || o.ghost) {
			continue
		}
		if !o.ghost {
			o.ghost, o.ghostAt = true, now
		}
		gr = append(gr, o)
	}
	a.sw.ghostRelays = gr
	// The tray's devices of the old page fade out in their slots.
	a.sw.trayGone = map[string]*device{}
	for _, d := range w.tray {
		a.sw.trayGone["dev:"+d.id] = d
	}
	// The "全部" view's regions fade where they were as another page
	// shows; coming to it, they fade in where they are.
	if len(w.regions) > 0 && !a.all() {
		if a.regionLast == nil {
			a.regionLast, a.regionLeft = map[model.LANID]regionPaint{}, map[model.LANID]time.Time{}
		}
		for lan, r := range w.regions {
			a.regionLast[lan] = r
			a.regionLeft[lan] = now
		}
	}
	if a.all() && len(w.regions) == 0 {
		a.regionsLaid, a.sw.regionsIn = true, true
	}
	c.AnimationFrame()
}

// sweepGhosts lets go of what has faded out.
func (a *app) sweepGhosts(now time.Time) {
	keep := a.sw.ghosts[:0]
	for _, g := range a.sw.ghosts {
		if now.Sub(g.ghostAt) < outFor || g.vis > 0.001 {
			keep = append(keep, g)
		}
	}
	a.sw.ghosts = keep
	kr := a.sw.ghostRelays[:0]
	for _, r := range a.sw.ghostRelays {
		if now.Sub(r.ghostAt) < outFor || r.vis > 0.001 {
			kr = append(kr, r)
		}
	}
	a.sw.ghostRelays = kr
}

// visibility is how a device or relay element fades in and out with the
// page: in, a moment after the old is out of the way, from nothing; out,
// at once; back, from where it got.
func visibility(el ui.Element, fresh, ghost bool) float32 {
	switch {
	case ghost:
		return el.AnimateTo("vis", 0, switchOut)
	case fresh:
		el.Jump("vis", 0)
		return el.AnimateTo("vis", 1, switchIn)
	}
	return el.AnimateTo("vis", 1, switchBack)
}

// namePlace is where a device's name shows, over its tile (up) or under
// it, and how opaque: going from one to the other, it fades out where it
// was, then in where it goes, one at a time.
func (a *app) namePlace(c *ui.Context, d *device, want bool) (bool, float32) {
	now := c.Now()
	if !d.nameSet {
		d.nameSet, d.nameUp = true, want
		return want, 1
	}
	if want != d.nameUp {
		if d.nameOut.IsZero() {
			d.nameOut = now
		}
		k := frac(now.Sub(d.nameOut), swapOut)
		if k < 1 {
			c.AnimationFrame()
			return d.nameUp, 1 - k
		}
		d.nameUp, d.nameOut, d.nameIn = want, time.Time{}, now
		c.AnimationFrame()
		return want, 0
	}
	if !d.nameOut.IsZero() {
		// Back before it was out: in again from where it got.
		k := frac(now.Sub(d.nameOut), swapOut)
		d.nameOut, d.nameIn = time.Time{}, now.Add(-time.Duration(float32(swapIn)*(1-k)))
	}
	if d.nameIn.IsZero() {
		return d.nameUp, 1
	}
	k := frac(now.Sub(d.nameIn), swapIn)
	if k >= 1 {
		d.nameIn = time.Time{}
		return d.nameUp, 1
	}
	c.AnimationFrame()
	return d.nameUp, ui.EaseOut(k)
}

// regionGhosts fades the "全部" view's regions where they were, as another
// page shows: their ground here, their names as their elements' exit.
func (a *app) regionGhosts(c *ui.Context) {
	a.regionPaints = a.regionPaints[:0]
	now := c.Now()
	for lan, last := range a.regionLast {
		left, ok := a.regionLeft[lan]
		k := float32(1)
		if ok {
			k = frac(now.Sub(left), swapOut)
		}
		if k >= 1 {
			delete(a.regionLast, lan)
			delete(a.regionLeft, lan)
			continue
		}
		last.alpha *= 1 - k
		a.regionPaints = append(a.regionPaints, last)
		c.AnimationFrame()
	}
}

// titleStep is the header's title to show, the LAN it is of (none for a
// view of several), and how opaque: another page's name takes this one's
// place one at a time, its picture with it.
func (a *app) titleStep(c *ui.Context, lan model.LANID, name string) (model.LANID, string, float32) {
	shown, alpha, busy := a.sw.title.step(string(lan)+"\x00"+name, c.Now())
	if busy {
		c.AnimationFrame()
	}
	id, words, _ := strings.Cut(shown, "\x00")
	return model.LANID(id), words, alpha
}

// switching reports whether the canvas took over from another page's just
// now: what comes and goes with the page fades as it does.
func (a *app) switching(now time.Time) bool {
	return !a.sw.at.IsZero() && now.Sub(a.sw.at) < switchFor
}

// lineKey is what tells a device's second line from another: its words
// with the digits left out, so that a count going up is the same line.
func lineKey(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= '0' && r <= '9' {
			b[i] = '#'
		}
	}
	return string(b)
}

// ghostsView builds what only the page before had, fading out where it
// was, under everything else. One the page shows again in the meantime,
// under the same key, steps aside for it: the page's own wins.
func (a *app) ghostsView(c *ui.Context, p *palette, w, h float32) {
	live := map[string]bool{}
	for _, d := range a.devices {
		live[d.key()] = true
	}
	a.sw.ghosts = slices.DeleteFunc(a.sw.ghosts, func(g *device) bool { return live[g.key()] })
	// "全部" shows no relay: there the page before's fade out still, rather
	// than go at once (the 19th batch).
	a.sw.ghostRelays = slices.DeleteFunc(a.sw.ghostRelays, func(g *relayStop) bool { return !a.all() && a.stopOf(g.id) != nil })
	for _, g := range slices.Clone(a.sw.ghosts) {
		a.deviceView(c, p, g, w, h)
	}
	for _, rl := range slices.Clone(a.sw.ghostRelays) {
		a.relayView(c, p, rl, w, h)
	}
}

// boardKind is what the canvas shows instead of devices: "" while it
// shows devices (a LAN's, "全部", the devices given to me); "empty" with
// no LAN at all; "waiting:<lan>" while I wait to be let into the LAN.
func (a *app) boardKind() string {
	l := a.snap.LAN(a.lan)
	switch {
	case !a.all() && l == nil:
		return "empty"
	case l != nil && l.Waiting:
		return "waiting:" + string(l.ID)
	}
	return ""
}

// boardStep notes the board the canvas shows in this frame, and when it
// changed: the first one known is just there.
func (a *app) boardStep(now time.Time, board string) {
	m := &a.sw
	switch {
	case !m.boardSeen:
		m.boardSeen, m.board = true, board
	case board != m.board:
		m.boardFrom, m.board, m.boardAt = m.board, board, now
	}
}

// otherBoard is the canvas showing words instead of devices: what the page
// before had fading out, then the words fading in.
func (a *app) otherBoard(c *ui.Context, p *palette, board string, w, h float32, now time.Time) {
	if was := a.sw.carry; was != nil {
		// No device to take over: all of the page before fades out.
		a.sw.carry = nil
		a.carryOver(c, was)
	}
	a.sweepGhosts(now)
	a.regionGhosts(c)
	box := ui.Box(c).Grow(1).Clip()
	if b := box.Bounds(); b.W > 0 {
		a.origin = ui.Point{X: b.X, Y: b.Y}
	}
	box.Draw(func(pt *ui.Painter, r ui.Rect) {
		a.paintRegions(pt, r, p)
		a.paintLinks(pt, r, p, nil)
	})
	box.Children(func() {
		a.ghostsView(c, p, w, h)
		a.boardOut(c, p, now)
		in := float32(1)
		if !a.sw.boardAt.IsZero() {
			k := frac(now.Sub(a.sw.boardAt)-swapOut, swapIn)
			if k < 1 {
				c.AnimationFrame()
			}
			in = ui.EaseOut(k)
		}
		a.boardContent(c, p, board, in)
		a.toastView(c, p, w, h, now)
		// The tray of the page before goes with it.
		a.trayView(c, p, w, h, now)
	})
}

// boardOut is the board of the page before, fading out where it was.
func (a *app) boardOut(c *ui.Context, p *palette, now time.Time) {
	m := &a.sw
	if m.boardFrom == "" || m.boardAt.IsZero() {
		return
	}
	k := frac(now.Sub(m.boardAt), swapOut)
	if k >= 1 {
		return
	}
	c.AnimationFrame()
	a.boardContent(c, p, m.boardFrom, 1-k)
}

// boardContent builds a board's words over the whole canvas, alpha opaque.
func (a *app) boardContent(c *ui.Context, p *palette, board string, alpha float32) {
	holder := ui.Column(c.Key("board-" + board)).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
	if alpha < 1 {
		holder.Opacity(alpha)
	}
	holder.Children(func() {
		switch {
		case board == "empty":
			a.emptyCanvas(c, p)
		case strings.HasPrefix(board, "waiting:"):
			if l := a.snap.LAN(model.LANID(strings.TrimPrefix(board, "waiting:"))); l != nil {
				a.waitingCanvas(c, p, l)
			}
		}
	})
}

// ghostSelf is this computer as it fades out with the page before, when
// the new page has none: the links of the page before end at it.
func (a *app) ghostSelf() *device {
	for _, g := range a.sw.ghosts {
		if g.id == a.self {
			return g
		}
	}
	return nil
}
