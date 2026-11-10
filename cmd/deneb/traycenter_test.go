package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The tray stays centered under the canvas while a card is open: at rest,
// and while the device whose card was open is dragged toward it (user,
// 2026-11-08: after clicking a device and dragging it, the tray showed up
// left of center, as if the hidden card still took its room).
func TestTrayStaysCenteredWithCardOpen(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	if a.selected == "" {
		t.Fatal("no card is open at the start")
	}
	centered := func(when string) {
		t.Helper()
		if a.trayAt.W == 0 {
			t.Fatalf("%s: no tray", when)
		}
		if d := a.trayAt.X + a.trayAt.W/2 - a.cw/2; d < -2 || d > 2 {
			t.Errorf("%s: the tray sits %.0f DIPs off the canvas's middle (tray %v, canvas %.0f wide)", when, d, a.trayAt, a.cw)
		}
	}
	centered("at rest, card open")

	game := a.device("game")
	if game == nil {
		t.Fatal("no 我的游戏主机 on the canvas")
	}
	from := ui.Point{X: a.origin.X + game.at().X, Y: a.origin.Y + game.at().Y}
	tt.Press(from.X, from.Y)
	tt.Release(from.X, from.Y)
	tt.Advance(600 * time.Millisecond)
	if a.selected != "game" {
		t.Fatalf("clicking 我的游戏主机 opened %q's card", a.selected)
	}
	centered("at rest, its card open")

	to := ui.Point{X: a.origin.X + a.cw/2 - 200, Y: a.origin.Y + a.ch - 140}
	tt.Press(from.X, from.Y)
	for i := 1; i <= 12; i++ {
		k := float32(i) / 12
		tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(500 * time.Millisecond)
	if !a.busy() {
		t.Fatal("the drag did not take")
	}
	centered("while it is dragged toward the tray")
	tt.Release(from.X, from.Y)
	tt.Advance(1500 * time.Millisecond)
}
