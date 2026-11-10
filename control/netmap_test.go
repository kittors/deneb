package control

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
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
		relayMeta:   map[proto.RelayID]*relayMeta{},
		joins:       map[string]*relayJoin{},
		kicks:       map[string]*relayKick{},
		bySecret:    map[string]proto.RelayID{},

		kickedPerson: map[proto.RelayID]map[proto.PersonID]bool{},
		kickedDevice: map[proto.RelayID]map[proto.DeviceID]bool{},
		byNodeKey:    map[proto.NodePublic]*device{},
		invites:      map[proto.InviteID]*invite{},
		byToken:      map[string]*invite{},
		shares:       map[string]*share{},
	}
	t0 := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	tick := 0
	at := func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }

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

	// Relays (M4): public ones, people's own, some not registered yet
	// (no port: on no list), some the deployment's defaults.
	for range 1 + r.IntN(5) {
		id := proto.RelayID(token(r, "r_"))
		rl := &proto.Relay{ID: id, Name: token(r, "RELAYNAME"), HostName: token(r, "host") + ".test", DERPPort: 443}
		if r.Float64() < 0.35 {
			rl.Source = proto.RelayPublic
		} else {
			rl.Source, rl.Owner = proto.RelayServer, people[r.IntN(len(people))].ID
		}
		if r.Float64() < 0.15 {
			rl.DERPPort = 0
		}
		w.relays[id] = rl
		// Weights 0 (a standby) to 3 (the plan's §16).
		w.relayMeta[id] = &relayMeta{CreatedAt: t0, Weight: r.IntN(4)}
		w.relayPos[id] = -1
		if r.Float64() < 0.3 {
			w.relayPos[id] = r.IntN(3)
		}
	}
	var relayIDs []proto.RelayID
	for id := range w.relays {
		relayIDs = append(relayIDs, id)
	}
	slices.Sort(relayIDs)
	for _, id := range relayIDs {
		if w.relayPos[id] >= 0 {
			w.defaults = append(w.defaults, id)
		}
	}
	slices.SortStableFunc(w.defaults, func(a, b proto.RelayID) int { return w.relayPos[a] - w.relayPos[b] })
	var lans []*lan
	for range r.IntN(5) {
		owner := people[r.IntN(len(people))]
		l := &lan{
			ID: proto.LANID(token(r, "l_")), Name: token(r, "LANNAME"), Owner: owner.ID, CreatedAt: at(),
			ApproveDevices: r.IntN(2) == 0, ApproveMembers: r.IntN(2) == 0,
			Mode:     []proto.LANMode{proto.LANModeLAN, proto.LANModeLAN, proto.LANModeShare}[r.IntN(3)],
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
		// The relays dragged in: any of them, in any order.
		for _, i := range r.Perm(len(relayIDs))[:r.IntN(len(relayIDs)+1)] {
			l.added = append(l.added, relayIDs[i])
		}
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
		if r.Float64() < 0.6 {
			// What it says it is connected to (M4), home first; an
			// engine from before M4 says nothing.
			if d.HomeRelay != "" {
				d.Relays = append(d.Relays, d.HomeRelay)
			}
			for _, i := range r.Perm(len(relayIDs))[:r.IntN(len(relayIDs)+1)] {
				if !slices.Contains(d.Relays, relayIDs[i]) {
					d.Relays = append(d.Relays, relayIDs[i])
				}
			}
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

	// Shares (M3): some live, some revoked, a few revoked so lately that
	// they still hold the seat; each opens some of the device's services.
	var devs []*device
	for _, d := range w.devices {
		devs = append(devs, d)
	}
	slices.SortFunc(devs, func(a, b *device) int { return cmp.Compare(a.ID, b.ID) })
	for _, d := range devs {
		for _, p := range people {
			if p.ID == d.Owner || r.Float64() > 0.25 {
				continue
			}
			sh := &share{ID: token(r, "s_"), Device: d.ID, Receiver: p.ID, Sharer: d.Owner, State: shareActive, CreatedAt: at()}
			switch x := r.Float64(); {
			case x < 0.15:
				// Still holds the seat at the views' now.
				sh.State, sh.RevokedAt, sh.HoldUntil = shareRevoked, t0.Add(12*time.Hour-3*time.Second), t0.Add(12*time.Hour+7*time.Second)
			case x < 0.3:
				sh.State, sh.RevokedAt, sh.HoldUntil = shareRevoked, t0, t0.Add(seatHold)
			case x < 0.4:
				sh.State, sh.RevokedAt = shareLeft, t0
			}
			w.shares[sh.ID] = sh
			sid := shareScope(sh.ID)
			for _, svc := range d.services {
				if r.Float64() < 0.5 {
					if d.opens[sid] == nil {
						d.opens[sid] = map[proto.ServiceKey]bool{}
					}
					d.opens[sid][svc.Key()] = true
				}
			}
			if r.Float64() < 0.2 {
				d.Paused[sid] = true
			}
		}
	}
	for _, d := range w.devices {
		if d.usable() {
			w.byNodeKey[d.NodeKey] = d
		}
	}
	// Some relays offline or full; some people and devices kicked off
	// some relays, and some kicks lifted.
	offline, full := map[proto.RelayID]bool{}, map[proto.RelayID]bool{}
	for _, id := range relayIDs {
		offline[id], full[id] = r.Float64() < 0.15, r.Float64() < 0.15
	}
	w.relayOffline = func(id proto.RelayID) bool { return offline[id] }
	w.relayFull = func(id proto.RelayID) bool { return full[id] }
	var kdevs []*device
	for _, d := range w.devices {
		kdevs = append(kdevs, d)
	}
	slices.SortFunc(kdevs, func(a, b *device) int { return cmp.Compare(a.ID, b.ID) })
	for range r.IntN(6) {
		k := &relayKick{ID: token(r, "k_"), Relay: relayIDs[r.IntN(len(relayIDs))], At: at()}
		if r.Float64() < 0.5 || len(kdevs) == 0 {
			k.Person = people[r.IntN(len(people))].ID
		} else {
			k.Device = kdevs[r.IntN(len(kdevs))].ID
		}
		if r.Float64() < 0.2 {
			k.LiftedAt = at()
		}
		w.kicks[k.ID] = k
		w.indexKick(k)
	}
	w.finishRelays()
	return w
}

// refKicked restates whether d is kicked off relay id: a kick in force
// of d, or of its owner.
func refKicked(w *World, id proto.RelayID, d *device) bool {
	for _, k := range w.kicks {
		if k.Relay == id && k.LiftedAt.IsZero() && (k.Device == d.ID || k.Person == d.Owner) {
			return true
		}
	}
	return false
}

// refSpread restates how a group of relays sorts for device d (the
// plan's §16): up and weighing something, by weighted rendezvous score,
// highest first; the standbys; and down: offline, or full and d not on
// it. The order given within the last two.
func refSpread(w *World, d *device, ids []proto.RelayID) (up, standby, down []proto.RelayID) {
	score := func(id proto.RelayID) float64 {
		h := sha256.Sum256([]byte(string(d.ID) + "\x00" + string(id)))
		u := (float64(binary.BigEndian.Uint64(h[:8])>>11) + 0.5) / (1 << 53)
		return float64(w.relayMeta[id].Weight) / -math.Log(u)
	}
	for _, id := range ids {
		switch {
		case w.relayOffline(id) || w.relayFull(id) && !slices.Contains(d.Relays, id):
			down = append(down, id)
		case w.relayMeta[id].Weight == 0:
			standby = append(standby, id)
		default:
			up = append(up, id)
		}
	}
	slices.SortStableFunc(up, func(a, b proto.RelayID) int { return cmp.Compare(score(b), score(a)) })
	return up, standby, down
}

// refListFor restates device d's list for a scope whose own relays are
// own: own's registered relays up, by score, and its standbys; the
// defaults by position; the other public relays up, by score, and their
// standbys; then own's down and the public ones down. Each once, and
// none d is kicked off.
func refListFor(w *World, d *device, own []proto.RelayID) []proto.RelayID {
	var ids []proto.RelayID
	for id := range w.relays {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var defaults, public []proto.RelayID
	for _, id := range ids {
		switch {
		case w.relayPos[id] >= 0:
			defaults = append(defaults, id)
		case w.relays[id].Source == proto.RelayPublic:
			public = append(public, id)
		}
	}
	slices.SortStableFunc(defaults, func(a, b proto.RelayID) int { return w.relayPos[a] - w.relayPos[b] })
	taken := map[proto.RelayID]bool{}
	group := func(list []proto.RelayID) []proto.RelayID {
		var g []proto.RelayID
		for _, id := range list {
			if w.relays[id].DERPPort != 0 && !taken[id] && !refKicked(w, id, d) {
				taken[id] = true
				g = append(g, id)
			}
		}
		return g
	}
	ownUp, ownStandby, ownDown := refSpread(w, d, group(own))
	defs := group(defaults)
	pubUp, pubStandby, pubDown := refSpread(w, d, group(public))
	out := []proto.RelayID{}
	for _, part := range [][]proto.RelayID{ownUp, ownStandby, defs, pubUp, pubStandby, ownDown, pubDown} {
		out = append(out, part...)
	}
	return out
}

// refScopeListFor restates d's list of a LAN (what was dragged in, then
// the fallback) or of a share (the sharer's own relays, then the
// fallback).
func refScopeListFor(w *World, d *device, sid proto.ScopeID) []proto.RelayID {
	if l, ok := sid.LAN(); ok {
		return refListFor(w, d, w.lans[l].added)
	}
	shid, _ := sid.Share()
	sh := w.shares[string(shid)]
	var own []proto.RelayID
	for id, rl := range w.relays {
		if rl.Owner == sh.Sharer && rl.Source != proto.RelayPublic && w.relayPos[id] < 0 {
			own = append(own, id)
		}
	}
	slices.Sort(own)
	return refListFor(w, d, own)
}

// refRelayList restates a scope's relay list (M4): the registered relays
// of own, then the fallback, the defaults by position and then the other
// public relays by ID, each once.
func refRelayList(w *World, own []proto.RelayID) []proto.RelayID {
	var ids []proto.RelayID
	for id := range w.relays {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var defaults, public []proto.RelayID
	for _, id := range ids {
		switch {
		case w.relayPos[id] >= 0:
			defaults = append(defaults, id)
		case w.relays[id].Source == proto.RelayPublic:
			public = append(public, id)
		}
	}
	slices.SortStableFunc(defaults, func(a, b proto.RelayID) int { return w.relayPos[a] - w.relayPos[b] })
	out := []proto.RelayID{}
	for _, list := range [][]proto.RelayID{own, defaults, public} {
		for _, id := range list {
			if w.relays[id].DERPPort != 0 && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// refScopeRelays restates the list of a LAN (what was dragged in, then
// the fallback) or of a share (the sharer's own relays, then the
// fallback).
func refScopeRelays(w *World, sid proto.ScopeID) []proto.RelayID {
	if l, ok := sid.LAN(); ok {
		return refRelayList(w, w.lans[l].added)
	}
	shid, _ := sid.Share()
	sh := w.shares[string(shid)]
	var own []proto.RelayID
	for id, rl := range w.relays {
		if rl.Owner == sh.Sharer && rl.Source != proto.RelayPublic && w.relayPos[id] < 0 {
			own = append(own, id)
		}
	}
	slices.Sort(own)
	return refRelayList(w, own)
}

// shareScopesOf restates which shares a usable device takes part in: its
// own live shares, and the live shares made out to its owner of another
// usable device.
func shareScopesOf(w *World, d *device) map[proto.ScopeID]*share {
	out := map[proto.ScopeID]*share{}
	if !d.usable() {
		return out
	}
	for _, sh := range w.shares {
		if sh.State != shareActive {
			continue
		}
		shd := w.devices[sh.Device]
		if sh.Device == d.ID || sh.Receiver == d.Owner && shd != nil && shd.ID != d.ID && shd.usable() {
			out[shareScope(sh.ID)] = sh
		}
	}
	return out
}

// scopesOf restates which LANs a device is really in.
func scopesOf(w *World, d *device) map[proto.ScopeID]bool {
	out := map[proto.ScopeID]bool{}
	for _, l := range w.lans {
		if l.hasDevice[d.ID] != nil && l.joined(d.Owner) && d.usable() && (l.Mode != proto.LANModeShare || d.Owner == l.Owner) {
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
	var shareScopes, receiverSide, heldSeats int
	var peerRelayed, peerFiltered, ownOnList, unreadyAdded, admitted, turnedAway int
	var reordered, kickedOff int
	for seed := range uint64(400) {
		r := rand.New(rand.NewPCG(seed, 7))
		w := randomWorld(r)
		for _, d := range w.devices {
			t.Run("", func(t *testing.T) { checkNetMap(t, w, d, online, seed) })
			if nm := w.netMap(d.ID, 1, online); nm != nil {
				for _, sc := range nm.Scopes {
					if sh := w.shares[strings.TrimPrefix(string(sc.ID), "share/")]; sh != nil {
						shareScopes++
						if sh.Device != d.ID {
							receiverSide++
						}
					}
				}
				for _, p := range nm.Peers {
					peers++
					if len(p.Scopes) > 1 {
						multiScope++
					}
					if p.RefusedByMe || p.RefusedByPeer {
						refused++
					}
					if len(p.Relays) > 0 {
						peerRelayed++
					}
					if len(p.Relays) < len(w.devices[p.ID].Relays) {
						peerFiltered++
					}
				}
				for _, sc := range nm.Scopes {
					for _, rl := range sc.Relays {
						if w.relays[rl].Source != proto.RelayPublic && w.relayPos[rl] < 0 {
							ownOnList++
						}
					}
					if l, ok := sc.ID.LAN(); ok && len(sc.Relays) > 1 && !slices.Equal(sc.Relays, w.lans[l].relays) {
						reordered++
					}
				}
			}
			for id := range w.relays {
				if w.admits(id, d.NodeKey) {
					admitted++
				} else if d.usable() && ready(w.relays[id]) {
					turnedAway++
					if w.kicked(id, d) {
						kickedOff++
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
			for _, id := range l.added {
				if !ready(w.relays[id]) {
					unreadyAdded++
				}
			}
		}
		for _, d := range w.devices {
			if d.expired {
				expired++
			}
		}
		for _, sh := range w.shares {
			if sh.held(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)) {
				heldSeats++
			}
		}
	}
	t.Logf("peers %d, through several scopes %d, refused %d; paused memberships %d; devices in a LAN their owner has not joined %d",
		peers, multiScope, refused, hiddenByPause, notJoined)
	t.Logf("waiting devices %d, people waiting to join %d, invitations %d, expired logins %d", waitingDevices, waitingMembers, invitations, expired)
	t.Logf("share scopes %d (receiver's side %d), seats held after a revoke %d", shareScopes, receiverSide, heldSeats)
	t.Logf("relays: peers with a relay %d, with relays left out %d; own relays on lists %d, not registered %d; admitted %d, turned away %d (kicked %d); lists in a device's own order %d",
		peerRelayed, peerFiltered, ownOnList, unreadyAdded, admitted, turnedAway, kickedOff, reordered)
	if peers < 500 || multiScope < 50 || refused < 50 || hiddenByPause < 50 || notJoined < 5 ||
		waitingDevices < 50 || waitingMembers < 50 || invitations < 50 || expired < 50 ||
		shareScopes < 200 || receiverSide < 100 || heldSeats < 20 ||
		peerRelayed < 200 || peerFiltered < 50 || ownOnList < 200 || unreadyAdded < 20 || admitted < 500 || turnedAway < 100 ||
		kickedOff < 50 || reordered < 100 {
		t.Fatal("the random worlds are too thin to test much")
	}
}

func checkNetMap(t *testing.T, w *World, d *device, isOnline online, seed uint64) {
	nm := w.netMap(d.ID, 9, isOnline)
	if !d.usable() {
		if nm != nil {
			t.Fatalf("seed %d: logged-out device %s got a map", seed, d.ID)
		}
		for id := range w.relays {
			if w.admits(id, d.NodeKey) {
				t.Errorf("seed %d: relay %s lets in %s, logged out or run out", seed, id, d.ID)
			}
		}
		return
	}
	if nm == nil {
		t.Fatalf("seed %d: no map for %s", seed, d.ID)
	}
	mine := scopesOf(w, d)
	shares := shareScopesOf(w, d)

	got := map[proto.ScopeID]bool{}
	for _, s := range nm.Scopes {
		got[s.ID] = true
		var want []proto.Service
		for _, svc := range d.services {
			if d.opens[s.ID][svc.Key()] {
				want = append(want, svc)
			}
		}
		if sh := shares[s.ID]; sh != nil {
			// The shared device opens what the share opens; the receiver's
			// side opens nothing: only the receiver connects in.
			if sh.Device != d.ID {
				want = nil
			}
			if fmt.Sprint(s.Open) != fmt.Sprint(nonNil(want)) || s.Paused != d.Paused[s.ID] {
				t.Errorf("seed %d: share scope %s of %s: %+v, want open %v", seed, s.ID, d.ID, s, want)
			}
			continue
		}
		l, _ := s.ID.LAN()
		if fmt.Sprint(s.Open) != fmt.Sprint(nonNil(want)) {
			t.Errorf("seed %d: %s opens %v in %s, want %v", seed, d.ID, s.Open, s.ID, want)
		}
		if s.Paused != d.Paused[s.ID] || s.Name != w.lans[l].Name || !slices.Equal(s.Relays, refScopeListFor(w, d, s.ID)) {
			t.Errorf("seed %d: scope %s of %s: %+v", seed, s.ID, d.ID, s)
		}
	}
	wantScopes := map[proto.ScopeID]bool{}
	for s := range mine {
		wantScopes[s] = true
	}
	for s := range shares {
		wantScopes[s] = true
	}
	if fmt.Sprint(got) != fmt.Sprint(wantScopes) {
		t.Fatalf("seed %d: %s has scopes %v, want %v", seed, d.ID, got, wantScopes)
	}

	peers := map[proto.DeviceID]proto.Peer{}
	for _, p := range nm.Peers {
		peers[p.ID] = p
	}
	for _, p := range w.devices {
		// The scopes they share, apart by whether p paused them.
		var want, wantPaused []proto.ScopeID
		if p.ID != d.ID && p.usable() {
			for s := range scopesOf(w, p) {
				switch {
				case mine[s] && !p.Paused[s]:
					want = append(want, s)
				case mine[s]:
					wantPaused = append(wantPaused, s)
				}
			}
			// A share joins the shared device and the receiver's devices,
			// never two of the receiver's devices.
			for sid, sh := range shares {
				if sh.Device == d.ID && p.Owner == sh.Receiver || sh.Device == p.ID && d.Owner == sh.Receiver {
					if p.Paused[sid] {
						wantPaused = append(wantPaused, sid)
					} else {
						want = append(want, sid)
					}
				}
			}
		}
		slices.Sort(want)
		slices.Sort(wantPaused)
		got, ok := peers[p.ID]
		if len(want) == 0 && len(wantPaused) == 0 {
			if ok {
				t.Errorf("seed %d: %s sees %s through %v; they share nothing", seed, d.ID, p.ID, got.Scopes)
			}
			continue
		}
		if !ok {
			t.Errorf("seed %d: %s does not see %s, though they share %v (paused %v)", seed, d.ID, p.ID, want, wantPaused)
			continue
		}
		if !slices.Equal(got.Scopes, nonNil(want)) || !slices.Equal(got.PausedScopes, wantPaused) {
			t.Errorf("seed %d: %s sees %s through %v (paused %v), want %v (paused %v)", seed, d.ID, p.ID, got.Scopes, got.PausedScopes, want, wantPaused)
		}
		other := p.Owner != d.Owner
		if got.RefusedByPeer != (other && p.refused[d.Owner]) || got.RefusedByMe != (other && d.refused[p.Owner]) {
			t.Errorf("seed %d: refusal flags of %s on %s: %+v", seed, p.ID, d.ID, got)
		}
		if got.NodeKey != p.NodeKey || got.Online != isOnline(p.ID) || got.FQDN != p.Name+"."+w.people[p.Owner].Name+"."+proto.Domain {
			t.Errorf("seed %d: node %s in %s's map: %+v", seed, p.ID, d.ID, got.Node)
		}
		// Through a relay: the ones the peer is on that are on a list of
		// a scope both are in and the peer has not paused (M4).
		allowed := map[proto.RelayID]bool{}
		for _, sid := range want {
			for _, rl := range refScopeListFor(w, d, sid) {
				allowed[rl] = !refKicked(w, rl, p)
			}
		}
		reported := p.Relays
		if len(reported) == 0 && p.HomeRelay != "" {
			reported = []proto.RelayID{p.HomeRelay}
		}
		var wantRelays []proto.RelayID
		for _, rl := range reported {
			if allowed[rl] && !slices.Contains(wantRelays, rl) {
				wantRelays = append(wantRelays, rl)
			}
		}
		home := proto.RelayID("")
		if len(wantRelays) > 0 {
			home = wantRelays[0]
		}
		if !slices.Equal(got.Relays, wantRelays) || got.HomeRelay != home {
			t.Errorf("seed %d: %s reaches %s through %v (home %q), want %v", seed, d.ID, p.ID, got.Relays, got.HomeRelay, wantRelays)
		}
		for _, rl := range got.Relays {
			if !w.admits(rl, d.NodeKey) || !w.admits(rl, p.NodeKey) {
				t.Errorf("seed %d: relay %s between %s and %s does not let both in", seed, rl, d.ID, p.ID)
			}
		}
	}

	// Relays: the fallback and the scopes' lists, nothing else (M4).
	wantRelays := map[proto.RelayID]bool{}
	for _, rl := range refListFor(w, d, nil) {
		wantRelays[rl] = true
	}
	for _, s := range nm.Scopes {
		for _, rl := range refScopeListFor(w, d, s.ID) {
			wantRelays[rl] = true
		}
	}
	gotRelays := map[proto.RelayID]bool{}
	for _, rl := range nm.Relays {
		gotRelays[rl.ID] = true
	}
	if fmt.Sprint(gotRelays) != fmt.Sprint(wantRelays) {
		t.Errorf("seed %d: %s gets relays %v, want %v", seed, d.ID, gotRelays, wantRelays)
	}
	// Admission (§2.6.3): a public relay lets in every device logged in,
	// someone's own relay exactly the devices whose map has it.
	for id, rl := range w.relays {
		want := ready(rl) && !refKicked(w, id, d) && (rl.Source == proto.RelayPublic || gotRelays[id])
		if w.admits(id, d.NodeKey) != want {
			t.Errorf("seed %d: relay %s (%s, owner %s) lets %s in: %v, want %v", seed, id, rl.Source, rl.Owner, d.ID, !want, want)
		}
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
	for _, sh := range w.shares {
		if shares[shareScope(sh.ID)] == nil && strings.Contains(js, sh.ID) {
			t.Errorf("seed %d: %s's map mentions share %s it is not in", seed, d.ID, sh.ID)
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
		if lv.Pending || lv.ApproveDevices != l.ApproveDevices || lv.ApproveMembers != l.ApproveMembers || lv.Mode != l.Mode {
			t.Errorf("seed %d: %s's view of %s: pending %v, switches %v %v, mode %q", seed, me.ID, l.ID, lv.Pending, lv.ApproveDevices, lv.ApproveMembers, lv.Mode)
		}
		if l.Mode == proto.LANModeShare && l.Owner != me.ID {
			// A member of a sharing LAN: whose it is, and nothing of it.
			for _, m := range lv.Members {
				if m.Person != l.Owner && m.Person != me.ID {
					t.Errorf("seed %d: %s sees member %s of sharing LAN %s", seed, me.ID, m.Person, l.ID)
				}
			}
			if len(lv.Devices) > 0 {
				t.Errorf("seed %d: %s sees %d devices of sharing LAN %s", seed, me.ID, len(lv.Devices), l.ID)
			}
			continue
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

	// Seats: me, who joined the LANs me owns, who me shares with (live, or
	// revoked a moment ago), once each.
	seatsWant := map[proto.PersonID]bool{me.ID: true}
	for _, l := range w.lans {
		for _, m := range l.members {
			if l.Owner == me.ID && m.State == proto.MemberJoined {
				seatsWant[m.Person] = true
			}
		}
	}
	for _, sh := range w.shares {
		if sh.Sharer == me.ID && (sh.State == shareActive || sh.held(now)) {
			seatsWant[sh.Receiver] = true
		}
	}
	gotSeats := map[proto.PersonID]bool{}
	for _, p := range v.Seats.People {
		gotSeats[p] = true
	}
	if fmt.Sprint(gotSeats) != fmt.Sprint(seatsWant) || v.Seats.Used != len(seatsWant) {
		t.Errorf("seed %d: %s's seats %v, want %v", seed, me.ID, v.Seats, seatsWant)
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
			if ll.Mode == proto.LANModeShare && (ll.Owner != me.ID || d.Owner != ll.Owner) {
				continue // its owner sees its own devices there; members see none
			}
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
			ll := w.lans[l]
			if !myLANs[l] || ll.hasDevice[d.ID] == nil || !ll.joined(d.Owner) || ll.Mode == proto.LANModeShare && d.Owner != ll.Owner {
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
			if ll.Mode == proto.LANModeShare && ll.Owner != me.ID && m.Person != ll.Owner {
				continue
			}
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
	for id := range seatsWant {
		people[id] = true
	}
	// Devices given to me: in "共享给我", with their giver and owner, and
	// nothing of the device but what I may use.
	mentionable := map[proto.DeviceID]bool{}
	for id := range visible {
		mentionable[id] = true
	}
	wantShared := map[proto.ShareID]*share{}
	for _, sh := range w.shares {
		d := w.devices[sh.Device]
		if sh.State == shareActive && sh.Receiver == me.ID && d != nil && !d.LoggedOut {
			wantShared[proto.ShareID(sh.ID)] = sh
			mentionable[d.ID] = true
			people[sh.Sharer] = true
			people[d.Owner] = true
		}
	}
	gotShared := map[proto.ShareID]bool{}
	for _, sd := range v.SharedWithMe {
		gotShared[sd.Share] = true
		sh := wantShared[sd.Share]
		if sh == nil {
			t.Errorf("seed %d: %s is given %s, which is not theirs", seed, me.ID, sd.Share)
			continue
		}
		d := w.devices[sh.Device]
		var open []proto.Service
		for _, svc := range d.services {
			if d.opens[shareScope(sh.ID)][svc.Key()] {
				open = append(open, svc)
			}
		}
		if sd.Device.ID != d.ID || sd.From != sh.Sharer || fmt.Sprint(sd.Services) != fmt.Sprint(nonNil(open)) ||
			len(sd.Device.LANs) != 0 || len(sd.Device.Services) != 0 || len(sd.Device.Refused) != 0 || len(sd.Device.Shares) != 0 ||
			!sd.Device.Login.ExpiresAt.IsZero() {
			t.Errorf("seed %d: %s's shared device %s says too much or too little: %+v", seed, me.ID, sd.Share, sd)
		}
	}
	if len(gotShared) != len(wantShared) {
		t.Errorf("seed %d: %s is given %d devices, want %d", seed, me.ID, len(gotShared), len(wantShared))
	}
	// Shares of my own devices list whom they are given to.
	for _, dv := range v.Devices {
		d := w.devices[dv.ID]
		want := 0
		for _, sh := range w.shares {
			if sh.Device == d.ID && sh.State == shareActive {
				want++
			}
		}
		if d.Owner != me.ID {
			want = 0
		}
		if len(dv.Shares) != want {
			t.Errorf("seed %d: %s sees %d shares of %s, want %d", seed, me.ID, len(dv.Shares), d.ID, want)
		}
	}
	// My devices that were logged out, and my LANs' public ones that were,
	// are mine to delete (M7): listed for me, and for no one else.
	wantOut := map[proto.DeviceID]bool{}
	for _, d := range w.devices {
		manager := d.Owner
		if l := w.lans[d.PublicLAN]; d.PublicLAN != "" && l != nil {
			manager = l.Owner
		}
		if d.LoggedOut && manager == me.ID {
			wantOut[d.ID] = true
			mentionable[d.ID] = true
		}
	}
	if len(v.LoggedOutDevices) != len(wantOut) {
		t.Errorf("seed %d: %s's logged-out devices: %d, want %d", seed, me.ID, len(v.LoggedOutDevices), len(wantOut))
	}
	for _, dv := range v.LoggedOutDevices {
		if !wantOut[dv.ID] {
			t.Errorf("seed %d: %s's logged-out devices have %s", seed, me.ID, dv.ID)
		}
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
		if !mentionable[d.ID] && (strings.Contains(js, string(d.ID)) || strings.Contains(js, d.DisplayName)) {
			t.Errorf("seed %d: %s's view mentions device %s it cannot see", seed, me.ID, d.ID)
		}
	}
	for _, sh := range w.shares {
		mine := sh.State == shareActive && (sh.Receiver == me.ID || w.devices[sh.Device].Owner == me.ID)
		if !mine && strings.Contains(js, sh.ID) {
			t.Errorf("seed %d: %s's view mentions share %s", seed, me.ID, sh.ID)
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
