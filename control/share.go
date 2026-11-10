package control

import (
	"fmt"
	"net/http"

	"github.com/kittors/deneb/proto"
)

// Shares and public devices (implementation.md §3 M3, §0.2): a share is one
// device given to one person, with the services it opens to them; a public
// device belongs to a LAN and its owner manages it.
//
// They run inside an op's transaction: share.create, share.revoke,
// service.set with a share, device.setPublic.

func errSeatsFull(limit int) error {
	return newErr(http.StatusForbidden, proto.ErrSeatsFull, fmt.Sprintf("%d 个位置都用上了", limit))
}

// hasService reports whether key is in d's catalog.
func (d *device) hasService(key proto.ServiceKey) bool {
	for _, s := range d.services {
		if s.Key() == key {
			return true
		}
	}
	return false
}

// shareCreate shares device id with person, opening the given services of
// its catalog. A share of the same device to the same person revoked
// before comes back as it was, services and all: that is undo (DESIGN
// §6.5); given services replace the old ones.
func (c *opCtx) shareCreate(id proto.DeviceID, person proto.PersonID, services []proto.ServiceKey) (string, error) {
	d, err := c.ownDevice(id, "分享它")
	if err != nil {
		return "", err
	}
	if person == c.me || person == d.Owner {
		return "", errInvalid("不能分享给自己")
	}
	if c.w.people[person] == nil {
		return "", errNotFound("找不到这个人")
	}
	for _, k := range services {
		if !d.hasService(k) {
			return "", errInvalid("这台设备上没有这项服务：" + string(k))
		}
	}
	var old *share
	for _, sh := range c.w.shares {
		if sh.Device == d.ID && sh.Receiver == person {
			old = sh
		}
	}
	if old != nil && old.State == shareActive {
		// Dropped on someone who has it: "已经有了" (scene 4, step 3).
		return old.ID, errConflict("已经有了")
	}
	if c.w.seatFull(c.me, person, c.now) {
		return "", errSeatsFull(c.w.seatLimitOf(c.me))
	}
	// Sharing says yes to this person: a refusal of them on the device goes.
	if err := c.exec(`DELETE FROM refusals WHERE device = ? AND person = ?`, d.ID, person); err != nil {
		return "", err
	}
	sid := ""
	if old != nil {
		sid = old.ID
		if err := c.exec(`UPDATE shares SET state = ?, revoked_at = 0, sharer = ? WHERE id = ?`, shareActive, c.me, sid); err != nil {
			return "", err
		}
		if services == nil {
			return sid, nil
		}
		if err := c.exec(`DELETE FROM opens WHERE device = ? AND scope = ?`, d.ID, shareScope(sid)); err != nil {
			return "", err
		}
	} else {
		sid = newID("s")
		if err := c.exec(`INSERT INTO shares (id, device, receiver, sharer, state, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			sid, d.ID, person, c.me, shareActive, ms(c.now)); err != nil {
			return "", err
		}
	}
	for _, k := range services {
		if err := c.exec(`INSERT OR IGNORE INTO opens (device, scope, key) VALUES (?, ?, ?)`, d.ID, shareScope(sid), k); err != nil {
			return "", err
		}
	}
	return sid, nil
}

// shareRevoke takes a share back. The row stays, revoked, so sharing again
// restores it; for a few seconds the receiver keeps the seat, so that undo
// cannot find it taken (pricing-and-relays §1.5 item 7).
func (c *opCtx) shareRevoke(id string) error {
	sh := c.w.shares[id]
	if sh == nil {
		return errNotFound("找不到这次分享")
	}
	if sh.Sharer != c.me {
		// Whoever manages the device now may take its shares back too.
		if _, err := c.ownDevice(sh.Device, "收回它"); err != nil {
			return err
		}
	}
	if sh.State != shareActive {
		return errNoChange
	}
	// The seat stays until the client says the undo is gone
	// (share.release), seatHold at most.
	return c.exec(`UPDATE shares SET state = ?, revoked_at = ?, hold_until = ? WHERE id = ?`,
		shareRevoked, ms(c.now), ms(c.now.Add(seatHold)), sh.ID)
}

// shareRelease frees the seat a revoked share keeps: the toast offering
// undo went away untouched.
func (c *opCtx) shareRelease(id string) error {
	sh := c.w.shares[id]
	if sh == nil {
		return errNotFound("找不到这次分享")
	}
	if sh.Sharer != c.me {
		if _, err := c.ownDevice(sh.Device, "收回它"); err != nil {
			return err
		}
	}
	if !sh.held(c.now) {
		return errNoChange
	}
	return c.exec(`UPDATE shares SET hold_until = ? WHERE id = ?`, ms(c.now), sh.ID)
}

// shareLeave is the receiver giving a share up. The giver hears nothing but
// the share going from their view; the seat is not kept, there being no
// undo on the giver's side.
func (c *opCtx) shareLeave(id string) error {
	sh := c.w.shares[id]
	if sh == nil || sh.Receiver != c.me {
		return errNotFound("找不到这次分享")
	}
	if sh.State != shareActive {
		return errNoChange
	}
	return c.exec(`UPDATE shares SET state = ?, revoked_at = ?, hold_until = 0 WHERE id = ?`, shareLeft, ms(c.now), sh.ID)
}

// lanSetMode switches a LAN between the two modes. Into sharing, members'
// devices leave it (their shares stay); back to a LAN, they add theirs
// again themselves.
func (c *opCtx) lanSetMode(id proto.LANID, mode proto.LANMode) error {
	l, err := c.lan(id)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能改局域网的用法"); err != nil {
		return err
	}
	if mode != proto.LANModeLAN && mode != proto.LANModeShare {
		return errInvalid("不认识的用法：" + string(mode))
	}
	if mode == l.Mode {
		return errNoChange
	}
	if mode == proto.LANModeShare {
		if err := c.exec(`DELETE FROM lan_devices WHERE lan = ? AND device IN
				(SELECT id FROM devices WHERE owner <> ? AND public_lan <> ?)`, l.ID, l.Owner, l.ID); err != nil {
			return err
		}
	}
	return c.exec(`UPDATE lans SET mode = ? WHERE id = ?`, mode, l.ID)
}

// shareService switches one service of a share.
func (c *opCtx) shareService(id string, svc *proto.Service, on bool) error {
	sh := c.w.shares[id]
	if sh == nil {
		return errNotFound("找不到这次分享")
	}
	d, err := c.ownDevice(sh.Device, "改它的服务")
	if err != nil {
		return err
	}
	return c.setService(d, shareScope(sh.ID), svc, on)
}

// deviceSetPublic makes one of the asker's devices a public device of a
// LAN they own, or (lan empty) a person's device again. A public device is
// in its LAN and only there.
func (c *opCtx) deviceSetPublic(id proto.DeviceID, lanID proto.LANID) error {
	d, err := c.ownDevice(id, "改它")
	if err != nil {
		return err
	}
	if lanID == "" {
		if d.PublicLAN == "" {
			return errNoChange
		}
		return c.exec(`UPDATE devices SET public_lan = '' WHERE id = ?`, d.ID)
	}
	l, err := c.lan(lanID)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能把设备放进「"+l.Name+"」当公共设备"); err != nil {
		return err
	}
	switch d.PublicLAN {
	case l.ID:
		return errNoChange
	case "":
	default:
		return errConflict("这台已经是另一个局域网的公共设备了")
	}
	if err := c.exec(`UPDATE devices SET public_lan = ? WHERE id = ?`, l.ID, d.ID); err != nil {
		return err
	}
	if err := c.exec(`DELETE FROM lan_devices WHERE device = ? AND lan <> ?`, d.ID, l.ID); err != nil {
		return err
	}
	return c.exec(`INSERT INTO lan_devices (lan, device, joined_at, state) VALUES (?, ?, ?, ?)
			ON CONFLICT (lan, device) DO UPDATE SET state = excluded.state`, l.ID, d.ID, ms(c.now), deviceJoined)
}
