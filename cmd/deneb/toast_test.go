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
	drag(tt, from, ui.Point{X: from.X + 150, Y: 740 - 40})
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
