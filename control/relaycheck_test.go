package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// Relay install, redone (M7, design/plan/m7/relay-install-api.md): the
// checks from outside and what they make of a relay, the second place,
// the same machine again, uninstalling and deleting, the install command.

// newCheckWorld is the M2 world on a server set up by mod: its relays'
// checks, the prober's secret, the download site.
func newCheckWorld(t *testing.T, mod func(*Config)) *m2World {
	t.Helper()
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	cfg := Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: DevSeed(), Now: clk.now, Logf: func(string, ...any) {}}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	w := &m2World{s: s, clk: clk,
		mac: newEngine(t, ts.URL, "mac", proto.MacOS), build: newEngine(t, ts.URL, "build", proto.Linux),
		jdesk: newEngine(t, ts.URL, "desktop", proto.Windows), jnas: newEngine(t, ts.URL, "nas", proto.Linux)}
	w.mac.login("kittors@deneb.test")
	w.build.login("kittors@deneb.test")
	w.jdesk.login("jie@deneb.test")
	w.jnas.login("jie@deneb.test")
	w.studio = w.mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: w.build.id})
	w.me, w.jie = w.mac.view().Me, w.jdesk.view().Me
	return w
}

// fakeProbes stands for the network: what each of a relay's addresses
// answers, ok unless set; with gate, a probe waits for it.
type fakeProbes struct {
	mu    sync.Mutex
	res   map[netip.Addr]proto.RelayProbe
	gate  chan struct{}
	calls int
}

func (f *fakeProbes) set(a netip.Addr, tcp, udp string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.res == nil {
		f.res = map[netip.Addr]proto.RelayProbe{}
	}
	f.res[a] = proto.RelayProbe{TCP: tcp, UDP: udp}
}

func (f *fakeProbes) probe(ctx context.Context, t proto.ProbeTarget) proto.RelayProbe {
	f.mu.Lock()
	r, ok := f.res[t.Addr]
	gate := f.gate
	f.calls++
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	if !ok {
		r = proto.RelayProbe{TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK}
	}
	return r
}

// check asks the server to check the relay now, as deneb-relay install
// does, and returns what it found.
func (r *testRelay) check() proto.RelayStatus {
	r.t.Helper()
	var st proto.RelayStatus
	if code, err := relayCall(r.t, r.url, r.secret, proto.PathRelayCheck, struct{}{}, &st); err != nil {
		r.t.Fatalf("check: %d %v", code, err)
	}
	return st
}

// status reads the relay's status without a check, as deneb-relay status
// does.
func (r *testRelay) status() proto.RelayStatus {
	r.t.Helper()
	req, _ := http.NewRequest("GET", r.url+proto.PathRelayCheck, nil)
	req.Header.Set("Authorization", "Bearer "+r.secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var st proto.RelayStatus
	if resp.StatusCode != http.StatusOK {
		r.t.Fatalf("status: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		r.t.Fatal(err)
	}
	return st
}

func codes(st proto.RelayStatus) []string {
	var out []string
	for _, p := range st.Problems {
		out = append(out, p.Code)
	}
	return out
}

var (
	lo     = netip.MustParseAddr("127.0.0.1")
	relay6 = netip.MustParseAddr("2001:db8::7")
)

// TestRelayChecks: what the checks make of a relay. It answers: usable,
// its unusual ports noted. TCP does not: unreachable, and why, from what
// it said of the machine (behind a router, the host firewall), or another
// certificate answers; it was usable, so its owner is alerted. UDP does
// not: partly. Only IPv6 does: partly. Its owner's devices cannot reach
// it though the checks can: partly, naming them. It stops syncing:
// offline.
func TestRelayChecks(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	s, url := w.s, w.mac.url
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	r := rc.Relay
	again := func() proto.RelayStatus {
		t.Helper()
		w.clk.add(checkAgainAfter + time.Second)
		return relay.check()
	}

	st := relay.check()
	if st.State != proto.RelayOK || st.Alert || st.CheckedAt.IsZero() || !slices.Equal(codes(st), []string{proto.RelayProblemPorts}) ||
		len(st.Probes) != 1 || st.Probes[0].From != fromControl || st.Probes[0].Addr != lo || st.Probes[0].TCP != proto.RelayProbeOK {
		t.Fatalf("a relay that answers: %+v", st)
	}
	w.mac.auth()
	mv := myRelay(w.mac.view(), r)
	if mv.Status == nil || mv.Status.State != proto.RelayOK || mv.Status.UsableAt.IsZero() || !mv.Status.CheckedAt.IsZero() || mv.Version != "test" {
		t.Fatalf("its card: %+v %+v", mv, mv.Status)
	}

	// It says it is behind a router, its LAN address 192.168.1.5; TCP does
	// not answer.
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{DERPPort: 8443, STUNPort: 3479, Pin: testPin, Version: "test",
		Local: []netip.Addr{netip.MustParseAddr("192.168.1.5")}, PortMap: &proto.RelayPortMap{Error: "no UPnP"}}})
	probes.set(lo, proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	st = again()
	p := st.Problems[0]
	if st.State != proto.RelayUnreachable || !st.Alert || p.Code != proto.RelayProblemBehindNAT || p.Addr != netip.MustParseAddr("192.168.1.5") ||
		!strings.Contains(p.Advice, "转发到 192.168.1.5") || !strings.Contains(p.Advice, "no UPnP") || p.Level != proto.RelayLevelError {
		t.Fatalf("behind a router: %+v", st)
	}
	if mv := myRelay(w.mac.view(), r); !mv.Status.Alert || mv.Status.State != proto.RelayUnreachable {
		t.Fatalf("its card: %+v", mv.Status)
	}

	// A public address of its own, and ufw closed: the command to open it.
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{DERPPort: 8443, STUNPort: 3479, Pin: testPin, Version: "test",
		Local: []netip.Addr{lo}, Firewall: proto.RelayFirewallUFW}})
	st = again()
	if p := st.Problems[0]; p.Code != proto.RelayProblemTCPBlocked || p.Firewall != "ufw" || !strings.Contains(p.Advice, "sudo ufw allow 8443/tcp && sudo ufw allow 3479/udp") ||
		!strings.Contains(p.Message, "TCP 8443") {
		t.Fatalf("ufw: %+v", st)
	}

	// Another certificate answers.
	probes.set(lo, proto.RelayProbePin, proto.RelayProbeOK)
	if st = again(); st.Problems[0].Code != proto.RelayProblemPinMismatch || st.State != proto.RelayUnreachable {
		t.Fatalf("another certificate: %+v", st)
	}

	// TCP, but no UDP: partly, no longer an alert.
	probes.set(lo, proto.RelayProbeOK, proto.RelayProbeTimeout)
	st = again()
	if st.State != proto.RelayPartial || st.Alert || !slices.Contains(codes(st), proto.RelayProblemUDPBlocked) {
		t.Fatalf("no UDP: %+v", st)
	}

	// Its IPv6 answers, its IPv4 not. The network is so before it says so:
	// a new address is checked at once.
	probes.set(lo, proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	probes.set(relay6, proto.RelayProbeOK, proto.RelayProbeOK)
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{IPv4: lo, IPv6: relay6, DERPPort: 8443, STUNPort: 3479, Pin: testPin, Version: "test"}})
	st = again()
	if st.State != proto.RelayPartial || st.Problems[0].Code != proto.RelayProblemIPv6Only || len(st.Probes) != 2 {
		t.Fatalf("IPv6 only: %+v", st)
	}
	// IPv4 answers again, IPv6 not: a note, usable.
	probes.set(lo, proto.RelayProbeOK, proto.RelayProbeOK)
	probes.set(relay6, proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	if st = again(); st.State != proto.RelayOK || !slices.Contains(codes(st), proto.RelayProblemIPv6Blocked) {
		t.Fatalf("IPv6 blocked: %+v", st)
	}

	// The build server, in 工作室 with it, cannot reach it; the Mac can.
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	w.build.auth()
	w.build.stream(t, false).next(t)
	w.mac.stream(t, false).next(t)
	w.build.report(proto.Report{RelayFailures: []proto.RelayFailure{{Relay: r, Reason: proto.RelayFailTimeout, Since: w.clk.now()}}})
	w.mac.report(proto.Report{HomeRelay: r, Relays: []proto.RelayID{r}})
	st = again()
	if st.State != proto.RelayPartial || st.Devices == nil || !slices.Equal(st.Devices.Reached, []proto.DeviceID{w.mac.id}) ||
		len(st.Devices.Failed) != 1 || st.Devices.Failed[0].Device != w.build.id || st.Devices.Failed[0].Reason != proto.RelayFailTimeout {
		t.Fatalf("a device that cannot reach it: %+v %+v", st, st.Devices)
	}
	i := slices.IndexFunc(st.Problems, func(p proto.RelayProblem) bool { return p.Code == proto.RelayProblemDevicesFailing })
	if i < 0 || st.Problems[i].Count != 1 || st.Problems[i].Of != 2 || !slices.Equal(st.Problems[i].Devices, []proto.DeviceID{w.build.id}) {
		t.Fatalf("devices failing: %+v", st.Problems)
	}

	// It keeps restarting.
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{IPv4: lo, IPv6: relay6, DERPPort: 8443, STUNPort: 3479, Pin: testPin, Version: "test", Restarts: 5}})
	if st = again(); !slices.Contains(codes(st), proto.RelayProblemCrashing) {
		t.Fatalf("restarting: %+v", st)
	}

	// No sync for 3 minutes: offline.
	w.clk.add(relayOfflineAfter)
	s.sweep()
	w.mac.auth()
	if mv := myRelay(w.mac.view(), r); mv.Status.State != proto.RelayOffline || mv.Online {
		t.Fatalf("offline: %+v", mv.Status)
	}
}

// TestRelayNoPublicAddress: a relay whose address is not on the Internet
// is not checked, and its card says it cannot be a relay; nor one behind a
// carrier's NAT.
func TestRelayNoPublicAddress(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	s, url := w.s, w.mac.url
	s.cfg.Dev = false
	s.publishChecks("", "a production server")
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "家里"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	st := relay.check()
	if st.State != proto.RelayUnreachable || st.Problems[0].Code != proto.RelayProblemNoPublicIP || probes.calls != 0 {
		t.Fatalf("127.0.0.1: %+v, %d probes", st, probes.calls)
	}

	// A public address, but the router's own is a carrier's. (Moving
	// starts a check of its own.)
	probes.set(netip.MustParseAddr("203.0.113.9"), proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{IPv4: netip.MustParseAddr("203.0.113.9"), DERPPort: 8443, STUNPort: 3479, Pin: testPin,
		PortMap: &proto.RelayPortMap{Method: "upnp", External: netip.MustParseAddr("100.64.3.4"), DERPPort: 8443}}})
	w.clk.add(checkAgainAfter + time.Second)
	if st = relay.check(); st.Problems[0].Code != proto.RelayProblemNoPublicIP || st.Problems[0].Addr != netip.MustParseAddr("100.64.3.4") || probes.calls == 0 || st.Alert {
		t.Fatalf("a carrier's NAT: %+v, %d probes", st, probes.calls)
	}
}

// proberCall is the prober's request, quietly: the test's server may go
// before its last poll ends.
func proberCall(ctx context.Context, url, secret, path string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", url+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// TestRelaySecondPlace: a prober with the probe secret checks too; a relay
// the control server cannot reach but the prober can is usable. Without
// the prober, only the control server's check counts.
func TestRelaySecondPlace(t *testing.T) {
	proberWait = 200 * time.Millisecond
	t.Cleanup(func() { proberWait = 25 * time.Second })
	probes := &fakeProbes{}
	secret := strings.Repeat("s", 32)
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay, c.ProbeSecret = probes.probe, secret })
	url := w.mac.url
	probes.set(lo, proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	if st := relay.check(); st.State != proto.RelayUnreachable || len(st.Probes) != 1 {
		t.Fatalf("no prober: %+v", st)
	}
	if code, _ := relayCall(t, url, "wrong", proto.PathProbeNext, proto.ProbeNextRequest{Name: "x"}, nil); code != 401 {
		t.Fatalf("a wrong secret: %d", code)
	}

	// The prober polls, and sees the relay answer.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		for ctx.Err() == nil {
			var jobs proto.ProbeJobs
			if err := proberCall(ctx, url, secret, proto.PathProbeNext, proto.ProbeNextRequest{Name: "hk-spare"}, &jobs); err != nil {
				return
			}
			for _, j := range jobs.Jobs {
				var rs []proto.RelayProbe
				for _, tg := range j.Targets {
					rs = append(rs, proto.RelayProbe{Addr: tg.Addr, Family: tg.Family, TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK})
				}
				proberCall(ctx, url, secret, proto.PathProbeDone, proto.ProbeDone{ID: j.ID, Results: rs}, nil)
			}
		}
	}()
	// Wait for its first poll.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		w.s.probers.mu.Lock()
		polled := !w.s.probers.lastPoll.IsZero()
		w.s.probers.mu.Unlock()
		if polled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prober never polled")
		}
	}
	w.clk.add(checkAgainAfter + time.Second)
	st := relay.check()
	if st.State != proto.RelayOK || len(st.Probes) != 2 || st.Probes[0].From != fromControl || st.Probes[1].From != "hk-spare" ||
		st.Probes[1].TCP != proto.RelayProbeOK || st.Probes[0].TCP != proto.RelayProbeTimeout {
		t.Fatalf("with the prober: %+v", st)
	}
}

// TestRelaySameMachine: a code for a new relay, used on a machine that is
// already one of its owner's relays (the same certificate, or the same
// address and port), sets that one up again; on someone else's, or another
// machine, it makes a new one. The secret it replaces hears 410.
func TestRelaySameMachine(t *testing.T) {
	w := newCheckWorld(t, nil)
	s := w.s
	s.cfg.Pin = testPin
	pin := func(n int) string {
		sum := sha256.Sum256([]byte(fmt.Sprint("machine", n)))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	v4 := netip.MustParseAddr("198.51.100.20")
	join := func(e *engine) string { return e.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin.Code }

	_, first := joinRegister(t, s, join(w.mac), proto.RelayInfo{IPv4: v4, DERPPort: 47445, Pin: pin(1), Region: "tz:Asia/Tokyo"})
	if first.Existing || first.Name != "东京" {
		t.Fatalf("the first: %+v", first)
	}
	// The same certificate, elsewhere: the same relay, its name kept.
	_, again := joinRegister(t, s, join(w.mac), proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.21"), DERPPort: 47446, Pin: pin(1)})
	if !again.Existing || again.Relay != first.Relay || again.Name != "东京" || again.Secret == first.Secret {
		t.Fatalf("the same certificate: %+v", again)
	}
	if code, err := relayCall(t, s.cfg.PublicURL, first.Secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 410 || err.Code != proto.ErrRelayRemoved {
		t.Fatalf("the secret it replaced: %d %v", code, err)
	}
	// The same address and port, a new certificate (set up anew).
	_, third := joinRegister(t, s, join(w.mac), proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.21"), DERPPort: 47446, Pin: pin(2)})
	if !third.Existing || third.Relay != first.Relay {
		t.Fatalf("the same address and port: %+v", third)
	}
	// Another machine; and 阿杰's, with the first certificate.
	_, other := joinRegister(t, s, join(w.mac), proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.22"), DERPPort: 47445, Pin: pin(3)})
	_, his := joinRegister(t, s, join(w.jdesk), proto.RelayInfo{IPv4: v4, DERPPort: 47445, Pin: pin(1)})
	if other.Existing || other.Relay == first.Relay || his.Existing || his.Relay == first.Relay {
		t.Fatalf("other machines: %+v, %+v", other, his)
	}
	if n := len(myRelaysOf(w.mac.view())); n != 2 {
		t.Fatalf("I have %d relays", n)
	}
}

func myRelaysOf(v *proto.UserView) []proto.RelayID {
	var out []proto.RelayID
	for _, r := range v.MyRelays {
		if r.ID != "hk" {
			out = append(out, r.ID)
		}
	}
	return out
}

// TestRelayLeave: a relay's program uninstalls itself: its secret hears
// 410 from then on, it leaves every list and network map, and its card
// waits, saying it was uninstalled, until it is set up again. A relay
// deleted in Deneb hears 410 too, for a month.
func TestRelayLeave(t *testing.T) {
	w := newCheckWorld(t, nil)
	s, url := w.s, w.mac.url
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	r := rc.Relay
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	if relayOf(netMapOf(s, w.build.id), r) == nil {
		t.Fatal("not in the map before")
	}
	if code, err := relayCall(t, url, relay.secret, proto.PathRelayLeave, proto.RelayLeaveRequest{Reason: "uninstall"}, nil); err != nil {
		t.Fatalf("leave: %d %v", code, err)
	}
	if code, err := relayCall(t, url, relay.secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 410 || err.Code != proto.ErrRelayRemoved || !strings.Contains(err.Message, "卸载") {
		t.Fatalf("after leaving: %d %v", code, err)
	}
	if relayOf(netMapOf(s, w.build.id), r) != nil {
		t.Fatal("still in the map")
	}
	mv := myRelay(w.mac.view(), r)
	if mv == nil || mv.Registered || mv.Status == nil || mv.Status.State != proto.RelayWaiting || len(mv.Status.Problems) != 1 ||
		mv.Status.Problems[0].Code != proto.RelayProblemUninstalled {
		t.Fatalf("its card: %+v %+v", mv, mv.Status)
	}
	// Installed again: back, no longer uninstalled.
	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Relay: r}).Result.RelayJoin
	control, _, token, _ := proto.ParseRelayJoinCode(j.Code)
	var reg proto.RelayRegisterResult
	if _, err := relayCall(t, control, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: token,
		RelayInfo: proto.RelayInfo{DERPPort: 8443, STUNPort: 3479, Pin: testPin}}, &reg); err != nil || reg.Relay != r {
		t.Fatalf("installed again: %+v %v", reg, err)
	}
	if mv := myRelay(w.mac.view(), r); !mv.Registered || mv.Status.State == proto.RelayWaiting || relayOf(netMapOf(s, w.build.id), r) == nil {
		t.Fatalf("installed again, its card: %+v", mv.Status)
	}

	// Deleted in Deneb: 410, for a month; then the cleanup forgets it.
	w.mac.must(proto.OpRelayDelete, proto.OpArgs{Relay: r})
	if code, err := relayCall(t, url, reg.Secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 410 || !strings.Contains(err.Message, "删掉") {
		t.Fatalf("deleted: %d %v", code, err)
	}
	w.clk.add(tombstoneKeep + time.Hour)
	if c, err := s.cleanup(); err != nil || c.tombstones != 2 { // the uninstalled one's and the deleted one's
		t.Fatalf("cleanup: %+v %v", c, err)
	}
	if code, _ := relayCall(t, url, reg.Secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 401 {
		t.Fatalf("a month on: %d", code)
	}
}

// TestRelayJoinLasts: a join code works for 24 hours, once.
func TestRelayJoinLasts(t *testing.T) {
	w := newCheckWorld(t, nil)
	s := w.s
	s.cfg.Pin = testPin
	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	if !j.ExpiresAt.Equal(w.clk.now().Add(24 * time.Hour)) {
		t.Fatalf("expires %v", j.ExpiresAt)
	}
	w.clk.add(23 * time.Hour)
	if code, _ := joinRegister(t, s, j.Code, proto.RelayInfo{}); code != 200 {
		t.Fatalf("after 23 hours: %d", code)
	}
	if code, _ := joinRegister(t, s, j.Code, proto.RelayInfo{}); code != 401 {
		t.Fatalf("twice: %d", code)
	}
	w.mac.auth()
	j2 := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	w.clk.add(24 * time.Hour)
	_, _, token, _ := proto.ParseRelayJoinCode(j2.Code)
	if code, err := relayCall(t, s.cfg.PublicURL, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: token,
		RelayInfo: proto.RelayInfo{DERPPort: 1, Pin: testPin}}, nil); code != 401 || err.Message != "加入码已过期，请在 Deneb 里重新生成" {
		t.Fatalf("after a day: %d %v", code, err)
	}
}

// TestRelayFailuresReported: what a device says of the relays it could not
// use is kept cleaned up: relays that exist, it is not connected to, with
// a reason it may give.
func TestRelayFailuresReported(t *testing.T) {
	w := newCheckWorld(t, nil)
	s, url := w.s, w.mac.url
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	registerRelay(t, url, rc.RelayToken)
	r := rc.Relay
	w.build.report(proto.Report{HomeRelay: "hk", Relays: []proto.RelayID{"hk"}, RelayFailures: []proto.RelayFailure{
		{Relay: r, Reason: proto.RelayFailRefused, Refused: proto.RelayDenyNotListed},
		{Relay: r, Reason: proto.RelayFailTLS},        // again
		{Relay: "r_gone", Reason: proto.RelayFailTLS}, // no such relay
		{Relay: "hk", Reason: proto.RelayFailTLS},     // connected to it
		{Relay: rc.Relay + "x", Reason: "weird"},
	}})
	d := s.state.Load().w.devices[w.build.id]
	if len(d.RelayFailures) != 1 || d.RelayFailures[0].Relay != r || d.RelayFailures[0].Refused != proto.RelayDenyNotListed {
		t.Fatalf("kept: %+v", d.RelayFailures)
	}
	v := s.Version()
	w.build.report(proto.Report{HomeRelay: "hk", Relays: []proto.RelayID{"hk"}, RelayFailures: []proto.RelayFailure{{Relay: r, Reason: proto.RelayFailRefused, Refused: proto.RelayDenyNotListed}}})
	if s.Version() != v {
		t.Fatal("the same report again moved the version")
	}
}

// TestRelayCheckOp: its owner asks for a check (the card's "重新检查"):
// the card says 正在检查 until it is done. No one else may (authz_test).
func TestRelayCheckOp(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	url := w.mac.url
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	first := relay.check()
	// deneb-relay status reads what the card says, checking nothing again.
	probes.mu.Lock()
	n := probes.calls
	probes.mu.Unlock()
	if st := relay.status(); st.State != first.State || st.CheckedAt.IsZero() || !st.CheckedAt.Equal(first.CheckedAt) {
		t.Fatalf("status: %+v, checked: %+v", st, first)
	}
	probes.mu.Lock()
	if probes.calls != n {
		t.Fatalf("status checked again: %d probes, %d before", probes.calls, n)
	}
	probes.gate = make(chan struct{})
	probes.mu.Unlock()
	w.clk.add(checkAgainAfter + time.Second)
	w.mac.auth()
	w.mac.must(proto.OpRelayCheck, proto.OpArgs{Relay: rc.Relay})
	seen := func(want bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if mv := myRelay(w.mac.view(), rc.Relay); mv.Status.Checking == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("checking never became %v", want)
			}
		}
	}
	seen(true)
	close(probes.gate)
	seen(false)

	// A relay not set up yet cannot be checked.
	rc2 := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "大阪"}).Result
	w.mac.refused(proto.ErrConflict, proto.OpRelayCheck, proto.OpArgs{Relay: rc2.Relay})
}

// TestRelayCheckWhilePublished: who asks while a check publishes what it
// found waits for it, and hears of it once the card says it: its card
// usable, since when written down. The relay's program asks; asked again
// at once, the check under way stands for that too.
func TestRelayCheckWhilePublished(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	found := make(chan struct{}, 1)
	gate := make(chan struct{})
	w.s.checks.mu.Lock()
	w.s.checks.publishing = func(proto.RelayID) {
		select {
		case found <- struct{}{}:
		default:
		}
		<-gate
	}
	w.s.checks.mu.Unlock()
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, w.mac.url, rc.RelayToken)

	answered := make(chan proto.RelayStatus, 2)
	ask := func() {
		go func() {
			var st proto.RelayStatus
			req, _ := http.NewRequest("POST", relay.url+proto.PathRelayCheck, strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+relay.secret)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				json.NewDecoder(resp.Body).Decode(&st)
				resp.Body.Close()
			}
			answered <- st
		}()
	}
	ask()
	<-found // its check found it usable, and has not said so
	ask()
	select {
	case st := <-answered:
		t.Fatalf("answered before its card said it: %+v", st)
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	w.mac.auth()
	for range 2 {
		st := <-answered
		if mv := myRelay(w.mac.view(), rc.Relay); st.State != proto.RelayOK || mv.Status == nil || mv.Status.UsableAt.IsZero() {
			t.Fatalf("checked: %+v; its card: %+v", st, mv.Status)
		}
	}
	probes.mu.Lock()
	defer probes.mu.Unlock()
	if probes.calls != 1 {
		t.Fatalf("%d probes for two asks at once", probes.calls)
	}
}

// TestRelaySetUpNotCheckedEarly: just set up, a relay is not checked
// before its program runs (its ports may not listen yet): its card says
// 检查中 until its program asks, once they listen (deneb-relay install),
// and then gets a check of its own. One whose program does not ask (a
// Docker one, an old one) is checked soon after it first syncs. Asked
// again within checkAgainAfter, the last check stands.
func TestRelaySetUpNotCheckedEarly(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	s := w.s
	calls := func() int {
		probes.mu.Lock()
		defer probes.mu.Unlock()
		return probes.calls
	}
	// Its service does not listen yet.
	probes.set(lo, proto.RelayProbeRefused, proto.RelayProbeTimeout)
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "东京"}).Result
	relay, _ := registerRelay(t, w.mac.url, rc.RelayToken)
	w.clk.add(sweepEvery) // a sweep while deneb-relay install still sets it up
	s.scheduleChecks()
	w.mac.auth()
	if mv := myRelay(w.mac.view(), rc.Relay); calls() != 0 || mv.Status == nil || mv.Status.State != proto.RelayChecking {
		t.Fatalf("checked before its program ran: %d probes, %+v", calls(), mv.Status)
	}
	// It listens; its program asks.
	probes.set(lo, proto.RelayProbeOK, proto.RelayProbeOK)
	if st := relay.check(); st.State != proto.RelayOK || calls() != 1 {
		t.Fatalf("asked: %+v, %d probes", st, calls())
	}
	if st := relay.check(); st.State != proto.RelayOK || calls() != 1 {
		t.Fatalf("asked again at once: %+v, %d probes", st, calls())
	}
	w.clk.add(checkAgainAfter)
	if relay.check(); calls() != 2 {
		t.Fatalf("asked again later: %d probes", calls())
	}

	// A Docker one: set up, its program syncs, and never asks.
	s.cfg.Pin = testPin
	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Name: "大阪"}).Result.RelayJoin
	_, reg := joinRegister(t, s, j.Code, proto.RelayInfo{STUNPort: defaultRelaySTUNPort})
	docker := &testRelay{t: t, url: s.cfg.PublicURL, secret: reg.Secret}
	before := calls()
	docker.sync(proto.RelaySyncRequest{})
	s.scheduleChecks()
	if calls() != before {
		t.Fatalf("checked the moment it synced: %d probes, %d before", calls(), before)
	}
	w.clk.add(checkSoonAfter)
	s.scheduleChecks()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if st := docker.status(); st.State == proto.RelayOK && calls() > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not checked after it synced: %+v, %d probes", docker.status(), calls())
		}
	}
}

// TestSeenSkipped: a place that did not check an address (the prober,
// which checks only addresses on the Internet) says nothing about it: with
// no route from here either, it is untested, not blocked.
func TestSeenSkipped(t *testing.T) {
	f := seen([]proto.RelayProbe{
		{From: fromControl, Family: "ipv6", Addr: relay6, TCP: proto.RelayProbeNoRoute, UDP: proto.RelayProbeNoRoute},
		{From: "hk-spare", Family: "ipv6", Addr: relay6, TCP: proto.RelayProbeSkipped, UDP: proto.RelayProbeSkipped},
	}, relay6)
	if f.tested || f.tcpOK || f.udpTried || f.udpOK {
		t.Fatalf("%+v", f)
	}
}

// TestRelayTunnelPublicOnly: the Cloudflare tunnel a relay says it has
// goes into the network map for a public relay only, as a host name alone;
// a sync changes it.
func TestRelayTunnelPublicOnly(t *testing.T) {
	w := newCheckWorld(t, nil)
	s := w.s
	s.cfg.Pin = testPin
	if err := s.opsSetOperator(nil, w.me, true); err != nil {
		t.Fatal(err)
	}
	pubInfo := proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.31"), DERPPort: 47445, STUNPort: 47478,
		Pin: "sha256:" + strings.Repeat("ab", 32), Tunnel: "relay.deneb.app"}
	pub := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Public: true, Name: "香港"}).Result.RelayJoin
	_, regPub := joinRegister(t, s, pub.Code, pubInfo)
	own := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Name: "家里"}).Result.RelayJoin
	_, regOwn := joinRegister(t, s, own.Code, proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.32"), DERPPort: 47445, STUNPort: 47478,
		Pin: "sha256:" + strings.Repeat("cd", 32), Tunnel: "home.deneb.app"})
	tunnelOf := func(id proto.RelayID) string { return s.state.Load().w.relays[id].Tunnel }
	if got, own := tunnelOf(regPub.Relay), tunnelOf(regOwn.Relay); got != "relay.deneb.app" || own != "" {
		t.Fatalf("the public relay's tunnel %q, the own one's %q", got, own)
	}
	relay := &testRelay{t: t, url: s.cfg.PublicURL, secret: regPub.Secret}
	for _, c := range []struct{ said, want string }{
		{"https://relay.deneb.app", ""}, {"relay.deneb.app:443", ""}, {"hk.relay.deneb.app", "hk.relay.deneb.app"}, {"", ""},
	} {
		info := pubInfo
		info.Tunnel = c.said
		relay.sync(proto.RelaySyncRequest{Info: &info})
		if got := tunnelOf(regPub.Relay); got != c.want {
			t.Fatalf("it said %q: the network map has %q", c.said, got)
		}
	}
}

// TestPublicRelayUnreachableAlert: a public relay the checks cannot reach
// is an alert for the operators.
func TestPublicRelayUnreachableAlert(t *testing.T) {
	probes := &fakeProbes{}
	w := newCheckWorld(t, func(c *Config) { c.ProbeRelay = probes.probe })
	s := w.s
	s.cfg.Pin = testPin
	if err := s.opsSetOperator(nil, w.me, true); err != nil {
		t.Fatal(err)
	}
	probes.set(netip.MustParseAddr("198.51.100.30"), proto.RelayProbeTimeout, proto.RelayProbeTimeout)
	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Public: true, Name: "新加坡"}).Result.RelayJoin
	_, reg := joinRegister(t, s, j.Code, proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.30"), DERPPort: 47445, STUNPort: 47478})
	relay := &testRelay{t: t, url: s.cfg.PublicURL, secret: reg.Secret}
	if st := relay.check(); st.State != proto.RelayUnreachable {
		t.Fatalf("checked: %+v", st)
	}
	s.checkHealth()
	active, _ := s.Alerts()
	found := false
	for _, a := range active {
		if a.Key == "relay-unreachable/"+string(reg.Relay) && strings.Contains(a.Title, "新加坡") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no alert: %+v", active)
	}
}

// TestInstallCommand: with a download site, the install command is the
// script's line, carrying the version and the hash of its SHA256SUMS that
// the server read from the site; wget's and Docker's are there too.
// Without one, it is Docker's, as before.
func TestInstallCommand(t *testing.T) {
	sums := "aa  deneb-relay-linux-amd64\nbb  deneb-relay-linux-arm64\n"
	var mu sync.Mutex
	latest := "" // no release on the site yet
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/download/relay/latest" && latest != "":
			fmt.Fprintln(w, latest)
		case r.URL.Path == "/download/relay/0.1.9/SHA256SUMS":
			fmt.Fprint(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	setSite := func(v string) {
		mu.Lock()
		defer mu.Unlock()
		latest = v
	}
	// The server reads the site as it starts; with nothing there yet, the
	// command carries no hash, and says so: no sha256.
	w := newCheckWorld(t, func(c *Config) { c.RelayDownload = site.URL + "/download/relay" })
	s := w.s
	s.relayClient = site.Client()
	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	if j.Command != "curl -fsSL "+site.URL+"/relay | sh -s -- "+j.Code || j.Version != "" || j.SHA256 != "" || !strings.HasPrefix(j.DockerCommand, "docker run") {
		t.Fatalf("before the site had a release: %+v", j)
	}
	// A release now: read again a minute on, not ten.
	setSite("0.1.9")
	s.refreshRelayBuild(context.Background())
	if v, _ := s.installBuild(); v != "" {
		t.Fatalf("read again at once: %q", v)
	}
	w.clk.add(relayBuildRetry)
	s.refreshRelayBuild(context.Background())
	sum := sha256.Sum256([]byte(sums))
	hash := hex.EncodeToString(sum[:])
	j = w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	tail := " | sh -s -- " + j.Code + " 0.1.9:" + hash
	if j.Command != "curl -fsSL "+site.URL+"/relay"+tail || j.WgetCommand != "wget -qO- "+site.URL+"/relay"+tail ||
		j.Version != "0.1.9" || j.SHA256 != hash || !strings.Contains(j.DockerCommand, "--join "+j.Code) {
		t.Fatalf("after: %+v", j)
	}
	// A broken site keeps what it said before.
	setSite("not a version!")
	w.clk.add(relayBuildEvery)
	s.refreshRelayBuild(context.Background())
	if v, _ := s.installBuild(); v != "0.1.9" {
		t.Fatalf("after a broken read: %q", v)
	}
	// A server that starts with the site up: its first command has the
	// hash, with no wait for a sweep.
	setSite("0.1.9")
	w2 := newCheckWorld(t, func(c *Config) { c.RelayDownload = site.URL + "/download/relay" })
	if j := w2.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin; j.Version != "0.1.9" || j.SHA256 != hash || !strings.HasSuffix(j.Command, " 0.1.9:"+hash) {
		t.Fatalf("from the start: %+v", j)
	}
	if _, err := checkDownloadURL("https://x.example.com/a b/"); err == nil {
		t.Fatal("a folder with a space")
	}
}
