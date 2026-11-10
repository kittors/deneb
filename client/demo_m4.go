package client

import (
	"slices"
	"strconv"
	"time"

	"github.com/kittors/deneb/client/model"
)

// M4 in the demo (DESIGN §6.12): Simulate("m4") brings the relays on the
// sample, as an M4 server would have them. 香港, the public one, carries
// 阿杰's NAS; 阿杰's server in 东京, which he dragged into 周末联机, carries
// Mia's MacBook in a hotel; my own server, 198.51.100.4, waits in the tray
// of 周末联机 and helps 工作室. Devices at home as relays come after the
// public beta (the user, 11-09), so the demo has none.

const (
	demoPublic = model.RelayID("hk")     // 香港 · 公共
	demoServer = model.RelayID("srv")    // 198.51.100.4 · 我的服务器
	demoJieSrv = model.RelayID("jiesrv") // 东京 · 阿杰的服务器
)

// demoCommand is the install command relay.join answers with: the server
// makes it (control.md §13); the demo's carries a code of its own.
func demoCommand(n int) string {
	return "docker run -d --name deneb-relay --restart unless-stopped --network host -v deneb-relay:/data deneb-relay:v0 --join deneb-join-q7Lm2xVt9K" + strconv.Itoa(n) + " --region auto"
}

// demoM4 is what the demo keeps for M4's ops beyond the snapshot: how many
// install commands it gave, and where each device went before my server
// stopped, or before its owner was kept off it, to go back to.
type demoM4 struct {
	joins int
	was   map[model.DeviceID]model.RelayID
}

// demoGB is n tenths of a GB.
func demoGB(n int64) int64 { return n * 100_000_000 }

// m4Base puts M4's relays on s.
func m4Base(s *model.Snapshot, now time.Time) *model.Snapshot {
	n := *s
	n.Features = maps(s.Features)
	n.Features[model.FeatureRelays] = true
	n.Relays = map[model.RelayID]*model.Relay{
		demoPublic: {ID: demoPublic, Name: "香港", Source: model.RelayPublic, Addr: "103.42.31.155",
			RateLimit: 2_000_000, RTT: 52 * time.Millisecond},
		demoServer: {ID: demoServer, Source: model.RelayServer, Owner: s.Me, Addr: "198.51.100.4",
			RTT: 38 * time.Millisecond, Since: now.Add(-12 * 24 * time.Hour),
			Load:  model.RelayLoad{People: 1, Devices: 2, Uptime: 12 * 24 * time.Hour},
			Month: []model.RelayMonth{{LAN: "weekend", Bytes: demoGB(124)}, {LAN: "work", Bytes: demoGB(31)}},
			Using: []model.PersonID{"lin"},
			Kicks: []model.RelayKick{{Person: "leo", At: now.Add(-3 * 24 * time.Hour)}}},
		demoJieSrv: {ID: demoJieSrv, Name: "东京", Source: model.RelayServer, Owner: "jie", RTT: 31 * time.Millisecond},
	}
	n.LANs = slices.Clone(s.LANs)
	for i, l := range n.LANs {
		nl := *l
		switch l.ID {
		case "weekend":
			nl.AddedRelays = []model.RelayID{demoJieSrv}
			nl.Relays = []model.RelayID{demoJieSrv, demoPublic}
		case "work":
			nl.AddedRelays = []model.RelayID{demoServer}
			nl.Relays = []model.RelayID{demoServer, demoPublic}
		default:
			nl.Relays = []model.RelayID{demoPublic}
		}
		n.LANs[i] = &nl
	}
	n.Paths = maps(s.Paths)
	n.Paths["jnas"] = model.Path{Kind: model.PathRelay, Relay: demoPublic, RTT: 52 * time.Millisecond, Since: now}
	n.Paths["mia"] = model.Path{Kind: model.PathRelay, Relay: demoJieSrv, RTT: 31 * time.Millisecond, Since: now}
	return &n
}

// m4Op decides one of M4's ops, as the M4 server and the engines would:
// a relay dragged in takes over what went through the public one; one
// dragged out, or kept from someone, hands it back.
func (d *Demo) m4Op(s *model.Snapshot, op model.Op, res *model.Result) *model.Snapshot {
	now := time.Now()
	switch op.Kind {
	case model.OpRelayJoin:
		d.m4.joins++
		id := "j" + strconv.Itoa(d.m4.joins)
		res.Command, res.Join = demoCommand(d.m4.joins), id
		n := *s
		n.RelayJoins = append(slices.Clone(s.RelayJoins), model.RelayJoin{ID: id, Command: res.Command, Expires: now.Add(time.Hour)})
		return &n
	case model.OpRelayAdd:
		n := s.With(op)
		if n == s {
			return s
		}
		return reroute(n, op.LAN, func(p model.Path) bool { return n.Relays[p.Relay].Public() }, op.Relay, now)
	case model.OpRelayRemove:
		n := s.With(op)
		if n == s {
			return s
		}
		return reroute(n, op.LAN, func(p model.Path) bool { return p.Relay == op.Relay }, demoPublic, now)
	case model.OpRelayKick, model.OpRelayUnkick:
		n := s.With(op)
		if n == s {
			return s
		}
		theirs := func(id model.DeviceID) bool { dev := n.Devices[id]; return dev != nil && dev.Owner == op.Person }
		r := *n.Relays[op.Relay]
		n.Relays = maps(n.Relays)
		n.Relays[op.Relay] = &r
		n.Paths = maps(n.Paths)
		var count int
		for id, p := range s.Paths {
			if !theirs(id) || p.Kind != model.PathRelay {
				continue
			}
			switch {
			case op.Kind == model.OpRelayKick && p.Relay == op.Relay:
				// Kept off it: on to the next in the list, the public one.
				n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: demoPublic, RTT: 52 * time.Millisecond, Since: now}
				count++
			case op.Kind == model.OpRelayUnkick && p.Relay == demoPublic && d.m4.was[id] == op.Relay:
				n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: op.Relay, RTT: 38 * time.Millisecond, Since: now}
				count++
			}
			if op.Kind == model.OpRelayKick && p.Relay == op.Relay {
				if d.m4.was == nil {
					d.m4.was = map[model.DeviceID]model.RelayID{}
				}
				d.m4.was[id] = op.Relay
			}
		}
		if op.Kind == model.OpRelayUnkick && count > 0 && !slices.Contains(r.Using, op.Person) {
			r.Using = append(slices.Clone(r.Using), op.Person)
		}
		r.Load = m4Load(n, op.Relay)
		return n
	}
	return s.With(op)
}

// m4Load counts who is on the relay now: the people in Using, and their
// devices that go through it, as the demo's world has them (each one's
// other devices on it too, as far as this computer cannot see).
func m4Load(s *model.Snapshot, id model.RelayID) model.RelayLoad {
	r := s.Relays[id]
	load := r.Load
	load.People = len(r.Using)
	load.Devices = 0
	for _, p := range r.Using {
		// Each one's devices on it: two each, but 小林's one.
		load.Devices += 2
		if p == "lin" {
			load.Devices--
		}
	}
	return load
}

// reroute moves the paths of the LAN's devices that went(p) onto the relay
// to: what a relay dragged in takes over, or what one dragged out hands to
// the public one.
func reroute(s *model.Snapshot, lan model.LANID, went func(model.Path) bool, to model.RelayID, now time.Time) *model.Snapshot {
	l := s.LAN(lan)
	if l == nil {
		return s
	}
	n := *s
	n.Paths = maps(s.Paths)
	rtt := map[model.RelayID]time.Duration{demoPublic: 52 * time.Millisecond, demoServer: 38 * time.Millisecond, demoJieSrv: 31 * time.Millisecond}[to]
	for _, id := range l.Devices {
		if p := s.Paths[id]; p.Kind == model.PathRelay && went(p) {
			n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: to, RTT: rtt, Since: now}
		}
	}
	return &n
}

// m4Event plays one of M4's outside events on s, or returns s:
//   - "m4": the relays, as an M4 server would have them.
//   - "m4-in": my server in 周末联机 as well, carrying 阿杰's NAS, and
//     阿杰 and Mia on it.
//   - "m4-fresh": no server of my own yet: 阿杰's NAS goes through the
//     public relay.
//   - "relay-down": my server stops, or comes back (⌥⌘R): what went
//     through it goes through the public one meanwhile.
//   - "relay-joined": the oldest install command waiting is run (⌥⌘J): my
//     server comes up, new.
func (d *Demo) m4Event(s *model.Snapshot, event string, now time.Time) *model.Snapshot {
	switch event {
	case "m4":
		return m4Base(s, now)
	case "m4-in":
		srv := s.Relays[demoServer]
		if srv == nil {
			return s
		}
		n := s.With(model.Op{Kind: model.OpRelayAdd, LAN: "weekend", Relay: demoServer})
		n = reroute(n, "weekend", func(p model.Path) bool { return n.Relays[p.Relay].Public() }, demoServer, now)
		r := *srv
		r.Using = []model.PersonID{"mia", "jie", "lin"}
		n.Relays = maps(n.Relays)
		n.Relays[demoServer] = &r
		r.Load = m4Load(n, demoServer)
		return n
	case "m4-fresh":
		n := *s
		n.Relays = maps(s.Relays)
		delete(n.Relays, demoServer)
		n.LANs = slices.Clone(s.LANs)
		for i, l := range n.LANs {
			if l.Added(demoServer) {
				nl := *l
				gone := func(id model.RelayID) bool { return id == demoServer }
				nl.AddedRelays = slices.DeleteFunc(slices.Clone(l.AddedRelays), gone)
				nl.Relays = slices.DeleteFunc(slices.Clone(l.Relays), gone)
				n.LANs[i] = &nl
			}
		}
		return &n
	case "relay-down":
		srv := s.Relays[demoServer]
		if srv == nil {
			return s
		}
		r := *srv
		r.Down = !r.Down
		n := *s
		n.Relays = maps(s.Relays)
		n.Relays[demoServer] = &r
		n.Paths = maps(s.Paths)
		if r.Down {
			r.LastSeen = now
			if d.m4.was == nil {
				d.m4.was = map[model.DeviceID]model.RelayID{}
			}
			for id, p := range s.Paths {
				if p.Kind == model.PathRelay && p.Relay == demoServer {
					d.m4.was[id] = demoServer
					n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: demoPublic, RTT: 52 * time.Millisecond, Since: now}
				}
			}
			return &n
		}
		r.LastSeen = time.Time{}
		for id, p := range s.Paths {
			if p.Kind == model.PathRelay && p.Relay == demoPublic && d.m4.was[id] == demoServer {
				delete(d.m4.was, id)
				n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: demoServer, RTT: 38 * time.Millisecond, Since: now}
			}
		}
		return &n
	case "relay-joined":
		if len(s.RelayJoins) == 0 {
			return s
		}
		n := *s
		n.RelayJoins = slices.Clone(s.RelayJoins[1:])
		n.Relays = maps(s.Relays)
		id, addr := demoServer, "198.51.100.4"
		if s.Relays[demoServer] != nil {
			id, addr = model.RelayID("srv"+strconv.Itoa(len(s.Relays))), "47.242.18."+strconv.Itoa(len(s.Relays))
		}
		n.Relays[id] = &model.Relay{ID: id, Source: model.RelayServer, Owner: s.Me, Addr: addr,
			RTT: 38 * time.Millisecond, Since: now}
		return &n
	}
	return s
}
