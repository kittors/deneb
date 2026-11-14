package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A toast stays as long as the pointer rests on it, still (DESIGN §6.1:
// 指针停在上面时不消失), though a pointer at rest sends nothing; it goes
// once the pointer has left.
func TestToastStaysUnderARestingPointer(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	rest := a.device("game").at()
	from := ui.Point{X: sidebarWidth + rest.X, Y: headerHeight + rest.Y}
	// Aim where the tray is, as a hand would: centered under the canvas.
	tray := a.trayZone(a.cw, a.ch)
	drag(tt, from, ui.Point{X: a.origin.X + tray.X + tray.W/2, Y: a.origin.Y + tray.Y + tray.H/2})
	acceptConfirmation(t, tt, "移出局域网")
	tt.Advance(500 * time.Millisecond)
	r, ok := tt.Find("撤销")
	if !ok {
		t.Fatal("no toast")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(10 * time.Second)
	if !tt.HasText("撤销") {
		t.Fatal("the toast went from under the pointer resting on it")
	}
	tt.Move(r.X+r.W/2, 200)
	tt.Advance(3 * time.Second)
	if tt.HasText("撤销") {
		t.Error("the toast stays after the pointer left it")
	}
}
