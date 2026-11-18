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

	// The same addresses in another order: nothing changes, no new map.
	dns.set("stun.cloudflare.com", "2001:db8::5", "198.51.100.30", "198.51.100.10")
	v = s.Version()
	s.refreshSTUN(t.Context())
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) || s.Version() != v {
		t.Fatalf("map %v at version %d (was %d) when the name gave the same address again", got, s.Version(), v)
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
	v = s.Version()
	s.refreshSTUN(t.Context())
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, want) || s.Version() != v {
		t.Fatalf("map %v at version %d (was %d) after a failed lookup", got, s.Version(), v)
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
	if _, err := s.opsAddSTUN(nil, "stun.example.com"); err == nil || !strings.Contains(err.Error(), "looks no names up") {
		t.Fatalf("adding by name: %v", err)
	}
	if _, err := s.opsAddSTUN(nil, "192.0.2.1:3478"); err != nil {
		t.Fatal(err)
	}
	if got := stunOf(netMapOf(s, e.id)); !slices.Equal(got, []string{"192.0.2.1:3478 192.0.2.1 invalid IP"}) {
		t.Fatalf("map %v", got)
	}
}
