package control

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/control/probe"
	"github.com/kittors/deneb/proto"
)

// Whether a relay can be used from outside (M7, design/plan/m7/
// relay-install-api.md): the control server checks it from where it is and
// from a second place (a prober: the watchdog on another machine), after it
// registers, when it asks, when it moves, and every 10 minutes; its owner's
// devices say whether they reach it. Its card's state and reasons come out
// of these (statusOf). What the checks found is kept in memory, like a
// relay being online: after a restart the next check finds it again.

const (
	// checkEvery is how often a relay online is checked unseen.
	checkEvery = 10 * time.Minute
	// checkAgainAfter is how soon a check asked for may run again: one
	// asked for sooner gets the last one's result.
	checkAgainAfter = 10 * time.Second
	// checkTimeout bounds a check, both places together.
	checkTimeout = 15 * time.Second
	// checkWait is how long /relay/v1/check waits for the check it asked.
	checkWait = 20 * time.Second
	// checksAtOnce is how many checks run at once at most.
	checksAtOnce = 8
	// crashingAfter restarts within an hour (RelayInfo.Restarts) make a
	// relay "反复出错".
	crashingAfter = 3
	// The ports a relay listens on unless they were taken.
	defaultRelayDERPPort = 47445
	defaultRelaySTUNPort = 47478
	// fromControl is the control server, as a probe's From says it.
	fromControl = "control"
)

// relayCheck is what the checks of one relay found.
type relayCheck struct {
	at     time.Time          // when the last check finished
	probes []proto.RelayProbe // what it saw, sorted
	done   bool               // a check finished since the server started
	// running is closed when the check under way ends: once its card is
	// published. nil when none runs. probing: it is still looking, so
	// what it will publish is not known yet. shown: someone asked for it,
	// so the card says 正在检查 while it probes.
	running chan struct{}
	probing bool
	shown   bool
	// rerun: a check was asked for anew while one was publishing what it
	// found; it runs once that one ends (rerunShown: and it is shown).
	rerun, rerunShown bool
}

// checkSnapshot is a relay's checks as a view reads them.
type checkSnapshot struct {
	at     time.Time
	probes []proto.RelayProbe
	done   bool
	shown  bool
}

// relayChecks are every relay's checks.
type relayChecks struct {
	mu      sync.Mutex
	byRelay map[proto.RelayID]*relayCheck
	slots   chan struct{} // checksAtOnce tokens
}

func newRelayChecks() *relayChecks {
	return &relayChecks{byRelay: map[proto.RelayID]*relayCheck{}, slots: make(chan struct{}, checksAtOnce)}
}

// snapshot is relay id's checks, for the views.
func (c *relayChecks) snapshot(id proto.RelayID) checkSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	rc := c.byRelay[id]
	if rc == nil {
		return checkSnapshot{}
	}
	return checkSnapshot{at: rc.at, probes: rc.probes, done: rc.done, shown: rc.shown && rc.probing}
}

// forget drops what the checks of a relay found: it was set up again,
// uninstalled or deleted.
func (c *relayChecks) forget(id proto.RelayID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc := c.byRelay[id]; rc != nil && rc.running == nil {
		delete(c.byRelay, id)
	} else if rc != nil {
		rc.done, rc.probes = false, nil
	}
}

// checkRelay starts a check of relay id, unless one runs (whose end it
// returns) or one ended less than again ago (a closed channel: its result
// stands). shown makes the card say 正在检查 while it runs. Without a
// prober (ProbeRelay nil, the tests) nothing is checked.
func (s *Server) checkRelay(id proto.RelayID, shown bool, again time.Duration, why string) <-chan struct{} {
	done := make(chan struct{})
	if s.cfg.ProbeRelay == nil {
		close(done)
		return done
	}
	c := s.checks
	c.mu.Lock()
	rc := c.byRelay[id]
	if rc == nil {
		rc = &relayCheck{}
		c.byRelay[id] = rc
	}
	switch {
	case rc.running != nil && rc.probing:
		ch := rc.running
		show := shown && !rc.shown
		rc.shown = rc.shown || shown
		c.mu.Unlock()
		if show {
			s.publishChecks(id, "a check asked for runs")
		}
		return ch
	case rc.running != nil:
		// It found what it found and is publishing it: that is fresh, and
		// heard of once the card says it. One asked for anew runs after it.
		ch := rc.running
		if again == 0 {
			rc.rerun, rc.rerunShown = true, rc.rerunShown || shown
		}
		c.mu.Unlock()
		return ch
	case rc.done && s.now().Sub(rc.at) < again:
		c.mu.Unlock()
		close(done)
		return done
	}
	rc.running, rc.probing, rc.shown = done, true, shown
	c.mu.Unlock()
	if shown {
		s.publishChecks(id, "checking ("+why+")")
	}
	go s.runCheck(id, done, why)
	return done
}

// publishChecks publishes a new version for a relay's card that changed
// with its checks.
func (s *Server) publishChecks(id proto.RelayID, what string) {
	if _, err := s.change("", func(*sql.Tx, *World) error { return nil }); err != nil {
		s.logf("relay %s: %s: %v", id, what, err)
	}
}

// runCheck checks relay id from here and from the prober, if one is
// connected, keeps what they saw, and publishes the card if it changed.
func (s *Server) runCheck(id proto.RelayID, done chan struct{}, why string) {
	c := s.checks
	c.slots <- struct{}{}
	defer func() { <-c.slots }()

	st := s.state.Load()
	rl := st.w.relays[id]
	var targets, where []proto.ProbeTarget
	if ready(rl) {
		where = probe.Targets(rl)
		for _, t := range where {
			// An address not on the Internet is not checked: devices could
			// not reach it anyway, and the server is no way to look into a
			// private network. A development server checks this machine's.
			if s.cfg.Dev || probe.Public(t.Addr) {
				targets = append(targets, t)
			}
		}
	}
	before := s.cardOf(id)

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	results := make(chan proto.RelayProbe, len(targets))
	for _, t := range targets {
		go func() {
			p := s.cfg.ProbeRelay(ctx, t)
			p.From, p.Family, p.Addr = fromControl, t.Family, t.Addr
			results <- p
		}()
	}
	job := s.probers.dispatch(targets, s.now())
	var probes []proto.RelayProbe
	for range targets {
		probes = append(probes, <-results)
	}
	if job != nil {
		select {
		case rs := <-job.done:
			probes = append(probes, rs...)
		case <-ctx.Done():
			s.logf("relay %s: the prober %s did not answer in %s", id, job.from(), checkTimeout)
		}
		s.probers.drop(job)
	}
	slices.SortFunc(probes, func(a, b proto.RelayProbe) int {
		return cmp.Or(cmp.Compare(a.Family, b.Family), cmp.Compare(probeOrder(a.From), probeOrder(b.From)), cmp.Compare(a.From, b.From))
	})

	c.mu.Lock()
	// Not its entry if the relay was deleted (scheduleChecks dropped it).
	rc := c.byRelay[id]
	mine := rc != nil && rc.running == done
	wasShown := false
	if mine {
		rc.at, rc.probes, rc.done, wasShown = s.now(), probes, true, rc.shown
		rc.probing, rc.shown = false, false
	}
	c.mu.Unlock()
	if mine {
		s.publishCheck(id, before, wasShown, why)
	}

	// Only now does it end: one who asked while its card was published
	// waited for it, and hears of it once the card says it.
	rerun, rerunShown := false, wasShown
	c.mu.Lock()
	if mine && rc.running == done {
		rc.running = nil
		rerun, rerunShown = rc.rerun, rerunShown || rc.rerunShown
		rc.rerun, rc.rerunShown = false, false
	}
	c.mu.Unlock()
	close(done)
	// It moved while it was checked, or it was asked for anew: check
	// where it is now.
	if cur := s.state.Load().w.relays[id]; mine && ready(cur) {
		switch {
		case !slices.Equal(probe.Targets(cur), where):
			s.checkRelay(id, rerunShown, 0, "moved while checked")
		case rerun:
			s.checkRelay(id, rerunShown, 0, "asked anew while checked")
		}
	}
}

// publishCheck logs what a check of relay id found, and publishes its
// card if that changed, someone asked for the check, or it became usable
// (which is written down: usable_at).
func (s *Server) publishCheck(id proto.RelayID, before card, wasShown bool, why string) {
	after := s.cardOf(id)
	st := s.state.Load()
	status := after.status
	if status != nil {
		s.logf("relay %s (%s) checked (%s): %s%s", id, nameOf(st.w, id), why, status.State, problemCodes(status))
	}
	m := st.w.relayMeta[id]
	newlyUsable := after.usable() && m != nil && (m.UsableAt.IsZero() || !before.usable())
	if !wasShown && !newlyUsable && before.same(after) {
		return
	}
	now := s.now()
	if _, err := s.change("", func(tx *sql.Tx, w *World) error {
		if !newlyUsable || w.relays[id] == nil {
			return nil
		}
		_, err := tx.Exec(`UPDATE relays SET usable_at = ? WHERE id = ?`, ms(now), id)
		return err
	}); err != nil {
		s.logf("relay %s: publish the check: %v", id, err)
	}
}

// probeOrder puts the control server's probes first.
func probeOrder(from string) int {
	if from == fromControl {
		return 0
	}
	return 1
}

func nameOf(w *World, id proto.RelayID) string {
	if rl := w.relays[id]; rl != nil {
		return rl.Name
	}
	return "gone"
}

func problemCodes(st *proto.RelayStatus) string {
	var codes []string
	for _, p := range st.Problems {
		codes = append(codes, p.Code)
	}
	if len(codes) == 0 {
		return ""
	}
	return " (" + strings.Join(codes, ", ") + ")"
}

// card is a relay's status as the views have it now, to tell a check that
// changed it from one that did not.
type card struct {
	status *proto.RelayStatus
	key    string
}

func (c card) same(d card) bool { return c.key == d.key }
func (c card) usable() bool {
	return c.status != nil && (c.status.State == proto.RelayOK || c.status.State == proto.RelayPartial)
}

func (s *Server) cardOf(id proto.RelayID) card {
	st := s.state.Load()
	rl := st.w.relays[id]
	if rl == nil {
		return card{}
	}
	status := st.w.statusOf(rl, s.now(), s.hub.onlineSnapshot())
	b, _ := json.Marshal(status)
	return card{status, string(b)}
}

// scheduleChecks starts the checks due: each relay online that registered
// gets one every checkEvery. The sweep calls it.
func (s *Server) scheduleChecks() {
	if s.cfg.ProbeRelay == nil {
		return
	}
	st := s.state.Load()
	var due []proto.RelayID
	s.checks.mu.Lock()
	for id := range s.checks.byRelay {
		if st.w.relays[id] == nil {
			delete(s.checks.byRelay, id)
		}
	}
	for id, m := range st.w.relayMeta {
		if !checked(m) || !ready(st.w.relays[id]) || s.relayLive.offline(id) {
			continue
		}
		rc := s.checks.byRelay[id]
		if rc == nil || rc.running == nil && s.now().Sub(rc.at) >= checkEvery {
			due = append(due, id)
		}
	}
	s.checks.mu.Unlock()
	slices.Sort(due)
	for _, id := range due {
		s.checkRelay(id, false, checkEvery, "every 10 minutes")
	}
}

// checked reports whether a relay is one the checks are for: one whose
// program registers (not the seed's derper, which never does).
func checked(m *relayMeta) bool {
	return m != nil && (m.SecretHash != "" || !m.CreatedAt.IsZero() || !m.LeftAt.IsZero())
}

// statusOf is relay rl's state and reasons, for its card: from whether it
// is set up and online, what the checks saw and what its owner's devices
// say. nil for a relay the checks are not for.
func (w *World) statusOf(rl *proto.Relay, now time.Time, isOnline online) *proto.RelayStatus {
	m := w.relayMeta[rl.ID]
	if !checked(m) {
		return nil
	}
	st := &proto.RelayStatus{Problems: []proto.RelayProblem{}, UsableAt: m.UsableAt}
	if !ready(rl) {
		st.State = proto.RelayWaiting
		if !m.LeftAt.IsZero() {
			st.Problems = append(st.Problems, proto.RelayProblem{Code: proto.RelayProblemUninstalled, Level: proto.RelayLevelInfo,
				Message: "已经在服务器上卸载了", Advice: "要再用，在 Deneb 里点\"重新安装\"，到服务器上运行新的安装命令"})
		}
		return st
	}
	if w.relayStatus != nil {
		if on, _ := w.relayStatus(rl.ID); !on {
			st.State = proto.RelayOffline
			w.addCrashing(st, m)
			return st
		}
	}
	var snap checkSnapshot
	if w.relayChecked != nil {
		snap = w.relayChecked(rl.ID)
	}
	st.Checking, st.Probes = snap.shown, snap.probes
	st.Devices = w.relayDevices(rl, isOnline)
	if !snap.done {
		st.State = proto.RelayChecking
		return st
	}
	w.judge(st, rl, m)
	w.addCrashing(st, m)
	if rl.DERPPort != defaultRelayDERPPort || rl.STUNPort != defaultRelaySTUNPort {
		st.Problems = append(st.Problems, proto.RelayProblem{Code: proto.RelayProblemPorts, Level: proto.RelayLevelInfo,
			DERPPort: rl.DERPPort, STUNPort: rl.STUNPort,
			Message: fmt.Sprintf("用的端口是 TCP %d、UDP %d，不是默认的 %d、%d", rl.DERPPort, rl.STUNPort, defaultRelayDERPPort, defaultRelaySTUNPort),
			Advice:  "放行和转发端口时，用这两个"})
	}
	// The worst first; the state follows it.
	order := map[string]int{proto.RelayLevelError: 0, proto.RelayLevelWarn: 1, proto.RelayLevelInfo: 2}
	slices.SortStableFunc(st.Problems, func(a, b proto.RelayProblem) int { return order[a.Level] - order[b.Level] })
	switch {
	case len(st.Problems) > 0 && st.Problems[0].Level == proto.RelayLevelError:
		st.State = proto.RelayUnreachable
		st.Alert = !m.UsableAt.IsZero()
	case len(st.Problems) > 0 && st.Problems[0].Level == proto.RelayLevelWarn:
		st.State = proto.RelayPartial
	default:
		st.State = proto.RelayOK
	}
	return st
}

// famSeen is what the probes saw of one of a relay's addresses.
type famSeen struct {
	addr netip.Addr
	// TCP answered with its certificate; with another; some place could
	// try at all.
	tcpOK, pin, tested bool
	// STUN answered; some place asked it; it has a STUN port.
	udpOK, udpTried, stun bool
}

// seen sums up the probes of address a.
func seen(probes []proto.RelayProbe, a netip.Addr) famSeen {
	f := famSeen{addr: a}
	for _, p := range probes {
		if p.Addr != a {
			continue
		}
		switch p.TCP {
		case proto.RelayProbeOK:
			f.tcpOK = true
		case proto.RelayProbePin:
			f.pin = true
		}
		if p.TCP != proto.RelayProbeNoRoute {
			f.tested = true
		}
		switch p.UDP {
		case proto.RelayProbeOK:
			f.udpOK, f.udpTried, f.stun = true, true, true
		case proto.RelayProbeNone, "":
		case proto.RelayProbeNoRoute:
			f.stun = true
		default:
			f.udpTried, f.stun = true, true
		}
	}
	return f
}

// judge works out a checked relay's problems: whether TCP answers on its
// addresses, why not if not, UDP, and its owner's devices.
func (w *World) judge(st *proto.RelayStatus, rl *proto.Relay, m *relayMeta) {
	f := m.Facts
	var v4, v6 *famSeen
	if rl.IPv4.IsValid() {
		s := seen(st.Probes, rl.IPv4)
		v4 = &s
	}
	if rl.IPv6.IsValid() {
		s := seen(st.Probes, rl.IPv6)
		v6 = &s
	}
	both := v4 != nil && v6 != nil
	add := func(p proto.RelayProblem) { st.Problems = append(st.Problems, p) }
	famWord := func(fam string) string {
		if !both {
			return ""
		}
		if fam == "ipv6" {
			return "（IPv6）"
		}
		return "（IPv4）"
	}
	ports := fmt.Sprintf("TCP %d、UDP %d", rl.DERPPort, rl.STUNPort)
	recheck := "放行后不用重装，Deneb 每 10 分钟会重新检查，也可以点\"重新检查\""

	// why says why TCP does not answer on one address.
	why := func(s *famSeen, fam string, level string) proto.RelayProblem {
		switch {
		case !w.probeAny && !probe.Public(s.addr):
			p := proto.RelayProblem{Code: proto.RelayProblemNoPublicIP, Level: level, Addr: s.addr, Family: fam,
				Message: fmt.Sprintf("这台机器的地址 %s 不在公网上，设备从外面连不进来", s.addr),
				Advice:  "当中转站要一台有公网地址的服务器"}
			if fam == "ipv4" && v6 == nil {
				p.Advice += "；有公网 IPv6 的话，在安装时写上它，只有支持 IPv6 的设备能用"
			}
			return p
		case s.pin:
			return proto.RelayProblem{Code: proto.RelayProblemPinMismatch, Level: level, Port: rl.DERPPort, Family: fam, Addr: s.addr,
				Message: fmt.Sprintf("TCP %d%s 上回答的不是这个中转站：证书对不上", rl.DERPPort, famWord(fam)),
				Advice:  "端口可能转发到了别的机器，或者被别的程序占着；检查端口转发和占用这个端口的程序"}
		case !s.tested:
			return proto.RelayProblem{Code: proto.RelayProblemUntested, Level: proto.RelayLevelWarn, Family: fam, Addr: s.addr,
				Message: fmt.Sprintf("没法从外网检查它的 %s 地址", strings.ToUpper(fam[:2])+fam[2:]),
				Advice:  "等设备连上它就知道能不能用"}
		}
		// Behind a carrier's NAT: the router's own address is not public.
		if pm := f.PortMap; fam == "ipv4" && pm != nil && pm.External.IsValid() && !probe.Public(pm.External) && !w.probeAny {
			p := proto.RelayProblem{Code: proto.RelayProblemNoPublicIP, Level: level, Addr: pm.External, Family: fam,
				Message: fmt.Sprintf("路由器拿到的地址 %s 是运营商的大 NAT，这台机器没有公网 IPv4", pm.External),
				Advice:  "这样的网络不能当中转站；请运营商给公网 IPv4，或者换一台有公网地址的服务器"}
			if v6 != nil {
				p.Advice = "IPv4 当不了中转站，只有支持 IPv6 的设备能用它"
			}
			return p
		}
		p := proto.RelayProblem{Code: proto.RelayProblemTCPBlocked, Level: level, Port: rl.DERPPort, Family: fam, Addr: s.addr,
			Firewall: f.Firewall, Message: fmt.Sprintf("从外网连不上 TCP %d%s", rl.DERPPort, famWord(fam))}
		lan := privateLocal(f.Local, fam)
		switch {
		case f.Firewall != "" && !f.FirewallOpen:
			p.Advice = fmt.Sprintf("系统防火墙 %s 没有放行：%s；云服务器的安全组也要放行 %s。%s", f.Firewall, firewallCommand(f.Firewall, rl), ports, recheck)
		case fam == "ipv4" && lan.IsValid() && !onCloud(f.Region):
			// Its own addresses are private: behind a router, or a cloud's
			// 1:1 NAT that did not say so.
			p.Code, p.Addr = proto.RelayProblemBehindNAT, lan
			p.Message = fmt.Sprintf("这台机器在路由器后面（局域网地址 %s），从外网连不进来", lan)
			p.Advice = fmt.Sprintf("请在路由器上把 %s 转发到 %s，或者打开路由器的 UPnP；如果是云服务器，在安全组的入方向里放行 %s。%s", ports, lan, ports, recheck)
			if pm := f.PortMap; pm != nil && pm.Error != "" {
				p.Advice = "自动映射端口没成功（" + pm.Error + "）。" + p.Advice
			}
		case onCloud(f.Region):
			p.Advice = fmt.Sprintf("请在云服务商的安全组（入方向）里放行 %s。%s", ports, recheck)
		default:
			p.Advice = fmt.Sprintf("请在云服务商的安全组里放行 %s；系统防火墙（ufw、firewalld）也要放行；家里的机器要在路由器上转发这两个端口。%s", ports, recheck)
		}
		return p
	}

	tcpOK := v4 != nil && v4.tcpOK || v6 != nil && v6.tcpOK
	if !tcpOK {
		for _, s := range []*famSeen{v4, v6} {
			if s != nil {
				add(why(s, probe.Family(s.addr), proto.RelayLevelError))
			}
		}
		if v4 == nil && v6 == nil {
			add(proto.RelayProblem{Code: proto.RelayProblemNoPublicIP, Level: proto.RelayLevelError,
				Message: "不知道这台机器的地址", Advice: "重新运行安装命令"})
		}
		return
	}
	switch {
	case v6 != nil && v6.tcpOK && (v4 == nil || !v4.tcpOK):
		p := proto.RelayProblem{Code: proto.RelayProblemIPv6Only, Level: proto.RelayLevelWarn, Port: rl.DERPPort, Family: "ipv6", Addr: v6.addr,
			Message: "只有 IPv6 连得上", Advice: "只有支持 IPv6 的设备能用它"}
		if v4 != nil {
			v4why := why(v4, "ipv4", proto.RelayLevelWarn)
			p.Advice += "；IPv4：" + v4why.Message + "。" + v4why.Advice
		}
		add(p)
	case v6 != nil && v6.tested && !v6.tcpOK:
		add(proto.RelayProblem{Code: proto.RelayProblemIPv6Blocked, Level: proto.RelayLevelInfo, Port: rl.DERPPort, Family: "ipv6", Addr: v6.addr,
			Message: fmt.Sprintf("IPv6 地址 %s 从外网连不上", v6.addr),
			Advice:  fmt.Sprintf("设备会用 IPv4；要用 IPv6，也为 IPv6 放行 %s", ports)})
	}
	// UDP: STUN on an address whose TCP answers.
	udpOK, udpTried, hasSTUN := false, false, false
	for _, s := range []*famSeen{v4, v6} {
		if s != nil && s.tcpOK {
			udpOK, udpTried, hasSTUN = udpOK || s.udpOK, udpTried || s.udpTried, hasSTUN || s.stun
		}
	}
	switch {
	case rl.STUNPort == 0:
		add(proto.RelayProblem{Code: proto.RelayProblemUDPBlocked, Level: proto.RelayLevelWarn,
			Message: "这个中转站没有开 UDP（STUN）", Advice: "能中转，但帮不上设备直连；重新安装时会打开"})
	case udpTried && !udpOK:
		add(proto.RelayProblem{Code: proto.RelayProblemUDPBlocked, Level: proto.RelayLevelWarn, Port: rl.STUNPort,
			Message: fmt.Sprintf("从外网连不上 UDP %d", rl.STUNPort),
			Advice:  fmt.Sprintf("能中转，但帮不上设备直连；请放行 UDP %d。%s", rl.STUNPort, recheck)})
	}
	// Its owner's devices.
	if d := st.Devices; d != nil && len(d.Failed) > 0 {
		var ids []proto.DeviceID
		for _, x := range d.Failed {
			ids = append(ids, x.Device)
		}
		add(proto.RelayProblem{Code: proto.RelayProblemDevicesFailing, Level: proto.RelayLevelWarn, Devices: ids,
			Count: len(d.Failed), Of: len(d.Failed) + len(d.Reached),
			Message: fmt.Sprintf("你的 %d 台设备连不上它", len(d.Failed)),
			Advice:  "从外网检查是通的，可能是这些设备所在的网络到它的线路不通（比如跨境线路）；它们会改用别的中转站"})
	}
}

func (w *World) addCrashing(st *proto.RelayStatus, m *relayMeta) {
	if n := m.Facts.Restarts; n >= crashingAfter {
		st.Problems = append(st.Problems, proto.RelayProblem{Code: proto.RelayProblemCrashing, Level: proto.RelayLevelWarn, Count: n,
			Message: fmt.Sprintf("中转站反复出错：一小时里重启了 %d 次", n),
			Advice:  "在服务器上运行 deneb-relay status 看原因"})
	}
}

// privateLocal is a private address of family fam among a relay's own:
// where a router would forward its ports to.
func privateLocal(local []netip.Addr, fam string) netip.Addr {
	for _, a := range local {
		if probe.Family(a) == fam && a.IsPrivate() {
			return a
		}
	}
	return netip.Addr{}
}

// onCloud reports whether a relay's region came from a cloud's metadata,
// as "aliyun:cn-hongkong" does: a security group stands in front of it.
func onCloud(region string) bool {
	kind, _, ok := strings.Cut(region, ":")
	return ok && kind != "tz" && kind != "name"
}

// firewallCommand is how to open a relay's ports in a host firewall.
func firewallCommand(fw string, rl *proto.Relay) string {
	switch fw {
	case proto.RelayFirewallUFW:
		return fmt.Sprintf("sudo ufw allow %d/tcp && sudo ufw allow %d/udp", rl.DERPPort, rl.STUNPort)
	case proto.RelayFirewallFirewalld:
		return fmt.Sprintf("sudo firewall-cmd --permanent --add-port=%d/tcp --add-port=%d/udp && sudo firewall-cmd --reload", rl.DERPPort, rl.STUNPort)
	}
	return fmt.Sprintf("放行 TCP %d、UDP %d", rl.DERPPort, rl.STUNPort)
}

// relayDevices are what its owner's devices online say of relay rl: those
// connected to it, and those that tried and could not. nil for a public
// relay, or when none has tried.
func (w *World) relayDevices(rl *proto.Relay, isOnline online) *proto.RelayDevices {
	if rl.Owner == "" || rl.Source == proto.RelayPublic || isOnline == nil {
		return nil
	}
	d := &proto.RelayDevices{Reached: []proto.DeviceID{}, Failed: []proto.RelayDeviceFailure{}}
	for _, dev := range w.devices {
		if dev.Owner != rl.Owner || !dev.usable() || !isOnline(dev.ID) {
			continue
		}
		if slices.Contains(dev.Relays, rl.ID) {
			d.Reached = append(d.Reached, dev.ID)
			continue
		}
		for _, f := range dev.RelayFailures {
			if f.Relay == rl.ID {
				d.Failed = append(d.Failed, proto.RelayDeviceFailure{Device: dev.ID, Reason: f.Reason})
				break
			}
		}
	}
	if len(d.Reached) == 0 && len(d.Failed) == 0 {
		return nil
	}
	slices.Sort(d.Reached)
	slices.SortFunc(d.Failed, func(a, b proto.RelayDeviceFailure) int { return cmp.Compare(a.Device, b.Device) })
	return d
}

// handleRelayCheck checks the relay now, from every place, and answers
// what it found: deneb-relay install prints it.
func (s *Server) handleRelayCheck(w http.ResponseWriter, r *http.Request, rl *proto.Relay, _ *World) {
	s.relayCheckedIn(rl.ID)
	done := s.checkRelay(rl.ID, true, checkAgainAfter, "asked by the relay")
	t := time.NewTimer(checkWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	case <-r.Context().Done():
		return
	}
	s.writeRelayStatus(w, r, rl.ID)
}

// handleRelayStatus answers the relay's status as its card shows it now:
// nothing is checked, and it is no sign of life (deneb-relay status asks,
// whether its service runs or not).
func (s *Server) handleRelayStatus(w http.ResponseWriter, r *http.Request, rl *proto.Relay, _ *World) {
	s.writeRelayStatus(w, r, rl.ID)
}

// writeRelayStatus answers relay id's status, and when it was last checked.
func (s *Server) writeRelayStatus(w http.ResponseWriter, r *http.Request, id proto.RelayID) {
	st := s.state.Load()
	cur := st.w.relays[id]
	if cur == nil {
		s.writeErr(w, r, newErr(http.StatusGone, proto.ErrRelayRemoved, "这个中转站已经在 Deneb 里删掉了"))
		return
	}
	status := st.w.statusOf(cur, s.now(), s.hub.onlineSnapshot())
	if status == nil {
		status = &proto.RelayStatus{State: proto.RelayOK, Problems: []proto.RelayProblem{}}
	}
	status.CheckedAt = s.checks.snapshot(id).at.UTC()
	writeJSON(w, http.StatusOK, status)
}

// handleRelayLeave is a relay's program saying it is being uninstalled:
// its secret stops working (its program hears 410 if it calls again), it
// leaves every list, and its card says it was uninstalled, for its owner
// to install it again or delete it.
func (s *Server) handleRelayLeave(w http.ResponseWriter, r *http.Request, rl *proto.Relay, _ *World) {
	var req proto.RelayLeaveRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.now()
	v, err := s.change("", func(tx *sql.Tx, w *World) error {
		m := w.relayMeta[rl.ID]
		if m == nil {
			return errNotFound("中转站已经删除了")
		}
		if err := tombstone(tx, m.SecretHash, rl.ID, "uninstalled", now); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE relays SET secret_hash = '', derp_port = 0, stun_port = 0, left_at = ? WHERE id = ?`, ms(now), rl.ID)
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.checks.forget(rl.ID)
	s.relayLive.forget(rl.ID)
	s.logf("relay %s (%s) uninstalled (%q) from %s; version %d", rl.ID, rl.Name, clip(req.Reason, 40), remoteIP(r), v)
	writeJSON(w, http.StatusOK, struct{}{})
}

// tombstone keeps the hash of a relay's secret for a month after the relay
// was deleted or uninstalled, so its program hears 410, and stops.
func tombstone(tx *sql.Tx, secretHash string, relay proto.RelayID, why string, now time.Time) error {
	if secretHash == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO relay_tombstones (secret_hash, relay, why, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (secret_hash) DO UPDATE SET relay = excluded.relay, why = excluded.why, at = excluded.at`,
		secretHash, relay, why, ms(now))
	return err
}

// tombstoneKeep is how long a deleted relay's secret is remembered.
const tombstoneKeep = 30 * 24 * time.Hour

// removedRelay reports whether a secret is a relay's that was deleted or
// uninstalled, and why.
func (s *Server) removedRelay(secretHash string) (string, bool) {
	var why string
	err := s.db.QueryRow(`SELECT why FROM relay_tombstones WHERE secret_hash = ? AND at > ?`, secretHash, ms(s.now().Add(-tombstoneKeep))).Scan(&why)
	return why, err == nil
}

func opRelayCheck(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.relayToManage(a.Relay, "重新检查", true)
	if err != nil {
		return err
	}
	if !ready(rl) {
		return errConflict("中转站还没装好：先在服务器上运行安装命令")
	}
	// The op holds the server's lock; the check publishes as it goes.
	id, s := rl.ID, c.s
	c.after = append(c.after, func() { go s.checkRelay(id, true, checkAgainAfter, "asked by "+string(c.me)) })
	return errNoChange
}

// The second place: probers (proto/probe.go) poll for jobs, run them, and
// post back what they saw.

// proberGone: a prober not heard from for this long is no longer
// connected, and checks go on without it.
const proberGone = time.Minute

// probers are the jobs for the second place, and whether one is there.
type probers struct {
	mu       sync.Mutex
	queue    []*probeJob
	byID     map[string]*probeJob
	wake     chan struct{} // closed when a job comes; then a new one
	lastPoll time.Time
}

type probeJob struct {
	id      string
	targets []proto.ProbeTarget
	done    chan []proto.RelayProbe // one send, from the prober's answer
	name    string                  // the prober that took it
	taken   bool
}

func (j *probeJob) from() string {
	if j.name == "" {
		return "(none took it)"
	}
	return j.name
}

func newProbers() *probers {
	return &probers{byID: map[string]*probeJob{}, wake: make(chan struct{})}
}

// dispatch queues a job for the prober, if one is connected; nil if none
// is, or there is nothing to check.
func (p *probers) dispatch(targets []proto.ProbeTarget, now time.Time) *probeJob {
	if len(targets) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastPoll.IsZero() || now.Sub(p.lastPoll) > proberGone {
		return nil
	}
	j := &probeJob{id: newID("pj"), targets: targets, done: make(chan []proto.RelayProbe, 1)}
	p.queue = append(p.queue, j)
	p.byID[j.id] = j
	close(p.wake)
	p.wake = make(chan struct{})
	return j
}

// drop forgets a job, answered or not.
func (p *probers) drop(j *probeJob) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.byID, j.id)
	p.queue = slices.DeleteFunc(p.queue, func(x *probeJob) bool { return x == j })
}

// take hands the jobs waiting to a prober.
func (p *probers) take(name string, now time.Time) ([]proto.ProbeJob, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastPoll = now
	var out []proto.ProbeJob
	for _, j := range p.queue {
		j.taken, j.name = true, name
		out = append(out, proto.ProbeJob{ID: j.id, Targets: j.targets})
	}
	p.queue = nil
	return out, p.wake
}

// answer hands a prober's results to the check that waits for them.
func (p *probers) answer(id, name string, results []proto.RelayProbe) bool {
	p.mu.Lock()
	j := p.byID[id]
	p.mu.Unlock()
	if j == nil || !j.taken {
		return false
	}
	byAddr := map[netip.Addr]proto.ProbeTarget{}
	for _, t := range j.targets {
		byAddr[t.Addr] = t
	}
	var out []proto.RelayProbe
	for _, r := range results {
		t, ok := byAddr[r.Addr]
		if !ok || len(out) >= 2*len(j.targets) {
			continue
		}
		r.From, r.Family = clip(name, 32), t.Family
		out = append(out, r)
	}
	select {
	case j.done <- out:
	default:
	}
	return true
}

// proberWait is how long a prober's poll waits for a job.
var proberWait = 25 * time.Second

// prober wraps a prober API handler: it wants the deployment's probe
// secret.
func (s *Server) prober(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.cfg.ProbeSecret == "" || !ok || !sameSecret(strings.TrimSpace(secret), s.cfg.ProbeSecret) {
			s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "探测的密钥不对"))
			return
		}
		h(w, r)
	}
}

func (s *Server) handleProbeNext(w http.ResponseWriter, r *http.Request) {
	var req proto.ProbeNextRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	name := cmp.Or(clip(req.Name, 32), "prober")
	t := time.NewTimer(proberWait)
	defer t.Stop()
	for {
		jobs, wake := s.probers.take(name, s.now())
		if len(jobs) > 0 {
			writeJSON(w, http.StatusOK, proto.ProbeJobs{Jobs: jobs})
			return
		}
		select {
		case <-wake:
		case <-t.C:
			writeJSON(w, http.StatusOK, proto.ProbeJobs{Jobs: []proto.ProbeJob{}})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleProbeDone(w http.ResponseWriter, r *http.Request) {
	var req proto.ProbeDone
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	name := "prober"
	s.probers.mu.Lock()
	if j := s.probers.byID[req.ID]; j != nil {
		name = j.name
	}
	s.probers.mu.Unlock()
	// A job the check gave up on already is no error: too late, that is all.
	s.probers.answer(req.ID, name, req.Results)
	writeJSON(w, http.StatusOK, struct{}{})
}
