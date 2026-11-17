package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// One thing at a time (DESIGN §6.5): the tray's words and the toast's
// content change without two of them showing at once, the shown one fading
// out before the next fades in (swapOut, swapIn: theme.go); the pill they
// are in stretches to the next one with the tray's spring as it comes.

// swapper is what a pill shows, by key, as it changes from one to the next.
type swapper struct {
	shown string
	from  float32   // how opaque the shown one was when it began to fade out
	out   time.Time // when the shown one began to fade out; zero: it has not
	in    time.Time // when the shown one began to fade in
}

// step moves s toward want at now: what to show, how opaque, and whether
// it still moves (frames to come).
func (s *swapper) step(want string, now time.Time) (string, float32, bool) {
	if s.in.IsZero() {
		// The first: there at once.
		s.shown, s.in = want, now.Add(-swapIn)
	}
	if want != s.shown {
		if s.out.IsZero() {
			s.from, s.out = s.alpha(now), now
		}
		if a := s.from * (1 - frac(now.Sub(s.out), swapOut)); a > 0 {
			return s.shown, a, true
		}
		s.shown, s.out, s.in = want, time.Time{}, now
		return s.shown, 0, true
	}
	if !s.out.IsZero() {
		// Back to the one fading out: it fades in again from where it got.
		// Its fade-in goes by EaseOut, 1-(1-t)³, so its clock is set back to
		// the t that gives that much, 1-∛(1-a), not to a: no jump.
		a := s.from * (1 - frac(now.Sub(s.out), swapOut))
		t := 1 - math.Cbrt(1-float64(a))
		s.in, s.out = now.Add(-time.Duration(float64(swapIn)*t)), time.Time{}
	}
	a := s.alpha(now)
	return s.shown, a, a < 1
}

// hold keeps the shown one from fading in yet: the room for it is not
// wide enough, and it would show cut off. It fades in once let go.
func (s *swapper) hold(now time.Time) {
	if s.out.IsZero() {
		s.in = now
	}
}

// alpha is how opaque the shown one is at now, fading in.
func (s *swapper) alpha(now time.Time) float32 {
	if !s.out.IsZero() {
		return s.from * (1 - frac(now.Sub(s.out), swapOut))
	}
	return ui.EaseOut(frac(now.Sub(s.in), swapIn))
}

// frac is how far d is into all, from 0 to 1.
func frac(d, all time.Duration) float32 {
	switch {
	case d <= 0:
		return 0
	case d >= all:
		return 1
	}
	return float32(d) / float32(all)
}

// kidLook is how a slot of the tray that is going looks while it closes.
type kidLook int

const (
	lookWell  kidLook = iota // a device's slot: the empty well
	lookRound                // a relay's: the round well
	lookJoin                 // an install command's: the dashed ring
	lookSlot                 // the slot opened for one coming in
	lookRule                 // the line between devices and relays
	lookAdd                  // 添加设备
)

// trayKids is what the tray showed in the frame before, by section, so
// that a slot that goes closes where it was and one that comes opens, with
// the tray's spring (trayKid).
type trayKids struct {
	prev, cur map[string][]string
	looks     map[string]kidLook
	// landed: a device that has just landed in the slot opened for it. Its
	// own slot is there at once and that one gone at once: one becomes the
	// other where they are.
	landed string
}

// kid is one slot of a section in the order it shows: gone, still closing
// where it was; fresh, there since this frame.
type kid struct {
	key         string
	gone, fresh bool
}

// order merges the slots a section has now with those it showed, each gone
// one in its place, and starts this frame's record of the section.
func (t *trayKids) order(section string, now []string, looks map[string]kidLook) []kid {
	if t.cur == nil {
		t.cur, t.looks = map[string][]string{}, map[string]kidLook{}
	}
	for k, l := range looks {
		t.looks[k] = l
	}
	present := map[string]bool{}
	for _, k := range now {
		present[k] = true
	}
	was := map[string]bool{}
	for _, k := range t.prev[section] {
		was[k] = true
	}
	var out []kid
	i := 0
	for _, pk := range t.prev[section] {
		if !present[pk] {
			out = append(out, kid{key: pk, gone: true})
			continue
		}
		for i < len(now) {
			k := now[i]
			i++
			out = append(out, kid{key: k, fresh: !was[k]})
			if k == pk {
				break
			}
		}
	}
	for ; i < len(now); i++ {
		out = append(out, kid{key: now[i], fresh: !was[now[i]]})
	}
	return out
}

// shown records that key showed in section this frame.
func (t *trayKids) shown(section, key string) { t.cur[section] = append(t.cur[section], key) }

// next makes this frame's record the one the next frame goes by; away
// forgets all, for a tray going away or gone.
func (t *trayKids) next(away bool) {
	t.prev, t.cur = t.cur, nil
	if away {
		t.prev = nil
	}
}
