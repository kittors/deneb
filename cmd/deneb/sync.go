package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// The view and the store (implementation.md §2.7). The view reads the
// store's snapshot at the start of every frame and lays over it the ops it
// sent that the store has not confirmed yet; what the hand does shows at
// once, as the op goes out. The canvas follows the result: devices that
// joined or left elsewhere come and go with the motion the prototype has
// for them, and an op the store turns down puts back what it moved.

// sync takes the store's latest snapshot and lays the ops not confirmed yet
// over it. It runs at the start of every frame, where it also brings the
// canvas to the result (reconcile), and right after an op is sent, with c
// nil, so that the op shows in the very frame it was sent in: the
// prototype changed its state there and then, and the demo's frames are the
// same as the prototype's.
func (a *app) sync(c *ui.Context) {
	if c != nil {
		a.clock = c.Now()
	}
	base := a.store.Snapshot()
	if dl := a.deleting; dl != nil && base != nil && !base.LoggedIn && a.base != nil && a.base.LoggedIn {
		// The server logs this computer out as it deletes the account: the
		// page stays as it was, names and all, while the window fades to
		// the sign-in (DESIGN §6.15).
		dl.loggedOut, base = true, a.base
	}
	if base != a.base || a.gen != a.synced {
		a.pend = slices.DeleteFunc(a.pend, func(p *pending) bool {
			return p.done && (p.err != nil || base.Version >= p.ver)
		})
		snap := base
		for _, p := range a.pend {
			snap = snap.With(p.op)
		}
		a.base, a.snap, a.synced = base, snap, a.gen
		a.refresh()
		a.unreconciled = true
	}
	if c != nil && a.unreconciled {
		a.unreconciled = false
		a.reconcile(c)
	}
}

// refresh copies the snapshot's facts into everything the view shows.
func (a *app) refresh() {
	s := a.snap
	a.me, a.self = string(s.Me), string(s.Self)
	people := make(map[string]*person, len(s.People)+1)
	for id, p := range s.People {
		name := p.Name
		if id == s.Me {
			name = "我"
		}
		people[string(id)] = &person{id: string(id), name: name, ch: p.Initial, color: ownerColors[p.Color%model.NumColors]}
	}
	a.people = people
	l := s.LAN(a.lan)
	// A new list, never the old one changed: the card may be going through
	// the old one as an op it sends brings the new one.
	var members []string
	if l != nil {
		for _, m := range l.Members {
			members = append(members, string(m))
		}
	}
	a.members = members
	a.respot()
	for _, d := range a.devices {
		a.facts(d, a.lanOf(d))
	}
	for _, d := range a.tray {
		a.facts(d, l)
	}
	a.twins()
	// An invite sent to me by name shows its card as it comes, once.
	for _, inv := range s.Invitations {
		if a.seenInvitations == nil {
			a.seenInvitations = map[string]bool{}
		}
		if !a.seenInvitations[inv.ID] && a.invite == nil && s.Loaded {
			a.seenInvitations[inv.ID] = true
			a.showInvitation(inv)
		}
	}
	// Whoever the view shows has a ball, the owner of a device that stayed
	// after its owner left too: grey, without an initial.
	for _, id := range members {
		a.somebody(id)
	}
	for _, d := range a.devices {
		a.somebody(d.owner)
	}
	for _, d := range a.tray {
		a.somebody(d.owner)
	}
	a.syncStops()
	a.relaysCame()
	// A LAN new to the sidebar lights up as it comes in: one made or joined
	// here, or elsewhere. Those there as the window opens do not.
	first := a.knownLANs == nil
	if first {
		a.knownLANs, a.flashFrom = map[model.LANID]bool{}, map[model.LANID]time.Time{}
	}
	for _, l := range s.LANs {
		a.somebodies(l.Members)
		if !a.knownLANs[l.ID] {
			a.knownLANs[l.ID] = true
			if !first && s.Loaded {
				a.flashFrom[l.ID] = time.Time{}
			}
		}
	}
	if !s.Loaded {
		// Nothing is known yet, so nothing that comes is new.
		a.knownLANs = nil
	}
}

func (a *app) somebodies(ids []model.PersonID) {
	for _, id := range ids {
		a.somebody(string(id))
	}
}

func (a *app) somebody(id string) {
	if _, ok := a.people[id]; !ok {
		a.people[id] = &person{id: id, color: ui.Hex("#a1a1aa")}
	}
}

// facts copies what the snapshot says about d into it. A device the
// snapshot no longer has keeps what it had, as it shows on its way out; one
// not in the LAN keeps how it was reached and what it opened there until it
// is in it again, so that its words do not change as it leaves.
func (a *app) facts(d *device, l *model.LAN) {
	s := a.snap
	m := s.Device(d.dev)
	if m == nil {
		return
	}
	d.owner = string(m.Owner)
	d.mine = m.Owner == s.Me
	d.kind = glyphFor(m.Kind)
	d.name = m.Name
	if d.id == a.self {
		d.name = "这台电脑"
	}
	d.online, d.refusesMe, d.addr, d.fqdn = m.Online, m.RefusesMe, m.Addr, m.FQDN
	d.login, d.lastSeen, d.using = m.Login, m.LastSeen, s.Using[m.ID]
	for _, c := range s.Cuts {
		if c.Device == m.ID && c.At.After(d.closedSeen) {
			// Cut just now: its red light and its sentence start as the
			// view learns of it.
			d.closed, d.closedSeen, d.closedAt = c.Service, c.At, a.now()
		}
	}
	d.pending = l != nil && l.HasPending(m.ID)
	refused := make(map[string]bool, len(m.Refused))
	for who, on := range m.Refused {
		if on {
			refused[string(who)] = true
		}
	}
	d.refused = refused
	in := l != nil && (l.Has(m.ID) || l.HasPending(m.ID))
	if in || d.path == "" {
		p := s.Path(m.ID)
		d.path, d.relayID, d.rtt = string(p.Kind), p.Relay, p.RTT
	}
	if in && l.Paused && d.id != a.self {
		// The LAN is paused on this computer: nothing of it connects here,
		// and the same device in another LAN goes on as it was (scene 3).
		d.path, d.relayID = "paused", ""
	}
	if s.NoNetwork && d.id != a.self && d.path != "paused" && d.path != "apart" {
		// This computer has no network: every other device waits for it,
		// rather than seem out of reach (DESIGN §6.10).
		d.path, d.relayID = "nonet", ""
	}
	if in {
		svcs := l.Services[m.ID]
		out := make([]service, len(svcs), len(svcs)+len(s.Local))
		for i, v := range svcs {
			out[i] = service{icon: serviceIcon(v.Kind), name: v.Name, on: v.On, svc: v}
		}
		if m.ID == s.Self {
			// This computer's card also offers what it listens on and has
			// not opened yet, off (§2.7: /v1/services).
			for _, v := range s.Local {
				if !slices.ContainsFunc(svcs, func(x model.Service) bool { return x.Key == v.Key }) {
					out = append(out, service{icon: serviceIcon(v.Kind), name: v.Name, svc: v})
				}
			}
		}
		d.services = out
	}
}

// glyphFor is the glyph a device of kind shows on its tile.
func glyphFor(kind string) string {
	switch kind {
	case "laptop", "desktop", "mini", "server", "nas", "console", "phone":
		return kind
	case "tablet":
		return "phone"
	case "other":
		return "mini"
	}
	return "desktop"
}

// serviceIcon is the icon a service of kind shows in the card.
func serviceIcon(kind string) string {
	switch kind {
	case "screen":
		return "screen"
	case "files":
		return "folder"
	case "shell":
		return "terminal"
	}
	return "globe"
}

// load lays the LAN out on the canvas as it is, without motion: as the
// window opens, and as the sidebar switches to another LAN.
func (a *app) load() {
	l := a.snap.LAN(a.lan)
	a.devices, a.tray = nil, nil
	a.trayOff, a.slots = map[string]*ui.Point{}, map[string]ui.Point{}
	a.beams, a.geos = map[string]*beamRun{}, map[string]*linkGeo{}
	if t := a.toast; t != nil && t.commit != nil {
		// What a toast held back goes as the canvas changes.
		a.post(t.commit)
	}
	a.landing, a.held, a.toast = nil, nil, nil
	a.regionRects, a.regionPaints, a.regionsLaid, a.regionHeld = nil, nil, false, ""
	a.focus, a.dropOver, a.dropNext, a.edgeNote, a.edgeNext = "", "", "", nil, nil
	a.relays = nil
	if l != nil || a.all() {
		for _, sp := range a.spots() {
			d := &device{facts: facts{id: sp.key, dev: sp.dev, lan: sp.lan}}
			if at, ok := a.places.get(a.lan, d.id); ok {
				d.base, d.rest = at, at
			} else {
				d.unplaced = true
			}
			a.devices = append(a.devices, d)
		}
	}
	if l != nil {
		for _, id := range a.trayIDs() {
			d := &device{facts: facts{id: string(id), dev: id, lan: a.lan}}
			a.tray = append(a.tray, d)
			a.trayOff[d.id] = &ui.Point{}
		}
	}
	a.refresh()
	for _, d := range a.devices {
		d.lastPath, d.lastRelay, d.waited = d.path, d.relayID, d.pending
	}
	// A relay there as the LAN shows is there: it does not pop in.
	a.syncStops()
	for _, r := range a.relays {
		r.shown = a.relayLinked(r) || a.kept(r)
	}
}

// show puts the LAN of id on the canvas. It changes what the window shows,
// and nothing else: every connection stays as it is. A LAN just made or
// joined here comes in (pop): its devices pop in one after the other, their
// tracks growing out from this computer (scene 3's 加入).
func (a *app) show(c *ui.Context, id model.LANID, pop bool) {
	if id == a.lan || id != allLANs && a.snap.LAN(id) == nil {
		return
	}
	a.keepPlaces()
	a.lan = id
	a.selected, a.more = "", false
	a.load()
	// When the canvas switched, to the millisecond, against the engine's
	// timing of the connections it must not touch (acceptance step 4).
	name := "全部"
	if l := a.snap.LAN(id); l != nil {
		name = l.Name
	}
	log.Printf("deneb: canvas shows %s (%s) at %s", name, id, time.Now().Format("15:04:05.000"))
	if !pop {
		return
	}
	now, i := c.Now(), 0
	for _, d := range a.devices {
		if d.id == a.self {
			continue
		}
		t := now.Add(time.Duration(120+90*i) * time.Millisecond)
		d.popped, d.popFirst, d.joined = t, true, t
		i++
	}
	c.AnimationFrame()
}

// reconcile brings the canvas to the snapshot (§2.7 step 4). What the hand
// did is there already, and stays as it is. A device that joined the LAN
// elsewhere pops in at its place, or a free one, as its track grows out
// from this computer; one that left shrinks away as its track is drawn back
// in; one of mine that moved between the LAN and the tray elsewhere, or
// whose move the store turned down, flies across. A link whose path changed
// is drawn anew.
func (a *app) reconcile(c *ui.Context) {
	// What came and went of the devices given to me, and the seats freed
	// that the store counts free now (DESIGN §6.11).
	a.watchShares(c)
	a.freed()
	if a.snap.LoggedIn {
		a.loggingIn, a.loginErr = false, ""
		if link := a.laterLink; link != "" && a.snap.Loaded {
			a.laterLink = ""
			a.openLink(c, link)
		}
	}
	if id := a.wantLAN; id != "" && a.snap.LAN(id) != nil {
		a.wantLAN = ""
		a.show(c, id, true)
	}
	if ls := a.shownLANs(); a.all() && len(ls) < 2 {
		// Down to one LAN, or none: "全部" goes from the sidebar, and the
		// canvas shows the one there is.
		a.lan = ""
		if len(ls) == 1 {
			a.lan = ls[0].ID
		}
		a.selected, a.more = "", false
		a.load()
		return
	}
	l := a.snap.LAN(a.lan)
	if l == nil && a.lan == "" && len(a.snap.LANs) == 0 {
		return
	}
	if l == nil && !a.all() {
		// The LAN is gone, left or deleted: the canvas shows the first one
		// there is.
		a.lan = ""
		if len(a.snap.LANs) > 0 {
			a.lan = a.snap.LANs[0].ID
		}
		a.selected, a.more = "", false
		a.load()
		return
	}
	in := map[string]bool{}
	spots := a.spots()
	for _, sp := range spots {
		in[sp.key] = true
	}
	tray := map[string]bool{}
	for _, id := range a.trayIDs() {
		tray[string(id)] = true
	}
	shown := map[string]bool{}
	for _, d := range slices.Clone(a.devices) {
		shown[d.id] = true
		switch {
		case !d.gone.IsZero():
			if in[d.id] {
				// Back before it was gone: it grows back where it is, its
				// track drawn out again.
				d.gone, d.leaving, d.joined = time.Time{}, time.Time{}, c.Now()
				delete(a.beams, d.id)
				c.AnimationFrame()
			}
		case a.landing == d:
			if in[d.id] {
				// Brought back elsewhere, or its leaving turned down: it
				// turns back to where it rested.
				a.turnBack(d)
			}
		case in[d.id]:
		case tray[d.id]:
			// Mine, taken out elsewhere, or its joining turned down.
			if d.waited {
				a.turnedAway(c, d)
			}
			a.startLeave(c, d, false, false)
		default:
			if d.waited && d.mine {
				a.turnedAway(c, d)
			}
			a.vanish(c, d)
		}
	}
	for _, d := range slices.Clone(a.tray) {
		shown[d.id] = true
		switch {
		case tray[d.id]:
		case in[d.id]:
			// Brought in elsewhere, or its leaving turned down: it flies out
			// of its slot to where it rested here, or a free spot.
			from, ok := a.slots[d.id]
			if !ok {
				from = ui.Point{X: a.cw / 2, Y: a.ch - 50}
			}
			want, ok := a.places.get(a.lan, d.id)
			if !ok {
				want = a.placeNew(d)
			}
			a.joinFrom(c, d, from, a.freeSpot(want, d, footprint, a.cw, a.ch), ui.Pose{Scale: float32(slotTile) / tileSize}, false)
		default:
			// No longer mine, or gone.
			a.tray = slices.DeleteFunc(slices.Clone(a.tray), func(x *device) bool { return x == d })
		}
	}
	fresh := map[model.LANID]int{}
	for _, sp := range spots {
		if shown[sp.key] {
			continue
		}
		delay := time.Duration(0)
		if _, ok := a.regionRects[sp.lan]; a.all() && sp.lan != "" && !ok {
			// A LAN joined just now comes in as a region of its own, its
			// devices popping in one after the other (scene 3's 加入).
			delay = time.Duration(120+90*fresh[sp.lan]) * time.Millisecond
			fresh[sp.lan]++
		}
		a.appear(c, sp, delay)
	}
	for _, id := range a.trayIDs() {
		if !shown[string(id)] {
			d := &device{facts: facts{id: string(id), dev: id, lan: a.lan}}
			a.facts(d, l)
			a.somebody(d.owner)
			a.tray = append(slices.Clone(a.tray), d)
			a.trayOff[d.id] = &ui.Point{}
		}
	}
	for _, d := range a.devices {
		switch {
		case d.waited && !d.pending && d.gone.IsZero() && a.landing != d && !d.turnedDown:
			// Let in: it springs up full, and its track grows out from this
			// computer as a joining device's does (DESIGN §6.7). One turned
			// away is on its way back to the tray instead.
			d.approved, d.joined, d.leaving = c.Now(), c.Now(), time.Time{}
			delete(a.beams, d.id)
			c.AnimationFrame()
		case !d.waited && d.pending && d.gone.IsZero() && !d.joined.IsZero():
			// Back to waiting, its letting in undone: its track draws back.
			d.approved, d.leaving = time.Time{}, c.Now()
			c.AnimationFrame()
		}
		d.waited = d.pending
		if a.watching[string(d.dev)] && d.lastPath == "offline" && d.path != "offline" && d.path != "" {
			// Asked to be told: it came online (DESIGN §6.8).
			delete(a.watching, string(d.dev))
			a.notify(c, d, short(d.name)+"上线了", nil)
		}
		if d.lastPath == "" {
			d.lastPath, d.lastRelay = d.path, d.relayID
		} else if d.path != d.lastPath || d.path == "relay" && d.relayID != d.lastRelay {
			// From one relay to another too (M4): the light goes on through
			// the new one as the old way fades.
			a.repath(c, d)
		}
	} // Those that came onto the canvas just now are numbered too.
	a.twins()
}

// turnBack sends a device on its way into the tray back to where it rested.
func (a *app) turnBack(d *device) {
	a.landing = nil
	d.leaving, d.aimed, d.flying = time.Time{}, false, true
	d.off = ui.Point{X: d.rest.X - d.base.X, Y: d.rest.Y - d.base.Y}
}

// appear puts a device that joined the LAN elsewhere on the canvas: where
// it rested here before, or at a free spot. It pops in there, after delay,
// as its track grows out from this computer.
func (a *app) appear(c *ui.Context, sp spot, delay time.Duration) {
	id := sp.key
	d := &device{facts: facts{id: id, dev: sp.dev, lan: sp.lan}}
	if slices.ContainsFunc(a.devices, func(o *device) bool { return o.key() == id }) {
		// Its key is taken by one that moved here from another region and
		// kept its old element's: this one's element is keyed apart.
		d.elem = id + "\x00" + strconv.Itoa(len(a.devices))
	}
	a.facts(d, a.lanOf(d))
	a.somebody(d.owner)
	if a.cw == 0 {
		d.unplaced = true
	} else {
		want, ok := a.places.get(a.lan, id)
		if !ok {
			want = a.placeNew(d)
		}
		spot := a.freeSpot(want, d, a.footOf(d), a.cw, a.ch)
		d.base, d.rest, d.live = spot, spot, spot
	}
	now := c.Now().Add(delay)
	d.joined, d.popped, d.popFirst = now, now, true
	delete(a.beams, id)
	delete(a.geos, id)
	a.devices = append(slices.Clone(a.devices), d)
	c.AnimationFrame()
}

// vanishFor is how long a device that left elsewhere takes to go.
const vanishFor = 360 * time.Millisecond

// vanish takes a device that left the LAN elsewhere off the canvas: it
// shrinks and fades where it is, as its track is drawn back in.
// turnedAway says that the owner did not let my device in, as it goes back
// (DESIGN §6.7): never without a word. It stays dashed on its way.
func (a *app) turnedAway(c *ui.Context, d *device) {
	l := a.snap.LAN(d.lan)
	if l == nil {
		return
	}
	d.turnedDown = true
	a.notify(c, d, say(a.personName(l.Owner), "没有同意", short(d.name), "加入「"+short(l.Name)+"」"), nil)
}

func (a *app) vanish(c *ui.Context, d *device) {
	now := c.Now()
	d.gone, d.leaving = now, now
	if a.selected == d.id {
		a.selected, a.more = "", false
	}
	c.AnimationFrame()
}

// repath draws d's link anew for the path it changed to: a track that
// connects grows out from both ends with fresh light, as when a device
// joins; one that stops connecting fades to the dotted line.
func (a *app) repath(c *ui.Context, d *device) {
	was := d.lastPath
	d.lastPath, d.fromPath, d.pathAt = d.path, was, c.Now()
	d.fromRelay, d.lastRelay = d.lastRelay, d.relayID
	if d.path == "self" || was == "self" {
		return
	}
	if linked(d.path) {
		if linked(was) {
			// From direct to the relay, or back: the old way's light stops,
			// and its track fades as the new way grows out (scene 1).
			d.oldGeo = a.geos[d.id]
		}
		d.joined = c.Now()
		delete(a.beams, d.id)
	}
	c.AnimationFrame()
}

// linked reports whether a link of path carries light: it connects.
func linked(path string) bool { return path == "direct" || path == "relay" }

// placeNew finds a place for a device that has none on this LAN's canvas:
// this computer a little left of the middle, as the design's sample has it,
// and the others around it, on the diagonals first, as the tracks of scene 5
// go out to the corners (DESIGN §4), then wherever there is room.
func (a *app) placeNew(d *device) ui.Point {
	w, h := a.cw, a.ch
	if a.all() {
		return a.freeSpot(a.allPlace(d), d, a.footOf(d), w, h)
	}
	ar := a.restArea(w, h)
	mid := ui.Point{X: min(max((w-cardWidth-20)/2, ar.X), ar.X+ar.W), Y: ar.Y + ar.H/2}
	me := a.device(a.self)
	if d.id == a.self || me == nil || me.unplaced {
		return a.freeSpot(mid, d, footprint, w, h)
	}
	at := me.at()
	for _, r := range []float64{220, 320} {
		for _, deg := range []float64{-135, -45, 45, 135, -90, 90, 180, 0, -112, -68, 68, 112, 158, 22, -158, -22} {
			rad := deg * math.Pi / 180
			want := ui.Point{X: at.X + float32(r*math.Cos(rad)), Y: at.Y + float32(r*0.8*math.Sin(rad))}
			if want.X < ar.X || want.X > ar.X+ar.W || want.Y < ar.Y || want.Y > ar.Y+ar.H {
				continue
			}
			if spot := a.freeSpot(want, d, footprint, w, h); dist(spot, want) < 24 {
				return spot
			}
		}
	}
	return a.freeSpot(at, d, footprint, w, h)
}

// place finds places for what has none yet, now that the canvas is laid
// out: devices first, this computer before the others, then the relay
// between this computer and the first device that goes through it.
func (a *app) place() {
	if a.cw == 0 {
		return
	}
	order := slices.Clone(a.devices)
	slices.SortStableFunc(order, func(x, y *device) int {
		return boolInt(y.id == a.self) - boolInt(x.id == a.self)
	})
	for _, d := range order {
		if d.unplaced {
			spot := a.placeNew(d)
			d.base, d.rest, d.off, d.unplaced = spot, spot, ui.Point{}, false
		}
	}
	for _, r := range a.relays {
		if !a.counts(r) {
			continue
		}
		// As it comes to show, or as the LAN does with it there: a place it
		// took by itself is looked at again, the devices perhaps moved since.
		if r.unplaced || !r.dragging && (!r.shown || !r.checked) && !r.byHand && !a.relayClear(r, r.base) {
			r.base = a.relaySpot(r)
			r.off, r.unplaced = ui.Point{}, false
		}
		r.checked = true
	}
}

// relaySpot is where the relay goes by itself: between this computer and
// the first device that goes through it, a little below, as scene 1 shows
// it; or, if a track through it would cross a name or a tile there, the
// nearest spot around that where none does (DESIGN: lines and pills never
// cover names). With no such spot, the first.
func (a *app) relaySpot(r *relayStop) ui.Point {
	me := a.device(a.self)
	var far *device
	for _, d := range a.devices {
		if a.via(d, r) {
			far = d
			break
		}
	}
	want := ui.Point{X: a.cw / 2, Y: a.ch / 2}
	switch {
	case me != nil && far != nil:
		p, q := me.at(), far.at()
		want = ui.Point{X: (p.X + q.X) / 2, Y: (p.Y+q.Y)/2 + 30}
	case me != nil:
		// No link through it yet (one dragged into the LAN, M4): under
		// this computer, out of the way.
		want = ui.Point{X: me.at().X, Y: me.at().Y + 150}
	}
	first := a.freeSpot(want, r, r.footprint, a.cw, a.ch)
	if a.relayClear(r, first) {
		return first
	}
	for _, rad := range []float64{40, 80, 120, 160, 200} {
		for deg := 90.0; deg < 450; deg += 30 {
			at := ui.Point{X: want.X + float32(rad*math.Cos(deg*math.Pi/180)), Y: want.Y + float32(rad*math.Sin(deg*math.Pi/180))}
			if spot := a.freeSpot(at, r, r.footprint, a.cw, a.ch); dist(spot, at) < 24 && a.relayClear(r, spot) {
				return spot
			}
		}
	}
	return first
}

// relayClear reports whether the tracks through a relay at at, as they are
// drawn, keep off every device's name, and off every tile but those at
// their ends.
func (a *app) relayClear(r *relayStop, at ui.Point) bool {
	me := a.device(a.self)
	if me == nil || me.unplaced {
		return true
	}
	for _, d := range a.devices {
		if !a.via(d, r) || d.unplaced || !d.gone.IsZero() {
			continue
		}
		cv := newCurve(me.at(), d.at(), &at, bendFor(d.id))
		for _, o := range a.devices {
			if o.unplaced || !o.gone.IsZero() || a.landing == o {
				continue
			}
			name, tile := a.nameBox(o), ui.Rect{X: o.at().X - tileSize/2 - 4, Y: o.at().Y - tileSize/2 - 4, W: tileSize + 8, H: tileSize + 8}
			for _, pt := range cv.pts {
				// With some air: a track that grazes a name looks as if it
				// touched it. The sample's relay leaves this computer's
				// name about 9 apart, at its corner.
				if rectDist(name, pt) < 6 || o != me && o != d && inRect(tile, pt) {
					return false
				}
			}
		}
	}
	return true
}

// nameBox is about where d's name and the line under it show, under its
// tile: the text itself, as wide as the longer of the two, up to
// nameWidth.
func (a *app) nameBox(d *device) ui.Rect {
	width := func(s string, size float32) float32 {
		w := float32(0)
		for _, r := range s {
			if r < 0x80 {
				w += size * 0.58
			} else {
				w += size
			}
		}
		return w
	}
	at := d.at()
	w := min(max(width(d.name, 13), width(a.status(d), 12)), nameWidth)
	return ui.Rect{X: at.X - w/2, Y: at.Y + tileSize/2 + 8, W: w, H: 38}
}

func inRect(r ui.Rect, p ui.Point) bool {
	return p.X >= r.X && p.X <= r.X+r.W && p.Y >= r.Y && p.Y <= r.Y+r.H
}

// rectDist is how far p is from r: 0 inside it.
func rectDist(r ui.Rect, p ui.Point) float32 {
	dx := max(r.X-p.X, 0, p.X-(r.X+r.W))
	dy := max(r.Y-p.Y, 0, p.Y-(r.Y+r.H))
	return float32(math.Hypot(float64(dx), float64(dy)))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// keepPlaces notes where everything on the canvas rests, for the next time
// the LAN shows.
func (a *app) keepPlaces() {
	for _, d := range a.devices {
		if !d.unplaced && d.gone.IsZero() && a.landing != d && !d.dragging && !d.flying && !d.arriving {
			a.places.set(a.lan, d.id, d.rest)
		}
	}
	for _, r := range a.relays {
		if !r.unplaced && !r.dragging && a.counts(r) {
			a.places.set(a.lan, r.key(), r.at())
		}
	}
}

// ---- Ops ------------------------------------------------------------------------------

// send asks the store for op and shows it at once: it is laid over the
// snapshot until the store confirms it. Turned down, it comes off again,
// what moved goes back (reconcile), and the toast says why.
func (a *app) send(c *ui.Context, op model.Op) {
	p := &pending{op: op}
	a.pend = append(a.pend, p)
	a.gen++
	a.commit(c, p)
	a.sync(nil)
}

// sendAfter sends first, and then second once the store took first: both
// show at once, and second goes only after first went through, or not at
// all if first was turned down. A move between LANs is into the new one,
// then out of the old, so that a device is never left in neither.
func (a *app) sendAfter(c *ui.Context, first, second model.Op) {
	p1, p2 := &pending{op: first}, &pending{op: second, held: true}
	a.pend = append(a.pend, p1, p2)
	a.gen++
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, p1.op) },
		func(c *ui.Context, r model.Result, err error) {
			p1.done, p1.ver, p1.err = true, r.Version, err
			a.gen++
			if err != nil {
				a.pend = slices.DeleteFunc(a.pend, func(x *pending) bool { return x == p2 })
				a.problem(c, err)
				return
			}
			a.commit(c, p2)
		})
	a.sync(nil)
}

// commit hands a pending op to the store.
func (a *app) commit(c *ui.Context, p *pending) {
	p.held = false
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, p.op) },
		func(c *ui.Context, r model.Result, err error) {
			p.done, p.ver, p.err = true, r.Version, err
			a.gen++
			if err != nil {
				a.problem(c, err)
			}
		})
}

// hold shows op at once, with a toast that offers undo, and sends it only
// as the toast goes: for what the server cannot take back, as letting a
// device in or turning it away (DESIGN §6.7). Undo drops it unsent.
func (a *app) hold(c *ui.Context, op model.Op, d *device, text string) {
	p := &pending{op: op, held: true}
	a.pend = append(a.pend, p)
	a.gen++
	a.sync(nil)
	a.notify(c, d, text, func() {
		a.pend = slices.DeleteFunc(a.pend, func(x *pending) bool { return x == p })
		a.gen++
	})
	a.toast.commit = func(c *ui.Context) {
		if p.held {
			a.commit(c, p)
		}
	}
}

// holdFor is hold for an op about no device on the canvas, as letting a
// person in.
func (a *app) holdFor(c *ui.Context, op model.Op, text string) { a.hold(c, op, nil, text) }

// run does work off the main thread and hands its result to done on it; a
// store that answers at once (the demo's) is asked in place, so that what it
// answers shows in the same frame. Without a frame to answer in (c nil: a
// window going away sends what it held), the work goes off the main thread
// all the same, and its answer to the next frame.
func run[T any](a *app, c *ui.Context, work func(context.Context) (T, error), done func(*ui.Context, T, error)) {
	if _, ok := a.store.(client.Immediate); ok && c != nil {
		v, err := work(context.Background())
		done(c, v, err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		v, err := work(ctx)
		a.post(func(c *ui.Context) { done(c, v, err) })
	}()
}

// problem says in the toast why something did not go through: in the
// store's words when it has them.
func (a *app) problem(c *ui.Context, err error) {
	msg := "没办成，等一下再试试"
	var e *model.Error
	switch {
	case errors.As(err, &e) && e.Message != "":
		msg = e.Message
	case errors.Is(err, context.DeadlineExceeded):
		msg = "等了太久，没办成"
	}
	a.notify(c, nil, msg, nil)
	if a.panelShown {
		// The menu bar's panel says it too: the window may be out of sight.
		a.panelNote = panelNote{text: msg, until: c.Now().Add(4 * time.Second)}
	}
}

// post hands fn to the main thread, where the next frame runs it first.
func (a *app) post(fn func(c *ui.Context)) {
	a.inboxMu.Lock()
	a.inbox = append(a.inbox, fn)
	a.inboxMu.Unlock()
	a.wakeUp()
}

// setWake sets how to ask the window for a frame, from any goroutine.
func (a *app) setWake(fn func()) {
	a.inboxMu.Lock()
	a.wake = fn
	a.inboxMu.Unlock()
}

// wakeUp asks the window for a frame, from any goroutine: one that takes in
// what was posted, or the store's new snapshot.
func (a *app) wakeUp() {
	a.inboxMu.Lock()
	wake := a.wake
	a.inboxMu.Unlock()
	if wake != nil {
		wake()
	}
}

// drain runs what other goroutines posted.
func (a *app) drain(c *ui.Context) {
	a.inboxMu.Lock()
	fns := a.inbox
	a.inbox = nil
	a.inboxMu.Unlock()
	for _, fn := range fns {
		fn(c)
	}
}

// ---- Places ----------------------------------------------------------------------------

// relayKey is the relay's key among a LAN's places: never a device's id.
// relayHandKey is there once the person put the relay somewhere by hand.
const (
	relayKey     = "\x00relay"
	relayHandKey = "\x00relay-by-hand"
)

// places is where things rest on each LAN's canvas, as the person left them
// (DESIGN §6.2): by LAN and device, so that the next time the window opens
// they are where they were. The demo's are the prototype's, in memory; the
// real ones are kept in a file, written at most once a second off the main
// thread.
type places struct {
	at    map[model.LANID]map[string]ui.Point
	file  string
	dirty bool
	saved time.Time
	mu    sync.Mutex // held while the file is written, so writes do not cross
}

func (p *places) get(lan model.LANID, id string) (ui.Point, bool) {
	at, ok := p.at[lan][id]
	return at, ok
}

func (p *places) set(lan model.LANID, id string, at ui.Point) {
	if old, ok := p.at[lan][id]; ok && old == at {
		return
	}
	if p.at[lan] == nil {
		p.at[lan] = map[string]ui.Point{}
	}
	p.at[lan][id] = at
	p.dirty = true
}

// save writes the places to their file when they changed, at most once a
// second.
func (p *places) save(now time.Time) {
	if !p.dirty || p.file == "" || now.Sub(p.saved) < time.Second {
		return
	}
	p.dirty, p.saved = false, now
	b, err := json.Marshal(p.at)
	if err != nil {
		return
	}
	go func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		tmp := p.file + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			os.Rename(tmp, p.file)
		}
	}()
}

// loadPlaces reads the places kept in file; none if there is no file yet.
func loadPlaces(file string) *places {
	p := &places{at: map[model.LANID]map[string]ui.Point{}, file: file}
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &p.at)
	}
	if p.at == nil {
		p.at = map[model.LANID]map[string]ui.Point{}
	}
	return p
}

// placesFile is where the places are kept: the user's config folder.
func placesFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "Deneb")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	return filepath.Join(dir, "layout.json")
}

// onCanvas are the devices a LAN's canvas shows: those in it, and those that
// wait for its owner to let them in (DESIGN §6.7), in the order they came.
func onCanvas(l *model.LAN) []model.DeviceID {
	if len(l.Pending) == 0 {
		return l.Devices
	}
	return append(slices.Clone(l.Devices), l.Pending...)
}

// twins numbers the devices on the canvas named as one before them, 2, 3…,
// and marks an old one of mine among them, gone for 30 days (DESIGN §6.8).
func (a *app) twins() {
	// Numbered by device, not by tile: one device in two regions of the
	// "全部" view is one device, not two of a name.
	devs := map[string][]model.DeviceID{}
	mine := map[string]map[model.DeviceID]bool{}
	for _, d := range a.devices {
		if d.id == a.self {
			continue
		}
		i := slices.Index(devs[d.name], d.dev)
		if i < 0 {
			devs[d.name] = append(devs[d.name], d.dev)
			i = len(devs[d.name]) - 1
		}
		d.twin = 0
		if i > 0 {
			d.twin = i + 1
		}
		if d.mine {
			if mine[d.name] == nil {
				mine[d.name] = map[model.DeviceID]bool{}
			}
			mine[d.name][d.dev] = true
		}
	}
	for _, d := range a.devices {
		d.stale = d.mine && len(mine[d.name]) > 1 && !d.online && !d.lastSeen.IsZero() && a.now().Sub(d.lastSeen) > 30*24*time.Hour
	}
}

// now is the view's clock as the frame being built has it: what the timers
// and the login's dates are counted against.
func (a *app) now() time.Time {
	if a.clock.IsZero() {
		return time.Now()
	}
	return a.clock
}

// personName is what a person is called here, as their ball says: their
// name, or 拥有者 when the snapshot does not have them.
func (a *app) personName(id model.PersonID) string {
	if p := a.people[string(id)]; p != nil {
		return p.name
	}
	return "拥有者"
}

// ownerName is the name of the owner of the LAN on the canvas.
func (a *app) ownerName() string {
	if l := a.snap.LAN(a.lan); l != nil {
		if p := a.people[string(l.Owner)]; p != nil {
			return p.name
		}
	}
	return ""
}

// clock is a count of hours, minutes and seconds: "2:14:08".
func clock(d time.Duration) string {
	d = max(d, 0).Truncate(time.Second)
	return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

// spot is where a device shows on the canvas: under which key, and in which
// LAN's region.
type spot struct {
	key string
	dev model.DeviceID
	lan model.LANID
}

// spots are the devices the canvas shows: those in the LAN on it, and those
// that wait to be let in (DESIGN §6.7).
func (a *app) spots() []spot {
	if a.all() {
		return a.allSpots()
	}
	l := a.snap.LAN(a.lan)
	if l == nil {
		return nil
	}
	var out []spot
	for _, id := range onCanvas(l) {
		out = append(out, spot{key: string(id), dev: id, lan: l.ID})
	}
	return out
}

// respot moves each device on the canvas to the region it is in now, as
// the snapshot has it.
func (a *app) respot() {
	at := map[string]model.LANID{}
	for _, sp := range a.spots() {
		at[sp.key] = sp.lan
	}
	for _, d := range a.devices {
		if lan, ok := at[d.id]; ok {
			d.lan = lan
		}
	}
}

// trayIDs are my devices the tray holds: those not in the LAN on the
// canvas; none in the "全部" view, which has no tray.
func (a *app) trayIDs() []model.DeviceID {
	if a.all() {
		return nil
	}
	return a.snap.Tray(a.lan)
}

// lanOf is the LAN a device on the canvas shows in, as the snapshot has it.
func (a *app) lanOf(d *device) *model.LAN { return a.snap.LAN(d.lan) }
