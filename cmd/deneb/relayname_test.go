package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// relayAround is the sample's 周末联机 with this computer below and to the
// left of 阿杰的 NAS, which goes through the relay: the spot between the
// two and a little below, where a relay goes by itself, is under the NAS's
// name, and its track up to the NAS runs through it (the M2 acceptance in
// real mode, item 13).
func relayAround(relay ui.Point, byHand bool) *places {
	at := map[string]ui.Point{
		"mac": {X: 360, Y: 430}, "jnas": {X: 620, Y: 210}, "mia": {X: 140, Y: 130},
		"jdesk": {X: 860, Y: 130}, "game": {X: 140, Y: 470},
	}
	if relay != (ui.Point{}) {
		at[relayKey] = relay
	}
	if byHand {
		at[relayHandKey] = ui.Point{X: 1, Y: 1}
	}
	return &places{at: map[model.LANID]map[string]ui.Point{"weekend": at}}
}

// The relay keeps its tracks off the names, and off every tile but those
// at their ends: placed by itself, or taken from where it last rested by
// itself; where the person put it by hand, it stays.
func TestRelayKeepsTracksOffNames(t *testing.T) {
	show := func(at *places) *app {
		a := newApp(&fixedStore{client.NewDemo().Snapshot()}, at)
		tt := ui.NewTester(a.view, 1180, 740)
		for i := 0; i < 60; i++ {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
		}
		if nas := a.device("jnas"); nas == nil || nas.path != "relay" {
			t.Fatal("阿杰的 NAS does not go through the relay")
		}
		return a
	}
	a := show(relayAround(ui.Point{}, false))
	mac, nas := a.device("mac").at(), a.device("jnas").at()
	naive := a.freeSpot(ui.Point{X: (mac.X + nas.X) / 2, Y: (mac.Y+nas.Y)/2 + 30}, a.relay, a.relayFootprint, a.cw, a.ch)
	if a.relayClear(naive) {
		t.Fatalf("the spot between them, %v, crosses no name: the layout tests nothing", naive)
	}
	if !a.relayClear(a.relay.base) {
		t.Errorf("the relay at %v: a track through it crosses a name or a tile", a.relay.base)
	}
	if d := dist(a.relay.base, naive); d > 200 {
		t.Errorf("the relay went %v from between them", d)
	}
	t.Logf("between them %v; the relay at %v", naive, a.relay.base)

	// Where it last rested by itself, crossing the name: it moves off.
	if a := show(relayAround(naive, false)); a.relay.base == naive || !a.relayClear(a.relay.base) {
		t.Errorf("the relay rested at %v by itself, and shows at %v", naive, a.relay.base)
	}
	// Put there by hand: it stays.
	if a := show(relayAround(naive, true)); a.relay.base != naive {
		t.Errorf("the relay put at %v by hand shows at %v", naive, a.relay.base)
	}
}
