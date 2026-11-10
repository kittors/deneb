package engine

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/derp/derpserver"
	"tailscale.com/types/key"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/proto"
)

// These tests run three engines in one process, with an in-process relay
// and network maps made by hand in place of the control server. They cover
// the data path end to end: WireGuard, magicsock, the filter, the
// user-space stack and its forwarding to local services. The colima
// topology (test/topo) covers the same over real NATs and a real control
// server.

func quiet(string, ...any) {}

// logFor is quiet, unless DENEB_TEST_LOG is set: then the engine's log goes
// to stderr with the device's name, to see what an engine did in a test.
func logFor(name string) func(string, ...any) {
	if os.Getenv("DENEB_TEST_LOG") == "" {
		return quiet
	}
	return log.New(os.Stderr, name+" ", log.Lmicroseconds).Printf
}

func testRelay(t *testing.T) (proto.Relay, *x509.CertPool) {
	pool := x509.NewCertPool()
	return testRelayInto(t, "relay-local", "本地", pool), pool
}

// testRelayInto starts another in-process relay and trusts its certificate
// in pool.
func testRelayInto(t *testing.T, id proto.RelayID, name string, pool *x509.CertPool) proto.Relay {
	s := derpserver.New(key.NewNode(), quiet)
	mux := http.NewServeMux()
	mux.Handle("/derp", derpserver.Handler(s))
	ts := httptest.NewUnstartedServer(mux)
	ts.StartTLS()
	t.Cleanup(func() { ts.Close(); s.Close() })
	pool.AddCert(ts.Certificate())
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	// The test certificate is for example.com; the relay is dialed by
	// address, as in production, and TLS checks the name.
	return proto.Relay{ID: id, Name: name, HostName: "example.com", IPv4: netip.MustParseAddr("127.0.0.1"), DERPPort: uint16(port)}
}

type tnode struct {
	// old: an engine from before proto.CapHandshakeDedupe: it claims
	// nothing, and does not drop repeated handshake messages.
	old    bool
	e      *Engine
	id     proto.DeviceID
	name   string
	owner  string
	person proto.PersonID
	addr   netip.Addr
}

// caps is what the node's engine claims, as the server copies it into the
// map (proto.Node.Caps).
func (n *tnode) caps() []string {
	if n.old {
		return nil
	}
	return []string{proto.CapHandshakeDedupe}
}

// makeOld turns n into an engine from before proto.CapHandshakeDedupe.
func (n *tnode) makeOld() {
	n.old = true
	n.e.ms.SetHandshakeDedupe(false)
}

func (n *tnode) node() proto.Node {
	return proto.Node{
		ID: n.id, Name: n.name, DisplayName: n.name, Owner: n.person, OwnerName: n.owner,
		FQDN:     n.name + "." + n.owner + ".deneb.internal",
		NodeKey:  proto.NodePublic(n.e.nodePriv.Public().Raw32()),
		DiscoKey: proto.DiscoPublic(magicsock.DiscoPublic(n.e.discoPriv)),
		Addrs:    []netip.Prefix{netip.PrefixFrom(n.addr, 32)},
		Online:   true, HomeRelay: n.home(),
		Caps: n.caps(),
	}
}

// home is the relay the device reports as where it is reachable.
func (n *tnode) home() proto.RelayID {
	if h := n.e.ms.HomeRelay(); h != "" {
		return h
	}
	return "relay-local"
}

// world is a hand-made control server: who is in which scope, what each
// device opens to each scope, and who refuses whom.
type world struct {
	relay   proto.Relay
	relays  []proto.Relay // all relays, when there is more than one
	nodes   []*tnode
	members map[proto.ScopeID][]*tnode
	open    map[proto.ScopeID]map[*tnode][]proto.Service
	refuses map[[2]*tnode]bool // [owner's device, refused device]
	paused  map[*tnode]map[proto.ScopeID]bool
	// noInbound marks the receiving side of a share (M3): it only
	// connects out through that scope.
	noInbound map[proto.ScopeID]map[*tnode]bool
	// scopeRelays are a scope's relays, best first (M4); default: the
	// local relay. peerRelays, when set, has netmapFor fill Peer.Relays as
	// the server does: the peer's resident relays that are on the lists of
	// the scopes both share.
	scopeRelays map[proto.ScopeID][]proto.RelayID
	peerRelays  bool
	version     uint64
}

func (w *world) netmapFor(n *tnode) *proto.NetMap {
	nm := &proto.NetMap{
		Version: w.version,
		Network: proto.Network{IPv4: netip.MustParsePrefix("10.77.0.0/16"), DNS: []netip.Addr{netip.MustParseAddr("10.77.0.53")}, Domain: proto.Domain},
		Self:    n.node(),
		Relays:  []proto.Relay{w.relay},
	}
	if len(w.relays) > 0 {
		nm.Relays = w.relays
	}
	shared := map[*tnode][]proto.ScopeID{}
	for _, id := range []proto.ScopeID{"lan/studio", "lan/weekend", "share/s1"} {
		in := false
		for _, m := range w.members[id] {
			if m == n {
				in = true
			}
		}
		if !in {
			continue
		}
		kind := proto.ScopeLAN
		if _, ok := id.Share(); ok {
			kind = proto.ScopeShare
		}
		srel := w.scopeRelays[id]
		if srel == nil {
			srel = []proto.RelayID{"relay-local"}
		}
		nm.Scopes = append(nm.Scopes, proto.Scope{ID: id, Name: string(id), Open: w.open[id][n], Relays: srel,
			Kind: kind, NoInbound: w.noInbound[id][n]})
		for _, m := range w.members[id] {
			if m != n {
				shared[m] = append(shared[m], id)
			}
		}
	}
	for _, m := range w.nodes {
		// As the control server does (M2): the scopes a device paused move
		// to PausedScopes, and a device that paused all it shares with n
		// stays, with empty Scopes, so its resume needs no new handshake.
		var paused []proto.ScopeID
		shared[m] = slices.DeleteFunc(shared[m], func(id proto.ScopeID) bool {
			if w.paused[m][id] {
				paused = append(paused, id)
				return true
			}
			return false
		})
		if len(shared[m]) == 0 && len(paused) == 0 {
			continue
		}
		peer := proto.Peer{Node: m.node(), Scopes: append([]proto.ScopeID{}, shared[m]...), PausedScopes: paused,
			RefusedByMe: w.refuses[[2]*tnode{n, m}], RefusedByPeer: w.refuses[[2]*tnode{m, n}]}
		if w.peerRelays {
			// As the server does (M4): the peer's resident relays on the
			// lists of the scopes both share; HomeRelay the first of them.
			onLists := map[proto.RelayID]bool{}
			for _, id := range shared[m] {
				srel := w.scopeRelays[id]
				if srel == nil {
					srel = []proto.RelayID{"relay-local"}
				}
				for _, r := range srel {
					onLists[r] = true
				}
			}
			peer.Relays = nil
			for _, r := range m.e.ms.ResidentRelays() {
				if onLists[r] {
					peer.Relays = append(peer.Relays, r)
				}
			}
			peer.HomeRelay = ""
			if len(peer.Relays) > 0 {
				peer.HomeRelay = peer.Relays[0]
			}
		}
		nm.Peers = append(nm.Peers, peer)
	}
	return nm
}

func (w *world) push() {
	w.version++
	for _, n := range w.nodes {
		n.e.applyFrame(controlclient.Frame{Version: w.version, NetMap: w.netmapFor(n)})
	}
}

func newWorld(t *testing.T, mods ...func(*Options)) (*world, *tnode, *tnode, *tnode) {
	if testing.Short() {
		t.Skip("runs three engines for about half a minute")
	}
	relay, pool := testRelay(t)
	w := &world{relay: relay, members: map[proto.ScopeID][]*tnode{}, open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name, owner string) *tnode {
		o := Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool, Logf: logFor(name), Version: "test"}
		for _, m := range mods {
			m(&o)
		}
		e, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: owner, person: proto.PersonID("p-" + owner),
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac", "kittors")
	build := mk(2, "build", "kittors")
	nas := mk(3, "nas", "jie")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.members["lan/weekend"] = []*tnode{mac, nas}
	w.open["lan/studio"] = map[*tnode][]proto.Service{}
	w.open["lan/weekend"] = map[*tnode][]proto.Service{}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	return w, mac, build, nas
}

func pingOK(t *testing.T, from *tnode, to string, within time.Duration) apitype.PingResult {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		sum, err := from.e.Ping(context.Background(), to, 1, 0, time.Second, nil)
		if err == nil && sum.Received == 1 {
			return sum.Results[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s cannot ping %s: %v %+v", from.name, to, err, sum)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func pingFails(t *testing.T, from *tnode, to string) error {
	t.Helper()
	sum, err := from.e.Ping(context.Background(), to, 2, 200*time.Millisecond, 700*time.Millisecond, nil)
	if err == nil && sum.Received > 0 {
		t.Fatalf("%s should not reach %s, got %+v", from.name, to, sum)
	}
	return err
}

func waitPath(t *testing.T, n *tnode, peer *tnode, kind string, within time.Duration) {
	t.Helper()
	if os.Getenv("CI") != "" {
		// A loaded CI runner (GitHub's macOS ones) runs the timers late:
		// wait twice as long there before calling it a failure.
		within *= 2
	}
	k := peer.node().NodeKey
	deadline := time.Now().Add(within)
	for {
		if st, ok := n.e.ms.Status(k); ok && st.Kind == kind {
			return
		}
		if time.Now().After(deadline) {
			st, _ := n.e.ms.Status(k)
			t.Fatalf("%s -> %s: path %q, want %q", n.name, peer.name, st.Kind, kind)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestTwoScopesIsolatedAndPaths(t *testing.T) {
	w, mac, build, nas := newWorld(t)

	// The Mac is in both scopes and reaches both peers; the first answer
	// comes through the relay at once, then the path goes direct.
	start := time.Now()
	pingOK(t, mac, "build.kittors", 5*time.Second)
	t.Logf("first answer from build after %s", time.Since(start).Round(time.Millisecond))
	pingOK(t, mac, "nas", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)
	waitPath(t, mac, nas, "direct", 5*time.Second)
	t.Logf("direct to both after %s", time.Since(start).Round(time.Millisecond))

	// build and nas share nothing: neither even has the other's name.
	var ae *apitype.Error
	if err := pingFails(t, build, "nas.jie"); !errors.As(err, &ae) || ae.Code != apitype.ErrNotFound {
		t.Fatalf("build -> nas: want not_found, got %v", err)
	}
	if err := pingFails(t, nas, "build.kittors"); !errors.As(err, &ae) || ae.Code != apitype.ErrNotFound {
		t.Fatalf("nas -> build: want not_found, got %v", err)
	}

	// Pausing weekend on the Mac stops nas at once and leaves build alone.
	if err := mac.e.Pause(context.Background(), apitype.PauseRequest{Scope: "lan/weekend", Paused: true}); err != nil {
		t.Fatal(err)
	}
	pingFails(t, mac, "nas.jie")
	pingOK(t, mac, "build.kittors", time.Second)
	if p := pathOf(mac, nas); p.Kind != apitype.PathPaused {
		t.Fatalf("paused peer path: %+v", p)
	}
	mac.e.Pause(context.Background(), apitype.PauseRequest{Scope: "lan/weekend", Paused: false})
	pingOK(t, mac, "nas.jie", 5*time.Second)

	// Taking nas out of weekend cuts it on the next map; putting it back
	// (the undo) brings it back.
	w.members["lan/weekend"] = []*tnode{mac}
	w.push()
	pingFails(t, mac, "nas.jie")
	w.members["lan/weekend"] = []*tnode{mac, nas}
	w.push()
	pingOK(t, mac, "nas.jie", 5*time.Second)
}

func pathOf(n, peer *tnode) apitype.PeerPath {
	for _, p := range n.e.State().Paths {
		if p.DeviceID == apitype.DeviceID(peer.id) {
			return p
		}
	}
	return apitype.PeerPath{}
}

func TestServicesPerScopeAndRefusal(t *testing.T) {
	w, mac, build, nas := newWorld(t)

	// A "website" on this machine's loopback: every engine here forwards
	// to it, so whether a peer gets an answer is the filter's decision.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				fmt.Fprintf(c, "website\n")
			}()
		}
	}()
	web := proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{proto.Port(proto.TCP, port)}}
	w.open["lan/studio"][mac] = []proto.Service{web} // the Mac opens it to studio only
	w.push()
	pingOK(t, build, "mac", 5*time.Second)
	pingOK(t, nas, "mac", 5*time.Second)

	if got := fetch(t, build, mac, port); got != "website" {
		t.Fatalf("studio member should get the website, got %q", got)
	}
	if got := fetch(t, nas, mac, port); got != "" {
		t.Fatalf("weekend member got a service opened only to studio: %q", got)
	}
	w.open["lan/weekend"][mac] = []proto.Service{web}
	w.push()
	if got := fetch(t, nas, mac, port); got != "website" {
		t.Fatalf("after opening it to weekend too: %q", got)
	}

	// The Mac's owner refuses nas's owner on the Mac: nas cannot even
	// ping it, and says so at once; the Mac can still reach nas.
	w.refuses[[2]*tnode{mac, nas}] = true
	w.push()
	var ae *apitype.Error
	if err := pingFails(t, nas, "mac"); !errors.As(err, &ae) || ae.Code != apitype.ErrRefused {
		t.Fatalf("nas -> mac after refusal: want refused, got %v", err)
	}
	if got := fetch(t, nas, mac, port); got != "" {
		t.Fatalf("refused peer still uses the website: %q", got)
	}
	pingOK(t, mac, "nas", 3*time.Second)
	if p := pathOf(nas, mac); p.Kind != apitype.PathRefused {
		t.Fatalf("refused path on nas: %+v", p)
	}
	delete(w.refuses, [2]*tnode{mac, nas})
	w.push()
	pingOK(t, nas, "mac", 5*time.Second)
}

// fetch reads one line from a peer's port through the overlay, or "" if the
// connection fails (as a filtered SYN does).
func fetch(t *testing.T, from, to *tnode, port uint16) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	c, err := from.e.dialPeer(ctx, netip.AddrPortFrom(to.addr, port))
	if err != nil {
		return ""
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line)
}

// TestDirectLostFallsBackToRelay is acceptance item 10 in miniature: drop
// every UDP packet between two devices inside the engine. Traffic moves to
// the relay within the trust time, a long connection survives, and the path
// returns to direct once UDP flows again.
func TestDirectLostFallsBackToRelay(t *testing.T) {
	_, mac, build, _ := newWorld(t)
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 5*time.Second)

	var mu sync.Mutex
	var fails int
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			sum, err := mac.e.Ping(context.Background(), "build", 1, 0, 400*time.Millisecond, nil)
			if err != nil || sum.Received == 0 {
				mu.Lock()
				fails++
				mu.Unlock()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	t0 := time.Now()
	mac.e.ms.SetBlocked(build.node().NodeKey, true)
	// Packets go through the relay too from Trust (2.5 s) on; the path is
	// shown as the relay once Hold (6.5 s) passes without an answer.
	waitPath(t, mac, build, "relay", 9*time.Second)
	t.Logf("on the relay %s after UDP was blocked", time.Since(t0).Round(time.Millisecond))
	time.Sleep(2 * time.Second)
	mac.e.ms.SetBlocked(build.node().NodeKey, false)
	t1 := time.Now()
	waitPath(t, mac, build, "direct", 6*time.Second)
	t.Logf("direct again %s after UDP came back", time.Since(t1).Round(time.Millisecond))
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	// With a 2.5 s trust time about 2.5 s of pings are lost (§6.2); allow
	// for scheduling on a busy machine.
	if fails > 10 {
		t.Fatalf("%d of the pings failed around the switch", fails)
	}
	t.Logf("%d pings lost around the switch", fails)
}
