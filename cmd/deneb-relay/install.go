package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// install, status, uninstall and update, as their users see them
// (design/plan/m7/relay-install.md §3): one line a step, a mark in
// front, and when something is wrong, what to do about it on the line
// below.

// steps prints install's lines.
type steps struct {
	w      io.Writer
	failed bool
}

func (s *steps) ok(f string, a ...any)   { fmt.Fprintf(s.w, "✓ "+f+"\n", a...) }
func (s *steps) warn(f string, a ...any) { fmt.Fprintf(s.w, "! "+f+"\n", a...) }
func (s *steps) note(f string, a ...any) { fmt.Fprintf(s.w, "  "+f+"\n", a...) }
func (s *steps) doing(f string, a ...any) {
	fmt.Fprintf(s.w, "… "+f+"\n", a...)
}

func (s *steps) fail(f string, a ...any) {
	s.failed = true
	fmt.Fprintf(s.w, "✗ "+f+"\n", a...)
}

// notes prints each of lines under the last step.
func (s *steps) notes(lines []string) {
	for _, l := range lines {
		s.note("%s", l)
	}
}

// installOpts are install's flags.
type installOpts struct {
	join             string
	yes, no          bool
	firewall         string // "ask", "open", "skip"
	region           string
	ipv4, ipv6       string
	tcpPort, udpPort int
	noUpdate         bool
	noPortMap        bool
}

func cmdInstall(args []string) int {
	var o installOpts
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.StringVar(&o.join, "join", os.Getenv("DENEB_RELAY_JOIN"), "the join code Deneb gave, in its install command")
	fs.BoolVar(&o.yes, "yes", false, "answer yes to install's questions: let the ports through the firewall, take over a Docker relay")
	fs.BoolVar(&o.no, "no", false, "answer no to install's questions")
	fs.StringVar(&o.firewall, "firewall", "ask", "the system's firewall: ask, open, or skip (only say what to run)")
	fs.StringVar(&o.region, "region", "", `where the relay is, for its name: "香港" (default: found)`)
	fs.StringVar(&o.ipv4, "ipv4", "", "the IPv4 address devices reach it at (default: found)")
	fs.StringVar(&o.ipv6, "ipv6", "", "the IPv6 address devices reach it at (default: found)")
	fs.IntVar(&o.tcpPort, "tcp-port", 0, "the TCP port devices connect to (default 47445, or the next free one)")
	fs.IntVar(&o.udpPort, "udp-port", 0, "the UDP port for STUN (default 47478, or the next free one)")
	fs.BoolVar(&o.noUpdate, "no-update", false, "do not follow Deneb's releases")
	fs.BoolVar(&o.noPortMap, "no-port-map", false, "do not ask the router to forward the ports")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.join == "" && fs.NArg() > 0 {
		o.join = fs.Arg(0)
	}
	if runtime.GOOS != "linux" {
		fmt.Println("中转站只能装在 Linux 上。")
		return 1
	}
	return realHost().install(context.Background(), o)
}

// Exit codes of install: notReachable when it runs but devices cannot
// reach it from outside yet.
const (
	exitOK           = 0
	exitFailed       = 1
	exitNotReachable = 3
)

func (h *host) install(ctx context.Context, o installOpts) int {
	st := &steps{w: h.out}
	l := h.layout()
	kind, how := h.detectInit(l.scope)
	sys := "Linux " + arch()
	if n := h.osName(); n != "" {
		sys += "（" + n + "）"
	}
	st.ok("系统：%s", sys)
	if l.scope == scopeUser {
		st.warn("没有 root：装成 %s 自己的服务；系统防火墙要请管理员放行", h.userName)
	}

	// What is here already: an earlier install, or a relay in Docker.
	s, err := loadSettings(h.path(l.data))
	if err != nil {
		st.fail("读不了 %s：%v", filepath.Join(l.data, settingsFile), err)
		return exitFailed
	}
	var prev *installed
	if s == nil {
		s = &settings{}
	}
	prev = s.Install
	var docker *dockerRelay
	if prev == nil && l.scope == scopeSystem {
		d, err := h.findDockerRelay()
		if err != nil {
			st.warn("%v", err)
		}
		if d != nil && o.ask(h, fmt.Sprintf("这台上有以前用 Docker 装的中转站（容器 %s，数据在 %s）。要迁过来、沿用原来的身份吗？", d.name, d.data)) {
			docker = d
		} else if d != nil {
			st.note("容器 %s 留着没动", d.name)
		}
	}

	// The control server: from the join code, or what was installed.
	var token string
	if o.join != "" {
		control, pin, tok, err := proto.ParseRelayJoinCode(o.join)
		if err != nil {
			st.fail("加入码不对：%v", err)
			st.note("请在 Deneb 里重新复制安装命令")
			return exitFailed
		}
		s.Control, s.ControlPin, token = control, pin, tok
	}
	if docker != nil && s.Control == "" {
		s.Control, s.ControlPin = docker.settings.Control, docker.settings.ControlPin
	}
	if s.Control == "" {
		st.fail("要给加入码")
		st.note("在 Deneb 里点“用我自己的服务器当中转站”，复制那一行命令到这台服务器上运行")
		return exitFailed
	}
	ctl, err := newControlClient(s.Control, s.ControlPin)
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	skew, err := clockSkew(ctx, ctl)
	switch {
	case err != nil:
		st.fail("连不上 Deneb 服务器：%v", err)
		st.note("这台服务器要能访问外网；要走代理的，请设好 HTTPS_PROXY 再运行")
		return exitFailed
	case abs(skew) > skewBad:
		st.fail("系统时间不对：比 Deneb 服务器%s", skewWords(skew))
		st.note("请先把时间校准再运行：timedatectl set-ntp true，或者装 chrony、ntp")
		return exitFailed
	case abs(skew) > skewWarn:
		st.warn("系统时间%s，最好校准一下（timedatectl set-ntp true）", skewWords(skew))
	}
	if p := proxyInUse(); p != "" {
		st.ok("经代理访问外网：%s", p)
	}

	// Room for the program, its updates and logs.
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		st.fail("找不到自己：%v", err)
		return exitFailed
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	if free, ok := h.freeSpace(existingDir(h.path(l.binDir))); ok {
		if need := diskNeed(selfInfo.Size()); free < need {
			st.fail("磁盘空间不够：要 %s，只剩 %s", mb(need), mb(free))
			return exitFailed
		} else {
			st.ok("磁盘：剩 %s", mb(free))
		}
	}

	// What this run makes, taken back if it fails before the relay is
	// registered: a failed install leaves the machine as it found it.
	// Once registered, what is here is written in config.json instead, for
	// a run again to finish or uninstall to take away.
	var made []func()
	abort := func() int {
		for i := len(made) - 1; i >= 0; i-- {
			made[i]()
		}
		if len(made) > 0 {
			st.note("这台上已经改动的都还原了")
		}
		return exitFailed
	}
	if !h.exists(l.data) {
		made = append(made, func() { h.remove(l.data) })
	}

	// Take over the Docker relay first: it frees its ports.
	if docker != nil {
		ds, err := h.takeOverDocker(docker, l.data)
		if err != nil {
			st.fail("迁 Docker 里的中转站：%v", err)
			h.giveBack(docker)
			return abort()
		}
		made = append(made, func() { h.giveBack(docker) })
		ds.Control, ds.ControlPin = s.Control, s.ControlPin
		*s = *ds
		st.ok("已迁过来：Docker 容器 %s 停下了，留着没删", docker.name)
	}

	// Ports: those asked for, else the ones it had, else the defaults;
	// taken by something else, the next free pair.
	oldTCP, oldUDP := s.tcpPort(), s.udpPort()
	ours := locked(h.path(l.data)) // our relay holds its own
	wantTCP, wantUDP := o.tcpPort, o.udpPort
	if wantTCP == 0 {
		wantTCP = s.tcpPort()
	}
	if wantUDP == 0 {
		wantUDP = s.udpPort()
	}
	tcpOK := func(p int) bool { return ours && p == s.tcpPort() || h.tcpFree(p) }
	udpOK := func(p int) bool { return ours && p == s.udpPort() || h.udpFree(p) }
	tcp, udp, ok := wantTCP, wantUDP, tcpOK(wantTCP) && udpOK(wantUDP)
	if !ok && o.tcpPort == 0 && o.udpPort == 0 {
		tcp, udp, ok = pickPorts(wantTCP, wantUDP, tcpOK, udpOK)
	}
	if !ok {
		st.fail("端口 TCP %d 或 UDP %d 被占了：%s", wantTCP, wantUDP, h.portUsers(wantTCP, wantUDP))
		st.note("换一组试试：deneb-relay install --tcp-port <端口> --udp-port <端口>")
		return abort()
	}
	lowPort := tcp < 1024 || udp < 1024
	if lowPort && (l.scope != scopeSystem || kind != initSystemd) {
		st.fail("1024 以下的端口只有 systemd 的系统服务能用")
		return abort()
	}
	if tcp != wantTCP || udp != wantUDP {
		st.ok("端口：TCP %d、UDP %d（%d、%d 被占了：%s）", tcp, udp, wantTCP, wantUDP, h.portUsers(wantTCP, wantUDP))
	} else {
		st.ok("端口：TCP %d、UDP %d", tcp, udp)
	}
	s.Listen, s.STUN = ":"+strconv.Itoa(tcp), ":"+strconv.Itoa(udp)

	// Addresses.
	if o.ipv4 != "" {
		s.IPv4 = o.ipv4
	}
	if o.ipv6 != "" {
		s.IPv6 = o.ipv6
	}
	if o.region != "" {
		s.Region = o.region
	}
	v4, v6, local := h.addrs()
	if s.IPv4 != "" || s.IPv6 != "" {
		st.ok("地址：%s（手动设的）", strings.Trim(s.IPv4+" "+s.IPv6, " "))
	} else if v4.IsValid() || v6.IsValid() {
		st.ok("公网地址：%s", addrsLine(v4, v6))
		if !v4.IsValid() {
			st.warn("网卡上没有公网 IPv4：只有支持 IPv6 的设备能直接用它，除非它在云服务器的 NAT 后面（Deneb 会从外网检查）")
		}
	} else {
		st.warn("网卡上没有公网地址：云服务器的话，Deneb 会用它看到的地址，再从外网检查；在家里路由器后面的话，要把端口转发到这台")
	}
	s.NoUpdate = s.NoUpdate || o.noUpdate
	s.NoPortMap = s.NoPortMap || o.noPortMap

	// The program.
	if !h.exists(l.binDir) {
		made = append(made, func() { h.remove(l.binDir) })
	}
	instVer := ""
	if h.exists(l.bin) {
		instVer = h.progVersion(h.path(l.bin))
	}
	mine := shortVersion()
	nowVer := mine // the release installed once this is done
	switch {
	case instVer != "" && instVer != "dev" && mine != "dev" && compareVersions(instVer, mine) > 0:
		st.ok("已装的 %s 比这个（%s）新，留着它", instVer, mine)
		nowVer = instVer
	case filepath.Clean(self) == filepath.Clean(h.path(l.bin)):
		// Run from where it is installed: nothing to copy.
	default:
		b, err := os.ReadFile(self)
		if err == nil {
			err = h.write(l.bin, b, 0o755)
		}
		if err != nil {
			st.fail("装程序到 %s：%v", l.binDir, err)
			return abort()
		}
		if v := h.progVersion(h.path(l.bin)); v == "" {
			st.fail("装好的程序在 %s 运行不了（这个目录不让运行程序？）", l.binDir)
			return abort()
		}
	}
	hadLink := h.exists(l.link)
	if err := h.link(l.bin, l.link); err != nil {
		st.warn("没能放链接 %s：%v；用 %s 运行它", l.link, err, l.bin)
	} else {
		if !hadLink {
			made = append(made, func() { os.Remove(h.path(l.link)) })
		}
		if l.scope == scopeUser && !strings.Contains(":"+h.getenv("PATH")+":", ":"+filepath.Dir(l.link)+":") {
			st.note("以后查看、卸载用 %s status、%s uninstall", l.link, l.link)
		}
	}
	h.labelForSELinux(l.binDir)

	// Its identity: the certificate devices trust, its keys.
	if err := os.MkdirAll(h.path(l.data), 0o700); err != nil {
		st.fail("%v", err)
		return abort()
	}
	_, pin, err := loadCert(h.path(l.data), "deneb-relay")
	if err == nil {
		_, err = loadDERPKey(h.path(l.data))
	}
	if err != nil {
		st.fail("证书：%v", err)
		return abort()
	}

	// Registered already, or now with the join code.
	inKind := (&installed{Init: kind, Scope: l.scope}).installKind()
	info := proto.RelayInfo{HostName: s.HostName, DERPPort: uint16(tcp), STUNPort: uint16(udp), Pin: pin, Version: version(),
		Install: inKind, Local: local}
	if s.DERPPort != 0 {
		info.DERPPort = s.DERPPort
	}
	if s.STUNPort != 0 {
		info.STUNPort = s.STUNPort
	}
	info.IPv4, info.IPv6 = v4, v6
	if a, err := parseAddr(s.IPv4); err == nil {
		info.IPv4 = a
	}
	if a, err := parseAddr(s.IPv6); err == nil {
		info.IPv6 = a
	}
	info.Region = detectRegion(ctx, s.Region)
	rs, err := h.ensureRegistered(ctx, ctl, l.data, token, info)
	if err != nil {
		st.fail("%v", err)
		return abort()
	}
	if rs.fresh && rs.state.existing {
		st.ok("这台已经是中转站“%s”了：重新装好了它，没有另建一个", rs.state.Name)
	} else if rs.fresh {
		st.ok("已向 Deneb 报到：中转站“%s”", rs.state.Name)
	} else if token != "" {
		st.ok("这台已经是中转站“%s”了，加入码没有用上", rs.state.Name)
	} else {
		st.ok("中转站“%s”", rs.state.Name)
	}

	// Registered: what is made from here on stays, and config.json says so
	// as it comes, for a run again to finish the install, or uninstall to
	// take it all away.
	made = nil
	fromDocker := ""
	if docker != nil {
		fromDocker = docker.name
	} else if prev != nil {
		fromDocker = prev.FromDocker
	}
	in := &installed{Version: nowVer, At: time.Now().UTC().Truncate(time.Second), Scope: l.scope, Init: kind,
		Bin: l.bin, Link: l.link, FromDocker: fromDocker}
	if prev != nil {
		in.User, in.MadeUser, in.Service = prev.User, prev.MadeUser, prev.Service
		in.Firewall, in.FirewallKind, in.FirewallOpen = prev.Firewall, prev.FirewallKind, prev.FirewallOpen
	}
	s.Install = in
	record := func() error { return saveSettings(h.path(l.data), s) }
	unfinished := func() int {
		prog := l.link
		if !h.exists(l.link) {
			prog = l.bin
		}
		st.note("没装完：解决上面的问题后，再运行一次安装命令就行，加入码不用重新生成；不装了请运行 %s uninstall", prog)
		return exitFailed
	}
	if err := record(); err != nil {
		st.fail("%v", err)
		return unfinished()
	}

	// Its own user.
	sp := spec{bin: l.bin, data: l.data, lowPort: lowPort}
	var acct account
	if l.scope == scopeSystem {
		a, newUser, err := h.ensureUser(relayUser, l.data)
		if err != nil {
			st.fail("%v", err)
			return unfinished()
		}
		acct, sp.user = a, a.name
		in.User, in.MadeUser = a.name, in.MadeUser || newUser
		record()
	}
	if kind == initOpenRC || kind == initCron || kind == initNone {
		sp.log = filepath.Join(l.data, "relay.log")
	}
	in.Log = sp.log

	// The firewall, before the service starts listening. Opened before for
	// the same ports, it is left as it is (iptables would take the same
	// rule twice); for other ports, the old rules go first. Only a port it
	// does not let in yet is opened, and so taken back later: a rule the
	// person made stays theirs.
	opened := in.FirewallOpen && len(in.Firewall) > 0
	if opened && tcp == oldTCP && udp == oldUDP {
		st.ok("系统防火墙 %s：TCP %d、UDP %d 已经放行", in.FirewallKind, tcp, udp)
	} else if opened {
		if err := h.apply(in.Firewall); err != nil {
			st.warn("收回系统防火墙里以前放行的端口：%v", err)
		}
		in.Firewall, in.FirewallOpen, opened = nil, false, false
	}
	if l.scope == scopeSystem && !opened {
		if fw := h.detectFirewall(tcp, udp); fw != nil {
			in.FirewallKind = fw.kind
			what, had := strings.Join(fw.opening, "、"), ""
			if len(fw.had) > 0 {
				had = "（" + strings.Join(fw.had, "、") + " 原来就放行了）"
			}
			switch {
			case len(fw.open) == 0:
				st.ok("系统防火墙 %s：%s 原来就放行了", fw.kind, strings.Join(fw.had, "、"))
				in.FirewallOpen = true
			case o.firewall == "open" || o.firewall == "ask" && o.ask(h, fmt.Sprintf("系统防火墙 %s 在用。要放行 %s 吗？%s", fw.kind, what, had)):
				if err := h.apply(fw.open); err != nil {
					h.apply(fw.undo) // what went in before the one that failed
					st.fail("系统防火墙 %s：%v", fw.kind, err)
				} else {
					st.ok("系统防火墙 %s：已放行 %s%s", fw.kind, what, had)
					in.Firewall, in.FirewallOpen = fw.undo, true
					if fw.keep != "" {
						st.note("%s", fw.keep)
					}
				}
			default:
				st.warn("系统防火墙 %s 在用，没有放行 %s。要放行请运行：", fw.kind, what)
				st.notes(commandLines(fw.open, true))
			}
		}
	}
	record()

	// The data is the relay's alone.
	if l.scope == scopeSystem {
		for _, p := range []string{l.data, l.binDir} {
			if err := h.chownTree(p, acct.uid, acct.gid); err != nil {
				st.fail("%v", err)
				return unfinished()
			}
		}
	}
	os.Remove(h.path(filepath.Join(l.data, portMapFile))) // the restarted relay writes its own
	service, notes, err := h.installService(kind, l.scope, sp)
	in.Service = service
	if serr := record(); serr != nil && err == nil {
		err = serr
	}
	if l.scope == scopeSystem {
		h.chown(h.path(filepath.Join(l.data, settingsFile)), acct.uid, acct.gid)
	}
	if err != nil {
		st.fail("装成服务：%v", err)
		st.notes(notes)
		return unfinished()
	}
	switch {
	case kind == initNone:
		st.ok("已启动")
	case prev != nil && instVer != "" && instVer != nowVer:
		st.ok("已升级：%s → %s，%s，开机自启", instVer, nowVer, how)
	case prev != nil:
		st.ok("已重新装好：%s，开机自启", how)
	default:
		st.ok("已安装为%s，开机自启", how)
	}
	st.notes(notes)

	// It answers here, before Deneb checks from outside.
	if !h.waitListening(tcp, h.waitFor) {
		st.fail("服务没有起来")
		if log := h.recentLog(in, 15); log != "" {
			st.notes(strings.Split(log, "\n"))
		}
		if strings.TrimSpace(h.read("/sys/fs/selinux/enforce")) == "1" {
			st.note("这台开着 SELinux：用 ausearch -m avc -ts recent 看看是不是它拦的")
		}
		return unfinished()
	}
	up := &upSince{at: time.Now(), skew: skew}

	// Behind a router: what came of asking it to forward the ports. A
	// mapping made is said now; one that failed only if devices cannot
	// reach it from outside after all (a cloud's 1:1 NAT has no router to
	// ask, and needs none).
	var unreachable func()
	if !v4.IsValid() && s.IPv4 == "" && !s.NoPortMap && s.DERPPort == 0 {
		unreachable = h.reportPortMap(st, l.data, tcp, udp)
	}

	// And from outside.
	return h.reportCheck(ctx, st, ctl, tcp, udp, l.scope, unreachable, up)
}

var mapMethods = map[string]string{"pcp": "PCP", "natpmp": "NAT-PMP", "upnp": "UPnP"}

// reportPortMap waits for the relay to say what came of asking the router
// to forward its ports. A mapping is said at once; for a failure, it
// returns what to say should the machine prove unreachable.
func (h *host) reportPortMap(st *steps, data string, tcp, udp int) func() {
	var rep *proto.RelayPortMap
	for deadline := time.Now().Add(h.waitFor); ; time.Sleep(500 * time.Millisecond) {
		if rep = readPortMap(h.path(data)); rep != nil || time.Now().After(deadline) {
			break
		}
	}
	switch {
	case rep == nil:
		return nil
	case rep.Method == "":
		return func() {
			lan := ""
			if self, _, ok := lanAddr(); ok {
				lan = "（" + self.String() + "）"
			}
			st.warn("没能让路由器自动转发端口：%s", rep.Error)
			st.note("如果这台在家里的路由器后面，请在路由器上把 TCP %d、UDP %d 转发到这台%s", tcp, udp, lan)
		}
	case !publicAddr(rep.External):
		st.warn("路由器转发了端口（%s），可它自己的外网地址 %s 也不是公网地址：运营商给的是大内网，这台当不了中转站", mapMethods[rep.Method], rep.External)
	default:
		st.ok("路由器已转发端口（%s）：外网 %s，TCP %d、UDP %d", mapMethods[rep.Method], rep.External, rep.DERPPort, rep.STUNPort)
	}
	return nil
}

// parseAddr parses an address given by hand; "" is none.
func parseAddr(s string) (netip.Addr, error) {
	if s == "" {
		return netip.Addr{}, errors.New("none")
	}
	return netip.ParseAddr(s)
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// portUsers says who holds the ports: "nginx 占着 TCP 47445".
func (h *host) portUsers(tcp, udp int) string {
	var p []string
	if !h.tcpFree(tcp) {
		w := "别的程序"
		if o := portOwner("tcp", tcp); o != "" {
			w = o
		}
		p = append(p, fmt.Sprintf("%s 占着 TCP %d", w, tcp))
	}
	if !h.udpFree(udp) {
		w := "别的程序"
		if o := portOwner("udp", udp); o != "" {
			w = o
		}
		p = append(p, fmt.Sprintf("%s 占着 UDP %d", w, udp))
	}
	return strings.Join(p, "，")
}

// ask asks q on the terminal; Enter means yes. --yes and --no answer for
// it; with no terminal (cloud-init, a pipe) the answer is no, and install
// says what it would have done.
func (o *installOpts) ask(h *host, q string) bool {
	switch {
	case o.yes:
		fmt.Fprintf(h.out, "? %s 是（--yes）\n", q)
		return true
	case o.no:
		return false
	}
	t, err := h.tty()
	if err != nil {
		fmt.Fprintf(h.out, "? %s 没有终端可以问：先不做\n", q)
		return false
	}
	defer t.Close()
	fmt.Fprintf(t, "? %s [Y/n] ", q)
	line, _ := bufio.NewReader(t).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes", "是", "好", "要":
		return true
	}
	return false
}

// ensureUser makes the user the relay runs as, unless it is there.
func (h *host) ensureUser(name, home string) (a account, made bool, err error) {
	if a, ok := h.lookupUser(name); ok {
		// One whose home is the relay's data is install's own, from a run
		// that did not finish (before 0.1.9 a failed install left it).
		return a, a.home == home, nil
	}
	nologin := "/bin/false"
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if h.exists(p) {
			nologin = p
			break
		}
	}
	var out string
	switch {
	case h.has("useradd"):
		out, err = h.run("useradd", "-r", "-U", "-M", "-d", home, "-s", nologin, name)
	case h.has("adduser"): // BusyBox
		if h.has("addgroup") {
			h.run("addgroup", "-S", name)
		}
		out, err = h.run("adduser", "-S", "-D", "-H", "-h", home, "-s", nologin, "-G", name, name)
	default:
		// Nothing to make one with (OpenWrt): nobody, which every system has.
		if a, ok := h.lookupUser("nobody"); ok {
			return a, false, nil
		}
		return account{}, false, errors.New("这台机器上没法新建运行中转站的用户")
	}
	if err != nil {
		return account{}, false, fmt.Errorf("新建用户 %s：%v%s", name, err, outTail(out))
	}
	a, ok := h.lookupUser(name)
	if !ok {
		return account{}, false, fmt.Errorf("新建了用户 %s，却找不到它", name)
	}
	return a, true, nil
}

// removeUser takes back the user install made.
func (h *host) removeUser(name string) error {
	var out string
	var err error
	switch {
	case h.has("userdel"):
		out, err = h.run("userdel", name)
	case h.has("deluser"):
		out, err = h.run("deluser", name)
		if h.has("delgroup") {
			h.run("delgroup", name)
		}
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("%v%s", err, outTail(out))
	}
	return nil
}

// link puts a link to bin at link, in place of what is there.
func (h *host) link(bin, link string) error {
	if err := os.MkdirAll(filepath.Dir(h.path(link)), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(h.path(link)); err == nil && fi.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%s 是别的文件", link)
	}
	os.Remove(h.path(link))
	return os.Symlink(bin, h.path(link))
}

// labelForSELinux gives the program's directory the type of programs, on
// a machine where SELinux is enforcing: in /usr/local/lib it would get
// lib_t, which systemd may refuse to run. New files there (updates) take
// the directory's type.
func (h *host) labelForSELinux(dir string) {
	if strings.TrimSpace(h.read("/sys/fs/selinux/enforce")) != "1" || !h.has("chcon") {
		return
	}
	h.run("chcon", "-R", "-t", "bin_t", dir)
}

// registered is the relay's identity after install.
type registered struct {
	state *state
	fresh bool // registered now, with the join code
}

// ensureRegistered checks the relay the data directory names is still
// the control server's, or registers it with token: an install run again
// spends no join code, and one whose relay was deleted in Deneb takes the
// new one.
func (h *host) ensureRegistered(ctx context.Context, ctl *controlClient, data, token string, info proto.RelayInfo) (*registered, error) {
	dir := h.path(data)
	st, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	if st != nil {
		ctl.setSecret(st.Secret)
		var res proto.RelaySyncResult
		err := ctl.call(ctx, proto.PathRelaySync, proto.RelaySyncRequest{Connected: []proto.NodePublic{}, Info: &info}, &res)
		switch {
		case err == nil:
			if res.Name != "" && res.Name != st.Name {
				st.Name = res.Name
				saveState(dir, st)
			}
			return &registered{state: st}, nil
		case !errors.Is(err, errRevoked):
			return nil, fmt.Errorf("连不上 Deneb 服务器：%v", err)
		case token == "":
			return nil, errors.New("Deneb 里已经没有这个中转站了：请在 Deneb 里重新生成安装命令，再运行一次")
		}
		ctl.setSecret("")
	}
	if token == "" {
		return nil, errors.New("要给加入码：在 Deneb 里点“用我自己的服务器当中转站”，复制那一行命令")
	}
	st, err = register(ctx, ctl, config{token: token, data: dir}, info, func(string, ...any) {})
	var ce *callError
	if errors.As(err, &ce) {
		// The server's own words: "加入码已过期，请在 Deneb 里重新生成".
		return nil, errors.New(ce.msg)
	}
	if err != nil {
		return nil, err
	}
	ctl.setSecret(st.Secret)
	return &registered{state: st, fresh: true}, nil
}

// waitListening waits for the relay to answer on its port, here.
func (h *host) waitListening(port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if h.answers(port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// answersLocally asks the relay on this machine for its probe page.
func answersLocally(port int) bool {
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		// Its own certificate, here: the pin is for devices.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           nil,
	}}
	resp, err := c.Get("https://127.0.0.1:" + strconv.Itoa(port) + "/derp/probe")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// findInstall finds the installed relay: this user's own, else the
// system's (which takes root to look at).
func (h *host) findInstall() (layout, *settings, error) {
	l := h.layout()
	s, err := loadSettings(h.path(l.data))
	if err != nil || s != nil && s.Install != nil || h.isRoot() {
		return l, s, err
	}
	sys := layout{scope: scopeSystem, binDir: "/usr/local/lib/deneb-relay", bin: "/usr/local/lib/deneb-relay/deneb-relay",
		link: "/usr/local/bin/deneb-relay", data: "/var/lib/deneb-relay"}
	if h.exists("/etc/openwrt_release") {
		sys.data = "/etc/deneb-relay"
	}
	if h.exists(sys.data) {
		ss, err := loadSettings(h.path(sys.data))
		if errors.Is(err, fs.ErrPermission) {
			return sys, nil, errors.New("这台的中转站是系统服务：请用 sudo 运行")
		}
		return sys, ss, err
	}
	return l, s, nil
}
