package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestServiceFiles: what each service manager is given.
func TestServiceFiles(t *testing.T) {
	s := spec{bin: "/usr/local/lib/deneb-relay/deneb-relay", data: "/var/lib/deneb-relay", user: "deneb-relay"}
	low := s
	low.lowPort = true
	if u := systemdUnit(low, scopeSystem); !strings.Contains(u, "AmbientCapabilities=CAP_NET_BIND_SERVICE") {
		t.Errorf("a port below 1024 without the capability:\n%s", u)
	}
	spaced := spec{bin: "/home/a b/.local/lib/deneb-relay/deneb-relay", data: "/home/a b/.local/share/deneb-relay"}
	if u := systemdUnit(spaced, scopeUser); !strings.Contains(u, `ExecStart="/home/a b/.local/lib/deneb-relay/deneb-relay" run -data "/home/a b/.local/share/deneb-relay"`) || strings.Contains(u, "ProtectSystem") {
		t.Errorf("a user unit with a space in the path:\n%s", u)
	}
	logged := s
	logged.log = "/var/lib/deneb-relay/relay.log"
	rc := openrcScript(logged)
	for _, want := range []string{"#!/sbin/openrc-run", "command=/usr/local/lib/deneb-relay/deneb-relay", "command_args='run -data /var/lib/deneb-relay -log /var/lib/deneb-relay/relay.log'", "command_user=deneb-relay:deneb-relay", "supervisor=supervise-daemon", "need net"} {
		if !strings.Contains(rc, want) {
			t.Errorf("no %q in the OpenRC script:\n%s", want, rc)
		}
	}
	pd := procdScript(s)
	for _, want := range []string{"#!/bin/sh /etc/rc.common", "USE_PROCD=1", "procd_set_param command /usr/local/lib/deneb-relay/deneb-relay run -data /var/lib/deneb-relay", "procd_set_param user deneb-relay", "procd_set_param respawn 600 5 10"} {
		if !strings.Contains(pd, want) {
			t.Errorf("no %q in the procd script:\n%s", want, pd)
		}
	}
}

// TestCronLines: install's lines replace its earlier ones and nothing
// else; taken out, the crontab is as it was.
func TestCronLines(t *testing.T) {
	lines := cronLines(spec{bin: "/opt/x%y/deneb-relay", data: "/d"})
	if !strings.HasPrefix(lines[0], `@reboot /opt/x\%y/deneb-relay run -data /d -if-stopped`) || !strings.HasSuffix(lines[1], cronMark) {
		t.Errorf("lines: %q", lines)
	}
	mine := "MAILTO=me\n0 3 * * * backup\n"
	once := withCronLines(mine, lines)
	if twice := withCronLines(once, lines); twice != once {
		t.Errorf("written twice:\n%s\nnot\n%s", twice, once)
	}
	if back := withCronLines(once, nil); back != mine {
		t.Errorf("taken out:\n%q", back)
	}
	if withCronLines("", nil) != "" {
		t.Error("an empty crontab")
	}
}

// TestDetectInit: what keeps the relay running, as root and not.
func TestDetectInit(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*fakeHost)
		root  bool
		want  string
	}{
		{"systemd", func(f *fakeHost) { f.systemd() }, true, initSystemd},
		{"openrc", func(f *fakeHost) { f.file("/sbin/openrc-run", ""); f.file("/run/openrc/softlevel", "default") }, true, initOpenRC},
		{"openrc installed, not running", func(f *fakeHost) { f.file("/sbin/openrc-run", "") }, true, initNone},
		{"procd", func(f *fakeHost) { f.file("/sbin/procd", ""); f.file("/etc/rc.common", "") }, true, initProcd},
		{"cron", func(f *fakeHost) { f.cron() }, true, initCron},
		{"nothing", func(*fakeHost) {}, true, initNone},
		{"user systemd", func(f *fakeHost) { f.systemd() }, false, initSystemd},
		{"user, no session bus", func(f *fakeHost) { f.systemd(); f.cron(); f.fails["systemctl --user"] = true }, false, initCron},
	} {
		f := newFakeHost(t, c.root)
		c.setup(f)
		scope := scopeSystem
		if !c.root {
			scope = scopeUser
		}
		if got, _ := f.detectInit(scope); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// TestFirewalls: which firewall keeps the ports out, and the commands
// that let them in and take that back.
func TestFirewalls(t *testing.T) {
	f := newFakeHost(t, true)
	f.have["firewall-cmd"] = true
	f.replies["firewall-cmd --state"] = "running\n"
	fw := f.detectFirewall(47445, 47478)
	if fw == nil || fw.kind != "firewalld" || strings.Join(fw.open[1], " ") != "firewall-cmd --permanent --add-port=47445/tcp --add-port=47478/udp" || strings.Join(fw.undo[0], " ") != "firewall-cmd --remove-port=47445/tcp --remove-port=47478/udp" {
		t.Errorf("firewalld: %+v", fw)
	}

	g := newFakeHost(t, true)
	g.have["nft"] = true
	g.replies["nft list ruleset"] = "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t\tct state established accept\n\t}\n}\n"
	g.replies["nft -a list chain inet filter input"] = "table inet filter {\n\tchain input {\n\t\ttcp dport 47445 accept comment \"deneb-relay\" # handle 7\n\t\tct state established accept # handle 2\n\t}\n}\n"
	fw = g.detectFirewall(47445, 47478)
	if fw == nil || fw.kind != "nftables" || strings.Join(fw.open[0], " ") != `nft insert rule inet filter input tcp dport 47445 accept comment "deneb-relay"` || fw.keep == "" {
		t.Fatalf("nftables: %+v", fw)
	}
	if err := g.apply(fw.undo); err != nil || !g.ran("nft delete rule inet filter input handle 7") || g.ran("nft delete rule inet filter input handle 2") {
		t.Errorf("nftables undo: %v %q", err, g.cmds)
	}
	if lines := commandLines(fw.open, false); !strings.HasPrefix(lines[0], "sudo nft insert rule") || len(commandLines(fw.undo, true)) != 0 {
		t.Errorf("command lines: %q", lines)
	}

	h := newFakeHost(t, true)
	h.have["iptables"], h.have["ip6tables"], h.have["netfilter-persistent"] = true, true, true
	h.replies["iptables -S INPUT"] = "-P INPUT ACCEPT\n-A INPUT -i lo -j ACCEPT\n-A INPUT -j REJECT --reject-with icmp-host-prohibited\n"
	h.replies["ip6tables -S INPUT"] = "-P INPUT ACCEPT\n"
	fw = h.detectFirewall(47445, 47478)
	if fw == nil || fw.kind != "iptables" || len(fw.open) != 3 || strings.Join(fw.open[0], " ") != "iptables -I INPUT -p tcp --dport 47445 -j ACCEPT" || strings.Join(fw.open[2], " ") != "netfilter-persistent save" {
		t.Errorf("iptables: %+v", fw)
	}

	none := newFakeHost(t, true)
	none.have["iptables"] = true
	none.replies["iptables -S INPUT"] = "-P INPUT ACCEPT\n"
	if fw := none.detectFirewall(47445, 47478); fw != nil {
		t.Errorf("an open firewall: %+v", fw)
	}
}

// TestPublicAddrs: which addresses devices can reach the machine at.
func TestPublicAddrs(t *testing.T) {
	for a, want := range map[string]bool{
		"203.0.113.7": true, "8.8.8.8": true, "10.1.2.3": false, "192.168.1.5": false, "172.20.0.1": false,
		"100.101.102.103": false, "169.254.1.1": false, "127.0.0.1": false, "198.18.0.9": false, "240.0.0.1": false,
		"2001:db8::7": true, "fd7a:115c:a1e0::1": false, "fe80::1": false, "2002:c000:204::1": false, "2001:0:4136:e378::1": false, "::1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(a)); got != want {
			t.Errorf("%s public: %v, want %v", a, got, want)
		}
	}
	v4, v6 := pickPublic([]ifaceAddr{
		{"tailscale0", netip.MustParseAddr("203.0.113.99"), true},
		{"eth1", netip.MustParseAddr("198.51.100.4"), true},
		{"eth0", netip.MustParseAddr("203.0.113.7"), true},
		{"eth0", netip.MustParseAddr("2001:db8::abcd"), false},
		{"eth0", netip.MustParseAddr("2001:db8::7"), true},
		{"eth0", netip.MustParseAddr("10.0.0.2"), true},
	}, "eth0")
	if v4.String() != "203.0.113.7" || v6.String() != "2001:db8::7" {
		t.Errorf("picked %s %s", v4, v6)
	}
	local := localOf([]ifaceAddr{
		{"eth0", netip.MustParseAddr("10.0.0.2"), true}, {"docker0", netip.MustParseAddr("172.17.0.1"), true},
		{"eth0", netip.MustParseAddr("fe80::1"), true}, {"eth0", netip.MustParseAddr("203.0.113.7"), true},
	})
	if fmt.Sprint(local) != "[10.0.0.2 203.0.113.7]" {
		t.Errorf("local: %v", local)
	}
}

// TestPickPorts: the ports asked for, else the next free pair.
func TestPickPorts(t *testing.T) {
	taken := map[int]bool{47445: true, 47479: true}
	free := func(p int) bool { return !taken[p] }
	if tcp, udp, ok := pickPorts(47445, 47478, free, free); !ok || tcp != 47447 || udp != 47480 {
		t.Errorf("picked %d %d %v", tcp, udp, ok)
	}
	if _, _, ok := pickPorts(65535, 65535, func(int) bool { return false }, free); ok {
		t.Error("picked a pair past 65535")
	}
}

// TestUpdateKeyIsTheWindows: the relay's updates are checked with the key
// the window's are (cmd/deneb/mygo.json); one release signs both.
func TestUpdateKeyIsTheWindows(t *testing.T) {
	b, err := os.ReadFile("../deneb/mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Updates.PublicKey != updatePublicKey || len(updateKey()) != ed25519.PublicKeySize {
		t.Fatalf("mygo.json's key %q, the relay's %q (%v)", c.Updates.PublicKey, updatePublicKey, err)
	}
}

// testRelease is a release on a test download site, signed with a key of
// the test's, which it puts in place of the real one.
type testRelease struct {
	srv     *httptest.Server
	program []byte
	m       manifest
}

func newTestRelease(t *testing.T, version string, program []byte) *testRelease {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := updatePublicKey
	updatePublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { updatePublicKey = old })
	sum := sha256.Sum256(program)
	r := &testRelease{program: program, m: manifest{Version: version, Files: map[string]manifestFile{
		platform(): {URL: version + "/deneb-relay-" + platform(), Size: int64(len(program)), SHA256: fmt.Sprintf("%x", sum),
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))},
	}}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/download/relay/relay.json":
			json.NewEncoder(w).Encode(r.m)
		case "/download/relay/" + version + "/deneb-relay-" + platform():
			w.Write(r.program)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	t.Setenv("DENEB_RELAY_FEED", r.srv.URL+"/download/relay/")
	return r
}

// fakeProgram is a program that says it is version.
func fakeProgram(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + "\n")
}

// TestUpdate: a newer release is downloaded, checked against its
// signature, run to see it is that release, and put in place, the old
// one kept beside it. A file altered on the way, or an older release
// renamed as the new one, is not taken.
func TestUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake program is a shell script")
	}
	r := newTestRelease(t, "0.2.0", fakeProgram("0.2.0"))
	dir := t.TempDir()
	bin := filepath.Join(dir, "deneb-relay")
	os.WriteFile(bin, fakeProgram("0.1.9"), 0o755)
	ctx := context.Background()
	m, base, err := latest(ctx)
	if err != nil || m.Version != "0.2.0" {
		t.Fatalf("latest: %+v, %v", m, err)
	}
	next, err := fetchProgram(ctx, m, base, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRuns(ctx, next, m.Version); err != nil {
		t.Fatal(err)
	}
	if err := replaceProgram(bin, next); err != nil {
		t.Fatal(err)
	}
	if v := installedVersion(ctx, bin); v != "0.2.0" {
		t.Errorf("after the update: %q", v)
	}
	if v := installedVersion(ctx, bin+".prev"); v != "0.1.9" {
		t.Errorf("the one kept: %q", v)
	}

	// Altered on the way: the signature no longer matches.
	r.program = fakeProgram("0.2.1")
	if _, err := fetchProgram(ctx, m, base, dir); err == nil {
		t.Error("an altered program was taken")
	}
	// An old release, signed, named the new one: it says what it is.
	old := fakeProgram("0.1.5")
	r2 := newTestRelease(t, "0.3.0", old)
	m2, base2, _ := latest(ctx)
	next2, err := fetchProgram(ctx, m2, base2, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRuns(ctx, next2, m2.Version); err == nil || !strings.Contains(err.Error(), "0.1.5") {
		t.Errorf("an old release renamed: %v", err)
	}
	_ = r2
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"0.1.10", "0.1.9", 1}, {"0.1.9", "0.1.9", 0}, {"0.2.0-beta.1", "0.2.0", -1}, {"v1.0.0", "0.9.9", 1}, {"0.1.9+abc", "0.1.9", 0}} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%s, %s) = %d", c.a, c.b, got)
		}
	}
}

// TestDockerInspect: a relay container's settings, as deploy.sh ran it.
func TestDockerInspect(t *testing.T) {
	js := `[{"Args":["-control","https://203.0.113.1:47443","-control-pin","sha256:ab","-data","/data","-listen",":47445","-stun",":47478","-ipv4","203.0.113.9","-region","香港"],
		"State":{"Running":true},"Mounts":[{"Source":"/opt/deneb-relay/data","Destination":"/data"}]}]`
	d, err := parseDockerInspect(js)
	if err != nil || !d.running || d.data != "/opt/deneb-relay/data" || d.settings.Control != "https://203.0.113.1:47443" || d.settings.ControlPin != "sha256:ab" || d.settings.IPv4 != "203.0.113.9" || d.settings.Region != "香港" {
		t.Fatalf("%+v, %v", d, err)
	}
	if _, err := parseDockerInspect(`[{"Args":[],"Mounts":[]}]`); err == nil {
		t.Error("a container whose data is not on the host")
	}
}

// TestRollingLog: past its size the log becomes .1, .1 becomes .2, and
// no more than two old ones are kept.
func TestRollingLog(t *testing.T) {
	old := logMax
	logMax = 100
	t.Cleanup(func() { logMax = old })
	path := filepath.Join(t.TempDir(), "relay.log")
	l, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(l, "%02d %s\n", i, strings.Repeat("x", 40))
	}
	for _, p := range []string{path, path + ".1", path + ".2"} {
		if fi, err := os.Stat(p); err != nil || fi.Size() > 100 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("a third old log")
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), "08 ") {
		t.Errorf("the current log: %q", b)
	}
}

// TestLockAndStarts: one relay on a data directory; its starts within
// the hour are counted.
func TestLockAndStarts(t *testing.T) {
	dir := t.TempDir()
	release, ok, err := holdLock(dir)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if runtime.GOOS != "windows" {
		if !locked(dir) {
			t.Error("not seen as locked")
		}
		if _, ok, _ := holdLock(dir); ok {
			t.Error("locked twice")
		}
	}
	release()
	if locked(dir) {
		t.Error("still locked")
	}
	now := time.Now()
	for i, at := range []time.Time{now.Add(-2 * time.Hour), now.Add(-30 * time.Minute), now} {
		got := noteStart(dir, at)
		if want := []int{0, 0, 1}[i]; got != want {
			t.Errorf("start %d: %d before it, want %d", i, got, want)
		}
	}
}

// TestShellQuote: what goes into a crontab or an init script.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/x": "/usr/bin/x", "a b": "'a b'", "it's": `'it'\''s'`, "": "''", "-data": "-data"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
