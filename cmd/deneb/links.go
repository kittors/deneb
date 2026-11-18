package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// ---- Links -----------------------------------------------------------------------

// curve is a link's shape: one quadratic from this computer to a device,
// or two through the relay, sampled so that positions go by length.
type curve struct {
	pts []ui.Point
	cum []float32
	mid float32 // where the relay is along it, or -1
}

func quadPts(a, b ui.Point, bend float32, n int, out []ui.Point) []ui.Point {
	dx, dy := b.X-a.X, b.Y-a.Y
	cx, cy := (a.X+b.X)/2-dy*bend, (a.Y+b.Y)/2+dx*bend
	for i := 0; i <= n; i++ {
		t := float32(i) / float32(n)
		s := 1 - t
		out = append(out, ui.Point{X: s*s*a.X + 2*s*t*cx + t*t*b.X, Y: s*s*a.Y + 2*s*t*cy + t*t*b.Y})
	}
	return out
}

func newCurve(a, b ui.Point, via *ui.Point, bend float32) curve {
	var cv curve
	if via != nil {
		cv.pts = quadPts(a, *via, bend, 40, nil)
		cv.pts = quadPts(*via, b, bend, 40, cv.pts[:len(cv.pts)-1])
	} else {
		cv.pts = quadPts(a, b, bend, 48, nil)
	}
	cv.cum = make([]float32, len(cv.pts))
	cv.mid = -1
	for i := 1; i < len(cv.pts); i++ {
		cv.cum[i] = cv.cum[i-1] + dist(cv.pts[i], cv.pts[i-1])
		if via != nil && i == 40 {
			cv.mid = cv.cum[i]
		}
	}
	return cv
}

func (cv curve) length() float32 { return cv.cum[len(cv.cum)-1] }

func (cv curve) at(s float32) ui.Point {
	s = max(0, min(s, cv.length()))
	i := 1
	for i < len(cv.cum)-1 && cv.cum[i] < s {
		i++
	}
	k := (s - cv.cum[i-1]) / max(cv.cum[i]-cv.cum[i-1], 1e-6)
	a, b := cv.pts[i-1], cv.pts[i]
	return ui.Point{X: a.X + (b.X-a.X)*k, Y: a.Y + (b.Y-a.Y)*k}
}

func hash01(s string, salt int) float64 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	h = (h ^ uint32(salt)) * 16777619
	return float64(h%10007) / 10007
}

const (
	beamSpeed = 150 // DIPs a second
	beamTail  = 74
	relayHold = 0.2 // seconds a beam rests in the relay
)

// beamRun is how far each of a link's four beams (two each way) has gone
// in its cycle, in DIPs. The light moves on by beamSpeed each second,
// whatever the link does, so a link that a drag stretches or shortens
// never makes its light jump. (A beam's place used to be the time modulo a
// period that went with the link's length: a drag changed the length every
// frame, and the light leapt about, the more the longer the window had
// been open.)
type beamRun [4]float64

// cycle is the length of one turn of beam i of link id, in DIPs: its run
// along the link and its tail, its rest in the relay, and the gap before
// it sets off again.
func cycle(id string, i int, L float32, viaRelay bool) float64 {
	gap := 0.7 + 1.1*hash01(id, i)
	hold := 0.0
	if viaRelay {
		hold = relayHold
	}
	return float64(L+beamTail) + (hold+gap)*beamSpeed
}

// beamAt places beam i of a link of length L, through the relay at mid
// (or mid < 0), at u along its cycle: its head and tail, by length from
// this computer. ok is false between its runs.
func beamAt(u float64, i int, L, mid float32) (head, tail float32, ok bool) {
	dir := i / 2
	s := float32(u)
	if mid >= 0 {
		m := mid
		if dir == 1 {
			m = L - mid
		}
		hv := float32(relayHold * beamSpeed)
		switch {
		case s < m:
		case s < m+hv:
			s = m
		default:
			s -= hv
		}
	}
	if s-beamTail > L {
		return 0, 0, false
	}
	if dir == 1 {
		return L - s, L - s + beamTail, true
	}
	return s, s - beamTail, true
}

// newRun starts the light of a link: spread along it for the links there
// when the window opens, and, for one just drawn out, setting off from
// both ends at once, the second beam each way half a run later. None of
// the fresh light is somewhere along the link at first: the second waits
// past the end (on a long link half a run before its turn was still on
// the way, and showed in the middle at once; the 19th batch).
func newRun(id string, L float32, viaRelay, fresh bool) *beamRun {
	var r beamRun
	for i := range r {
		total := cycle(id, i, L, viaRelay)
		hold := 0.0
		if viaRelay {
			hold = relayHold * beamSpeed
		}
		switch {
		case fresh && i%2 == 0:
			r[i] = 0
		case fresh:
			r[i] = max(total-float64(L+beamTail)/2, float64(L+beamTail)+hold+1)
		default:
			r[i] = math.Mod((hash01(id, i)+float64(i%2)/2)*total, total)
		}
	}
	return &r
}

// linkGeo is a link's shape, kept while its ends (and the relay it goes
// through) stay where they are: its curve, its track, and the track in
// dots for a link cut. Only the beams change from frame to frame.
type linkGeo struct {
	me, d, via, o     ui.Point
	relay             bool
	cv                curve
	track, dots, wait ui.Path
}

// geo returns the shape of the link to d, made anew only when its ends,
// or the relay it goes through, moved.
func (a *app) geo(d *device, me, o ui.Point) *linkGeo {
	relay := a.viaRelay(d)
	var via ui.Point
	if relay {
		via = a.stopFor(d).live
	}
	g := a.geos[d.id]
	if g != nil && g.me == me && g.d == d.live && g.via == via && g.o == o && g.relay == relay {
		return g
	}
	g = &linkGeo{me: me, d: d.live, via: via, o: o, relay: relay}
	a.geos[d.id] = g
	var vp *ui.Point
	if relay {
		vp = &via
	}
	g.cv = newCurve(me, d.live, vp, bendFor(d.id))
	L := g.cv.length()
	g.cv.pathInto(&g.track, 0, L, o)
	g.cv.pathInto(&g.dots, 0, L, o)
	g.dots.Dash(0, 0.01, 7)
	g.cv.pathInto(&g.wait, 0, L, o)
	return g
}

// viaRelay reports whether d's link is drawn through its relay: it goes
// through one, and the page shows it ("全部" shows none).
func (a *app) viaRelay(d *device) bool {
	return d.path == "relay" && !a.all() && a.stopFor(d) != nil
}

// pathInto draws the part of the curve from s0 to s1 into path, offset by
// o, and returns it: a path reused from frame to frame.
func (cv curve) pathInto(path *ui.Path, s0, s1 float32, o ui.Point) *ui.Path {
	path.Reset()
	n := max(2, int((s1-s0)/4))
	for i := 0; i <= n; i++ {
		q := cv.at(s0 + (s1-s0)*float32(i)/float32(n))
		if i == 0 {
			path.MoveTo(q.X+o.X, q.Y+o.Y)
		} else {
			path.LineTo(q.X+o.X, q.Y+o.Y)
		}
	}
	return path
}

// paintLinks draws every link: the faint track, then the beams of light
// running along it both ways, each a short comet of three strokes (a wide
// soft glow, a narrower one, and a hot core). A link whose owner I refused
// on this computer fades to a dotted line, without light.
func (a *app) paintLinks(pt *ui.Painter, r ui.Rect, p *palette, grow map[string]float32) {
	pt.AnimationFrameRate(60)
	now := pt.Now()
	dt := 0.0
	if !a.beamsAt.IsZero() {
		// At most a few frames' worth: after a stall the light goes on
		// from where it was.
		dt = min(max(now.Sub(a.beamsAt).Seconds(), 0), 0.1)
	}
	a.beamsAt = now
	me := a.device(a.self)
	if me == nil {
		// The page before's links fade out where they were, though the
		// new page has no device (switchmotion.go).
		me = a.ghostSelf()
	}
	if me == nil {
		return
	}
	o := ui.Point{X: r.X, Y: r.Y}
	glow := float32(0.16)
	if p.dark {
		glow = 0.26
	}
	devices := a.devices
	if len(a.sw.ghosts) > 0 {
		// The links of the page before fade with their devices, as they
		// were (switchmotion.go).
		devices = append(append([]*device{}, a.sw.ghosts...), a.devices...)
	}
	for _, d := range devices {
		g, ok := grow[d.id]
		if d.ghost {
			g, ok = 1, true
		}
		// Its path just changed (repath): in the first frame the new way
		// has nothing drawn yet, but what went before still shows (the old
		// way's track and light, the line it waited with), never nothing for
		// a frame (the 19th batch).
		changed := !d.pathAt.IsZero() && d.pathAt.Equal(d.joined) && now.Sub(d.pathAt) < trackIn
		if d == me || !ok || g <= 0 && !changed || d.unplaced || d.pending && d.leaving.IsZero() || d.vis <= 0 {
			continue
		}
		// Dimmed with its region while another LAN has the focus: its light
		// goes on flowing, faintly (scene 3).
		fk := (1 - 0.65*d.faded) * d.vis
		// This computer held up in the header: every link of its all but
		// goes, rather than run up there (DESIGN §6.11).
		fk *= 1 - 0.92*me.up
		// Drawn another way than on the page before (through the relay or
		// straight): the old shape and its light fade out first, then this
		// one fades in, one at a time (the 19th batch).
		if old := d.wasGeo; old != nil {
			t := now.Sub(d.reshaped)
			if t >= swapOut+swapIn {
				d.wasGeo, d.wasBeams = nil, nil
			} else {
				if t < swapOut {
					out := fk * (1 - ui.EaseOut(float32(t)/float32(swapOut)))
					pt.StrokePath(&old.track, 3, p.track.Alpha(out))
					if d.wasBeams != nil {
						a.paintBeams(pt, d, me, old, d.wasBeams, out, glow, dt, o)
					}
				}
				fk *= ui.EaseOut(max(0, float32(t-swapOut)/float32(swapIn)))
			}
		}
		// The shape the link had before its path changed (repath), kept
		// while what was drawn on it goes.
		was := d.oldGeo
		if k := float32(now.Sub(d.pathAt)) / float32(trackIn); was != nil && k >= 1 {
			d.oldGeo, d.oldBeams, was = nil, nil, nil
		} else if was != nil && linked(d.path) && linked(d.fromPath) {
			pt.StrokePath(&was.track, 3, p.track.Alpha(fk*(1-ui.EaseInOut(max(0, k)))))
			// Its light runs on where it was as it fades, rather than go at
			// once.
			if f := float32(now.Sub(d.pathAt)) / float32(fadeTime); d.oldBeams != nil && f < 1 {
				a.paintBeams(pt, d, me, was, d.oldBeams, fk*(1-ui.EaseOut(max(0, f))), glow, dt, o)
			}
		}
		geo := d.ghostGeo
		if !d.ghost || geo == nil {
			geo = a.geo(d, me.live, o)
		}
		cv := geo.cv
		L := cv.length()
		if !linked(d.path) {
			// What it stopped carrying goes along the way it went: through
			// the relay, if it did, not along the line it waits with.
			went := geo
			if was != nil {
				went = was
			}
			a.paintWaiting(pt, geo, went, d, g, fk, p, now, o)
			// Its light fades out as it stops, rather than go at once
			// (switchmotion.go: the LAN's switch, a device gone offline).
			if k := float32(now.Sub(d.pathAt)) / float32(fadeTime); !d.pathAt.IsZero() && k < 1 && linked(d.fromPath) {
				a.paintBeams(pt, d, me, went, a.beamRunOf(d, went), fk*(1-ui.EaseOut(max(0, k))), glow, dt, o)
				pt.AnimationFrameRate(60)
			}
			continue
		}
		if g < 1 {
			// A link that just connected keeps the line it waited with under
			// the track growing out, which fades as the track meets: the
			// link never shows nothing in between (scene 1).
			if !d.pathAt.IsZero() && d.pathAt.Equal(d.joined) && d.fromPath != "" && !linked(d.fromPath) && d.fromPath != "self" {
				// Where it waited: the line it waited with goes from there.
				line := geo
				if was != nil {
					line = was
				}
				a.paintWaitLine(pt, line, d.fromPath, (1-g)*fk, p, now)
			}
			if d.fromPath == "paused" && d.pathAt.Equal(d.joined) {
				// Resumed: the track grows back out of this computer, a spot
				// of light at its end (scene 3).
				h := L * g
				if h > 0.5 {
					pt.StrokePath(cv.pathInto(&a.beamPath, 0, h, o), 3, p.track.Alpha(fk))
				}
				a.paintSpot(pt, cv, me, d, h, L, fk, o)
				continue
			}
			// Drawn out from both ends, meeting in the middle (or back).
			h := L / 2 * g
			if h > 0.5 {
				pt.StrokePath(cv.pathInto(&a.beamPath, 0, h, o), 3, p.track.Alpha(fk))
				pt.StrokePath(cv.pathInto(&a.beamPath, L-h, L, o), 3, p.track.Alpha(fk))
			}
			// A device that pops in or shrinks away where it is leaves its end
			// of the track bare, which a tile in flight covers: the glow at
			// the track's ends comes and goes with the track, rather than
			// show before the device or stay after it.
			glow := fk
			if !d.gone.IsZero() || !d.popped.IsZero() && pt.Now().Sub(d.popped) < trackIn {
				glow = min(1, 3*g) * fk
			}
			for _, s := range [2]float32{h, L - h} {
				q := cv.at(s)
				col := a.people[me.owner].color.Mix(a.people[d.owner].color, s/max(L, 1))
				hot := col.Mix(white, 0.5)
				if glow < 1 {
					hot = hot.Alpha(glow)
				}
				pt.FillRadialGradient(ui.Rect{X: q.X + o.X - 7, Y: q.Y + o.Y - 7, W: 14, H: 14},
					ui.RadialGradient{Stops: []ui.GradientStop{{Color: hot, At: 0}, {Color: col.Alpha(0), At: 1}}}, 7)
			}
			continue
		}
		// Drawn out from both ends, its two spots of light met in the
		// middle: they fade there as its light sets off from the ends,
		// rather than go at once (the 19th batch). (Grown out of this
		// computer, its one spot ended under the device.)
		if !d.joined.IsZero() && !(d.fromPath == "paused" && d.pathAt.Equal(d.joined)) {
			if k := float32(now.Sub(d.joined)-trackIn) / float32(fadeTime); k >= 0 && k < 1 {
				spot := fk * (1 - ui.EaseOut(k))
				a.paintSpot(pt, cv, me, d, L/2, L, spot, o)
				a.paintSpot(pt, cv, me, d, L/2, L, spot, o)
			}
		}
		// Held over the tray, it fades, to say that letting go cuts it;
		// cut, it is a dotted line.
		if live := fk * (1 - 0.6*d.dim) * (1 - 0.92*d.up) * (1 - d.cut); live > 0 {
			pt.StrokePath(&geo.track, 3, p.track.Alpha(live))
		}
		if d.cut > 0 {
			pt.StrokePath(&geo.dots, 3, p.track.Alpha(fk*min(1, 2.2*d.cut)))
		}
		light := fk * (1 - 0.75*d.dim) * (1 - d.up) * (1 - d.cut)
		a.paintBeams(pt, d, me, geo, a.beamRunOf(d, geo), light, glow, dt, o)
		if k := float32(now.Sub(d.closedAt)) / float32(closedRun); d.closed != "" && k >= 0 && k < 1 {
			a.paintClosed(pt, cv, L, k, fk*(1-0.65*d.dim), glow, o)
		}
	}
}

// closedRun is how long the red light of a use cut runs, its ring
// included; closedSay how long the device's second line says so.
const (
	closedRun = 1100 * time.Millisecond
	closedSay = 4 * time.Second
)

// paintClosed is the red light of a use of this computer's service cut
// just now, k of the way through (scene 5): a short run of it from the
// device's end of the link to this computer's door, where it spreads into
// a small ring and is gone.
func (a *app) paintClosed(pt *ui.Painter, cv curve, L, k, alpha, glow float32, o ui.Point) {
	red := ui.Hex("#EF4444")
	door := min(L, tileSize/2+6)
	const run = 0.7 // of the time it runs; the ring after
	if k < run {
		head := L - (L-door)*ui.EaseInOut(k/run)
		tail := min(L, head+44)
		if tail-head >= 1 {
			hp, tp := cv.at(head), cv.at(tail)
			angle := float32(math.Atan2(float64(hp.X-tp.X), float64(-(hp.Y-tp.Y))) * 180 / math.Pi)
			light := func(k int, a float32, hot bool) ui.LinearGradient {
				end := red.Alpha(a * alpha)
				if hot {
					end = red.Mix(white, 0.45).Alpha(alpha)
				}
				return ui.LinearGradient{Angle: angle, Stops: []ui.GradientStop{{Color: red.Alpha(0), At: 0}, {Color: red.Alpha(a * alpha * 0.85), At: 0.7}, {Color: end, At: 1}}}
			}
			path := cv.pathInto(&a.beamPath, head, tail, o)
			pt.StrokePathGradient(path, 9, light(0, glow*0.9, false))
			pt.StrokePathGradient(path, 5, light(1, min(1, glow*2), false))
			pt.StrokePathGradient(path, 2, light(2, 1, true))
		}
	}
	if k > run-0.08 {
		r := min(1, (k-(run-0.08))/(1-run+0.08))
		q := cv.at(door)
		rad := 5 + 15*ui.EaseOut(r)
		pt.Stroke(ui.Rect{X: q.X + o.X - rad, Y: q.Y + o.Y - rad, W: 2 * rad, H: 2 * rad}, red.Alpha((1-r)*alpha), rad, 2)
	}
}

// paintWaiting draws the link to a device this computer does not reach: a
// dotted line, flowing slowly toward the device while it is being connected
// to (正在连接…), and a still, dashed one while it cannot be (连不上, 不在线,
// 已暂停) (DESIGN §3.5, scene 1). g draws it out and back in as the device
// comes and goes; a link that just stopped carrying light fades its track
// out as the line fades in, its track along went, the way it went (through
// the relay, if it did; the line goes straight).
func (a *app) paintWaiting(pt *ui.Painter, geo, went *linkGeo, d *device, g, fk float32, p *palette, now time.Time, o ui.Point) {
	alpha := g * fk
	if k := float32(now.Sub(d.pathAt)) / float32(trackIn); d.path == "paused" && !d.pathAt.IsZero() && k < 1 && linked(d.fromPath) {
		// Paused: the track goes back into this computer, carrying a spot
		// of light home, and the dots stay where it was (scene 3).
		e := ui.EaseInOut(max(0, k))
		L := went.cv.length()
		h := L * (1 - e)
		pt.StrokePath(went.cv.pathInto(&a.beamPath, 0, h, o), 3, p.track.Alpha(alpha))
		if me := a.device(a.self); me != nil {
			a.paintSpot(pt, went.cv, me, d, h, L, alpha, o)
		}
		a.paintWaitLine(pt, geo, d.path, alpha*e, p, now)
		return
	}
	if k := float32(now.Sub(d.pathAt)) / float32(waitFade); !d.pathAt.IsZero() && k < 1 && linked(d.fromPath) {
		pt.StrokePath(&went.track, 3, p.track.Alpha(fk*(1-k)))
		alpha *= max(0, k)
	}
	a.paintWaitLine(pt, geo, d.path, alpha, p, now)
}

// paintSpot draws the spot of light at s along a link's track: the colors
// of its two ends mixed as far along as it is.
func (a *app) paintSpot(pt *ui.Painter, cv curve, me, d *device, s, L, alpha float32, o ui.Point) {
	q := cv.at(s)
	col := a.people[me.owner].color.Mix(a.people[d.owner].color, s/max(L, 1))
	hot := col.Mix(white, 0.5).Alpha(alpha)
	pt.FillRadialGradient(ui.Rect{X: q.X + o.X - 7, Y: q.Y + o.Y - 7, W: 14, H: 14},
		ui.RadialGradient{Stops: []ui.GradientStop{{Color: hot, At: 0}, {Color: col.Alpha(0), At: 1}}}, 7)
}

// paintWaitLine draws the line of a link waiting as path says, alpha of the
// way in.
func (a *app) paintWaitLine(pt *ui.Painter, geo *linkGeo, path string, alpha float32, p *palette, now time.Time) {
	if alpha <= 0 {
		return
	}
	if path == "connecting" {
		// 14 DIPs a second, from this computer to the device.
		phase := float32(math.Mod(now.Sub(a.start).Seconds()*14, 7))
		pt.StrokePath(geo.wait.Dash(-phase, 0.01, 7), 3, p.dots.Alpha(alpha))
		return
	}
	pt.StrokePath(geo.wait.Dash(0, 5, 6), 2, p.dots.Alpha(0.75*alpha))
}

// waitFade is how long a track that stops carrying light takes to fade to
// the line of a link that waits.
const waitFade = 240 * time.Millisecond

// bendFor curves each link a little, the same way every frame.
func bendFor(id string) float32 {
	switch id {
	case "mia":
		return -0.1
	case "jdesk", "game":
		return 0.1
	case "jnas":
		return 0.06
	}
	return 0.08
}

// beamRunOf is the light of d's link on geo, kept from frame to frame: its
// own, or a ghost's, started anew when it has none.
func (a *app) beamRunOf(d *device, geo *linkGeo) *beamRun {
	run := a.beams[d.id]
	if d.ghost {
		run = d.ghostBeam
	}
	if run == nil {
		run = newRun(d.id, geo.cv.length(), geo.relay, !d.joined.IsZero())
		if d.ghost {
			d.ghostBeam = run
		} else {
			a.beams[d.id] = run
		}
	}
	return run
}

// paintBeams draws the light run along a device's link on geo, light as
// strong as it is, and moves it on by dt.
func (a *app) paintBeams(pt *ui.Painter, d, me *device, geo *linkGeo, run *beamRun, light, glow float32, dt float64, o ui.Point) {
	cv := geo.cv
	L := cv.length()
	ca, cb := a.people[me.owner].color, a.people[d.owner].color
	for i := range run {
		run[i] = math.Mod(run[i]+dt*beamSpeed, cycle(d.id, i, L, geo.relay))
		if light <= 0 {
			continue
		}
		head, tail, ok := beamAt(run[i], i, L, cv.mid)
		if !ok {
			continue
		}
		s0, s1 := max(0, min(head, tail)), min(L, max(head, tail))
		if s1-s0 < 1 {
			continue
		}
		hp, tp := cv.at(max(0, min(L, head))), cv.at(max(0, min(L, tail)))
		col := ca.Mix(cb, max(0, min(1, head/L)))
		angle := float32(math.Atan2(float64(hp.X-tp.X), float64(-(hp.Y-tp.Y))) * 180 / math.Pi)
		// The painter copies a gradient's stops as it draws, so the three
		// strokes fill the same buffers in turn, frame after frame.
		beam := func(k int, alpha float32, hot bool) ui.LinearGradient {
			alpha *= light
			end := col.Alpha(alpha)
			if hot {
				end = col.Mix(white, 0.55).Alpha(light)
			}
			st := &a.beamStops[k]
			*st = [3]ui.GradientStop{{Color: col.Alpha(0), At: 0}, {Color: col.Alpha(alpha * 0.85), At: 0.75}, {Color: end, At: 1}}
			return ui.LinearGradient{Angle: angle, Stops: st[:]}
		}
		// One path for the three strokes: the painter flattens it as each
		// stroke is drawn, so it is reused for the next beam.
		path := cv.pathInto(&a.beamPath, s0, s1, o)
		pt.StrokePathGradient(path, 9, beam(0, glow*0.5, false))
		pt.StrokePathGradient(path, 5, beam(1, glow, false))
		pt.StrokePathGradient(path, 2, beam(2, 1, true))
	}
}
