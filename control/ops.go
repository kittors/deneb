package control

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kittors/deneb/proto"
)

// Who may do what is the table in implementation.md §2.2. Only the server
// judges it; a refusal comes back as one sentence for the toast.

// inviteTTL is how long an invite link works.
const inviteTTL = 7 * 24 * time.Hour

// opCache answers a retried op with its first answer. Entries live ten
// minutes, long enough for an engine to retry across a reconnect. It is
// guarded by Server.mu, which every op holds.
type opCache struct {
	entries map[string]opEntry
	swept   time.Time
}

type opEntry struct {
	status int
	body   []byte
	at     time.Time
}

const opCacheTTL = 10 * time.Minute

func (c *opCache) get(key string, now time.Time) (opEntry, bool) {
	// Answers that ran out go at most once a minute: every op holds
	// Server.mu, and a sweep on each would make a burst of ops cost the
	// square of its size (M7 review).
	if now.Sub(c.swept) >= time.Minute || now.Before(c.swept) {
		c.swept = now
		for k, e := range c.entries {
			if now.Sub(e.at) > opCacheTTL {
				delete(c.entries, k)
			}
		}
	}
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) > opCacheTTL {
		return opEntry{}, false
	}
	return e, true
}

func (c *opCache) put(key string, e opEntry) {
	if c.entries == nil {
		c.entries = map[string]opEntry{}
	}
	c.entries[key] = e
}

func (s *Server) handleOps(w http.ResponseWriter, r *http.Request, c *caller) {
	var op proto.Op
	if err := readJSON(w, r, &op); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if op.ID == "" || len(op.ID) > 128 {
		s.writeErr(w, r, errInvalid("操作缺少 id，或者 id 太长"))
		return
	}
	key := string(c.dev.ID) + "\x00" + op.ID
	now := s.now()

	s.mu.Lock()
	e, ok := s.ops.get(key, now)
	if !ok {
		e = s.applyOp(c.dev.ID, "", op)
		if e.status < 500 {
			s.ops.put(key, e)
		}
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	w.Write(e.body)
}

// applyOp runs one op for the person logged in on device from, or, from
// a browser (/api/v1), for person with no device; s.mu is held.
func (s *Server) applyOp(from proto.DeviceID, person proto.PersonID, op proto.Op) opEntry {
	now := s.now()
	var out proto.OpOutput
	var ctx *opCtx
	before := s.state.Load().v
	v, err := s.changeLocked(from, func(tx *sql.Tx, w *World) error {
		var dev *device
		if from != "" {
			dev = w.devices[from]
			if dev == nil || !dev.usable() {
				return newErr(http.StatusForbidden, proto.ErrNotLoggedIn, "这台设备还没登录")
			}
			person = dev.Owner
		} else if w.people[person] == nil {
			return newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "请先登录")
		}
		h := opHandlers[op.Op]
		if h == nil {
			return errInvalid("不认识的操作：" + string(op.Op))
		}
		ctx = &opCtx{s: s, tx: tx, w: w, dev: dev, me: person, now: now}
		return h(ctx, op.Args, &out)
	})
	if err == nil && ctx != nil {
		for _, f := range ctx.after {
			f()
		}
	}
	var body bytes.Buffer
	if err != nil {
		var ae *apiError
		if !errors.As(err, &ae) {
			s.logf("op %s from %s: %v", op.Op, from, err)
			ae = newErr(http.StatusInternalServerError, proto.ErrInternal, "服务器出了点问题，请稍后再试")
		}
		json.NewEncoder(&body).Encode(proto.ErrorBody{Error: &ae.body})
		return opEntry{status: ae.status, body: body.Bytes(), at: now}
	}
	if v == before {
		// Nothing changed (errNoChange): the answer is the newest version
		// the asker's streams were handed, one it has or is about to get.
		// The current version may be someone else's change that never
		// reaches it, and an engine waiting for that would wait in vain
		// (Engine.Do gives up after 3 s); a change that does reach it, as
		// another device's to the same switch a moment before, is in it,
		// so the asker still waits for that one.
		s.logf("op %s from %s: no change", op.Op, from)
		v = s.hub.lastSent(from, person)
	} else {
		s.logf("op %s from %s: version %d", op.Op, from, v)
	}
	res := proto.OpResult{Version: v}
	if out != (proto.OpOutput{}) {
		res.Result = &out
	}
	json.NewEncoder(&body).Encode(res)
	return opEntry{status: http.StatusOK, body: body.Bytes(), at: now}
}

// opCtx is what an op handler works with: the transaction, the world as it
// was when the op began, and who asks.
type opCtx struct {
	s   *Server
	tx  *sql.Tx
	w   *World
	dev *device        // the device the op came from
	me  proto.PersonID // whose it is
	now time.Time
	// after runs once the change is in the new world, for what must not
	// happen before it is: waking someone who will look at it.
	after []func()
}

func (c *opCtx) exec(q string, args ...any) error {
	_, err := c.tx.Exec(q, args...)
	return err
}

// lan finds a LAN the asking person is in. A LAN they are not in does not
// exist for them, so the answer is the same as for a LAN that does not.
func (c *opCtx) lan(id proto.LANID) (*lan, error) {
	l := c.w.lans[id]
	if l == nil || !l.joined(c.me) {
		return nil, errNotFound("找不到这个局域网")
	}
	return l, nil
}

// ownDevice finds one of the asking person's devices. For someone else's
// device they can see, the refusal names its owner: "这是阿杰的设备，只有
// 阿杰能移出" (DESIGN §6.2).
func (c *opCtx) ownDevice(id proto.DeviceID, verb string) (*device, error) {
	d := c.w.devices[id]
	if d == nil || d.LoggedOut {
		return nil, errNotFound("找不到这台设备")
	}
	if l := c.w.lans[d.PublicLAN]; d.PublicLAN != "" && l != nil {
		// A public device is its LAN's owner's to manage (§0.2).
		if l.Owner == c.me {
			return d, nil
		}
		if !c.sees(d) {
			return nil, errNotFound("找不到这台设备")
		}
		return nil, errForbidden("这是「" + l.Name + "」的公共设备，只有拥有者能" + verb)
	}
	if d.Owner == c.me {
		return d, nil
	}
	if !c.sees(d) {
		return nil, errNotFound("找不到这台设备")
	}
	name := "别人"
	if p := c.w.people[d.Owner]; p != nil {
		name = p.DisplayName
	}
	return nil, errForbidden("这是" + spaced(name) + "的设备，只有" + spaced(name) + "能" + verb)
}

// deletableDevice finds a device the asking person may delete: theirs, or
// a public device of a LAN they own, logged in or not. A device logged
// out is in no one else's view, so for the others it does not exist.
func (c *opCtx) deletableDevice(id proto.DeviceID) (*device, error) {
	d := c.w.devices[id]
	if d == nil {
		return nil, errNotFound("找不到这台设备")
	}
	if d.LoggedOut {
		if c.w.manager(d) != c.me {
			return nil, errNotFound("找不到这台设备")
		}
		return d, nil
	}
	return c.ownDevice(id, "删除")
}

func opDeviceDelete(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.deletableDevice(a.Device)
	if err != nil {
		return err
	}
	me := c.me
	c.after = append(c.after, func() {
		c.s.logf("device %s (%s, %q) deleted by %s", d.ID, d.Name, d.DisplayName, me)
	})
	return deleteDevice(c.tx, d, c.now)
}

// sees reports whether the asking person sees device d: it is in one of
// their LANs.
func (c *opCtx) sees(d *device) bool {
	for l := range d.lans {
		if c.w.lans[l] != nil && c.w.lans[l].joined(c.me) {
			return true
		}
	}
	return false
}

func (c *opCtx) ownerOnly(l *lan, msg string) error {
	if l.Owner != c.me {
		return errForbidden(msg)
	}
	return nil
}

// spaced puts spaces around a name in Latin letters, as Chinese text sets
// them apart: "这是 Mia 的设备", but "这是阿杰的设备".
func spaced(name string) string {
	if r, _ := utf8.DecodeRuneInString(name); r < 0x80 && name != "" {
		return " " + name + " "
	}
	return name
}

// checkName cleans up a name people gave a LAN or a device.
func checkName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errInvalid("名字不能是空的")
	}
	if utf8.RuneCountInString(s) > 40 {
		return "", errInvalid("名字最多 40 个字")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", errInvalid("名字里有不能用的字符")
		}
	}
	return s, nil
}

type opHandler func(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error

var opHandlers = map[proto.OpType]opHandler{
	proto.OpLANCreate:       opLANCreate,
	proto.OpLANRename:       opLANRename,
	proto.OpLANDelete:       opLANDelete,
	proto.OpLANInvite:       opLANInvite,
	proto.OpInviteAccept:    opInviteAccept,
	proto.OpInviteDecline:   opInviteDecline,
	proto.OpInviteRevoke:    opInviteRevoke,
	proto.OpLANAddDevice:    opLANAddDevice,
	proto.OpLANRemoveDevice: opLANRemoveDevice,
	proto.OpServiceSet:      opServiceSet,
	proto.OpDeviceRefuse:    opDeviceRefuse,
	proto.OpDeviceRename:    opDeviceRename,
	proto.OpDeviceSetKind:   opDeviceSetKind,
	proto.OpMemberRemove:    opMemberRemove,
	proto.OpLANLeave:        opLANLeave,

	proto.OpLANSetApproveDevices: opLANSetApproveDevices,
	proto.OpLANSetApproveMembers: opLANSetApproveMembers,
	proto.OpDeviceApprove:        opDeviceApprove,
	proto.OpDeviceReject:         opDeviceReject,
	proto.OpMemberApprove:        opMemberApprove,
	proto.OpMemberReject:         opMemberReject,
	proto.OpDeviceRenew:          opDeviceRenew,
	proto.OpDeviceDelete:         opDeviceDelete,
	proto.OpDeviceSetDNSName:     opDeviceSetDNSName,
	proto.OpAccountDelete:        opAccountDelete,

	proto.OpShareCreate:     opShareCreate,
	proto.OpShareRevoke:     opShareRevoke,
	proto.OpShareRelease:    opShareRelease,
	proto.OpShareLeave:      opShareLeave,
	proto.OpDeviceSetPublic: opDeviceSetPublic,
	proto.OpLANSetMode:      opLANSetMode,

	proto.OpRelayCreate: opRelayCreate,
	proto.OpRelayToken:  opRelayToken,
	proto.OpRelayRename: opRelayRename,
	proto.OpRelayDelete: opRelayDelete,
	proto.OpRelayAdd:    opRelayAdd,
	proto.OpRelayRemove: opRelayRemove,

	proto.OpRelayJoin:        opRelayJoin,
	proto.OpRelayRevokeJoin:  opRelayRevokeJoin,
	proto.OpRelaySetWeight:   opRelaySetWeight,
	proto.OpRelaySetCapacity: opRelaySetCapacity,
	proto.OpRelayKick:        opRelayKick,
	proto.OpRelayUnkick:      opRelayUnkick,
}

func opShareRelease(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	return c.shareRelease(string(a.Share))
}

func opShareLeave(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	return c.shareLeave(string(a.Share))
}

func opLANSetMode(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	return c.lanSetMode(a.LAN, a.Mode)
}

func opShareCreate(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	id, err := c.shareCreate(a.Device, a.Person, a.Services)
	if err == nil {
		out.Share = proto.ShareID(id)
	}
	return err
}

func opShareRevoke(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	return c.shareRevoke(string(a.Share))
}

func opDeviceSetPublic(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	return c.deviceSetPublic(a.Device, a.LAN)
}

func opLANCreate(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	name, err := checkName(a.Name)
	if err != nil {
		return err
	}
	for _, l := range c.w.lans {
		if l.joined(c.me) && l.Name == name {
			return errConflict("你已经在一个叫「" + name + "」的局域网里了")
		}
	}
	id := proto.LANID(newID("l"))
	// New LANs want new devices and new members approved: the plan's
	// recommendation (§0.2, DESIGN §12 item 6), which the user took. The
	// owner turns either off with lan.setApprove*.
	if err := c.exec(`INSERT INTO lans (id, name, owner, created_at, approve_devices, approve_members) VALUES (?, ?, ?, ?, 1, 1)`,
		id, name, c.me, ms(c.now)); err != nil {
		return err
	}
	if err := c.exec(`INSERT INTO members (lan, person, role, state, joined_at) VALUES (?, ?, ?, ?, ?)`,
		id, c.me, proto.RoleOwner, proto.MemberJoined, ms(c.now)); err != nil {
		return err
	}
	// The computer the LAN was made on is in it (DESIGN §6.2); made in a
	// browser, it starts empty.
	if c.dev != nil {
		if err := c.exec(`INSERT INTO lan_devices (lan, device, joined_at) VALUES (?, ?, ?)`, id, c.dev.ID, ms(c.now)); err != nil {
			return err
		}
	}
	out.LAN = id
	return nil
}

func opLANRename(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能改局域网的名字"); err != nil {
		return err
	}
	name, err := checkName(a.Name)
	if err != nil {
		return err
	}
	if name == l.Name {
		return errNoChange
	}
	return c.exec(`UPDATE lans SET name = ? WHERE id = ?`, name, l.ID)
}

func opLANDelete(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能删除这个局域网"); err != nil {
		return err
	}
	return c.deleteLAN(l)
}

// deleteLAN deletes l, for lan.delete and account.delete.
func (c *opCtx) deleteLAN(l *lan) error {
	// Opens are keyed by scope, not by a foreign key, as shares (M3) are
	// scopes too; they go by hand. Members, devices, invites and the
	// relays dragged in cascade.
	if err := c.exec(`DELETE FROM opens WHERE scope = ?`, proto.LANScope(l.ID)); err != nil {
		return err
	}
	// Its public devices go back to the people who logged them in.
	if err := c.exec(`UPDATE devices SET public_lan = '' WHERE public_lan = ?`, l.ID); err != nil {
		return err
	}
	return c.exec(`DELETE FROM lans WHERE id = ?`, l.ID)
}

func opLANInvite(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	// Any member invites (M2). The owner's invitees join at once; another
	// member's wait for the owner if the LAN wants new members approved
	// (see opInviteAccept).
	i := invite{
		ID:        proto.InviteID(newID("i")),
		LAN:       l.ID,
		Inviter:   c.me,
		Person:    a.Person,
		Token:     randomCode(20),
		State:     proto.InvitePending,
		CreatedAt: c.now,
		ExpiresAt: c.now.Add(inviteTTL),
	}
	if a.Person != "" {
		p := c.w.people[a.Person]
		if p == nil {
			return errNotFound("找不到这个人")
		}
		if a.Person == c.me {
			return errInvalid("不能邀请自己")
		}
		if m := l.memberOf[a.Person]; m != nil && m.State == proto.MemberJoined {
			return errConflict(pangu(p.DisplayName + "已经在「" + l.Name + "」里了"))
		} else if m != nil && m.State == proto.MemberPending {
			return errConflict(pangu(p.DisplayName + "已经在等拥有者同意了"))
		}
		// One invite per person: asking again gives the one they have.
		for _, old := range c.w.invitesOf(l.ID) {
			if old.Person == a.Person && old.state(c.now) == proto.InvitePending {
				v := c.w.inviteView(old, c.now)
				out.Invite = &v
				return errNoChange
			}
		}
	}
	if err := c.exec(`INSERT INTO invites (id, lan, inviter, person, token, state, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.LAN, i.Inviter, i.Person, i.Token, i.State, ms(i.CreatedAt), ms(i.ExpiresAt)); err != nil {
		return err
	}
	v := c.w.inviteView(&i, c.now)
	out.Invite = &v
	return nil
}

// usedUp says why an invite that is not pending cannot be used.
func usedUp(st proto.InviteState) error {
	switch st {
	case proto.InviteExpired:
		return errConflict("这个邀请已经过期了，请对方重新发一个")
	case proto.InviteAccepted:
		return errConflict("这个邀请已经有人用过了，请对方重新发一个")
	case proto.InviteRevoked:
		return errConflict("这个邀请已经被收回了")
	case proto.InviteDeclined:
		return errConflict("这个邀请已经被拒绝了")
	}
	return nil
}

// addDevice puts device d into LAN l: in at once, or waiting for the owner
// when its owner waits to join, or the LAN wants new devices approved and
// they are not its owner.
func (c *opCtx) addDevice(l *lan, d *device, ownerWaits bool) error {
	state := deviceJoined
	if ownerWaits || l.ApproveDevices && d.Owner != l.Owner {
		state = devicePending
	}
	return c.exec(`INSERT INTO lan_devices (lan, device, joined_at, state) VALUES (?, ?, ?, ?)
			ON CONFLICT (lan, device) DO UPDATE SET state = excluded.state, joined_at = excluded.joined_at`,
		l.ID, d.ID, ms(c.now), state)
}

// inviteRef is what an op names its invite by: an invitation's id, or a
// link or token.
func inviteRef(a proto.OpArgs) string {
	if a.Invitation != "" {
		return string(a.Invitation)
	}
	return a.Invite
}

func opInviteAccept(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	i, err := findInvite(c.w, inviteRef(a), c.me)
	if err != nil {
		return err
	}
	l := c.w.lans[i.LAN]
	out.LAN = l.ID
	if m := l.memberOf[c.me]; m != nil && (m.State == proto.MemberJoined || m.State == proto.MemberPending) {
		// Already in, or already waiting, say from another device: the
		// invite is left for the one it was meant for, and this device
		// joins (or waits) as on a fresh accept.
		if c.dev == nil || l.hasDevice[c.dev.ID] != nil || l.hasPending[c.dev.ID] != nil || !l.counts(c.dev) {
			return errNoChange
		}
		return c.addDevice(l, c.dev, m.State == proto.MemberPending)
	}
	if err := usedUp(i.state(c.now)); err != nil {
		return err
	}
	state := proto.MemberJoined
	if i.Inviter != l.Owner && l.ApproveMembers {
		state = proto.MemberPending
	}
	if state == proto.MemberJoined && c.w.seatFull(l.Owner, c.me, c.now) {
		// The owner's seats are full: the person waits, a dashed ball, and
		// the owner frees a seat to let them in (pricing-and-relays §1.5
		// item 5).
		state = proto.MemberPending
	}
	if err := c.exec(`INSERT INTO members (lan, person, role, state, joined_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (lan, person) DO UPDATE SET state = excluded.state, joined_at = excluded.joined_at`,
		l.ID, c.me, proto.RoleMember, state, ms(c.now)); err != nil {
		return err
	}
	if err := c.exec(`UPDATE invites SET state = ?, used_by = ?, used_at = ? WHERE id = ?`,
		proto.InviteAccepted, c.me, ms(c.now), i.ID); err != nil {
		return err
	}
	// The computer the invite was accepted on joins too (DESIGN §6.2): it
	// comes in with its owner, or waits with them. Letting a person in lets
	// in the computer they came with; devices they add later are what
	// "新设备要同意" is about.
	if c.dev == nil || !l.counts(c.dev) {
		// Accepted in a browser, no device comes with the person; a
		// sharing LAN takes no member's device: they get shares.
		return nil
	}
	ds := deviceJoined
	if state == proto.MemberPending {
		ds = devicePending
	}
	return c.exec(`INSERT INTO lan_devices (lan, device, joined_at, state) VALUES (?, ?, ?, ?)
			ON CONFLICT (lan, device) DO UPDATE SET state = excluded.state, joined_at = excluded.joined_at`,
		l.ID, c.dev.ID, ms(c.now), ds)
}

func opInviteDecline(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	i, err := findInvite(c.w, inviteRef(a), c.me)
	if err != nil {
		return err
	}
	st := i.state(c.now)
	if st == proto.InviteDeclined {
		return errNoChange
	}
	if err := usedUp(st); err != nil {
		return err
	}
	return c.exec(`UPDATE invites SET state = ?, used_by = ?, used_at = ? WHERE id = ?`,
		proto.InviteDeclined, c.me, ms(c.now), i.ID)
}

func opInviteRevoke(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	i := c.w.invites[proto.InviteID(a.Invite)]
	if i == nil {
		return errNotFound("找不到这个邀请")
	}
	l, err := c.lan(i.LAN)
	if err != nil {
		return errNotFound("找不到这个邀请")
	}
	if l.Owner != c.me && i.Inviter != c.me {
		return errForbidden("只有拥有者和发出邀请的人能收回它")
	}
	if i.state(c.now) != proto.InvitePending {
		return errNoChange
	}
	return c.exec(`UPDATE invites SET state = ? WHERE id = ?`, proto.InviteRevoked, i.ID)
}

func opLANAddDevice(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	d, err := c.ownDevice(a.Device, "把它加进来")
	if err != nil {
		return err
	}
	if d.PublicLAN != "" && d.PublicLAN != l.ID {
		return errInvalid("公共设备只在它自己的局域网里")
	}
	if !l.counts(d) {
		return errInvalid("「" + l.Name + "」只用来分享设备，成员的设备不放进来")
	}
	if l.hasDevice[d.ID] != nil || l.hasPending[d.ID] != nil {
		return errNoChange
	}
	return c.addDevice(l, d, false)
}

func opLANRemoveDevice(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	d, err := c.ownDevice(a.Device, "移出")
	if err != nil {
		return err
	}
	if l.hasDevice[d.ID] == nil && l.hasPending[d.ID] == nil {
		return errNoChange
	}
	// A public device taken out of its LAN is its person's again.
	if d.PublicLAN == l.ID {
		if err := c.exec(`UPDATE devices SET public_lan = '' WHERE id = ?`, d.ID); err != nil {
			return err
		}
	}
	// What it opened here stays on record, so putting it back (undo)
	// brings its switches back. A device still waiting is taken back.
	return c.exec(`DELETE FROM lan_devices WHERE lan = ? AND device = ?`, l.ID, d.ID)
}

func opServiceSet(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	if a.Share != "" {
		// A service in a share (M3).
		return c.shareService(string(a.Share), a.Service, a.On)
	}
	d, err := c.ownDevice(a.Device, "改它的服务")
	if err != nil {
		return err
	}
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if l.hasDevice[d.ID] == nil {
		return errInvalid("这台设备不在「" + l.Name + "」里")
	}
	return c.setService(d, proto.LANScope(l.ID), a.Service, a.On)
}

// setService switches service svc of d in scope sid, setting the service
// up in d's catalog the first time.
func (c *opCtx) setService(d *device, sid proto.ScopeID, in *proto.Service, on bool) error {
	if in == nil {
		return errInvalid("缺少要开关的服务")
	}
	svc := *in
	svc.Name = strings.TrimSpace(svc.Name)
	if err := svc.Validate(); err != nil {
		return errInvalid("服务不对：" + err.Error())
	}
	key := svc.Key()
	var have *proto.Service
	for i := range d.services {
		old := &d.services[i]
		if old.Key() == key {
			have = old
			continue
		}
		for _, p := range svc.Ports {
			for _, q := range old.Ports {
				if p.Proto == q.Proto && p.First <= q.Last && q.First <= p.Last {
					return errConflict(fmt.Sprintf("端口 %s 已经是「%s」在用了", p, old.DisplayName()))
				}
			}
		}
	}
	changed := false
	if have == nil {
		ports, _ := json.Marshal(svc.Ports)
		if err := c.exec(`INSERT INTO services (device, key, kind, name, ports) VALUES (?, ?, ?, ?, ?)`,
			d.ID, key, svc.Kind, svc.Name, string(ports)); err != nil {
			return err
		}
		changed = true
	} else if have.Kind != svc.Kind || have.Name != svc.Name {
		if err := c.exec(`UPDATE services SET kind = ?, name = ? WHERE device = ? AND key = ?`, svc.Kind, svc.Name, d.ID, key); err != nil {
			return err
		}
		changed = true
	}
	if was := d.opens[sid][key]; was != on {
		changed = true
		var err error
		if on {
			err = c.exec(`INSERT INTO opens (device, scope, key) VALUES (?, ?, ?)`, d.ID, sid, key)
		} else {
			err = c.exec(`DELETE FROM opens WHERE device = ? AND scope = ? AND key = ?`, d.ID, sid, key)
		}
		if err != nil {
			return err
		}
	}
	if !changed {
		return errNoChange
	}
	return nil
}

func opDeviceRefuse(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.ownDevice(a.Device, "决定谁能用")
	if err != nil {
		return err
	}
	if a.Person == c.me {
		return errInvalid("不能不让自己用自己的设备")
	}
	if c.w.people[a.Person] == nil {
		return errNotFound("找不到这个人")
	}
	if d.refused[a.Person] == a.Refuse {
		return errNoChange
	}
	if a.Refuse {
		return c.exec(`INSERT INTO refusals (device, person) VALUES (?, ?)`, d.ID, a.Person)
	}
	return c.exec(`DELETE FROM refusals WHERE device = ? AND person = ?`, d.ID, a.Person)
}

func opDeviceRename(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.ownDevice(a.Device, "改它的名字")
	if err != nil {
		return err
	}
	name, err := checkName(a.Name)
	if err != nil {
		return err
	}
	if name == d.DisplayName {
		return errNoChange
	}
	if err := c.exec(`UPDATE devices SET display_name = ? WHERE id = ?`, name, d.ID); err != nil {
		return err
	}
	// The DNS label follows the name, unless it was set by hand or the
	// name has nothing to make one from ("阿杰的台式机" keeps its label).
	if label := proto.MakeLabel(name, ""); label != "" && !d.NamePinned {
		if label = c.freeLabel(d, label); label != d.Name {
			return c.exec(`UPDATE devices SET name = ? WHERE id = ?`, label, d.ID)
		}
	}
	return nil
}

// freeLabel is label, or label-2, label-3… if another of d's owner's
// devices has it.
func (c *opCtx) freeLabel(d *device, label string) string {
	taken := map[string]bool{}
	for _, o := range c.w.devices {
		if o.Owner == d.Owner && o.ID != d.ID {
			taken[o.Name] = true
		}
	}
	base := label
	for n := 2; taken[label]; n++ {
		label = base + "-" + strconv.Itoa(n)
	}
	return label
}

func opDeviceSetKind(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.ownDevice(a.Device, "改它的种类")
	if err != nil {
		return err
	}
	if !a.Kind.Valid() {
		return errInvalid("不认识的设备种类：" + string(a.Kind))
	}
	if a.Kind == d.Kind {
		return errNoChange
	}
	return c.exec(`UPDATE devices SET kind = ? WHERE id = ?`, a.Kind, d.ID)
}

// dropMember takes person p out of LAN l with all their devices.
func (c *opCtx) dropMember(l *lan, p proto.PersonID) error {
	if err := c.exec(`DELETE FROM lan_devices WHERE lan = ? AND device IN (SELECT id FROM devices WHERE owner = ?)`, l.ID, p); err != nil {
		return err
	}
	// Their relays leave with them: a relay serves only LANs its owner is
	// in (pricing-and-relays §2.3).
	if err := c.exec(`DELETE FROM lan_relays WHERE lan = ? AND relay IN (SELECT id FROM relays WHERE owner = ?)`, l.ID, p); err != nil {
		return err
	}
	return c.exec(`DELETE FROM members WHERE lan = ? AND person = ?`, l.ID, p)
}

func opMemberRemove(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能移出成员"); err != nil {
		return err
	}
	if a.Person == l.Owner {
		return errInvalid("不能移出拥有者")
	}
	if l.memberOf[a.Person] == nil {
		return errNotFound("这个人不在这个局域网里")
	}
	return c.dropMember(l, a.Person)
}

func opLANLeave(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	// A person waiting to join may leave too: it takes back their request.
	l := c.w.lans[a.LAN]
	if l == nil || l.memberOf[c.me] == nil || l.memberOf[c.me].State == proto.MemberInvited {
		return errNotFound("找不到这个局域网")
	}
	if l.Owner == c.me {
		return errInvalid("你是拥有者，不能退出；不想要了可以删除这个局域网")
	}
	return c.dropMember(l, c.me)
}

func opLANSetApproveDevices(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能改这个设置"); err != nil {
		return err
	}
	if l.ApproveDevices == a.On {
		return errNoChange
	}
	// Devices already waiting keep waiting when it is turned off: the owner
	// still decides on them.
	return c.exec(`UPDATE lans SET approve_devices = ? WHERE id = ?`, a.On, l.ID)
}

func opLANSetApproveMembers(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能改这个设置"); err != nil {
		return err
	}
	if l.ApproveMembers == a.On {
		return errNoChange
	}
	return c.exec(`UPDATE lans SET approve_members = ? WHERE id = ?`, a.On, l.ID)
}

func opDeviceApprove(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能同意新设备"); err != nil {
		return err
	}
	if l.hasDevice[a.Device] != nil {
		return errNoChange
	}
	d := c.w.devices[a.Device]
	if l.hasPending[a.Device] == nil || d == nil {
		return errNotFound("这台设备没在等同意")
	}
	if !l.joined(d.Owner) {
		name := ""
		if p := c.w.people[d.Owner]; p != nil {
			name = p.DisplayName
		}
		return errConflict(pangu("先同意" + name + "加入，他的设备会一起进来"))
	}
	return c.exec(`UPDATE lan_devices SET state = ? WHERE lan = ? AND device = ?`, deviceJoined, l.ID, d.ID)
}

func opDeviceReject(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能拒绝新设备"); err != nil {
		return err
	}
	if l.hasPending[a.Device] == nil {
		return errNotFound("这台设备没在等同意")
	}
	return c.exec(`DELETE FROM lan_devices WHERE lan = ? AND device = ?`, l.ID, a.Device)
}

func opMemberApprove(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能同意新成员"); err != nil {
		return err
	}
	m := l.memberOf[a.Person]
	switch {
	case m == nil || m.State == proto.MemberInvited:
		return errNotFound("这个人没在等同意")
	case m.State == proto.MemberJoined:
		return errNoChange
	}
	if c.w.seatFull(l.Owner, a.Person, c.now) {
		return errSeatsFull(c.w.seatLimitOf(l.Owner))
	}
	if err := c.exec(`UPDATE members SET state = ? WHERE lan = ? AND person = ?`, proto.MemberJoined, l.ID, a.Person); err != nil {
		return err
	}
	// The devices that came with them come in with them.
	return c.exec(`UPDATE lan_devices SET state = ? WHERE lan = ? AND device IN (SELECT id FROM devices WHERE owner = ?)`,
		deviceJoined, l.ID, a.Person)
}

func opMemberReject(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能拒绝新成员"); err != nil {
		return err
	}
	m := l.memberOf[a.Person]
	switch {
	case m == nil || m.State == proto.MemberInvited:
		return errNotFound("这个人没在等同意")
	case m.State == proto.MemberJoined:
		return errConflict("这个人已经在局域网里了；要请他离开，用移出成员")
	}
	return c.dropMember(l, a.Person)
}

func opDeviceRenew(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.ownDevice(a.Device, "续上它的登录")
	if err != nil {
		return err
	}
	// A device whose login ran out waits in login/wait: wake it, so it is
	// back without a browser.
	c.after = append(c.after, func() { c.s.logins.finishMachine(d.MachineKey) })
	return c.exec(`UPDATE devices SET expires_at = ? WHERE id = ?`, ms(c.now.Add(c.s.cfg.LoginLifetime)), d.ID)
}

func opDeviceSetDNSName(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	d, err := c.ownDevice(a.Device, "改它的名字")
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(a.Name))
	if !proto.ValidLabel(name) {
		return errInvalid("短名只能用小写字母、数字和中间的连字符，最多 63 个")
	}
	if name == d.Name && d.NamePinned {
		return errNoChange
	}
	for _, o := range c.w.devices {
		if o.Owner == d.Owner && o.ID != d.ID && o.Name == name {
			return errConflict("你已经有一台设备叫 " + name + " 了")
		}
	}
	return c.exec(`UPDATE devices SET name = ?, name_pinned = 1 WHERE id = ?`, name, d.ID)
}
