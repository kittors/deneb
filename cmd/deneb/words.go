package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Words that change (the user, 11-18: "文字的切换等等都得有微动效…所有的内
// 容的变化都得有微动效"): a line, a count, the name of a state take one
// another's place one at a time (DESIGN §5 Swap). The shown words fade out
// (swapOut), then the new ones fade in (swapIn); the room they take goes
// from the one's width to the other's meanwhile, so that what is beside
// them moves along rather than jumps. The tray's words, the toast's, a
// device's line and the header's do the same in their own ways
// (swapper, swap.go).

// changes are the window's words that change, by their place's key
// (swapWords).
type changes map[string]*changing

// changing is a place's words as they change: what shows and how opaque
// (its swapper), and when it was last drawn, so that the places gone are
// let go.
type changing struct {
	sw   swapper
	seen time.Time
}

// swapWords draws words that take the last ones' place one at a time, at
// key, in their look; end keeps them to the end of their room (a count at
// a row's end), else to its start. Nothing shows while there are no words,
// nor do they take room: a place's words that come where there were none
// fade in too, at once, so long as it was drawn with none (asked for with
// ""). The first words of a place show at once. Without ch (nil), they show
// as they are.
func swapWords(c *ui.Context, ch changes, key, words string, size float32, weight int, ink ui.Color, end bool) {
	if ch == nil {
		if words != "" {
			ui.Text(c.Key(key), words).FontSize(size).FontWeight(weight).TextColor(ink).SingleLine()
		}
		return
	}
	now := c.Now()
	w := ch[key]
	if w == nil {
		ch.forget(now)
		w = &changing{}
		ch[key] = w
	}
	w.seen = now
	if w.sw.shown == "" && words != "" && !w.sw.in.IsZero() {
		// Nothing shows, so nothing need go first: the new words start
		// fading in at once (an error as it comes, not a beat after).
		w.sw.shown, w.sw.out, w.sw.in = words, time.Time{}, now
	}
	shown, alpha, moving := w.sw.step(words, now)
	if moving {
		c.AnimationFrame()
	}
	if shown == "" && words == "" {
		return
	}
	width := func(s string) float32 {
		x, _ := c.MeasureText(0, ui.Span{Text: s, Size: size, Weight: weight})
		return x
	}
	// Room for the words that show, and, while new ones are on the way, for
	// them too: growing, it is theirs by the time the shown ones are gone;
	// shrinking, it waits for the shown ones to go.
	room := width(shown)
	if shown != words {
		room = max(room, width(words))
	}
	box := ui.Row(c.Key(key)).Shrink(0).AlignItems(ui.Center)
	if end {
		// At a row's end, by what shows of them (inkRoom).
		box.Justify(ui.End).Margin(0, -inkRoom(shown, size), 0, 0)
	}
	box.Width(box.AnimateTo("room", room, ui.Animation{Duration: swapOut}))
	box.Children(func() {
		t := ui.Text(c.Key("words"), shown).FontSize(size).FontWeight(weight).TextColor(ink).SingleLine().Shrink(0)
		if alpha < 1 {
			t.Opacity(alpha)
		}
	})
}

// forget lets go of the places not drawn for a while, once there are many.
func (ch changes) forget(now time.Time) {
	if len(ch) < 128 {
		return
	}
	for k, w := range ch {
		if now.Sub(w.seen) > 10*time.Second {
			delete(ch, k)
		}
	}
}
