package control

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// consoleWorld is the M2 world with an operator's console: kittors is the
// deployment's operator, logged in in a browser; Mia and Leo have
// devices too; the server's log is in files, its mail recorded.
type consoleWorld struct {
	*m2World
	ops   *browser2 // kittors's browser
	mia   *engine
	leo   *engine
	mails recMailer
	mu    sync.Mutex
	logs  []string
}

func newConsoleWorld(t *testing.T) *consoleWorld {
	t.Helper()
	cw := &consoleWorld{mails: make(recMailer, 16)}
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: DevSeed(), Now: clk.now, Mailer: cw.mails, LogDir: t.TempDir(),
		Logf: func(f string, a ...any) {
			cw.mu.Lock()
			cw.logs = append(cw.logs, fmt.Sprintf(f, a...))
			cw.mu.Unlock()
		}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	w := &m2World{s: s, clk: clk,
		mac: newEngine(t, ts.URL, "mac", proto.MacOS), build: newEngine(t, ts.URL, "build", proto.Linux),
		jdesk: newEngine(t, ts.URL, "desktop", proto.Windows), jnas: newEngine(t, ts.URL, "nas", proto.Linux)}
	w.mac.login("kittors@deneb.test")
	w.build.login("kittors@deneb.test")
	w.jdesk.login("jie@deneb.test")
	w.jnas.login("jie@deneb.test")
	w.studio = w.mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	w.weekend = w.jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	inv := w.jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	w.mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	w.me, w.jie = w.mac.view().Me, w.jdesk.view().Me
	cw.m2World = w
	if err := s.opsSetOperator(nil, w.me, true); err != nil {
		t.Fatal(err)
	}
	cw.ops = newWebBrowser(t, ts.URL)
	cw.ops.login("kittors@deneb.test", DevCode)
	cw.mia = newEngine(t, ts.URL, "mia-pc", proto.Windows)
	cw.mia.login("mia@deneb.test")
	cw.leo = newEngine(t, ts.URL, "leo-vm", proto.Linux)
	cw.leo.login("leo@deneb.test")
	return cw
}

func (cw *consoleWorld) get(t *testing.T, b *browser2, path string, out any) int {
	t.Helper()
	return b.do("GET", "/api/v1/operator/"+path, nil, out, nil)
}

func (cw *consoleWorld) act(t *testing.T, b *browser2, act string, args map[string]string, out any) int {
	t.Helper()
	return b.do("POST", "/api/v1/operator/act", OpsAct{Act: act, Args: args}, out, nil)
}

func (cw *consoleWorld) records(t *testing.T) []OpsRecord {
	t.Helper()
	var recs []OpsRecord
	if st := cw.get(t, cw.ops, "records", &recs); st != 200 {
		t.Fatalf("records: %d", st)
	}
	return recs
}

func (cw *consoleWorld) logged(sub string) bool {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return slices.ContainsFunc(cw.logs, func(l string) bool { return strings.Contains(l, sub) })
}

// TestConsoleOnlyOperators: every page and act of the console is the
// deployment's operators'; anyone else gets 403, and nothing changes.
func TestConsoleOnlyOperators(t *testing.T) {
	cw := newConsoleWorld(t)
	mia := newWebBrowser(t, cw.mac.url)
	mia.login("mia@deneb.test", DevCode)
	for _, p := range []string{"overview", "backups", "people", "people/" + string(cw.me), "devices", "logins", "records", "log"} {
		if st := cw.get(t, mia, p, nil); st != http.StatusForbidden {
			t.Errorf("%s as Mia: %d", p, st)
		}
		if st := cw.get(t, cw.ops, p, nil); st != 200 {
			t.Errorf("%s as the operator: %d", p, st)
		}
	}
	v := cw.s.Version()
	if st := cw.act(t, mia, "device.delete", map[string]string{"device": string(cw.build.id)}, nil); st != http.StatusForbidden || cw.s.Version() != v {
		t.Fatalf("an act as Mia: %d", st)
	}
	logged := newWebBrowser(t, cw.mac.url)
	if st := cw.get(t, logged, "overview", nil); st != http.StatusUnauthorized {
		t.Fatalf("not logged in: %d", st)
	}
}

// TestConsoleViews: what the console shows of people, devices, waiting
// logins and the deployment, and nothing of what devices report of their
// addresses.
func TestConsoleViews(t *testing.T) {
	cw := newConsoleWorld(t)
	s := cw.s
	// Mia's LAN has Leo in it; Mia's computer is online.
	miaHome := cw.mia.must(proto.OpLANCreate, proto.OpArgs{Name: "Mia 的家"}).Result.LAN
	inv := cw.mia.must(proto.OpLANInvite, proto.OpArgs{LAN: miaHome}).Result.Invite
	cw.clk.add(time.Minute)
	cw.leo.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	st := cw.mia.stream(t, false)
	st.next(t)

	var people struct {
		Total  int         `json:"total"`
		People []OpsPerson `json:"people"`
	}
	// The seed's four, made at once: by email.
	if cw.get(t, cw.ops, "people", &people); people.Total != 4 || len(people.People) != 4 || people.People[0].Email != "jie@deneb.test" {
		t.Fatalf("people: %+v", people)
	}
	for q, want := range map[string]int{"people?q=MIA": 1, "people?filter=operators": 1, "people?filter=test": 4, "people?filter=online": 1, "people?filter=new": 4, "people?limit=2&offset=3": 4} {
		cw.get(t, cw.ops, q, &people)
		if people.Total != want {
			t.Errorf("%s: %d, want %d", q, people.Total, want)
		}
	}
	if cw.get(t, cw.ops, "people?limit=2&offset=3", &people); len(people.People) != 1 {
		t.Fatalf("a page: %d", len(people.People))
	}

	mia := s.state.Load().w.byEmail["mia@deneb.test"]
	var d OpsPersonDetail
	if st := cw.get(t, cw.ops, "people/"+string(mia.ID), &d); st != 200 {
		t.Fatalf("Mia's card: %d", st)
	}
	switch {
	case d.Name != "Mia", !d.Online, d.Devices != 1, len(d.DeviceList) != 1, d.DeviceList[0].State != "online":
		t.Fatalf("Mia's card: %+v", d)
	case d.LANs.Count != 1 || d.LANs.Owned != 1, len(d.Owned) != 1, d.Owned[0].Name != "Mia 的家", d.Owned[0].Others != 1, d.Owned[0].First != "Leo":
		t.Fatalf("Mia's LANs: %+v %+v", d.LANs, d.Owned)
	case d.Seats.Own, d.Seats.Default != DefaultSeatLimit, d.Seats.Limit != DefaultSeatLimit, d.Seats.Used != 2:
		t.Fatalf("Mia's seats: %+v", d.Seats)
	case d.Browsers.Count != 0, d.Relays != 0, d.Test != true:
		t.Fatalf("Mia's card: %+v", d)
	}
	if st := cw.get(t, cw.ops, "people/p_nobody", nil); st != http.StatusNotFound {
		t.Fatalf("no one's card: %d", st)
	}

	var devs struct {
		Total, All, Online int
		Devices            []OpsDevice `json:"devices"`
	}
	cw.get(t, cw.ops, "devices", &devs)
	if devs.All != 6 || devs.Total != 6 || devs.Online != 1 || len(devs.Devices) != 6 {
		t.Fatalf("devices: %d of %d, %d online", devs.Total, devs.All, devs.Online)
	}
	cw.get(t, cw.ops, "devices?state=online", &devs)
	if devs.Total != 1 || devs.Devices[0].Owner.Name != "Mia" || devs.Devices[0].Address == "" {
		t.Fatalf("online: %+v", devs.Devices)
	}
	cw.get(t, cw.ops, "devices?q=jie@deneb", &devs)
	if devs.Total != 2 {
		t.Fatalf("阿杰's devices: %d", devs.Total)
	}

	// A device waiting to log in, by an id that is not its code.
	ipad := newEngine(t, cw.mac.url, "ipad", proto.IOS)
	ipad.auth()
	var ls proto.LoginState
	ipad.call("POST", proto.PathLogin, ipad.loginRequest(), &ls)
	var logins struct {
		Logins   []OpsLogin `json:"logins"`
		Accounts []OpsWho   `json:"accounts"`
	}
	cw.get(t, cw.ops, "logins", &logins)
	code := loginCode(ls.LoginURL)
	if len(logins.Logins) != 1 || logins.Logins[0].Hostname != "ipad" || strings.Contains(logins.Logins[0].ID, code) || len(logins.Accounts) != 4 {
		t.Fatalf("logins: %+v", logins)
	}

	var o OpsOverview
	cw.get(t, cw.ops, "overview", &o)
	if o.Counts.People != 4 || o.Counts.Devices != 6 || o.Counts.Online != 1 || o.Counts.LANs != 3 || o.Counts.Browsers != 1 ||
		o.Server.Started.IsZero() || o.Server.Version == "" || o.Alerts == nil || o.Relays.Names == nil {
		t.Fatalf("overview: %+v", o)
	}
	var bs []Backup
	if st := cw.get(t, cw.ops, "backups", &bs); st != 200 || bs == nil {
		t.Fatalf("backups: %d %v", st, bs)
	}
}

// TestConsoleActs: each act of the console, its line in the record and in
// the log, and what it refuses.
func TestConsoleActs(t *testing.T) {
	cw := newConsoleWorld(t)
	s := cw.s
	w := func() *World { return s.state.Load().w }
	mia := w().byEmail["mia@deneb.test"].ID
	var d OpsPersonDetail

	// Seats: up, then the deployment's again; a number that is no limit.
	if st := cw.act(t, cw.ops, "seats.set", map[string]string{"person": string(mia), "limit": "8"}, &d); st != 200 || d.Seats.Limit != 8 || !d.Seats.Own {
		t.Fatalf("seats to 8: %d %+v", st, d.Seats)
	}
	if st := cw.act(t, cw.ops, "seats.set", map[string]string{"person": string(mia), "limit": fmt.Sprint(DefaultSeatLimit)}, &d); st != 200 || d.Seats.Own {
		t.Fatalf("seats back: %d %+v", st, d.Seats)
	}
	if st := cw.act(t, cw.ops, "seats.set", map[string]string{"person": string(mia), "limit": "-5"}, nil); st != http.StatusBadRequest {
		t.Fatalf("a limit of -5: %d", st)
	}

	// Browsers: Mia in two, then none.
	for range 2 {
		newWebBrowser(t, cw.mac.url).login("mia@deneb.test", DevCode)
	}
	if st := cw.act(t, cw.ops, "sessions.revoke", map[string]string{"person": string(mia)}, &d); st != 200 || d.Browsers.Count != 0 {
		t.Fatalf("sessions: %d %+v", st, d.Browsers)
	}

	// Operators: Mia in; kittors cannot put himself out.
	if st := cw.act(t, cw.ops, "operator.set", map[string]string{"person": string(mia), "on": "true"}, &d); st != 200 || !d.Operator {
		t.Fatalf("Mia an operator: %d %+v", st, d)
	}
	if st := cw.act(t, cw.ops, "operator.set", map[string]string{"person": string(cw.me), "on": "false"}, nil); st != http.StatusConflict || !w().people[cw.me].Operator {
		t.Fatalf("out himself: %d", st)
	}

	// Devices: Leo's logs in again; 阿杰's NAS goes.
	leoVM, nas := w().devices[cw.leo.id].DisplayName, w().devices[cw.jnas.id].DisplayName
	var dev OpsDevice
	if st := cw.act(t, cw.ops, "device.relogin", map[string]string{"device": string(cw.leo.id)}, &dev); st != 200 || dev.State != "expired" {
		t.Fatalf("relogin: %d %+v", st, dev)
	}
	if st := cw.act(t, cw.ops, "device.delete", map[string]string{"device": string(cw.jnas.id)}, nil); st != 200 || w().devices[cw.jnas.id] != nil {
		t.Fatalf("delete: %d", st)
	}
	if st := cw.act(t, cw.ops, "device.delete", map[string]string{"device": string(cw.jnas.id)}, nil); st != http.StatusNotFound {
		t.Fatalf("delete again: %d", st)
	}

	// A login waiting, let in to Leo's test account.
	ipad := newEngine(t, cw.mac.url, "ipad", proto.IOS)
	ipad.auth()
	ipad.call("POST", proto.PathLogin, ipad.loginRequest(), &proto.LoginState{})
	var logins struct {
		Logins []OpsLogin `json:"logins"`
	}
	cw.get(t, cw.ops, "logins", &logins)
	if st := cw.act(t, cw.ops, "login.approve", map[string]string{"login": logins.Logins[0].ID, "email": "leo@deneb.test"}, nil); st != 200 {
		t.Fatalf("approve: %d", st)
	}
	var ipadName string
	for _, d := range w().devices {
		if d.Hostname == "ipad" {
			ipadName = d.DisplayName
		}
	}
	if st := cw.act(t, cw.ops, "login.approve", map[string]string{"login": logins.Logins[0].ID, "email": "leo@deneb.test"}, nil); st != http.StatusNotFound {
		t.Fatalf("approve again: %d", st)
	}

	// An account: 阿杰's, whose 周末联机 kittors is in. The seed names him:
	// take him out of it first.
	s.cfg.Seed = Seed{}
	jie := w().people[cw.jie]
	args := map[string]string{"person": string(cw.jie), "confirm": "nobody@deneb.test"}
	if st := cw.act(t, cw.ops, "account.delete", args, nil); st != http.StatusBadRequest {
		t.Fatalf("a wrong confirm: %d", st)
	}
	args["confirm"] = " JIE@deneb.test "
	if st := cw.act(t, cw.ops, "account.delete", args, nil); st != http.StatusConflict || w().people[cw.jie] == nil {
		t.Fatalf("周末联机 not chosen for: %d", st)
	}
	args["lan:"+string(cw.weekend)] = "keep"
	if st := cw.act(t, cw.ops, "account.delete", args, nil); st != http.StatusBadRequest {
		t.Fatalf("a choice that is none: %d", st)
	}
	args["lan:"+string(cw.weekend)] = proto.AccountTransfer
	if st := cw.act(t, cw.ops, "account.delete", args, nil); st != 200 || w().people[cw.jie] != nil || w().lans[cw.weekend].Owner != cw.me {
		t.Fatalf("deleted: %d", st)
	}
	if code, _ := cw.mails.next(t); code != "deleted" {
		t.Fatalf("the mail to %s: %s", jie.Email, code)
	}
	if st := cw.act(t, cw.ops, "account.delete", map[string]string{"person": string(cw.me), "confirm": "kittors@deneb.test"}, nil); st != http.StatusConflict {
		t.Fatalf("his own: %d", st)
	}
	if st := cw.act(t, cw.ops, "nothing", nil, nil); st != http.StatusBadRequest {
		t.Fatalf("no such act: %d", st)
	}

	// The record, newest first, by kittors, each also a line of the log.
	recs := cw.records(t)
	want := []string{
		"替阿杰（jie@deneb.test）注销了账号，「周末联机」转给了 kittors",
		pangu("批准了「" + ipadName + "」登进 leo@deneb.test"),
		pangu("删了阿杰的「" + nas + "」"),
		pangu("让 Leo 的「" + leoVM + "」重新登录"),
		"把 Mia 设成了部署管理员",
		"让 Mia 在 2 个浏览器里都退出了",
		fmt.Sprintf("把 Mia 的位置上限恢复成了部署的默认（%d 个）", DefaultSeatLimit),
		"把 Mia 的位置上限调成了 8",
		"把 kittors 设成了部署管理员", // newConsoleWorld's, at the admin socket
	}
	var got []string
	for _, r := range recs {
		got = append(got, r.Text)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the record:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if recs[0].By.ID != cw.me || recs[0].By.Name != "kittors" || recs[len(recs)-1].By.Name != "服务器上的命令" {
		t.Fatalf("who: %+v, %+v", recs[0].By, recs[len(recs)-1].By)
	}
	if !cw.logged("operator kittors@deneb.test: " + pangu("删了阿杰的「"+nas+"」") + " [device.delete]") {
		t.Fatal("no line in the log")
	}
}

// TestConsoleRelayRecords: what operators do to public relays on the
// public relays' page goes into the record; what someone does to their
// own relay does not.
func TestConsoleRelayRecords(t *testing.T) {
	cw := newConsoleWorld(t)
	s := cw.s
	// A public relay the console made (not the seed's), into the world.
	s.db.Exec(`INSERT INTO relays (id, name, source, host_name, derp_port, created_at) VALUES ('pub', '香港', ?, 'pub.example.com', 443, ?)`,
		proto.RelayPublic, ms(cw.clk.now()))
	s.change("", func(*sql.Tx, *World) error { return nil })
	mia := s.state.Load().w.byEmail["mia@deneb.test"].ID
	two := 2
	k := cw.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: "pub", Person: mia}).Result.Kick
	cw.mac.must(proto.OpRelayUnkick, proto.OpArgs{Kick: k})
	cw.mac.must(proto.OpRelaySetWeight, proto.OpArgs{Relay: "pub", Weight: &two})
	cw.mac.must(proto.OpRelayJoin, proto.OpArgs{Public: true, Name: "新加坡"})
	own := cw.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "我的"}).Result.Relay
	cw.mac.must(proto.OpRelaySetWeight, proto.OpArgs{Relay: own, Weight: &two})

	var got []string
	for _, r := range cw.records(t) {
		got = append(got, r.Text)
	}
	want := []string{
		"为加一台公共中转站「新加坡」生成了安装命令",
		"把公共中转站「香港」的份额调成了 2",
		"又让 Mia 用公共中转站「香港」了",
		"不让 Mia 用公共中转站「香港」",
		"把 kittors 设成了部署管理员",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the record:\n%s", strings.Join(got, "\n"))
	}
}

// TestConsoleLog: today's and yesterday's log, each line with its kind,
// masked; the last 200 first, 200 more at a time back to 2000 at most,
// and what came after since; a seq that holds as the files grow.
func TestConsoleLog(t *testing.T) {
	cw := newConsoleWorld(t)
	s := cw.s
	now := cw.clk.now().UTC()
	today := now.Truncate(24 * time.Hour)
	file := func(day time.Time) string { return filepath.Join(s.cfg.LogDir, logFileName(day.Format("20060102"))) }
	line := func(at time.Time, text string) string { return at.Format("2006/01/02 15:04:05") + " " + text + "\n" }
	var y strings.Builder
	for i := range 1500 {
		y.WriteString(line(today.Add(-time.Hour).Add(time.Duration(i)*time.Second), fmt.Sprintf("device d_%d (pc) online, engine \"0.7\"", i)))
	}
	os.WriteFile(file(today.AddDate(0, 0, -1)), []byte(y.String()), 0o600)
	os.WriteFile(file(today.AddDate(0, 0, -2)), []byte(line(today.AddDate(0, 0, -2), "too old")), 0o600)
	var b strings.Builder
	secrets := []string{
		"web login: code asked for \"kittors@deneb.test\" from 203.0.113.9, account p_x: sent",
		"http: TLS handshake error from 198.51.100.7:52011: EOF",
		"report from d_1: endpoints [198.51.100.7:41641 [2001:db8::7]:41641]",
		"mail (development, not sent): the login code for lin@example.net is 482917",
		"mail (development, not sent): its login link is http://127.0.0.1:47621/login/link#abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		"certificate pin sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4",
		"alert firing relay-full/pub: 公共中转站「香港」满了 [logged]",
		"op lan.create from d_1: version 7",
		"backup: VACUUM INTO failed: disk full",
		"cleanup: 1 invites",
	}
	for i := range 1000 {
		b.WriteString(line(today.Add(time.Duration(i)*time.Second), secrets[i%len(secrets)]))
	}
	os.WriteFile(file(today), []byte(b.String()), 0o600)

	var res OpsLog
	if st := cw.get(t, cw.ops, "log", &res); st != 200 || len(res.Lines) != 200 || !res.More || res.Last != res.Lines[199].Seq {
		t.Fatalf("the last 200: %d lines, more %v, last %d", len(res.Lines), res.More, res.Last)
	}
	for _, l := range res.Lines {
		for _, bad := range []string{"198.51.100.7:", "2001:db8::7]:41641", "482917", "abcdefghijklmnopqrstuvwxyz", "a90009bb"} {
			if strings.Contains(l.Text, bad) {
				t.Fatalf("not masked: %q", l.Text)
			}
		}
	}
	kinds := map[string]string{}
	for _, l := range res.Lines {
		kinds[strings.Fields(l.Text)[0]] = l.Kind
	}
	for first, want := range map[string]string{"web": "login", "http:": "error", "alert": "alert", "op": "op", "backup:": "error", "cleanup:": "other", "mail": "login"} {
		if kinds[first] != want {
			t.Errorf("%s… is %q, want %q", first, kinds[first], want)
		}
	}
	if !slices.ContainsFunc(res.Lines, func(l OpsLine) bool { return strings.Contains(l.Text, "from 203.0.113.9,") }) {
		t.Fatal("the source address of a login is the log's, and stays")
	}

	// Back: 200 at a time, to 2000 in all, today's then yesterday's.
	seen := len(res.Lines)
	first := res.Lines[0].Seq
	for res.More {
		cw.get(t, cw.ops, fmt.Sprintf("log?before=%d", first), &res)
		if len(res.Lines) == 0 || res.Lines[len(res.Lines)-1].Seq >= first {
			t.Fatalf("back from %d: %d lines", first, len(res.Lines))
		}
		seen += len(res.Lines)
		first = res.Lines[0].Seq
	}
	if seen != opsLogMost || !strings.Contains(res.Lines[0].Text, "device d_500 ") {
		t.Fatalf("back to %d lines, the first %q", seen, res.Lines[0].Text)
	}

	// A kind alone.
	cw.get(t, cw.ops, "log?kind=alert", &res)
	if len(res.Lines) != 100 || slices.ContainsFunc(res.Lines, func(l OpsLine) bool { return l.Kind != "alert" }) {
		t.Fatalf("alerts: %d", len(res.Lines))
	}

	// After: only what came since; the seqs before stay as they were.
	cw.get(t, cw.ops, "log", &res)
	last := res.Last
	f, _ := os.OpenFile(file(today), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line(now, "operator kittors@deneb.test: 删了阿杰的「nas」 [device.delete]"))
	f.WriteString(strings.TrimSuffix(line(now, "half a line, still being writ"), "\n"))
	f.Close()
	var after OpsLog
	cw.get(t, cw.ops, fmt.Sprintf("log?after=%d", last), &after)
	if len(after.Lines) != 1 || after.Lines[0].Kind != "op" || after.Last != after.Lines[0].Seq || after.Last <= last {
		t.Fatalf("after: %+v", after)
	}
	newest := after.Last
	cw.get(t, cw.ops, fmt.Sprintf("log?after=%d&kind=alert", newest), &after)
	if len(after.Lines) != 0 || after.Last != newest {
		t.Fatalf("after, alerts: %+v", after)
	}

	// No files: a note, no lines.
	s.cfg.LogDir = ""
	cw.get(t, cw.ops, "log", &res)
	if len(res.Lines) != 0 || res.Note == "" {
		t.Fatalf("no files: %+v", res)
	}
}

// TestOpsRecordsKeepAYear: the daily cleanup drops the record's lines
// older than a year, and no other.
func TestOpsRecordsKeepAYear(t *testing.T) {
	cw := newConsoleWorld(t)
	s := cw.s
	now := cw.clk.now()
	for _, age := range []time.Duration{366 * 24 * time.Hour, 364 * 24 * time.Hour} {
		s.db.Exec(`INSERT INTO ops_records (at, by_name, act, text) VALUES (?, 'kittors', 'x', ?)`, ms(now.Add(-age)), fmt.Sprint(age))
	}
	c, err := s.cleanup()
	if err != nil || c.records != 1 {
		t.Fatalf("cleanup: %+v, %v", c, err)
	}
	if n := len(cw.records(t)); n != 2 { // the 364 days' and newConsoleWorld's
		t.Fatalf("left: %d", n)
	}
}
