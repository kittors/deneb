package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// M3 (DESIGN §6.11): one of my devices given to one person, dropped on
// their ball in the header; the canvas of the devices given to me; and the
// seats. Every control here shows only where the server lists its feature.

const (
	personCard = "\x00person:" // a.selected for a person's card: this, then their id
	seatCard   = "\x00seats"   // a.selected for the seats' card
)

// shared reports whether the canvas is the one of the devices given to me.
func (a *app) shared() bool { return a.lan == model.SharedLANID }

// sharedFrom is where a device given to me comes from, as its second line
// says on the canvas of those: a LAN's own device, the LAN; any other, who
// gave it. "" for a device not given to me, or another canvas.
func (a *app) sharedFrom(d *device) string {
	if !a.shared() {
		return ""
	}
	sh := a.snap.SharedOf(d.dev)
	if sh == nil {
		return ""
	}
	if sh.FromLAN != "" {
		return short(sh.FromLAN)
	}
	if who := a.people[string(sh.From)]; who != nil {
		return who.name
	}
	return ""
}

// canShare reports whether d, in the hand, can be given to someone from
// here: one of mine, on a server that shares, on a canvas with balls to
// drop it on.
func (a *app) canShare(d *device) bool {
	return d != nil && d.mine && a.snap.Can(model.FeatureShare) && !a.all()
}

// shareAim is what the hand aims at, holding one of my devices up over the
// header's balls: who is under it, and whether it can go to them, or why
// not (DESIGN §6.11: what cannot go in says so before the hand lets go).
type shareAim struct {
	d        *device // in the hand: on the canvas, or out of the tray
	inHeader bool    // the hand is up in the header
	who      string  // whose ball it is over
	ok       bool
	why      string
}

// aimFor works out the aim for the device in the hand, with the pointer at
// (x, y) in the window and the balls where the last frame showed them.
func (a *app) aimFor(d *device, x, y float32) shareAim {
	aim := shareAim{d: d, inHeader: y < a.origin.Y}
	best := float32(24)
	for id, r := range a.ballRects {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		if dist := float32(math.Hypot(float64(x-cx), float64(y-cy))); dist < best {
			best, aim.who = dist, id
		}
	}
	if aim.who == "" {
		return aim
	}
	m := a.snap.Device(d.dev)
	name := a.people[aim.who].name
	switch {
	case m != nil && m.ShareTo(model.PersonID(aim.who)) != nil:
		aim.why = say(name, "已经有了")
	case m != nil && a.usesAlready(m, model.PersonID(aim.who)):
		// In a LAN with it, where they see it already.
		aim.why = say(name, "已经能用了")
	case a.seatsFull() && !a.seats().Holds(model.PersonID(aim.who)):
		aim.why = "位置满了"
	default:
		aim.ok = true
	}
	return aim
}

// takeAim sets this frame's aim for whichever of my devices the hand
// holds, on the canvas or out of the tray, by where the pointer is (cx, cy
// in the canvas): the tile stops at the canvas's side, the hand does not.
func (a *app) takeAim(cx, cy float32) {
	var held *device
	if d := a.drag.d; d != nil && d.dragging {
		held = d
	} else if a.trayDrag != nil {
		held = a.trayDrag
	}
	if !a.canShare(held) {
		a.aim = shareAim{}
		return
	}
	a.aim = a.aimFor(held, a.origin.X+cx, a.origin.Y+cy)
}

// usesAlready reports whether who sees m, one of my devices, already: they
// are in a LAN it is in, one where members see each other's devices, and I
// do not refuse them on it. Giving it to them would add nothing.
func (a *app) usesAlready(m *model.Device, who model.PersonID) bool {
	if m.Refused[who] {
		return false
	}
	for _, l := range a.snap.LANs {
		if l.Mode != model.ModeShare && l.Has(m.ID) && slices.Contains(l.Members, who) {
			return true
		}
	}
	return false
}

// shareTop is how high a tile's middle can go in the canvas while the hand
// holds it: up into the header, to the balls, for one of mine on a server
// that shares; else the canvas's own top.
func (a *app) shareTop(d *device) float32 {
	if a.canShare(d) {
		return -46
	}
	return 44
}

// upHigh reports whether the hand holds d up in the header, over the balls:
// its tile is small there, its name gone, its link faint.
func (a *app) upHigh(d *device) bool { return a.aim.d == d && a.aim.inHeader }

// aimLine is what the line under the header's balls says while the hand
// holds one of my devices: what letting go does, over whom.
func (a *app) aimLine() string {
	aim := a.aim
	switch {
	case aim.d == nil:
		return ""
	case aim.who == "":
		return "放到谁身上，就只给谁用这一台"
	case aim.ok:
		return say("松手，只给", a.people[aim.who].name, "用这一台")
	}
	return aim.why
}

// dropOnBall is what letting go of one of my devices does where the aim
// says: given to whoever's ball it is over, when it can be. It reports
// whether the device goes back where it came from: let go up in the header,
// on a ball or not, it does.
func (a *app) dropOnBall(c *ui.Context, d *device) bool {
	aim := a.aim
	a.aim = shareAim{}
	if aim.d != d || !aim.inHeader && aim.who == "" {
		return false
	}
	if aim.ok {
		a.shareTo(c, d, aim.who)
	}
	return true
}

// shareTo gives d, one of mine, to who, opening what it opens to its LANs
// now (DESIGN §6.11); their ball springs. Undo takes it back at once, its
// seat with it.
func (a *app) shareTo(c *ui.Context, d *device, who string) {
	pid := model.PersonID(who)
	a.send(c, model.Op{Kind: model.OpShareCreate, Device: d.dev, Person: pid, Services: a.snap.OpenOf(d.dev)})
	if a.popAt == nil {
		a.popAt = map[string]time.Time{}
	}
	a.popAt[who] = c.Now()
	dev := d.dev
	a.notify(c, d, say("已把", short(d.name), "分享给", a.people[who].name), func() {
		a.askConfirm("撤回共享", d.name, a.people[who].name, func(c *ui.Context) {
			a.sendAfter(c, model.Op{Kind: model.OpShareRevoke, Device: dev, Person: pid}, model.Op{Kind: model.OpShareRelease, Device: dev, Person: pid})
		})
	})
}

// revokeShare takes dev back from who at once. The toast offers undo,
// which gives it back as it was; once the toast goes without it, the
// server hears that the seat it kept can go (share.release).
func (a *app) revokeShare(c *ui.Context, dev model.DeviceID, who string) {
	m := a.snap.Device(dev)
	if m == nil {
		return
	}
	a.askConfirm("收回共享", m.Name, "取消分享给 "+a.people[who].name+" 的访问。", func(c *ui.Context) { a.revokeShareNow(c, dev, who) })
}
func (a *app) revokeShareNow(c *ui.Context, dev model.DeviceID, who string) {
	m := a.snap.Device(dev)
	if m == nil {
		return
	}
	pid := model.PersonID(who)
	id := ""
	if sh := m.ShareTo(pid); sh != nil {
		id = sh.ID
	}
	a.send(c, model.Op{Kind: model.OpShareRevoke, Share: id, Device: dev, Person: pid})
	if a.shareOpen == string(dev)+"/"+who {
		a.shareOpen = ""
	}
	a.notify(c, a.deviceAnywhere(dev), say("已从", a.people[who].name, "收回", a.nameOf(m)), func() {
		a.send(c, model.Op{Kind: model.OpShareCreate, Device: dev, Person: pid})
	})
	a.toast.commit = func(c *ui.Context) {
		a.send(c, model.Op{Kind: model.OpShareRelease, Share: id, Device: dev, Person: pid})
	}
}

// leaveShare gives back a device given to me ("不要了"). It goes at once
// and the toast offers undo; the server hears of it as the toast goes.
func (a *app) leaveShare(c *ui.Context, d *device) {
	a.askConfirm("取消共享", d.name, "此设备将从共享列表移除。", func(c *ui.Context) { a.leaveShareNow(c, d) })
}
func (a *app) leaveShareNow(c *ui.Context, d *device) {
	sh := a.snap.SharedOf(d.dev)
	if sh == nil {
		return
	}
	if a.leaving == nil {
		a.leaving = map[model.DeviceID]bool{}
	}
	a.leaving[d.dev] = true
	a.selected, a.more = "", false
	a.hold(c, model.Op{Kind: model.OpShareLeave, Share: sh.Share, Device: d.dev}, d, say("已拿掉", short(d.name)))
	dev, undo := d.dev, a.toast.undo
	a.toast.undo = func() {
		undo()
		delete(a.leaving, dev)
	}
}

// freeSeat takes who out of every LAN of mine and back from every device
// I gave them, to free their seat (DESIGN §6.11). Their ball and their
// devices go at once; the server hears of it only as the toast goes, so
// that undo is nothing done, and the seat stays theirs until then.
func (a *app) freeSeat(c *ui.Context, who string) {
	a.askConfirm("移出成员", a.people[who].name, "移出所有自有局域网，并收回向其共享的设备。", func(c *ui.Context) { a.freeSeatNow(c, who) })
}
func (a *app) freeSeatNow(c *ui.Context, who string) {
	pid := model.PersonID(who)
	var ops []model.Op
	for _, l := range a.snap.LANs {
		if l.Owner == a.snap.Me && slices.Contains(l.Members, pid) {
			ops = append(ops, model.Op{Kind: model.OpMemberRemove, LAN: l.ID, Person: pid})
		}
	}
	for _, id := range a.snap.Mine {
		if m := a.snap.Device(id); m != nil {
			if sh := m.ShareTo(pid); sh != nil {
				ops = append(ops, model.Op{Kind: model.OpShareRevoke, Share: sh.ID, Device: id, Person: pid},
					model.Op{Kind: model.OpShareRelease, Share: sh.ID, Device: id, Person: pid})
			}
		}
	}
	if a.freeing == nil {
		a.freeing = map[string]bool{}
	}
	a.freeing[who] = true
	a.holdAll(c, ops, nil, say("已移出", a.people[who].name), func() { delete(a.freeing, who) })
}

// holdAll is hold for several ops at once, sent one after another as the
// toast goes; undo drops them all, unsent, and then runs undone.
func (a *app) holdAll(c *ui.Context, ops []model.Op, d *device, text string, undone func()) {
	ps := make([]*pending, len(ops))
	for i, op := range ops {
		ps[i] = &pending{op: op, held: true}
	}
	a.pend = append(a.pend, ps...)
	a.gen++
	a.sync(nil)
	a.notify(c, d, text, func() {
		a.pend = slices.DeleteFunc(a.pend, func(x *pending) bool { return slices.Contains(ps, x) })
		a.gen++
		if undone != nil {
			undone()
		}
	})
	a.toast.commit = func(c *ui.Context) { a.commitChain(c, ps) }
}

// commitChain hands the store one held op after another, each once the one
// before went through; one turned down drops the rest.
func (a *app) commitChain(c *ui.Context, ps []*pending) {
	for len(ps) > 0 && !ps[0].held {
		ps = ps[1:]
	}
	if len(ps) == 0 {
		return
	}
	p, rest := ps[0], ps[1:]
	p.held = false
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, p.op) },
		func(c *ui.Context, r model.Result, err error) {
			p.done, p.ver, p.err = true, r.Version, err
			a.gen++
			if err != nil {
				a.pend = slices.DeleteFunc(a.pend, func(x *pending) bool { return slices.Contains(rest, x) })
				a.problem(c, err)
				return
			}
			a.commitChain(c, rest)
		})
}

// seats are my seats as the view counts them: the store's, less the people
// I am freeing a seat of while their toast offers undo.
func (a *app) seats() model.Seats {
	s := a.snap.Seats
	if !s.Enforced || len(a.freeing) == 0 {
		return s
	}
	n := len(s.People)
	s.People = slices.DeleteFunc(slices.Clone(s.People), func(p model.PersonID) bool { return a.freeing[string(p)] })
	s.Used -= n - len(s.People)
	s.Left = max(s.Limit-s.Used, 0)
	return s
}

// seatsFull reports whether no one new finds a seat of mine, on a server
// that holds to the seats.
func (a *app) seatsFull() bool { return a.snap.Can(model.FeatureSeats) && a.seats().Full() }

// seatLine is the small line under "邀请朋友": how many more people, when
// only one or two (pricing-and-relays §1.5 item 1); "" otherwise.
func (a *app) seatLine() string {
	s := a.seats()
	if !a.snap.Can(model.FeatureSeats) || !s.Enforced || s.Left > 2 || s.Left <= 0 {
		return ""
	}
	return "还能加 " + strconv.Itoa(s.Left) + " 人"
}

// fullFor reports whether bringing who into a LAN of mine finds no seat:
// mine full, and they hold none of it yet.
func (a *app) fullFor(lan model.LANID, who model.PersonID) bool {
	l := a.snap.LAN(lan)
	return l != nil && l.Owner == a.snap.Me && a.seatsFull() && !a.seats().Holds(who)
}

// freed forgets the people freed whose seat the store no longer counts.
func (a *app) freed() {
	for who := range a.freeing {
		if !a.base.Seats.Holds(model.PersonID(who)) {
			delete(a.freeing, who)
		}
	}
}

// deviceAnywhere is the device of id shown on the canvas or in the tray,
// or nil.
func (a *app) deviceAnywhere(id model.DeviceID) *device {
	for _, d := range a.devices {
		if d.dev == id {
			return d
		}
	}
	for _, d := range a.tray {
		if d.dev == id {
			return d
		}
	}
	return nil
}

// watchShares follows the devices given to me: one that comes lights its
// row in the sidebar up; one whoever gave it took back says so, on its
// canvas (DESIGN §6.11: nothing goes without a word).
func (a *app) watchShares(c *ui.Context) {
	now := map[model.DeviceID]bool{}
	for _, sh := range a.base.Shared {
		now[sh.Device] = true
	}
	known := a.knownShared
	a.knownShared = now
	if known == nil || !a.base.Loaded {
		return
	}
	for id := range now {
		if !known[id] && a.flashFrom != nil {
			a.flashFrom[model.SharedLANID] = time.Time{}
		}
	}
	for id := range known {
		if now[id] {
			continue
		}
		if a.leaving[id] {
			delete(a.leaving, id)
			continue
		}
		if !a.shared() {
			continue
		}
		d := a.deviceAnywhere(id)
		if d == nil {
			continue
		}
		a.notify(c, d, say(a.people[d.owner].name, "收回了", short(d.name)), nil)
	}
}

// sharedSummary is the line under "共享给我": how many devices and whether
// they are all connected, as a LAN's; with none, where they will come.
func (a *app) sharedSummary(p *palette) (ui.Color, string) {
	n, up := 0, 0
	for _, d := range a.devices {
		if !d.gone.IsZero() || d.id == a.self {
			continue
		}
		n++
		if linked(d.path) {
			up++
		}
	}
	switch {
	case n == 0 && len(a.snap.ShareLANs) > 0:
		l := a.snap.ShareLANs[0]
		return p.ink3, say("已加入", short(l.Name), "，", a.personName(l.Owner), "分给你的设备会出现在这里")
	case n == 0:
		return p.ink3, "别人分给你的设备会出现在这里"
	case up == n && n == 1:
		return p.ok, "1 台设备，连上了"
	case up == n:
		return p.ok, fmt.Sprintf("%d 台设备都连上了", n)
	}
	return p.ink3, fmt.Sprintf("%d 台设备，%d 台连上了", n, up)
}

// nameOf is what the view calls one of the snapshot's devices: this one is
// "这台电脑".
func (a *app) nameOf(m *model.Device) string {
	if m.ID == a.snap.Self {
		return "这台电脑"
	}
	return short(m.Name)
}

// upScale is how large a tile held up in the header is, against a tile of
// the canvas: small, and over a ball smaller than the ball, so that the
// ball, and its ring, show round it.
func (a *app) upScale() float32 {
	if a.aim.who != "" {
		return 0.38
	}
	return 0.6
}

// textWidth is about how wide s shows at size: a Latin letter a little
// over half the size, any other the size (as nameBox reckons it).
func textWidth(s string, size float32) float32 {
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

// snapTo is how far a tile held up in the header, its middle at tile in the
// window, moves to sit in the ball the hand is over: none off the balls.
func (a *app) snapTo(tile ui.Point) ui.Point {
	r, ok := a.ballRects[a.aim.who]
	if a.aim.who == "" || !ok {
		return ui.Point{}
	}
	return ui.Point{X: r.X + r.W/2 - tile.X, Y: r.Y + r.H/2 - tile.Y}
}
