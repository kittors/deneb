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
	defaults  []proto.RelayID       // the relays a LAN uses unless it has its own list
	invites   map[proto.InviteID]*invite
	byToken   map[string]*invite
}

type person struct {
	proto.Person
	CreatedAt time.Time
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
	members        []*member // in the order they joined, every state
	memberOf       map[proto.PersonID]*member
	devices        []*lanDevice // in, in the order they joined
	hasDevice      map[proto.DeviceID]*lanDevice
	// pending are the devices waiting for the owner. They are kept apart
	// from devices so nothing that computes a network map can count them.
	pending    []*lanDevice
	hasPending map[proto.DeviceID]*lanDevice
	relays     []proto.RelayID
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
	ID            proto.DeviceID
	Owner         proto.PersonID
	MachineKey    proto.MachinePublic
	NodeKey       proto.NodePublic
	DiscoKey      proto.DiscoPublic
	Name          string // DNS label, unique per owner
	NamePinned    bool   // set by hand (device.setDNSName): renames leave it
	DisplayName   string
	Kind          proto.DeviceKind
	OS            proto.OS
	Hostname      string
	IPv4          netip.Addr
	IPv6          netip.Addr
	Endpoints     []netip.AddrPort
	HomeRelay     proto.RelayID
	Paused        map[proto.ScopeID]bool
	EngineVersion string
	LoggedOut     bool
	CreatedAt     time.Time
	LastSeen      time.Time
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
		invites:   map[proto.InviteID]*invite{},
		byToken:   map[string]*invite{},
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

	err := each(`SELECT id, email, name, display_name, initial, created_at FROM people`, func(r *sql.Rows) error {
		var p person
		var created int64
		if err := r.Scan(&p.ID, &p.Email, &p.Name, &p.DisplayName, &p.Initial, &created); err != nil {
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

	err = each(`SELECT id, name, owner, created_at, approve_devices, approve_members FROM lans`, func(r *sql.Rows) error {
		l := &lan{
			memberOf:   map[proto.PersonID]*member{},
			hasDevice:  map[proto.DeviceID]*lanDevice{},
			hasPending: map[proto.DeviceID]*lanDevice{},
		}
		var created int64
		if err := r.Scan(&l.ID, &l.Name, &l.Owner, &created, &l.ApproveDevices, &l.ApproveMembers); err != nil {
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
			ipv4, ipv6, endpoints, home_relay, paused, engine_version, logged_out, created_at, last_seen, expires_at, name_pinned FROM devices`,
		func(r *sql.Rows) error {
			d := &device{
				Paused:  map[proto.ScopeID]bool{},
				lans:    map[proto.LANID]bool{},
				waiting: map[proto.LANID]bool{},
				opens:   map[proto.ScopeID]map[proto.ServiceKey]bool{},
				refused: map[proto.PersonID]bool{},
			}
			var mk, nk, dk, v4, v6, eps, paused string
			var created, seen, expires int64
			if err := r.Scan(&d.ID, &d.Owner, &mk, &nk, &dk, &d.Name, &d.DisplayName, &d.Kind, &d.OS, &d.Hostname,
				&v4, &v6, &eps, &d.HomeRelay, &paused, &d.EngineVersion, &d.LoggedOut, &created, &seen, &expires, &d.NamePinned); err != nil {
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
		if l == nil || d == nil || l.memberOf[d.Owner] == nil {
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

	type pos struct {
		id  proto.RelayID
		pos int
	}
	var defaults []pos
	err = each(`SELECT id, name, source, owner, host_name, ipv4, ipv6, derp_port, stun_port, default_pos FROM relays`, func(r *sql.Rows) error {
		var rl proto.Relay
		var v4, v6 string
		var dpos int
		if err := r.Scan(&rl.ID, &rl.Name, &rl.Source, &rl.Owner, &rl.HostName, &v4, &v6, &rl.DERPPort, &rl.STUNPort, &dpos); err != nil {
			return err
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

	own := map[proto.LANID][]proto.RelayID{}
	err = each(`SELECT lan, relay FROM lan_relays ORDER BY lan, position`, func(r *sql.Rows) error {
		var l proto.LANID
		var rl proto.RelayID
		if err := r.Scan(&l, &rl); err != nil {
			return err
		}
		if w.relays[rl] != nil {
			own[l] = append(own[l], rl)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id, l := range w.lans {
		if rs, ok := own[id]; ok {
			l.relays = rs
		} else {
			l.relays = slices.Clone(w.defaults)
		}
	}
	return w, nil
}
