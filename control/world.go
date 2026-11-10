package control

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/kittors/deneb/proto"
)

// World is the whole database in memory, read-only once built. After every
// change the server builds a new one and swaps it in, so network maps and
// views are computed from a consistent picture without holding any lock.
// At M1's size (a handful of people) a full reload costs about a
// millisecond; deltas can come when there are thousands of devices.
type World struct {
	Network   proto.Network
	publicURL string // how devices and browsers reach the server, for links
	// renewWindow is how long before a login runs out the interface starts
	// to ask for it to be renewed.
	renewWindow time.Duration

	people    map[proto.PersonID]*person
	byEmail   map[string]*person
	lans      map[proto.LANID]*lan
	devices   map[proto.DeviceID]*device
	byMachine map[proto.MachinePublic]*device
	relays    map[proto.RelayID]*proto.Relay
	relayPos  map[proto.RelayID]int // each relay's default_pos, as stored
	defaults  []proto.RelayID       // the deployment's default relays, in position order
	// fallback is what every LAN and share falls back on after its own
	// relays (M4): the defaults, then the other public relays.
	fallback  []proto.RelayID
	relayMeta map[proto.RelayID]*relayMeta
	joins     map[string]*relayJoin // join codes not used yet, expired ones too until swept
	kicks     map[string]*relayKick // every kick, lifted ones too
	// kickedPerson and kickedDevice are the kicks in force, per relay.
	kickedPerson map[proto.RelayID]map[proto.PersonID]bool
	kickedDevice map[proto.RelayID]map[proto.DeviceID]bool
	// relayOffline and relayFull say whether a relay that syncs is offline
	// or full, so lists put it last; the server sets them, a test world
	// may leave them nil.
	relayOffline func(proto.RelayID) bool
	relayFull    func(proto.RelayID) bool
	bySecret     map[string]proto.RelayID     // a relay's secret hash → the relay
	byNodeKey    map[proto.NodePublic]*device // usable devices only
	// relayStatus says whether a relay is online and when it last checked
	// in; the server sets it, a test world may leave it nil.
	relayStatus func(proto.RelayID) (bool, time.Time)
	invites     map[proto.InviteID]*invite
	byToken     map[string]*invite
	shares      map[string]*share // every share, revoked ones too
	// seatLimit is the deployment's seats per person; 0 means no limit.
	seatLimit int
}

// share is one device shared with one person (M3).
type share struct {
	ID        string
	Device    proto.DeviceID
	Receiver  proto.PersonID
	Sharer    proto.PersonID // who shared it: the device's owner, or its LAN's owner for a public device
	State     string         // shareActive or shareRevoked
	CreatedAt time.Time
	RevokedAt time.Time
	// HoldUntil is when a revoked share's seat comes free: when the client
	// says the undo is gone (share.release), or seatHold after the revoke
	// at the latest.
	HoldUntil time.Time
}

const (
	shareActive  = "active"
	shareRevoked = "revoked" // taken back by the giver; undo brings it back
	shareLeft    = "left"    // given up by the receiver
)

// seatHold is how long at most a revoked share keeps its seat: the toast
// offers undo for 4 seconds and then the client releases it; this bounds
// a client that went away first (pricing-and-relays §1.5 item 7).
const seatHold = 10 * time.Second

// held reports whether a revoked share still holds its seat at now.
func (s *share) held(now time.Time) bool {
	return s.State == shareRevoked && now.Before(s.HoldUntil)
}

func shareScope(id string) proto.ScopeID { return proto.ShareScope(proto.ShareID(id)) }

type person struct {
	proto.Person
	CreatedAt time.Time
	// SeatLimit is the operator's limit for this person alone, for a
	// test: 0 is the deployment's, -1 none.
	SeatLimit int
	// Operator: one of the deployment's operators (deneb-control
	// operators), who manage the public relays on the ops page.
	Operator bool
}

type lan struct {
	ID        proto.LANID
	Name      string
	Owner     proto.PersonID
	CreatedAt time.Time
	// ApproveDevices and ApproveMembers are the LAN's "新设备要同意" and
	// "新成员要同意": a device or a person brought in by anyone but the
	// owner waits for the owner (M2).
	ApproveDevices bool
	ApproveMembers bool
	// Mode is how the LAN works (M3): proto.LANModeShare holds only the
	// owner's devices and its public devices; members get shares.
	Mode      proto.LANMode
	members   []*member // in the order they joined, every state
	memberOf  map[proto.PersonID]*member
	devices   []*lanDevice // in, in the order they joined
	hasDevice map[proto.DeviceID]*lanDevice
	// pending are the devices waiting for the owner. They are kept apart
	// from devices so nothing that computes a network map can count them.
	pending    []*lanDevice
	hasPending map[proto.DeviceID]*lanDevice
	// added are the relays dragged in (relay.add), in order (M4); relays
	// is the list the LAN's devices try: added, then the fallback.
	added  []proto.RelayID
	relays []proto.RelayID
}

// counts reports whether device d may be in l: any member's device, but in
// a sharing LAN only the owner's and the LAN's public devices (M3). The
// loader, the maps and the views all ask, so a slip in one place never
// shows a member's device in a sharing LAN.
func (l *lan) counts(d *device) bool {
	return l.Mode != proto.LANModeShare || d.Owner == l.Owner || d.PublicLAN == l.ID
}

// joined reports whether p is a member of l who has joined. Only joined
// members see the LAN and have devices in it.
func (l *lan) joined(p proto.PersonID) bool {
	m := l.memberOf[p]
	return m != nil && m.State == proto.MemberJoined
}

type member struct {
	Person   proto.PersonID
	Role     proto.Role
	State    proto.MemberState
	JoinedAt time.Time
}

type lanDevice struct {
	Device   proto.DeviceID
	JoinedAt time.Time
}

type device struct {
	ID         proto.DeviceID
	Owner      proto.PersonID
	MachineKey proto.MachinePublic
	NodeKey    proto.NodePublic
	DiscoKey   proto.DiscoPublic
	Name       string // DNS label, unique per owner
	NamePinned bool   // set by hand (device.setDNSName): renames leave it
	// PublicLAN is the LAN a public device belongs to (M3): its owner,
	// not the person who logged it in, manages it. Empty for a person's.
	PublicLAN   proto.LANID
	DisplayName string
	Kind        proto.DeviceKind
	OS          proto.OS
	Hostname    string
	IPv4        netip.Addr
	IPv6        netip.Addr
	Endpoints   []netip.AddrPort
	HomeRelay   proto.RelayID
	// Relays are the relays the engine says it is connected to, HomeRelay
	// first (M4).
	Relays        []proto.RelayID
	Paused        map[proto.ScopeID]bool
	EngineVersion string
	// Caps are what its engine claimed at its last login (checkCaps'd),
	// nil for none.
	Caps      []string
	LoggedOut bool
	CreatedAt time.Time
	LastSeen  time.Time
	// ExpiresAt is when its login runs out. Each token the device gets
	// moves it forward, so only a device away for the whole lifetime
	// expires. An expired device is in no network map; logging in again,
	// or its owner renewing it, brings it back as it was.
	ExpiresAt time.Time
	expired   bool // ExpiresAt had passed when the world was loaded

	lans     map[proto.LANID]bool // the LANs it is in
	waiting  map[proto.LANID]bool // the LANs it waits to be let into
	services []proto.Service      // the catalog, in the order they were set up
	opens    map[proto.ScopeID]map[proto.ServiceKey]bool
	refused  map[proto.PersonID]bool
}

func (d *device) addrs() []netip.Prefix {
	return []netip.Prefix{netip.PrefixFrom(d.IPv4, 32), netip.PrefixFrom(d.IPv6, 128)}
}

type invite struct {
	ID        proto.InviteID
	LAN       proto.LANID
	Inviter   proto.PersonID
	Person    proto.PersonID // whom it is for; empty for a link anyone may use
	Token     string
	State     proto.InviteState
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedBy    proto.PersonID
	UsedAt    time.Time
}

// state is the invite's state at now: a pending invite past its time is
// expired, though the row still says pending.
func (i *invite) state(now time.Time) proto.InviteState {
	if i.State == proto.InvitePending && !now.Before(i.ExpiresAt) {
		return proto.InviteExpired
	}
	return i.State
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// A device's row in lan_devices is in one of two states.
const (
	deviceJoined  = "joined"
	devicePending = "pending" // waits for the LAN's owner (M2)
)

// usable reports whether the device can be in a network map: someone is
// logged in on it and the login has not run out.
func (d *device) usable() bool { return !d.LoggedOut && !d.expired }

// loadWorld reads the whole database. now decides which logins have run
// out, so the world and its version agree on it.
func loadWorld(q querier, network proto.Network, publicURL string, now time.Time) (*World, error) {
	ctx := context.Background()
	w := &World{
		Network:   network,
		publicURL: publicURL,
		people:    map[proto.PersonID]*person{},
		byEmail:   map[string]*person{},
		lans:      map[proto.LANID]*lan{},
		devices:   map[proto.DeviceID]*device{},
		byMachine: map[proto.MachinePublic]*device{},
		relays:    map[proto.RelayID]*proto.Relay{},
		relayPos:  map[proto.RelayID]int{},
		relayMeta: map[proto.RelayID]*relayMeta{},
		joins:     map[string]*relayJoin{},
		kicks:     map[string]*relayKick{},

		kickedPerson: map[proto.RelayID]map[proto.PersonID]bool{},
		kickedDevice: map[proto.RelayID]map[proto.DeviceID]bool{},
		bySecret:     map[string]proto.RelayID{},
		byNodeKey:    map[proto.NodePublic]*device{},
		invites:      map[proto.InviteID]*invite{},
		byToken:      map[string]*invite{},
		shares:       map[string]*share{},
	}
	each := func(query string, scan func(*sql.Rows) error) error {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return fmt.Errorf("%s: %w", query, err)
			}
		}
		return rows.Err()
	}

	err := each(`SELECT id, email, name, display_name, initial, created_at, seat_limit, operator FROM people`, func(r *sql.Rows) error {
		var p person
		var created int64
		if err := r.Scan(&p.ID, &p.Email, &p.Name, &p.DisplayName, &p.Initial, &created, &p.SeatLimit, &p.Operator); err != nil {
			return err
		}
		p.CreatedAt = fromMS(created)
		w.people[p.ID] = &p
		w.byEmail[p.Email] = &p
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT id, name, owner, created_at, approve_devices, approve_members, mode FROM lans`, func(r *sql.Rows) error {
		l := &lan{
			memberOf:   map[proto.PersonID]*member{},
			hasDevice:  map[proto.DeviceID]*lanDevice{},
			hasPending: map[proto.DeviceID]*lanDevice{},
		}
		var created int64
		if err := r.Scan(&l.ID, &l.Name, &l.Owner, &created, &l.ApproveDevices, &l.ApproveMembers, &l.Mode); err != nil {
			return err
		}
		l.CreatedAt = fromMS(created)
		w.lans[l.ID] = l
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT lan, person, role, state, joined_at FROM members ORDER BY joined_at, rowid`, func(r *sql.Rows) error {
		var m member
		var lanID proto.LANID
		var joined int64
		if err := r.Scan(&lanID, &m.Person, &m.Role, &m.State, &joined); err != nil {
			return err
		}
		m.JoinedAt = fromMS(joined)
		l := w.lans[lanID]
		if l == nil || w.people[m.Person] == nil {
			return nil
		}
		l.members = append(l.members, &m)
		l.memberOf[m.Person] = &m
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT id, owner, machine_key, node_key, disco_key, name, display_name, kind, os, hostname,
			ipv4, ipv6, endpoints, home_relay, relays, paused, engine_version, caps, logged_out, created_at, last_seen, expires_at, name_pinned, public_lan FROM devices`,
		func(r *sql.Rows) error {
			d := &device{
				Paused:  map[proto.ScopeID]bool{},
				lans:    map[proto.LANID]bool{},
				waiting: map[proto.LANID]bool{},
				opens:   map[proto.ScopeID]map[proto.ServiceKey]bool{},
				refused: map[proto.PersonID]bool{},
			}
			var mk, nk, dk, v4, v6, eps, relays, paused, caps string
			var created, seen, expires int64
			if err := r.Scan(&d.ID, &d.Owner, &mk, &nk, &dk, &d.Name, &d.DisplayName, &d.Kind, &d.OS, &d.Hostname,
				&v4, &v6, &eps, &d.HomeRelay, &relays, &paused, &d.EngineVersion, &caps, &d.LoggedOut, &created, &seen, &expires, &d.NamePinned, &d.PublicLAN); err != nil {
				return err
			}
			d.ExpiresAt = fromMS(expires)
			d.expired = !d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt)
			var err error
			if d.MachineKey, err = proto.ParseMachinePublic(mk); err != nil {
				return err
			}
			if d.NodeKey, err = proto.ParseNodePublic(nk); err != nil {
				return err
			}
			if d.DiscoKey, err = proto.ParseDiscoPublic(dk); err != nil {
				return err
			}
			if d.IPv4, err = netip.ParseAddr(v4); err != nil {
				return err
			}
			if d.IPv6, err = netip.ParseAddr(v6); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(eps), &d.Endpoints); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(relays), &d.Relays); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(caps), &d.Caps); err != nil {
				return err
			}
			if len(d.Caps) == 0 {
				d.Caps = nil // "[]": one spelling for none
			}
			var ps []proto.ScopeID
			if err := json.Unmarshal([]byte(paused), &ps); err != nil {
				return err
			}
			for _, s := range ps {
				d.Paused[s] = true
			}
			d.CreatedAt, d.LastSeen = fromMS(created), fromMS(seen)
			if w.people[d.Owner] == nil {
				return nil
			}
			w.devices[d.ID] = d
			w.byMachine[d.MachineKey] = d
			if d.usable() {
				w.byNodeKey[d.NodeKey] = d
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT lan, device, joined_at, state FROM lan_devices ORDER BY joined_at, rowid`, func(r *sql.Rows) error {
		var ld lanDevice
		var lanID proto.LANID
		var joined int64
		var state string
		if err := r.Scan(&lanID, &ld.Device, &joined, &state); err != nil {
			return err
		}
		ld.JoinedAt = fromMS(joined)
		l, d := w.lans[lanID], w.devices[ld.Device]
		if l == nil || d == nil || l.memberOf[d.Owner] == nil || !l.counts(d) {
			return nil
		}
		// A device is in a LAN only while its owner is a joined member.
		// The ops keep it so; checking here too means a slip there never
		// shows a device to a LAN its owner left. A device of a member who
		// is still waiting waits too, whatever its row says.
		if state == deviceJoined && l.joined(d.Owner) {
			l.devices = append(l.devices, &ld)
			l.hasDevice[d.ID] = &ld
			d.lans[l.ID] = true
			return nil
		}
		if state == devicePending || state == deviceJoined {
			l.pending = append(l.pending, &ld)
			l.hasPending[d.ID] = &ld
			d.waiting[l.ID] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT device, key, kind, name, ports FROM services ORDER BY rowid`, func(r *sql.Rows) error {
		var dev proto.DeviceID
		var key, ports string
		var s proto.Service
		if err := r.Scan(&dev, &key, &s.Kind, &s.Name, &ports); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(ports), &s.Ports); err != nil {
			return err
		}
		if d := w.devices[dev]; d != nil {
			d.services = append(d.services, s)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT device, scope, key FROM opens`, func(r *sql.Rows) error {
		var dev proto.DeviceID
		var scope proto.ScopeID
		var key proto.ServiceKey
		if err := r.Scan(&dev, &scope, &key); err != nil {
			return err
		}
		if d := w.devices[dev]; d != nil {
			if d.opens[scope] == nil {
				d.opens[scope] = map[proto.ServiceKey]bool{}
			}
			d.opens[scope][key] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT device, person FROM refusals`, func(r *sql.Rows) error {
		var dev proto.DeviceID
		var p proto.PersonID
		if err := r.Scan(&dev, &p); err != nil {
			return err
		}
		if d := w.devices[dev]; d != nil {
			d.refused[p] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT id, lan, inviter, person, token, state, created_at, expires_at, used_by, used_at FROM invites ORDER BY created_at, rowid`, func(r *sql.Rows) error {
		var i invite
		var created, expires, used int64
		if err := r.Scan(&i.ID, &i.LAN, &i.Inviter, &i.Person, &i.Token, &i.State, &created, &expires, &i.UsedBy, &used); err != nil {
			return err
		}
		i.CreatedAt, i.ExpiresAt, i.UsedAt = fromMS(created), fromMS(expires), fromMS(used)
		if w.lans[i.LAN] == nil {
			return nil
		}
		w.invites[i.ID] = &i
		w.byToken[i.Token] = &i
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = each(`SELECT id, device, receiver, sharer, state, created_at, revoked_at, hold_until FROM shares`, func(r *sql.Rows) error {
		var sh share
		var created, revoked, hold int64
		if err := r.Scan(&sh.ID, &sh.Device, &sh.Receiver, &sh.Sharer, &sh.State, &created, &revoked, &hold); err != nil {
			return err
		}
		sh.CreatedAt, sh.RevokedAt, sh.HoldUntil = fromMS(created), fromMS(revoked), fromMS(hold)
		if w.devices[sh.Device] == nil || w.people[sh.Receiver] == nil {
			return nil
		}
		w.shares[sh.ID] = &sh
		return nil
	})
	if err != nil {
		return nil, err
	}

	type pos struct {
		id  proto.RelayID
		pos int
	}
	var defaults []pos
	err = each(`SELECT id, name, source, owner, host_name, ipv4, ipv6, derp_port, stun_port, default_pos,
			pin, rate_limit, secret_hash, created_at, registered_at, weight, capacity FROM relays`, func(r *sql.Rows) error {
		var rl proto.Relay
		var m relayMeta
		var v4, v6 string
		var dpos int
		var created, registered int64
		if err := r.Scan(&rl.ID, &rl.Name, &rl.Source, &rl.Owner, &rl.HostName, &v4, &v6, &rl.DERPPort, &rl.STUNPort, &dpos,
			&rl.Pin, &rl.RateLimit, &m.SecretHash, &created, &registered, &m.Weight, &m.Capacity); err != nil {
			return err
		}
		m.CreatedAt, m.RegisteredAt = fromMS(created), fromMS(registered)
		w.relayMeta[rl.ID] = &m
		if m.SecretHash != "" {
			w.bySecret[m.SecretHash] = rl.ID
		}
		if v4 != "" {
			a, err := netip.ParseAddr(v4)
			if err != nil {
				return err
			}
			rl.IPv4 = a
		}
		if v6 != "" {
			a, err := netip.ParseAddr(v6)
			if err != nil {
				return err
			}
			rl.IPv6 = a
		}
		w.relays[rl.ID] = &rl
		w.relayPos[rl.ID] = dpos
		if dpos >= 0 {
			defaults = append(defaults, pos{rl.ID, dpos})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(defaults, func(a, b pos) int {
		if a.pos != b.pos {
			return a.pos - b.pos
		}
		return cmp.Compare(a.id, b.id)
	})
	for _, d := range defaults {
		w.defaults = append(w.defaults, d.id)
	}

	err = each(`SELECT lan, relay FROM lan_relays ORDER BY lan, position, relay`, func(r *sql.Rows) error {
		var l proto.LANID
		var rl proto.RelayID
		if err := r.Scan(&l, &rl); err != nil {
			return err
		}
		if w.relays[rl] != nil && w.lans[l] != nil {
			w.lans[l].added = append(w.lans[l].added, rl)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(`SELECT id, owner, relay, source, name, rate_limit, token_hash, created_at, expires_at FROM relay_joins`, func(r *sql.Rows) error {
		var j relayJoin
		var relay sql.NullString
		var created, expires int64
		if err := r.Scan(&j.ID, &j.Owner, &relay, &j.Source, &j.Name, &j.RateLimit, &j.TokenHash, &created, &expires); err != nil {
			return err
		}
		j.Relay, j.CreatedAt, j.ExpiresAt = proto.RelayID(relay.String), fromMS(created), fromMS(expires)
		w.joins[j.ID] = &j
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(`SELECT id, relay, person, device, by_person, at, lifted_at, lifted_by FROM relay_kicks`, func(r *sql.Rows) error {
		var k relayKick
		var at, lifted int64
		if err := r.Scan(&k.ID, &k.Relay, &k.Person, &k.Device, &k.By, &at, &lifted, &k.LiftedBy); err != nil {
			return err
		}
		k.At, k.LiftedAt = fromMS(at), fromMS(lifted)
		w.kicks[k.ID] = &k
		w.indexKick(&k)
		return nil
	})
	if err != nil {
		return nil, err
	}
	w.finishRelays()
	return w, nil
}
