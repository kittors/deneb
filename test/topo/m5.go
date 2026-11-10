package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// M5's Linux engine (plan §3 M5, m5-acceptance l1 and l2): a Debian or
// Ubuntu machine with systemd, the deneb package installed, the engine in
// kernel mode as a systemd service, logged in as one more of kittors's
// devices and added to 工作室. Then the server package on the same machine.
var m5Distros = flag.String("m5", "", "M5's Linux checks on these distributions, each in a systemd container: debian, debian12, ubuntu, ubuntu26")

var m5Bases = map[string]string{
	"debian":   "debian:stable-slim", // 13
	"debian12": "debian:12-slim",
	"ubuntu":   "ubuntu:24.04",
	"ubuntu26": "ubuntu:26.04",
}

type m5Check struct {
	name string
	fn   func() error
}

func m5Checks() []m5Check {
	var cs []m5Check
	for i, d := range strings.Split(*m5Distros, ",") {
		if d == "" {
			continue
		}
		d, ip := d, fmt.Sprintf("198.51.100.%d", 40+2*i)
		cs = append(cs, m5Check{"linux-" + d, func() error { return checkLinux(d, ip) }})
	}
	return cs
}

type m5State struct {
	LoggedIn bool   `json:"loggedIn"`
	Mode     string `json:"mode"`
	Engine   struct {
		Problems []struct {
			Code string `json:"code"`
		} `json:"problems"`
	} `json:"engine"`
	Self *struct {
		Addrs []string `json:"addrs"`
	} `json:"self"`
	Peers []struct {
		Name  string   `json:"name"`
		FQDN  string   `json:"fqdn"`
		Addrs []string `json:"addrs"`
	} `json:"peers"`
}

func firstV4Of(addrs []string) string {
	for _, a := range addrs {
		if strings.Count(a, ".") == 3 {
			return a
		}
	}
	return ""
}

func checkLinux(distro, ip string) error {
	base, ok := m5Bases[distro]
	if !ok {
		return fmt.Errorf("-m5 %q: one of debian, debian12, ubuntu, ubuntu26", distro)
	}
	root, _ := os.Getwd()
	dir := filepath.Join(root, "test", "topo")
	img := "deneb-m5-" + distro
	if _, err := docker("build", "-q", "-t", img, "--build-arg", "BASE="+base, filepath.Join(dir, "m5")); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	arch := "arm64"
	if out, err := docker("info", "--format", "{{.Architecture}}"); err == nil && strings.Contains(out, "x86_64") {
		arch = "amd64"
	}
	version := "0.5.0~topo"
	if _, err := shEnv([]string{"VERSION=" + version, "TAGS=denebtest", "DENEB_TEST_PACKAGE=1"}, filepath.Join(root, "deploy", "linux", "build-deb.sh"), arch); err != nil {
		return fmt.Errorf("packages: %w", err)
	}
	pkgs := filepath.Join(root, "deploy", "linux", "out")
	name := "dnb-lin-" + distro
	certs := filepath.Join(dir, ".run", "fake", "certs")
	if _, err := docker("run", "-d", "--name", name, "--hostname", "lin-"+distro, "--network", "dnb-net", "--ip", ip,
		"--privileged", "--cgroupns=private", "--tmpfs", "/run", "--tmpfs", "/run/lock",
		"-v", certs+":/certs:ro", "-v", pkgs+":/pkg:ro", img); err != nil {
		return err
	}
	defer docker("rm", "-f", name)
	lx := func(args ...string) (string, error) { return docker(append([]string{"exec", name}, args...)...) }
	lsh := func(script string) (string, error) { return lx("sh", "-c", script) }
	if _, err := waitFor(60*time.Second, func() (string, error) {
		out, _ := lx("systemctl", "is-system-running")
		if s := strings.TrimSpace(out); s != "running" && s != "degraded" {
			return "", fmt.Errorf("systemd: %q", s)
		}
		return "", nil
	}); err != nil {
		return err
	}
	st := func() (*m5State, error) {
		out, err := lx("deneb", "status", "--json")
		if err != nil {
			return nil, err
		}
		var s m5State
		return &s, json.Unmarshal([]byte(out), &s)
	}
	journal := func() string {
		out, _ := lx("journalctl", "-u", "denebd", "--no-pager", "-o", "cat")
		return out
	}
	fail := func(err error) error {
		j := journal()
		if len(j) > 3000 {
			j = j[len(j)-3000:]
		}
		return fmt.Errorf("%w\n---- %s: journalctl -u denebd (end) ----\n%s", err, name, j)
	}

	// The desktop package: enabled and started by its postinst, then given
	// this topology's control server. Installed as `sudo apt install` would
	// by jie (SUDO_UID), who may then use the engine without sudo; guest,
	// another user, may not (M6 i3).
	if out, err := lsh("useradd -m -u 1500 jie && useradd -m -u 1501 guest"); err != nil {
		return fmt.Errorf("useradd: %v %s", err, out)
	}
	if out, err := docker("exec", "-e", "SUDO_UID=1500", name, "dpkg", "-i", fmt.Sprintf("/pkg/denebd_%s_%s.deb", version, arch)); err != nil {
		return fmt.Errorf("dpkg -i denebd: %v %s", err, out)
	}
	conf := fmt.Sprintf("DENEB_MODE=kernel\nDENEB_CONTROL=https://%s:%s\nDENEB_CONTROL_PIN=%s\nDENEB_FLAGS=\"-relay-ca /certs/relays.pem -name Linux-%s -kind desktop -idle 6s\"\n",
		ctlIP, ctlPort, controlPin, distro)
	if _, err := lsh("cat > /etc/default/denebd <<'EOF'\n" + conf + "EOF\nsystemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	var link string
	if _, err := waitFor(15*time.Second, func() (string, error) {
		out, err := lx("deneb", "login", "--no-wait")
		link = loginRE.FindString(out)
		if err == nil && link == "" {
			err = fmt.Errorf("no login link in %q", out)
		}
		return "", err
	}); err != nil {
		return fail(err)
	}
	body := fmt.Sprintf(`{"login":%q,"email":"kittors@deneb.test"}`, link)
	if _, err := docker("exec", "dnb-build", "curl", "-sfk", "-X", "POST", "https://"+ctlIP+":"+ctlPort+"/dev/approve", "-d", body); err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	if _, err := waitFor(15*time.Second, func() (string, error) {
		s, err := st()
		if err == nil && !s.LoggedIn {
			err = errors.New("not logged in yet")
		}
		return "", err
	}); err != nil {
		return fail(err)
	}
	if out, err := lx("deneb", "lan", "add", "工作室"); err != nil {
		return fail(fmt.Errorf("lan add: %v %s", err, out))
	}
	if out, err := lx("runuser", "-u", "jie", "--", "deneb", "status", "--json"); err != nil || !strings.Contains(out, `"loggedIn": true`) && !strings.Contains(out, `"loggedIn":true`) {
		return fail(fmt.Errorf("the desktop user's deneb status: %v %s", err, out))
	}
	if out, err := lx("runuser", "-u", "guest", "--", "deneb", "status"); err == nil {
		return fail(fmt.Errorf("another user got in: %s", out))
	}

	// l1: the interface, a /32 route per peer, the filter.
	var s *m5State
	var self, buildIP, buildName string
	if _, err := waitFor(20*time.Second, func() (string, error) {
		var err error
		if s, err = st(); err != nil {
			return "", err
		}
		for _, p := range s.Peers {
			if p.Name == "build" {
				buildIP, buildName = firstV4Of(p.Addrs), p.FQDN
			}
		}
		if s.Self != nil {
			self = firstV4Of(s.Self.Addrs)
		}
		if self == "" || buildIP == "" {
			return "", errors.New("no address or no build yet")
		}
		out, err := lx("ip", "-4", "-o", "addr", "show", "dev", "deneb0")
		if err != nil || !strings.Contains(out, self+"/32") {
			return "", fmt.Errorf("deneb0: %v %s", err, out)
		}
		return "", nil
	}); err != nil {
		return fail(err)
	}
	if s.Mode != "kernel" {
		return fail(fmt.Errorf("mode %q, want kernel", s.Mode))
	}
	routes, err := waitFor(10*time.Second, func() (string, error) {
		out, err := lx("ip", "-4", "route", "show", "proto", "68")
		if err == nil && !strings.Contains(out, buildIP+" dev deneb0") {
			err = fmt.Errorf("no route to build %s: %s", buildIP, out)
		}
		return out, err
	})
	if err != nil {
		return fail(err)
	}
	nroutes := strings.Count(strings.TrimSpace(routes), "\n") + 1
	if out, err := lx("ping", "-c", "2", "-W", "2", buildName); err != nil {
		return fail(fmt.Errorf("ping %s by name through the kernel: %v %s", buildName, err, out))
	}
	rtt := regexp.MustCompile(`time=([0-9.]+) ms`).FindStringSubmatch(func() string { o, _ := lx("ping", "-c", "1", "-W", "2", buildName); return o }())
	// Larger than deneb0's MTU: the kernel sends each ping in pieces, and
	// build's netstack answers in pieces (the filter once refused those).
	for _, size := range []string{"1400", "3000"} {
		if out, err := lx("ping", "-c", "2", "-W", "2", "-M", "dont", "-s", size, buildName); err != nil {
			return fail(fmt.Errorf("ping %s with %s bytes (in pieces): %v %s", buildName, size, err, out))
		}
	}
	if out, err := lx("bash", "-c", "exec 3<>/dev/tcp/"+buildName+"/7000 && timeout 3 head -c 16 <&3 | wc -c"); err != nil || strings.TrimSpace(out) == "0" {
		return fail(fmt.Errorf("build's tick service by name, no proxy: %v %q", err, out))
	}

	// l2: systemd-resolved hands the domain, and only it, to the engine.
	res, err := lx("resolvectl", "status", "deneb0")
	if err != nil || !strings.Contains(res, "~deneb.internal") {
		return fail(fmt.Errorf("resolvectl status deneb0: %v %s", err, res))
	}
	// "Default Route: no" from systemd 256 on, "Protocols: -DefaultRoute"
	// before (Debian 12, Ubuntu 24.04).
	if !regexp.MustCompile(`Default Route: no|-DefaultRoute`).MatchString(res) {
		return fail(fmt.Errorf("deneb0 is a default route for names: %s", res))
	}
	if out, err := lx("getent", "hosts", buildName); err != nil || !strings.Contains(out, buildIP) {
		return fail(fmt.Errorf("getent hosts %s: %v %q", buildName, err, out))
	}

	// l1: with IP forwarding on (Docker's default), a packet another host
	// routes through this one into the interface goes nowhere: the filter
	// drops it on the way out (transit-out). The machine's own still go.
	if _, err := lx("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	client := "dnb-lin-client-" + distro
	cip := strings.TrimSuffix(ip, ip[strings.LastIndex(ip, ".")+1:]) + fmt.Sprint(atoiLast(ip)+1)
	if _, err := docker("run", "-d", "--name", client, "--network", "dnb-net", "--ip", cip, "--cap-add", "NET_ADMIN",
		"--entrypoint", "sleep", image, "infinity"); err != nil {
		return err
	}
	defer docker("rm", "-f", client)
	block := buildIP[:strings.LastIndex(buildIP[:strings.LastIndex(buildIP, ".")], ".")] + ".0.0/16"
	if _, err := docker("exec", client, "ip", "route", "add", block, "via", ip); err != nil {
		return err
	}
	if out, err := docker("exec", client, "ping", "-c", "3", "-W", "1", buildIP); err == nil {
		return fail(fmt.Errorf("another host reached build through this one: %s", out))
	}
	if !strings.Contains(journal(), "transit-out") {
		return fail(errors.New("the forwarded packets were not dropped as transit-out"))
	}
	if out, err := lx("ping", "-c", "1", "-W", "2", buildName); err != nil {
		return fail(fmt.Errorf("with forwarding on, this machine's own ping: %v %s", err, out))
	}

	// l2: the service. Stopped, nothing is left; started, all comes back;
	// killed, systemd starts it again and the old interface is gone.
	for _, q := range [][]string{{"is-active", "denebd"}, {"is-enabled", "denebd"}} {
		if out, err := lx(append([]string{"systemctl"}, q...)...); err != nil {
			return fail(fmt.Errorf("systemctl %s: %v %s", q[0], err, out))
		}
	}
	gone := func(what string) error {
		if out, err := lx("ip", "link", "show", "deneb0"); err == nil {
			return fmt.Errorf("%s: deneb0 still there: %s", what, out)
		}
		if out, _ := lx("ip", "-4", "route", "show", "proto", "68"); strings.TrimSpace(out) != "" {
			return fmt.Errorf("%s: routes left: %s", what, out)
		}
		if out, _ := lsh("resolvectl domain; resolvectl dns"); strings.Contains(out, "deneb") {
			return fmt.Errorf("%s: resolver settings left: %s", what, out)
		}
		return nil
	}
	back := func(what string) error {
		_, err := waitFor(15*time.Second, func() (string, error) {
			out, err := lx("ip", "-4", "route", "show", "proto", "68")
			if err == nil && !strings.Contains(out, buildIP+" dev deneb0") {
				err = fmt.Errorf("%s: no route to build yet", what)
			}
			return "", err
		})
		if err == nil {
			_, err = lx("ping", "-c", "1", "-W", "2", buildName)
		}
		return err
	}
	if _, err := lx("systemctl", "stop", "denebd"); err != nil {
		return err
	}
	if err := gone("stopped"); err != nil {
		return fail(err)
	}
	t0 := time.Now()
	lx("systemctl", "reset-failed", "denebd") // this check restarts it more often than systemd's start limit allows
	if _, err := lx("systemctl", "start", "denebd"); err != nil {
		return err
	}
	if err := back("started"); err != nil {
		return fail(err)
	}
	restart := time.Since(t0)
	t0 = time.Now()
	// The main process only: systemctl kill trips on Ubuntu 24.04's
	// systemd ("auxiliary processes").
	if _, err := lsh("kill -9 $(systemctl show -p MainPID --value denebd)"); err != nil {
		return err
	}
	if err := back("killed"); err != nil {
		return fail(err)
	}
	afterKill := time.Since(t0)
	if out, _ := lx("ip", "-o", "link", "show"); strings.Contains(out, "deneb1") {
		return fail(fmt.Errorf("a second interface after the kill: %s", out))
	}

	// l2: without systemd-resolved in the hosts line (and Docker's own
	// resolv.conf), names could not reach the engine: user-space instead.
	if _, err := lsh("cp /etc/nsswitch.conf /etc/nsswitch.conf.deneb && sed -i 's/ resolve[^ ]*//' /etc/nsswitch.conf && systemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	if _, err := waitFor(15*time.Second, func() (string, error) {
		s, err := st()
		if err == nil && (s.Mode != "userspace" || !s.LoggedIn) {
			err = fmt.Errorf("mode %q", s.Mode)
		}
		return "", err
	}); err != nil {
		return fail(fmt.Errorf("no systemd-resolved: %w", err))
	}
	if err := gone("user-space fallback"); err != nil {
		return fail(err)
	}
	if !strings.Contains(journal(), "running in user-space mode instead") {
		return fail(errors.New("the fallback is not in the log"))
	}
	if out, err := lx("deneb", "ping", "build", "-c", "1"); err != nil {
		return fail(fmt.Errorf("ping build in user-space mode: %v %s", err, out))
	}
	if _, err := lsh("mv /etc/nsswitch.conf.deneb /etc/nsswitch.conf && systemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	if err := back("resolved back"); err != nil {
		return fail(err)
	}

	// l1: another network's route inside the block (a Docker network, a
	// VPN's): a /32 per peer would cut into it, so user-space instead; back
	// to kernel mode once it is gone.
	other := block[:strings.LastIndex(block, ".0.0/16")] + ".250.0/24"
	if _, err := lsh("ip route add " + other + " dev eth0 && systemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	if _, err := waitFor(15*time.Second, func() (string, error) {
		s, err := st()
		if err == nil && (s.Mode != "userspace" || !s.LoggedIn) {
			err = fmt.Errorf("mode %q with %s routed elsewhere", s.Mode, other)
		}
		return "", err
	}); err != nil {
		return fail(err)
	}
	if !strings.Contains(journal(), "the address block is in use here") {
		return fail(errors.New("the conflict is not in the log"))
	}
	if _, err := lsh("ip route del " + other + " dev eth0 && systemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	if err := back("conflict gone"); err != nil {
		return fail(err)
	}

	// The server package: engine and command line only, user-space mode,
	// the same device (its keys stay in /var/lib/deneb).
	if out, err := lx("dpkg", "-r", "denebd"); err != nil {
		return fmt.Errorf("dpkg -r denebd: %v %s", err, out)
	}
	if err := gone("removed"); err != nil {
		return fail(err)
	}
	// --force-confnew: /etc/default/denebd stays behind denebd (removed, not
	// purged); the server package's own goes in, without dpkg asking.
	if out, err := lx("dpkg", "--force-confnew", "-i", fmt.Sprintf("/pkg/deneb-server_%s_%s.deb", version, arch)); err != nil {
		return fmt.Errorf("dpkg -i deneb-server: %v %s", err, out)
	}
	if _, err := lsh("sed -i 's|^DENEB_FLAGS=.*|DENEB_FLAGS=\"-relay-ca /certs/relays.pem -kind server\"|' /etc/default/denebd && systemctl reset-failed denebd; systemctl restart denebd"); err != nil {
		return err
	}
	if _, err := waitFor(15*time.Second, func() (string, error) {
		s, err := st()
		if err == nil && (s.Mode != "userspace" || !s.LoggedIn) {
			err = fmt.Errorf("server package: mode %q, logged in %v", s.Mode, s.LoggedIn)
		}
		return "", err
	}); err != nil {
		return fail(err)
	}
	if _, err := waitFor(10*time.Second, func() (string, error) { return lx("deneb", "ping", "build", "-c", "1") }); err != nil {
		return fail(fmt.Errorf("server package: ping build: %w", err))
	}
	if err := gone("server package"); err != nil {
		return fail(err)
	}
	if out, err := lx("dpkg", "-P", "deneb-server"); err != nil {
		return fmt.Errorf("dpkg -P: %v %s", err, out)
	}
	if out, err := lsh("for f in /var/lib/deneb /etc/default/denebd /usr/lib/systemd/system/denebd.service /usr/bin/denebd; do [ ! -e $f ] || echo left: $f; done"); err != nil || strings.TrimSpace(out) != "" {
		return fmt.Errorf("after purge: %v %s", err, out)
	}
	ms := ""
	if rtt != nil {
		ms = rtt[1] + " ms"
	}
	fmt.Printf("     linux-%s: deneb0 %s/32, %d routes (proto 68); ping by name %s, 1400 and 3000 bytes in pieces too; the installing user's deneb works without sudo, another user's is refused; resolved ~deneb.internal, not a default route; forwarded packets dropped (transit-out); restart %s, after a kill %s; no resolved, or a route inside the block: user-space; server package user-space; purge clean\n",
		distro, self, nroutes, ms, restart.Round(100*time.Millisecond), afterKill.Round(100*time.Millisecond))
	return nil
}

func atoiLast(ip string) int {
	var n int
	fmt.Sscanf(ip[strings.LastIndex(ip, ".")+1:], "%d", &n)
	return n
}

// shEnv runs a command with more environment.
func shEnv(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
