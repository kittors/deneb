package controlclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// ways stands in for the two ways to the control server, pinned to the
// interface and by the routing table, and counts the connections on each.
type ways struct {
	pinned, route   func(ctx context.Context, network, addr string) (net.Conn, error)
	pinnedN, routeN atomic.Int32
}

func (w *ways) dial(ctx context.Context, network, addr, ifname string) (net.Conn, error) {
	if ifname != "" {
		w.pinnedN.Add(1)
		return w.pinned(ctx, network, addr)
	}
	w.routeN.Add(1)
	return w.route(ctx, network, addr)
}

func direct(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// pathsServer is a control server on this machine, https with its pin and
// HTTP/2, that answers /ping; and a client for it pinned to en9, which
// goes its ways by w, the pinned way head ahead of the routing table.
func pathsServer(t *testing.T, w *ways, head time.Duration) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("pong"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	old := pinnedHeadStart
	pinnedHeadStart = head
	t.Cleanup(func() { pinnedHeadStart = old })
	c, err := New(Options{URL: srv.URL, Pin: proto.Fingerprint(srv.Certificate()), Machine: proto.NewMachineKey(), BindInterface: "en9", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if w.route == nil {
		w.route = direct
	}
	c.dial = w.dial
	return srv, c
}

func ping(c *Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := c.send(ctx, c.conns.Load().hc, "GET", "/ping", nil, false)
	if err != nil {
		return err
	}
	return decode(resp, nil)
}

// listener accepts connections on this machine and hands each to serve;
// the way to it ignores where it was asked to go.
func listener(t *testing.T, serve func(net.Conn)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			go serve(conn)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
	}
}

// TestPinnedNeverThroughTLS: the pinned way lets TCP through and never
// finishes TLS (11-16): the request still gets through, by the routing
// table, within the head start and a handshake; new connections go that
// way at once; on another interface the pinned way is tried again.
func TestPinnedNeverThroughTLS(t *testing.T) {
	w := &ways{pinned: listener(t, func(net.Conn) {})}
	_, c := pathsServer(t, w, 300*time.Millisecond)
	start := time.Now()
	if err := ping(c); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s", d)
	}
	if !c.pinnedAvoided() || w.pinnedN.Load() != 1 || w.routeN.Load() != 1 {
		t.Fatalf("avoided %v, pinned %d, routed %d", c.pinnedAvoided(), w.pinnedN.Load(), w.routeN.Load())
	}
	c.ResetConnections()
	if err := ping(c); err != nil {
		t.Fatalf("ping again: %v", err)
	}
	if w.pinnedN.Load() != 1 || w.routeN.Load() != 2 {
		t.Errorf("a new connection: pinned %d, routed %d; want the routing table at once", w.pinnedN.Load(), w.routeN.Load())
	}
	c.SetBindInterface("en10")
	if err := ping(c); err != nil {
		t.Fatalf("ping on en10: %v", err)
	}
	if w.pinnedN.Load() != 2 || w.routeN.Load() != 3 {
		t.Errorf("on en10: pinned %d, routed %d; want the pinned way tried again", w.pinnedN.Load(), w.routeN.Load())
	}
}

// TestPinnedRefused: a pinned way that fails at once has the routing table
// tried at once, not after the head start.
func TestPinnedRefused(t *testing.T) {
	w := &ways{pinned: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}}
	_, c := pathsServer(t, w, 5*time.Second)
	start := time.Now()
	if err := ping(c); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s: waited for the head start", d)
	}
	if !c.pinnedAvoided() {
		t.Error("the pinned way is still tried first")
	}
}

// TestPinnedHangsUpBeforeAnswering: the pinned way finishes TLS, with the
// server's own key, and hangs up before any answer. That way counts as not
// working: the next connection goes by the routing table, and gets through.
func TestPinnedHangsUpBeforeAnswering(t *testing.T) {
	w := &ways{}
	srv, c := pathsServer(t, w, 5*time.Second)
	cfg := srv.TLS.Clone()
	w.pinned = listener(t, func(conn net.Conn) {
		tc := tls.Server(conn, cfg)
		tc.Handshake()
		tc.Close()
	})
	ping(c) // may fail, or net/http may try again on a new connection
	if !c.pinnedAvoided() {
		t.Fatal("the way that hung up still comes first")
	}
	if err := ping(c); err != nil {
		t.Fatalf("by the routing table: %v", err)
	}
	if w.routeN.Load() == 0 {
		t.Error("never went by the routing table")
	}
}

// TestPinnedWorks: where the pinned way works, the routing table is never
// tried (the control plane does not ride the proxy), and the pinned
// connection closing once answered counts against nothing.
func TestPinnedWorks(t *testing.T) {
	w := &ways{pinned: direct, route: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("went by the routing table")
	}}
	_, c := pathsServer(t, w, 2*time.Second)
	for range 2 {
		if err := ping(c); err != nil {
			t.Fatalf("ping: %v", err)
		}
	}
	c.ResetConnections()
	if err := ping(c); err != nil {
		t.Fatalf("ping on a new connection: %v", err)
	}
	if c.pinnedAvoided() || w.routeN.Load() != 0 || w.pinnedN.Load() != 2 {
		t.Errorf("avoided %v, pinned %d, routed %d", c.pinnedAvoided(), w.pinnedN.Load(), w.routeN.Load())
	}
}
