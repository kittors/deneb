package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A device dragged in from the tray joins the LAN where it was let go.
func TestTrayDragJoins(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	r, ok := tt.Find("拖出来，加进局域网")
	if !ok {
		t.Fatal("no tray")
	}
	drag(tt, ui.Point{X: r.X + r.W + 34, Y: r.Y + r.H/2}, ui.Point{X: 360, Y: 330})
	tt.Advance(time.Second)
	st := a.device("studio")
	if st == nil {
		t.Fatalf("工作室主机 did not join; tray holds %d", len(a.tray))
	}
	if d := dist(st.at(), ui.Point{X: 360 - sidebarWidth, Y: 330 - headerHeight}); d > 40 {
		t.Errorf("joined at %v, %v from where it was let go", st.at(), d)
	}
}
