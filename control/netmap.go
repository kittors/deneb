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
		sc := proto.Scope{
			ID:     sid,
			Name:   l.Name,
			Open:   d.openIn(sid),
			Paused: d.Paused[sid],
			// The device's own order: spread by weight (the plan's §16).
			Relays: w.listFor(d, l.added),
			Kind:   proto.ScopeLAN,
		}
		nm.Scopes = append(nm.Scopes, sc)
		for _, r := range sc.Relays {
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
			if p == nil || p.ID == d.ID || !p.usable() || !l.joined(p.Owner) || !l.counts(p) {
				continue
			}
			if p.Paused[sid] {
				pausedBy[p.ID] = append(pausedBy[p.ID], sid)
			} else {
				shared[p.ID] = append(shared[p.ID], sid)
			}
		}
	}
	// Shares (M3). The shared device sees every usable device of the
	// receiver; each of those sees the shared device and nothing else of
	// its owner's. The receiver's side opens nothing: only the receiver
	// connects in, never the other way (DESIGN §12 item 12).
	addPeer := func(p *device, sid proto.ScopeID) {
		if p.Paused[sid] {
			pausedBy[p.ID] = append(pausedBy[p.ID], sid)
		} else {
			shared[p.ID] = append(shared[p.ID], sid)
		}
	}
	for _, sh := range w.sharesOf(d) {
		sid := shareScope(sh.ID)
		sc := proto.Scope{ID: sid, Paused: d.Paused[sid], Relays: w.listFor(d, w.shareOwn(sh)), Kind: proto.ScopeShare}
		if sh.Device == d.ID {
			sc.Name = "分享给" + w.people[sh.Receiver].DisplayName
			sc.Open = d.openIn(sid)
			for _, e := range w.devices {
				if e.Owner == sh.Receiver && e.ID != d.ID && e.usable() {
					addPeer(e, sid)
				}
			}
		} else {
			shd := w.devices[sh.Device]
			sc.Name = shd.DisplayName
			sc.Open = []proto.Service{}
			sc.NoInbound = true // the receiver only connects out
			addPeer(shd, sid)
		}
		nm.Scopes = append(nm.Scopes, sc)
		for _, r := range sc.Relays {
			relays[r] = true
		}
	}
	slices.SortFunc(nm.Scopes, func(a, b proto.Scope) int { return cmp.Compare(a.ID, b.ID) })

	peers := map[proto.DeviceID]bool{}
	for id := range shared {
		peers[id] = true
	}
	for id := range pausedBy {
		peers[id] = true
	}
	scopeRelays := map[proto.ScopeID][]proto.RelayID{}
	for _, sc := range nm.Scopes {
		scopeRelays[sc.ID] = sc.Relays
	}
	for pid := range peers {
		p := w.devices[pid]
		scopes, paused := nonNil(shared[pid]), pausedBy[pid]
		slices.Sort(scopes)
		slices.Sort(paused)
		peer := proto.Peer{
			Node:          w.node(p, isOnline),
			Scopes:        scopes,
			PausedScopes:  paused,
			RefusedByPeer: p.Owner != d.Owner && p.refused[d.Owner],
			RefusedByMe:   p.Owner != d.Owner && d.refused[p.Owner],
			Relays:        w.peerRelays(p, scopes, scopeRelays),
		}
		peer.HomeRelay = ""
		if len(peer.Relays) > 0 {
			peer.HomeRelay = peer.Relays[0]
		}
		nm.Peers = append(nm.Peers, peer)
	}
	slices.SortFunc(nm.Peers, func(a, b proto.Peer) int { return cmp.Compare(a.ID, b.ID) })
	// Every relay d may use (relaysOf): its scopes' lists and the
	// fallback, which a device in no scope yet still has. The peers'
	// relays are on the scopes' lists already.
	for _, r := range w.listFor(d, nil) {
		relays[r] = true
	}
	// A relay's owner shows only to those who see them anyway: a default
	// relay is on every LAN's list, and its owner is no one's business.
	owners := map[proto.PersonID]bool{d.Owner: true}
	for _, p := range nm.Peers {
		owners[p.Owner] = true
	}
	for r := range relays {
		if rl := w.relays[r]; rl != nil {
			c := *rl
			if !owners[c.Owner] {
				c.Owner = ""
			}
			nm.Relays = append(nm.Relays, c)
		}
	}
	slices.SortFunc(nm.Relays, func(a, b proto.Relay) int { return cmp.Compare(a.ID, b.ID) })
	return nm
}

// sharesOf lists the active shares d takes part in: the ones of d itself,
// and the ones made out to d's owner of another usable device. Sorted by
// id.
func (w *World) sharesOf(d *device) []*share {
	var out []*share
	for _, sh := range w.shares {
		if sh.State != shareActive {
			continue
		}
		switch {
		case sh.Device == d.ID:
			out = append(out, sh)
		case sh.Receiver == d.Owner:
			if shd := w.devices[sh.Device]; shd != nil && shd.usable() {
				out = append(out, sh)
			}
		}
	}
	slices.SortFunc(out, func(a, b *share) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// shareRelays are the relays a share goes through (pricing-and-relays
// §2.3): the sharer's own, then the fallback, so the share still connects
// through a public relay when the sharer's is down (M4). Admission lets
// the two sides in on the sharer's relay through this list.
func (w *World) shareRelays(sh *share) []proto.RelayID {
	return w.withFallback(w.shareOwn(sh))
}

// peerRelays are where a viewer reaches peer p (Peer.Relays): the relays p
// says it is connected to, best first, that are on the list of a scope
// the two share and p has not paused. Each lets both in, so a relay of
// someone's that only one of them may use is never offered. An engine
// from before M4 reports only its home relay.
func (w *World) peerRelays(p *device, scopes []proto.ScopeID, scopeRelays map[proto.ScopeID][]proto.RelayID) []proto.RelayID {
	allowed := map[proto.RelayID]bool{}
	for _, sid := range scopes {
		for _, r := range scopeRelays[sid] {
			allowed[r] = true
		}
	}
	reported := p.Relays
	if len(reported) == 0 && p.HomeRelay != "" {
		reported = []proto.RelayID{p.HomeRelay}
	}
	var out []proto.RelayID
	for _, r := range reported {
		// Not one the peer is kicked off, whatever it last said.
		if allowed[r] && !w.kicked(r, p) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// lansOf lists the LANs d is in, sorted by id, checking again that its
// owner is a joined member of each.
func (w *World) lansOf(d *device) []*lan {
	var out []*lan
	for id := range d.lans {
		if l := w.lans[id]; l != nil && l.joined(d.Owner) && l.counts(d) {
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
		PublicLAN:   d.PublicLAN,
		Caps:        slices.Clone(d.Caps),
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
		Features: slices.Clone(proto.M4Features),
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
			AddedRelays:    slices.Clone(l.added),
			ApproveDevices: l.ApproveDevices,
			ApproveMembers: l.ApproveMembers,
			Mode:           l.Mode,
		}
		if l.Mode == proto.LANModeShare && !owner {
			// A member of a sharing LAN sees whose it is and nothing of it:
			// not its devices, not its other members (scene 4). What they
			// are given shows in SharedWithMe.
			for _, m := range l.members {
				if m.Person == l.Owner || m.Person == me {
					lv.Members = append(lv.Members, proto.Member{Person: m.Person, Role: m.Role, State: m.State, JoinedAt: m.JoinedAt})
					people[m.Person] = true
				}
			}
			lv.Relays, lv.AddedRelays = []proto.RelayID{}, nil
			v.LANs = append(v.LANs, lv)
			continue
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
			if d == nil || d.LoggedOut || !l.joined(d.Owner) || !l.counts(d) {
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
			if d == nil || d.LoggedOut || !(owner || d.Owner == me) || !l.counts(d) {
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
		for _, r := range l.added {
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

	// Devices given to me: each says what I may use and nothing more.
	for _, sh := range w.shares {
		d := w.devices[sh.Device]
		if sh.State != shareActive || sh.Receiver != me || d == nil || d.LoggedOut {
			continue
		}
		n := w.node(d, isOnline)
		sd := proto.SharedDevice{
			Share: proto.ShareID(sh.ID),
			Device: proto.DeviceView{
				ID: d.ID, Owner: d.Owner, Name: d.Name, DisplayName: d.DisplayName, FQDN: n.FQDN,
				Kind: d.Kind, OS: d.OS, Addrs: d.addrs(), Online: n.Online, LastSeen: d.LastSeen,
				LANs: []proto.LANID{}, Login: proto.LoginInfo{Expired: d.expired}, PublicLAN: d.PublicLAN,
			},
			From:     sh.Sharer,
			Services: d.openIn(shareScope(sh.ID)),
			SharedAt: sh.CreatedAt,
		}
		if l := w.lans[d.PublicLAN]; d.PublicLAN != "" && l != nil {
			sd.FromLAN = l.Name
		}
		v.SharedWithMe = append(v.SharedWithMe, sd)
		people[sh.Sharer] = true
		people[d.Owner] = true
	}
	slices.SortFunc(v.SharedWithMe, func(a, b proto.SharedDevice) int {
		if c := a.SharedAt.Compare(b.SharedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Share, b.Share)
	})

	// Everyone in my seats, so the card "6 个位置都用上了" can show their
	// balls: the people I share devices with are mine to know.
	for id := range w.seatPeople(me, now) {
		people[id] = true
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
		dv.PublicLAN = d.PublicLAN
		if w.manager(d) == me {
			for _, sh := range w.shares {
				if sh.Device == d.ID && sh.State == shareActive {
					dv.Shares = append(dv.Shares, proto.ShareView{
						ID: proto.ShareID(sh.ID), Person: sh.Receiver, Sharer: sh.Sharer,
						Services: d.openIn(shareScope(sh.ID)), CreatedAt: sh.CreatedAt,
					})
				}
			}
			slices.SortFunc(dv.Shares, func(a, b proto.ShareView) int { return cmp.Compare(a.ID, b.ID) })
		}
		for l := range d.lans {
			if ll := w.lans[l]; inMine[l] && ll.joined(d.Owner) && ll.counts(d) && (ll.Mode != proto.LANModeShare || ll.Owner == me || d.Owner == me) {
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

	// The devices I would delete that were logged out (M7): what they
	// were, and the LANs they are still in.
	for _, d := range w.devices {
		if !d.LoggedOut || w.manager(d) != me {
			continue
		}
		dv := proto.DeviceView{
			ID: d.ID, Owner: d.Owner, Name: d.Name, DisplayName: d.DisplayName, FQDN: w.node(d, nil).FQDN,
			Kind: d.Kind, OS: d.OS, Addrs: d.addrs(), LastSeen: d.LastSeen, LANs: []proto.LANID{}, PublicLAN: d.PublicLAN,
		}
		for l := range d.lans {
			if inMine[l] {
				dv.LANs = append(dv.LANs, l)
			}
		}
		slices.Sort(dv.LANs)
		v.LoggedOutDevices = append(v.LoggedOutDevices, dv)
	}
	slices.SortFunc(v.LoggedOutDevices, func(a, b proto.DeviceView) int { return cmp.Compare(a.ID, b.ID) })

	// The relays of the shares I give and am given, and my own (M4).
	for _, sh := range w.shares {
		if sh.State == shareActive && (sh.Sharer == me || sh.Receiver == me) {
			for _, r := range w.shareRelays(sh) {
				relays[r] = true
			}
		}
	}
	v.MyRelays = w.myRelays(me, now, isOnline)
	v.RelayJoins = w.myJoins(me, now)
	for _, rv := range v.MyRelays {
		relays[rv.ID] = true
	}
	if p := w.people[me]; p != nil && p.Operator {
		v.Operator = w.operatorView(now, isOnline)
		for _, rv := range v.Operator.Relays {
			relays[rv.ID] = true
		}
	}
	for r := range relays {
		if rl := w.relays[r]; rl != nil {
			c := *rl
			if !people[c.Owner] {
				// A default relay's owner, whom I share nothing with.
				c.Owner = ""
			}
			v.Relays = append(v.Relays, c)
		}
	}
	slices.SortFunc(v.Relays, func(a, b proto.Relay) int { return cmp.Compare(a.ID, b.ID) })

	v.Seats = w.seats(me, now)
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

// seatPeople are the people taking one of me's seats at now
// (pricing-and-relays §1.4): me; who joined the LANs me owns; who me shares
// a device with, the shares revoked a moment ago included while undo may
// still bring them back. Each person once. Public devices, people waiting,
// and what others share with me take none.
func (w *World) seatPeople(me proto.PersonID, now time.Time) map[proto.PersonID]bool {
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
	for _, sh := range w.shares {
		if sh.Sharer == me && (sh.State == shareActive || sh.held(now)) {
			in[sh.Receiver] = true
		}
	}
	return in
}

// seats is me's seats as the view shows them.
func (w *World) seats(me proto.PersonID, now time.Time) proto.Seats {
	limit := w.seatLimitOf(me)
	s := proto.Seats{Limit: limit, Enforced: limit > 0, People: []proto.PersonID{}}
	for p := range w.seatPeople(me, now) {
		s.People = append(s.People, p)
	}
	slices.Sort(s.People)
	s.Used = len(s.People)
	if s.Enforced {
		s.Left = max(s.Limit-s.Used, 0)
	}
	return s
}

// manager is who manages device d: its owner, or for a public device its
// LAN's owner (§0.2).
func (w *World) manager(d *device) proto.PersonID {
	if l := w.lans[d.PublicLAN]; d.PublicLAN != "" && l != nil {
		return l.Owner
	}
	return d.Owner
}

// seatFull reports whether bringing person in would need a seat owner does
// not have: person takes none yet, and owner's are all used. Someone
// already counted always fits.
func (w *World) seatFull(owner, person proto.PersonID, now time.Time) bool {
	limit := w.seatLimitOf(owner)
	if limit <= 0 {
		return false
	}
	in := w.seatPeople(owner, now)
	return !in[person] && len(in) >= limit
}

// seatLimitOf is p's seats: the operator's limit for p if there is one,
// else the deployment's; 0 is no limit.
func (w *World) seatLimitOf(p proto.PersonID) int {
	if pp := w.people[p]; pp != nil && pp.SeatLimit != 0 {
		return max(pp.SeatLimit, 0)
	}
	return w.seatLimit
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
		// Or wait for a seat, if the owner's are all taken (§1.5 item 5).
		SeatsFull: asker != "" && !l.joined(asker) && w.seatFull(l.Owner, asker, now),
	}
	if inv := w.people[i.Inviter]; inv != nil {
		p.Inviter = inv.Person
		p.Inviter.Email = ""
	}
	return p
}

// withLAN adds to an invite link's preview who is in the LAN and how many
// devices: the card's "里面有 3 个人，5 台设备". Only the link's own
// preview, for whoever holds it, while it still works; the views list
// invitations without it, so no one's view names people it shares
// nothing with.
func (w *World) withLAN(p *proto.InvitePreview) {
	l := w.lans[p.LAN]
	if l == nil || p.State != proto.InvitePending && !p.AlreadyIn {
		return
	}
	for _, m := range l.members {
		if pp := w.people[m.Person]; pp != nil && m.State == proto.MemberJoined {
			c := pp.Person
			c.Email = ""
			p.Members = append(p.Members, c)
		}
	}
	for _, ld := range l.devices {
		if d := w.devices[ld.Device]; d != nil && d.usable() {
			p.Devices++
		}
	}
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
