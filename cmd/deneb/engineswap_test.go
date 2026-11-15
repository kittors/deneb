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
	// The old engine serves a while longer: what it says shows.
	at(200 * time.Millisecond)
	still := &model.Snapshot{Engine: true, LoggedIn: true, Loaded: true, Version: 8, Paths: held.Paths}
	if got := a.holdThroughSwap(still); got != still {
		t.Fatal("the old engine's own snapshot did not show")
	}
	// Gone: the picture stays.
	at(time.Second)
	gone := &model.Snapshot{Version: 8}
	if got := a.holdThroughSwap(gone); got != held {
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
	if got := a.holdThroughSwap(&model.Snapshot{Engine: true, LoggedIn: true}); got != held {
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

	// Never went away (the change did not happen): over after 15 s.
	a = &app{base: held, clock: t0}
	a.beginSwap(t0)
	at(16 * time.Second)
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
