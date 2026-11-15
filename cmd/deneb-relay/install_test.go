package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control/controltest"
	"github.com/kittors/deneb/proto"
)

// fakeHost is a machine made up in a temporary directory: its files are
// under root, its commands are recorded and answered as a real system's
// would be, and nothing on the machine running the test is touched.
type fakeHost struct {
	*host
	t       *testing.T
	mu      sync.Mutex
	cmds    []string
	out     bytes.Buffer
	have    map[string]bool
	crontab string // the relay user's, or the user's
	// answers to commands, by their first words.
	replies map[string]string
	fails   map[string]bool
	taken   map[string]bool // "tcp 47445"
	spawned []string
	killed  []int
	typed   string // what the person types at the terminal; "" for none
	checks  int
	left    int
}

func newFakeHost(t *testing.T, root bool) *fakeHost {
	f := &fakeHost{t: t, have: map[string]bool{}, replies: map[string]string{}, fails: map[string]bool{}, taken: map[string]bool{}}
	h := &host{root: t.TempDir(), uid: 1000, userName: "kittors", home: "/home/kittors", getenv: func(string) string { return "" }, out: &f.out}
	if root {
		h.uid, h.userName, h.home = 0, "root", "/root"
	}
	h.run = func(name string, args ...string) (string, error) { return f.do("", name, args) }
	h.runIn = func(in, name string, args ...string) (string, error) { return f.do(in, name, args) }
	h.has = func(name string) bool { return f.have[name] }
	h.spawn = func(user, name string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.spawned = append(f.spawned, strings.TrimSpace(user+" "+name+" "+strings.Join(args, " ")))
		return nil
	}
	h.kill = func(pid int) error { f.killed = append(f.killed, pid); return nil }
	h.chown = func(string, int, int) error { return nil }
	h.freeSpace = func(string) (uint64, bool) { return 50 << 30, true }
	h.tcpFree = func(p int) bool { return !f.taken[fmt.Sprint("tcp ", p)] }
	h.udpFree = func(p int) bool { return !f.taken[fmt.Sprint("udp ", p)] }
	h.addrs = func() (netip.Addr, netip.Addr, []netip.Addr) {
		v4 := netip.MustParseAddr("203.0.113.7")
		return v4, netip.Addr{}, []netip.Addr{v4}
	}
	h.progVersion = func(string) string { return shortVersion() }
	h.answers = func(int) bool { return true }
	h.check = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		f.checks++
		return &proto.RelayStatus{State: proto.RelayOK, Probes: []proto.RelayProbe{
			{From: "control", Family: "ipv4", Addr: netip.MustParseAddr("203.0.113.7"), TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK}}}, nil
	}
	h.current = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		return &proto.RelayStatus{State: proto.RelayOK}, nil
	}
	h.leave = func(context.Context, *controlClient) error { f.left++; return nil }
	h.tty = func() (io.ReadWriteCloser, error) {
		if f.typed == "" {
			return nil, errors.New("no terminal")
		}
		return fakeTTY{strings.NewReader(f.typed + "\n")}, nil
	}
	f.host = h
	f.file("/etc/passwd", "root:x:0:0:root:/root:/bin/sh\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n")
	f.file("/usr/sbin/nologin", "")
	return f
}

type fakeTTY struct{ io.Reader }

func (fakeTTY) Write(b []byte) (int, error) { return len(b), nil }
func (fakeTTY) Close() error                { return nil }

// file makes a file of the made-up machine.
func (f *fakeHost) file(p, body string) {
	f.t.Helper()
	full := f.path(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// systemd makes it a machine systemd runs.
func (f *fakeHost) systemd() *fakeHost {
	os.MkdirAll(f.path("/run/systemd/system"), 0o755)
	f.have["systemctl"], f.have["useradd"], f.have["userdel"], f.have["loginctl"], f.have["journalctl"] = true, true, true, true, true
	return f
}

// cron makes it a machine with no service manager, where cron runs.
func (f *fakeHost) cron() *fakeHost {
	f.file("/proc/42/comm", "crond\n")
	f.have["crontab"] = true
	return f
}

func (f *fakeHost) do(input, name string, args []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)
	for prefix := range f.fails {
		if strings.HasPrefix(line, prefix) {
			return "it failed", errors.New("exit status 1")
		}
	}
	switch {
	case name == "useradd":
		u := args[len(args)-1]
		f.mu.Unlock()
		f.file("/etc/passwd", f.read("/etc/passwd")+u+":x:999:999::/var/lib/deneb-relay:/usr/sbin/nologin\n")
		f.mu.Lock()
		return "", nil
	case name == "userdel":
		var keep []string
		for _, l := range strings.Split(f.read("/etc/passwd"), "\n") {
			if l != "" && !strings.HasPrefix(l, args[0]+":") {
				keep = append(keep, l)
			}
		}
		f.mu.Unlock()
		f.file("/etc/passwd", strings.Join(keep, "\n")+"\n")
		f.mu.Lock()
		return "", nil
	case name == "crontab" && slices.Contains(args, "-l"):
		if f.crontab == "" {
			return "no crontab for deneb-relay", errors.New("exit status 1")
		}
		return f.crontab, nil
	case name == "crontab" && slices.Contains(args, "-"):
		f.crontab = input
		return "", nil
	case name == "crontab" && slices.Contains(args, "-r"):
		f.crontab = ""
		return "", nil
	}
	for prefix, out := range f.replies {
		if strings.HasPrefix(line, prefix) {
			return out, nil
		}
	}
	return "", nil
}

// count is how many commands starting with prefix were run.
func (f *fakeHost) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// ran: a command starting with prefix was run.
func (f *fakeHost) ran(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeHost) settings(data string) *settings {
	f.t.Helper()
	s, err := loadSettings(f.path(data))
	if err != nil || s == nil {
		f.t.Fatalf("config.json: %v, %v", s, err)
	}
	return s
}

// joinCode is a fresh join code from the test's control server.
func joinCode(t *testing.T, ctl *controltest.Server, email string) string {
	t.Helper()
	dev := ctl.Login(t, email, "mac-"+fmt.Sprint(len(email)), newNode().pub)
	j := dev.MustOp(proto.OpRelayJoin, proto.OpArgs{}).RelayJoin
	if j == nil {
		t.Fatal("no join code")
	}
	return j.Code
}

// TestInstallSystemd: as root on a systemd machine, install makes its
// user, puts the program and a link in place, registers with the join
// code, writes and starts the unit, and prints a line a step ending in
// the check from outside. Run again, it registers nothing and spends no
// code; uninstall takes everything away and tells the control server.
func TestInstallSystemd(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	code := joinCode(t, ctl, "kittors@deneb.test")
	if rc := f.install(context.Background(), installOpts{join: code, firewall: "ask"}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	out := f.out.String()
	for _, want := range []string{"✓ 系统：Linux", "✓ 端口：TCP 47445、UDP 47478", "✓ 公网地址：IPv4 203.0.113.7", "✓ 已向 Deneb 报到", "✓ 已安装为系统服务（systemd），开机自启", "从外网（Deneb 服务器）连得上 203.0.113.7", "可以用了"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	unit := f.read("/etc/systemd/system/deneb-relay.service")
	for _, want := range []string{"User=deneb-relay", "ExecStart=/usr/local/lib/deneb-relay/deneb-relay run -data /var/lib/deneb-relay\n", "MemoryMax=256M", "CPUQuota=100%", "ReadWritePaths=/var/lib/deneb-relay /usr/local/lib/deneb-relay", "CapabilityBoundingSet=\n", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("no %q in the unit:\n%s", want, unit)
		}
	}
	for _, c := range []string{"useradd -r -U -M -d /var/lib/deneb-relay -s /usr/sbin/nologin deneb-relay", "systemctl daemon-reload", "systemctl enable deneb-relay", "systemctl restart deneb-relay"} {
		if !f.ran(c) {
			t.Errorf("did not run %q: %q", c, f.cmds)
		}
	}
	if l, err := os.Readlink(f.path("/usr/local/bin/deneb-relay")); err != nil || l != "/usr/local/lib/deneb-relay/deneb-relay" {
		t.Errorf("the link: %q, %v", l, err)
	}
	s := f.settings("/var/lib/deneb-relay")
	if s.Control != ctl.URL || s.Listen != ":47445" || s.STUN != ":47478" || s.Install == nil || s.Install.Init != initSystemd || s.Install.Scope != scopeSystem || !s.Install.MadeUser || s.Install.User != relayUser {
		t.Errorf("config.json: %+v %+v", s, s.Install)
	}
	st, err := loadState(f.path("/var/lib/deneb-relay"))
	if err != nil || st == nil || st.Secret == "" {
		t.Fatalf("relay.json: %+v, %v", st, err)
	}

	// Again: the same relay, no code needed, nothing registered twice.
	f.out.Reset()
	if rc := f.install(context.Background(), installOpts{firewall: "ask"}); rc != exitOK {
		t.Fatalf("install again: %d\n%s", rc, f.out.String())
	}
	if out := f.out.String(); !strings.Contains(out, "✓ 中转站“"+st.Name+"”") || !strings.Contains(out, "已重新装好") || strings.Contains(out, "已向 Deneb 报到") {
		t.Errorf("install again:\n%s", out)
	}
	if st2, _ := loadState(f.path("/var/lib/deneb-relay")); st2 == nil || st2.Secret != st.Secret {
		t.Error("install again registered again")
	}

	// Uninstall.
	f.out.Reset()
	if rc := f.uninstall(context.Background(), false); rc != exitOK {
		t.Fatalf("uninstall: %d\n%s", rc, f.out.String())
	}
	if f.left != 1 || !f.ran("systemctl disable --now deneb-relay") || !f.ran("userdel deneb-relay") {
		t.Errorf("uninstall: left %d, %q", f.left, f.cmds)
	}
	for _, p := range []string{"/etc/systemd/system/deneb-relay.service", "/var/lib/deneb-relay", "/usr/local/lib/deneb-relay", "/usr/local/bin/deneb-relay"} {
		if f.exists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	if _, ok := f.lookupUser(relayUser); ok {
		t.Error("its user is still there")
	}
}

// TestInstallWithoutRoot: no root, so the user's own systemd, with
// lingering, under their home; and no firewall touched.
func TestInstallWithoutRoot(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, false).systemd()
	f.have["ufw"] = true
	f.replies["ufw status"] = "Status: active"
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test"), yes: true}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	out := f.out.String()
	if !strings.Contains(out, "! 没有 root：装成 kittors 自己的服务") || !strings.Contains(out, "已安装为当前用户的服务（systemd）") {
		t.Errorf("output:\n%s", out)
	}
	unit := f.read("/home/kittors/.config/systemd/user/deneb-relay.service")
	if !strings.Contains(unit, "ExecStart=/home/kittors/.local/lib/deneb-relay/deneb-relay run -data /home/kittors/.local/share/deneb-relay") || strings.Contains(unit, "User=") || !strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("the user unit:\n%s", unit)
	}
	if !f.ran("systemctl --user enable deneb-relay") || !f.ran("loginctl enable-linger kittors") || f.ran("useradd") || f.ran("ufw allow") {
		t.Errorf("commands: %q", f.cmds)
	}
}

// TestInstallCron: no service manager but cron: two lines in the relay
// user's crontab (at boot, and every minute if it stopped), and it starts
// now, detached. Uninstall takes the lines back out, leaving the rest.
func TestInstallCron(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).cron()
	f.have["useradd"], f.have["userdel"] = true, true
	f.crontab = "0 3 * * * /usr/local/bin/backup\n"
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	want := "@reboot /usr/local/lib/deneb-relay/deneb-relay run -data /var/lib/deneb-relay -log /var/lib/deneb-relay/relay.log -if-stopped # deneb-relay"
	if !strings.Contains(f.crontab, want) || !strings.Contains(f.crontab, "* * * * * /usr/local/lib") || !strings.HasPrefix(f.crontab, "0 3 * * * /usr/local/bin/backup\n") {
		t.Errorf("crontab:\n%s", f.crontab)
	}
	if !f.ran("crontab -u deneb-relay -") || len(f.spawned) != 1 || !strings.HasPrefix(f.spawned[0], "deneb-relay /usr/local/lib/deneb-relay/deneb-relay run -data /var/lib/deneb-relay -log") {
		t.Errorf("commands %q, started %q", f.cmds, f.spawned)
	}
	if rc := f.uninstall(context.Background(), false); rc != exitOK {
		t.Fatalf("uninstall: %d\n%s", rc, f.out.String())
	}
	if f.crontab != "0 3 * * * /usr/local/bin/backup\n" {
		t.Errorf("crontab after uninstall:\n%s", f.crontab)
	}
}

// TestInstallPortsTaken: the usual ports taken, it takes the next free
// pair and says who holds them; ports given by hand and taken, it stops.
func TestInstallPortsTaken(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	f.taken["tcp 47445"] = true
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	if out := f.out.String(); !strings.Contains(out, "✓ 端口：TCP 47446、UDP 47479（47445、47478 被占了：别的程序 占着 TCP 47445）") {
		t.Errorf("output:\n%s", out)
	}
	if s := f.settings("/var/lib/deneb-relay"); s.Listen != ":47446" || s.STUN != ":47479" {
		t.Errorf("ports in config.json: %s %s", s.Listen, s.STUN)
	}

	g := newFakeHost(t, true).systemd()
	g.taken["udp 3478"] = true
	if rc := g.install(context.Background(), installOpts{join: joinCode(t, ctl, "jie@deneb.test"), tcpPort: 8443, udpPort: 3478}); rc != exitFailed {
		t.Fatalf("ports given and taken: %d\n%s", rc, g.out.String())
	}
	if out := g.out.String(); !strings.Contains(out, "✗ 端口 TCP 8443 或 UDP 3478 被占了") || !strings.Contains(out, "--tcp-port") {
		t.Errorf("output:\n%s", out)
	}
}

// TestInstallFirewall: ufw guards the machine; asked, yes opens the two
// ports and uninstall closes them again. Without a terminal to ask, it
// opens nothing and prints the commands.
func TestInstallFirewall(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	f.have["ufw"] = true
	f.replies["ufw status"] = "Status: active\n\nTo Action From\n22/tcp ALLOW Anywhere\n"
	f.typed = "y"
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test"), firewall: "ask"}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	if !f.ran("ufw allow 47445/tcp") || !f.ran("ufw allow 47478/udp") || !strings.Contains(f.out.String(), "✓ 系统防火墙 ufw：已放行 TCP 47445、UDP 47478") {
		t.Errorf("commands %q\n%s", f.cmds, f.out.String())
	}
	if s := f.settings("/var/lib/deneb-relay"); s.Install.FirewallKind != "ufw" || !s.Install.FirewallOpen || len(s.Install.Firewall) != 2 {
		t.Errorf("config.json: %+v", s.Install)
	}
	// Again, the same ports: nothing asked, nothing opened twice.
	f.out.Reset()
	f.typed = ""
	if rc := f.install(context.Background(), installOpts{firewall: "ask"}); rc != exitOK {
		t.Fatalf("install again: %d\n%s", rc, f.out.String())
	}
	if n := f.count("ufw allow 47445/tcp"); n != 1 || !strings.Contains(f.out.String(), "✓ 系统防火墙 ufw：TCP 47445、UDP 47478 已经放行") {
		t.Errorf("opened %d times:\n%s", n, f.out.String())
	}
	f.uninstall(context.Background(), false)
	if !f.ran("ufw delete allow 47445/tcp") || !f.ran("ufw delete allow 47478/udp") {
		t.Errorf("uninstall did not close the ports: %q", f.cmds)
	}

	g := newFakeHost(t, true).systemd()
	g.have["ufw"] = true
	g.replies["ufw status"] = "Status: active"
	if rc := g.install(context.Background(), installOpts{join: joinCode(t, ctl, "jie@deneb.test"), firewall: "ask"}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, g.out.String())
	}
	if g.ran("ufw allow") || !strings.Contains(g.out.String(), "没有终端可以问") || !strings.Contains(g.out.String(), "  ufw allow 47445/tcp") {
		t.Errorf("without a terminal: %q\n%s", g.cmds, g.out.String())
	}
}

// TestInstallTakesOverDocker: a relay Deneb ran in Docker is taken over,
// asked first: its identity and settings carry on, so no join code is
// needed, and the container is stopped and kept.
func TestInstallTakesOverDocker(t *testing.T) {
	ctl := controltest.New(t)
	dev := ctl.Login(t, "kittors@deneb.test", "mac", newNode().pub)
	tok := dev.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).RelayToken
	f := newFakeHost(t, true).systemd()
	old := f.path("/opt/deneb-relay/data")
	os.MkdirAll(old, 0o700)
	_, pin, err := loadCert(old, "deneb-relay")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newControlClient(ctl.URL, "")
	st, err := register(context.Background(), c, config{token: tok, data: old}, proto.RelayInfo{DERPPort: 47445, Pin: pin}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	f.have["docker"] = true
	f.replies["docker ps -a"] = "abc123\tdeneb-relay\tdeneb-relay:v0\n"
	inspect, _ := json.Marshal([]any{map[string]any{
		"Args":   []string{"-control", ctl.URL, "-data", "/data", "-listen", ":47445", "-stun", ":47478", "-ipv4", "203.0.113.9", "-region", "香港"},
		"State":  map[string]any{"Running": true},
		"Mounts": []any{map[string]any{"Source": "/opt/deneb-relay/data", "Destination": "/data"}},
	}})
	f.replies["docker inspect abc123"] = string(inspect)
	if rc := f.install(context.Background(), installOpts{yes: true}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	if !f.ran("docker update --restart=no abc123") || !f.ran("docker stop -t 10 abc123") || f.ran("docker rm") {
		t.Errorf("docker: %q", f.cmds)
	}
	s := f.settings("/var/lib/deneb-relay")
	if s.IPv4 != "203.0.113.9" || s.Region != "香港" || s.Control != ctl.URL || s.Install.FromDocker != "deneb-relay" {
		t.Errorf("config.json: %+v %+v", s, s.Install)
	}
	if got, _ := loadState(f.path("/var/lib/deneb-relay")); got == nil || got.Relay != st.Relay || got.Secret != st.Secret {
		t.Errorf("the identity: %+v, want %+v", got, st)
	}
	if out := f.out.String(); !strings.Contains(out, "已迁过来：Docker 容器 deneb-relay 停下了") || !strings.Contains(out, "✓ 中转站“香港”") {
		t.Errorf("output:\n%s", out)
	}
}

// TestInstallNeedsCode: nothing installed and no code: it says where to
// get one; a code mangled in the copy, likewise.
func TestInstallNeedsCode(t *testing.T) {
	f := newFakeHost(t, true).systemd()
	if rc := f.install(context.Background(), installOpts{}); rc != exitFailed || !strings.Contains(f.out.String(), "✗ 要给加入码") {
		t.Errorf("no code: %d\n%s", rc, f.out.String())
	}
	f.out.Reset()
	if rc := f.install(context.Background(), installOpts{join: "deneb-join-%%%"}); rc != exitFailed || !strings.Contains(f.out.String(), "✗ 加入码不对") {
		t.Errorf("a broken code: %d\n%s", rc, f.out.String())
	}
}

// TestInstallServiceDoesNotStart: the service never answers: install says
// so, with its last lines of log.
func TestInstallServiceDoesNotStart(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	f.answers = func(int) bool { return false }
	f.replies["journalctl"] = "deneb-relay: listen tcp :47445: bind: permission denied"
	f.waitFor = 0
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitFailed {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	if out := f.out.String(); !strings.Contains(out, "✗ 服务没有起来") || !strings.Contains(out, "bind: permission denied") {
		t.Errorf("output:\n%s", out)
	}
}

// TestReportCheckUnreachable: the check from outside says it cannot be
// reached: each place's result, the control server's reason and advice
// as it words them, the router hint when the server did not give one, and
// exit code 3.
func TestReportCheckUnreachable(t *testing.T) {
	f := newFakeHost(t, true)
	f.check = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		return &proto.RelayStatus{State: proto.RelayUnreachable,
			Probes: []proto.RelayProbe{
				{From: "control", Family: "ipv4", Addr: netip.MustParseAddr("203.0.113.7"), TCP: proto.RelayProbeTimeout, UDP: proto.RelayProbeTimeout},
				{From: "hk-spare", Family: "ipv6", Addr: netip.MustParseAddr("2001:db8::7"), TCP: proto.RelayProbeNoRoute},
			},
			Problems: []proto.RelayProblem{{Code: proto.RelayProblemTCPBlocked, Level: proto.RelayLevelError,
				Message: "从外网连不上 TCP 47445", Advice: "请在云服务商的安全组里放行：TCP 47445、UDP 47478"}}}, nil
	}
	hinted := false
	rc := f.reportCheck(context.Background(), &steps{w: &f.out}, nil, 47445, 47478, scopeSystem, func() { hinted = true }, nil)
	out := f.out.String()
	for _, want := range []string{"✗ 从外网（Deneb 服务器）连不上 203.0.113.7 的 TCP 47445：没有回应，路上被挡了", "✗ 从外网连不上 TCP 47445", "  请在云服务商的安全组里放行：TCP 47445、UDP 47478", "还不能用：连不上"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if rc != exitNotReachable || !hinted || strings.Contains(out, "2001:db8::7") {
		t.Errorf("exit %d, hinted %v:\n%s", rc, hinted, out)
	}
}

// TestReportCheckSkipped: an address a place did not check (no address on
// the Internet) gets a plain line, no failure; UDP not checked is no
// trouble, and the line does not claim it either.
func TestReportCheckSkipped(t *testing.T) {
	f := newFakeHost(t, true)
	f.check = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		return &proto.RelayStatus{State: proto.RelayOK,
			Probes: []proto.RelayProbe{
				{From: "control", Family: "ipv4", Addr: netip.MustParseAddr("203.0.113.7"), TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK},
				{From: "香港", Family: "ipv4", Addr: netip.MustParseAddr("192.168.1.5"), TCP: proto.RelayProbeSkipped, UDP: proto.RelayProbeSkipped},
				{From: "control", Family: "ipv6", Addr: netip.MustParseAddr("2001:db8::8"), TCP: proto.RelayProbeOK, UDP: proto.RelayProbeSkipped},
			}}, nil
	}
	rc := f.reportCheck(context.Background(), &steps{w: &f.out}, nil, 47445, 47478, scopeSystem, nil, nil)
	out := f.out.String()
	for _, want := range []string{
		"✓ 从外网（Deneb 服务器）连得上 203.0.113.7：TCP 47445、UDP 47478\n",
		"  从外网（香港）没查 192.168.1.5：不是公网地址\n",
		"✓ 从外网（Deneb 服务器）连得上 2001:db8::8：TCP 47445\n",
		"可以用了",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if rc != exitOK || strings.Contains(out, "✗") || strings.Contains(out, "! ") {
		t.Errorf("exit %d:\n%s", rc, out)
	}
}

// TestCheckAndLeaveWithServer: the relay's calls as the control server
// answers them: a check from outside comes back with a state, and once
// it said it leaves, the server answers it with 410, which stops it.
func TestCheckAndLeaveWithServer(t *testing.T) {
	ctl := controltest.New(t)
	dev := ctl.Login(t, "kittors@deneb.test", "mac", newNode().pub)
	tok := dev.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "家里"}).RelayToken
	rr := startRelay(t, ctl, t.TempDir(), tok, nil)
	c, err := newControlClient(ctl.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	c.setSecret(rr.r.st.Secret)
	ctx := context.Background()
	res, err := checkFromOutside(ctx, c)
	if errors.Is(err, errNoCheck) {
		t.Skip("this control server has no check yet")
	}
	if err != nil || res.State == "" {
		t.Fatalf("check: %+v, %v", res, err)
	}
	t.Logf("check: %s, %d problems, %d probes", res.State, len(res.Problems), len(res.Probes))
	if err := leave(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := rr.r.sync(ctx); !errors.Is(err, errRemoved) || !errors.Is(err, errRevoked) {
		t.Fatalf("sync after leaving: %v", err)
	}
	if err := leave(ctx, c); err != nil {
		t.Errorf("leaving twice: %v", err)
	}
}

// TestStatus: what status says of an installed relay: it runs, and its
// state in Deneb as of the last check, with the reasons (in its own
// words when the server sends none); a relay deleted in Deneb is told so.
func TestStatus(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	f.replies["systemctl is-active"] = "active\n"
	f.current = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		return &proto.RelayStatus{State: proto.RelayUnreachable, CheckedAt: time.Now().Add(-5 * time.Minute),
			Problems: []proto.RelayProblem{{Code: proto.RelayProblemTCPBlocked, Level: proto.RelayLevelError, Port: 47445}}}, nil
	}
	f.out.Reset()
	rc := f.status(context.Background(), false)
	out := f.out.String()
	for _, want := range []string{"✓ deneb-relay " + shortVersion() + "：在运行（systemd 服务）", "✗ Deneb 里的状态：连不上（5 分钟前检查的）", "✗ 从外网连不上 TCP 47445", "  请在云服务商的安全组（入方向）里放行它", "马上重新检查：deneb-relay status --recheck"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if rc != exitNotReachable || f.checks != 1 {
		t.Errorf("exit %d, %d checks from outside (install's alone)", rc, f.checks)
	}
	f.current = func(context.Context, *controlClient) (*proto.RelayStatus, error) { return nil, errRemoved }
	f.out.Reset()
	if rc := f.status(context.Background(), false); rc != exitFailed || !strings.Contains(f.out.String(), "✗ Deneb 里已经删掉了这个中转站") {
		t.Errorf("deleted in Deneb: %d\n%s", rc, f.out.String())
	}
	f.out.Reset()
	f.status(context.Background(), true)
	if f.checks != 2 {
		t.Errorf("--recheck made %d checks in all", f.checks)
	}
}
