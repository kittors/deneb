package main

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The light on a link keeps its place as a drag stretches and shortens the
// link: from one frame to the next a beam's head moves on by the light's
// own run, and, for one coming from the far end, by as far as that end
// moved; never more. (It used to leap about, the more the longer the
// window had been open.)
func TestLightKeepsItsPlaceAsALinkStretches(t *testing.T) {
	const dt = 1.0 / 60
	L := float32(300)
	run := newRun("mia", L, false, false)
	var prev [4]float32
	var seen [4]bool
	for f := 0; f < 60*60; f++ { // a minute of a hand dragging to and fro
		was := L
		L = max(40, L+float32(6*math.Sin(float64(f)/11)))
		for i := range run {
			run[i] = math.Mod(run[i]+dt*beamSpeed, cycle("mia", i, L, false))
			head, _, ok := beamAt(run[i], i, L, -1)
			if ok && seen[i] {
				if d := abs32(head - prev[i]); d > dt*beamSpeed+abs32(L-was)+0.01 {
					t.Fatalf("frame %d: beam %d jumped %.1f DIPs", f, i, d)
				}
			}
			prev[i], seen[i] = head, ok
		}
	}
}

// Dropping one of my devices over the tray takes it out of the LAN as it
// lands there; the toast's undo puts it back where it rested.
func TestDropIntoTrayLeaves(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	game := a.device("game")
	rest := game.at()
	from := ui.Point{X: sidebarWidth + rest.X, Y: headerHeight + rest.Y}
	// Aim where the tray is, as a hand would: centered under the canvas.
	tray := a.trayZone(a.cw, a.ch)
	drag(tt, from, ui.Point{X: a.origin.X + tray.X + tray.W/2, Y: a.origin.Y + tray.Y + tray.H/2})
	acceptConfirmation(t, tt, "移出局域网")
	tt.Advance(1500 * time.Millisecond)
	if a.device("game") != nil || !slices.Contains(a.tray, game) {
		t.Fatalf("游戏主机 did not go into the tray: in the LAN %v, tray holds %d", a.device("game") != nil, len(a.tray))
	}
	r, ok := tt.Find("撤销")
	if !ok {
		t.Fatal("no undo")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(1500 * time.Millisecond)
	if a.device("game") == nil {
		t.Fatal("undo did not bring it back")
	}
	if d := dist(game.at(), rest); d > 1 {
		t.Errorf("back at %v, %v from where it rested", game.at(), d)
	}
}

// Someone else's device never reaches the tray: it stops above it, and
// stays in the LAN.
func TestOthersDeviceStaysAboveTheTray(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	jd := a.device("jdesk")
	from := ui.Point{X: sidebarWidth + jd.at().X, Y: headerHeight + jd.at().Y}
	drag(tt, from, ui.Point{X: from.X, Y: 740 - 40})
	tt.Advance(1500 * time.Millisecond)
	if a.device("jdesk") == nil {
		t.Fatal("阿杰的台式机 left the LAN")
	}
	if y := jd.at().Y; y > a.ch-trayBand-70+0.5 {
		t.Errorf("rests at y %v, in the tray's band", y)
	}
}

// Every link's track shows: a faint line, but a line. (Its color's alpha
// was once multiplied in twice, and the tracks all but vanished.)
func TestTracksShow(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	img := tt.Image()
	me := a.device("mac")
	for _, id := range []string{"mia", "jdesk", "game"} {
		d := a.device(id)
		cv := newCurve(me.live, d.live, nil, bendFor(id))
		// The darkest pixel around a point of the track that no tile or
		// name covers.
		q := cv.at(cv.length() * 0.4)
		x, y := int(q.X+a.origin.X), int(q.Y+a.origin.Y)
		darkest := 255
		for dy := -3; dy <= 3; dy++ {
			for dx := -3; dx <= 3; dx++ {
				c := img.RGBAAt(x+dx, y+dy)
				darkest = min(darkest, (int(c.R)+int(c.G)+int(c.B))/3)
			}
		}
		if 255-darkest < 12 {
			t.Errorf("the track to %s hardly shows: darkest %d near (%d, %d)", id, darkest, x, y)
		}
	}
}
