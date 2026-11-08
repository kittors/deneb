package control

import (
	"database/sql"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// clock is a test clock that only moves when told.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// newClockServer starts a development server on a clock the test moves.
func newClockServer(t *testing.T, life time.Duration) (*Server, *httptest.Server, *clock) {
	t.Helper()
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)} // the database keeps milliseconds
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: DevSeed(), Now: clk.now, LoginLifetime: life, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	return s, ts, clk
}

func TestLoginExpiry(t *testing.T) {
	s, ts, clk := newClockServer(t, 10*24*time.Hour)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	nas := newEngine(t, ts.URL, "nas", proto.Linux)
	mac.login("kittors@deneb.test")
	nas.login("kittors@deneb.test")
	home := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "家"}).Result.LAN
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: home, Device: nas.id})
	addr := netMapOf(s, nas.id).Self.Addrs[0]
	if got := s.state.Load().w.devices[nas.id].ExpiresAt; got.Sub(clk.now()) != 10*24*time.Hour {
		t.Fatalf("a new login lasts %v", got.Sub(clk.now()))
	}

	// Coming back for a token renews the login, about once a day.
	clk.add(2 * 24 * time.Hour)
	mac.auth()
	if got := s.state.Load().w.devices[mac.id].ExpiresAt; got.Sub(clk.now()) != 10*24*time.Hour {
		t.Fatalf("after coming back the Mac's login has %v left", got.Sub(clk.now()))
	}
	v := s.Version()
	mac.auth()
	if s.Version() != v {
		t.Fatal("a second token the same day renewed again")
	}

	// The NAS stays away past its lifetime: it leaves every map, its own
	// included, and its calls say it is not logged in.
	clk.add(9 * 24 * time.Hour)
	if s.sweep() != true {
		t.Fatal("the sweep did not notice the NAS ran out")
	}
	if netMapOf(s, nas.id) != nil {
		t.Fatal("the expired NAS still has a map")
	}
	if peerOf(netMapOf(s, mac.id), nas.id) != nil {
		t.Fatal("the Mac still sees the expired NAS")
	}
	if s.sweep() {
		t.Fatal("a second sweep found the same expiry again")
	}
	nas.auth()
	if _, err := nas.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("the expired NAS read its view: %v", err)
	}
	mac.auth()
	if dv := deviceView(mac.view(), nas.id); dv == nil {
		t.Fatal("the owner's view lost the expired NAS")
	}

	// Logging in again brings it back as it was.
	nas.login("kittors@deneb.test")
	nm := netMapOf(s, nas.id)
	if nm == nil || nm.Self.Addrs[0] != addr || len(nm.Scopes) != 1 || nm.Scopes[0].ID != proto.LANScope(home) {
		t.Fatalf("after logging in again: %+v", nm)
	}
	if got := s.state.Load().w.devices[nas.id].ExpiresAt; got.Sub(clk.now()) != 10*24*time.Hour {
		t.Fatalf("logging in again left %v", got.Sub(clk.now()))
	}
}

func TestInviteExpirySweep(t *testing.T) {
	s, ts, clk := newClockServer(t, 0)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	lan := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "家"}).Result.LAN
	mac.must(proto.OpLANInvite, proto.OpArgs{LAN: lan})
	if s.sweep() {
		t.Fatal("a sweep with nothing run out published")
	}
	clk.add(inviteTTL + time.Minute)
	v := s.Version()
	if !s.sweep() || s.Version() != v+1 {
		t.Fatal("an invite running out published nothing")
	}
	mac.auth()
	if lv := lanByName(mac.view(), "家"); len(lv.Invites) != 0 {
		t.Fatalf("the owner still sees %d invites", len(lv.Invites))
	}
	if s.sweep() {
		t.Fatal("the same invite ran out twice")
	}
}

// TestMigrateFromM1 opens a database made by the M1 server: the M2
// columns appear, nothing is lost, and old devices start their lifetime.
func TestMigrateFromM1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deneb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO people (id, email, name, display_name, initial, created_at) VALUES ('p_1', 'kittors@deneb.test', 'kittors', 'kittors', 'K', 1);
		INSERT INTO lans (id, name, owner, created_at) VALUES ('l_1', '家', 'p_1', 1);
		INSERT INTO members (lan, person, role, state, joined_at) VALUES ('l_1', 'p_1', 'owner', 'joined', 1);
		INSERT INTO devices (id, owner, machine_key, node_key, disco_key, name, display_name, kind, os, hostname, ipv4, ipv6, created_at)
			VALUES ('d_1', 'p_1', 'machinekey:` + hex32(1) + `', 'nodekey:` + hex32(2) + `', 'discokey:` + hex32(3) + `', 'mac', 'Mac', 'laptop', 'macos', 'mac', '10.67.1.1', 'fd00::a43:101', 1);
		INSERT INTO lan_devices (lan, device, joined_at) VALUES ('l_1', 'd_1', 1);
		INSERT INTO settings (key, value) VALUES ('network', '{"ipv4":"10.67.0.0/16","ipv6":"fd00::/48","dns":["10.67.0.53"],"domain":"deneb.internal"}');`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	s, err := New(Config{DataDir: dir, PublicURL: "http://x", Seed: DevSeed(), Now: func() time.Time { return now }, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ver int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&ver)
	if ver != len(migrations) {
		t.Fatalf("user_version %d after opening", ver)
	}
	w := s.state.Load().w
	l, d := w.lans["l_1"], w.devices["d_1"]
	if l == nil || d == nil || !l.joined("p_1") || l.hasDevice["d_1"] == nil || l.ApproveDevices || !l.ApproveMembers {
		t.Fatalf("after migrating: lan %+v, device %+v", l, d)
	}
	if d.ExpiresAt.Sub(now) != DefaultLoginLifetime || !d.usable() {
		t.Fatalf("an M1 device's login: expires %v", d.ExpiresAt)
	}
	if nm := w.netMap("d_1", 1, nil); nm == nil || len(nm.Scopes) != 1 {
		t.Fatalf("the M1 device's map: %+v", nm)
	}
}

func hex32(b byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = digits[(int(b)+i)%16]
	}
	return string(out)
}

// TestPauseLeavesOtherLANAlone is the server's half of M2's pause check
// ("暂停周末联机 10 秒，工作室零影响"): the Mac pausing 周末联机 sends
// nothing at all to a device that shares only 工作室 with it, while the
// NAS in 周末联机 loses it at once and gets it back on resume.
func TestPauseLeavesOtherLANAlone(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	build := newEngine(t, ts.URL, "build", proto.Linux)
	nas := newEngine(t, ts.URL, "nas", proto.Linux)
	mac.login("kittors@deneb.test")
	build.login("kittors@deneb.test")
	nas.login("jie@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: build.id})
	weekend := nas.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	inv := nas.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}).Result.Invite
	mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})

	// The build server is headless: it streams its map only. (A stream that
	// asks for kittors's view does see the pause, as it should: the view
	// shows the Mac paused in 周末联机.)
	ep := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:41641")}
	mac.report(proto.Report{Endpoints: ep})
	bst := build.stream(t, false)
	nst := nas.stream(t, false)
	mst := mac.stream(t, false)
	drain := func(st *stream) {
		for {
			select {
			case <-st.frames:
			case <-time.After(300 * time.Millisecond):
				return
			}
		}
	}
	drain(bst)
	drain(nst)
	drain(mst)

	t0 := time.Now()
	mac.report(proto.Report{Endpoints: ep, Paused: []proto.ScopeID{proto.LANScope(weekend)}})
	f := nst.next(t)
	took := time.Since(t0)
	if peerOf(f.NetMap, mac.id) != nil {
		t.Fatal("the NAS still sees the paused Mac")
	}
	if f := mst.next(t); !scopeOf(f.NetMap, weekend).Paused || peerOf(f.NetMap, build.id) == nil {
		t.Fatalf("the Mac's own map after pausing: %+v", f.NetMap.Scopes)
	}
	mac.report(proto.Report{Endpoints: ep})
	if f := nst.next(t); peerOf(f.NetMap, mac.id) == nil {
		t.Fatal("the NAS did not get the Mac back")
	}
	select {
	case f := <-bst.frames:
		t.Fatalf("the build server, only in 工作室, got a frame (version %d) for a pause in 周末联机", f.Version)
	case <-time.After(300 * time.Millisecond):
	}
	t.Logf("report to the NAS's frame: %v", took)
	_ = s
}

func scopeOf(nm *proto.NetMap, l proto.LANID) proto.Scope {
	for _, sc := range nm.Scopes {
		if sc.ID == proto.LANScope(l) {
			return sc
		}
	}
	return proto.Scope{}
}

// TestRenewWakesWaitingLogin: a device whose login ran out sits in
// login/wait; its owner renews it from another device, and the wait ends at
// once with the device logged in, no browser needed.
func TestRenewWakesWaitingLogin(t *testing.T) {
	s, ts, clk := newClockServer(t, 10*24*time.Hour)
	desk := newEngine(t, ts.URL, "desktop", proto.Windows)
	nas := newEngine(t, ts.URL, "nas", proto.Linux)
	desk.login("jie@deneb.test")
	nas.login("jie@deneb.test")
	clk.add(9 * 24 * time.Hour)
	desk.auth() // the desktop came back; the NAS did not
	clk.add(2 * 24 * time.Hour)
	s.sweep()
	desk.auth()
	nas.auth()
	var st proto.LoginState
	nas.call("POST", proto.PathLogin, nas.loginRequest(), &st)
	if st.LoggedIn || st.LoginURL == "" {
		t.Fatalf("the expired NAS's login: %+v", st)
	}
	waited := make(chan proto.LoginState, 1)
	go func() {
		var w proto.LoginState
		nas.call("POST", proto.PathLoginWait, nil, &w)
		waited <- w
	}()
	time.Sleep(20 * time.Millisecond)
	desk.must(proto.OpDeviceRenew, proto.OpArgs{Device: nas.id})
	select {
	case w := <-waited:
		if !w.LoggedIn || w.Device != nas.id || w.LoginExpiresAt.Sub(clk.now()) != 10*24*time.Hour {
			t.Fatalf("the NAS's wait ended with %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("renewing did not wake the NAS's wait")
	}
	if netMapOf(s, nas.id) == nil {
		t.Fatal("the renewed NAS has no map")
	}
}

// TestStreamRenewsLogin: a device that keeps its stream up for days without
// a new token still renews its login, from the stream's keep-alives.
func TestStreamRenewsLogin(t *testing.T) {
	s, ts, clk := newClockServer(t, 10*24*time.Hour)
	s.keepAlive = 20 * time.Millisecond
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	st := mac.stream(t, false)
	st.next(t)
	clk.add(3 * 24 * time.Hour)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if left := s.state.Load().w.devices[mac.id].ExpiresAt.Sub(clk.now()); left == 10*24*time.Hour {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("after three days on one stream the login has %v left", s.state.Load().w.devices[mac.id].ExpiresAt.Sub(clk.now()))
}
