package magicsock

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kittors/deneb/cmd/deneb-relay/tunnel"
	"github.com/kittors/deneb/proto"
	"tailscale.com/derp"
	"tailscale.com/derp/derpserver"
	"tailscale.com/types/key"
)

// fakeCloudflare stands for Cloudflare and its tunnel in front of s: a TLS
// server on this machine, its certificate for example.com (pool trusts
// it), that takes nothing but a WebSocket at /derp with the derp
// subprotocol, and hands it on as cloudflared does: a WebSocket of its own
// to the relay's tunnel port on this machine, the relay's real handler
// (cmd/deneb-relay/tunnel), with CF-Connecting-IP. dial reaches it
// whatever is asked (the tunnel's name on 443).
func fakeCloudflare(t *testing.T, s *derpserver.Server) (dial func(ctx context.Context, network, addr string) (net.Conn, error), pool *x509.CertPool) {
	t.Helper()
	relay := httptest.NewServer(tunnel.Handler(tunnel.ServerAccept(s), func(string, ...any) {}))
	t.Cleanup(relay.Close)
	origin := "ws://" + relay.Listener.Addr().String() + tunnel.Path
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tunnel.Path || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "a WebSocket only", http.StatusForbidden)
			return
		}
		in, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{tunnel.Subprotocol}})
		if err != nil {
			return
		}
		defer in.CloseNow()
		out, _, err := websocket.Dial(r.Context(), origin, &websocket.DialOptions{Subprotocols: []string{tunnel.Subprotocol},
			HTTPHeader: http.Header{"CF-Connecting-IP": {"203.0.113.7"}}})
		if err != nil {
			in.Close(websocket.StatusTryAgainLater, "the origin does not answer")
			return
		}
		defer out.CloseNow()
		in.SetReadLimit(1 << 20)
		out.SetReadLimit(1 << 20)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		a, b := websocket.NetConn(ctx, in, websocket.MessageBinary), websocket.NetConn(ctx, out, websocket.MessageBinary)
		done := make(chan struct{}, 2)
		go func() { io.Copy(a, b); done <- struct{}{} }()
		go func() { io.Copy(b, a); done <- struct{}{} }()
		<-done
	}))
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	pool = x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	addr := ts.Listener.Addr().String()
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}, pool
}

// pinnedRelay is s by its own address: TLS with a self-signed certificate,
// trusted by its pin, as a public relay is.
func pinnedRelay(t *testing.T, s *derpserver.Server) (port int, pin string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(derpserver.Handler(s))
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts.Listener.Addr().(*net.TCPAddr).Port, proto.Fingerprint(ts.Certificate())
}

// tunnelRelay is a public relay at port with pin, and a tunnel.
func tunnelRelay(port int, pin string) proto.Relay {
	return proto.Relay{ID: "hk", Name: "香港", HostName: "example.com", IPv4: netip.MustParseAddr("127.0.0.1"),
		DERPPort: uint16(port), Pin: pin, Tunnel: "example.com"}
}

// tunnelConn is a Conn whose only relay is r, its tunnel reached by dial.
func tunnelConn(t *testing.T, r proto.Relay, dial func(ctx context.Context, network, addr string) (net.Conn, error), pool *x509.CertPool) *Conn {
	t.Helper()
	c, err := New(key.NewNode(), [32]byte{7}, Options{Logf: func(string, ...any) {}, RelayRoots: pool})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.tunnelDial = dial
	c.SetRelays([]proto.Relay{r}, []RelayList{{Scope: "lan/a", Relays: []proto.RelayID{r.ID}}}, "")
	return c
}

// relayBy waits for c's relay id to be up, by way via.
func relayBy(t *testing.T, c *Conn, id proto.RelayID, via string) {
	t.Helper()
	waitFor(t, "relay "+string(id)+" up by "+via, func() bool {
		for _, r := range c.Relays() {
			if r.ID == id {
				return r.Up && r.Via == via
			}
		}
		return false
	})
}

// TestRelayTunnelWhenDirectDoesNotConnect: nothing at the relay's own
// address: its connection goes by the tunnel, and comes up there (§10.4).
func TestRelayTunnelWhenDirectDoesNotConnect(t *testing.T) {
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	_, pin := pinnedRelay(t, s)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	c := tunnelConn(t, tunnelRelay(dead, pin), dial, pool)
	relayBy(t, c, "hk", "tunnel")
}

// TestRelayTunnelWhenTLSStalls: TCP through at the relay's own address and
// the TLS handshake never done (12:10): by the tunnel once relayTLSTimeout
// is up.
func TestRelayTunnelWhenTLSStalls(t *testing.T) {
	old := relayTLSTimeout
	relayTLSTimeout = 300 * time.Millisecond
	t.Cleanup(func() { relayTLSTimeout = old })
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	_, pin := pinnedRelay(t, s)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	start := time.Now()
	c := tunnelConn(t, tunnelRelay(ln.Addr().(*net.TCPAddr).Port, pin), dial, pool)
	relayBy(t, c, "hk", "tunnel")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
}

// TestRelayTunnelAndBack: the own address turned off (the test switch,
// deneb debug relay-direct) moves the connection to the tunnel, where a new
// connection then starts; turned on again, it comes back to the own address.
func TestRelayTunnelAndBack(t *testing.T) {
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	port, pin := pinnedRelay(t, s)
	c := tunnelConn(t, tunnelRelay(port, pin), dial, pool)
	relayBy(t, c, "hk", "direct")

	if err := c.SetRelayDirectOff("hk", true); err != nil {
		t.Fatal(err)
	}
	relayBy(t, c, "hk", "tunnel")
	remembered := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, ok := c.viaTunnel["hk"]
		return ok
	}
	waitFor(t, "the tunnel remembered for the next connection", remembered)

	if err := c.SetRelayDirectOff("hk", false); err != nil {
		t.Fatal(err)
	}
	relayBy(t, c, "hk", "direct")
	waitFor(t, "the tunnel forgotten once the own address is back", func() bool { return !remembered() })
	if err := c.SetRelayDirectOff("nosuch", true); err == nil {
		t.Error("a relay with no tunnel turned off")
	}
}

// TestRelayNoNetworkStaysDirect: neither the own address nor the tunnel
// gets through, as with no network at all (the lid closed, the Wi-Fi
// changing): the relay is not moved to the tunnel, nor remembered there;
// when the network is back, it comes up by its own address (§10.4 甲).
func TestRelayNoNetworkStaysDirect(t *testing.T) {
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	port, pin := pinnedRelay(t, s)
	var down atomic.Bool
	down.Store(true)
	c, err := New(key.NewNode(), [32]byte{7}, Options{Logf: func(string, ...any) {}, RelayRoots: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.tunnelDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if down.Load() {
			return nil, errors.New("no network")
		}
		return dial(ctx, network, addr)
	}
	c.mu.Lock()
	c.directOff["hk"] = true // the own address does not get through either
	c.mu.Unlock()
	r := tunnelRelay(port, pin)
	c.SetRelays([]proto.Relay{r}, []RelayList{{Scope: "lan/a", Relays: []proto.RelayID{r.ID}}}, "")
	waitFor(t, "the relay on record as failed", func() bool {
		for _, f := range c.FailedRelays() {
			if f.Relay == "hk" {
				return true
			}
		}
		return false
	})
	c.mu.Lock()
	remembered := len(c.viaTunnel)
	c.mu.Unlock()
	for _, rs := range c.Relays() {
		if rs.Via == "tunnel" {
			t.Error("moved to the tunnel with no network")
		}
	}
	if remembered != 0 {
		t.Error("the tunnel remembered with no network")
	}

	// The network back: the own address first.
	down.Store(false)
	c.mu.Lock()
	delete(c.directOff, "hk")
	c.relayFail["hk"].until = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.rechooseRelays()
	relayBy(t, c, "hk", "direct")
}

// TestRelayTunnelTriesDirectOnSchedule: the own address blocked, the relay
// goes by the tunnel; unblocked, with nothing said, it is back on the own
// address once tunnelProbeEvery (shortened here) is up.
func TestRelayTunnelTriesDirectOnSchedule(t *testing.T) {
	old := tunnelProbeEvery
	tunnelProbeEvery = 300 * time.Millisecond
	t.Cleanup(func() { tunnelProbeEvery = old })
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	port, pin := pinnedRelay(t, s)
	c := tunnelConn(t, tunnelRelay(port, pin), dial, pool)
	relayBy(t, c, "hk", "direct")
	if err := c.SetRelayDirectOff("hk", true); err != nil {
		t.Fatal(err)
	}
	relayBy(t, c, "hk", "tunnel")
	c.mu.Lock()
	delete(c.directOff, "hk") // unblocked, nothing woken
	c.mu.Unlock()
	relayBy(t, c, "hk", "direct")
}

// TestRelayBackOnNetworkChange: on the tunnel with the own address
// blocked, the network changes (NetworkChanged, from the engine's
// netChanged) and the address gets through there: back to it at once,
// not after tunnelProbeEvery.
func TestRelayBackOnNetworkChange(t *testing.T) {
	old := tunnelProbeEvery
	tunnelProbeEvery = time.Hour
	t.Cleanup(func() { tunnelProbeEvery = old })
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	port, pin := pinnedRelay(t, s)
	c := tunnelConn(t, tunnelRelay(port, pin), dial, pool)
	if err := c.SetRelayDirectOff("hk", true); err != nil {
		t.Fatal(err)
	}
	relayBy(t, c, "hk", "tunnel")
	c.mu.Lock()
	delete(c.directOff, "hk")
	c.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	for _, rs := range c.Relays() {
		if rs.Via != "tunnel" {
			t.Fatalf("back to %q before the network changed", rs.Via)
		}
	}
	c.NetworkChanged()
	relayBy(t, c, "hk", "direct")
}

// TestTunnelCarriesPackets: two devices through the tunnel, each to the
// other.
func TestTunnelCarriesPackets(t *testing.T) {
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	dial, pool := fakeCloudflare(t, s)
	ka, kb := key.NewNode(), key.NewNode()
	a := newWSClient(ka, "example.com", dial, pool, func(string, ...any) {})
	b := newWSClient(kb, "example.com", dial, pool, func(string, ...any) {})
	defer a.Close()
	defer b.Close()
	recv := func(c *wsClient, from key.NodePublic) chan string {
		got := make(chan string, 1)
		go func() {
			for {
				m, err := c.Recv()
				if err != nil {
					return
				}
				if p, ok := m.(derp.ReceivedPacket); ok && p.Source == from {
					got <- string(p.Data)
					return
				}
			}
		}()
		return got
	}
	fromA, fromB := recv(b, ka.Public()), recv(a, kb.Public())
	// Each has said who it is before the other sends to it.
	waitFor(t, "both connected", func() bool {
		a.mu.Lock()
		b.mu.Lock()
		defer a.mu.Unlock()
		defer b.mu.Unlock()
		return a.client != nil && b.client != nil
	})
	time.Sleep(100 * time.Millisecond)
	if err := a.Send(kb.Public(), []byte("from a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Send(ka.Public(), []byte("from b")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		got  chan string
		want string
	}{{fromA, "from a"}, {fromB, "from b"}} {
		select {
		case s := <-c.got:
			if s != c.want {
				t.Errorf("got %q, want %q", s, c.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%q never came", c.want)
		}
	}
}
