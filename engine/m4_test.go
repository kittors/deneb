package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// TestScopeRelayFallsBack: a scope's list starts with a relay this device
// cannot use (here nothing answers on its port; an own relay that turns the
// device away looks the same, or says so first). The engine keeps the
// next one on the list for the scope, and says which in the local API: the
// client shows "改走公共中转站".
func TestScopeRelayFallsBack(t *testing.T) {
	relay, pool := testRelay(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing answers there now
	dead := proto.Relay{ID: "relay-dead", Name: "自带", HostName: "example.com", IPv4: netip.MustParseAddr("127.0.0.1"), DERPPort: uint16(port)}
	w := &world{relay: relay, relays: []proto.Relay{dead, relay}, members: map[proto.ScopeID][]*tnode{},
		open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{},
		scopeRelays: map[proto.ScopeID][]proto.RelayID{"lan/studio": {"relay-dead", "relay-local"}}}
	mk := func(i int, name string) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool, Logf: logFor(name), Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: "kittors", person: "p-kittors", addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac, build := mk(1, "mac"), mk(2, "build")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ch := mac.e.ms.ChosenRelays()
		if ch["lan/studio"] == "relay-local" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("studio's relay is %q, want relay-local after relay-dead failed (failures: %v)", ch["lan/studio"], mac.e.ms.RelayFailures())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if why := mac.e.ms.RelayFailures()["relay-dead"]; why == "" {
		t.Error("relay-dead is not on record as failed")
	}
	var sc *apitype.Scope
	st := mac.e.State()
	for i := range st.Scopes {
		if st.Scopes[i].ID == "lan/studio" {
			sc = &st.Scopes[i]
		}
	}
	if sc == nil || sc.Relay != "relay-local" {
		t.Fatalf("studio in the local API: %+v, want relay relay-local", sc)
	}
	if got := mac.e.ms.ResidentRelays(); len(got) != 1 || got[0] != "relay-local" {
		t.Fatalf("resident relays %v, want [relay-local]", got)
	}
	pingOK(t, mac, "build", 5*time.Second)
}

// TestDeviceOnTwoOwnRelays is M4's reason for more than one home relay:
// the Mac is in studio (whose relay is relay-b, say someone's own server)
// and weekend (relay-local). An own relay lets in only the devices of its
// scopes, so the Mac keeps both: build reaches it through relay-b, nas
// through relay-local, each as the server lists it in Peer.Relays. UDP is
// blocked everywhere, so everything goes through the relays.
func TestDeviceOnTwoOwnRelays(t *testing.T) {
	relayA, pool := testRelay(t)
	relayB := testRelayInto(t, "relay-b", "乙", pool)
	w := &world{relay: relayA, relays: []proto.Relay{relayA, relayB}, members: map[proto.ScopeID][]*tnode{},
		open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{},
		scopeRelays: map[proto.ScopeID][]proto.RelayID{"lan/studio": {"relay-b"}, "lan/weekend": {"relay-local"}},
		peerRelays:  true}
	mk := func(i int, name, owner string) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool, Logf: logFor(name), Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: owner, person: proto.PersonID("p-" + owner), addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac, build, nas := mk(1, "mac", "kittors"), mk(2, "build", "kittors"), mk(3, "nas", "jie")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.members["lan/weekend"] = []*tnode{mac, nas}
	w.open["lan/studio"] = map[*tnode][]proto.Service{}
	w.open["lan/weekend"] = map[*tnode][]proto.Service{}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	w.push() // with each device's resident relays as the server would have them
	if got := mac.e.ms.ResidentRelays(); len(got) != 2 {
		t.Fatalf("the Mac keeps %v, want relay-b and relay-local", got)
	}
	for _, pr := range [][2]*tnode{{mac, build}, {build, mac}, {mac, nas}, {nas, mac}} {
		pr[0].e.ms.SetBlocked(pr[1].node().NodeKey, true)
	}
	pingOK(t, build, "mac", 10*time.Second)
	pingOK(t, nas, "mac", 10*time.Second)
	pingOK(t, mac, "build", 10*time.Second)
	pingOK(t, mac, "nas", 10*time.Second)
	waitPath(t, build, mac, "relay", 9*time.Second)
	waitPath(t, nas, mac, "relay", 9*time.Second)
	if r := pathOf(build, mac).Relay; r != "relay-b" {
		t.Errorf("build reaches the Mac through %s, want relay-b (studio's)", r)
	}
	if r := pathOf(nas, mac).Relay; r != "relay-local" {
		t.Errorf("nas reaches the Mac through %s, want relay-local (weekend's)", r)
	}
}

// testPinnedRelay starts an in-process relay whose certificate no root
// vouches for, and gives it the pin of its key, as a Deneb relay's
// self-signed one (M4). wrongPin gives it another key's pin instead.
func testPinnedRelay(t *testing.T, id proto.RelayID, wrongPin bool) proto.Relay {
	t.Helper()
	pool := x509.NewCertPool() // not shared with the engines: only the pin vouches
	r := testRelayInto(t, id, string(id), pool)
	// Its key's pin, read off the relay by dialing it once.
	conn, err := tls.Dial("tcp", net.JoinHostPort(r.IPv4.String(), fmt.Sprint(r.DERPPort)), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	pin := proto.Fingerprint(conn.ConnectionState().PeerCertificates[0])
	conn.Close()
	if wrongPin {
		pin = "sha256:" + strings.Repeat("ab", 32)
	}
	r.Pin = pin
	return r
}

// TestPinnedRelay: a relay trusted by its pin, not by a root (M4: Deneb
// relays' certificates are self-signed). The engine connects to it; with a
// pin that is not the relay's key it does not, and its scope falls back to
// the next relay on the list.
func TestPinnedRelay(t *testing.T) {
	relay, pool := testRelay(t)
	good := testPinnedRelay(t, "relay-pinned", false)
	bad := testPinnedRelay(t, "relay-badpin", true)
	w := &world{relay: relay, relays: []proto.Relay{good, bad, relay}, members: map[proto.ScopeID][]*tnode{},
		open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{},
		scopeRelays: map[proto.ScopeID][]proto.RelayID{"lan/studio": {"relay-pinned"}, "lan/weekend": {"relay-badpin", "relay-local"}},
		peerRelays:  true}
	mk := func(i int, name string) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool, Logf: logFor(name), Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: "kittors", person: "p-kittors", addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac, build := mk(1, "mac"), mk(2, "build")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.members["lan/weekend"] = []*tnode{mac}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		up := map[proto.RelayID]bool{}
		for _, r := range mac.e.ms.Relays() {
			up[r.ID] = r.Up
		}
		ch := mac.e.ms.ChosenRelays()
		if up["relay-pinned"] && ch["lan/weekend"] == "relay-local" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relays %+v, chosen %v, failures %v", mac.e.ms.Relays(), ch, mac.e.ms.RelayFailures())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if why := mac.e.ms.RelayFailures()["relay-badpin"]; !strings.Contains(why, "pinned TLS") {
		t.Errorf("relay-badpin failed for %q, want the pin", why)
	}
	w.push() // the peers' relays as the server has them now
	// Through the pinned relay: UDP blocked between the two.
	mac.e.ms.SetBlocked(build.node().NodeKey, true)
	build.e.ms.SetBlocked(mac.node().NodeKey, true)
	pingOK(t, mac, "build", 10*time.Second)
}

// toggleRelay is a relay that can be stopped and started again on the same
// port: a TCP forwarder in front of an in-process relay. stop cuts every
// connection through it; new ones are then taken but never answered (TLS
// waits until the dial gives up, as with r2's stopped relay behind its
// host's port), until start.
func toggleRelay(t *testing.T, id proto.RelayID, name string, pool *x509.CertPool) (r proto.Relay, stop, start func()) {
	real := testRelayInto(t, id, name, pool)
	to := net.JoinHostPort("127.0.0.1", fmt.Sprint(real.DERPPort))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	paused := false
	serve := func(ln net.Listener) {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			p := paused
			if p {
				conns = append(conns, c) // held, silent
			}
			mu.Unlock()
			if p {
				continue
			}
			u, err := net.Dial("tcp4", to)
			if err != nil {
				c.Close()
				continue
			}
			mu.Lock()
			conns = append(conns, c, u)
			mu.Unlock()
			go func() { io.Copy(u, c); u.Close() }()
			go func() { io.Copy(c, u); c.Close() }()
		}
	}
	go serve(ln)
	cut := func() {
		for _, c := range conns {
			c.Close()
		}
		conns = nil
	}
	stop = func() {
		mu.Lock()
		defer mu.Unlock()
		paused = true
		cut()
	}
	start = func() {
		mu.Lock()
		defer mu.Unlock()
		paused = false
		cut() // the silent ones: their dials start over
	}
	t.Cleanup(func() { ln.Close(); stop() })
	r = real
	r.DERPPort = uint16(ln.Addr().(*net.TCPAddr).Port)
	return r, stop, start
}

// r2 (11-09 21:21): "my server" stopped; the line went to the public relay
// 4.5 s later, then for 3 s back to the stopped one when its backoff ran
// out, losing pings. A relay that dropped is taken back only once it
// answers: tried on the side meanwhile.
func TestStoppedRelayIsTakenBackOnlyOnceItAnswers(t *testing.T) {
	pool := x509.NewCertPool()
	public := testRelayInto(t, "relay-public", "公共", pool)
	own, stop, start := toggleRelay(t, "relay-own", "我的服务器", pool)
	w := &world{relay: public, relays: []proto.Relay{own, public}, members: map[proto.ScopeID][]*tnode{},
		open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{},
		scopeRelays: map[proto.ScopeID][]proto.RelayID{"lan/studio": {"relay-own", "relay-public"}}}
	mk := func(i int, name string) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool,
			Logf: logFor(name), Version: "test", RelayRetry: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: "kittors", person: "p-kittors", addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac, build := mk(1, "mac"), mk(2, "build")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	chosen := func(n *tnode) proto.RelayID { return n.e.ms.ChosenRelays()["lan/studio"] }
	waitChosen := func(n *tnode, want proto.RelayID, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for chosen(n) != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: studio's relay %q, want %q (failures %v)", n.name, chosen(n), want, n.e.ms.RelayFailures())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitChosen(mac, "relay-own", 10*time.Second)
	waitChosen(build, "relay-own", 10*time.Second)
	// Everything through the relays.
	mac.e.ms.SetBlocked(build.node().NodeKey, true)
	build.e.ms.SetBlocked(mac.node().NodeKey, true)
	pingOK(t, mac, "build", 10*time.Second)

	stop()
	t0 := time.Now()
	waitChosen(mac, "relay-public", 10*time.Second)
	waitChosen(build, "relay-public", 10*time.Second)
	moved := time.Since(t0)
	w.push() // the server's next map: each now lists relay-public first (as their reports say)
	// Down for 12 s: several backoffs run out, and each try fails. The
	// lists stay on the public relay, and pings keep getting through.
	fails, n := 0, 0
	for time.Since(t0) < 12*time.Second {
		for _, x := range []*tnode{mac, build} {
			if c := chosen(x); c != "relay-public" {
				t.Fatalf("%s: back on %q %s after the stop, while it is still down", x.name, c, time.Since(t0).Round(100*time.Millisecond))
			}
		}
		sum, err := mac.e.Ping(context.Background(), "build", 1, 0, time.Second, nil)
		n++
		if err != nil || sum.Received == 0 {
			fails++
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("on the public relay %s after the stop; %d of %d pings lost while the own relay was down", moved.Round(100*time.Millisecond), fails, n)
	if fails > 2 {
		t.Fatalf("%d of %d pings lost on the public relay", fails, n)
	}
	start()
	t1 := time.Now()
	waitChosen(mac, "relay-own", 20*time.Second)
	waitChosen(build, "relay-own", 20*time.Second)
	t.Logf("back on the own relay %s after it started again", time.Since(t1).Round(100*time.Millisecond))
	pingOK(t, mac, "build", 5*time.Second)
}
