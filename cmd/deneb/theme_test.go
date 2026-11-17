package main

import "testing"

// The palette's press is its well 8% toward its ink (theme.go). It is
// written out as a color so that web/tokens/gen can read it; this keeps it
// so when the well or the ink changes.
func TestPressIsTheWellTowardTheInk(t *testing.T) {
	for _, p := range []*palette{&lightPalette, &darkPalette} {
		got, want := p.press, p.well.Mix(p.ink, 0.08)
		if got.R != want.R || got.G != want.G || got.B != want.B || got.A != want.A {
			t.Errorf("dark %v: press is %v, the well 8%% toward the ink is %v", p.dark, got, want)
		}
	}
}
