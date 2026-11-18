package control

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// fakeDNS answers lookups from a table, as the server's resolver would.
type fakeDNS struct {
	mu    sync.Mutex
	names map[string][]netip.Addr
	fail  map[string]error
	asked []string
}

func (d *fakeDNS) set(host string, addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var as []netip.Addr
	for _, a := range addrs {
		as = append(as, netip.MustParseAddr(a))
	}
	d.names[host] = as
	delete(d.fail, host)
}

func (d *fakeDNS) failing(host string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail[host] = err
}

func (d *fakeDNS) lookup(ctx context.Context, network, host string) ([]netip.Addr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked = append(d.asked, network+" "+host)
	if network != "ip" {
		return nil, fmt.Errorf("asked for %s", network)
	}
	if err := d.fail[host]; err != nil {
		return nil, err
	}
	as, ok := d.names[host]
	if !ok {
		return nil, errors.New("lookup " + host + ": no such host")
	}
	return slices.Clone(as), nil
}

// logLines keeps what a server logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}

func (l *logLines) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.ContainsFunc(l.lines, func(s string) bool { return strings.Contains(s, sub) })
}

// stunTestServer is a development server on dir that looks names up in dns
// and logs into lines.
func stunTestServer(t *testing.T, dir string, dns *fakeDNS, lines *logLines) (*Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: dir, PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true, Seed: DevSeed(),
		LookupIP: dns.lookup, Logf: lines.logf})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
		s.Close()
	})
	return s, ts
}

func stunOf(nm *proto.NetMap) []string {
	var out []string
	for _, s := range nm.STUN {
		out = append(out, fmt.Sprintf("%s %v %v", s, s.IPv4, s.IPv6))
	}
	return out
}

// opsLines is the record, oldest first: who, what, about what, the text.
func opsLines(t *testing.T, s *Server) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT by_name, act, target, text FROM ops_records ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var by, act, target, text string
		if err := rows.Scan(&by, &act, &target, &text); err != nil {
			t.Fatal(err)
		}
		out = append(out, by+" | "+act+" | "+target+" | "+text)
	}
	return out
}

// Cloudflare's STUN is there from the start, looked up where the server
// runs; devices get the addresses found, which stay put while the name
// still gives them, and the old ones when a lookup fails.
func TestSTUNLookups(t *testing.T) {
	dns := &fakeDNS{names: map[string][]netip.Addr{}, fail: map[string]error{}}
	var lines logLines
	dir := t.TempDir()
	s, ts := stunTestServer(t, dir, dns, &lines)
	e := newEngine(t, ts.URL, "mac", proto.MacOS)
	e.login("kittors@deneb.test")

	// Not looked up yet: on the list, not in the map.
	if got := stunOf(netMapOf(s, e.id)); got != nil {
		t.Fatalf("before a lookup the map has %v", got)
	}
	if l := s.adminSTUN(); len(l) != 1 || l[0].Server != "stun.cloudflare.com:3478" || l[0].IPv4.IsValid() || !l[0].LookedUp.IsZero() {
		t.Fatalf("the list to begin with: %+v", l)
	}
	if !slices.Contains(e.view().Features, proto.FeatureSTUN) {
		t.Fatal("the server does not say it has STUN servers")
	}

	// The least of each kind.
	dns.set("stun.cloudflare.com", "198.51.100.20", "198.51.100.10", "2001:db8::5")
	v := s.Version()
	s.refreshSTUN(t.Context())
	want := []string{"stun.cloudflare.com:3478 198.51.100.10 2001:db8::5"}
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map after the lookup %v, want %v", got, want)
	}
	if s.Version() != v+1 {
		t.Fatalf("version %d after the lookup, was %d: want one new map", s.Version(), v)
	}
	if !lines.has("stun: stun.cloudflare.com:3478 is at 198.51.100.10 and 2001:db8::5 now, was no address") {
		t.Fatalf("log: %q", lines.lines)
	}

	// The same addresses in another order: the map stays as it was (the
	// version moves for the operators' lookup times, TestSTUNPublishes).
	dns.set("stun.cloudflare.com", "2001:db8::5", "198.51.100.30", "198.51.100.10")
	s.refreshSTUN(t.Context())
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map %v when the name gave the same address again", got)
	}

	// The address gone: the least of the new ones; no IPv6 any more.
	dns.set("stun.cloudflare.com", "198.51.100.40", "198.51.100.30")
	s.refreshSTUN(t.Context())
	want = []string{"stun.cloudflare.com:3478 198.51.100.30 invalid IP"}
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map after the address went %v, want %v", got, want)
	}

	// A failed lookup keeps the address, and the list says what happened.
	dns.failing("stun.cloudflare.com", errors.New("i/o timeout"))
	s.refreshSTUN(t.Context())
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map %v after a failed lookup", got)
	}
	if l := s.adminSTUN(); l[0].Error != "i/o timeout" || l[0].LookedUp.IsZero() || l[0].IPv4 != netip.MustParseAddr("198.51.100.30") {
		t.Fatalf("the list after a failed lookup: %+v", l)
	}
	if !lines.has("stun: looking stun.cloudflare.com:3478 up: i/o timeout; it stays at 198.51.100.30") {
		t.Fatalf("log: %q", lines.lines)
	}

	// The addresses are kept across a restart: devices have them before
	// the first lookup.
	s.Close()
	s2, err := New(Config{DataDir: dir, PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true, Seed: DevSeed(),
		LookupIP: dns.lookup, Logf: lines.logf})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := stunOf(netMapOf(s2, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map after a restart %v, want %v", got, want)
	}

	// Lookups are only ever for "ip": both kinds at once.
	for _, a := range dns.asked {
		if a != "ip stun.cloudflare.com" {
			t.Fatalf("looked up %q", a)
		}
	}
	// Nothing an operator did: nothing in the record.
	if r := opsLines(t, s2); len(r) != 0 {
		t.Fatalf("record %q", r)
	}
}

// Operators add and remove STUN servers with deneb-control stun: each goes
// to the devices at once, with a line of the record; one whose name cannot
// be looked up is not added.
func TestSTUNAdminCommands(t *testing.T) {
	dns := &fakeDNS{names: map[string][]netip.Addr{}, fail: map[string]error{}}
	var lines logLines
	s, ts := stunTestServer(t, t.TempDir(), dns, &lines)
	sock := serveAdmin(t, s)
	e := newEngine(t, ts.URL, "mac", proto.MacOS)
	e.login("kittors@deneb.test")
	dns.set("stun.cloudflare.com", "198.51.100.10")
	s.refreshSTUN(t.Context())

	dns.set("stun.example.com", "192.0.2.7", "2001:db8::7")
	l, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: " STUN.Example.com.:19302 "})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 || l[1].Server != "stun.example.com:19302" || l[1].IPv4 != netip.MustParseAddr("192.0.2.7") || l[1].Error != "" || l[1].AddedAt.IsZero() {
		t.Fatalf("list after adding: %+v", l)
	}
	want := []string{"stun.cloudflare.com:3478 198.51.100.10 invalid IP", "stun.example.com:19302 192.0.2.7 2001:db8::7"}
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map after adding %v, want %v", got, want)
	}
	if !lines.has("operator (the admin socket): 加了查外网地址的服务 stun.example.com:19302 [stun.add]") {
		t.Fatalf("log: %q", lines.lines)
	}

	// The port is 3478 unless it says; an address needs no lookup.
	dns.set("stun2.example.com", "192.0.2.8")
	if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: "stun2.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: "[2001:db8::9]:3479"}); err != nil {
		t.Fatal(err)
	}
	want = append(want, "stun2.example.com:3478 192.0.2.8 invalid IP", "[2001:db8::9]:3479 invalid IP 2001:db8::9")
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) {
		t.Fatalf("map %v, want %v", got, want)
	}
	if slices.Contains(dns.asked, "ip 2001:db8::9") {
		t.Fatal("an address was looked up")
	}

	// Refused, and nothing changes.
	v := s.Version()
	for spec, msg := range map[string]string{
		"stun.example.com:19302": "已经有 stun.example.com:19302 了",
		"stun.nowhere.example":   "查不到 stun.nowhere.example 的地址，没有加",
		"stun":                   "写成主机名",
		"stun_1.example.com":     "写成主机名",
		"stun.example.com:0":     "写成主机名",
		"stun.example.com:70000": "写成主机名",
		"1.2.3":                  "写成主机名",
		"0.0.0.0":                "写成主机名",
		"[fe80::1%en0]:3478":     "写成主机名",
		"":                       "加一个，或者去掉一个",
	} {
		if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: spec}); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("adding %q: %v, want %q", spec, err, msg)
		}
	}
	if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Remove: "stun.nowhere.example"}); err == nil || !strings.Contains(err.Error(), "没有 stun.nowhere.example:3478") {
		t.Errorf("removing one not there: %v", err)
	}
	if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: "a.example.com", Remove: "b.example.com"}); err == nil {
		t.Error("adding and removing at once")
	}
	if s.Version() != v {
		t.Fatalf("version %d after refusals, was %d", s.Version(), v)
	}

	// Eight at most.
	for i := 3; i < maxSTUN-1; i++ {
		if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: fmt.Sprintf("192.0.2.%d", 100+i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Add: "192.0.2.200"}); err == nil || !strings.Contains(err.Error(), "最多 8 个") {
		t.Fatalf("a ninth: %v", err)
	}

	// Removing Cloudflare's, by its name alone: the devices stop asking it.
	l, err = AdminSetSTUN(sock, AdminSTUNRequest{Remove: "stun.cloudflare.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != maxSTUN-1 || l[0].Server != "stun.example.com:19302" {
		t.Fatalf("list after removing: %+v", l)
	}
	if got := stunOf(netMapOf(s, e.id)); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "stun.cloudflare.com") }) {
		t.Fatalf("map after removing %v", got)
	}

	// The record has the changes, by the command on the server, and none
	// of the refusals.
	r := opsLines(t, s)
	if len(r) != 3+(maxSTUN-1-3)+1 { // three added, the ones up to eight, one removed
		t.Fatalf("record %q", r)
	}
	if r[0] != "服务器上的命令 | stun.add | stun.example.com:19302 | 加了查外网地址的服务 stun.example.com:19302" ||
		r[len(r)-1] != "服务器上的命令 | stun.remove | stun.cloudflare.com:3478 | 去掉了查外网地址的服务 stun.cloudflare.com:3478" {
		t.Fatalf("record %q", r)
	}

	// Taking them all off is allowed: devices ask the relays only.
	for _, a := range l {
		if _, err := AdminSetSTUN(sock, AdminSTUNRequest{Remove: a.Server}); err != nil {
			t.Fatal(err)
		}
	}
	if nm := netMapOf(s, e.id); nm.STUN != nil {
		t.Fatalf("map with none: %v", stunOf(nm))
	}
	if l, err := AdminSTUNServers(sock); err != nil || len(l) != 0 {
		t.Fatalf("list with none: %v, %v", l, err)
	}
}

// A server that looks nothing up (no LookupIP) keeps Cloudflare's off the
// maps, and says so when one is added by name.
func TestSTUNWithoutLookups(t *testing.T) {
	s, ts := newTestServer(t, "")
	e := newEngine(t, ts.URL, "mac", proto.MacOS)
	e.login("kittors@deneb.test")
	s.refreshSTUN(t.Context())
	if nm := netMapOf(s, e.id); nm.STUN != nil {
		t.Fatalf("map %v", stunOf(nm))
	}
	// Said plainly; the lookup's own words are the log's.
	if _, err := s.opsAddSTUN(nil, "stun.example.com"); err == nil || !strings.HasSuffix(err.Error(), ": 查不到 stun.example.com 的地址，没有加") {
		t.Fatalf("adding by name: %v", err)
	}
	if _, err := s.opsAddSTUN(nil, "192.0.2.1:3478"); err != nil {
		t.Fatal(err)
	}
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, []string{"192.0.2.1:3478 192.0.2.1 invalid IP"}) {
		t.Fatalf("map %v", got)
	}
}

// opsServer is a development server whose kittors is an operator, with a
// device of his and one of 阿杰's logged in, and kittors in a browser.
func opsServer(t *testing.T, dns *fakeDNS, lines *logLines) (*Server, *httptest.Server, *engine, *engine, *browser2) {
	t.Helper()
	s, ts := stunTestServer(t, t.TempDir(), dns, lines)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	jdesk := newEngine(t, ts.URL, "jdesk", proto.Windows)
	jdesk.login("jie@deneb.test")
	if err := s.opsSetOperator(nil, s.state.Load().w.byEmail["kittors@deneb.test"].ID, true); err != nil {
		t.Fatal(err)
	}
	b := newWebBrowser(t, ts.URL)
	b.login("kittors@deneb.test", DevCode)
	return s, ts, mac, jdesk, b
}

func (b *browser2) view() *proto.UserView {
	b.t.Helper()
	var v proto.UserView
	if st := b.do("GET", proto.PathWebUserView, nil, &v, nil); st != 200 {
		b.t.Fatalf("view: %d", st)
	}
	return &v
}

func opErr(t *testing.T, b *browser2, op proto.OpType, a proto.OpArgs) (int, string) {
	t.Helper()
	b.n++
	var body proto.ErrorBody
	st := b.do("POST", proto.PathWebOps, proto.Op{ID: fmt.Sprintf("web-%d", b.n), Op: op, Args: a}, &body, nil)
	if body.Error == nil {
		return st, ""
	}
	return st, body.Error.Message
}

// The ops page's list (FeatureSTUN): an operator's view has every STUN
// server with its addresses and how its last lookup here went; no one
// else's view has any.
func TestSTUNOperatorView(t *testing.T) {
	dns := &fakeDNS{names: map[string][]netip.Addr{}, fail: map[string]error{}}
	var lines logLines
	s, _, _, jdesk, b := opsServer(t, dns, &lines)

	// Before a lookup: listed, no address, never looked up.
	ov := b.view().Operator
	if ov == nil || len(ov.STUN) != 1 || ov.STUN[0].Server != "stun.cloudflare.com:3478" || ov.STUN[0].IPv4.IsValid() || !ov.STUN[0].LookedUp.IsZero() {
		t.Fatalf("operator's list to begin with: %+v", ov)
	}
	// After one: the address and when.
	dns.set("stun.cloudflare.com", "198.51.100.10", "2001:db8::10")
	s.refreshSTUN(t.Context())
	got := b.view().Operator.STUN[0]
	if got.IPv4 != netip.MustParseAddr("198.51.100.10") || got.IPv6 != netip.MustParseAddr("2001:db8::10") || got.LookedUp.IsZero() || got.LookupError != "" {
		t.Fatalf("after a lookup: %+v", got)
	}
	// One that failed: the address kept, what the lookup said.
	dns.failing("stun.cloudflare.com", errors.New("i/o timeout"))
	s.refreshSTUN(t.Context())
	if got := b.view().Operator.STUN[0]; got.IPv4 != netip.MustParseAddr("198.51.100.10") || got.LookupError != "i/o timeout" {
		t.Fatalf("after a failed lookup: %+v", got)
	}
	if v := jdesk.view(); v.Operator != nil {
		t.Fatalf("someone else sees %+v", v.Operator)
	}
}

// Operators add and remove STUN servers on the ops page, as ops: the name
// is looked up before the op, outside the server's lock, and only for an
// operator; a retry gets the first answer without a second lookup; what
// goes wrong is said plainly; the record names the operator.
func TestSTUNOps(t *testing.T) {
	dns := &fakeDNS{names: map[string][]netip.Addr{}, fail: map[string]error{}}
	var lines logLines
	s, ts, mac, jdesk, b := opsServer(t, dns, &lines)
	dns.set("stun.example.com", "192.0.2.7")

	// Someone who is not an operator: refused, and nothing looked up.
	if _, e := jdesk.op(proto.OpSTUNAdd, proto.OpArgs{STUN: "stun.example.com"}); e == nil || e.Code != proto.ErrForbidden {
		t.Fatalf("someone else adding: %v", e)
	}
	jb := newWebBrowser(t, ts.URL)
	jb.login("jie@deneb.test", DevCode)
	if st, msg := opErr(t, jb, proto.OpSTUNAdd, proto.OpArgs{STUN: "stun.example.com"}); st != 403 || !strings.Contains(msg, "只有部署管理员") {
		t.Fatalf("someone else adding from a browser: %d %q", st, msg)
	}
	if st, _ := opErr(t, jb, proto.OpSTUNRemove, proto.OpArgs{STUN: "stun.cloudflare.com"}); st != 403 {
		t.Fatalf("someone else removing: %d", st)
	}
	if len(dns.asked) != 0 {
		t.Fatalf("looked up for someone else: %q", dns.asked)
	}

	// The operator adds one; a retry with the same id is the same answer.
	op := proto.Op{ID: "add-1", Op: proto.OpSTUNAdd, Args: proto.OpArgs{STUN: "stun.example.com"}}
	var r1, r2 proto.OpResult
	if st := b.do("POST", proto.PathWebOps, op, &r1, nil); st != 200 || r1.Version == 0 {
		t.Fatalf("adding: %d %+v", st, r1)
	}
	if st := b.do("POST", proto.PathWebOps, op, &r2, nil); st != 200 || r2 != r1 {
		t.Fatalf("the retry: %d %+v, first %+v", st, r2, r1)
	}
	if n := len(dns.asked); n != 1 {
		t.Fatalf("looked up %d times: %q", n, dns.asked)
	}
	if got := stunOf(netMapOf(s, mac.id)); !slices.Contains(got, "stun.example.com:3478 192.0.2.7 invalid IP") {
		t.Fatalf("map after adding: %v", got)
	}
	if l := b.view().Operator.STUN; len(l) != 2 || l[1].Server != "stun.example.com:3478" || l[1].LookedUp.IsZero() {
		t.Fatalf("list after adding: %+v", l)
	}

	// Said plainly; the lookup's own words in the log.
	for spec, want := range map[string]string{
		"stun.nowhere.example": "查不到 stun.nowhere.example 的地址，没有加",
		"stun.example.com":     "已经有 stun.example.com:3478 了",
		"stun":                 "写成主机名，或者主机名:端口，比如 stun.cloudflare.com:3478",
	} {
		if st, msg := opErr(t, b, proto.OpSTUNAdd, proto.OpArgs{STUN: spec}); st/100 != 4 || msg != want {
			t.Errorf("adding %q: %d %q, want %q", spec, st, msg, want)
		}
	}
	if !lines.has("stun: looking stun.nowhere.example:3478 up to add it: lookup stun.nowhere.example: no such host") {
		t.Errorf("log: %q", lines.lines)
	}

	// Removing, and one that is not there.
	if st, r := b.op(proto.OpSTUNRemove, proto.OpArgs{STUN: "stun.example.com"}); st != 200 || r.Version == 0 {
		t.Fatalf("removing: %d", st)
	}
	if st, msg := opErr(t, b, proto.OpSTUNRemove, proto.OpArgs{STUN: "stun.example.com"}); st != 404 || msg != "没有 stun.example.com:3478" {
		t.Fatalf("removing one not there: %d %q", st, msg)
	}
	if got := stunOf(netMapOf(s, mac.id)); slices.ContainsFunc(got, func(x string) bool { return strings.HasPrefix(x, "stun.example.com") }) {
		t.Fatalf("map after removing: %v", got)
	}

	// The record: by kittors, the two changes, none of the refusals.
	r := opsLines(t, s)
	want := []string{
		"kittors | stun.add | stun.example.com:3478 | 加了查外网地址的服务 stun.example.com:3478",
		"kittors | stun.remove | stun.example.com:3478 | 去掉了查外网地址的服务 stun.example.com:3478",
	}
	var stun []string
	for _, l := range r {
		if strings.Contains(l, "| stun.") {
			stun = append(stun, l)
		}
	}
	if !slices.Equal(stun, want) {
		t.Fatalf("record %q, want %q", stun, want)
	}
	if !lines.has("operator kittors@deneb.test: 加了查外网地址的服务 stun.example.com:3478 [stun.add]") {
		t.Fatalf("log: %q", lines.lines)
	}
}

// Each round of lookups publishes, for the operators' "6 分钟前查过"; a
// device whose frame is the same as before is sent nothing, and an
// operator's view is sent anew.
func TestSTUNPublishes(t *testing.T) {
	dns := &fakeDNS{names: map[string][]netip.Addr{}, fail: map[string]error{}}
	var lines logLines
	s, _, mac, jdesk, _ := opsServer(t, dns, &lines)
	dns.set("stun.cloudflare.com", "198.51.100.10")
	s.refreshSTUN(t.Context())

	op := mac.stream(t, true) // kittors's device, with his view: the operator's
	dev := jdesk.stream(t, true)
	op.next(t)
	dev.next(t)
	quiet := func(st *stream, within time.Duration) bool {
		select {
		case <-st.frames:
			return false
		case <-time.After(within):
			return true
		}
	}
	// Let the devices' coming online settle (it is published a moment later).
	for i := 0; !(quiet(op, 600*time.Millisecond) && quiet(dev, 50*time.Millisecond)); i++ {
		if i == 20 {
			t.Fatal("the streams never went quiet")
		}
	}

	// The same address again: the operator gets a frame, the other device none.
	v := s.Version()
	s.refreshSTUN(t.Context())
	if s.Version() != v+1 {
		t.Fatalf("version %d after a round, was %d", s.Version(), v)
	}
	f := op.next(t)
	if f.UserView == nil || f.UserView.Operator == nil || f.UserView.Operator.STUN[0].LookedUp.IsZero() {
		t.Fatalf("the operator's frame: %+v", f.UserView)
	}
	if !quiet(dev, 500*time.Millisecond) {
		t.Fatal("a device was sent a frame for the operators' lookup times")
	}
	// A new address: every device's map has it.
	dns.set("stun.cloudflare.com", "198.51.100.11")
	s.refreshSTUN(t.Context())
	if f := dev.next(t); f.NetMap == nil || len(f.NetMap.STUN) != 1 || f.NetMap.STUN[0].IPv4 != netip.MustParseAddr("198.51.100.11") {
		t.Fatalf("a device's frame for the new address: %+v", f.NetMap)
	}
}
