package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
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
	base := a.store.Snapshot()
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
	for _, d := range a.devices {
		a.facts(d, l)
	}
	for _, d := range a.tray {
		a.facts(d, l)
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
	a.relay.name = a.relayName(l)
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
	m := s.Device(model.DeviceID(d.id))
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
	refused := make(map[string]bool, len(m.Refused))
	for who, on := range m.Refused {
		if on {
			refused[string(who)] = true
		}
	}
	d.refused = refused
	in := l != nil && l.Has(m.ID)
	if in || d.path == "" {
		p := s.Path(m.ID)
		d.path, d.relayID, d.rtt = string(p.Kind), p.Relay, p.RTT
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

// relayName names the relay the LAN's links go through: the one the first of
// them uses, or else the one the LAN prefers.
func (a *app) relayName(l *model.LAN) string {
	var id model.RelayID
	for _, d := range a.devices {
		if d.path == "relay" && d.relayID != "" {
			id = d.relayID
			break
		}
	}
	if id == "" && l != nil && len(l.Relays) > 0 {
		id = l.Relays[0]
	}
	if r := a.snap.Relays[id]; r != nil {
		return r.Name
	}
	if a.relay.name != "" {
		return a.relay.name
	}
	return "中转站"
}

// load lays the LAN out on the canvas as it is, without motion: as the
// window opens, and as the sidebar switches to another LAN.
func (a *app) load() {
	l := a.snap.LAN(a.lan)
	a.devices, a.tray = nil, nil
	a.trayOff, a.slots = map[string]*ui.Point{}, map[string]ui.Point{}
	a.beams, a.geos = map[string]*beamRun{}, map[string]*linkGeo{}
	a.landing, a.held, a.toast = nil, nil, nil
	a.relay = &relayStop{sub: "中转站"}
	if at, ok := a.places.get(a.lan, relayKey); ok {
		a.relay.base = at
	} else {
		a.relay.unplaced = true
	}
	if l != nil {
		for _, id := range l.Devices {
			d := &device{facts: facts{id: string(id)}}
			if at, ok := a.places.get(a.lan, d.id); ok {
				d.base, d.rest = at, at
			} else {
				d.unplaced = true
			}
			a.devices = append(a.devices, d)
		}
	}
	if l != nil {
		for _, id := range a.snap.Tray(a.lan) {
			d := &device{facts: facts{id: string(id)}}
			a.tray = append(a.tray, d)
			a.trayOff[d.id] = &ui.Point{}
		}
	}
	a.refresh()
	for _, d := range a.devices {
		d.lastPath = d.path
	}
	// A relay there as the LAN shows is there: it does not pop in.
	a.relay.shown = a.relayLinked()
}

// show puts the LAN of id on the canvas. It changes what the window shows,
// and nothing else: every connection stays as it is. A LAN just made or
// joined here comes in (pop): its devices pop in one after the other, their
// tracks growing out from this computer (scene 3's 加入).
func (a *app) show(c *ui.Context, id model.LANID, pop bool) {
	if id == a.lan || a.snap.LAN(id) == nil {
		return
	}
	a.keepPlaces()
	a.lan = id
	a.selected, a.more = "", false
	a.load()
	// When the canvas switched, to the millisecond, against the engine's
	// timing of the connections it must not touch (acceptance step 4).
	log.Printf("deneb: canvas shows %s (%s) at %s", a.snap.LAN(id).Name, id, time.Now().Format("15:04:05.000"))
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
	l := a.snap.LAN(a.lan)
	if l == nil && a.lan == "" && len(a.snap.LANs) == 0 {
		return
	}
	if l == nil {
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
	for _, id := range l.Devices {
		in[string(id)] = true
	}
	tray := map[string]bool{}
	for _, id := range a.snap.Tray(a.lan) {
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
			a.startLeave(c, d, false, false)
		default:
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
	for _, id := range l.Devices {
		if !shown[string(id)] {
			a.appear(c, string(id))
		}
	}
	for _, id := range a.snap.Tray(a.lan) {
		if !shown[string(id)] {
			d := &device{facts: facts{id: string(id)}}
			a.facts(d, l)
			a.somebody(d.owner)
			a.tray = append(slices.Clone(a.tray), d)
			a.trayOff[d.id] = &ui.Point{}
		}
	}
	for _, d := range a.devices {
		if d.lastPath == "" {
			d.lastPath = d.path
		} else if d.path != d.lastPath {
			a.repath(c, d)
		}
	}
}

// turnBack sends a device on its way into the tray back to where it rested.
func (a *app) turnBack(d *device) {
	a.landing = nil
	d.leaving, d.aimed, d.flying = time.Time{}, false, true
	d.off = ui.Point{X: d.rest.X - d.base.X, Y: d.rest.Y - d.base.Y}
}

// appear puts a device that joined the LAN elsewhere on the canvas: where
// it rested here before, or at a free spot. It pops in there as its track
// grows out from this computer.
func (a *app) appear(c *ui.Context, id string) {
	d := &device{facts: facts{id: id}}
	a.facts(d, a.snap.LAN(a.lan))
	a.somebody(d.owner)
	if a.cw == 0 {
		d.unplaced = true
	} else {
		want, ok := a.places.get(a.lan, id)
		if !ok {
			want = a.placeNew(d)
		}
		spot := a.freeSpot(want, d, footprint, a.cw, a.ch)
		d.base, d.rest, d.live = spot, spot, spot
	}
	now := c.Now()
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
	if r := a.relay; r.unplaced && a.hasRelayLink() {
		me := a.device(a.self)
		var far *device
		for _, d := range a.devices {
			if d.path == "relay" {
				far = d
				break
			}
		}
		want := ui.Point{X: a.cw / 2, Y: a.ch / 2}
		if me != nil && far != nil {
			// Between the two, a little below, as scene 1 shows it.
			p, q := me.at(), far.at()
			want = ui.Point{X: (p.X + q.X) / 2, Y: (p.Y+q.Y)/2 + 30}
		}
		r.base = a.freeSpot(want, r, a.relayFootprint, a.cw, a.ch)
		r.off, r.unplaced = ui.Point{}, false
	}
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
	if !a.relay.unplaced && !a.relay.dragging {
		a.places.set(a.lan, relayKey, a.relay.at())
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
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) },
		func(c *ui.Context, r model.Result, err error) {
			p.done, p.ver, p.err = true, r.Version, err
			a.gen++
			if err != nil {
				a.problem(c, err)
			}
		})
	a.sync(nil)
}

// run does work off the main thread and hands its result to done on it; a
// store that answers at once (the demo's) is asked in place, so that what it
// answers shows in the same frame.
func run[T any](a *app, c *ui.Context, work func(context.Context) (T, error), done func(*ui.Context, T, error)) {
	if _, ok := a.store.(client.Immediate); ok {
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
const relayKey = "\x00relay"

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
