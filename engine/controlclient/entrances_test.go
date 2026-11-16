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
