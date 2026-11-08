package client

import (
	"time"

	"github.com/kittors/deneb/client/model"
)

// Paths are debounced here and only here (implementation.md §2.7 step 7).
// The engine reports every change of a path as it happens; the view shows
// only the ones that last, so that a link never flickers:
//
//   - A device being connected to shows 正在连接… for the first 2 seconds,
//     whatever it goes through meanwhile (through the relay after 0.12 s,
//     direct after 0.63 s, in the PoC's startup test), then the path it has
//     by then.
//   - A connected device keeps its path until another one has lasted 2
//     seconds since the old one went: a direct path tried for a moment, or a
//     relay used while a direct path hiccups, never shows. Not being reached
//     at all (正在连接…, 连不上, 不在线) has to last 6 seconds to show, as
//     a direct path that drops takes about 2.4 seconds to be replaced by the
//     relay (multi-lan-tech §6.2) and the link must never show 断开 for it.
//   - What a person did shows at once: a refusal, a pause.
const (
	settle = 2 * time.Second
	lost   = 6 * time.Second
)

// debouncer turns the engine's paths, as reported, into those to show.
type debouncer struct {
	peers map[model.DeviceID]*peerPath
}

// peerPath is one device's path, as reported and as shown.
type peerPath struct {
	raw   model.Path // as the engine last reported it
	shown model.Path
	// window is when the first seconds of being connected to began; zero
	// outside them.
	window time.Time
	// left is when raw stopped being of the kind shown; zero while it is.
	left time.Time
}

// replace takes in the paths of all the devices the engine reaches, as of
// now: those not among them are gone.
func (d *debouncer) replace(paths map[model.DeviceID]model.Path, now time.Time) {
	for id := range d.peers {
		if _, ok := paths[id]; !ok {
			delete(d.peers, id)
		}
	}
	for id, p := range paths {
		d.report(id, p, now)
	}
}

// report takes in the path of one device, as of now.
func (d *debouncer) report(id model.DeviceID, p model.Path, now time.Time) {
	if d.peers == nil {
		d.peers = map[model.DeviceID]*peerPath{}
	}
	pp := d.peers[id]
	if pp == nil {
		// A device new to the store: its first seconds began when the engine
		// started to reach it, as far as the report tells. One reached well
		// before the store looked (as the window opens on peers connected for
		// hours) shows as it is, without first seconds of its own.
		start := now
		if !p.Since.IsZero() && p.Since.Before(now) {
			start = p.Since
		}
		pp = &peerPath{window: start, shown: model.Path{Kind: model.PathConnecting, Since: start}}
		d.peers[id] = pp
	}
	// What was reported before settles first, as it would have had the
	// store looked when it was due: a device that was direct when its first
	// seconds ended was shown direct then, whatever comes now.
	pp.step(now)
	pp.raw = p
	pp.step(now)
}

// at returns the paths to show as of now, and when they may change next
// without a report (zero if they will not).
func (d *debouncer) at(now time.Time) (map[model.DeviceID]model.Path, time.Time) {
	out := make(map[model.DeviceID]model.Path, len(d.peers))
	var next time.Time
	for id, pp := range d.peers {
		pp.step(now)
		out[id] = pp.shown
		if w := pp.wake(); !w.IsZero() && (next.IsZero() || w.Before(next)) {
			next = w
		}
	}
	return out, next
}

func connected(k model.PathKind) bool { return k == model.PathDirect || k == model.PathRelay }

// chosen reports whether a path is what a person did, which shows at once.
func chosen(k model.PathKind) bool { return k == model.PathRefused || k == model.PathPaused }

// step brings what shows up to date with what was reported, as of now.
func (pp *peerPath) step(now time.Time) {
	raw := pp.raw.Kind
	show := func(p model.Path) { pp.shown, pp.left = p, time.Time{} }
	switch {
	case raw == "":
	case chosen(raw):
		pp.window = time.Time{}
		show(pp.raw)
	case !pp.window.IsZero():
		if now.Sub(pp.window) < settle {
			pp.shown = model.Path{Kind: model.PathConnecting, Since: pp.window}
			return
		}
		pp.window = time.Time{}
		show(pp.raw)
	case raw == pp.shown.Kind:
		// The same kind of path: what changed is its round trip or its
		// loss, which show as they come.
		show(pp.raw)
	case connected(pp.shown.Kind):
		if pp.left.IsZero() {
			pp.left = now
		}
		wait := settle
		if !connected(raw) {
			wait = lost
		}
		if now.Sub(pp.left) >= wait {
			show(pp.raw)
		}
	case pp.shown.Kind == model.PathConnecting:
		// Past its first seconds: what it finds shows as it finds it.
		show(pp.raw)
	case raw == model.PathConnecting || connected(raw):
		// Reached again after it was not (连不上, 不在线, refused or paused):
		// first seconds anew.
		pp.window = now
		pp.shown, pp.left = model.Path{Kind: model.PathConnecting, Since: now}, time.Time{}
	default:
		show(pp.raw)
	}
}

// wake is when what shows may change without a report: the end of the first
// seconds, or of the wait for a new path to last.
func (pp *peerPath) wake() time.Time {
	switch {
	case !pp.window.IsZero():
		return pp.window.Add(settle)
	case !pp.left.IsZero() && pp.raw.Kind != pp.shown.Kind:
		if connected(pp.raw.Kind) {
			return pp.left.Add(settle)
		}
		return pp.left.Add(lost)
	}
	return time.Time{}
}
