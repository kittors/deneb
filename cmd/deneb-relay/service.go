package main

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What starts the relay at boot and again when it fails
// (design/plan/m7/relay-install.md §5.1).
const (
	initSystemd = "systemd" // most distributions
	initOpenRC  = "openrc"  // Alpine, Gentoo
	initProcd   = "procd"   // OpenWrt
	initCron    = "cron"    // no service manager, but cron runs
	initNone    = "none"    // nothing at all: a container, a chroot
)

const (
	scopeSystem = "system" // installed as root
	scopeUser   = "user"   // no root: the installing user's service
)

const serviceName = "deneb-relay"

// relayUser is the user a system install runs the relay as: one of its
// own, with no shell and no home of its own.
const relayUser = "deneb-relay"

// layout is where the relay goes.
type layout struct {
	scope  string
	binDir string // the program's directory, the relay's own: it updates itself there
	bin    string
	link   string // a link on the PATH, so that `deneb-relay status` works
	data   string // secret, certificate, keys, config.json
}

func (h *host) layout() layout {
	if h.isRoot() {
		data := "/var/lib/deneb-relay"
		if h.exists("/etc/openwrt_release") {
			data = "/etc/deneb-relay" // /var is in memory on OpenWrt
		}
		return layout{scope: scopeSystem, binDir: "/usr/local/lib/deneb-relay", bin: "/usr/local/lib/deneb-relay/deneb-relay",
			link: "/usr/local/bin/deneb-relay", data: data}
	}
	dataHome := h.getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(h.home, ".local/share")
	}
	binDir := filepath.Join(h.home, ".local/lib/deneb-relay")
	return layout{scope: scopeUser, binDir: binDir, bin: filepath.Join(binDir, "deneb-relay"),
		link: filepath.Join(h.home, ".local/bin/deneb-relay"), data: filepath.Join(dataHome, "deneb-relay")}
}

// detectInit finds what will keep the relay running, and says why in
// words for the install's output.
func (h *host) detectInit(scope string) (string, string) {
	systemd := h.exists("/run/systemd/system") && h.has("systemctl")
	cron := h.cronRunning() && h.has("crontab")
	if scope == scopeUser {
		if systemd {
			if _, err := h.run("systemctl", "--user", "show-environment"); err == nil {
				return initSystemd, "当前用户的服务（systemd）"
			}
		}
		if cron {
			return initCron, "当前用户的定时任务（cron）"
		}
		return initNone, ""
	}
	switch {
	case systemd:
		return initSystemd, "系统服务（systemd）"
	case h.exists("/sbin/openrc-run") && h.exists("/run/openrc/softlevel"):
		return initOpenRC, "系统服务（OpenRC）"
	case h.exists("/sbin/procd") && h.exists("/etc/rc.common"):
		return initProcd, "系统服务（procd）"
	case cron:
		return initCron, "定时任务（cron）"
	}
	return initNone, ""
}

// cronRunning: a cron daemon runs.
func (h *host) cronRunning() bool {
	p := h.procComms()
	return p["cron"] || p["crond"] || p["cronie"] || p["fcron"]
}

// spec is what a service runs.
type spec struct {
	bin, data string
	user      string // a system install's user; "" runs as root
	log       string // its own log file, where no journal takes its output
	lowPort   bool   // a port below 1024: systemd lets it bind one
}

// runArgs is the relay's command line in a service.
func (s spec) runArgs() []string {
	a := []string{s.bin, "run", "-data", s.data}
	if s.log != "" {
		a = append(a, "-log", s.log)
	}
	return a
}

// systemdUnit is the unit of a system or user service: restarted when it
// fails, given up after ten failures in ten minutes, held to one CPU and
// 256 MB (as the Docker container was), its journal rate-limited, and a
// system one run as its own user with nothing it does not need.
func systemdUnit(s spec, scope string) string {
	var b strings.Builder
	b.WriteString(`# Written by deneb-relay install; deneb-relay install writes it again,
# deneb-relay uninstall removes it.
[Unit]
Description=Deneb relay
Documentation=https://deneb.app
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=600
StartLimitBurst=10

[Service]
Type=simple
`)
	if scope == scopeSystem && s.user != "" {
		fmt.Fprintf(&b, "User=%s\nGroup=%s\n", s.user, s.user)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", systemdArgs(s.runArgs()))
	b.WriteString(`Restart=always
RestartSec=5
CPUQuota=100%
MemoryMax=256M
MemorySwapMax=0
TasksMax=256
LimitNOFILE=65536
LogRateLimitIntervalSec=30s
LogRateLimitBurst=1000
UMask=0077
`)
	if scope == scopeSystem {
		fmt.Fprintf(&b, `NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=%s %s
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
LockPersonality=yes
`, systemdQuote(s.data), systemdQuote(filepath.Dir(s.bin)))
		if s.lowPort {
			b.WriteString("CapabilityBoundingSet=CAP_NET_BIND_SERVICE\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n")
		} else {
			b.WriteString("CapabilityBoundingSet=\nAmbientCapabilities=\n")
		}
	}
	b.WriteString("\n[Install]\n")
	if scope == scopeSystem {
		b.WriteString("WantedBy=multi-user.target\n")
	} else {
		b.WriteString("WantedBy=default.target\n")
	}
	return b.String()
}

// systemdQuote quotes a word for a unit file, if it needs it.
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\%$;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

func systemdArgs(a []string) string {
	q := make([]string, len(a))
	for i, s := range a {
		q[i] = systemdQuote(s)
	}
	return strings.Join(q, " ")
}

// openrcScript is an OpenRC service, supervised: restarted 5 s after it
// fails, given up after ten failures in ten minutes. OpenRC keeps no
// journal: the relay writes its own log, which it keeps under 30 MB.
func openrcScript(s spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `#!/sbin/openrc-run
# Written by deneb-relay install; deneb-relay uninstall removes it.
name="deneb-relay"
description="Deneb relay"
command=%s
command_args=%s
`, shellQuote(s.bin), shellQuote(shellJoin(s.runArgs()[1:])))
	if s.user != "" {
		fmt.Fprintf(&b, "command_user=%s\n", shellQuote(s.user+":"+s.user))
	}
	b.WriteString(`supervisor=supervise-daemon
respawn_delay=5
respawn_max=10
respawn_period=600
rc_ulimit="-n 65536"

depend() {
	need net
	after firewall
}
`)
	return b.String()
}

// procdScript is an OpenWrt service: procd restarts it 5 s after it
// fails, gives up after ten failures within ten minutes, and takes its
// output into the system log, which lives in memory.
func procdScript(s spec) string {
	var b strings.Builder
	b.WriteString(`#!/bin/sh /etc/rc.common
# Written by deneb-relay install; deneb-relay uninstall removes it.
START=99
STOP=10
USE_PROCD=1

start_service() {
	procd_open_instance
`)
	fmt.Fprintf(&b, "\tprocd_set_param command %s\n", shellJoin(s.runArgs()))
	if s.user != "" {
		fmt.Fprintf(&b, "\tprocd_set_param user %s\n", shellQuote(s.user))
	}
	b.WriteString(`	procd_set_param respawn 600 5 10
	procd_set_param limits nofile="65536 65536"
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
`)
	return b.String()
}

// cronMark ends the crontab lines install writes, so that it finds them
// again: a comment for sh, which cron hands the line to.
const cronMark = "# deneb-relay"

// cronLines start the relay at boot, and again within a minute if it
// stopped: -if-stopped makes the minute's start a no-op while it runs.
func cronLines(s spec) []string {
	cmd := shellJoin(append(s.runArgs(), "-if-stopped"))
	// cron hands the line to sh; a % would end the command.
	cmd = strings.ReplaceAll(cmd, "%", `\%`)
	return []string{
		"@reboot " + cmd + " " + cronMark,
		"* * * * * " + cmd + " " + cronMark,
	}
}

// withCronLines is a crontab with install's lines replaced by lines (none
// to take them out).
func withCronLines(crontab string, lines []string) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(crontab, "\n"), "\n") {
		if l == "" || strings.HasSuffix(strings.TrimSpace(l), cronMark) {
			continue
		}
		keep = append(keep, l)
	}
	keep = append(keep, lines...)
	if len(keep) == 0 {
		return ""
	}
	return strings.Join(keep, "\n") + "\n"
}

// serviceFile is where kind's service definition goes.
func (h *host) serviceFile(kind, scope string) string {
	switch kind {
	case initSystemd:
		if scope == scopeUser {
			cfg := h.getenv("XDG_CONFIG_HOME")
			if cfg == "" {
				cfg = filepath.Join(h.home, ".config")
			}
			return filepath.Join(cfg, "systemd/user", serviceName+".service")
		}
		return "/etc/systemd/system/" + serviceName + ".service"
	case initOpenRC, initProcd:
		return "/etc/init.d/" + serviceName
	}
	return ""
}

// systemctl runs systemctl, for the user's own manager in a user install.
func (h *host) systemctl(scope string, args ...string) (string, error) {
	if scope == scopeUser {
		args = append([]string{"--user"}, args...)
	}
	return h.run("systemctl", args...)
}

// crontab runs crontab on the relay user's crontab (the user's own in a
// user install).
func (h *host) crontab(user, input string, args ...string) (string, error) {
	if user != "" {
		args = append([]string{"-u", user}, args...)
	}
	if input != "" || len(args) > 0 && args[len(args)-1] == "-" {
		return h.runIn(input, "crontab", args...)
	}
	return h.run("crontab", args...)
}

// installService writes kind's service for s and (re)starts it; it returns
// where it wrote it, and anything worth saying.
func (h *host) installService(kind, scope string, s spec) (service string, notes []string, err error) {
	fail := func(what string, out string, err error) (string, []string, error) {
		return service, notes, fmt.Errorf("%s：%v%s", what, err, outTail(out))
	}
	switch kind {
	case initSystemd:
		service = h.serviceFile(kind, scope)
		if err := h.write(service, []byte(systemdUnit(s, scope)), 0o644); err != nil {
			return "", nil, err
		}
		for _, a := range [][]string{{"daemon-reload"}, {"enable", serviceName}, {"restart", serviceName}} {
			if out, err := h.systemctl(scope, a...); err != nil {
				return fail("systemctl "+strings.Join(a, " "), out, err)
			}
		}
		if scope == scopeUser {
			// Without lingering, a user's services stop when they log out,
			// and start only once they log in after a reboot.
			if out, err := h.run("loginctl", "enable-linger", h.userName); err != nil {
				notes = append(notes, "没能让它在你退出登录后继续运行（loginctl enable-linger："+strings.TrimSpace(firstLine(out))+"）：请管理员运行 loginctl enable-linger "+h.userName+"，不然重启后要你登录一次它才会运行")
			}
		}
	case initOpenRC:
		service = h.serviceFile(kind, scope)
		if err := h.write(service, []byte(openrcScript(s)), 0o755); err != nil {
			return "", nil, err
		}
		for _, a := range [][]string{{"rc-update", "add", serviceName, "default"}, {"rc-service", serviceName, "restart"}} {
			if out, err := h.run(a[0], a[1:]...); err != nil {
				return fail(strings.Join(a, " "), out, err)
			}
		}
	case initProcd:
		service = h.serviceFile(kind, scope)
		if err := h.write(service, []byte(procdScript(s)), 0o755); err != nil {
			return "", nil, err
		}
		for _, a := range []string{"enable", "restart"} {
			if out, err := h.run(service, a); err != nil {
				return fail(service+" "+a, out, err)
			}
		}
	case initCron:
		user := ""
		if scope == scopeSystem {
			user = s.user
		}
		service = "crontab:" + cmp.Or(user, h.userName)
		cur, err := h.crontab(user, "", "-l")
		if err != nil {
			cur = "" // "no crontab for …"
		}
		if out, err := h.crontab(user, withCronLines(cur, cronLines(s)), "-"); err != nil {
			return fail("crontab", out, err)
		}
		if err := h.restartLoose(s, user); err != nil {
			return service, notes, err
		}
	case initNone:
		user := ""
		if scope == scopeSystem {
			user = s.user
		}
		if err := h.restartLoose(s, user); err != nil {
			return "", notes, err
		}
		notes = append(notes, "这台机器上没有服务管理程序，也没有 cron：重启以后它不会自己运行。请把这一行加进开机运行的脚本："+shellJoin(s.runArgs()))
	default:
		return "", nil, fmt.Errorf("不认识的启动方式 %q", kind)
	}
	return service, notes, nil
}

// restartLoose stops a relay that nothing supervises (cron's, or one
// started by hand) and starts it again, detached.
func (h *host) restartLoose(s spec, user string) error {
	h.stopLoose(s.data)
	args := s.runArgs()
	if err := h.spawn(user, args[0], append(args[1:], "-if-stopped")...); err != nil {
		return fmt.Errorf("启动：%w", err)
	}
	return nil
}

// stopLoose stops the relay running on data, by the pid it noted, and
// waits for it to let go of its data directory: a relay started before
// that would find it taken and, with -if-stopped, leave.
func (h *host) stopLoose(data string) {
	pid, err := strconv.Atoi(strings.TrimSpace(h.read(filepath.Join(data, pidFile))))
	if err != nil || pid <= 1 {
		return
	}
	h.kill(pid)
	for i := 0; i < 100 && locked(h.path(data)); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// removeService stops kind's service and takes it away.
func (h *host) removeService(in *installed, data string) error {
	var errs []error
	note := func(out string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%v%s", err, outTail(out)))
		}
	}
	switch in.Init {
	case initSystemd:
		note(h.systemctl(in.Scope, "disable", "--now", serviceName))
		note("", h.remove(in.Service))
		note(h.systemctl(in.Scope, "daemon-reload"))
	case initOpenRC:
		note(h.run("rc-service", serviceName, "stop"))
		note(h.run("rc-update", "del", serviceName, "default"))
		note("", h.remove(in.Service))
	case initProcd:
		note(h.run(in.Service, "stop"))
		note(h.run(in.Service, "disable"))
		note("", h.remove(in.Service))
	case initCron:
		user := ""
		if in.Scope == scopeSystem {
			user = in.User
		}
		if cur, err := h.crontab(user, "", "-l"); err == nil {
			if rest := withCronLines(cur, nil); rest != "" {
				note(h.crontab(user, rest, "-"))
			} else {
				note(h.crontab(user, "", "-r"))
			}
		}
		h.stopLoose(data)
	default:
		h.stopLoose(data)
	}
	return errors.Join(errs...)
}

// restartService restarts the installed relay: after an update.
func (h *host) restartService(in *installed, data string) error {
	var out string
	var err error
	switch in.Init {
	case initSystemd:
		out, err = h.systemctl(in.Scope, "restart", serviceName)
	case initOpenRC:
		out, err = h.run("rc-service", serviceName, "restart")
	case initProcd:
		out, err = h.run(in.Service, "restart")
	default:
		user := ""
		if in.Scope == scopeSystem {
			user = in.User
		}
		s := spec{bin: in.Bin, data: data, user: in.User, log: in.Log}
		return h.restartLoose(s, user)
	}
	if err != nil {
		return fmt.Errorf("%v%s", err, outTail(out))
	}
	return nil
}

// serviceState says whether the installed service runs, in words.
func (h *host) serviceState(in *installed, data string) (running bool, words string) {
	switch in.Init {
	case initSystemd:
		out, _ := h.systemctl(in.Scope, "is-active", serviceName)
		st := strings.TrimSpace(firstLine(out))
		return st == "active", st
	case initOpenRC:
		out, err := h.run("rc-service", serviceName, "status")
		return err == nil, strings.TrimSpace(firstLine(out))
	}
	// procd, cron, nothing: the relay holds its data directory's lock.
	if locked(h.path(data)) {
		return true, "running"
	}
	return false, "stopped"
}

// recentLog is the service's last lines of log, for a failure.
func (h *host) recentLog(in *installed, n int) string {
	switch {
	case in.Init == initSystemd:
		args := []string{"-u", serviceName, "-n", strconv.Itoa(n), "--no-pager", "-o", "cat"}
		if in.Scope == scopeUser {
			args = append([]string{"--user"}, args...)
		}
		out, _ := h.run("journalctl", args...)
		return strings.TrimSpace(out)
	case in.Init == initProcd:
		out, _ := h.run("logread", "-e", serviceName)
		return lastLines(out, n)
	case in.Log != "":
		return lastLines(h.read(in.Log), n)
	}
	return ""
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// outTail is a command's output, short, for an error.
func outTail(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return "（" + lastLines(out, 3) + "）"
}
