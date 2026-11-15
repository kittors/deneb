package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// status, uninstall and update.

// cmdStatus checks the relay here, and says its state in Deneb, as of the
// last check from outside; --recheck has Deneb check again now.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	recheck := fs.Bool("recheck", false, "have Deneb check again from outside, now")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Println("中转站只能装在 Linux 上。")
		return 1
	}
	return realHost().status(context.Background(), *recheck)
}

func (h *host) status(ctx context.Context, recheck bool) int {
	st := &steps{w: h.out}
	l, s, err := h.findInstall()
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	if s == nil || s.Install == nil {
		st.fail("这台没有装中转站")
		st.note("要装：在 Deneb 里点“用我自己的服务器当中转站”，复制那一行命令到这里运行")
		return exitFailed
	}
	in := s.Install
	ver := h.progVersion(h.path(in.Bin))
	running, words := h.serviceState(in, l.data)
	where := map[string]string{initSystemd: "systemd 服务", initOpenRC: "OpenRC 服务", initProcd: "procd 服务", initCron: "cron", initNone: "没有服务管理"}[in.Init]
	if running {
		st.ok("deneb-relay %s：在运行（%s）", ver, where)
	} else {
		st.fail("deneb-relay %s：没在运行（%s：%s）", ver, where, words)
		if log := h.recentLog(in, 15); log != "" {
			st.notes(strings.Split(log, "\n"))
		}
	}
	rs, err := loadState(h.path(l.data))
	if err != nil || rs == nil {
		st.fail("还没向 Deneb 报到")
		return exitFailed
	}
	st.ok("中转站“%s”", rs.Name)
	if running && h.answers(s.tcpPort()) {
		st.ok("端口：TCP %d、UDP %d", s.tcpPort(), s.udpPort())
	} else {
		// Stopped, or serving no more (deleted in Deneb): it holds no port.
		st.warn("端口：TCP %d、UDP %d，现在没在服务", s.tcpPort(), s.udpPort())
	}
	if v4, v6, _ := h.addrs(); v4.IsValid() || v6.IsValid() {
		st.ok("公网地址：%s", addrsLine(v4, v6))
	} else {
		st.warn("网卡上没有公网地址（Deneb 用它看到的地址）")
	}
	ctl, err := newControlClient(s.Control, s.ControlPin)
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	ctl.setSecret(rs.Secret)
	if skew, err := clockSkew(ctx, ctl); err != nil {
		st.fail("连不上 Deneb 服务器：%v", err)
		return exitFailed
	} else if abs(skew) > skewWarn {
		st.warn("系统时间%s，请校准（timedatectl set-ntp true）", skewWords(skew))
	}
	if free, ok := h.freeSpace(existingDir(h.path(l.data))); ok && free < 64<<20 {
		st.warn("磁盘只剩 %s", mb(free))
	}
	if recheck && running {
		return h.reportCheck(ctx, st, ctl, s.tcpPort(), s.udpPort(), in.Scope, nil, nil)
	}
	// The state its card shows, from the last check: no new one.
	cur, err := h.current(ctx, ctl)
	switch {
	case errors.Is(err, errNoCheck):
		// A control server from before M7 knows nothing of it.
	case err != nil:
		return h.reportCallError(st, err)
	default:
		when := ""
		if !cur.CheckedAt.IsZero() {
			when = "（" + agoWords(time.Since(cur.CheckedAt)) + "检查的）"
		}
		switch cur.State {
		case proto.RelayOK:
			st.ok("Deneb 里的状态：%s%s", stateWords[cur.State], when)
		case proto.RelayPartial, proto.RelayChecking, proto.RelayWaiting:
			st.warn("Deneb 里的状态：%s%s", stateWords[cur.State], when)
		default:
			st.fail("Deneb 里的状态：%s%s", stateWords[cur.State], when)
		}
		h.printProblems(st, cur.Problems)
		if running && cur.State != proto.RelayOK {
			st.note("马上重新检查：deneb-relay status --recheck")
		}
	}
	if !running {
		return exitFailed
	}
	if cur != nil && cur.State == proto.RelayUnreachable {
		return exitNotReachable
	}
	return exitOK
}

// current is the relay's state as its card shows it: the last check's,
// with none made now.
func currentStatus(ctx context.Context, ctl *controlClient) (*proto.RelayStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res proto.RelayStatus
	err := ctl.get(ctx, proto.PathRelayCheck, &res)
	if notThere(err) {
		return nil, errNoCheck
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// reportCallError says what a refused call means: the relay deleted in
// Deneb, or set up again elsewhere.
func (h *host) reportCallError(st *steps, err error) int {
	switch {
	case errors.Is(err, errRemoved):
		st.fail("Deneb 里已经删掉了这个中转站，它停止了服务")
		st.note("还要用的话，在 Deneb 里重新生成安装命令，到这里运行；不要了：deneb-relay uninstall")
	case errors.Is(err, errRevoked):
		st.fail("Deneb 不认这个中转站了：它在别处重新装过")
		st.note("还要在这台上用的话，在 Deneb 里重新生成安装命令，到这里运行；不要了：deneb-relay uninstall")
	default:
		st.warn("没能从 Deneb 取到状态：%v", err)
		return exitOK
	}
	return exitFailed
}

// agoWords says how long ago: "3 分钟前".
func agoWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	}
	return fmt.Sprintf("%d 天前", int(d/(24*time.Hour)))
}

func cmdUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	keep := fs.Bool("keep-data", false, "keep the data directory (the relay's identity)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Println("中转站只能装在 Linux 上。")
		return 1
	}
	return realHost().uninstall(context.Background(), *keep)
}

func (h *host) uninstall(ctx context.Context, keepData bool) int {
	st := &steps{w: h.out}
	l, s, err := h.findInstall()
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	if s == nil || s.Install == nil {
		in := h.leftovers(l)
		if in == nil {
			st.fail("这台没有用 deneb-relay install 装过中转站")
			return exitFailed
		}
		st.warn("上次安装没有装完，清掉它留下的")
		if s == nil {
			s = &settings{}
		}
		s.Install = in
	}
	in := s.Install
	if in.Scope == scopeSystem && !h.isRoot() {
		st.fail("这台的中转站是系统服务：请用 sudo 运行")
		return exitFailed
	}
	// The control server first, while the secret is here.
	if rs, _ := loadState(h.path(l.data)); rs != nil && !keepData {
		if ctl, err := newControlClient(cmp.Or(s.Control, rs.Control), s.ControlPin); err == nil {
			ctl.setSecret(rs.Secret)
			switch err := h.leave(ctx, ctl); {
			case err == nil:
				st.ok("已告诉 Deneb：不再用中转站“%s”", rs.Name)
			case errors.Is(err, errNoLeave):
				st.warn("Deneb 服务器还不支持卸载通知：请在 Deneb 里删掉中转站“%s”", rs.Name)
			default:
				var ce *callError
				if errors.As(err, &ce) {
					st.warn("Deneb 服务器没收下卸载的通知：请在 Deneb 里删掉中转站“%s”", rs.Name)
				} else {
					st.warn("连不上 Deneb 服务器：请在 Deneb 里删掉中转站“%s”", rs.Name)
				}
			}
		}
	}
	if err := h.removeService(in, l.data); err != nil {
		st.warn("停服务时：%v", err)
	} else if in.Service != "" || in.Init == initCron || in.Init == initNone {
		st.ok("服务已停止并删除")
	}
	if len(in.Firewall) > 0 {
		if err := h.apply(in.Firewall); err != nil {
			st.warn("系统防火墙：%v", err)
		} else {
			st.ok("系统防火墙里放行的端口已收回")
		}
	}
	if in.Link != "" {
		if fi, err := os.Lstat(h.path(in.Link)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			os.Remove(h.path(in.Link))
		}
	}
	if err := h.remove(filepath.Dir(in.Bin)); err != nil {
		st.warn("删程序：%v", err)
	}
	if keepData {
		st.ok("程序已删除；数据留在 %s", l.data)
	} else if err := h.remove(l.data); err != nil {
		st.warn("删数据：%v", err)
	} else {
		st.ok("程序和数据已删除")
	}
	if in.MadeUser {
		if err := h.removeUser(in.User); err != nil {
			st.warn("删用户 %s：%v", in.User, err)
		}
	}
	if in.FromDocker != "" {
		st.note("以前的 Docker 容器 %s 还在（停着）：不要了可以 docker rm %s", in.FromDocker, in.FromDocker)
	}
	return exitOK
}

// leftovers is what an install that did not finish left here with no
// config.json to say so, as uninstall is to take it away; nil when there
// is nothing. Before 0.1.9 one that failed (a join code used already, say)
// left its user, program, link and data.
func (h *host) leftovers(l layout) *installed {
	in := &installed{Scope: l.scope, Bin: l.bin}
	found := h.exists(l.binDir) || h.exists(l.data)
	if t, err := os.Readlink(h.path(l.link)); err == nil && t == l.bin {
		in.Link, found = l.link, true
	}
	if l.scope == scopeSystem {
		if a, ok := h.lookupUser(relayUser); ok && a.home == l.data {
			in.User, in.MadeUser, found = relayUser, true, true
		}
	}
	kind, _ := h.detectInit(l.scope)
	if f := h.serviceFile(kind, l.scope); f != "" && h.exists(f) {
		in.Init, in.Service, found = kind, f, true
	} else if kind == initCron && (l.scope == scopeUser || in.User != "") {
		if cur, err := h.crontab(in.User, "", "-l"); err == nil && withCronLines(cur, nil) != cur {
			in.Init, found = initCron, true
		}
	}
	if !found {
		return nil
	}
	return in
}

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "only say whether there is a newer release")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Println("中转站只能装在 Linux 上。")
		return 1
	}
	return realHost().update(context.Background(), *check)
}

func (h *host) update(ctx context.Context, check bool) int {
	st := &steps{w: h.out}
	l, s, err := h.findInstall()
	if err != nil {
		st.fail("%v", err)
		return exitFailed
	}
	if s == nil || s.Install == nil {
		st.fail("这台没有装中转站")
		return exitFailed
	}
	in := s.Install
	if in.Scope == scopeSystem && !h.isRoot() {
		st.fail("这台的中转站是系统服务：请用 sudo 运行")
		return exitFailed
	}
	cur := h.progVersion(h.path(in.Bin))
	m, base, err := latest(ctx)
	if err != nil {
		st.fail("取不到最新版本：%v", err)
		return exitFailed
	}
	if cur != "" && cur != "dev" && compareVersions(m.Version, cur) <= 0 {
		st.ok("已经是最新版：%s", cur)
		return exitOK
	}
	if check {
		st.ok("有新版本：%s（现在是 %s）", m.Version, cur)
		return exitOK
	}
	dir := h.path(filepath.Dir(in.Bin))
	next, err := fetchProgram(ctx, m, base, dir)
	if err == nil {
		if err = checkRuns(ctx, next, m.Version); err == nil {
			err = replaceProgram(h.path(in.Bin), next)
		}
	}
	if err != nil {
		os.Remove(filepath.Join(dir, ".deneb-relay.new"))
		st.fail("更新：%v", err)
		return exitFailed
	}
	st.ok("已下载 deneb-relay %s（签名核对过）", m.Version)
	if in.Scope == scopeSystem {
		if a, ok := h.lookupUser(in.User); ok {
			h.chownTree(filepath.Dir(in.Bin), a.uid, a.gid)
		}
	}
	s.Install.Version = m.Version
	saveSettings(h.path(l.data), s)
	if err := h.restartService(in, l.data); err != nil {
		st.fail("重启服务：%v", err)
		return exitFailed
	}
	st.ok("已更新：%s → %s，服务已重启", cur, m.Version)
	return exitOK
}

// The control server's check from outside, and the relay's leaving: its
// side is the control line's (proto.PathRelayCheck, proto.PathRelayLeave).

// errNoCheck, errNoLeave: the control server is from before M7.
var (
	errNoCheck = errors.New("no check")
	errNoLeave = errors.New("no leave")
)

// notThere: the control server has no such call.
func notThere(err error) bool {
	var ce *callError
	return errors.As(err, &ce) && (ce.status == http.StatusNotFound || ce.status == http.StatusMethodNotAllowed)
}

// checkFromOutside has the control server check, now, whether the relay
// can be reached from outside: it answers when it knows, within 20 s.
func checkFromOutside(ctx context.Context, ctl *controlClient) (*proto.RelayStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var res proto.RelayStatus
	err := ctl.call(ctx, proto.PathRelayCheck, struct{}{}, &res)
	if notThere(err) {
		return nil, errNoCheck
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// leave tells the control server the relay is being uninstalled.
func leave(ctx context.Context, ctl *controlClient) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res struct{}
	err := ctl.call(ctx, proto.PathRelayLeave, proto.RelayLeaveRequest{Reason: "uninstall"}, &res)
	switch {
	case notThere(err):
		return errNoLeave
	case errors.Is(err, errRevoked):
		return nil // gone already
	}
	return err
}

// stateWords are the card's words for a relay's state.
var stateWords = map[proto.RelayState]string{
	proto.RelayWaiting:     "等待安装",
	proto.RelayChecking:    "检查中",
	proto.RelayOK:          "可以用",
	proto.RelayPartial:     "部分可用",
	proto.RelayUnreachable: "连不上",
	proto.RelayOffline:     "不在线",
}

// upSince is when the relay began to answer here, for reportCheck.
type upSince struct {
	at   time.Time     // in this machine's clock
	skew time.Duration // how far this machine's clock is ahead of the control server's (clockSkew)
}

// before: res comes from a check that finished before the relay answered
// (in the control server's clock, give or take the second clockSkew
// rounds to) and found it wanting: it says nothing of the relay now.
func (u *upSince) before(res *proto.RelayStatus) bool {
	if u == nil || res.CheckedAt.IsZero() || res.State == proto.RelayOK || res.State == proto.RelayChecking {
		return false
	}
	return res.CheckedAt.Before(u.at.Add(-u.skew).Add(time.Second))
}

// againIn is how long until the control server checks anew rather than
// answer with that check: 10 seconds after it, and a second's leeway.
func (u *upSince) againIn(res *proto.RelayStatus) time.Duration {
	return min(time.Until(res.CheckedAt.Add(u.skew).Add(11*time.Second)), 15*time.Second)
}

// probeWords say what a probe found.
var probeWords = map[string]string{
	proto.RelayProbeRefused:     "没有程序在听",
	proto.RelayProbeTimeout:     "没有回应，路上被挡了",
	proto.RelayProbeUnreachable: "网络不通",
	proto.RelayProbeTLS:         "回应的不是中转站",
	proto.RelayProbePin:         "回应的是别的程序",
	proto.RelayProbeNone:        "没开 STUN",
	proto.RelayProbeError:       "出错",
}

// reportCheck prints the check from outside, and install's conclusion:
// what each place saw, then the control server's reasons and advice, as
// it words them (the card says the same). unreachable, if not nil, says
// more should the relay prove unreachable (the router's mapping failed),
// unless the control server said it already.
func (h *host) reportCheck(ctx context.Context, st *steps, ctl *controlClient, tcp, udp int, scope string, unreachable func(), up *upSince) int {
	st.doing("从外网检查能不能连上")
	res, err := h.check(ctx, ctl)
	if err == nil && up.before(res) {
		// A check made while it was starting, which a control server from
		// before 11-15 answers with for a few seconds: ask once that is
		// over, for one of the relay as it is now.
		if wait := up.againIn(res); wait > 0 {
			time.Sleep(wait)
		}
		res, err = h.check(ctx, ctl)
	}
	if errors.Is(err, errNoCheck) {
		st.ok("服务在运行，端口 TCP %d、UDP %d", tcp, udp)
		st.note("从外网能不能连上，以 Deneb 里的状态为准；连不上的话，请在云服务商的安全组里放行 TCP %d、UDP %d", tcp, udp)
		if unreachable != nil {
			unreachable()
		}
		return exitOK
	}
	if errors.Is(err, errRevoked) {
		return h.reportCallError(st, err)
	}
	if err != nil {
		st.warn("没能从外网检查：%v", err)
		st.note("过一会儿运行 deneb-relay status 再看")
		return exitOK
	}
	for _, p := range res.Probes {
		place, where := placeWords(p.From), p.Addr.String()
		switch p.TCP {
		case proto.RelayProbeNoRoute:
			continue // the place has no route to that family: says nothing
		case proto.RelayProbeSkipped:
			// A place checks only addresses on the Internet (a development
			// control server hands out this machine's): says nothing either,
			// and the control server does not count it.
			st.note("从外网（%s）没查 %s：不是公网地址", place, where)
			continue
		}
		tcpOK := p.TCP == proto.RelayProbeOK
		// UDP not checked there, or no STUN to check, is no trouble either,
		// as the control server counts it; the line just does not claim it.
		udpOK := p.UDP == "" || p.UDP == proto.RelayProbeOK || p.UDP == proto.RelayProbeNone || p.UDP == proto.RelayProbeSkipped
		switch {
		case tcpOK && p.UDP == proto.RelayProbeOK:
			st.ok("从外网（%s）连得上 %s：TCP %d、UDP %d", place, where, tcp, udp)
		case tcpOK && udpOK:
			st.ok("从外网（%s）连得上 %s：TCP %d", place, where, tcp)
		case tcpOK:
			st.warn("从外网（%s）连得上 %s 的 TCP %d，UDP %d %s", place, where, tcp, udp, probeWords[p.UDP])
		default:
			st.fail("从外网（%s）连不上 %s 的 TCP %d：%s", place, where, tcp, probeWords[p.TCP])
		}
	}
	h.printProblems(st, res.Problems)
	switch res.State {
	case proto.RelayOK:
		fmt.Fprintln(h.out, "可以用了：在 Deneb 里把它拖进局域网就行。")
		return exitOK
	case proto.RelayPartial:
		fmt.Fprintln(h.out, "可以用，但有上面说的问题。")
		return exitOK
	case proto.RelayChecking:
		st.note("Deneb 还在检查：过一会儿运行 deneb-relay status 再看")
		return exitOK
	}
	if unreachable != nil && !slices.ContainsFunc(res.Problems, func(p proto.RelayProblem) bool {
		return p.Code == proto.RelayProblemBehindNAT || p.Code == proto.RelayProblemNoPublicIP
	}) {
		unreachable()
	}
	if scope == scopeUser {
		st.note("没有 root 装的：系统防火墙要请管理员放行 TCP %d、UDP %d", tcp, udp)
	}
	fmt.Fprintf(h.out, "还不能用：%s。放行以后不用重装，Deneb 每 10 分钟会重新检查（马上看：deneb-relay status）。\n", stateWords[res.State])
	return exitNotReachable
}

// printProblems prints the control server's reasons, a mark by how bad,
// and what to do below each: in its words, else in these.
func (h *host) printProblems(st *steps, problems []proto.RelayProblem) {
	for _, pr := range problems {
		msg, advice := pr.Message, pr.Advice
		if msg == "" {
			msg, advice = problemWords(pr)
		}
		switch pr.Level {
		case proto.RelayLevelError:
			st.fail("%s", msg)
		case proto.RelayLevelWarn:
			st.warn("%s", msg)
		default:
			st.note("%s", msg)
		}
		if advice != "" {
			st.note("%s", advice)
		}
	}
}

// problemWords says a reason, should the control server send no words
// for it.
func problemWords(p proto.RelayProblem) (msg, advice string) {
	switch p.Code {
	case proto.RelayProblemNoPublicIP:
		return "这台机器没有公网地址，当不了中转站", "有公网 IPv6 的话，只有支持 IPv6 的设备能用它"
	case proto.RelayProblemBehindNAT:
		return "这台在路由器后面，外面连不进来", fmt.Sprintf("请在路由器上把 TCP %d、UDP %d 转发到 %s", p.DERPPort, p.STUNPort, p.Addr)
	case proto.RelayProblemTCPBlocked:
		return fmt.Sprintf("从外网连不上 TCP %d", p.Port), "请在云服务商的安全组（入方向）里放行它，系统防火墙也要放行"
	case proto.RelayProblemPinMismatch:
		return fmt.Sprintf("TCP %d 连上的不是这个中转站", p.Port), "这个端口被转到了别的机器或程序上"
	case proto.RelayProblemUDPBlocked:
		return fmt.Sprintf("UDP %d 不通：能转发，但帮不上设备直连", p.Port), "请放行 UDP 端口"
	case proto.RelayProblemIPv6Only:
		return "只有 IPv6 地址连得上：只有支持 IPv6 的设备能用它", ""
	case proto.RelayProblemIPv6Blocked:
		return "IPv6 地址连不上，设备会用 IPv4", ""
	case proto.RelayProblemUntested:
		return "没有地方能从外网检查它，以设备实际连的结果为准", ""
	case proto.RelayProblemDevicesFailing:
		return fmt.Sprintf("你在线的 %d 台设备里，有 %d 台连不上它", p.Of, p.Count), "多半是跨境线路，这些设备会绕开它"
	case proto.RelayProblemCrashing:
		return fmt.Sprintf("中转站反复出错：一小时里重启了 %d 次", p.Count), "看看日志：deneb-relay status"
	case proto.RelayProblemPorts:
		return fmt.Sprintf("用的是 TCP %d、UDP %d", p.DERPPort, p.STUNPort), "放行端口时放行这两个"
	case proto.RelayProblemUninstalled:
		return "在服务器上卸载了", ""
	}
	return p.Code, ""
}

// placeWords names where a check came from.
func placeWords(from string) string {
	if from == "control" {
		return "Deneb 服务器"
	}
	return from
}
