package main

import (
	"maps"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The window while this computer's engine is changed for the one the
// package ships (design/plan/m7/engine-self-update.md §5): the picture stays
// as it was, without "正在连接 Deneb…" or "连不上这台电脑上的 Deneb"; each
// device keeps the line it had until the new engine connects it again; past
// 10 seconds the LAN's line says "正在更新网卡…", past 60 the window shows
// what is.

const (
	// swapQuiet is how long, from the engine going away, nothing changes.
	swapQuiet = 10 * time.Second
	// swapGiveUp is how long, from the engine going away, the window waits
	// for the new one before it shows what is.
	swapGiveUp = 60 * time.Second
	// swapNeverDown is how long an engine that said it changes may take to
	// go away: past it, it did not change.
	swapNeverDown = 15 * time.Second
	// swapPaths is how long, from the new engine having the LANs, each
	// device keeps its line while the new engine connects it again.
	swapPaths = 10 * time.Second
)

// engineSwap is an engine change under way.
type engineSwap struct {
	at time.Time // asked for
	// held is the picture to keep: the old engine's last, as long as it
	// answers (engine-restart-offline.md §3).
	held  *model.Snapshot
	said  time.Time // the engine said it changes (saidSwap)
	down  time.Time // the engine went away
	back  time.Time // the new one answered with the LANs
	asked bool      // the system's window asks for the password (updateService)
	// from and shown: the store's last snapshot and what was shown for it,
	// so that the same one shows until the store has another (sync
	// refreshes the view only for a new snapshot).
	from, shown *model.Snapshot
}

// swapUntil is set while a change is under way, to when it is over at the
// latest (Unix nanoseconds): keepEngine starts no engine of the window's
// meanwhile, which would take this computer's identity while the service
// starts again. A deadline of its own, as a hidden window draws no frame
// to end the change in.
var swapUntil atomic.Int64

// swapping: a change is under way.
func swapping() bool { return time.Now().UnixNano() < swapUntil.Load() }

// postWait is post, waiting up to a second for the window to have run fn:
// a window that draws no frame now (hidden) runs it when it shows.
func (a *app) postWait(fn func(c *ui.Context)) {
	done := make(chan struct{})
	a.post(func(c *ui.Context) {
		fn(c)
		close(done)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// beginSwap starts holding the picture, before the engine is asked to
// change.
func (a *app) beginSwap(now time.Time) {
	sw := &engineSwap{at: now}
	if serving(a.base) {
		sw.held = a.base
	}
	a.swap = sw
	swapUntil.Store(time.Now().Add(swapNeverDown + swapGiveUp).UnixNano())
}

// saidSwap: the engine said it changes (UpdateEngine answered so, or the
// service was installed again). From now, the window's clock, it has
// swapNeverDown to go away: not from the asking, as checking the new one
// takes its time.
func (a *app) saidSwap() {
	if a.swap == nil || !a.swap.said.IsZero() {
		return
	}
	a.swap.said = a.clock
	if a.clock.IsZero() {
		a.swap.said = time.Now()
	}
}

// serving: s pictures an engine that answers with what it has, the LANs
// when someone logged in: one to keep while the engine is changed.
func serving(s *model.Snapshot) bool {
	return s != nil && s.Engine && !(s.LoggedIn && !s.Loaded)
}

// endSwap stops holding it: the engine did not change after all, or the
// change is over.
func (a *app) endSwap() {
	a.swap = nil
	swapUntil.Store(0)
}

// holdThroughSwap is the snapshot to show in the place of base, the
// store's, while a change is under way.
func (a *app) holdThroughSwap(base *model.Snapshot) *model.Snapshot {
	sw := a.swap
	if sw == nil {
		return base
	}
	now := a.clock
	gone := !serving(base)
	if sw.down.IsZero() {
		if !gone {
			// The old engine still answers: what it shows is the picture to
			// keep once it goes, even when the window had none as the change
			// was asked for (a window just opened asks at once, 11-16).
			sw.held = base
		}
		if !gone || sw.held == nil {
			// Still there; or gone with nothing shown to keep, and what is
			// shows, as it would (the starting view).
			if !sw.said.IsZero() && now.Sub(sw.said) >= swapNeverDown {
				// It said it changes, and never went away.
				a.endSwap()
			}
			return base
		}
		sw.down = now
	}
	switch {
	case now.Sub(sw.down) >= swapGiveUp:
		// Gone too long: what is shows.
		a.endSwap()
		return base
	case gone:
		return sw.held
	}
	if sw.back.IsZero() {
		sw.back = now
	}
	if now.Sub(sw.back) >= swapPaths {
		a.endSwap()
		return base
	}
	if base != sw.from {
		sw.from, sw.shown = base, withHeldPaths(base, sw.held)
	}
	return sw.shown
}

// swapLine is the LAN's line while a change takes longer than it should,
// or while the system's window asks for the password.
func (a *app) swapLine() (string, bool) {
	sw := a.swap
	if sw == nil {
		return "", false
	}
	if sw.back.IsZero() && (sw.asked || !sw.down.IsZero() && a.clock.Sub(sw.down) >= swapQuiet) {
		return "正在更新网卡…", true
	}
	return "", false
}

// withHeldPaths is base with each device's path from held where the new
// engine has not connected it yet: its line stays as it was.
func withHeldPaths(base, held *model.Snapshot) *model.Snapshot {
	var paths map[model.DeviceID]model.Path
	for id, p := range held.Paths {
		if !p.Kind.Connected() || base.Path(id).Kind.Connected() {
			continue
		}
		if paths == nil {
			paths = maps.Clone(base.Paths)
			if paths == nil {
				paths = map[model.DeviceID]model.Path{}
			}
		}
		paths[id] = p
	}
	if paths == nil {
		return base
	}
	s := *base
	s.Paths = paths
	return &s
}
