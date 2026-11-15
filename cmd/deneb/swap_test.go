package main

import (
	"math"
	"testing"
	"time"
)

// A pill's words going back to the one fading out, halfway, go on from how
// opaque it was, and up from there: no jump (DESIGN §6.5).
func TestSwapperBackIsContinuous(t *testing.T) {
	var s swapper
	t0 := time.Unix(1, 0)
	s.step("a", t0) // there at once
	out := t0.Add(time.Second)
	s.step("b", out) // "a" begins to fade out
	for _, part := range []float32{0.1, 0.25, 0.5, 0.75, 0.9} {
		w := s // as it was when "a" began to fade out
		at := out.Add(time.Duration(float32(swapOut) * part))
		_, before, _ := w.step("b", at)
		shown, after, _ := w.step("a", at)
		if shown != "a" || math.Abs(float64(after-before)) > 1e-3 {
			t.Errorf("back at %.0f%% of the fade: %q at %.3f, was %.3f", part*100, shown, after, before)
		}
		if _, next, _ := w.step("a", at.Add(time.Second/60)); next < after {
			t.Errorf("back at %.0f%% of the fade: %.3f, then %.3f a frame on", part*100, after, next)
		}
	}
}
