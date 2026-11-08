package control

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// "Who sees whom" is security-critical (implementation.md §2.2): on any
// topology a device gets exactly the peers it shares a scope with, and each
// peer's scopes are exactly the shared ones. These tests build random
// worlds and check every device's map and every person's view against a
// plain restatement of the rules, then grep the JSON for anything that
// should not be there.

// token makes an id or a name no other token is a substring of.
func token(r *rand.Rand, prefix string) string {
	const letters = "abcdefghjkmnpqrstuvwxyz"
	b := []byte(prefix)
	for range 10 {
		b = append(b, letters[r.IntN(len(letters))])
	}
	return string(b)
}

func randomWorld(r *rand.Rand) *World {
	w := &World{
		Network: proto.Network{
			IPv4: netip.MustParsePrefix("10.67.0.0/16"), IPv6: netip.MustParsePrefix("fd12:3456:789a::/48"),
			Domain: proto.Domain,
		},
		publicURL:   "https://control.test",
		renewWindow: 14 * 24 * time.Hour,
		people:      map[proto.PersonID]*person{},
		byEmail:     map[string]*person{},
		lans:        map[proto.LANID]*lan{},
		devices:     map[proto.DeviceID]*device{},
		byMachine:   map[proto.MachinePublic]*device{},
		relays:      map[proto.RelayID]*proto.Relay{},
		relayPos:    map[proto.RelayID]int{},
		invites:     map[proto.InviteID]*invite{},
		byToken:     map[string]*invite{},
	}
	t0 := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	tick := 0
	at := func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }

	for range 1 + r.IntN(3) {
		id := proto.RelayID(token(r, "r_"))
		w.relays[id] = &proto.Relay{ID: id, Name: token(r, "RELAYNAME"), HostName: token(r, "host") + ".test", DERPPort: 443}
	}
	var relayIDs []proto.RelayID
	for id := range w.relays {
		relayIDs = append(relayIDs, id)
	}
	slices.Sort(relayIDs)

	var people []*person
	for range 1 + r.IntN(5) {
		p := &person{Person: proto.Person{
			ID: proto.PersonID(token(r, "p_")), Name: token(r, "label"), DisplayName: token(r, "DISPLAY"),
			Initial: "X", Email: token(r, "mail") + "@x.test",
		}}
		w.people[p.ID] = p
		w.byEmail[p.Email] = p
		people = append(people, p)
	}
	var lans []*lan
	for range r.IntN(5) {
		owner := people[r.IntN(len(people))]
		l := &lan{
			ID: proto.LANID(token(r, "l_")), Name: token(r, "LANNAME"), Owner: owner.ID, CreatedAt: at(),
			ApproveDevices: r.IntN(2) == 0, ApproveMembers: r.IntN(2) == 0,
			memberOf: map[proto.PersonID]*member{}, hasDevice: map[proto.DeviceID]*lanDevice{},
			hasPending: map[proto.DeviceID]*lanDevice{},
		}
		add := func(p proto.PersonID, role proto.Role, st proto.MemberState) {
			m := &member{Person: p, Role: role, State: st, JoinedAt: at()}
			l.members = append(l.members, m)
			l.memberOf[p] = m
		}
		add(owner.ID, proto.RoleOwner, proto.MemberJoined)
		for _, p := range people {
			if p.ID == owner.ID {
				continue
			}
			switch x := r.Float64(); {
			case x < 0.5:
				add(p.ID, proto.RoleMember, proto.MemberJoined)
			case x < 0.65:
				// Not in yet: must see nothing, and be seen nowhere.
				add(p.ID, proto.RoleMember, []proto.MemberState{proto.MemberInvited, proto.MemberPending}[r.IntN(2)])
			}
		}
		// A LAN's relays: a random prefix of all of them.
		l.relays = slices.Clone(relayIDs[:r.IntN(len(relayIDs)+1)])
		w.lans[l.ID] = l
		lans = append(lans, l)
		var joined []proto.PersonID
		for _, m := range l.members {
			if m.State == proto.MemberJoined {
				joined = append(joined, m.Person)
			}
		}
		for range r.IntN(4) {
			i := &invite{
				ID: proto.InviteID(token(r, "i_")), LAN: l.ID, Inviter: joined[r.IntN(len(joined))], Token: token(r, "TOKEN"),
				State: proto.InvitePending, CreatedAt: at(), ExpiresAt: t0.Add(48 * time.Hour),
			}
			if r.Float64() < 0.4 {
				// Made out to someone: only they may see it among their
				// invitations.
				if p := people[r.IntN(len(people))]; !l.joined(p.ID) {
					i.Person = p.ID
				}
			}
			w.invites[i.ID] = i
			w.byToken[i.Token] = i
		}
	}

	for range r.IntN(9) {
		owner := people[r.IntN(len(people))]
		var nk proto.NodePublic
		var mk proto.MachinePublic
		for i := range nk {
			nk[i] = byte(r.IntN(256))
			mk[i] = byte(r.IntN(256))
		}
		d := &device{
			ID: proto.DeviceID(token(r, "d_")), Owner: owner.ID, MachineKey: mk, NodeKey: nk,
			Name: token(r, "dev"), DisplayName: token(r, "DEVNAME"), Kind: proto.KindLaptop, OS: proto.MacOS,
			IPv4: netip.AddrFrom4([4]byte{10, 67, 1, byte(1 + len(w.devices))}), LoggedOut: r.Float64() < 0.15,
			Paused: map[proto.ScopeID]bool{}, lans: map[proto.LANID]bool{}, waiting: map[proto.LANID]bool{},
			opens: map[proto.ScopeID]map[proto.ServiceKey]bool{}, refused: map[proto.PersonID]bool{},
			ExpiresAt: t0.Add(180 * 24 * time.Hour),
		}
		if r.Float64() < 0.1 {
			// Its login ran out: in no map, still in its owner's view.
			d.ExpiresAt, d.expired = t0.Add(-time.Hour), true
		}
		d.IPv6 = v6For(w.Network.IPv6, d.IPv4)
		if r.Float64() < 0.5 && len(relayIDs) > 0 {
			d.HomeRelay = relayIDs[r.IntN(len(relayIDs))]
		}
		for _, port := range []uint16{22, 80, 445, 3389} {
			if r.Float64() < 0.4 {
				d.services = append(d.services, proto.Service{Kind: proto.ServiceOther, Name: token(r, "SVC"), Ports: []proto.PortRange{proto.Port(proto.TCP, port)}})
			}
		}
		for _, l := range lans {
			m := l.memberOf[owner.ID]
			// Mostly devices whose owner joined; now and then one whose
			// owner did not, which no view or map may show.
			if m == nil && r.Float64() > 0.05 || m != nil && r.Float64() > 0.6 {
				continue
			}
			ld := &lanDevice{Device: d.ID, JoinedAt: at()}
			sid := proto.LANScope(l.ID)
			if m != nil && (m.State == proto.MemberPending || r.Float64() < 0.2) {
				// Waiting for the owner: no map may count it.
				l.pending = append(l.pending, ld)
				l.hasPending[d.ID] = ld
				d.waiting[l.ID] = true
				continue
			}
			l.devices = append(l.devices, ld)
			l.hasDevice[d.ID] = ld
			d.lans[l.ID] = true
			if r.Float64() < 0.25 {
				d.Paused[sid] = true
			}
			for _, svc := range d.services {
				if r.Float64() < 0.5 {
					if d.opens[sid] == nil {
						d.opens[sid] = map[proto.ServiceKey]bool{}
					}
					d.opens[sid][svc.Key()] = true
				}
			}
		}
		for _, p := range people {
			if p.ID != owner.ID && r.Float64() < 0.3 {
				d.refused[p.ID] = true
			}
		}
		w.devices[d.ID] = d
		w.byMachine[mk] = d
	}
	return w
}

// scopesOf restates which LANs a device is really in.
func scopesOf(w *World, d *device) map[proto.ScopeID]bool {
	out := map[proto.ScopeID]bool{}
	for _, l := range w.lans {
		if l.hasDevice[d.ID] != nil && l.joined(d.Owner) && d.usable() {
			out[proto.LANScope(l.ID)] = true
		}
	}
	return out
}

func TestNetMapIsolationRandom(t *testing.T) {
	online := func(id proto.DeviceID) bool { return id[len(id)-1]%2 == 0 }
	// What the random worlds held, so the test cannot pass by testing
	// nothing.
	var peers, multiScope, hiddenByPause, refused, notJoined int
	var waitingDevices, waitingMembers, invitations, expired int
	for seed := range uint64(400) {
		r := rand.New(rand.NewPCG(seed, 7))
		w := randomWorld(r)
		for _, d := range w.devices {
			t.Run("", func(t *testing.T) { checkNetMap(t, w, d, online, seed) })
			if nm := w.netMap(d.ID, 1, online); nm != nil {
				for _, p := range nm.Peers {
					peers++
					if len(p.Scopes) > 1 {
						multiScope++
					}
					if p.RefusedByMe || p.RefusedByPeer {
						refused++
					}
				}
			}
			for l := range d.lans {
				if !w.lans[l].joined(d.Owner) {
					notJoined++
				}
				if d.Paused[proto.LANScope(l)] {
					hiddenByPause++
				}
			}
		}
		now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
		for _, p := range w.people {
			t.Run("", func(t *testing.T) { checkUserView(t, w, p, online, now, seed) })
			v := w.userView(p.ID, 1, online, now)
			invitations += len(v.Invitations)
			for _, lv := range v.LANs {
				if lv.Pending {
					waitingMembers++
				}
			}
		}
		for _, l := range w.lans {
			waitingDevices += len(l.pending)
		}
		for _, d := range w.devices {
			if d.expired {
				expired++
			}
		}
	}
	t.Logf("peers %d, through several scopes %d, refused %d; paused memberships %d; devices in a LAN their owner has not joined %d",
		peers, multiScope, refused, hiddenByPause, notJoined)
	t.Logf("waiting devices %d, people waiting to join %d, invitations %d, expired logins %d", waitingDevices, waitingMembers, invitations, expired)
	if peers < 500 || multiScope < 50 || refused < 50 || hiddenByPause < 50 || notJoined < 5 ||
		waitingDevices < 50 || waitingMembers < 50 || invitations < 50 || expired < 50 {
		t.Fatal("the random worlds are too thin to test much")
	}
}

func checkNetMap(t *testing.T, w *World, d *device, isOnline online, seed uint64) {
	nm := w.netMap(d.ID, 9, isOnline)
	if !d.usable() {
		if nm != nil {
			t.Fatalf("seed %d: logged-out device %s got a map", seed, d.ID)
		}
		return
	}
	if nm == nil {
		t.Fatalf("seed %d: no map for %s", seed, d.ID)
	}
	mine := scopesOf(w, d)

	got := map[proto.ScopeID]bool{}
	for _, s := range nm.Scopes {
		got[s.ID] = true
		l, _ := s.ID.LAN()
		var want []proto.Service
		for _, svc := range d.services {
			if d.opens[s.ID][svc.Key()] {
				want = append(want, svc)
			}
		}
		if fmt.Sprint(s.Open) != fmt.Sprint(nonNil(want)) {
			t.Errorf("seed %d: %s opens %v in %s, want %v", seed, d.ID, s.Open, s.ID, want)
		}
		if s.Paused != d.Paused[s.ID] || s.Name != w.lans[l].Name || !slices.Equal(s.Relays, w.lans[l].relays) {
			t.Errorf("seed %d: scope %s of %s: %+v", seed, s.ID, d.ID, s)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(mine) {
		t.Fatalf("seed %d: %s has scopes %v, want %v", seed, d.ID, got, mine)
	}

	peers := map[proto.DeviceID]proto.Peer{}
	for _, p := range nm.Peers {
		peers[p.ID] = p
	}
	for _, p := range w.devices {
		var want []proto.ScopeID
		if p.ID != d.ID && p.usable() {
			for s := range scopesOf(w, p) {
				if mine[s] && !p.Paused[s] {
					want = append(want, s)
				}
			}
		}
		slices.Sort(want)
		got, ok := peers[p.ID]
		if len(want) == 0 {
			if ok {
				t.Errorf("seed %d: %s sees %s through %v; they share nothing it may use", seed, d.ID, p.ID, got.Scopes)
			}
			continue
		}
		if !ok {
			t.Errorf("seed %d: %s does not see %s, though they share %v", seed, d.ID, p.ID, want)
			continue
		}
		if !slices.Equal(got.Scopes, want) {
			t.Errorf("seed %d: %s sees %s through %v, want %v", seed, d.ID, p.ID, got.Scopes, want)
		}
		other := p.Owner != d.Owner
		if got.RefusedByPeer != (other && p.refused[d.Owner]) || got.RefusedByMe != (other && d.refused[p.Owner]) {
			t.Errorf("seed %d: refusal flags of %s on %s: %+v", seed, p.ID, d.ID, got)
		}
		if got.NodeKey != p.NodeKey || got.Online != isOnline(p.ID) || got.FQDN != p.Name+"."+w.people[p.Owner].Name+"."+proto.Domain {
			t.Errorf("seed %d: node %s in %s's map: %+v", seed, p.ID, d.ID, got.Node)
		}
	}

	// Relays: the scopes' and the peers' homes, nothing else.
	wantRelays := map[proto.RelayID]bool{}
	for s := range mine {
		l, _ := s.LAN()
		for _, rl := range w.lans[l].relays {
			wantRelays[rl] = true
		}
	}
	for _, p := range nm.Peers {
		if p.HomeRelay != "" {
			wantRelays[p.HomeRelay] = true
		}
	}
	if d.HomeRelay != "" {
		wantRelays[d.HomeRelay] = true
	}
	gotRelays := map[proto.RelayID]bool{}
	for _, rl := range nm.Relays {
		gotRelays[rl.ID] = true
	}
	if fmt.Sprint(gotRelays) != fmt.Sprint(wantRelays) {
		t.Errorf("seed %d: %s gets relays %v, want %v", seed, d.ID, gotRelays, wantRelays)
	}

	// Nothing of a LAN it is not in, or of a device it cannot see, is in
	// the bytes it gets.
	b, _ := json.Marshal(nm)
	js := string(b)
	for _, l := range w.lans {
		if !mine[proto.LANScope(l.ID)] {
			if strings.Contains(js, string(l.ID)) || strings.Contains(js, l.Name) {
				t.Errorf("seed %d: %s's map mentions LAN %s (%s) it is not in", seed, d.ID, l.ID, l.Name)
			}
		}
	}
	for _, x := range w.devices {
		if _, ok := peers[x.ID]; !ok && x.ID != d.ID {
			if strings.Contains(js, string(x.ID)) || strings.Contains(js, x.NodeKey.String()) || strings.Contains(js, x.DisplayName) {
				t.Errorf("seed %d: %s's map mentions device %s it cannot see", seed, d.ID, x.ID)
			}
		}
	}
	for _, p := range w.people {
		if strings.Contains(js, p.Email) {
			t.Errorf("seed %d: %s's map carries an email", seed, d.ID)
		}
	}
}

func checkUserView(t *testing.T, w *World, me *person, isOnline online, now time.Time, seed uint64) {
	v := w.userView(me.ID, 9, isOnline, now)
	if v == nil {
		t.Fatalf("seed %d: no view for %s", seed, me.ID)
	}
	myLANs := map[proto.LANID]bool{}   // joined: the whole LAN
	waitLANs := map[proto.LANID]bool{} // waiting to be let in: a stub
	for _, l := range w.lans {
		switch m := l.memberOf[me.ID]; {
		case l.joined(me.ID):
			myLANs[l.ID] = true
		case m != nil && m.State == proto.MemberPending:
			waitLANs[l.ID] = true
		}
	}
	// pendingSeen restates who sees a device waiting for the owner: the
	// owner, and the device's own owner.
	pendingSeen := func(l *lan, d *device) bool {
		return myLANs[l.ID] && !d.LoggedOut && (l.Owner == me.ID || d.Owner == me.ID)
	}

	gotLANs := map[proto.LANID]bool{}
	for i, lv := range v.LANs {
		gotLANs[lv.ID] = true
		if i > 0 && w.lans[v.LANs[i-1].ID].memberOf[me.ID].JoinedAt.After(w.lans[lv.ID].memberOf[me.ID].JoinedAt) {
			t.Errorf("seed %d: %s's LANs are not in the order they joined", seed, me.ID)
		}
		l := w.lans[lv.ID]
		if waitLANs[l.ID] {
			if !lv.Pending || len(lv.Members) > 0 || len(lv.Devices) > 0 || len(lv.Invites) > 0 || lv.ApproveDevices || lv.ApproveMembers {
				t.Errorf("seed %d: %s, waiting to join %s, sees inside it: %+v", seed, me.ID, l.ID, lv)
			}
			continue
		}
		if lv.Pending || lv.ApproveDevices != l.ApproveDevices || lv.ApproveMembers != l.ApproveMembers {
			t.Errorf("seed %d: %s's view of %s: pending %v, switches %v %v", seed, me.ID, l.ID, lv.Pending, lv.ApproveDevices, lv.ApproveMembers)
		}
		for _, m := range lv.Members {
			wm := l.memberOf[m.Person]
			if wm == nil || wm.State != m.State || !(m.State == proto.MemberJoined || m.State == proto.MemberPending && l.Owner == me.ID) {
				t.Errorf("seed %d: %s sees %s as a %s member of %s", seed, me.ID, m.Person, m.State, l.ID)
			}
		}
		for _, ld := range lv.Devices {
			d := w.devices[ld.Device]
			switch {
			case d == nil:
				t.Errorf("seed %d: %s sees a device that does not exist", seed, me.ID)
			case ld.Pending:
				if l.hasPending[d.ID] == nil || !pendingSeen(l, d) {
					t.Errorf("seed %d: %s sees %s waiting in %s", seed, me.ID, d.ID, l.ID)
				}
			default:
				if d.LoggedOut || !l.joined(d.Owner) || l.hasDevice[d.ID] == nil {
					t.Errorf("seed %d: %s sees device %s in %s", seed, me.ID, ld.Device, l.ID)
				}
			}
			for _, s := range ld.Services {
				if d != nil && d.Owner != me.ID && !s.On {
					t.Errorf("seed %d: %s sees %s's switched-off service", seed, me.ID, d.ID)
				}
			}
		}
		named := 0
		for _, iv := range lv.Invites {
			i := w.invites[iv.ID]
			if i == nil || i.LAN != l.ID || i.state(now) != proto.InvitePending || !(l.Owner == me.ID || i.Inviter == me.ID) {
				t.Errorf("seed %d: %s sees invite %s of %s", seed, me.ID, iv.ID, l.ID)
			}
			if i != nil && i.Person != "" {
				named++
			}
		}
		for _, ip := range lv.Invited {
			i := w.invites[ip.Invite]
			if i == nil || i.Person != ip.Person || i.Inviter != ip.InvitedBy || !i.ExpiresAt.Equal(ip.ExpiresAt) ||
				!slices.ContainsFunc(lv.Invites, func(iv proto.InviteView) bool { return iv.ID == ip.Invite }) {
				t.Errorf("seed %d: %s sees %+v as invited to %s", seed, me.ID, ip, l.ID)
			}
		}
		if len(lv.Invited) != named {
			t.Errorf("seed %d: %s sees %d invited people in %s, %d invites made out to someone", seed, me.ID, len(lv.Invited), l.ID, named)
		}
	}
	wantLANs := map[proto.LANID]bool{}
	for l := range myLANs {
		wantLANs[l] = true
	}
	for l := range waitLANs {
		wantLANs[l] = true
	}
	if fmt.Sprint(gotLANs) != fmt.Sprint(wantLANs) {
		t.Fatalf("seed %d: %s sees LANs %v, want %v", seed, me.ID, gotLANs, wantLANs)
	}

	// Invitations: exactly the pending invites made out to me, for LANs I
	// am not in.
	gotInv := map[proto.InviteID]bool{}
	invLANs := map[proto.LANID]bool{}
	inviters := map[proto.PersonID]bool{}
	for _, p := range v.Invitations {
		gotInv[p.Invite] = true
		invLANs[p.LAN] = true
		inviters[p.Inviter.ID] = true
		if p.Inviter.Email != "" {
			t.Errorf("seed %d: an invitation carries the inviter's email", seed)
		}
	}
	wantInv := map[proto.InviteID]bool{}
	for _, i := range w.invites {
		if i.Person == me.ID && i.state(now) == proto.InvitePending && !w.lans[i.LAN].joined(me.ID) {
			wantInv[i.ID] = true
		}
	}
	if fmt.Sprint(gotInv) != fmt.Sprint(wantInv) {
		t.Errorf("seed %d: %s's invitations %v, want %v", seed, me.ID, gotInv, wantInv)
	}

	visible := map[proto.DeviceID]bool{}
	for _, d := range w.devices {
		if d.LoggedOut {
			continue
		}
		if d.Owner == me.ID {
			visible[d.ID] = true
		}
		for l := range myLANs {
			ll := w.lans[l]
			if ll.hasDevice[d.ID] != nil && ll.joined(d.Owner) || ll.hasPending[d.ID] != nil && pendingSeen(ll, d) {
				visible[d.ID] = true
			}
		}
	}
	gotDevices := map[proto.DeviceID]bool{}
	for _, dv := range v.Devices {
		gotDevices[dv.ID] = true
		d := w.devices[dv.ID]
		for _, l := range dv.LANs {
			if !myLANs[l] || w.lans[l].hasDevice[d.ID] == nil || !w.lans[l].joined(d.Owner) {
				t.Errorf("seed %d: %s sees %s in LAN %s", seed, me.ID, d.ID, l)
			}
		}
		if d.Owner != me.ID && (len(dv.Services) > 0 || len(dv.Refused) > 0) {
			t.Errorf("seed %d: %s sees the catalog or refusals of %s's device", seed, me.ID, d.Owner)
		}
		if d.Owner != me.ID && dv.RefusesMe != d.refused[me.ID] {
			t.Errorf("seed %d: RefusesMe of %s for %s", seed, d.ID, me.ID)
		}
		mine := d.Owner == me.ID
		if dv.Login.Expired != d.expired ||
			mine && (!dv.Login.ExpiresAt.Equal(d.ExpiresAt) || !dv.Login.RenewFrom.Before(dv.Login.ExpiresAt)) ||
			!mine && (!dv.Login.ExpiresAt.IsZero() || !dv.Login.RenewFrom.IsZero()) {
			t.Errorf("seed %d: %s's login as %s sees it: %+v", seed, d.ID, me.ID, dv.Login)
		}
	}
	if fmt.Sprint(gotDevices) != fmt.Sprint(visible) {
		t.Fatalf("seed %d: %s sees devices %v, want %v", seed, me.ID, gotDevices, visible)
	}

	people := map[proto.PersonID]bool{me.ID: true}
	for l := range myLANs {
		ll := w.lans[l]
		for _, m := range ll.members {
			if m.State == proto.MemberJoined || m.State == proto.MemberPending && ll.Owner == me.ID {
				people[m.Person] = true
			}
		}
	}
	for l := range waitLANs {
		people[w.lans[l].Owner] = true
	}
	for id := range visible {
		people[w.devices[id].Owner] = true
	}
	for _, i := range w.invites {
		l := w.lans[i.LAN]
		if i.Person != "" && myLANs[l.ID] && i.state(now) == proto.InvitePending && (l.Owner == me.ID || i.Inviter == me.ID) {
			people[i.Person] = true
		}
	}
	// The people I refused on my own devices are mine to know, even after
	// they left all my LANs.
	refusedByMe := map[proto.PersonID]bool{}
	for _, d := range w.devices {
		if d.Owner == me.ID {
			for p := range d.refused {
				refusedByMe[p] = true
			}
		}
	}
	b, _ := json.Marshal(v)
	js := string(b)
	for _, l := range w.lans {
		if !myLANs[l.ID] && !waitLANs[l.ID] && !invLANs[l.ID] && (strings.Contains(js, string(l.ID)) || strings.Contains(js, l.Name)) {
			t.Errorf("seed %d: %s's view mentions LAN %s it is not in", seed, me.ID, l.ID)
		}
	}
	for _, p := range w.people {
		if p.ID != me.ID && strings.Contains(js, p.Email) {
			t.Errorf("seed %d: %s's view carries %s's email", seed, me.ID, p.ID)
		}
		if !people[p.ID] && !inviters[p.ID] && (strings.Contains(js, string(p.ID)) && !refusedByMe[p.ID] || strings.Contains(js, p.DisplayName)) {
			t.Errorf("seed %d: %s's view mentions %s, who shares no LAN with them", seed, me.ID, p.ID)
		}
	}
	for _, d := range w.devices {
		if !visible[d.ID] && (strings.Contains(js, string(d.ID)) || strings.Contains(js, d.DisplayName)) {
			t.Errorf("seed %d: %s's view mentions device %s it cannot see", seed, me.ID, d.ID)
		}
	}
	for _, i := range w.invites {
		l := w.lans[i.LAN]
		mayKnow := myLANs[l.ID] && i.state(now) == proto.InvitePending && (l.Owner == me.ID || i.Inviter == me.ID)
		if !mayKnow && strings.Contains(js, i.Token) {
			t.Errorf("seed %d: %s's view carries the token of invite %s", seed, me.ID, i.ID)
		}
	}
}
