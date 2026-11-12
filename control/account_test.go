package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestAccountDelete is account.delete (design/plan/m7/account-delete.md):
// an account the seed names is refused; a dry run says what would go and
// changes nothing; a LAN owned with others in it blocks the deletion until
// the person chooses, and a wrong confirmation stops it; then everything
// of theirs goes, a LAN handed over goes to its member who joined first,
// their relays go even where others used them, kicks they made on a
// public relay stay in force, and no one else's view mentions them. The
// operator deletes another through the admin socket, its LAN with it.
func TestAccountDelete(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	sock := serveAdmin(t, s)
	now := w.clk.now()
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	set := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	consistent := func() {
		t.Helper()
		rows, err := s.db.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if rows.Next() {
			t.Fatal("a row still points at what went")
		}
	}

	mia := newEngine(t, url, "mia-pc", proto.Windows)
	mia.login("mia@deneb.test")
	leo := newEngine(t, url, "leo-pc", proto.Linux)
	leo.login("leo@deneb.test")
	miaID, leoID := mia.view().Me, leo.view().Me
	join := func(e, owner *engine, l proto.LANID) {
		inv := owner.must(proto.OpLANInvite, proto.OpArgs{LAN: l}).Result.Invite
		e.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	}
	// 工作室 gets Mia, and Leo a minute later; Mia joins 周末联机 too.
	join(mia, w.mac, w.studio)
	w.clk.add(time.Minute)
	join(leo, w.mac, w.studio)
	join(mia, w.jdesk, w.weekend)
	// 家 is mine alone; a link to 工作室 no one used.
	home := w.mac.must(proto.OpLANCreate, proto.OpArgs{Name: "家"}).Result.LAN
	w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio})

	// My relays besides the seed's hk (a default): 香港 in 工作室, where
	// Mia's traffic is counted, 东京 nowhere, and a code for one to come.
	hk := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	registerRelay(t, url, hk.RelayToken)
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: hk.Relay})
	tokyo := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result.Relay
	w.mac.must(proto.OpRelayJoin, proto.OpArgs{})
	set(`INSERT INTO relay_usage (relay, device, day, sent, received) VALUES (?, ?, ?, 1, 1)`, hk.Relay, mia.id, now.Unix()/86400)

	// 阿杰's NAS shared with me, its SSH open to me; his desktop refuses me;
	// his relay kicks me. My build server shared with him.
	svc := &proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{{Proto: "tcp", First: 22, Last: 22}}}
	fromJie := w.jdesk.must(proto.OpShareCreate, proto.OpArgs{Device: w.jnas.id, Person: w.me}).Result.Share
	w.jdesk.must(proto.OpServiceSet, proto.OpArgs{Share: fromJie, Service: svc, On: true})
	w.jdesk.must(proto.OpDeviceRefuse, proto.OpArgs{Device: w.jdesk.id, Person: w.me, Refuse: true})
	jr := w.jdesk.must(proto.OpRelayCreate, proto.OpArgs{Name: "深圳"}).Result.Relay
	kickedMe := w.jdesk.must(proto.OpRelayKick, proto.OpArgs{Relay: jr, Person: w.me}).Result.Kick
	toJie := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}).Result.Share
	// A public relay, where I kicked Leo off as an operator.
	set(`INSERT INTO relays (id, name, source, host_name, derp_port) VALUES ('pub', '公共', ?, 'pub.example.com', 443)`, proto.RelayPublic)
	set(`INSERT INTO relay_kicks (id, relay, person, by_person, at) VALUES ('k_op', 'pub', ?, ?, ?)`, leoID, w.me, ms(now))
	// A browser logged in as me.
	cookie := &http.Cookie{Name: proto.WebSessionCookie, Value: "a browser's token"}
	set(`INSERT INTO web_sessions (id, person, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashSecret(cookie.Value), w.me, ms(now), ms(now.Add(time.Hour)))
	browser := httptest.NewRequest("GET", "/", nil)
	browser.AddCookie(cookie)
	if s.sessionOf(browser) == nil {
		t.Fatal("the browser is not logged in")
	}

	// The seed names kittors (an account, and hk's owner): refused, even
	// a dry run.
	w.mac.refused(proto.ErrForbidden, proto.OpAccountDelete, proto.OpArgs{DryRun: true})
	s.cfg.Seed = Seed{} // the operator took them out of it

	// A dry run: what would go, and 工作室 blocking it; nothing changes.
	v := s.Version()
	d := w.mac.must(proto.OpAccountDelete, proto.OpArgs{DryRun: true}).Result.Account
	relays := []proto.RelayID{"hk", hk.Relay, tokyo}
	slices.Sort(relays)
	inUse := []proto.RelayID{"hk", hk.Relay}
	slices.Sort(inUse)
	devices := []proto.DeviceID{w.mac.id, w.build.id}
	slices.Sort(devices)
	switch {
	case s.Version() != v || !d.DryRun:
		t.Fatalf("the dry run changed something (version %d → %d) or did not say it was one", v, s.Version())
	case !slices.Equal(d.Devices, devices), !slices.Equal(d.LANs, []proto.LANID{home}), !slices.Equal(d.Blocked, []proto.LANID{w.studio}),
		!slices.Equal(d.Left, []proto.LANID{w.weekend}), len(d.Transferred) != 0:
		t.Fatalf("the dry run: %+v", d)
	case !slices.Equal(d.Relays, relays), !slices.Equal(d.RelaysInUse, inUse), d.Shares != 2, d.Invites != 4:
		// Invites: my three to 工作室 and 阿杰's to 周末联机 that I used.
		t.Fatalf("the dry run: %+v", d)
	}
	if d := w.mac.must(proto.OpAccountDelete, proto.OpArgs{DryRun: true, OwnedLANs: proto.AccountTransfer}).Result.Account; len(d.Blocked) != 0 ||
		len(d.Transferred) != 1 || d.Transferred[0] != (proto.LANTransfer{LAN: w.studio, To: miaID}) {
		t.Fatalf("handing over, Mia joined first: %+v", d)
	}

	// Blocked, a choice no one knows, a wrong confirmation: nothing goes.
	if err := w.mac.refused(proto.ErrConflict, proto.OpAccountDelete, proto.OpArgs{Confirm: "kittors@deneb.test"}); !strings.Contains(err.Message, "「工作室」") {
		t.Fatalf("blocked: %q", err.Message)
	}
	w.mac.refused(proto.ErrInvalid, proto.OpAccountDelete, proto.OpArgs{Confirm: "kittors@deneb.test", OwnedLANs: "keep"})
	w.mac.refused(proto.ErrInvalid, proto.OpAccountDelete, proto.OpArgs{Confirm: "jie@deneb.test", OwnedLANs: proto.AccountTransfer})
	if s.Version() != v {
		t.Fatal("a refused deletion changed something")
	}

	// Deleted, 工作室 handed over.
	mst := w.mac.stream(t, false)
	mst.next(t)
	d = w.mac.must(proto.OpAccountDelete, proto.OpArgs{Confirm: " Kittors@deneb.test ", OwnedLANs: proto.AccountTransfer}).Result.Account
	if d.DryRun || !slices.Equal(d.LANs, []proto.LANID{home}) || len(d.Transferred) != 1 || d.Transferred[0].To != miaID {
		t.Fatalf("deleted: %+v", d)
	}
	for f := mst.next(t); !f.LoggedOut; f = mst.next(t) {
	}
	if _, err := w.build.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("the build server's call: %v", err)
	}
	if s.sessionOf(browser) != nil {
		t.Fatal("the browser is still logged in")
	}
	consistent()
	sw := s.state.Load().w
	switch {
	case sw.people[w.me] != nil, sw.byEmail["kittors@deneb.test"] != nil:
		t.Fatal("the account is still there")
	case sw.devices[w.mac.id] != nil, sw.devices[w.build.id] != nil:
		t.Fatal("a device is still there")
	case sw.lans[home] != nil:
		t.Fatal("家 is still there")
	case sw.shares[string(fromJie)] != nil, sw.shares[string(toJie)] != nil:
		t.Fatal("a share is still there")
	case sw.relays["hk"] != nil, sw.relays[hk.Relay] != nil, sw.relays[tokyo] != nil, sw.relays[jr] == nil:
		t.Fatal("the relays: mine are to go, 阿杰's to stay")
	case len(sw.lans[w.studio].added) != 0:
		t.Fatalf("工作室 still has %v dragged in", sw.lans[w.studio].added)
	case sw.kicks[kickedMe] != nil, sw.kicks["k_op"] == nil || sw.kicks["k_op"].By != "" || !sw.kickedPerson["pub"][leoID]:
		t.Fatal("the kicks: of me to go, mine on the public relay to stay in force as the deployment's")
	}
	for q, n := range map[string]int{
		`SELECT COUNT(*) FROM opens WHERE scope = ?`:              count(`SELECT COUNT(*) FROM opens WHERE scope = ?`, string(shareScope(string(fromJie)))),
		`SELECT COUNT(*) FROM refusals WHERE person = ?`:          count(`SELECT COUNT(*) FROM refusals WHERE person = ?`, w.me),
		`SELECT COUNT(*) FROM invites WHERE …me`:                  count(`SELECT COUNT(*) FROM invites WHERE inviter = ? OR person = ? OR used_by = ?`, w.me, w.me, w.me),
		`SELECT COUNT(*) FROM relay_joins WHERE owner = ?`:        count(`SELECT COUNT(*) FROM relay_joins WHERE owner = ?`, w.me),
		`SELECT COUNT(*) FROM relay_usage WHERE relay = ?`:        count(`SELECT COUNT(*) FROM relay_usage WHERE relay = ?`, hk.Relay),
		`SELECT COUNT(*) FROM web_sessions WHERE person = ?`:      count(`SELECT COUNT(*) FROM web_sessions WHERE person = ?`, w.me),
		`SELECT COUNT(*) FROM relay_kicks WHERE … = me`:           count(`SELECT COUNT(*) FROM relay_kicks WHERE person = ? OR by_person = ? OR lifted_by = ?`, w.me, w.me, w.me),
		`SELECT COUNT(*) FROM members WHERE person = ?`:           count(`SELECT COUNT(*) FROM members WHERE person = ?`, w.me),
		`SELECT COUNT(*) FROM lan_devices of the deleted devices`: count(`SELECT COUNT(*) FROM lan_devices WHERE device IN (?, ?)`, w.mac.id, w.build.id),
	} {
		if n != 0 {
			t.Errorf("%s: %d left", q, n)
		}
	}
	// 工作室 is Mia's now, Leo still in it; 周末联机 is 阿杰's and Mia's.
	lv := lanByName(mia.view(), "工作室")
	if lv == nil || lv.Owner != miaID || len(lv.Members) != 2 {
		t.Fatalf("工作室 as Mia sees it: %+v", lv)
	}
	for _, m := range lv.Members {
		if m.Person == miaID && m.Role != proto.RoleOwner || m.Person != miaID && m.Person != leoID {
			t.Fatalf("工作室's members: %+v", lv.Members)
		}
	}
	mia.must(proto.OpLANRename, proto.OpArgs{LAN: w.studio, Name: "Mia 的工作室"}) // hers to manage
	if lv := lanByName(w.jdesk.view(), "周末联机"); lv == nil || len(lv.Members) != 2 {
		t.Fatalf("周末联机 as 阿杰 sees it: %+v", lv)
	}
	// No one's view says who I was.
	for _, e := range []*engine{w.jdesk, w.jnas, mia, leo} {
		b, _ := json.Marshal(e.view())
		for _, gone := range []string{string(w.me), "kittors@deneb.test", string(w.mac.id), string(w.build.id), string(hk.Relay)} {
			if strings.Contains(string(b), gone) {
				t.Errorf("%s's view still has %s", e.host, gone)
			}
		}
	}

	// The operator deletes 阿杰's account for him: a dry run first, by
	// default; 周末联机, with Mia in it, has to be chosen for.
	d, err := AdminAccountDelete(sock, AdminAccountDeleteRequest{Email: "jie@deneb.test", DryRun: true})
	if err != nil || !d.DryRun || !slices.Equal(d.Blocked, []proto.LANID{w.weekend}) {
		t.Fatalf("the admin's dry run: %+v, %v", d, err)
	}
	if _, err := AdminAccountDelete(sock, AdminAccountDeleteRequest{Email: "jie@deneb.test"}); err == nil || !strings.Contains(err.Error(), "「周末联机」") {
		t.Fatalf("blocked: %v", err)
	}
	d, err = AdminAccountDelete(sock, AdminAccountDeleteRequest{Email: "jie@deneb.test", OwnedLANs: proto.AccountDeleteLANs})
	if err != nil || d.DryRun || !slices.Equal(d.LANs, []proto.LANID{w.weekend}) || !slices.Equal(d.Relays, []proto.RelayID{jr}) || len(d.Devices) != 2 {
		t.Fatalf("the admin deleted: %+v, %v", d, err)
	}
	consistent()
	if mv := mia.view(); lanByName(mv, "周末联机") != nil || lanByName(mv, "Mia 的工作室") == nil {
		t.Fatal("Mia's LANs: 周末联机 is to go, her 工作室 to stay")
	}
	if _, err := AdminAccountDelete(sock, AdminAccountDeleteRequest{Email: "jie@deneb.test", DryRun: true}); err == nil {
		t.Fatal("deleted twice")
	}
}
