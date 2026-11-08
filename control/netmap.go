package control

import (
	"cmp"
	"slices"
	"time"

	"github.com/kittors/deneb/proto"
)

// This file is the security-critical part of the server: what each device
// and each person gets to know. The rules (multi-lan-tech §3.5, layer 1):
//
//   - Device D sees device P only if both are in some LAN L, both owners
//     are joined members of L, and P has not paused L. The shared scopes it
//     is told about are exactly those LANs. A device waiting for the owner
//     is not in the LAN yet; a device logged out or whose login ran out is
//     in no map, its own included.
//   - Person X sees only the LANs X joined, the people and devices in them,
//     and X's own devices. Another person's device shows only the LANs X is
//     in too, and only the services opened to them.
//
// Nothing else goes out, and netmap_test.go checks it on random worlds.

// online says whether a device holds a stream now.
type online func(proto.DeviceID) bool

// netMap computes device id's network map, or nil if there is no such
// device or no one is logged in on it.
func (w *World) netMap(id proto.DeviceID, version uint64, isOnline online) *proto.NetMap {
	d := w.devices[id]
	if d == nil || !d.usable() {
		return nil
	}
	nm := &proto.NetMap{
		Version: version,
		Network: w.Network,
		Self:    w.node(d, isOnline),
		Peers:   []proto.Peer{},
		Scopes:  []proto.Scope{},
		Relays:  []proto.Relay{},
	}
	relays := map[proto.RelayID]bool{}

	mine := w.lansOf(d)
	for _, l := range mine {
		sid := proto.LANScope(l.ID)
		nm.Scopes = append(nm.Scopes, proto.Scope{
			ID:     sid,
			Name:   l.Name,
			Open:   d.openIn(sid),
			Paused: d.Paused[sid],
			Relays: slices.Clone(l.relays),
		})
		for _, r := range l.relays {
			relays[r] = true
		}
	}

	// The scopes each peer shares with d, apart by whether the peer paused
	// them: a peer that paused them all stays, so its pause lifts without a
	// round trip to here.
	shared := map[proto.DeviceID][]proto.ScopeID{}
	pausedBy := map[proto.DeviceID][]proto.ScopeID{}
	for _, l := range mine {
		sid := proto.LANScope(l.ID)
		for _, ld := range l.devices {
			p := w.devices[ld.Device]
			if p == nil || p.ID == d.ID || !p.usable() || !l.joined(p.Owner) {
				continue
			}
			if p.Paused[sid] {
				pausedBy[p.ID] = append(pausedBy[p.ID], sid)
			} else {
				shared[p.ID] = append(shared[p.ID], sid)
			}
		}
	}
	peers := map[proto.DeviceID]bool{}
	for id := range shared {
		peers[id] = true
	}
	for id := range pausedBy {
		peers[id] = true
	}
	for pid := range peers {
		p := w.devices[pid]
		scopes, paused := nonNil(shared[pid]), pausedBy[pid]
		slices.Sort(scopes)
		slices.Sort(paused)
		nm.Peers = append(nm.Peers, proto.Peer{
			Node:          w.node(p, isOnline),
			Scopes:        scopes,
			PausedScopes:  paused,
			RefusedByPeer: p.Owner != d.Owner && p.refused[d.Owner],
			RefusedByMe:   p.Owner != d.Owner && d.refused[p.Owner],
		})
		if p.HomeRelay != "" && w.relays[p.HomeRelay] != nil {
			relays[p.HomeRelay] = true
		}
	}
	slices.SortFunc(nm.Peers, func(a, b proto.Peer) int { return cmp.Compare(a.ID, b.ID) })
	if d.HomeRelay != "" && w.relays[d.HomeRelay] != nil {
		relays[d.HomeRelay] = true
	}
	for r := range relays {
		if rl := w.relays[r]; rl != nil {
			nm.Relays = append(nm.Relays, *rl)
		}
	}
	slices.SortFunc(nm.Relays, func(a, b proto.Relay) int { return cmp.Compare(a.ID, b.ID) })
	return nm
}

// lansOf lists the LANs d is in, sorted by id, checking again that its
// owner is a joined member of each.
func (w *World) lansOf(d *device) []*lan {
	var out []*lan
	for id := range d.lans {
		if l := w.lans[id]; l != nil && l.joined(d.Owner) {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b *lan) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (w *World) node(d *device, isOnline online) proto.Node {
	owner := w.people[d.Owner]
	n := proto.Node{
		ID:          d.ID,
		Name:        d.Name,
		DisplayName: d.DisplayName,
		Owner:       d.Owner,
		FQDN:        proto.FQDN(d.Name, ""),
		Kind:        d.Kind,
		OS:          d.OS,
		NodeKey:     d.NodeKey,
		DiscoKey:    d.DiscoKey,
		Addrs:       d.addrs(),
		Endpoints:   slices.Clone(d.Endpoints),
		HomeRelay:   d.HomeRelay,
		Online:      isOnline != nil && isOnline(d.ID),
		LastSeen:    d.LastSeen,
	}
	if owner != nil {
		n.OwnerName = owner.Name
		n.OwnerDisplayName = owner.DisplayName
		n.FQDN = proto.FQDN(d.Name, owner.Name)
	}
	return n
}

// openIn lists the services d opens in scope s, in catalog order.
func (d *device) openIn(s proto.ScopeID) []proto.Service {
	out := []proto.Service{}
	for _, svc := range d.services {
		if d.opens[s][svc.Key()] {
			out = append(out, svc)
		}
	}
	return out
}

// seatLimit is the free seats per person (pricing-and-relays §1.4). M1
// counts and does not enforce.
const seatLimit = 6

// userView computes person me's view, or nil if there is no such person.
func (w *World) userView(me proto.PersonID, version uint64, isOnline online, now time.Time) *proto.UserView {
	p := w.people[me]
	if p == nil {
		return nil
	}
	v := &proto.UserView{
		Version:  version,
		Me:       me,
		People:   []proto.Person{},
		LANs:     []proto.LANView{},
		Devices:  []proto.DeviceView{},
		Relays:   []proto.Relay{},
		Features: slices.Clone(proto.M2Features),
	}

	// My LANs, in the order I joined them, or asked to: the ones I wait
	// to be let into show as stubs.
	var mine []*lan
	for _, l := range w.lans {
		if m := l.memberOf[me]; m != nil && (m.State == proto.MemberJoined || m.State == proto.MemberPending) {
			mine = append(mine, l)
		}
	}
	slices.SortFunc(mine, func(a, b *lan) int {
		if c := a.memberOf[me].JoinedAt.Compare(b.memberOf[me].JoinedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	inMine := map[proto.LANID]bool{}
	for _, l := range mine {
		if l.joined(me) {
			inMine[l.ID] = true
		}
	}

	people := map[proto.PersonID]bool{me: true}
	devices := map[proto.DeviceID]bool{}
	for _, d := range w.devices {
		if d.Owner == me && !d.LoggedOut {
			devices[d.ID] = true
		}
	}
	relays := map[proto.RelayID]bool{}

	for _, l := range mine {
		if !l.joined(me) {
			// Waiting to be let in: the name and whose it is, nothing of
			// what is inside.
			v.LANs = append(v.LANs, proto.LANView{
				ID: l.ID, Name: l.Name, Owner: l.Owner, CreatedAt: l.CreatedAt, Pending: true,
				Members: []proto.Member{}, Devices: []proto.LANDevice{}, Relays: []proto.RelayID{},
			})
			people[l.Owner] = true
			continue
		}
		sid := proto.LANScope(l.ID)
		owner := l.Owner == me
		lv := proto.LANView{
			ID:             l.ID,
			Name:           l.Name,
			Owner:          l.Owner,
			CreatedAt:      l.CreatedAt,
			Members:        []proto.Member{},
			Devices:        []proto.LANDevice{},
			Relays:         slices.Clone(l.relays),
			ApproveDevices: l.ApproveDevices,
			ApproveMembers: l.ApproveMembers,
		}
		for _, m := range l.members {
			// Those waiting to join are the owner's to see.
			if m.State == proto.MemberJoined || m.State == proto.MemberPending && owner {
				lv.Members = append(lv.Members, proto.Member{Person: m.Person, Role: m.Role, State: m.State, JoinedAt: m.JoinedAt})
				people[m.Person] = true
			}
		}
		for _, ld := range l.devices {
			d := w.devices[ld.Device]
			if d == nil || d.LoggedOut || !l.joined(d.Owner) {
				continue
			}
			devices[d.ID] = true
			lv.Devices = append(lv.Devices, proto.LANDevice{
				Device:   d.ID,
				JoinedAt: ld.JoinedAt,
				Paused:   d.Paused[sid],
				Services: d.servicesIn(sid, d.Owner == me),
			})
		}
		// Devices waiting: the owner sees them all, a device's owner sees
		// their own; they are in no network map either way.
		for _, ld := range l.pending {
			d := w.devices[ld.Device]
			if d == nil || d.LoggedOut || !(owner || d.Owner == me) {
				continue
			}
			devices[d.ID] = true
			people[d.Owner] = true
			lv.Devices = append(lv.Devices, proto.LANDevice{
				Device:   d.ID,
				JoinedAt: ld.JoinedAt,
				Pending:  true,
				Services: d.servicesIn(sid, d.Owner == me),
			})
		}
		for _, i := range w.invitesOf(l.ID) {
			if i.state(now) == proto.InvitePending && (owner || i.Inviter == me) {
				lv.Invites = append(lv.Invites, w.inviteView(i, now))
				if i.Person != "" {
					// Whom it is made out to, so the screen can show
					// their ball as invited.
					lv.Invited = append(lv.Invited, proto.InvitedPerson{
						Person: i.Person, InvitedBy: i.Inviter, ExpiresAt: i.ExpiresAt, Invite: i.ID,
					})
					people[i.Person] = true
				}
			}
		}
		for _, r := range l.relays {
			relays[r] = true
		}
		v.LANs = append(v.LANs, lv)
	}

	// Invites made out to me, waiting for my answer.
	var mineInv []*invite
	for _, i := range w.invites {
		if i.Person == me && i.state(now) == proto.InvitePending && !w.lans[i.LAN].joined(me) {
			mineInv = append(mineInv, i)
		}
	}
	slices.SortFunc(mineInv, func(a, b *invite) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, i := range mineInv {
		v.Invitations = append(v.Invitations, w.preview(i, me, now))
	}

	for id := range people {
		if q := w.people[id]; q != nil {
			pp := q.Person
			if id != me {
				pp.Email = ""
			}
			v.People = append(v.People, pp)
		}
	}
	slices.SortFunc(v.People, func(a, b proto.Person) int { return cmp.Compare(a.ID, b.ID) })

	for id := range devices {
		d := w.devices[id]
		n := w.node(d, isOnline)
		dv := proto.DeviceView{
			ID:          d.ID,
			Owner:       d.Owner,
			Name:        d.Name,
			DisplayName: d.DisplayName,
			FQDN:        n.FQDN,
			Kind:        d.Kind,
			OS:          d.OS,
			Addrs:       d.addrs(),
			Online:      n.Online,
			LastSeen:    d.LastSeen,
			LANs:        []proto.LANID{},
			// Others' devices say only whether their login ran out, for
			// "连不上：登录过期"; when is their owner's business.
			Login: proto.LoginInfo{Expired: d.expired},
		}
		if d.Owner == me {
			dv.Login.ExpiresAt, dv.Login.RenewFrom = d.ExpiresAt, d.ExpiresAt.Add(-w.renewWindow)
		}
		for l := range d.lans {
			if inMine[l] && w.lans[l].joined(d.Owner) {
				dv.LANs = append(dv.LANs, l)
			}
		}
		slices.Sort(dv.LANs)
		if d.Owner == me {
			dv.Services = slices.Clone(d.services)
			for q := range d.refused {
				dv.Refused = append(dv.Refused, q)
			}
			slices.Sort(dv.Refused)
		} else {
			dv.RefusesMe = d.refused[me]
		}
		v.Devices = append(v.Devices, dv)
	}
	slices.SortFunc(v.Devices, func(a, b proto.DeviceView) int { return cmp.Compare(a.ID, b.ID) })

	for r := range relays {
		if rl := w.relays[r]; rl != nil {
			v.Relays = append(v.Relays, *rl)
		}
	}
	slices.SortFunc(v.Relays, func(a, b proto.Relay) int { return cmp.Compare(a.ID, b.ID) })

	v.Seats = w.seats(me)
	return v
}

// servicesIn lists d's services in scope s: all of them with their switch
// for its owner, only the open ones for everyone else.
func (d *device) servicesIn(s proto.ScopeID, forOwner bool) []proto.ServiceState {
	out := []proto.ServiceState{}
	for _, svc := range d.services {
		on := d.opens[s][svc.Key()]
		if on || forOwner {
			out = append(out, proto.ServiceState{Service: svc, On: on})
		}
	}
	return out
}

// seats counts the distinct people who joined the LANs me owns, me
// included (pricing-and-relays §1.4). Shares count from M3.
func (w *World) seats(me proto.PersonID) proto.Seats {
	in := map[proto.PersonID]bool{me: true}
	for _, l := range w.lans {
		if l.Owner != me {
			continue
		}
		for _, m := range l.members {
			if m.State == proto.MemberJoined {
				in[m.Person] = true
			}
		}
	}
	s := proto.Seats{Limit: seatLimit, People: []proto.PersonID{}}
	for p := range in {
		s.People = append(s.People, p)
	}
	slices.Sort(s.People)
	s.Used = len(s.People)
	return s
}

func (w *World) invitesOf(l proto.LANID) []*invite {
	var out []*invite
	for _, i := range w.invites {
		if i.LAN == l {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b *invite) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// preview is what someone holding invite i may know before answering it;
// asker is who asks, or empty for the landing page.
func (w *World) preview(i *invite, asker proto.PersonID, now time.Time) proto.InvitePreview {
	l := w.lans[i.LAN]
	p := proto.InvitePreview{
		Invite:    i.ID,
		LAN:       l.ID,
		LANName:   l.Name,
		State:     i.state(now),
		ExpiresAt: i.ExpiresAt,
		AlreadyIn: asker != "" && l.joined(asker),
		// The owner's invitees join at once; another member's wait, if the
		// LAN wants new members approved.
		NeedsApproval: i.Inviter != l.Owner && l.ApproveMembers,
	}
	if inv := w.people[i.Inviter]; inv != nil {
		p.Inviter = inv.Person
		p.Inviter.Email = ""
	}
	return p
}

func (w *World) inviteView(i *invite, now time.Time) proto.InviteView {
	return proto.InviteView{
		ID:        i.ID,
		LAN:       i.LAN,
		Inviter:   i.Inviter,
		Person:    i.Person,
		URL:       proto.InviteURL(w.publicURL, i.Token),
		CreatedAt: i.CreatedAt,
		ExpiresAt: i.ExpiresAt,
		State:     i.state(now),
	}
}
