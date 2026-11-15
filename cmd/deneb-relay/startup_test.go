package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/control/controltest"
	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
)

// freePorts are a TCP and a UDP port nothing listens on now.
func freePorts(t *testing.T) (tcp, udp int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp = pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	return tcp, udp
}

// stunAnswers: a STUN binding request to port gets its answer.
func stunAnswers(port int) bool {
	c, err := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	defer c.Close()
	tx := stun.NewTxID()
	c.Write(stun.Request(tx))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return false
	}
	got, _, err := stun.ParseResponse(buf[:n])
	return err == nil && got == tx
}

// TestRelayAnswersBeforeItRegisters: the relay takes TLS and STUN before
// the control server first hears from it, so a check from outside made as
// the server learns of it (registered, online again) finds it up; its
// pages wait until it is set up.
func TestRelayAnswersBeforeItRegisters(t *testing.T) {
	ctl := controltest.New(t)
	dev := ctl.Login(t, "kittors@deneb.test", "mac", newNode().pub)
	tok := dev.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "家里"}).RelayToken
	target, _ := url.Parse(ctl.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	held, release := make(chan struct{}, 1), make(chan struct{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == proto.PathRelayRegister {
			held <- struct{}{}
			<-release
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()

	tcp, udp := freePorts(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config{control: front.URL, token: tok, data: dir, listen: "127.0.0.1:" + strconv.Itoa(tcp),
			stun: "127.0.0.1:" + strconv.Itoa(udp), syncEvery: time.Hour, region: regionOff}, t.Logf, nil)
	}()
	t.Cleanup(func() { // run logs with t: it ends before the test does
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the relay did not register")
	}

	// Registering, not set up: TLS and STUN answer, the probe page waits.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", "127.0.0.1:"+strconv.Itoa(tcp), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("no TLS while it registers: %v", err)
	}
	conn.Close()
	if !stunAnswers(udp) {
		t.Error("no STUN while it registers")
	}
	early := make(chan bool, 1)
	go func() { early <- answersLocally(tcp) }()
	select {
	case ok := <-early:
		t.Fatalf("its probe page answered (%v) before it was set up", ok)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if ok := <-early; !ok {
		t.Error("the probe page did not answer once it was set up")
	}
}

// TestRemovedRelayHoldsNoPort: deleted in Deneb, the relay stops serving,
// and holds neither of its ports while its process waits (cmdRun).
func TestRemovedRelayHoldsNoPort(t *testing.T) {
	ctl := controltest.New(t)
	dev := ctl.Login(t, "kittors@deneb.test", "mac", newNode().pub)
	tok := dev.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "家里"}).RelayToken
	tcp, udp := freePorts(t)
	rr := startRelay(t, ctl, t.TempDir(), tok, func(c *config) {
		c.listen, c.stun, c.syncEvery = "127.0.0.1:"+strconv.Itoa(tcp), "127.0.0.1:"+strconv.Itoa(udp), 100*time.Millisecond
	})
	dev.MustOp(proto.OpRelayDelete, proto.OpArgs{Relay: rr.r.st.Relay})
	select {
	case <-rr.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the relay did not stop once deleted")
	}
	if !errors.Is(rr.err, errRevoked) {
		t.Errorf("run: %v", rr.err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(tcp))
	if err != nil {
		t.Errorf("TCP %d still held: %v", tcp, err)
	} else {
		ln.Close()
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(udp))
	if err != nil {
		t.Errorf("UDP %d still held: %v", udp, err)
	} else {
		pc.Close()
	}
}

// TestInstallFailedLeavesNothing: an install that fails on the join code
// (used already) takes back what it made: no program, link, data or user
// is left, and uninstall has nothing to do.
func TestInstallFailedLeavesNothing(t *testing.T) {
	ctl := controltest.New(t)
	code := joinCode(t, ctl, "kittors@deneb.test")
	first := newFakeHost(t, true).systemd()
	if rc := first.install(context.Background(), installOpts{join: code}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, first.out.String())
	}

	f := newFakeHost(t, true).systemd()
	if rc := f.install(context.Background(), installOpts{join: code}); rc != exitFailed {
		t.Fatalf("install with a used code: %d\n%s", rc, f.out.String())
	}
	out := f.out.String()
	if !strings.Contains(out, "这台上已经改动的都还原了") {
		t.Errorf("no word of putting things back:\n%s", out)
	}
	for _, p := range []string{"/usr/local/lib/deneb-relay", "/usr/local/bin/deneb-relay", "/var/lib/deneb-relay", "/etc/systemd/system/deneb-relay.service"} {
		if f.exists(p) {
			t.Errorf("%s left", p)
		}
	}
	if f.ran("useradd") || f.ran("systemctl") {
		t.Errorf("ran %q", f.cmds)
	}
	f.out.Reset()
	if rc := f.uninstall(context.Background(), false); rc != exitFailed || !strings.Contains(f.out.String(), "没有用 deneb-relay install 装过") {
		t.Errorf("uninstall: %d\n%s", rc, f.out.String())
	}
}

// TestInstallUnfinished: registered, then the service fails: what is here
// is in config.json, the person is told how to go on; uninstall takes it
// all away and tells the control server, and a run again finishes it with
// no new code.
func TestInstallUnfinished(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	f.fails["systemctl restart"] = true
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitFailed {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	if out := f.out.String(); !strings.Contains(out, "没装完：解决上面的问题后，再运行一次安装命令就行") || !strings.Contains(out, "/usr/local/bin/deneb-relay uninstall") {
		t.Errorf("no way on:\n%s", out)
	}
	s := f.settings("/var/lib/deneb-relay")
	if s.Install == nil || !s.Install.MadeUser || s.Install.Service == "" {
		t.Fatalf("config.json: %+v", s.Install)
	}

	// A run again, the service fixed: done, no code spent.
	delete(f.fails, "systemctl restart")
	f.out.Reset()
	if rc := f.install(context.Background(), installOpts{}); rc != exitOK {
		t.Fatalf("install again: %d\n%s", rc, f.out.String())
	}
	f.out.Reset()
	if rc := f.uninstall(context.Background(), false); rc != exitOK {
		t.Fatalf("uninstall: %d\n%s", rc, f.out.String())
	}
	if f.left != 1 || !f.ran("userdel deneb-relay") {
		t.Errorf("left %d, %q", f.left, f.cmds)
	}
	for _, p := range []string{"/usr/local/lib/deneb-relay", "/usr/local/bin/deneb-relay", "/var/lib/deneb-relay", "/etc/systemd/system/deneb-relay.service"} {
		if f.exists(p) {
			t.Errorf("%s left", p)
		}
	}
}

// TestUninstallLeftovers: what an install from before 0.1.9 left when it
// failed (its user, program, link and data, and no config.json), uninstall
// finds and takes away.
func TestUninstallLeftovers(t *testing.T) {
	f := newFakeHost(t, true).systemd()
	f.file("/usr/local/lib/deneb-relay/deneb-relay", "program")
	os.MkdirAll(f.path("/usr/local/bin"), 0o755)
	os.Symlink("/usr/local/lib/deneb-relay/deneb-relay", f.path("/usr/local/bin/deneb-relay"))
	os.MkdirAll(f.path("/var/lib/deneb-relay"), 0o700)
	if _, _, err := loadCert(f.path("/var/lib/deneb-relay"), "deneb-relay"); err != nil {
		t.Fatal(err)
	}
	f.file("/etc/passwd", f.read("/etc/passwd")+"deneb-relay:x:999:999::/var/lib/deneb-relay:/usr/sbin/nologin\n")
	if rc := f.uninstall(context.Background(), false); rc != exitOK {
		t.Fatalf("uninstall: %d\n%s", rc, f.out.String())
	}
	if !strings.Contains(f.out.String(), "上次安装没有装完") || !f.ran("userdel deneb-relay") || f.ran("systemctl disable") {
		t.Errorf("%q\n%s", f.cmds, f.out.String())
	}
	for _, p := range []string{"/usr/local/lib/deneb-relay", "/usr/local/bin/deneb-relay", "/var/lib/deneb-relay"} {
		if f.exists(p) {
			t.Errorf("%s left", p)
		}
	}
	// An install that got as far as its unit is stopped too.
	g := newFakeHost(t, true).systemd()
	g.file("/etc/systemd/system/deneb-relay.service", "[Unit]\n")
	if rc := g.uninstall(context.Background(), false); rc != exitOK || !g.ran("systemctl disable --now deneb-relay") || g.exists("/etc/systemd/system/deneb-relay.service") {
		t.Errorf("uninstall: %d %q\n%s", rc, g.cmds, g.out.String())
	}
}

// TestInstallFirewallLeavesTheirs: a port the firewall lets in already is
// neither asked about, opened nor taken back: the person's rule stays.
func TestInstallFirewallLeavesTheirs(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, true).systemd()
	f.have["ufw"] = true
	f.replies["ufw status"] = "Status: active"
	f.replies["ufw show added"] = "Added user rules (see 'ufw status' for running firewall):\nufw allow 22/tcp\nufw allow 47445/tcp\n"
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test"), firewall: "ask", yes: true}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	out := f.out.String()
	if f.ran("ufw allow 47445/tcp") || !f.ran("ufw allow 47478/udp") || !strings.Contains(out, "要放行 UDP 47478 吗？（TCP 47445 原来就放行了）") ||
		!strings.Contains(out, "✓ 系统防火墙 ufw：已放行 UDP 47478（TCP 47445 原来就放行了）") {
		t.Errorf("%q\n%s", f.cmds, out)
	}
	f.uninstall(context.Background(), false)
	if f.ran("ufw delete allow 47445/tcp") || !f.ran("ufw delete allow 47478/udp") {
		t.Errorf("uninstall: %q", f.cmds)
	}

	// Both let in already: nothing asked, opened or taken back.
	g := newFakeHost(t, true).systemd()
	g.have["ufw"] = true
	g.replies["ufw status"] = "Status: active"
	g.replies["ufw show added"] = "ufw allow 47445/tcp\nufw allow 47478\n"
	if rc := g.install(context.Background(), installOpts{join: joinCode(t, ctl, "jie@deneb.test"), firewall: "ask"}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, g.out.String())
	}
	if g.ran("ufw allow") || !strings.Contains(g.out.String(), "✓ 系统防火墙 ufw：TCP 47445、UDP 47478 原来就放行了") {
		t.Errorf("%q\n%s", g.cmds, g.out.String())
	}
	if s := g.settings("/var/lib/deneb-relay"); !s.Install.FirewallOpen || len(s.Install.Firewall) != 0 {
		t.Errorf("config.json: %+v", s.Install)
	}
	g.uninstall(context.Background(), false)
	if g.ran("ufw delete") {
		t.Errorf("uninstall: %q", g.cmds)
	}
}

// TestReportCheckWhileStarting: a check that finished before the relay
// answered here (a control server from before 11-15 answers with one made
// as it registered) is asked again; one made since stands.
func TestReportCheckWhileStarting(t *testing.T) {
	stale := &proto.RelayStatus{State: proto.RelayUnreachable, CheckedAt: time.Now().Add(-30 * time.Second),
		Probes: []proto.RelayProbe{{From: "control", Family: "ipv4", Addr: netip.MustParseAddr("203.0.113.7"), TCP: proto.RelayProbeRefused, UDP: proto.RelayProbeRefused}}}
	fresh := &proto.RelayStatus{State: proto.RelayOK, CheckedAt: time.Now(),
		Probes: []proto.RelayProbe{{From: "control", Family: "ipv4", Addr: netip.MustParseAddr("203.0.113.7"), TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK}}}
	f := newFakeHost(t, true)
	answers := []*proto.RelayStatus{stale, fresh}
	f.check = func(context.Context, *controlClient) (*proto.RelayStatus, error) {
		f.checks++
		return answers[min(f.checks, len(answers))-1], nil
	}
	up := &upSince{at: time.Now().Add(-time.Second)}
	if rc := f.reportCheck(context.Background(), &steps{w: &f.out}, nil, 47445, 47478, scopeSystem, nil, up); rc != exitOK || f.checks != 2 || strings.Contains(f.out.String(), "✗") {
		t.Errorf("exit %d after %d checks:\n%s", rc, f.checks, f.out.String())
	}

	// A check made after it answered, that cannot reach it, is the answer.
	g := newFakeHost(t, true)
	late := *stale
	late.CheckedAt = time.Now()
	g.check = func(context.Context, *controlClient) (*proto.RelayStatus, error) { g.checks++; return &late, nil }
	if rc := g.reportCheck(context.Background(), &steps{w: &g.out}, nil, 47445, 47478, scopeSystem, nil, &upSince{at: time.Now().Add(-5 * time.Second)}); rc != exitNotReachable || g.checks != 1 {
		t.Errorf("exit %d after %d checks:\n%s", rc, g.checks, g.out.String())
	}
}

// TestInstallUserWithoutUserSystemd: no root, systemd runs but this user
// cannot use its user services, and there is no cron: it says so, not
// that the machine has no service manager.
func TestInstallUserWithoutUserSystemd(t *testing.T) {
	ctl := controltest.New(t)
	f := newFakeHost(t, false).systemd()
	f.fails["systemctl --user show-environment"] = true
	if rc := f.install(context.Background(), installOpts{join: joinCode(t, ctl, "kittors@deneb.test")}); rc != exitOK {
		t.Fatalf("install: %d\n%s", rc, f.out.String())
	}
	out := f.out.String()
	if !strings.Contains(out, "当前用户用不了 systemd 的用户服务，也没有 cron") || strings.Contains(out, "没有服务管理程序") {
		t.Errorf("%s", out)
	}
	if !strings.Contains(out, "/home/kittors/.local/lib/deneb-relay/deneb-relay run") {
		t.Errorf("no command line to add:\n%s", out)
	}
}
