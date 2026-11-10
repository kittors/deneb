package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/stun"
)

// TestAccept is M4's check on the real machines (design/plan/m4-acceptance.md
// r3, r4, r5, r7) against the deployed control server and the two Deneb
// relays. It logs in three test devices (two of kittors's, one of leo's),
// makes a test LAN with kittors's relay in it, checks admission, the
// public relay's rate limit, the load and usage, weights and kicks, and
// cleans up: the LAN deleted, the devices logged out. It runs only with
// DENEB_ACCEPT set:
//
//	DENEB_ACCEPT=1 \
//	DENEB_ACCEPT_CONTROL=https://103.42.31.155:47443 DENEB_ACCEPT_CONTROL_PIN=sha256:… \
//	DENEB_ACCEPT_PUBLIC=r_…,103.42.31.155:47445,sha256:… DENEB_ACCEPT_OWN=r_…,198.51.100.4:47445,sha256:… \
//	DENEB_ACCEPT_ADMIN='ssh hk-155 docker exec deneb-control deneb-control' \
//	go test ./cmd/deneb-relay -run TestAccept -v -timeout 20m
func TestAccept(t *testing.T) {
	if os.Getenv("DENEB_ACCEPT") == "" {
		t.Skip("DENEB_ACCEPT not set")
	}
	ctl := newRemote(t, os.Getenv("DENEB_ACCEPT_CONTROL"), os.Getenv("DENEB_ACCEPT_CONTROL_PIN"))
	pub, own := relayArg(t, "DENEB_ACCEPT_PUBLIC"), relayArg(t, "DENEB_ACCEPT_OWN")
	admin := strings.Fields(os.Getenv("DENEB_ACCEPT_ADMIN"))

	t.Run("stun", func(t *testing.T) {
		for _, r := range []acceptRelay{pub, own} {
			h, _, _ := net.SplitHostPort(r.hostport)
			t.Logf("%s STUN %s:47478: %s", r.id, h, stunCheck(h+":47478"))
		}
	})

	a := ctl.login(t, admin, "kittors@deneb.test", "relay-accept-a")
	b := ctl.login(t, admin, "kittors@deneb.test", "relay-accept-b")
	l := ctl.login(t, admin, "leo@deneb.test", "relay-accept-l")
	var lan proto.LANID
	defer func() {
		if lan != "" {
			a.op(t, proto.OpLANDelete, proto.OpArgs{LAN: lan})
		}
		for _, d := range []*remoteDevice{a, b, l} {
			d.logout(t)
		}
		t.Log("cleaned up: the test LAN deleted, the three test devices logged out")
	}()
	lan = a.mustOp(t, proto.OpLANCreate, proto.OpArgs{Name: "中转站验收"}).Result.LAN
	b.mustOp(t, proto.OpLANAddDevice, proto.OpArgs{LAN: lan, Device: b.id})
	a.mustOp(t, proto.OpRelayAdd, proto.OpArgs{LAN: lan, Relay: own.id})
	v := a.view(t)
	for _, lv := range v.LANs {
		if lv.ID == lan {
			t.Logf("test LAN %s: added %v, list %v, %d devices", lan, lv.AddedRelays, lv.Relays, len(lv.Devices))
		}
	}

	t.Run("r3-admission", func(t *testing.T) {
		stranger := newNode()
		check := func(who string, n node, r acceptRelay, want string) {
			got := admission(t, n, r)
			ok := got == want
			t.Logf("%-28s → %-7s %-24s want %-24s %s", who, r.name, got, want, map[bool]string{true: "ok", false: "WRONG"}[ok])
			if !ok {
				t.Fail()
			}
		}
		check("leo's device, not in its LANs", l.node, own, "deneb-refused:"+proto.RelayDenyNotListed)
		check("a key no device has", stranger, own, "deneb-refused:"+proto.RelayDenyLoggedOut)
		check("a key no device has", stranger, pub, "deneb-refused:"+proto.RelayDenyLoggedOut)
		check("leo's device, in no LAN", l.node, pub, "admitted")
		check("kittors's device in the LAN", a.node, own, "admitted")
	})

	t.Run("r4-rate", func(t *testing.T) {
		adminRun(t, admin, "relay-rate", "-data", "/data", "-relay", string(pub.id), "-rate", "250KB/s")
		defer adminRun(t, admin, "relay-rate", "-data", "/data", "-relay", string(pub.id), "-rate", "2MB/s")
		time.Sleep(8 * time.Second) // the relay's watch syncs it in
		limited, limitedSent := throughput(t, a.node, b.node, pub, 10*time.Second)
		free, freeSent := throughput(t, a.node, b.node, own, 10*time.Second)
		t.Logf("through the public relay at 250 KB/s (2 Mbit/s): sent %.0f KB/s, arrived %.0f KB/s; through kittors's relay, no limit: sent %.0f KB/s, arrived %.0f KB/s",
			limitedSent/1e3, limited/1e3, freeSent/1e3, free/1e3)
		// What arrived is the measure: what the sender hands its socket
		// runs ahead of the relay by the kernel's buffers (and a local
		// proxy's, if one is in the way).
		if limited > 340e3 || limited < 150e3 || free < 2*limited {
			t.Errorf("limited sent %.0f arrived %.0f B/s, free %.0f B/s", limitedSent, limited, free)
		}
	})

	t.Run("r7-load-weight-kick", func(t *testing.T) {
		// Keep two of kittors's devices on his relay and wait for its
		// next sync: the card counts them.
		ca, cb := connect(t, a.node, own), connect(t, b.node, own)
		defer ca.Close()
		defer cb.Close()
		go func() {
			payload := make([]byte, 1000)
			for range 2000 {
				if ca.Send(b.node.priv.Public(), payload) != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		go func() {
			for {
				if _, err := cb.Recv(); err != nil {
					return
				}
			}
		}()
		var m *proto.RelayMonth
		deadline := time.Now().Add(80 * time.Second)
		for time.Now().Before(deadline) {
			m = a.month(t, own.id)
			if m != nil && m.Load != nil && m.Load.Devices >= 2 && time.Since(m.Load.At) < 30*time.Second {
				break
			}
			time.Sleep(5 * time.Second)
		}
		if m == nil || m.Load == nil || m.Load.Devices < 2 {
			t.Fatalf("the card's load: %+v", m)
		}
		t.Logf("kittors's relay card: %d devices, %d people, %d connections, %.0f KB/s, CPU %.0f%%, memory %d MB; this month %d bytes; connected %v",
			m.Load.Devices, m.Load.People, m.Load.Connections, float64(m.Load.BytesPerSec)/1e3, m.Load.CPU*100, m.Load.Memory>>20, m.Bytes, m.Connected)

		three, one := 3, 1
		a.mustOp(t, proto.OpRelaySetWeight, proto.OpArgs{Relay: own.id, Weight: &three})
		w3 := myRelayView(a.view(t), own.id).Weight
		a.mustOp(t, proto.OpRelaySetWeight, proto.OpArgs{Relay: own.id, Weight: &one})
		t.Logf("weight set to 3: the card says %d; back to %d", w3, myRelayView(a.view(t), own.id).Weight)
		if w3 != 3 {
			t.Errorf("weight %d", w3)
		}

		start := time.Now()
		k := a.mustOp(t, proto.OpRelayKick, proto.OpArgs{Relay: own.id, Device: b.id}).Result.Kick
		for {
			if _, err := cb.Recv(); err != nil {
				break
			}
		}
		closed := time.Since(start)
		again := admission(t, b.node, own)
		kicks := a.month(t, own.id).Kicks
		a.mustOp(t, proto.OpRelayUnkick, proto.OpArgs{Kick: k})
		back := admission(t, b.node, own)
		t.Logf("kicked B: its connection closed after %s; back, it hears %s; the card lists %+v; lifted, it is %s", closed.Round(10*time.Millisecond), again, kicks, back)
		if closed > 5*time.Second || again != "deneb-refused:"+proto.RelayDenyKicked || back != "admitted" || len(kicks) == 0 {
			t.Errorf("kick: closed after %s, %s, %s, %+v", closed, again, back, kicks)
		}
	})

	t.Run("r5-usage", func(t *testing.T) {
		m := a.month(t, own.id)
		t.Logf("kittors's relay this month: %d bytes; by LAN %+v; by device %+v", m.Bytes, m.LANs, m.Devices)
		if m.Bytes == 0 || len(m.Devices) == 0 {
			t.Errorf("no usage recorded: %+v", m)
		}
	})
}

// acceptRelay is a deployed relay: id, address, pin.
type acceptRelay struct {
	id       proto.RelayID
	name     string
	hostport string
	pin      string
}

func relayArg(t *testing.T, env string) acceptRelay {
	f := strings.Split(os.Getenv(env), ",")
	if len(f) != 3 {
		t.Fatalf("%s: want id,host:port,pin", env)
	}
	name := "public"
	if env == "DENEB_ACCEPT_OWN" {
		name = "own"
	}
	return acceptRelay{id: proto.RelayID(f[0]), name: name, hostport: f[1], pin: f[2]}
}

// connect is a DERP client on r, trusted by the pin, let in.
func connect(t *testing.T, n node, r acceptRelay) *derphttp.Client {
	t.Helper()
	dc, err := pinnedDERP(n.priv, r.hostport, r.pin, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dc.Connect(ctx); err != nil {
		t.Fatalf("connect to %s: %v", r.name, err)
	}
	if m, err := recv(dc); err != nil {
		t.Fatalf("%s: %v", r.name, err)
	} else if _, ok := m.(derp.ServerInfoMessage); !ok {
		t.Fatalf("%s turned the client away: %T %+v", r.name, m, m)
	}
	return dc
}

// admission is what r says to n: "admitted", or the reason it gave.
func admission(t *testing.T, n node, r acceptRelay) string {
	t.Helper()
	dc, err := pinnedDERP(n.priv, r.hostport, r.pin, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dc.Connect(ctx); err != nil {
		return "connect: " + err.Error()
	}
	m, err := recv(dc)
	switch mm := m.(type) {
	case derp.ServerInfoMessage:
		return "admitted"
	case derp.HealthMessage:
		return mm.Problem
	}
	return fmt.Sprintf("%T %v", m, err)
}

// throughput sends from one node to another through r as fast as the
// relay takes it for d: what arrived, and what the sender got out, in
// bytes a second.
func throughput(t *testing.T, from, to node, r acceptRelay, d time.Duration) (arrived, sent float64) {
	t.Helper()
	cf, ct := connect(t, from, r), connect(t, to, r)
	defer cf.Close()
	defer ct.Close()
	var got atomic.Int64
	go func() {
		for {
			m, err := ct.Recv()
			if err != nil {
				return
			}
			if p, ok := m.(derp.ReceivedPacket); ok {
				got.Add(int64(len(p.Data)))
			}
		}
	}()
	payload := make([]byte, 1200)
	rand.Read(payload)
	start := time.Now()
	var out int64
	for time.Since(start) < d {
		if err := cf.Send(to.priv.Public(), payload); err != nil {
			t.Fatalf("send through %s: %v", r.name, err)
		}
		out += int64(len(payload))
	}
	time.Sleep(time.Second)
	return float64(got.Load()) / d.Seconds(), float64(out) / d.Seconds()
}

// stunCheck asks a STUN server for this machine's address, as the engine
// does (Tailscale's STUN server answers only its own kind of request).
func stunCheck(addr string) string {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return err.Error()
	}
	defer c.Close()
	tx := stun.NewTxID()
	if _, err := c.Write(stun.Request(tx)); err != nil {
		return err.Error()
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil {
		return "no answer: " + err.Error()
	}
	got, ap, err := stun.ParseResponse(buf[:n])
	if err != nil || got != tx {
		return fmt.Sprintf("a bad answer: %v", err)
	}
	return "answers, sees this machine as " + ap.String()
}

func myRelayView(v *proto.UserView, id proto.RelayID) proto.RelayView {
	for _, r := range v.MyRelays {
		if r.ID == id {
			return r
		}
	}
	return proto.RelayView{}
}

// remote is the deployed control server's device API.
type remote struct {
	url string
	hc  *http.Client
}

func newRemote(t *testing.T, url, pin string) *remote {
	cfg, err := proto.PinnedTLSConfig(pin)
	if err != nil {
		t.Fatal(err)
	}
	return &remote{url: strings.TrimRight(url, "/"), hc: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg, Proxy: nil}}}
}

// remoteDevice is a test device logged in on it.
type remoteDevice struct {
	r     *remote
	mk    proto.MachinePrivate
	node  node
	token string
	id    proto.DeviceID
	n     int
}

func (r *remote) call(t *testing.T, token, method, path string, in, out any) *proto.Error {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.url+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var eb proto.ErrorBody
		if json.Unmarshal(b, &eb) == nil && eb.Error != nil {
			return eb.Error
		}
		t.Fatalf("%s %s: %s %s", method, path, resp.Status, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return nil
}

// login logs a new device in as email, approved through the admin socket.
func (r *remote) login(t *testing.T, admin []string, email, host string) *remoteDevice {
	t.Helper()
	d := &remoteDevice{r: r, mk: proto.NewMachineKey(), node: newNode()}
	var ch proto.AuthChallenge
	if err := r.call(t, "", "POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: d.mk.Public()}, &ch); err != nil {
		t.Fatal(err)
	}
	var tok proto.AuthToken
	if err := r.call(t, "", "POST", proto.PathAuthToken, proto.AuthTokenRequest{
		MachineKey: d.mk.Public(), Challenge: ch.Challenge, Signature: d.mk.SignChallenge(ch.Challenge),
	}, &tok); err != nil {
		t.Fatal(err)
	}
	d.token = tok.Token
	var disco proto.DiscoPublic
	rand.Read(disco[:])
	req := proto.LoginRequest{NodeKey: d.node.pub, DiscoKey: disco, Hostname: host, OS: proto.Linux, EngineVersion: "m4-accept"}
	var st proto.LoginState
	if err := r.call(t, d.token, "POST", proto.PathLogin, req, &st); err != nil {
		t.Fatal(err)
	}
	// The code alone: a link's "?" would meet the remote shell.
	code := st.LoginURL[strings.LastIndex(st.LoginURL, "c=")+2:]
	adminRun(t, admin, "approve", "-data", "/data", "-code", code, "-email", email)
	if err := r.call(t, d.token, "POST", proto.PathLogin, req, &st); err != nil || !st.LoggedIn {
		t.Fatalf("%s: %+v, %v", host, st, err)
	}
	d.id = st.Device
	t.Logf("test device %s (%s) logged in as %s", host, d.id, email)
	return d
}

func (d *remoteDevice) op(t *testing.T, op proto.OpType, a proto.OpArgs) (proto.OpResult, *proto.Error) {
	t.Helper()
	d.n++
	var res proto.OpResult
	err := d.r.call(t, d.token, "POST", proto.PathOps, proto.Op{ID: fmt.Sprintf("m4accept-%s-%d-%d", d.id, time.Now().UnixNano(), d.n), Op: op, Args: a}, &res)
	return res, err
}

func (d *remoteDevice) mustOp(t *testing.T, op proto.OpType, a proto.OpArgs) proto.OpResult {
	t.Helper()
	res, err := d.op(t, op, a)
	if err != nil {
		t.Fatalf("%s %+v: %v", op, a, err)
	}
	return res
}

func (d *remoteDevice) view(t *testing.T) *proto.UserView {
	t.Helper()
	var v proto.UserView
	if err := d.r.call(t, d.token, "GET", proto.PathUserView, nil, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func (d *remoteDevice) month(t *testing.T, id proto.RelayID) *proto.RelayMonth {
	t.Helper()
	var u proto.RelayUsage
	if err := d.r.call(t, d.token, "GET", proto.PathRelayUsage, nil, &u); err != nil {
		t.Fatal(err)
	}
	for i := range u.Relays {
		if u.Relays[i].Relay == id {
			return &u.Relays[i]
		}
	}
	return nil
}

func (d *remoteDevice) logout(t *testing.T) {
	t.Helper()
	d.r.call(t, d.token, "POST", proto.PathLogout, nil, nil)
}

// adminRun runs a deneb-control admin command on the control server's
// machine.
func adminRun(t *testing.T, admin []string, args ...string) string {
	t.Helper()
	if len(admin) == 0 {
		t.Fatal("DENEB_ACCEPT_ADMIN not set")
	}
	out, err := exec.Command(admin[0], append(admin[1:], args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %v: %v\n%s", admin, args[0], err, out)
	}
	return string(out)
}
