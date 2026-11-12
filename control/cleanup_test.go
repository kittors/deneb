package control

import (
	"net/netip"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestCleanup is the daily cleanup (PRIVACY.md §7): invites used or run out,
// shares taken back or given up and kicks lifted go after 30 days, and not
// before; nothing still in force goes (a live invite, an active share, one
// taken back that still holds its seat, a kick in force); relay usage
// keeps this month and the two before; a device logged out or run out
// keeps its row but not the addresses it reported, as logging out does at
// once.
func TestCleanup(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	now := w.clk.now()
	day := 24 * time.Hour
	ago := func(d time.Duration) int64 { return ms(now.Add(-d)) }
	set := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Invites.
	invite := func() proto.InviteID {
		return w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio}).Result.Invite.ID
	}
	runOut, recentlyOut, live, used, recentlyUsed, revoked := invite(), invite(), invite(), invite(), invite(), invite()
	set(`UPDATE invites SET expires_at = ? WHERE id = ?`, ago(31*day), runOut)
	set(`UPDATE invites SET expires_at = ? WHERE id = ?`, ago(29*day), recentlyOut)
	set(`UPDATE invites SET state = 'accepted', used_at = ? WHERE id = ?`, ago(31*day), used)
	set(`UPDATE invites SET state = 'accepted', used_at = ? WHERE id = ?`, ago(29*day), recentlyUsed)
	set(`UPDATE invites SET state = 'revoked', expires_at = ? WHERE id = ?`, ago(31*day), revoked)

	// Shares: taken back long ago, with what it opened; taken back lately;
	// given up long ago; active; taken back long ago yet still holding its
	// seat (the undo is not over).
	svc := &proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{{Proto: "tcp", First: 22, Last: 22}}}
	oldShare := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}).Result.Share
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Share: oldShare, Service: svc, On: true})
	w.mac.must(proto.OpShareRevoke, proto.OpArgs{Share: oldShare})
	set(`UPDATE shares SET revoked_at = ?, hold_until = ? WHERE id = ?`, ago(31*day), ago(31*day), oldShare)
	newShare := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.mac.id, Person: w.jie}).Result.Share
	w.mac.must(proto.OpShareRevoke, proto.OpArgs{Share: newShare})
	set(`UPDATE shares SET revoked_at = ?, hold_until = ? WHERE id = ?`, ago(29*day), ago(29*day), newShare)
	leftShare := w.jdesk.must(proto.OpShareCreate, proto.OpArgs{Device: w.jnas.id, Person: w.me}).Result.Share
	w.mac.must(proto.OpShareLeave, proto.OpArgs{Share: leftShare})
	set(`UPDATE shares SET revoked_at = ? WHERE id = ?`, ago(31*day), leftShare)
	activeShare := w.jdesk.must(proto.OpShareCreate, proto.OpArgs{Device: w.jdesk.id, Person: w.me}).Result.Share
	mia := s.state.Load().w.byEmail["mia@deneb.test"].ID
	set(`INSERT INTO shares (id, device, receiver, sharer, state, created_at, revoked_at, hold_until) VALUES ('s_holding', ?, ?, ?, ?, ?, ?, ?)`,
		w.mac.id, mia, w.me, shareRevoked, ago(40*day), ago(31*day), ms(now.Add(5*time.Second)))
	if n := count(`SELECT COUNT(*) FROM opens WHERE scope = ?`, string(shareScope(string(oldShare)))); n != 1 {
		t.Fatalf("the old share opens %d", n)
	}

	// Kicks: lifted long ago, lifted lately, in force.
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	registerRelay(t, url, rc.RelayToken)
	r := rc.Relay
	k1 := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Device: w.build.id}).Result.Kick
	w.mac.must(proto.OpRelayUnkick, proto.OpArgs{Kick: k1})
	k2 := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Device: w.jnas.id}).Result.Kick
	w.mac.must(proto.OpRelayUnkick, proto.OpArgs{Kick: k2})
	k3 := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Person: w.jie}).Result.Kick
	set(`UPDATE relay_kicks SET lifted_at = ? WHERE id = ?`, ago(31*day), k1)
	set(`UPDATE relay_kicks SET lifted_at = ? WHERE id = ?`, ago(29*day), k2)

	// Relay usage: today, the first day of the month two months back (the
	// oldest that stays), the day before it, and long ago.
	u := now.UTC()
	first := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)
	for _, d := range []time.Time{u, first, first.AddDate(0, 0, -1), u.AddDate(0, 0, -200)} {
		set(`INSERT INTO relay_usage (relay, device, day, sent, received) VALUES (?, ?, ?, 100, 100)`, r, w.build.id, d.Unix()/86400)
	}

	// Devices: one logged out (as before this change, its addresses still
	// there), one run out, one in use; and one logging out now.
	ep := []netip.AddrPort{netip.MustParseAddrPort("198.51.100.7:41641"), netip.MustParseAddrPort("192.0.2.7:41641")}
	box := func(host string) *engine {
		e := newEngine(t, url, host, proto.Linux)
		e.login("kittors@deneb.test")
		e.report(proto.Report{Endpoints: ep})
		return e
	}
	gone, ranOut, now3 := box("gone"), box("ran-out"), box("logs-out")
	w.mac.report(proto.Report{Endpoints: ep})
	set(`UPDATE devices SET logged_out = 1 WHERE id = ?`, gone.id)
	set(`UPDATE devices SET expires_at = ? WHERE id = ?`, ago(day), ranOut.id)
	now3.call("POST", proto.PathLogout, nil, nil)
	endpoints := func(id proto.DeviceID) string {
		var e string
		s.db.QueryRow(`SELECT endpoints FROM devices WHERE id = ?`, id).Scan(&e)
		return e
	}
	if e := endpoints(now3.id); e != "[]" {
		t.Fatalf("logging out kept the addresses: %s", e)
	}

	c, err := s.cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if c.invites != 3 || c.shares != 2 || c.kicks != 1 || c.usage != 2 || c.addresses != 2 {
		t.Fatalf("cleaned %+v", c)
	}
	for id, want := range map[proto.InviteID]int{runOut: 0, used: 0, revoked: 0, recentlyOut: 1, live: 1, recentlyUsed: 1} {
		if n := count(`SELECT COUNT(*) FROM invites WHERE id = ?`, id); n != want {
			t.Errorf("invite %s: %d left, want %d", id, n, want)
		}
	}
	for id, want := range map[string]int{string(oldShare): 0, string(leftShare): 0, string(newShare): 1, string(activeShare): 1, "s_holding": 1} {
		if n := count(`SELECT COUNT(*) FROM shares WHERE id = ?`, id); n != want {
			t.Errorf("share %s: %d left, want %d", id, n, want)
		}
	}
	if n := count(`SELECT COUNT(*) FROM opens WHERE scope = ?`, string(shareScope(string(oldShare)))); n != 0 {
		t.Errorf("the old share's opens stayed: %d", n)
	}
	for id, want := range map[string]int{k1: 0, k2: 1, k3: 1} {
		if n := count(`SELECT COUNT(*) FROM relay_kicks WHERE id = ?`, id); n != want {
			t.Errorf("kick %s: %d left, want %d", id, n, want)
		}
	}
	if n := count(`SELECT COUNT(*) FROM relay_usage WHERE day >= ?`, first.Unix()/86400); n != 2 || count(`SELECT COUNT(*) FROM relay_usage`) != 2 {
		t.Errorf("relay usage left: %d of the last three months", n)
	}
	if endpoints(gone.id) != "[]" || endpoints(ranOut.id) != "[]" || endpoints(w.mac.id) == "[]" {
		t.Errorf("addresses: logged out %s, run out %s, in use %s", endpoints(gone.id), endpoints(ranOut.id), endpoints(w.mac.id))
	}
	if sw := s.state.Load().w; sw.devices[gone.id] == nil || sw.devices[ranOut.id] == nil {
		t.Error("a device logged out or run out went: its owner would get another address")
	}
	// The cards still work: the share in force, the kick in force.
	if v := w.jdesk.view(); deviceView(v, w.jdesk.id) == nil {
		t.Fatal("阿杰's view")
	}

	// Nothing more to do: nothing changes.
	v := s.Version()
	if c, err := s.cleanup(); err != nil || c.any() || s.Version() != v {
		t.Fatalf("a second cleanup: %+v, %v, version %d → %d", c, err, v, s.Version())
	}
}
