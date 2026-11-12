package main

import (
	"net/http"
	"slices"
	"strings"

	"github.com/kittors/deneb/proto"
)

// M7 in the mock (DESIGN §6.15): deleting a device (device.delete), and
// deleting the account (account.delete, a dry run first), as the control
// server does them (design/plan/m7/account-delete.md).

// seedRefusal is the server's words for an account the deployment's seed
// names.
const seedRefusal = "这个账号写在部署的种子文件里，服务器每次启动都会把它建回来：要注销，先请部署管理员把它从种子文件里拿掉"

// deleteDevice deletes a device of me's for good: out of every LAN, its
// shares and what relays saw of it with it.
func (m *mock) deleteDevice(me proto.PersonID, id proto.DeviceID) *opError {
	d := m.devices[id]
	if d == nil || !m.manages(me, d) {
		return errNoDevice()
	}
	m.dropDevice(id)
	return nil
}

func (m *mock) dropDevice(id proto.DeviceID) {
	for _, l := range m.lans {
		l.devs = slices.DeleteFunc(l.devs, func(ld *mlandev) bool { return ld.id == id })
	}
	for _, s := range m.shares {
		if s.device == id {
			s.active = false
		}
	}
	for _, r := range m.relays {
		r.connected = slices.DeleteFunc(r.connected, func(c proto.RelayClient) bool { return c.Device == id })
		m.recount(r)
	}
	delete(m.devices, id)
	m.order = slices.DeleteFunc(m.order, func(x proto.DeviceID) bool { return x == id })
}

// accountDeletion says what deleting me's account deletes, the LANs they own
// that have others in them as owned says.
func (m *mock) accountDeletion(me proto.PersonID, owned string) *proto.AccountDeletion {
	acc := &proto.AccountDeletion{Devices: []proto.DeviceID{}, LANs: []proto.LANID{}, Left: []proto.LANID{}, Relays: []proto.RelayID{}}
	for _, id := range m.order {
		if m.devices[id].v.Owner == me {
			acc.Devices = append(acc.Devices, id)
		}
	}
	for _, l := range m.lans {
		if l.member(me) == nil {
			continue
		}
		if l.owner != me {
			acc.Left = append(acc.Left, l.id)
			continue
		}
		var first *proto.Member
		for i := range l.members {
			mb := &l.members[i]
			if mb.Person == me || mb.State != proto.MemberJoined {
				continue
			}
			if first == nil || mb.JoinedAt.Before(first.JoinedAt) || mb.JoinedAt.Equal(first.JoinedAt) && mb.Person < first.Person {
				first = mb
			}
		}
		switch {
		case first == nil:
			acc.LANs = append(acc.LANs, l.id)
		case owned == proto.AccountTransfer:
			acc.Transferred = append(acc.Transferred, proto.LANTransfer{LAN: l.id, To: first.Person})
		case owned == proto.AccountDeleteLANs:
			acc.LANs = append(acc.LANs, l.id)
		default:
			acc.Blocked = append(acc.Blocked, l.id)
		}
	}
	for _, r := range m.relays {
		if r.r.Owner != me {
			continue
		}
		acc.Relays = append(acc.Relays, r.r.ID)
		inUse := false
		for _, c := range r.connected {
			inUse = inUse || c.Person != me
		}
		if inUse {
			acc.RelaysInUse = append(acc.RelaysInUse, r.r.ID)
		}
	}
	for _, s := range m.shares {
		if s.active && (s.sharer == me || s.person == me) {
			acc.Shares++
		}
	}
	for _, inv := range m.invites {
		if inv.inviter == me {
			acc.Invites++
		}
	}
	slices.Sort(acc.Devices)
	slices.Sort(acc.LANs)
	slices.Sort(acc.Left)
	slices.Sort(acc.Blocked)
	slices.Sort(acc.Relays)
	slices.Sort(acc.RelaysInUse)
	return acc
}

// deleteAccount does account.delete for me: a dry run says what would go;
// for real, with my email again, it all goes, and every session of mine
// with it (their streams end with loggedOut).
func (m *mock) deleteAccount(me proto.PersonID, a proto.OpArgs) (*proto.AccountDeletion, *opError) {
	if m.seed[me] {
		return nil, fail(403, proto.ErrForbidden, seedRefusal)
	}
	if a.OwnedLANs != "" && a.OwnedLANs != proto.AccountTransfer && a.OwnedLANs != proto.AccountDeleteLANs {
		return nil, fail(400, proto.ErrInvalid, "ownedLANs 只能是 transfer 或 delete")
	}
	acc := m.accountDeletion(me, a.OwnedLANs)
	if a.DryRun {
		acc.DryRun = true
		return acc, nil
	}
	if len(acc.Blocked) > 0 {
		var names []string
		for _, id := range acc.Blocked {
			names = append(names, "「"+m.lan(id).name+"」")
		}
		return nil, fail(409, proto.ErrConflict, "你是"+strings.Join(names, "、")+"的拥有者，里面还有别人：先选好转给别人还是整个删掉，再注销")
	}
	if p := m.person(me); p == nil || !strings.EqualFold(strings.TrimSpace(a.Confirm), p.Email) {
		return nil, fail(400, proto.ErrInvalid, "要注销，请再输一遍你的邮箱")
	}
	for _, id := range acc.Devices {
		m.dropDevice(id)
	}
	for _, t := range acc.Transferred {
		l := m.lan(t.LAN)
		l.owner = t.To
		l.member(t.To).Role = proto.RoleOwner
	}
	m.lans = slices.DeleteFunc(m.lans, func(l *mlan) bool { return slices.Contains(acc.LANs, l.id) })
	for _, l := range m.lans {
		l.members = slices.DeleteFunc(l.members, func(mb proto.Member) bool { return mb.Person == me })
		l.added = slices.DeleteFunc(l.added, func(r proto.RelayID) bool { return slices.Contains(acc.Relays, r) })
	}
	for _, s := range m.shares {
		if s.sharer == me || s.person == me {
			s.active = false
		}
	}
	for tok, inv := range m.invites {
		if inv.inviter == me {
			delete(m.invites, tok)
		}
	}
	m.relays = slices.DeleteFunc(m.relays, func(r *mrelay) bool { return r.r.Owner == me })
	for tok, p := range m.sessions {
		if p == me {
			delete(m.sessions, tok)
		}
	}
	for e, p := range m.emails {
		if p == me {
			delete(m.emails, e)
		}
	}
	m.people = slices.DeleteFunc(m.people, func(p *proto.Person) bool { return p.ID == me })
	return acc, nil
}

// seedMe makes me's account one the deployment's seed names, or not: the
// recording of an account that cannot be deleted.
func (m *mock) seedMe(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	m.mu.Lock()
	m.seed[me] = !m.seed[me]
	m.mu.Unlock()
	writeJSON(w, struct{}{})
}
