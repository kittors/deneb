package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The card steps out of the way while a device is dragged, so that nothing
// is ever dragged over it or under it, and comes back once all is still.
func TestCardStepsAsideWhileDragging(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	if !tt.HasText("公开服务") {
		t.Fatal("the card for this computer is not open")
	}
	mia := a.device("mia")
	from := ui.Point{X: sidebarWidth + mia.at().X, Y: headerHeight + mia.at().Y}
	tt.Press(from.X, from.Y)
	for i := 1; i <= 8; i++ {
		tt.Move(from.X+float32(i)*30, from.Y+float32(i)*12)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(400 * time.Millisecond)
	if tt.HasText("公开服务") {
		t.Error("the card stayed while a device was dragged")
	}
	tt.Release(from.X+240, from.Y+96)
	tt.Advance(1500 * time.Millisecond)
	if !tt.HasText("公开服务") {
		t.Error("the card did not come back after the drop")
	}
	if a.cardAt.W > 0 && overlaps(footprint(mia.at()), a.cardAt) {
		t.Errorf("Mia's MacBook rests under the card: %v and %v", footprint(mia.at()), a.cardAt)
	}
}

// Let go in the tray's band away from the tray, one of my devices stays in
// the LAN: only the tray itself takes it.
func TestBandBesideTheTrayIsNoTray(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	game := a.device("game")
	from := ui.Point{X: sidebarWidth + game.at().X, Y: headerHeight + game.at().Y}
	// The bottom right corner of the canvas, far from the tray.
	drag(tt, from, ui.Point{X: 1180 - 90, Y: 740 - 40})
	tt.Advance(1500 * time.Millisecond)
	if a.device("game") == nil {
		t.Fatal("游戏主机 left the LAN, let go far from the tray")
	}
	if y := game.at().Y; y > a.ch-trayBand-70+0.5 {
		t.Errorf("rests at y %v, in the tray's band", y)
	}
}

// Refusing someone on one device leaves them free on the others.
func TestRefusalIsPerDevice(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	game := a.device("game")
	tt.ClickAt(sidebarWidth+game.at().X, headerHeight+game.at().Y)
	tt.Advance(600 * time.Millisecond)
	r, ok := tt.Find("Mia")
	if !ok {
		t.Fatal("no chip for Mia")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	acceptConfirmation(t, tt, "撤销访问权限")
	tt.Advance(400 * time.Millisecond)
	if !game.refused["mia"] {
		t.Fatal("Mia is not refused on the console")
	}
	if a.device("mac").refused["mia"] {
		t.Error("refusing Mia on the console refused her on this computer too")
	}
}
