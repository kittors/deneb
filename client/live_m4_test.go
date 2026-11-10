package client

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// m4View is a user view of an M4 server as the test deployment has it:
// the public relay; a relay the seed file lists for every LAN, which is
// mine; my own server, dragged into 工作室, and another of mine in no LAN,
// down, named by nothing but its address; and 阿杰's server, which he
// dragged into 周末联机.
func m4View() *proto.UserView {
	t0 := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	return &proto.UserView{Version: 9, Me: "me", Features: []string{proto.FeatureRelays},
		People: []proto.Person{{ID: "me", Name: "kittors"}, {ID: "jie", Name: "jie", DisplayName: "阿杰"}},
		LANs: []proto.LANView{
			{ID: "work", Name: "工作室", Owner: "me", Relays: []proto.RelayID{"srv", "pub", "seed"}, AddedRelays: []proto.RelayID{"srv"}},
			{ID: "weekend", Name: "周末联机", Owner: "jie", Relays: []proto.RelayID{"jiesrv", "pub", "seed", "jieseed"}, AddedRelays: []proto.RelayID{"jiesrv"}},
		},
		Relays: []proto.Relay{
			{ID: "pub", Name: "香港", Source: proto.RelayPublic, IPv4: netip.MustParseAddr("203.0.113.10"), RateLimit: 2_000_000},
			{ID: "seed", Name: "香港", Source: proto.RelayServer, Owner: "me", HostName: "derp.example.com"},
			{ID: "srv", Name: "公司机房", Source: proto.RelayServer, Owner: "me", IPv4: netip.MustParseAddr("198.51.100.4")},
			{ID: "spare", Name: "198.51.100.9", Source: proto.RelayServer, Owner: "me", IPv4: netip.MustParseAddr("198.51.100.9")},
			{ID: "jiesrv", Name: "东京", Source: proto.RelayServer, Owner: "jie", IPv4: netip.MustParseAddr("203.0.113.20")},
			// Another deployment relay, someone else's: listed, dragged in by no one.
			{ID: "jieseed", Name: "上海", Source: proto.RelayServer, Owner: "jie", IPv4: netip.MustParseAddr("203.0.113.30")},
		},
		MyRelays: []proto.RelayView{
			{ID: "seed", Registered: true, Seeded: true, Default: true},
			{ID: "spare", Registered: true, Online: false, LastSeen: t0.Add(-3 * time.Minute), Since: t0.Add(-48 * time.Hour)},
			{ID: "srv", LANs: []proto.LANID{"work"}, Registered: true, Online: true, Since: t0.Add(-12 * 24 * time.Hour)},
		},
		RelayJoins: []proto.RelayJoinView{{ID: "j1", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}},
	}
}

// m4Usage is the relays' usage the server says for m4View.
func m4Usage() *proto.RelayUsage {
	t0 := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	return &proto.RelayUsage{Month: "2026-11", Relays: []proto.RelayMonth{
		{Relay: "srv", Bytes: 15_500_000_000,
			LANs:      []proto.UsageLine{{LAN: "work", Bytes: 12_400_000_000}, {LAN: "weekend", Bytes: 0}},
			Shares:    []proto.UsageLine{{Share: "s1", Bytes: 2_000_000}, {Share: "s2", Bytes: 1_000_000}},
			Load:      &proto.RelayLoad{People: 3, Devices: 5, Uptime: 12 * 24 * 3600, CPU: 0.2},
			Connected: []proto.RelayClient{{Person: "me", Device: "mac"}, {Person: "jie"}, {Person: "jie"}, {Person: "lin"}},
			Kicks: []proto.RelayKick{
				{ID: "k1", Person: "leo", At: t0.Add(-72 * time.Hour)},
				{ID: "k0", Person: "leo", At: t0.Add(-240 * time.Hour), LiftedAt: t0.Add(-200 * time.Hour)},
				{ID: "k2", Device: "jpi", At: t0},
			}},
		{Relay: "spare", Load: &proto.RelayLoad{Full: true}},
	}}
}

// liveOf is a store that took in st and the view, as the engine said them.
func liveOf(v *proto.UserView, usage *proto.RelayUsage) *Live {
	l := NewLive(nil)
	l.state = &apitype.State{Version: v.Version, LoggedIn: true, User: apitype.PersonID(v.Me)}
	l.view, l.usage = v, usage
	return l
}

// TestLiveM4Relays: the relays of an M4 server in the live store (DESIGN
// §6.12): whose each is, where it is reached, the public one's limit; the
// seed file's, in every LAN, as public as ours; a name that is only the
// address is no name; mine up or down, since when; the LANs' relays that
// someone dragged in, mine only where my relay says it is; and from the
// usage, how busy mine is, what it carried for each LAN and for my shares,
// who else is on it, and whom I keep off it.
func TestLiveM4Relays(t *testing.T) {
	l := liveOf(m4View(), m4Usage())
	s := l.buildLocked(time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC))
	if !s.Can(model.FeatureRelays) {
		t.Fatal("relays not offered")
	}
	pub, seed, srv, spare, jie := s.Relays["pub"], s.Relays["seed"], s.Relays["srv"], s.Relays["spare"], s.Relays["jiesrv"]
	if pub == nil || !pub.Public() || pub.RateLimit != 2_000_000 || pub.Addr != "203.0.113.10" {
		t.Errorf("the public relay: %+v", pub)
	}
	if seed == nil || !seed.Public() || seed.Owner != "" || seed.Addr != "derp.example.com" {
		t.Errorf("the seed file's relay: %+v", seed)
	}
	if srv == nil || !s.MineRelay(srv) || srv.Name != "公司机房" || srv.Down || srv.Since.IsZero() {
		t.Errorf("my server: %+v", srv)
	}
	if spare == nil || spare.Name != "" || spare.Label() != "198.51.100.9" || !spare.Down || spare.LastSeen.IsZero() || spare.Load.Busy != model.BusyFull {
		t.Errorf("my server known by its address, down: %+v", spare)
	}
	if jie == nil || jie.Source != model.RelayServer || jie.Owner != "jie" || s.MineRelay(jie) {
		t.Errorf("阿杰's server: %+v", jie)
	}
	if js := s.Relays["jieseed"]; js == nil || !js.Public() || js.Owner != "" {
		t.Errorf("a deployment relay of someone else's: %+v", js)
	}
	work, weekend := s.LAN("work"), s.LAN("weekend")
	if !slices.Equal(work.AddedRelays, []model.RelayID{"srv"}) || !slices.Equal(weekend.AddedRelays, []model.RelayID{"jiesrv"}) {
		t.Errorf("dragged in: 工作室 %v, 周末联机 %v", work.AddedRelays, weekend.AddedRelays)
	}
	if srv.Load.People != 3 || srv.Load.Devices != 5 || srv.Load.Uptime != 12*24*time.Hour || srv.Load.Busy != model.BusyNo {
		t.Errorf("my server's load: %+v", srv.Load)
	}
	want := []model.RelayMonth{{LAN: "work", Bytes: 12_400_000_000}, {Shares: true, Bytes: 3_000_000}}
	if !slices.Equal(srv.Month, want) {
		t.Errorf("my server's month: %+v", srv.Month)
	}
	if !slices.Equal(srv.Using, []model.PersonID{"jie", "lin"}) {
		t.Errorf("on my server besides me: %v", srv.Using)
	}
	if len(srv.Kicks) != 2 || srv.Kick("leo") == nil || srv.Kicks[1].Lifted.IsZero() {
		t.Errorf("kept off my server: %+v", srv.Kicks)
	}
	if len(s.RelayJoins) != 1 || s.RelayJoins[0].ID != "j1" || s.RelayJoins[0].Command != "" {
		t.Errorf("waiting to be installed: %+v", s.RelayJoins)
	}
}

// TestLiveM4OldServer: a server before M4 says its relays' names only, and
// the store keeps no more of them, as M1–M3 showed them.
func TestLiveM4OldServer(t *testing.T) {
	v := m4View()
	v.Features = nil
	s := liveOf(v, m4Usage()).buildLocked(time.Now())
	if r := s.Relays["srv"]; r == nil || r.Name != "公司机房" || r.Source != "" || r.Owner != "" {
		t.Errorf("an M3 server's relay: %+v", r)
	}
	if l := s.LAN("work"); len(l.AddedRelays) != 0 {
		t.Errorf("dragged in, on an M3 server: %v", l.AddedRelays)
	}
}

// TestLiveM4OpArgs: an op on a relay names it; lifting a kick names the kick
// in force, which the usage lists, or fails; the install command of
// relay.join is kept for the join's card, which the view lists without it.
func TestLiveM4OpArgs(t *testing.T) {
	l := liveOf(m4View(), m4Usage())
	var a proto.OpArgs
	if err := l.relayArgs(model.Op{Kind: model.OpRelayAdd, LAN: "weekend", Relay: "srv"}, &a); err != nil || a.Relay != "srv" {
		t.Errorf("relay.add: %+v, %v", a, err)
	}
	a = proto.OpArgs{Person: "leo"}
	if err := l.relayArgs(model.Op{Kind: model.OpRelayUnkick, Relay: "srv", Person: "leo"}, &a); err != nil || a.Kick != "k1" || a.Person != "" {
		t.Errorf("relay.unkick: %+v, %v", a, err)
	}
	a = proto.OpArgs{}
	if err := l.relayArgs(model.Op{Kind: model.OpRelayUnkick, Relay: "srv", Person: "jie"}, &a); err == nil {
		t.Errorf("relay.unkick of someone not kept off: %+v", a)
	}
	a = proto.OpArgs{}
	if err := l.relayArgs(model.Op{Kind: model.OpRelayRevokeJoin, Join: "j1"}, &a); err != nil || a.Join != "j1" {
		t.Errorf("relay.revokeJoin: %+v, %v", a, err)
	}
	var r model.Result
	l.relaysDone(model.Op{Kind: model.OpRelayJoin}, proto.OpOutput{RelayJoin: &proto.RelayJoin{ID: "j1", Command: "docker run … --join deneb-join-x"}}, &r)
	if r.Join != "j1" || r.Command == "" {
		t.Errorf("relay.join's result: %+v", r)
	}
	s := l.buildLocked(time.Now())
	if len(s.RelayJoins) != 1 || s.RelayJoins[0].Command != r.Command {
		t.Errorf("the join's command after relay.join: %+v", s.RelayJoins)
	}
}
