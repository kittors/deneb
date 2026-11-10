package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/controltest"
	"github.com/kittors/deneb/proto"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/types/key"
)

// node is a device's node key pair: the private half for a DERP client,
// the public half for the control server.
type node struct {
	priv key.NodePrivate
	pub  proto.NodePublic
}

func newNode() node {
	k := key.NewNode()
	return node{priv: k, pub: proto.NodePublic(k.Public().Raw32())}
}

// running is a relay started by a test.
type running struct {
	r      *relay
	derp   string // https://127.0.0.1:port/derp
	pin    string
	cancel context.CancelFunc
	done   chan struct{} // closed when run returns, with err set
	err    error

	mu   sync.Mutex
	logs []string
}

func (rr *running) log() string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return strings.Join(rr.logs, "\n")
}

// startRelay runs a relay with its data in dir until the test ends or
// stop is called.
func startRelay(t *testing.T, ctl *controltest.Server, dir, token string, more func(*config)) *running {
	t.Helper()
	// Never the cloud metadata probes from a test: they go to link-local
	// addresses.
	cfg := config{control: ctl.URL, token: token, data: dir, listen: "127.0.0.1:0", stun: "127.0.0.1:0", syncEvery: time.Hour, region: regionOff}
	if more != nil {
		more(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rr := &running{cancel: cancel, done: make(chan struct{})}
	ready := make(chan struct{})
	logf := func(f string, a ...any) {
		rr.mu.Lock()
		rr.logs = append(rr.logs, fmt.Sprintf(f, a...))
		rr.mu.Unlock()
	}
	go func() {
		rr.err = run(ctx, cfg, logf, func(d, _ net.Addr, r *relay) {
			rr.r, rr.derp = r, "https://"+d.String()+"/derp"
			close(ready)
		})
		close(rr.done)
	}()
	select {
	case <-ready:
	case <-rr.done:
		t.Fatalf("the relay did not start: %v\n%s", rr.err, rr.log())
	case <-time.After(10 * time.Second):
		t.Fatal("the relay did not start")
	}
	_, pin, err := loadCert(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	rr.pin = pin
	t.Cleanup(rr.stop)
	return rr
}

func (rr *running) stop() {
	rr.cancel()
	select {
	case <-rr.done:
	case <-time.After(10 * time.Second):
	}
}

// pinnedDERP is a DERP client that trusts the relay by its pin alone, as
// the engine has to. derphttp cannot take the pin itself (tlsdial panics
// on a config that skips its checks), so the URL says http and the dialer
// brings the TLS: derphttp speaks HTTP over whatever the dialer returns.
func pinnedDERP(priv key.NodePrivate, hostport, pin string, logf func(string, ...any)) (*derphttp.Client, error) {
	cfg, err := proto.PinnedTLSConfig(pin)
	if err != nil {
		return nil, err
	}
	dc, err := derphttp.NewClient(priv, "http://"+hostport+"/derp", logf, netmon.NewStatic())
	if err != nil {
		return nil, err
	}
	dc.SetURLDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", hostport)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(c, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			c.Close()
			return nil, err
		}
		return tc, nil
	})
	return dc, nil
}

func (rr *running) hostport() string {
	return strings.TrimSuffix(strings.TrimPrefix(rr.derp, "https://"), "/derp")
}

// dial connects a DERP client the way the engine does.
func (rr *running) dial(t *testing.T, n node) *derphttp.Client {
	t.Helper()
	dc, err := pinnedDERP(n.priv, rr.hostport(), rr.pin, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dc.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { dc.Close() })
	return dc
}

// recv is the client's next message, or its error.
func recv(dc *derphttp.Client) (derp.ReceivedMessage, error) {
	type got struct {
		m   derp.ReceivedMessage
		err error
	}
	c := make(chan got, 1)
	go func() { m, err := dc.Recv(); c <- got{m, err} }()
	select {
	case g := <-c:
		return g.m, g.err
	case <-time.After(5 * time.Second):
		return nil, errors.New("nothing within 5s")
	}
}

// admitted reads the client's first message: the server info, once the
// relay let it in.
func admitted(t *testing.T, dc *derphttp.Client) {
	t.Helper()
	m, err := recv(dc)
	if _, ok := m.(derp.ServerInfoMessage); err != nil || !ok {
		t.Fatalf("not let in: %T %+v, %v", m, m, err)
	}
}

// refused reads the reason the relay gives a client it turns away, and
// the end of the connection after it.
func refused(t *testing.T, dc *derphttp.Client, reason string) {
	t.Helper()
	m, err := recv(dc)
	if h, ok := m.(derp.HealthMessage); err != nil || !ok || h.Problem != proto.RelayRefusedPrefix+reason {
		t.Fatalf("turned away with %T %+v, %v; want the reason %q", m, m, err, reason)
	}
	if m, err := recv(dc); err == nil {
		t.Fatalf("after the reason the connection goes on: %T %+v", m, m)
	}
}

func relayOf(nm *proto.NetMap, id proto.RelayID) *proto.Relay {
	for i := range nm.Relays {
		if nm.Relays[i].ID == id {
			return &nm.Relays[i]
		}
	}
	return nil
}

// TestOwnRelay runs "我的服务器" end to end: my relay registers, lets in
// the devices of the LAN I drag it into and turns away the others with
// the reason, carries packets, reports them per device, closes clients
// when I take it out, and stops when I delete it.
func TestOwnRelay(t *testing.T) {
	ctl := controltest.New(t)
	buildN, macN, nasN := newNode(), newNode(), newNode()
	build := ctl.Login(t, "kittors@deneb.test", "build", buildN.pub)
	mac := ctl.Login(t, "kittors@deneb.test", "mac", macN.pub)
	nas := ctl.Login(t, "jie@deneb.test", "nas", nasN.pub)
	studio := mac.MustOp(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).LAN
	mac.MustOp(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: build.ID})
	nas.MustOp(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"})

	out := mac.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "香港"})
	id := out.Relay
	mac.MustOp(proto.OpRelayAdd, proto.OpArgs{LAN: studio, Relay: id})
	dir := t.TempDir()
	rr := startRelay(t, ctl, dir, out.RelayToken, nil)

	// The maps carry it as it registered: the address the server saw,
	// the port it listens on, its certificate's pin.
	rl := relayOf(build.NetMap(), id)
	if rl == nil || rl.Pin != rr.pin || rl.IPv4.String() != "127.0.0.1" || fmt.Sprint(rl.DERPPort) != rr.derp[strings.LastIndex(rr.derp, ":")+1:strings.LastIndex(rr.derp, "/")] || rl.STUNPort == 0 {
		t.Fatalf("my relay in the build server's map: %+v (relay at %s, pin %s)", rl, rr.derp, rr.pin)
	}

	// 工作室's devices get in; 阿杰's NAS and a stranger are told why not.
	b, m := rr.dial(t, buildN), rr.dial(t, macN)
	admitted(t, b)
	admitted(t, m)
	refused(t, rr.dial(t, nasN), proto.RelayDenyNotListed)
	refused(t, rr.dial(t, newNode()), proto.RelayDenyLoggedOut)
	// Another certificate than the pinned one: no TLS, so no DERP.
	if dc, err := pinnedDERP(buildN.priv, rr.hostport(), "sha256:"+strings.Repeat("0", 64), t.Logf); err != nil {
		t.Fatal(err)
	} else if err := dc.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "pin") {
		t.Fatalf("a wrong pin: %v", err)
	}

	// It carries a packet, and counts it for the device that sent it.
	payload := bytes.Repeat([]byte("x"), 1000)
	if err := b.Send(macN.priv.Public(), payload); err != nil {
		t.Fatal(err)
	}
	for {
		msg, err := recv(m)
		if err != nil {
			t.Fatalf("the Mac waits for the packet: %v", err)
		}
		if p, ok := msg.(derp.ReceivedPacket); ok {
			if p.Source != buildN.priv.Public() || !bytes.Equal(p.Data, payload) {
				t.Fatalf("the Mac got %+v", p)
			}
			break
		}
	}
	if err := rr.r.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	u := mac.RelayUsage()
	var month *proto.RelayMonth
	for i := range u.Relays {
		if u.Relays[i].Relay == id {
			month = &u.Relays[i]
		}
	}
	if month == nil || month.Bytes < 1000 || len(month.LANs) != 1 || month.LANs[0].Bytes < 1000 || len(month.Devices) != 2 {
		t.Fatalf("what my relay carried: %+v", month)
	}
	// A second sync has nothing new to report: bytes go in once.
	if err := rr.r.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	amounts := func(u proto.RelayUsage) string {
		var b strings.Builder
		for _, m := range u.Relays {
			fmt.Fprint(&b, m.Relay, m.Bytes, m.LANs, m.Shares, m.Devices)
		}
		return b.String()
	}
	if again := mac.RelayUsage(); amounts(again) != amounts(u) {
		t.Fatalf("counted twice: %s, then %s", amounts(u), amounts(again))
	}

	// Taken out of 工作室: the next sync closes both.
	mac.MustOp(proto.OpRelayRemove, proto.OpArgs{LAN: studio, Relay: id})
	if err := rr.r.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, dc := range []*derphttp.Client{b, m} {
		for {
			if _, err := recv(dc); err != nil {
				break
			}
		}
	}
	if n := rr.r.openConns(); n != 0 {
		t.Fatalf("%d connections still open", n)
	}

	// Restarted, somewhere else: no token needed, the same pin, the new
	// address in the maps.
	rr.stop()
	mac.MustOp(proto.OpRelayAdd, proto.OpArgs{LAN: studio, Relay: id})
	pin := rr.pin
	rr = startRelay(t, ctl, dir, "", func(c *config) { c.ipv4 = netip.MustParseAddr("203.0.113.9") })
	if rl := relayOf(build.NetMap(), id); rl == nil || rl.IPv4.String() != "203.0.113.9" || rl.Pin != pin || rr.pin != pin {
		t.Fatalf("after the restart: %+v", rl)
	}
	admitted(t, rr.dial(t, buildN))
	if n := strings.Count(strings.Join(ctl.Logs(), "\n"), "registered from"); n != 1 {
		t.Fatalf("registered %d times", n)
	}

	// Deleted: the server no longer knows it, and it stops.
	mac.MustOp(proto.OpRelayDelete, proto.OpArgs{Relay: id})
	if err := rr.r.sync(context.Background()); !errors.Is(err, errRevoked) {
		t.Fatalf("sync after the delete: %v", err)
	}
}

// TestPublicRelay: ours, from the admin socket. Anyone logged in gets in,
// in a LAN or not; each connection is held to its rate limit, which
// follows the server's.
func TestPublicRelay(t *testing.T) {
	ctl := controltest.New(t)
	tok, err := control.AdminRelayAdd(ctl.Sock, "东京", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	rr := startRelay(t, ctl, t.TempDir(), tok.Token, nil)
	if rr.r.st.Source != proto.RelayPublic || rr.r.rate != 200_000 {
		t.Fatalf("a public relay: %+v, %d bytes/s", rr.r.st, rr.r.rate)
	}
	if !strings.Contains(rr.log(), "each connection limited to 200000 bytes/s") {
		t.Fatalf("the rate limit is not applied:\n%s", rr.log())
	}
	nasN := newNode()
	nas := ctl.Login(t, "jie@deneb.test", "nas", nasN.pub)
	admitted(t, rr.dial(t, nasN))
	nas.Logout()
	refused(t, rr.dial(t, nasN), proto.RelayDenyLoggedOut)

	if _, err := control.AdminRelayRate(ctl.Sock, rr.r.st.Relay, 0); err != nil {
		t.Fatal(err)
	}
	if err := rr.r.sync(context.Background()); err != nil || rr.r.rate != 0 || !strings.Contains(rr.log(), "no rate limit") {
		t.Fatalf("the limit lifted: %d bytes/s, %v", rr.r.rate, err)
	}
}

// TestFirstStartNeedsToken: without a registration token, a relay never
// registered says what to do; a used token does not work twice.
func TestFirstStartNeedsToken(t *testing.T) {
	ctl := controltest.New(t)
	cfg := config{control: ctl.URL, data: t.TempDir(), listen: "127.0.0.1:0", stun: "", syncEvery: time.Hour, region: regionOff}
	if err := run(context.Background(), cfg, t.Logf, nil); err == nil || !strings.Contains(err.Error(), "登记口令") {
		t.Fatalf("no token: %v", err)
	}
	n := newNode()
	mac := ctl.Login(t, "kittors@deneb.test", "mac", n.pub)
	out := mac.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "家里"})
	startRelay(t, ctl, t.TempDir(), out.RelayToken, nil)
	cfg.token = out.RelayToken
	if err := run(context.Background(), cfg, t.Logf, nil); err == nil || !strings.Contains(err.Error(), "用过") {
		t.Fatalf("a used token: %v", err)
	}
	if _, err := newControlClient("https://127.0.0.1:1", ""); err == nil {
		t.Fatal("an HTTPS control server without a pin")
	}
	if !slices.Contains([]string{"deneb-relay"}, strings.Fields(version())[0]) {
		t.Fatalf("version %q", version())
	}
}

// TestJoin is the one-line install: the join code from relay.join is all
// the relay needs. It finds the control server in it, registers, and is
// named after the place it says it is.
func TestJoin(t *testing.T) {
	ctl := controltest.New(t)
	mac := ctl.Login(t, "kittors@deneb.test", "mac", newNode().pub)
	j := mac.MustOp(proto.OpRelayJoin, proto.OpArgs{}).RelayJoin
	if j == nil || !strings.Contains(j.Command, "--join "+j.Code) {
		t.Fatalf("relay.join: %+v", j)
	}
	rr := startRelay(t, ctl, t.TempDir(), "", func(c *config) {
		c.control, c.region = "", "香港"
		if err := c.useJoin(j.Code); err != nil {
			t.Fatal(err)
		}
	})
	if rr.r.st.Name != "香港" || rr.r.st.Source != proto.RelayServer {
		t.Fatalf("registered as %+v", rr.r.st)
	}
	rs, err := control.AdminRelays(ctl.Sock)
	if err != nil || len(rs) != 2 || rs[1].ID != rr.r.st.Relay || rs[1].Owner != "kittors@deneb.test" || rs[1].Name != "香港" || rs[1].Pin != rr.pin || !rs[1].Online {
		t.Fatalf("the relays: %+v, %v", rs, err)
	}
	var c config
	if err := c.useJoin("deneb-join-nope"); err == nil {
		t.Fatal("a broken code")
	}
}

func TestDetectRegion(t *testing.T) {
	probed := ""
	probeClouds = func(context.Context) string { return probed }
	defer func() { probeClouds = cloudRegion }()
	ctx := context.Background()
	t.Setenv("TZ", "Asia/Hong_Kong")
	if got := detectRegion(ctx, " 香港 "); got != "name:香港" {
		t.Errorf("-region: %q", got)
	}
	if got := detectRegion(ctx, regionOff); got != "" {
		t.Errorf("-region off: %q", got)
	}
	probed = "aws:ap-east-1"
	if got := detectRegion(ctx, ""); got != "tz:Asia/Hong_Kong" {
		t.Errorf("not asked, the cloud says nothing: %q", got)
	}
	if got := detectRegion(ctx, regionAuto); got != "aws:ap-east-1" {
		t.Errorf("auto: the cloud before $TZ: %q", got)
	}
	probed = ""
	if got := detectRegion(ctx, regionAuto); got != "tz:Asia/Hong_Kong" {
		t.Errorf("auto, no cloud: %q", got)
	}
	t.Setenv("TZ", "UTC")
	if got := detectRegion(ctx, regionAuto); got != "" {
		t.Errorf("UTC says nothing: %q", got)
	}
}

// TestKickAtOnce: kicked in the client, a device is off the relay within
// a couple of seconds: the watch wakes the relay, which syncs and closes
// it; back, it is turned away as kicked. The sync carries the relay's
// load for the owner's card.
func TestKickAtOnce(t *testing.T) {
	ctl := controltest.New(t)
	buildN, macN := newNode(), newNode()
	build := ctl.Login(t, "kittors@deneb.test", "build", buildN.pub)
	mac := ctl.Login(t, "kittors@deneb.test", "mac", macN.pub)
	studio := mac.MustOp(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).LAN
	mac.MustOp(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: build.ID})
	out := mac.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "香港"})
	mac.MustOp(proto.OpRelayAdd, proto.OpArgs{LAN: studio, Relay: out.Relay})
	rr := startRelay(t, ctl, t.TempDir(), out.RelayToken, nil)
	b, m := rr.dial(t, buildN), rr.dial(t, macN)
	admitted(t, b)
	admitted(t, m)

	if err := rr.r.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var month *proto.RelayMonth
	for _, x := range mac.RelayUsage().Relays {
		if x.Relay == out.Relay {
			month = &x
		}
	}
	if l := month.Load; l == nil || l.Connections != 2 || l.Devices != 2 || l.People != 1 || l.Memory <= 0 || l.Uptime < 0 || len(month.Connected) != 2 {
		t.Fatalf("the load: %+v, connected %+v", month.Load, month.Connected)
	}

	start := time.Now()
	mac.MustOp(proto.OpRelayKick, proto.OpArgs{Relay: out.Relay, Device: build.ID})
	if _, err := recv(b); err == nil {
		t.Fatal("the kicked build server is still on")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("the kick took %s", took)
	}
	refused(t, rr.dial(t, buildN), proto.RelayDenyKicked)
	if n := rr.r.openConns(); n != 1 {
		t.Fatalf("%d connections open; the Mac's should be", n)
	}
}
