package main

import (
	"github.com/kittors/deneb/client/model"
)

// The canvas had one relay until M4; now each relay has a stop of its own
// (DESIGN §6.12): the public one while links go through it, and the ones
// people dragged into the LAN, mine and my friends', all the time. In a
// world of one relay, as M1–M3's, there is one stop, as there was.

// relayOf is the relay d's link goes through: the one its path names, or,
// for a path that names none, the LAN's first.
func (a *app) relayOf(d *device) model.RelayID {
	if d.relayID != "" {
		return d.relayID
	}
	if l := a.snap.LAN(a.lan); l != nil && len(l.Relays) > 0 {
		return l.Relays[0]
	}
	return ""
}

// stopOf is the stop of the relay id on this canvas, or nil.
func (a *app) stopOf(id model.RelayID) *relayStop {
	for _, r := range a.relays {
		if r.id == id {
			return r
		}
	}
	return nil
}

// stopFor is the stop d's link goes through, or nil.
func (a *app) stopFor(d *device) *relayStop { return a.stopOf(a.relayOf(d)) }

// via reports whether d's link goes through r.
func (a *app) via(d *device, r *relayStop) bool { return d.path == "relay" && a.stopFor(d) == r }

// kept reports whether r stays on the canvas with no link through it: one
// of M4's relays that someone dragged into this LAN, not the public one.
func (a *app) kept(r *relayStop) bool {
	if a.all() {
		return false
	}
	l, rl := a.snap.LAN(a.lan), a.snap.Relays[r.id]
	return l != nil && rl != nil && rl.Source != "" && !rl.Public() && l.Added(r.id)
}

// used reports whether a link goes through r, as the devices' paths have
// it: what makes it take room on the canvas.
func (a *app) used(r *relayStop) bool {
	if a.all() {
		return false
	}
	for _, d := range a.devices {
		if a.via(d, r) {
			return true
		}
	}
	return false
}

// counts reports whether r takes room on the canvas, which everything
// else keeps clear of.
func (a *app) counts(r *relayStop) bool { return a.used(r) || a.kept(r) }

// syncStops makes a stop for each relay a link goes through, and for each
// that stays on the canvas, and gives every stop its words.
func (a *app) syncStops() {
	need := func(id model.RelayID) {
		if a.stopOf(id) != nil {
			return
		}
		rl := a.snap.Relays[id]
		r := &relayStop{id: id, sub: "中转站", legacy: rl == nil || rl.Source == ""}
		if at, ok := a.places.get(a.lan, r.key()); ok {
			r.base = at
			_, r.byHand = a.places.get(a.lan, r.handKey())
		} else {
			r.unplaced = true
		}
		a.relays = append(a.relays, r)
	}
	for _, d := range a.devices {
		if d.path == "relay" {
			need(a.relayOf(d))
		}
	}
	if l := a.snap.LAN(a.lan); l != nil && !a.all() {
		for _, id := range l.AddedRelays {
			if rl := a.snap.Relays[id]; rl != nil && rl.Source != "" && !rl.Public() {
				need(id)
			}
		}
	}
	for _, r := range a.relays {
		a.relayWords(r)
	}
}

// relayWords gives r its two lines (DESIGN §3.6): where it is, and whose
// it is; "连不上" while it is down.
func (a *app) relayWords(r *relayStop) {
	rl := a.snap.Relays[r.id]
	switch {
	case rl == nil:
		if r.name == "" {
			r.name = "中转站"
		}
	case rl.Source == "":
		r.name = rl.Name
	default:
		r.name, r.sub, r.down = rl.Label(), a.relayFrom(rl), rl.Down
		if rl.Down {
			r.sub = "连不上"
		}
		r.nameW = max(textWidth(r.name, 12)+2, textWidth(r.sub, 12))
	}
}

// relayCall is what a sentence calls a relay (DESIGN §6.12): its name,
// "香港" or what its owner named it; one known only by its address is
// whose it is, "我的服务器", "阿杰的服务器": an address is for the ball and
// the card, where it tells the one from the other, not for words.
func (a *app) relayCall(rl *model.Relay) string {
	if rl.Name != "" || rl.Source == "" || rl.Public() {
		return rl.Label()
	}
	return a.relayFrom(rl)
}

// relayFrom is whose a relay is, in the words of its ball's second line.
func (a *app) relayFrom(rl *model.Relay) string {
	if rl.Public() {
		return "公共"
	}
	what := "服务器"
	if rl.Source == model.RelayHome {
		what = "设备"
	}
	if rl.Owner == a.snap.Me {
		return "我的" + what
	}
	return say(a.personName(rl.Owner), "的"+what)
}

// key and handKey are where the stop's place is kept among the LAN's:
// M1's relay under the keys it always had, M4's each under its own.
func (r *relayStop) key() string {
	if r.legacy {
		return relayKey
	}
	return relayKey + "/" + string(r.id)
}

func (r *relayStop) handKey() string {
	if r.legacy {
		return relayHandKey
	}
	return relayHandKey + "/" + string(r.id)
}

// fellBack reports whether d goes through the public relay because the
// LAN's own one stopped (DESIGN §6.12, M4-04): "改走公共中转站".
func (a *app) fellBack(d *device) bool {
	if d.path != "relay" {
		return false
	}
	rl := a.snap.Relays[a.relayOf(d)]
	l := a.snap.LAN(d.lan)
	if rl == nil || !rl.Public() || l == nil {
		return false
	}
	for _, id := range l.AddedRelays {
		if o := a.snap.Relays[id]; o != nil && o.Down {
			return true
		}
	}
	return false
}

// viaWords is how d's link goes through a relay, under its name.
func (a *app) viaWords(d *device) string {
	if a.fellBack(d) {
		return "改走公共中转站"
	}
	name := "中转站"
	if rl := a.snap.Relays[a.relayOf(d)]; rl != nil && rl.Source != "" {
		name = a.relayCall(rl)
	} else if r := a.stopFor(d); r != nil {
		name = r.name
	} else if rl != nil {
		name = rl.Label()
	}
	return say("经", name, "中转")
}

// viaSub is the line under "经香港中转" in a device's card: whose the
// relay is; or, for M1's, that it is a little slower.
func (a *app) viaSub(d *device) string {
	rl := a.snap.Relays[a.relayOf(d)]
	switch {
	case rl == nil || rl.Source == "":
		return "慢一点，但能正常用"
	case a.fellBack(d):
		for _, id := range a.snap.LAN(d.lan).AddedRelays {
			if o := a.snap.Relays[id]; o != nil && o.Down {
				return say(a.relayCall(o), "连不上，先经", a.relayCall(rl), "中转")
			}
		}
	case rl.Public():
		return "公共中转站，慢一点但能正常用"
	case a.snap.MineRelay(rl):
		return "你自己的服务器"
	}
	return a.relayFrom(rl)
}

// relayDragging reports whether the hand has a relay.
func (a *app) relayDragging() bool {
	for _, r := range a.relays {
		if r.dragging {
			return true
		}
	}
	return false
}
