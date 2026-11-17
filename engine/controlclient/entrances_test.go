package controlclient

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestEntrances: the ways to one server in order (design/plan/
// control-reachability-20261116.md §9.3). The first never finishes TLS:
// the second, the same server's key handed through, takes over, and stays.
// A login link the server writes with its own address goes the way in use.
// With nothing getting through, the one in use stays. When the two before
// it do not get through, a CDN's, checked by the roots and its name. And
// the first is taken back once it gets through again.
func TestEntrances(t *testing.T) {
	oldHS, oldProbe := handshakeTimeout, probeEvery
	handshakeTimeout, probeEvery = 300*time.Millisecond, time.Hour
	t.Cleanup(func() { handshakeTimeout, probeEvery = oldHS, oldProbe })

	pong := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte("pong")) })
	srv := httptest.NewUnstartedServer(pong)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	cdn := httptest.NewUnstartedServer(pong) // its certificate is for example.com
	cdn.EnableHTTP2 = true
	cdn.StartTLS()
	t.Cleanup(cdn.Close)
	stall, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn // TCP through, TLS never
	var heldMu sync.Mutex
	t.Cleanup(func() {
		stall.Close()
		heldMu.Lock()
		defer heldMu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := stall.Accept()
			if err != nil {
				return
			}
			heldMu.Lock()
			held = append(held, conn)
			heldMu.Unlock()
		}
	}()
	pin := proto.Fingerprint(srv.Certificate())
	srvAddr := srv.Listener.Addr().String()
	_, cdnPort, _ := net.SplitHostPort(cdn.Listener.Addr().String())
	ents := []proto.ControlEntrance{
		{URL: "https://" + stall.Addr().String(), Pin: pin},
		{URL: srv.URL, Pin: pin},
		{URL: "https://example.com:" + cdnPort},
	}
	var mu sync.Mutex
	var moves []string
	c, err := New(Options{Entrances: ents, Machine: proto.NewMachineKey(), Logf: t.Logf, OnEntrance: func(u string) {
		mu.Lock()
		moves = append(moves, u)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cdn.Certificate())
	c.rootsFor(pool)
	var down, onlyCDN, firstGood atomic.Bool
	var stallDials atomic.Int32
	c.dial = func(ctx context.Context, network, addr, _ string) (net.Conn, error) {
		switch {
		case down.Load():
			return nil, errors.New("no network")
		case onlyCDN.Load() && !strings.HasPrefix(addr, "example.com:"):
			return nil, errors.New("blocked")
		case addr == stall.Addr().String() && firstGood.Load():
			addr = srvAddr
		case addr == stall.Addr().String():
			stallDials.Add(1)
		case strings.HasPrefix(addr, "example.com:"):
			addr = "127.0.0.1:" + cdnPort
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	if err := ping(c); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if c.URL() != srv.URL || stallDials.Load() != 1 {
		t.Fatalf("by %s, the first dialed %d times; want by %s", c.URL(), stallDials.Load(), srv.URL)
	}
	c.ResetConnections()
	if err := ping(c); err != nil || stallDials.Load() != 1 {
		t.Errorf("a new connection: %v, the first dialed %d times; want the second at once", err, stallDials.Load())
	}
	link := ents[0].URL + "/login?c=abc"
	if got := c.Through(link); got != srv.URL+"/login?c=abc" {
		t.Errorf("login link %s by %s", got, c.URL())
	}
	if other := "https://203.0.113.9:47443/login?c=abc"; c.Through(other) != other {
		t.Error("a link elsewhere changed")
	}

	down.Store(true)
	c.ResetConnections()
	if err := ping(c); !IsOffline(err) || c.URL() != srv.URL {
		t.Errorf("nothing gets through: %v, by %s; want offline, still by %s", err, c.URL(), srv.URL)
	}
	down.Store(false)

	onlyCDN.Store(true)
	c.ResetConnections()
	if err := ping(c); err != nil || c.URL() != ents[2].URL {
		t.Fatalf("only the CDN gets through: %v, by %s", err, c.URL())
	}
	onlyCDN.Store(false)

	firstGood.Store(true)
	probeEvery = 0
	if err := ping(c); err != nil {
		t.Fatalf("ping by the CDN: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); c.URL() != ents[0].URL && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if c.URL() != ents[0].URL {
		t.Errorf("the first gets through again; still by %s", c.URL())
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{srv.URL, ents[2].URL, ents[0].URL}; strings.Join(moves, " ") != strings.Join(want, " ") {
		t.Errorf("moves %v, want %v", moves, want)
	}
}

// TestEntrancesBackOnNetworkChange: on the second entrance, the first
// blocked; the network changes (NetworkChanged, from the engine's
// netChanged) and the first gets through there: taken back at once, not
// after probeEvery.
func TestEntrancesBackOnNetworkChange(t *testing.T) {
	oldProbe := probeEvery
	probeEvery = time.Hour
	t.Cleanup(func() { probeEvery = oldProbe })
	pong := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte("pong")) })
	srv := httptest.NewUnstartedServer(pong)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pin := proto.Fingerprint(srv.Certificate())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	first := ln.Addr().String() // its own address; dialed to srv once unblocked
	ln.Close()
	c, err := New(Options{Entrances: []proto.ControlEntrance{{URL: "https://" + first, Pin: pin}, {URL: srv.URL, Pin: pin}},
		Machine: proto.NewMachineKey(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	var blocked atomic.Bool
	blocked.Store(true)
	c.dial = func(ctx context.Context, network, addr, _ string) (net.Conn, error) {
		if addr == first {
			if blocked.Load() {
				return nil, errors.New("blocked")
			}
			addr = srv.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	if err := ping(c); err != nil || c.URL() != srv.URL {
		t.Fatalf("ping: %v, by %s; want the second", err, c.URL())
	}
	blocked.Store(false)
	time.Sleep(200 * time.Millisecond)
	if c.URL() != srv.URL {
		t.Fatalf("back to %s before the network changed", c.URL())
	}
	c.NetworkChanged()
	for deadline := time.Now().Add(5 * time.Second); c.URL() != "https://"+first && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if c.URL() != "https://"+first {
		t.Errorf("after the network changed, still by %s", c.URL())
	}
}

// TestEntranceSwitch: the test switch for a real machine (deneb debug
// control-entrance). The entrance in use turned off: the stream is told to
// start again, and the next request goes by the next. Turned on again: back
// to it at once. A place past the list is refused.
func TestEntranceSwitch(t *testing.T) {
	oldProbe := probeEvery
	probeEvery = time.Hour
	t.Cleanup(func() { probeEvery = oldProbe })
	pong := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte("pong")) })
	start := func() *httptest.Server {
		s := httptest.NewUnstartedServer(pong)
		s.EnableHTTP2 = true
		s.StartTLS()
		t.Cleanup(s.Close)
		return s
	}
	a, b := start(), start()
	var kicked atomic.Int32
	c, err := New(Options{Entrances: []proto.ControlEntrance{
		{URL: a.URL, Pin: proto.Fingerprint(a.Certificate())},
		{URL: b.URL, Pin: proto.Fingerprint(b.Certificate())},
	}, Machine: proto.NewMachineKey(), Logf: t.Logf, OnBack: func() { kicked.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := ping(c); err != nil || c.URL() != a.URL {
		t.Fatalf("ping: %v by %s; want the first", err, c.URL())
	}

	if err := c.SetEntranceOff(1, true); err != nil {
		t.Fatal(err)
	}
	if kicked.Load() == 0 {
		t.Error("the stream not told to start again")
	}
	if err := ping(c); err != nil || c.URL() != b.URL {
		t.Fatalf("the first off: %v by %s; want the second", err, c.URL())
	}
	if got := c.Entrances(); !got[0].Off || got[0].InUse || !got[1].InUse || got[1].Off || !got[0].Pinned {
		t.Errorf("listed %+v", got)
	}

	if err := c.SetEntranceOff(1, false); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); c.URL() != a.URL && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if c.URL() != a.URL {
		t.Errorf("the first on again: still by %s", c.URL())
	}

	for _, n := range []int{0, 3, -1} {
		if err := c.SetEntranceOff(n, true); err == nil {
			t.Errorf("entrance %d of 2 turned off", n)
		}
	}
}

// TestEntranceChecks: a CDN's entrance, with no pin, is checked by its name
// against the roots; one named by its address cannot be.
func TestEntranceChecks(t *testing.T) {
	if _, err := New(Options{Entrances: []proto.ControlEntrance{{URL: "https://203.0.113.9:443"}}}); err == nil {
		t.Error("an address with no pin taken")
	}
	if _, err := proto.ParseControlEntrances(`{"version":1,"entrances":[{"url":"https://203.0.113.9"}]}`); err == nil {
		t.Error("the list: an address with no pin taken")
	}
	l, err := proto.ParseControlEntrances(`{"version":1,"entrances":[{"url":"https://203.0.113.9:47443/","pin":"sha256:` + strings.Repeat("ab", 32) + `"},{"url":"https://control.example.com"}]}`)
	if err != nil || len(l.Entrances) != 2 || !l.Has("https://203.0.113.9:47443") {
		t.Errorf("a good list: %+v %v", l, err)
	}
}
