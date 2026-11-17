package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// What the window shows while it waits (DESIGN §5, design/components.md
// 进行中): on the thing pressed, in its place, and only past busyDelay: a
// ring turning, of the color of the words it stands for.

// busy times a piece of work's "under way". Begun, it shows past busyDelay;
// once it has, it stays at least busyHold, the work done or not, so that it
// never blinks.
type busy struct {
	since, shown time.Time
	running      bool
}

// begin starts the work at now; end has it done.
func (b *busy) begin(now time.Time) { *b = busy{since: now, running: true} }
func (b *busy) end()                { b.running = false }

// follow has b follow work the window sees but does not start itself (a
// check of the update begun at start, the store bringing numbers anew):
// it begins at the first frame running says so, and ends at the first
// that does not.
func (b *busy) follow(now time.Time, running bool) {
	switch {
	case running && b.since.IsZero():
		b.begin(now)
	default:
		b.running = running
	}
}

// on reports whether to show the work under way at now. It has the frame
// built anew when that will change.
func (b *busy) on(c *ui.Context, now time.Time) bool {
	switch {
	case b.since.IsZero():
		return false
	case b.running:
		if left := busyDelay - now.Sub(b.since); left > 0 {
			c.After(left)
			return false
		}
		if b.shown.IsZero() {
			b.shown = now
		}
		return true
	case b.shown.IsZero():
		*b = busy{}
		return false
	}
	if left := busyHold - now.Sub(b.shown); left > 0 {
		c.After(left)
		return true
	}
	*b = busy{}
	return false
}

// working reports whether the work runs, or its ring still shows at now
// (held busyHold): the thing pressed keeps its look meanwhile, and takes
// no second click. It needs no on: a button not built takes none either.
func (b *busy) working(now time.Time) bool {
	return b.running || !b.shown.IsZero() && now.Sub(b.shown) < busyHold
}

// underWay reads a button's work, if it is given one: whether it shows
// under way (on), and whether it is working.
func underWay(c *ui.Context, work []*busy) (ring, working bool) {
	if len(work) == 0 || work[0] == nil {
		return false, false
	}
	now := c.Now()
	return work[0].on(c, now), work[0].working(now)
}

// busyRing is the ring turning while work is under way, size across, in
// col on a track of col, faint. Beside words of 12 or 13 it is 12; in a
// button, 16.
func busyRing(c *ui.Context, size float32, col ui.Color, now time.Time) ui.Element {
	c.AnimationFrame()
	turn := float32(now.UnixNano()%int64(spinTime)) / float32(spinTime) * 360
	s := ui.Box(c).Size(size, size).Shrink(0)
	stroke := size / 8
	s.Draw(func(pt *ui.Painter, r ui.Rect) {
		radius := r.W/2 - stroke*0.75
		var path ui.Path
		path.Circle(r.X+r.W/2, r.Y+r.H/2, radius)
		pt.StrokePath(&path, stroke, col.Alpha(0.25))
		round := 2 * math.Pi * float64(radius)
		path.Dash(0, float32(round*0.3), float32(round))
		pt.StrokePath(&path, stroke, col)
	})
	s.Rotate(turn)
	return s
}

// busyLabel holds a button's words, one element: while ring (a busy's on),
// they fade out, then a ring of their color, size across, fades in where
// they were, the button keeping its width: one thing at a time (§6.5).
// Off, the ring goes and the words come again the same way.
func busyLabel(c *ui.Context, ring bool, ink ui.Color, size float32, words func()) {
	target := float32(0)
	if ring {
		target = 1
	}
	holder := ui.Box(c.Key("busy-label")).AlignItems(ui.Center).Justify(ui.Center)
	k := holder.AnimateTo("busy", target, ui.Animation{Duration: swapOut + swapIn})
	split := float32(swapOut) / float32(swapOut+swapIn)
	out := min(k/split, 1)
	in := max((k-split)/(1-split), 0)
	holder.Children(func() {
		ui.Box(c).Opacity(1 - out).Children(words)
		if in > 0 {
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Opacity(in).PassThrough().
				Children(func() { busyRing(c, size, ink, c.Now()) })
		}
	})
}

// busyValue is a row's value while its work is under way: a ring of 12
// before the words of 12, both in ink.
func busyValue(c *ui.Context, words string, ink ui.Color, now time.Time) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		busyRing(c, 12, ink, now)
		ui.Text(c, words).FontSize(12).TextColor(ink).SingleLine()
	})
}

// waitRing and waitRoom are a page's waiting line's ring, across, and the
// room between it and the words (waitingWords): the line, centered, is
// the two of them.
const (
	waitRing = 12
	waitRoom = 8
)

// waitingWords is a whole page's waiting, under the mark (the starting
// page, the curtain while logging out): a ring before words of 13, both
// ink2, as the starting page's line is.
func waitingWords(c *ui.Context, p *palette, words string, now time.Time) {
	ui.Row(c).Gap(waitRoom).AlignItems(ui.Center).Children(func() {
		busyRing(c, waitRing, p.ink2, now)
		ui.Text(c, words).FontSize(13).TextColor(p.ink2).SingleLine()
	})
}
