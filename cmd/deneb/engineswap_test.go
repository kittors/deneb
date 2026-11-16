package main

import (
	"context"
	"testing"
	"time"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestHoldThroughSwap: while the engine is changed for the one shipped, the
// window shows the picture it had (engine-self-update.md §5): through the
// engine going away and coming back without its LANs; the LAN's line says
// "正在更新网卡…" only past 10 s; each device keeps its line until the new
// engine connects it, 10 s at most; past 60 s gone, what is shows.
func TestHoldThroughSwap(t *testing.T) {
	const build, nas = model.DeviceID("dev-build"), model.DeviceID("dev-nas")
	held := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 7,
		Paths: map[model.DeviceID]model.Path{build: {Kind: model.PathDirect}, nas: {Kind: model.PathRelay}}}
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	a := &app{base: held, clock: t0}
	a.beginSwap(t0)

	at := func(d time.Duration) { a.clock = t0.Add(d) }
	// The old engine serves a while longer: what it says shows, and is the
	// picture to keep.
	at(200 * time.Millisecond)
	still := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 8, Paths: held.Paths}
	if got := a.holdThroughSwap(still); got != still {
		t.Fatal("the old engine's own snapshot did not show")
	}
	a.saidSwap()
	// Gone: its last picture stays.
	at(time.Second)
	gone := &model.Snapshot{Version: 8}
	if got := a.holdThroughSwap(gone); got != still {
		t.Fatal("the picture did not stay with the engine gone")
	}
	at(5 * time.Second)
	if _, ok := a.swapLine(); ok {
		t.Error("a line before 10 s")
	}
	at(12 * time.Second)
	if line, ok := a.swapLine(); !ok || line != "正在更新网卡…" {
		t.Errorf("line at 11 s gone: %q %v", line, ok)
	}
	// Back, without the LANs yet: still the picture.
	at(13 * time.Second)
	if got := a.holdThroughSwap(&model.Snapshot{Engine: true, LoggedIn: true}); got != still {
		t.Fatal("the picture did not stay before the LANs came")
	}
	// With the LANs, the devices still connecting: their lines as they were.
	at(14 * time.Second)
	back := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 9, Paths: map[model.DeviceID]model.Path{}}
	got := a.holdThroughSwap(back)
	if got.Path(build).Kind != model.PathDirect || got.Path(nas).Kind != model.PathRelay {
		t.Errorf("lines while connecting again: %v %v", got.Path(build), got.Path(nas))
	}
	if again := a.holdThroughSwap(back); again != got {
		t.Error("the same snapshot shown anew: the view would refresh every frame")
	}
	if _, ok := a.swapLine(); ok {
		t.Error("the line stayed once the engine was back")
	}
	// One connected again: its own line; the other still held.
	at(15 * time.Second)
	some := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 10,
		Paths: map[model.DeviceID]model.Path{build: {Kind: model.PathRelay}}}
	got = a.holdThroughSwap(some)
	if got.Path(build).Kind != model.PathRelay || got.Path(nas).Kind != model.PathRelay {
		t.Errorf("lines: %v %v", got.Path(build), got.Path(nas))
	}
	// 10 s after it came back: what is.
	at(24 * time.Second)
	if got := a.holdThroughSwap(some); got != some || a.swap != nil || swapping() {
		t.Error("still held 10 s after the engine came back")
	}

	// Gone past 60 s: what is shows.
	a = &app{base: held, clock: t0}
	a.beginSwap(t0)
	at(time.Second)
	a.holdThroughSwap(gone)
	at(62 * time.Second)
	if got := a.holdThroughSwap(gone); got != gone || a.swap != nil {
		t.Error("still held past 60 s gone")
	}

	// Said it changes and never went away (the change did not happen): over
	// 15 s after it said so. While checking the new one, before it says, no
	// end: that takes its time.
	a = &app{base: held, clock: t0}
	a.beginSwap(t0)
	at(20 * time.Second)
	if got := a.holdThroughSwap(still); got != still || a.swap == nil {
		t.Error("the swap ended before the engine said it changes")
	}
	a.saidSwap()
	at(36 * time.Second)
	if got := a.holdThroughSwap(still); got != still || a.swap != nil {
		t.Error("still held for an engine that never went away")
	}

	// The system's window asks for the password: the line at once, and no
	// end while it asks.
	a = &app{base: held, clock: t0}
	a.beginSwap(t0)
	a.swap.asked = true
	at(30 * time.Second)
	if got := a.holdThroughSwap(still); got != still || a.swap == nil {
		t.Error("the swap ended while the password was asked for")
	}
	if line, ok := a.swapLine(); !ok || line != "正在更新网卡…" {
		t.Errorf("line while asking: %q", line)
	}
	a.endSwap()
}

// TestSwapInAWindowJustOpened: a window just opened asks for the change at
// once, before it has shown anything (11-16, the real one: the hold ended
// there and then). The old engine's first picture is the one kept, through
// it going away and the new one coming back; had it gone before showing
// anything, what is shows: the starting view, as before.
func TestSwapInAWindowJustOpened(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 13, 23, 46, 0, time.UTC)
	starting := &model.Snapshot{} // no engine heard yet
	a := &app{base: starting, clock: t0}
	at := func(d time.Duration) { a.clock = t0.Add(d) }
	a.beginSwap(t0)
	if got := a.holdThroughSwap(starting); got != starting || a.swap == nil {
		t.Fatal("before anything shows: not what is, or the swap given up")
	}
	at(100 * time.Millisecond)
	first := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 2891}
	if got := a.holdThroughSwap(first); got != first {
		t.Fatal("the old engine's first picture did not show")
	}
	a.saidSwap()
	at(2 * time.Second)
	if got := a.holdThroughSwap(&model.Snapshot{Version: 2891}); got != first {
		t.Fatal("the engine gone: the picture did not stay")
	}
	at(3 * time.Second)
	if got := a.holdThroughSwap(&model.Snapshot{Engine: true, LoggedIn: true, Version: 2891}); got != first {
		t.Fatal("the new engine without its LANs: the picture did not stay")
	}
	at(4 * time.Second)
	back := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 2892}
	if got := a.holdThroughSwap(back); got.Version != back.Version {
		t.Fatal("the new engine with its LANs did not show")
	}

	// Gone before showing anything: nothing to keep; the starting view.
	a = &app{base: starting, clock: t0}
	a.beginSwap(t0)
	a.saidSwap()
	at(time.Second)
	if got := a.holdThroughSwap(starting); got != starting {
		t.Error("nothing to keep, and not what is")
	}
	at(2 * time.Second)
	if got := a.holdThroughSwap(back); got != back {
		t.Error("the new engine did not show")
	}
	at(16 * time.Second)
	a.holdThroughSwap(back)
	if a.swap != nil {
		t.Error("the swap went on past 15 s after the engine said it changes")
	}
}

// TestSwapNewEngineWithoutControl: the new engine comes back without its
// LANs, the control server out of reach and none saved: the picture stays,
// the line says "正在更新网卡…" past 10 s, and past 60 s from the old engine
// going away, what is shows; the LANs coming before that end it as usual.
func TestSwapNewEngineWithoutControl(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 13, 23, 46, 0, time.UTC)
	held := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 7}
	noLANs := &model.Snapshot{Engine: true, LoggedIn: true, Version: 7}
	for _, lansAt := range []time.Duration{0, 30 * time.Second} {
		a := &app{base: held, clock: t0}
		at := func(d time.Duration) { a.clock = t0.Add(d) }
		a.beginSwap(t0)
		a.saidSwap()
		at(time.Second)
		a.holdThroughSwap(&model.Snapshot{Version: 7})
		at(2 * time.Second)
		if got := a.holdThroughSwap(noLANs); got != held {
			t.Fatal("the new engine without its LANs: the picture did not stay")
		}
		at(12 * time.Second)
		if line, ok := a.swapLine(); !ok || line != "正在更新网卡…" {
			t.Errorf("line at 11 s: %q %v", line, ok)
		}
		if lansAt > 0 {
			at(lansAt)
			back := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 8}
			if got := a.holdThroughSwap(back); got.Version != 8 {
				t.Fatal("the LANs came: they did not show")
			}
			if _, ok := a.swapLine(); ok {
				t.Error("the line stayed once the LANs came")
			}
			continue
		}
		at(40 * time.Second)
		if got := a.holdThroughSwap(noLANs); got != held {
			t.Error("the picture did not stay at 40 s")
		}
		at(62 * time.Second)
		if got := a.holdThroughSwap(noLANs); got != noLANs || a.swap != nil {
			t.Error("still held past 60 s gone")
		}
	}
}

// pidEngine answers Version with its pids in turn, the last one after.
type pidEngine struct {
	fakeEngine
	pids []int
}

func (p *pidEngine) Version(context.Context) (*apitype.VersionInfo, error) {
	pid := p.pids[0]
	if len(p.pids) > 1 {
		p.pids = p.pids[1:]
	}
	if pid == 0 {
		return nil, context.DeadlineExceeded // not answering
	}
	return &apitype.VersionInfo{Engine: "0.2.0", PID: pid}, nil
}

// TestWaitRestarted: after the change, the window waits for a process other
// than the one it asked: the old one answers a moment longer.
func TestWaitRestarted(t *testing.T) {
	e := &pidEngine{pids: []int{41, 0, 0, 52}}
	v := waitRestarted(context.Background(), e, &apitype.VersionInfo{PID: 41}, 5*time.Second)
	if v == nil || v.PID != 52 {
		t.Fatalf("got %+v, want the new process 52", v)
	}
	if v := waitRestarted(context.Background(), &pidEngine{pids: []int{41}}, &apitype.VersionInfo{PID: 41}, time.Second); v != nil {
		t.Errorf("the old process taken for the new: %+v", v)
	}
}
