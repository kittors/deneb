package client

import (
	"testing"
	"time"

	"github.com/kittors/deneb/client/model"
)

// timeline feeds the debouncer a device's reported paths at the times given
// and checks what shows at others: what the view would draw.
type timeline struct {
	t     *testing.T
	d     debouncer
	start time.Time
}

func newTimeline(t *testing.T) *timeline {
	return &timeline{t: t, start: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
}

func (tl *timeline) at(s float64) time.Time {
	return tl.start.Add(time.Duration(s * float64(time.Second)))
}

func (tl *timeline) report(s float64, k model.PathKind) {
	tl.d.report("dev", model.Path{Kind: k}, tl.at(s))
}

func (tl *timeline) shows(s float64, want model.PathKind) {
	tl.t.Helper()
	paths, _ := tl.d.at(tl.at(s))
	if got := paths["dev"].Kind; got != want {
		tl.t.Errorf("at %.2fs shows %s, want %s", s, got, want)
	}
}

// A device just reached shows 正在连接… for its first 2 seconds, through
// what the PoC's startup test went through (the relay at 0.12 s, direct at
// 0.63 s), then the path it has: never the relay in between.
func TestFirstSecondsShowConnecting(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathConnecting)
	tl.report(0.12, model.PathRelay)
	tl.shows(0.5, model.PathConnecting)
	tl.report(0.63, model.PathDirect)
	tl.shows(1.99, model.PathConnecting)
	tl.shows(2, model.PathDirect)
	tl.shows(30, model.PathDirect)
}

// A direct path that drops is replaced by the relay about 2.4 seconds later
// (multi-lan-tech §6.2): the link goes from direct to the relay and never
// shows that it could not be reached in between.
func TestDirectToRelayNeverShowsLost(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathDirect)
	tl.shows(3, model.PathDirect)
	tl.report(10, model.PathUnreachable)
	tl.shows(11, model.PathDirect)
	tl.shows(12.3, model.PathDirect)
	tl.report(12.35, model.PathRelay)
	tl.shows(12.35, model.PathRelay) // gone from direct for 2.35 s by now
	tl.shows(20, model.PathRelay)
}

// A path that flips and flips back within 2 seconds never shows.
func TestFlickerDoesNotShow(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathDirect)
	tl.shows(2, model.PathDirect)
	for i := 0; i < 6; i++ {
		s := 3 + float64(i)
		tl.report(s, model.PathRelay)
		tl.shows(s+0.5, model.PathDirect)
		tl.report(s+0.9, model.PathDirect)
	}
	tl.shows(12, model.PathDirect)
	// Kept up, the relay shows once it has lasted 2 seconds.
	tl.report(20, model.PathRelay)
	tl.shows(21.9, model.PathDirect)
	tl.shows(22, model.PathRelay)
}

// A connected device that cannot be reached at all shows it after 6
// seconds; one that is back is connected to anew, 正在连接… first.
func TestLostAndBack(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathDirect)
	tl.report(5, model.PathOffline)
	tl.shows(10.9, model.PathDirect)
	tl.shows(11, model.PathOffline)
	tl.report(20, model.PathConnecting)
	tl.report(20.3, model.PathDirect)
	tl.shows(21, model.PathConnecting)
	tl.shows(22, model.PathDirect)
}

// What a person did shows at once.
func TestRefusalShowsAtOnce(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathDirect)
	tl.shows(3, model.PathDirect)
	tl.report(4, model.PathRefused)
	tl.shows(4, model.PathRefused)
	tl.report(9, model.PathDirect)
	tl.shows(9.5, model.PathConnecting)
	tl.shows(11, model.PathDirect)
}

// A device connected long before the store first looks (the window opens on
// peers connected for hours) shows its path at once; one reported connected
// a moment ago still gets the rest of its first seconds.
func TestConnectedBeforeTheStoreLooked(t *testing.T) {
	tl := newTimeline(t)
	tl.d.report("dev", model.Path{Kind: model.PathDirect, Since: tl.at(-3600)}, tl.at(0))
	tl.shows(0, model.PathDirect)
	tl.d.report("new", model.Path{Kind: model.PathRelay, Since: tl.at(-0.5)}, tl.at(0))
	if paths, _ := tl.d.at(tl.at(1.4)); paths["new"].Kind != model.PathConnecting {
		t.Errorf("a device reached 1.9 s ago shows %s", paths["new"].Kind)
	}
	if paths, _ := tl.d.at(tl.at(1.5)); paths["new"].Kind != model.PathRelay {
		t.Errorf("a device reached 2 s ago shows %s", paths["new"].Kind)
	}
}

// The debouncer says when to look again, so that a store can show what
// settles without a new report.
func TestWake(t *testing.T) {
	tl := newTimeline(t)
	tl.report(0, model.PathConnecting)
	if _, next := tl.d.at(tl.at(0.5)); !next.Equal(tl.at(2)) {
		t.Errorf("wake at %v, want the end of the first seconds", next.Sub(tl.start))
	}
	tl.report(1, model.PathDirect)
	tl.d.at(tl.at(2))
	tl.report(5, model.PathRelay)
	if _, next := tl.d.at(tl.at(5)); !next.Equal(tl.at(7)) {
		t.Errorf("wake at %v, want 2 s after the relay was reported", next.Sub(tl.start))
	}
	tl.report(5.5, model.PathDirect)
	if _, next := tl.d.at(tl.at(5.5)); !next.IsZero() {
		t.Errorf("wake at %v with nothing to wait for", next.Sub(tl.start))
	}
}

// Devices the engine no longer reaches are forgotten, and one reached again
// starts with its first seconds.
func TestReplaceForgets(t *testing.T) {
	var d debouncer
	t0 := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	d.replace(map[model.DeviceID]model.Path{"a": {Kind: model.PathDirect}, "b": {Kind: model.PathDirect}}, t0)
	d.at(t0.Add(3 * time.Second))
	d.replace(map[model.DeviceID]model.Path{"a": {Kind: model.PathDirect}}, t0.Add(4*time.Second))
	paths, _ := d.at(t0.Add(4 * time.Second))
	if _, ok := paths["b"]; ok {
		t.Error("b is still shown after the engine stopped reaching it")
	}
	if paths["a"].Kind != model.PathDirect {
		t.Errorf("a shows %s", paths["a"].Kind)
	}
	d.replace(map[model.DeviceID]model.Path{"a": {Kind: model.PathDirect}, "b": {Kind: model.PathDirect}}, t0.Add(5*time.Second))
	if paths, _ := d.at(t0.Add(5 * time.Second)); paths["b"].Kind != model.PathConnecting {
		t.Errorf("b, back, shows %s, want its first seconds", paths["b"].Kind)
	}
}
