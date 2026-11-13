package control

import (
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// Managing relays (the plan's §16): who is on them and how loaded they
// are, weights that spread a LAN's devices over its relays, and kicks.

// relayKick is someone kicked off a relay: a person (all their devices)
// or one device. Kept after it is lifted, for the record.
type relayKick struct {
	ID       string
	Relay    proto.RelayID
	Person   proto.PersonID
	Device   proto.DeviceID
	By       proto.PersonID // empty: the deployment's admin
	At       time.Time
	LiftedAt time.Time
	LiftedBy proto.PersonID
}

func (k *relayKick) active() bool { return k.LiftedAt.IsZero() }

// indexKick puts a kick in force in the world's indexes.
func (w *World) indexKick(k *relayKick) {
	if !k.active() {
		return
	}
	if k.Person != "" {
		if w.kickedPerson[k.Relay] == nil {
			w.kickedPerson[k.Relay] = map[proto.PersonID]bool{}
		}
		w.kickedPerson[k.Relay][k.Person] = true
	}
	if k.Device != "" {
		if w.kickedDevice[k.Relay] == nil {
			w.kickedDevice[k.Relay] = map[proto.DeviceID]bool{}
		}
		w.kickedDevice[k.Relay][k.Device] = true
	}
}

// kicked reports whether d is off relay id: it, or its owner, was kicked.
func (w *World) kicked(id proto.RelayID, d *device) bool {
	return w.kickedDevice[id][d.ID] || w.kickedPerson[id][d.Owner]
}

// Weights. Each device's list of a scope's relays is its own: within the
// relays dragged in, and again within the public ones, the order comes
// from weighted rendezvous hashing of the device and each relay, so a
// 3:1 pair puts about three devices in four on the first, and a device's
// choice changes only when the relays or their weights or health do.
// The engine connects to the first it can: the order is the assignment.

// rendezvous is relay id's score for device d with weight weight: the
// highest score goes first.
func rendezvous(d proto.DeviceID, id proto.RelayID, weight int) float64 {
	h := sha256.Sum256([]byte(string(d) + "\x00" + string(id)))
	// A uniform number in (0, 1) from the hash, and -weight/ln(u): the
	// weighted rendezvous score.
	u := (float64(binary.BigEndian.Uint64(h[:8])>>11) + 0.5) / (1 << 53)
	return float64(weight) / -math.Log(u)
}

// weight is relay id's weight; a relay with no row of settings (a test
// world's) weighs 1.
func (w *World) weight(id proto.RelayID) int {
	if m := w.relayMeta[id]; m != nil {
		return m.Weight
	}
	return 1
}

// spread sorts relays for device d into those up, by weighted score;
// the standbys (weight 0); and those down: offline, or full unless d is
// on it already (a full relay takes no one new, and sends no one away).
// Ties, and the last two groups, keep the order they came in.
func (w *World) spread(d *device, relays []proto.RelayID) (up, standby, down []proto.RelayID) {
	type scored struct {
		id    proto.RelayID
		score float64
	}
	var ups []scored
	for _, id := range relays {
		switch wt := w.weight(id); {
		case w.relayOffline != nil && w.relayOffline(id),
			w.relayFull != nil && w.relayFull(id) && !slices.Contains(d.Relays, id):
			down = append(down, id)
		case wt <= 0:
			standby = append(standby, id)
		default:
			ups = append(ups, scored{id, rendezvous(d.ID, id, wt)})
		}
	}
	slices.SortStableFunc(ups, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	for _, u := range ups {
		up = append(up, u.id)
	}
	return up, standby, down
}

// listFor is device d's list for a scope whose own relays are own: own's
// registered relays spread by weight, then the fallback (the defaults in
// the deployment's order, then the public relays spread by weight), then
// the relays down, leaving out the relays d is kicked off. A device whose
// own relay is down tries the public ones first, and is back on it when
// it is up again.
func (w *World) listFor(d *device, own []proto.RelayID) []proto.RelayID {
	seen := map[proto.RelayID]bool{}
	pick := func(ids []proto.RelayID) []proto.RelayID {
		var out []proto.RelayID
		for _, id := range ids {
			if ready(w.relays[id]) && !seen[id] && !w.kicked(id, d) {
				seen[id] = true
				out = append(out, id)
			}
		}
		return out
	}
	ownUp, ownStandby, ownDown := w.spread(d, pick(own))
	defaults := pick(w.defaults)
	var public []proto.RelayID
	for _, id := range w.fallback {
		if !slices.Contains(w.defaults, id) {
			public = append(public, id)
		}
	}
	pubUp, pubStandby, pubDown := w.spread(d, pick(public))
	out := []proto.RelayID{}
	for _, part := range [][]proto.RelayID{ownUp, ownStandby, defaults, pubUp, pubStandby, ownDown, pubDown} {
		out = append(out, part...)
	}
	return out
}

// shareOwn are the relays a share's list starts with: the sharer's own,
// but not a default relay they happen to own, which is the deployment's.
func (w *World) shareOwn(sh *share) []proto.RelayID {
	return slices.DeleteFunc(w.ownRelays(sh.Sharer), func(id proto.RelayID) bool { return slices.Contains(w.defaults, id) })
}

// Relay ops for its owner, and for the deployment's operators on a public
// relay (relayToManage).

const maxRelayWeight = 100

// maxRelayCapacity bounds relay.setCapacity: devices at once.
const maxRelayCapacity = 100000

func opRelaySetWeight(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.relayToManage(a.Relay, "调权重", false)
	if err != nil {
		return err
	}
	if a.Weight == nil || *a.Weight < 0 || *a.Weight > maxRelayWeight {
		return errInvalid(fmt.Sprintf("权重要在 0 到 %d 之间", maxRelayWeight))
	}
	if c.w.weight(rl.ID) == *a.Weight {
		return errNoChange
	}
	if err := c.exec(`UPDATE relays SET weight = ? WHERE id = ?`, *a.Weight, rl.ID); err != nil {
		return err
	}
	return c.recordOps(rl, "relay.setWeight", fmt.Sprintf("把公共中转站「%s」的份额调成了 %d", rl.Name, *a.Weight))
}

func opRelaySetCapacity(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.relayToManage(a.Relay, "设容量", false)
	if err != nil {
		return err
	}
	if a.Capacity == nil || *a.Capacity < 0 || *a.Capacity > maxRelayCapacity {
		return errInvalid(fmt.Sprintf("容量要在 0 到 %d 之间，0 是不限", maxRelayCapacity))
	}
	if m := c.w.relayMeta[rl.ID]; m != nil && m.Capacity == *a.Capacity {
		return errNoChange
	}
	if err := c.exec(`UPDATE relays SET capacity = ? WHERE id = ?`, *a.Capacity, rl.ID); err != nil {
		return err
	}
	text := fmt.Sprintf("把公共中转站「%s」同时最多调成了 %d 台", rl.Name, *a.Capacity)
	if *a.Capacity == 0 {
		text = fmt.Sprintf("把公共中转站「%s」改成了不限台数", rl.Name)
	}
	return c.recordOps(rl, "relay.setCapacity", text)
}

func opRelayKick(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.relayToManage(a.Relay, "踢人", true)
	if err != nil {
		return err
	}
	id, err := kick(c.tx, c.w, rl.ID, a.Person, a.Device, c.me, c.now)
	if err != nil {
		return err
	}
	out.Kick = id
	return c.recordOps(rl, "relay.kick", "不让"+c.w.kickedName(a.Person, a.Device)+"用公共中转站「"+rl.Name+"」")
}

// kickedName is who a kick names, as the record says it: a person's name, or
// a device's with its owner's.
func (w *World) kickedName(person proto.PersonID, device proto.DeviceID) string {
	if p := w.people[person]; p != nil {
		return p.DisplayName
	}
	if d := w.devices[device]; d != nil {
		if o := w.people[d.Owner]; o != nil {
			return o.DisplayName + "的「" + d.DisplayName + "」"
		}
		return "「" + d.DisplayName + "」"
	}
	return string(person) + string(device)
}

func opRelayUnkick(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	k := c.w.kicks[a.Kick]
	if k == nil || !k.active() {
		return errNotFound("找不到这次踢人，或者已经撤销了")
	}
	rl := c.w.relays[k.Relay]
	switch {
	case rl == nil:
		return errNotFound("找不到这次踢人，或者已经撤销了")
	case rl.Source == proto.RelayPublic:
		// The operators' to lift, whoever of them kicked.
		if !c.operator() {
			return errNotFound("找不到这次踢人，或者已经撤销了")
		}
	case rl.Owner != c.me:
		return errNotFound("找不到这次踢人，或者已经撤销了")
	}
	if err := unkick(c.tx, k.ID, c.me, c.now); err != nil {
		return err
	}
	return c.recordOps(rl, "relay.unkick", "又让"+c.w.kickedName(k.Person, k.Device)+"用公共中转站「"+rl.Name+"」了")
}

// kick writes a kick of person or device off relay, by by (empty: the
// deployment's admin).
func kick(tx *sql.Tx, w *World, relay proto.RelayID, person proto.PersonID, device proto.DeviceID, by proto.PersonID, now time.Time) (string, error) {
	switch {
	case (person == "") == (device == ""):
		return "", errInvalid("踢人要给一个人或者一台设备")
	case person != "" && w.people[person] == nil:
		return "", errNotFound("没有这个人")
	case device != "" && w.devices[device] == nil:
		return "", errNotFound("没有这台设备")
	case person != "" && w.kickedPerson[relay][person], device != "" && w.kickedDevice[relay][device]:
		return "", errNoChange
	}
	id := newID("k")
	_, err := tx.Exec(`INSERT INTO relay_kicks (id, relay, person, device, by_person, at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, relay, person, device, by, ms(now))
	return id, err
}

func unkick(tx *sql.Tx, id string, by proto.PersonID, now time.Time) error {
	_, err := tx.Exec(`UPDATE relay_kicks SET lifted_at = ?, lifted_by = ? WHERE id = ?`, ms(now), by, id)
	return err
}

// kicksOf are relay id's kicks in force and those lifted since since,
// newest first.
func (w *World) kicksOf(id proto.RelayID, since time.Time) []proto.RelayKick {
	var out []proto.RelayKick
	for _, k := range w.kicks {
		if k.Relay == id && (k.active() || k.LiftedAt.After(since)) {
			out = append(out, proto.RelayKick{ID: k.ID, Person: k.Person, Device: k.Device, By: k.By, At: k.At, LiftedAt: k.LiftedAt, LiftedBy: k.LiftedBy})
		}
	}
	slices.SortFunc(out, func(a, b proto.RelayKick) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// Load. A relay's last sync says who is on it and how it is doing; the
// server keeps the last one in memory, like a device being online.

// relayLoad is a relay's last sync, as relayLive keeps it.
type relayLoad struct {
	at        time.Time
	health    proto.RelayHealth
	connected []proto.NodePublic
	devices   int
	people    int
}

// Full: at capacity or near the CPU it may use. Back below 90% of
// capacity and 75% CPU it is not, so it does not flip with every client.
const (
	fullCPU      = 0.9
	fullCPUUnder = 0.75
	fullCapUnder = 0.9
)

// isFull is whether a relay with this load, capacity and state before is
// full now.
func isFull(was bool, devices, capacity int, cpu float64) bool {
	if !was {
		return capacity > 0 && devices >= capacity || cpu >= fullCPU
	}
	return capacity > 0 && float64(devices) >= fullCapUnder*float64(capacity) || cpu >= fullCPUUnder
}

// loadOf counts the devices and the people among a sync's node keys.
func (w *World) loadOf(connected []proto.NodePublic) (devices, people int) {
	ps := map[proto.PersonID]bool{}
	ds := map[proto.DeviceID]bool{}
	for _, k := range connected {
		if d := w.byNodeKey[k]; d != nil && !ds[d.ID] {
			ds[d.ID] = true
			ps[d.Owner] = true
		}
	}
	return len(ds), len(ps)
}

// The relays' watch: a relay waits on the version, and syncs when it
// moves.

// versionWaiter lets a relay wait for the next change.
type versionWaiter struct {
	mu sync.Mutex
	ch chan struct{} // closed at the next change
}

func newVersionWaiter() *versionWaiter { return &versionWaiter{ch: make(chan struct{})} }

// next is a channel the next change closes.
func (v *versionWaiter) next() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ch
}

// changed wakes every waiter.
func (v *versionWaiter) changed() {
	v.mu.Lock()
	close(v.ch)
	v.ch = make(chan struct{})
	v.mu.Unlock()
}

// relayWatchWait is how long a watch waits before answering anyway.
var relayWatchWait = 30 * time.Second

func (s *Server) handleRelayWatch(w http.ResponseWriter, r *http.Request, rl *proto.Relay, _ *World) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	wait := time.NewTimer(relayWatchWait)
	defer wait.Stop()
	for {
		next := s.versions.next()
		if v := s.Version(); v > since {
			writeJSON(w, http.StatusOK, proto.RelayWatchResult{Version: v})
			return
		}
		select {
		case <-next:
		case <-wait.C:
			writeJSON(w, http.StatusOK, proto.RelayWatchResult{Version: s.Version()})
			return
		case <-r.Context().Done():
			return
		}
	}
}
