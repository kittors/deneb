// Command topo builds the M1 fake topology in colima's Docker and runs the
// engine's checks against it (plan §3 M1: "每次改动都跑"):
//
//	go run ./test/topo            build, bring up, check, tear down
//	go run ./test/topo -keep      leave it running to poke at
//	go run ./test/topo -down      tear down only
//
//	                       ┌──────────── dnb-net (198.51.100.0/24, "the Internet") ────────────┐
//	 control .2  derper .3 │  build .10 (kittors, public)     nas .11 (阿杰, public)          │
//	                       └── isp-a .21 ──────────────────────── isp-b .22 (symmetric NAT) ──┘
//	                             │ 172.16.201.0/24                    │ 172.16.202.0/24
//	                          router-a                           router-b
//	                             │ 192.168.110.0/24                  │ 192.168.120.0/24
//	                           mac .10 (kittors)                  desktop .10 (阿杰)
//
// Two LANs as in the acceptance: 工作室 (mac, build) made by kittors, 周末联机
// (desktop, nas) made by 阿杰, who invites kittors; mac joins it. mac sits
// behind two NATs that keep ports (direct paths form); desktop behind an ISP
// NAT that picks a new port per flow, so mac↔desktop must go through the
// relay while desktop↔nas still goes direct.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

var (
	keep      = flag.Bool("keep", false, "leave the topology running")
	down      = flag.Bool("down", false, "only tear the topology down")
	skipBuild = flag.Bool("skip-build", false, "use the image already built")
	only      = flag.String("only", "", "comma-separated checks to run (default: all)")
	verbose   = flag.Bool("v", false, "print every docker command")
	repeat    = flag.Int("repeat", 1, "run the chosen checks this many times (to catch a flaky one)")
)

const (
	image   = "deneb-topo:latest"
	ctlIP   = "198.51.100.2"
	derpIP  = "198.51.100.3"
	derp2IP = "198.51.100.4" // a second relay: nas lives there (M2, peers on different relays)
	ctlPort = "47080"
)

type node struct {
	name, display, kind, owner, email, ip, gw, net string
	extra                                          []string // more denebd run flags
}

var nodes = []node{
	{name: "mac", display: "这台电脑", kind: "laptop", owner: "kittors", email: "kittors@deneb.test", ip: "192.168.110.10", gw: "192.168.110.2", net: "dnb-homea"},
	{name: "build", display: "构建服务器", kind: "server", owner: "kittors", email: "kittors@deneb.test", ip: "198.51.100.10", gw: "-", net: "dnb-net"},
	{name: "desktop", display: "阿杰的台式机", kind: "desktop", owner: "jie", email: "jie@deneb.test", ip: "192.168.120.10", gw: "192.168.120.2", net: "dnb-homeb"},
	{name: "nas", display: "阿杰的 NAS", kind: "nas", owner: "jie", email: "jie@deneb.test", ip: "198.51.100.11", gw: "-", net: "dnb-net",
		extra: []string{"-home-relay", "second"}},
}

func main() {
	flag.Parse()
	root, _ := os.Getwd()
	dir := filepath.Join(root, "test", "topo")
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err != nil {
		fatal("run from the repository root: %v", err)
	}
	teardown()
	if *down {
		return
	}
	if !*skipBuild {
		step("build", func() error { return build(root, dir) })
	}
	// Only its own directory: real.sh, kernel-round.sh and the coexistence
	// monitor keep their records next to it, under .run and .accept.
	run := filepath.Join(dir, ".run", "fake")
	os.RemoveAll(run)
	os.MkdirAll(filepath.Join(run, "certs"), 0o755)
	step("up", func() error { return up(run) })
	if !*keep {
		defer teardown()
	}
	step("login", login)
	step("lans", setupLANs)

	checks := []struct {
		name string
		fn   func() error
	}{
		{"visibility", checkVisibility},
		{"ping-matrix", checkPingMatrix},
		{"paths", checkPaths},
		{"services", checkServices},
		{"transit", checkTransit},
		{"spoof", checkSpoof},
		{"pause", checkPause},
		{"add-remove-undo", checkAddRemoveUndo},
		{"refuse", checkRefuse},
		{"udp-block", checkUDPBlock},
		{"two-lans-long-connections", checkTwoLANs},
		{"control-down", checkControlDown},
		{"logout-login", checkLogoutLogin},
		{"restart", checkRestart},
		// M2
		{"idle", checkIdle},
		{"lossy-line", checkLossy},
		{"pause-resume", checkPauseResume},
		{"cross-relay", checkCrossRelay},
		{"net-change", checkNetChange},
		{"sleep", checkSleep},
	}
	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s != "" {
			want[s] = true
		}
	}
	var failed []string
	var runs []struct {
		name string
		fn   func() error
	}
	for i := 0; i < max(*repeat, 1); i++ {
		runs = append(runs, checks...)
	}
	for _, c := range runs {
		if len(want) > 0 && !want[c.name] {
			continue
		}
		start := time.Now()
		err := c.fn()
		if err != nil {
			failed = append(failed, c.name)
			fmt.Printf("FAIL %-28s %6.1fs  %v\n", c.name, time.Since(start).Seconds(), err)
			continue
		}
		fmt.Printf("ok   %-28s %6.1fs\n", c.name, time.Since(start).Seconds())
	}
	if len(failed) > 0 {
		dumpLogs(run)
		fmt.Printf("\n%d failed: %s (logs in %s)\n", len(failed), strings.Join(failed, ", "), run)
		if !*keep {
			teardown()
		}
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "topo: "+f+"\n", a...)
	os.Exit(1)
}

func step(name string, fn func() error) {
	start := time.Now()
	if err := fn(); err != nil {
		fmt.Printf("FAIL %-28s %v\n", name, err)
		dumpLogs(filepath.Join("test", "topo", ".run", "fake"))
		if !*keep {
			teardown()
		}
		os.Exit(1)
	}
	fmt.Printf("ok   %-28s %6.1fs\n", name, time.Since(start).Seconds())
}

func sh(name string, args ...string) (string, error) {
	if *verbose {
		fmt.Println("+", name, strings.Join(args, " "))
	}
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %v: %s%s", name, strings.Join(args, " "), err, errb.String(), out.String())
	}
	return out.String(), nil
}

func docker(args ...string) (string, error) { return sh("docker", args...) }

// logsOf returns a container's log; the engine logs to stderr, which
// docker logs replays on stderr, so both streams are read.
func logsOf(name string, since string) (string, error) {
	args := []string{"logs"}
	if since != "" {
		args = append(args, "--since", since)
	}
	out, err := exec.Command("docker", append(args, name)...).CombinedOutput()
	return string(out), err
}

// deneb runs the command line inside a node.
func deneb(n string, args ...string) (string, error) {
	return docker(append([]string{"exec", "dnb-" + n, "deneb"}, args...)...)
}

func build(root, dir string) error {
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	arch := "arm64"
	if out, err := docker("info", "--format", "{{.Architecture}}"); err == nil && strings.Contains(out, "x86_64") {
		arch = "amd64"
	}
	env := append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	for _, b := range []struct{ out, pkg, tags string }{
		{"denebd", "./cmd/denebd", "denebtest"},
		{"deneb-control", "./cmd/deneb-control", ""},
		{"derper", "./test/topo/derper", ""},
	} {
		args := []string{"build", "-o", filepath.Join(bin, b.out)}
		if b.tags != "" {
			args = append(args, "-tags", b.tags)
		}
		args = append(args, b.pkg)
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = root, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %v\n%s", b.pkg, err, out)
		}
	}
	_, err := docker("build", "-q", "-t", image, dir)
	return err
}

func teardown() {
	out, _ := docker("ps", "-aq", "--filter", "name=^dnb-")
	if ids := strings.Fields(out); len(ids) > 0 {
		docker(append([]string{"rm", "-f"}, ids...)...)
	}
	for _, n := range []string{"dnb-net", "dnb-ispa", "dnb-homea", "dnb-ispb", "dnb-homeb"} {
		docker("network", "rm", n)
	}
}

func up(run string) error {
	for _, n := range []struct{ name, subnet string }{
		{"dnb-net", "198.51.100.0/24"}, {"dnb-ispa", "172.16.201.0/24"}, {"dnb-homea", "192.168.110.0/24"},
		{"dnb-ispb", "172.16.202.0/24"}, {"dnb-homeb", "192.168.120.0/24"},
	} {
		// Internal: Docker routes nothing in or out; the NAT containers do.
		if _, err := docker("network", "create", "--internal", "--subnet", n.subnet, n.name); err != nil {
			return err
		}
	}
	certs := filepath.Join(run, "certs")
	runc := func(name, network, ip string, extra []string, args ...string) error {
		a := []string{"run", "-d", "--name", "dnb-" + name, "--hostname", name, "--network", network, "--ip", ip}
		a = append(a, extra...)
		a = append(a, image)
		a = append(a, args...)
		_, err := docker(a...)
		return err
	}
	connect := func(name, network, ip string) error {
		_, err := docker("network", "connect", "--ip", ip, network, "dnb-"+name)
		return err
	}
	router := []string{"--cap-add", "NET_ADMIN", "--sysctl", "net.ipv4.ip_forward=1"}
	steps := []func() error{
		func() error {
			return runc("control", "dnb-net", ctlIP, nil, "control", "-dev", "-tls", "on", "-listen", "0.0.0.0:"+ctlPort,
				"-public-url", "https://"+ctlIP+":"+ctlPort, "-tls-hosts", ctlIP, "-data", "/data",
				"-relay", "id=local,name=本地,host=derper.deneb.test,ipv4="+derpIP+",derp=443,stun=3478",
				"-relay", "id=second,name=第二,host=derper2.deneb.test,ipv4="+derp2IP+",derp=443,stun=3478")
		},
		func() error {
			return runc("derper", "dnb-net", derpIP, []string{"-v", certs + ":/certs"}, "derper")
		},
		func() error {
			return runc("derper2", "dnb-net", derp2IP, []string{"-v", certs + "2:/certs"}, "derper", "-hostname", "derper2.deneb.test")
		},
		func() error {
			return runc("isp-a", "dnb-net", "198.51.100.21", router, "router", "198.51.100.", "172.16.201.", "-")
		},
		func() error { return connect("isp-a", "dnb-ispa", "172.16.201.2") },
		func() error {
			return runc("isp-b", "dnb-net", "198.51.100.22", router, "router", "198.51.100.", "172.16.202.", "-", "symmetric")
		},
		func() error { return connect("isp-b", "dnb-ispb", "172.16.202.2") },
		func() error {
			return runc("router-a", "dnb-ispa", "172.16.201.3", router, "router", "172.16.201.", "192.168.110.", "172.16.201.2")
		},
		func() error { return connect("router-a", "dnb-homea", "192.168.110.2") },
		func() error {
			return runc("router-b", "dnb-ispb", "172.16.202.3", router, "router", "172.16.202.", "192.168.120.", "172.16.202.2")
		},
		func() error { return connect("router-b", "dnb-homeb", "192.168.120.2") },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	pin, err := waitFor(20*time.Second, func() (string, error) {
		out, err := docker("exec", "dnb-control", "cat", "/data/tls/pin.txt")
		return strings.TrimSpace(out), err
	})
	if err != nil {
		return fmt.Errorf("control server pin: %w", err)
	}
	var both []byte
	for _, f := range []string{filepath.Join(certs, "derper.pem"), filepath.Join(certs+"2", "derper.pem")} {
		b, err := waitFor(10*time.Second, func() (string, error) {
			b, err := os.ReadFile(f)
			return string(b), err
		})
		if err != nil {
			return fmt.Errorf("relay certificate %s: %w", f, err)
		}
		both = append(both, b...)
	}
	if err := os.WriteFile(filepath.Join(certs, "relays.pem"), both, 0o644); err != nil {
		return err
	}
	for _, n := range nodes {
		// -idle 6s: M2's idle check need not wait the default 45 s.
		// -path-by-loss: M2's lossy-line check (off by default for now).
		args := []string{"node", n.gw, "-control", "https://" + ctlIP + ":" + ctlPort, "-control-pin", pin,
			"-relay-ca", "/certs/relays.pem", "-name", n.display, "-kind", n.kind, "-userview=false", "-idle", "6s", "-path-by-loss"}
		args = append(args, n.extra...)
		if err := runc(n.name, n.net, n.ip, []string{"--cap-add", "NET_ADMIN", "-v", certs + ":/certs:ro"}, args...); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		if _, err := waitFor(15*time.Second, func() (string, error) { return deneb(n.name, "status") }); err != nil {
			return fmt.Errorf("%s engine did not start: %w", n.name, err)
		}
	}
	return nil
}

func waitFor(d time.Duration, fn func() (string, error)) (string, error) {
	deadline := time.Now().Add(d)
	for {
		out, err := fn()
		if err == nil {
			return out, nil
		}
		if time.Now().After(deadline) {
			return out, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func state(n string) (*apitype.State, error) {
	out, err := deneb(n, "status", "--json")
	if err != nil {
		return nil, err
	}
	var s apitype.State
	return &s, json.Unmarshal([]byte(out), &s)
}

var loginRE = regexp.MustCompile(`https?://\S+`)

// login logs every node in through the dev server's approve hook, the way
// a person would in a browser.
func login() error {
	for _, n := range nodes {
		out, err := deneb(n.name, "login", "--no-wait")
		if err != nil {
			return err
		}
		url := loginRE.FindString(out)
		if url == "" {
			return fmt.Errorf("%s: no login link in %q", n.name, out)
		}
		body := fmt.Sprintf(`{"login":%q,"email":%q}`, url, n.email)
		if _, err := docker("exec", "dnb-build", "curl", "-sfk", "-X", "POST", "https://"+ctlIP+":"+ctlPort+"/dev/approve", "-d", body); err != nil {
			return fmt.Errorf("%s: approve: %w", n.name, err)
		}
	}
	for _, n := range nodes {
		if _, err := waitFor(10*time.Second, func() (string, error) {
			s, err := state(n.name)
			if err == nil && !s.LoggedIn {
				err = errors.New("not logged in yet")
			}
			return "", err
		}); err != nil {
			return fmt.Errorf("%s: %w", n.name, err)
		}
	}
	return nil
}

// setupLANs plays plan §3 M1 items 2 and 3 on the command line.
func setupLANs() error {
	cmds := [][]string{
		{"mac", "lan", "create", "工作室"},
		{"build", "lan", "add", "工作室"},
		{"desktop", "lan", "create", "周末联机"},
		{"nas", "lan", "add", "周末联机"},
		// The tick service (tcp/7000) is what the long-connection checks
		// hold open; each peer opens it to its LAN.
		{"build", "service", "工作室", "other", "tcp/7000", "on", "--name", "计时流"},
		{"desktop", "service", "周末联机", "other", "tcp/7000", "on", "--name", "计时流"},
		{"nas", "service", "周末联机", "other", "tcp/7000", "on", "--name", "计时流"},
	}
	for _, c := range cmds {
		if out, err := deneb(c[0], c[1:]...); err != nil {
			return fmt.Errorf("%s: %v %s", c[0], err, out)
		}
	}
	// M2's control server can make new devices wait for the owner's
	// approval; these checks are M1's flows, so 阿杰 turns that off.
	if s, err := state("desktop"); err == nil {
		for _, sc := range s.Scopes {
			if sc.Name == "周末联机" {
				deneb("desktop", "op", "lan.setApproveDevices", fmt.Sprintf(`{"lan":%q,"on":false}`, strings.TrimPrefix(string(sc.ID), "lan/")))
			}
		}
	}
	out, err := deneb("desktop", "lan", "invite", "周末联机")
	if err != nil {
		return err
	}
	link := loginRE.FindString(out)
	if out, err := deneb("mac", "lan", "join", link); err != nil {
		return fmt.Errorf("mac join: %v %s", err, out)
	}
	return nil
}

func peersOf(n string) ([]string, error) {
	s, err := state(n)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range s.Peers {
		out = append(out, p.Name)
	}
	slices.Sort(out)
	return out, nil
}

func checkVisibility() error {
	want := map[string][]string{"mac": {"build", "desktop", "nas"}, "build": {"mac"}, "desktop": {"mac", "nas"}, "nas": {"desktop", "mac"}}
	for n, w := range want {
		_, err := waitFor(10*time.Second, func() (string, error) {
			got, err := peersOf(n)
			if err == nil && !slices.Equal(got, w) {
				err = fmt.Errorf("%s sees %v, want %v", n, got, w)
			}
			return "", err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func ping(from, to string, n int) (string, error) {
	return deneb(from, "ping", to, "-c", fmt.Sprint(n), "-i", "200ms")
}

func pingOK(from, to string, within time.Duration) error {
	_, err := waitFor(within, func() (string, error) { return ping(from, to, 1) })
	if err != nil {
		return fmt.Errorf("%s cannot ping %s: %v", from, to, err)
	}
	return nil
}

func pingFails(from, to string) error {
	out, err := ping(from, to, 2)
	if err == nil {
		return fmt.Errorf("%s should not reach %s:\n%s", from, to, out)
	}
	return nil
}

func checkPingMatrix() error {
	for _, p := range [][2]string{{"mac", "build"}, {"mac", "desktop"}, {"mac", "nas"}, {"build", "mac"}, {"desktop", "mac"}, {"nas", "mac"}, {"desktop", "nas"}, {"nas", "desktop"}} {
		if err := pingOK(p[0], p[1], 10*time.Second); err != nil {
			return err
		}
	}
	for _, p := range [][2]string{{"build", "nas"}, {"build", "desktop"}, {"nas", "build"}, {"desktop", "build"}} {
		out, err := ping(p[0], p[1], 1)
		if err == nil || !strings.Contains(err.Error(), "找不到") {
			return fmt.Errorf("%s -> %s should be not found: %v %s", p[0], p[1], err, out)
		}
	}
	return nil
}

func pathKind(from, to string) (apitype.PeerPath, error) {
	s, err := state(from)
	if err != nil {
		return apitype.PeerPath{}, err
	}
	var id apitype.DeviceID
	for _, p := range s.Peers {
		if p.Name == to {
			id = p.ID
		}
	}
	for _, p := range s.Paths {
		if p.DeviceID == id {
			return p, nil
		}
	}
	return apitype.PeerPath{}, fmt.Errorf("%s has no path to %s", from, to)
}

func waitPath(from, to string, kind apitype.PathKind, within time.Duration) error {
	var last apitype.PeerPath
	_, err := waitFor(within, func() (string, error) {
		p, err := pathKind(from, to)
		last = p
		if err == nil && p.Kind != kind {
			err = fmt.Errorf("%s -> %s is %s", from, to, p.Kind)
		}
		return "", err
	})
	if err != nil {
		return fmt.Errorf("%s -> %s: %s, want %s (%v)", from, to, last.Kind, kind, err)
	}
	return nil
}

func checkPaths() error {
	for _, c := range []struct {
		from, to string
		kind     apitype.PathKind
	}{
		{"mac", "build", apitype.PathDirect}, {"mac", "nas", apitype.PathDirect},
		{"desktop", "nas", apitype.PathDirect}, {"nas", "desktop", apitype.PathDirect},
		{"mac", "desktop", apitype.PathRelay}, {"desktop", "mac", apitype.PathRelay},
	} {
		if err := waitPath(c.from, c.to, c.kind, 15*time.Second); err != nil {
			return err
		}
	}
	return nil
}

func httpGet(from, to string) (map[string]any, error) {
	out, err := deneb(from, "debug", "http", to)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal([]byte(out), &m)
}

func httpOK(from, to string, within time.Duration) error {
	_, err := waitFor(within, func() (string, error) {
		m, err := httpGet(from, to)
		if err == nil && m["ok"] != true {
			err = fmt.Errorf("%v", m["error"])
		}
		return "", err
	})
	if err != nil {
		return fmt.Errorf("%s cannot open %s's website: %v", from, to, err)
	}
	return nil
}

func httpFails(from, to string) error {
	m, err := httpGet(from, to)
	if err == nil && m["ok"] == true {
		return fmt.Errorf("%s opened %s's website, which is not open to it", from, to)
	}
	return nil
}

// checkServices is acceptance item 8: a service opened to one LAN only.
func checkServices() error {
	if err := httpFails("mac", "build"); err != nil {
		return fmt.Errorf("before opening: %w", err)
	}
	if _, err := deneb("build", "service", "工作室", "web", "tcp/80", "on"); err != nil {
		return err
	}
	if err := httpOK("mac", "build", 5*time.Second); err != nil {
		return err
	}
	if _, err := deneb("mac", "service", "工作室", "web", "tcp/80", "on"); err != nil {
		return err
	}
	if err := httpOK("build", "mac", 5*time.Second); err != nil {
		return err
	}
	if err := httpFails("desktop", "mac"); err != nil {
		return err
	}
	if _, err := deneb("mac", "service", "周末联机", "web", "tcp/80", "on"); err != nil {
		return err
	}
	if err := httpOK("desktop", "mac", 5*time.Second); err != nil {
		return err
	}
	if _, err := deneb("mac", "service", "周末联机", "web", "tcp/80", "off"); err != nil {
		return err
	}
	time.Sleep(time.Second)
	return httpFails("desktop", "mac")
}

func addrOf(n string) (string, error) {
	s, err := state(n)
	if err != nil {
		return "", err
	}
	if s.Self == nil || len(s.Self.Addrs) == 0 {
		return "", fmt.Errorf("%s has no address", n)
	}
	return s.Self.Addrs[0], nil
}

func drops(n string) (map[string]float64, error) {
	out, err := deneb(n, "debug", "drops")
	if err != nil {
		return nil, err
	}
	var m struct {
		Drops map[string]float64 `json:"drops"`
	}
	return m.Drops, json.Unmarshal([]byte(out), &m)
}

// checkTransit is the borrowing attack of multi-lan-tech §6.4 in both
// directions: a member of one LAN routes the other LAN's device through the
// Mac, which is in both. The Mac's filter must drop it.
func checkTransit() error {
	for _, a := range [][2]string{{"build", "desktop"}, {"desktop", "build"}} {
		attacker, victim := a[0], a[1]
		target, err := addrOf(victim)
		if err != nil {
			return err
		}
		before, _ := drops("mac")
		if _, err := deneb(attacker, "debug", "attack", "mac", target+"/32"); err != nil {
			return err
		}
		out, err := deneb(attacker, "debug", "ping-addr", target)
		deneb(attacker, "debug", "unattack", "mac")
		if err != nil {
			return err
		}
		if !strings.Contains(out, `"received":0`) {
			return fmt.Errorf("%s reached %s through the Mac: %s", attacker, victim, out)
		}
		after, _ := drops("mac")
		if after["in:transit-in"] <= before["in:transit-in"] {
			return fmt.Errorf("the Mac did not record a transit drop (%v -> %v)", before["in:transit-in"], after["in:transit-in"])
		}
	}
	return nil
}

// checkSpoof: the Mac skips its own filter and forges a source address;
// the receiving peer's WireGuard must drop it (layer 4).
func checkSpoof() error {
	build, _ := addrOf("build")
	desktop, _ := addrOf("desktop")
	for _, c := range [][3]string{{build, desktop, "desktop"}, {desktop, build, "build"}} {
		since := time.Now().Add(-time.Second).Format(time.RFC3339)
		if _, err := deneb("mac", "debug", "inject", c[0], c[1], "80"); err != nil {
			return err
		}
		_, err := waitFor(5*time.Second, func() (string, error) {
			out, err := logsOf("dnb-"+c[2], since)
			if err == nil && !strings.Contains(out, "disallowed source") {
				err = errors.New("no drop logged yet")
			}
			return "", err
		})
		if err != nil {
			return fmt.Errorf("%s's WireGuard did not drop a packet forged as %s: %v", c[2], c[0], err)
		}
	}
	return nil
}

// checkPause: pausing one LAN on the Mac stops it at once and leaves the
// other alone (multi-lan-tech §6.3).
func checkPause() error {
	if _, err := deneb("mac", "lan", "pause", "周末联机"); err != nil {
		return err
	}
	if err := pingFails("mac", "nas"); err != nil {
		return err
	}
	if err := pingOK("mac", "build", 2*time.Second); err != nil {
		return err
	}
	// The pause reaches the server, and nas stops seeing the Mac too.
	if _, err := waitFor(5*time.Second, func() (string, error) {
		got, err := peersOf("nas")
		if err == nil && slices.Contains(got, "mac") {
			err = errors.New("nas still sees the Mac")
		}
		return "", err
	}); err != nil {
		return err
	}
	if _, err := deneb("mac", "lan", "resume", "周末联机"); err != nil {
		return err
	}
	if err := pingOK("mac", "nas", 5*time.Second); err != nil {
		return err
	}
	return pingOK("nas", "mac", 5*time.Second)
}

// tick holds a long connection for secs seconds and returns its report.
type tickResult struct {
	Lines    int    `json:"lines"`
	MaxGapMs int64  `json:"maxGapMs"`
	Broken   bool   `json:"broken"`
	Error    string `json:"error"`
}

func tick(from, to string, secs int) (tickResult, error) {
	var r tickResult
	out, err := deneb(from, "debug", "tick", to, fmt.Sprint(secs))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal([]byte(out), &r)
}

func tickAsync(from, to string, secs int) func() (tickResult, error) {
	var r tickResult
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r, err = tick(from, to, secs) }()
	return func() (tickResult, error) { wg.Wait(); return r, err }
}

// checkAddRemoveUndo is acceptance item 7, with a long connection in the
// other LAN running throughout (item 5: the other LAN is not disturbed).
func checkAddRemoveUndo() error {
	studio := tickAsync("mac", "build", 14)
	time.Sleep(time.Second)
	if _, err := deneb("mac", "lan", "add", "周末联机", "构建服务器"); err != nil {
		return err
	}
	if err := pingOK("nas", "build", 6*time.Second); err != nil {
		return err
	}
	t0 := time.Now()
	if _, err := deneb("mac", "lan", "remove", "周末联机", "构建服务器"); err != nil {
		return err
	}
	if err := pingFails("nas", "build"); err != nil {
		return err
	}
	if d := time.Since(t0); d > 3*time.Second {
		return fmt.Errorf("removal took %s to take effect", d)
	}
	if _, err := deneb("mac", "lan", "add", "周末联机", "构建服务器"); err != nil { // the undo
		return err
	}
	if err := pingOK("nas", "build", 6*time.Second); err != nil {
		return err
	}
	deneb("mac", "lan", "remove", "周末联机", "构建服务器")
	r, err := studio()
	if err != nil {
		return err
	}
	if r.Broken || r.MaxGapMs > 1000 {
		return fmt.Errorf("工作室's long connection was disturbed: %+v", r)
	}
	return nil
}

// checkRefuse is acceptance item 9: the Mac's owner refuses 阿杰 on the Mac.
func checkRefuse() error {
	if _, err := deneb("mac", "refuse", "这台电脑", "jie"); err != nil {
		return err
	}
	_, err := waitFor(5*time.Second, func() (string, error) {
		out, err := ping("desktop", "mac", 1)
		if err == nil || !strings.Contains(err.Error(), "没让你用") {
			return out, fmt.Errorf("desktop -> mac should be refused: %v", err)
		}
		return out, nil
	})
	if err != nil {
		return err
	}
	if err := httpFails("desktop", "mac"); err != nil {
		return err
	}
	if err := pingOK("mac", "desktop", 3*time.Second); err != nil {
		return fmt.Errorf("refusing 阿杰 must not stop the Mac from using his devices: %w", err)
	}
	if _, err := deneb("mac", "refuse", "这台电脑", "jie", "--undo"); err != nil {
		return err
	}
	return pingOK("desktop", "mac", 5*time.Second)
}

// checkUDPBlock is acceptance item 10: UDP between the Mac and build is
// dropped inside the engine; traffic moves to the relay without breaking a
// long connection, and comes back to direct once UDP flows.
func checkUDPBlock() error {
	if err := waitPath("mac", "build", apitype.PathDirect, 10*time.Second); err != nil {
		return err
	}
	long := tickAsync("mac", "build", 16)
	time.Sleep(3 * time.Second)
	t0 := time.Now()
	if _, err := deneb("mac", "debug", "block", "build"); err != nil {
		return err
	}
	if err := waitPath("mac", "build", apitype.PathRelay, 6*time.Second); err != nil {
		return err
	}
	toRelay := time.Since(t0)
	time.Sleep(4 * time.Second)
	t1 := time.Now()
	deneb("mac", "debug", "unblock", "build")
	if err := waitPath("mac", "build", apitype.PathDirect, 6*time.Second); err != nil {
		return err
	}
	back := time.Since(t1)
	r, err := long()
	if err != nil {
		return err
	}
	fmt.Printf("     udp-block: relay after %s, direct again after %s, long connection %d lines, longest stall %d ms\n",
		toRelay.Round(100*time.Millisecond), back.Round(100*time.Millisecond), r.Lines, r.MaxGapMs)
	if r.Broken || r.MaxGapMs > 4000 {
		return fmt.Errorf("long connection: %+v", r)
	}
	return nil
}

// checkTwoLANs holds a long connection into each LAN at once (item 4).
func checkTwoLANs() error {
	a := tickAsync("mac", "build", 10)
	b := tickAsync("mac", "desktop", 10)
	c := tickAsync("mac", "nas", 10)
	for name, f := range map[string]func() (tickResult, error){"build": a, "desktop": b, "nas": c} {
		r, err := f()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if r.Broken || r.Lines < 80 || r.MaxGapMs > 1500 {
			return fmt.Errorf("long connection to %s: %+v", name, r)
		}
	}
	return nil
}

// checkControlDown: with the control server gone, the engines keep the
// last network map and stay connected (§2.6.1).
func checkControlDown() error {
	if _, err := docker("stop", "dnb-control"); err != nil {
		return err
	}
	defer docker("start", "dnb-control")
	time.Sleep(2 * time.Second)
	if err := pingOK("mac", "build", 3*time.Second); err != nil {
		return err
	}
	if err := pingOK("mac", "desktop", 3*time.Second); err != nil {
		return err
	}
	if _, err := docker("start", "dnb-control"); err != nil {
		return err
	}
	_, err := waitFor(40*time.Second, func() (string, error) {
		s, err := state("mac")
		if err == nil && !s.ControlConnected {
			err = errors.New("not reconnected")
		}
		return "", err
	})
	return err
}

// checkLogoutLogin: nas logs out; its peers lose it at once and it keeps a
// fresh login link ready; logging in again brings everything back.
func checkLogoutLogin() error {
	if _, err := deneb("nas", "logout"); err != nil {
		return err
	}
	if _, err := waitFor(5*time.Second, func() (string, error) {
		got, err := peersOf("mac")
		if err == nil && slices.Contains(got, "nas") {
			err = errors.New("mac still sees nas")
		}
		return "", err
	}); err != nil {
		return err
	}
	link, err := waitFor(10*time.Second, func() (string, error) {
		s, err := state("nas")
		if err == nil && (s.LoggedIn || s.LoginURL == "") {
			err = errors.New("no login link ready after logout")
		}
		if err != nil {
			return "", err
		}
		return s.LoginURL, nil
	})
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"login":%q,"email":"jie@deneb.test"}`, link)
	if _, err := docker("exec", "dnb-build", "curl", "-sfk", "-X", "POST", "https://"+ctlIP+":"+ctlPort+"/dev/approve", "-d", body); err != nil {
		return err
	}
	// It comes back as the same device, still in 周末联机.
	return pingOK("mac", "nas", 15*time.Second)
}

// checkRestart restarts the Mac's engine: it comes back from its saved map
// at once, answers through the relay first and goes direct soon after.
func checkRestart() error {
	if _, err := docker("restart", "-t", "2", "dnb-mac"); err != nil {
		return err
	}
	t0 := time.Now()
	if err := pingOK("mac", "build", 15*time.Second); err != nil {
		return err
	}
	first := time.Since(t0)
	if err := waitPath("mac", "build", apitype.PathDirect, 10*time.Second); err != nil {
		return err
	}
	fmt.Printf("     restart: first answer %s after the container restarted, direct after %s\n",
		first.Round(100*time.Millisecond), time.Since(t0).Round(100*time.Millisecond))
	// The other direction: peers that still hold a session with the old
	// engine must not wait for WireGuard to give up on it (15 s).
	for _, n := range []string{"nas", "desktop"} {
		t := time.Now()
		if err := pingOK(n, "mac", 4*time.Second); err != nil {
			return fmt.Errorf("after the Mac restarted: %w", err)
		}
		fmt.Printf("     restart: %s reached the restarted Mac after %s\n", n, time.Since(t).Round(100*time.Millisecond))
	}
	return nil
}

func dumpLogs(run string) {
	os.MkdirAll(run, 0o755)
	for _, n := range []string{"control", "derper", "derper2", "mac", "build", "desktop", "nas", "isp-a", "isp-b", "router-a", "router-b"} {
		out, _ := logsOf("dnb-"+n, "")
		os.WriteFile(filepath.Join(run, n+".log"), []byte(out), 0o644)
	}
}
