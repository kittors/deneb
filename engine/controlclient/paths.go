package controlclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/engine/bind"
)

// The two ways to the control server (Options.BindInterface): pinned to the
// physical interface, so that the control plane does not rise and fall with
// a proxy's node, and by the routing table, which may go through the
// proxy's TUN. The pinned way is kept while it works: it has
// pinnedHeadStart to get through its handshake before the routing table is
// tried alongside, and a connection on it works only once the server's
// first answer came over it. One that fails before that, or loses to the
// routing table, has new connections go by the routing table for
// pinnedAvoidFor, or until the interface changes (11-16: the direct way to
// the server's data centre let TCP through and never finished TLS, while
// the proxy's took 0.5 s; every request timed out on the pinned way).
// design/plan/m7/engine-restart-offline.md §1.

var (
	pinnedHeadStart = 3 * time.Second
	pinnedAvoidFor  = 10 * time.Minute
)

// handshakeTimeout is how long a TLS handshake may take, either way: past
// it, the next entrance is tried (entrances.go).
var handshakeTimeout = 10 * time.Second

// errSlow: the pinned way was not through its handshake when the routing
// table's was.
var errSlow = errors.New("the routing table was through first")

// dialFunc opens one connection: pinned to ifname, or by the routing table
// when ifname is "". Tests put their own.
type dialFunc func(ctx context.Context, network, addr, ifname string) (net.Conn, error)

func dialDefault(ctx context.Context, network, addr, ifname string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if ifname != "" {
		ctl, err := bind.Control(ifname)
		if err != nil {
			return nil, err
		}
		d.Timeout, d.Control = 5*time.Second, ctl
	}
	return d.DialContext(ctx, network, addr)
}

// connect opens a connection to the entrance at addr, through its TLS
// handshake when it is https; one that does not get through is a dialError
// (send tries the next entrance).
func (c *Client) connect(ctx context.Context, network, addr string) (net.Conn, error) {
	e := c.entranceFor(addr)
	if e == nil {
		return nil, dialError{fmt.Errorf("%s is none of the control server's entrances", addr)}
	}
	conn, err := c.connectTo(ctx, network, e)
	if err != nil {
		return nil, dialError{err}
	}
	return conn, nil
}

// connectTo opens a connection to e: the pinned way while that works,
// otherwise the routing table.
func (c *Client) connectTo(ctx context.Context, network string, e *entrance) (net.Conn, error) {
	addr := e.addr
	ifname := *c.bindIf.Load()
	if ifname == "" || c.pinnedAvoided() {
		return c.open(ctx, network, e, "")
	}
	type result struct {
		conn   net.Conn
		err    error
		pinned bool
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 2)
	try := func(ifname string) {
		conn, err := c.open(ctx, network, e, ifname)
		results <- result{conn, err, ifname != ""}
	}
	go try(ifname)
	head := time.NewTimer(pinnedHeadStart)
	defer head.Stop()
	routed, left := false, 1
	route := func() {
		if !routed {
			routed, left = true, left+1
			go try("")
		}
	}
	var pinnedErr, routeErr error
	for {
		select {
		case <-head.C:
			route()
			continue
		case r := <-results:
			left--
			if r.err == nil {
				if !r.pinned {
					why := pinnedErr
					if why == nil {
						why = errSlow
					}
					c.pinnedFailed(ifname, why)
				}
				if left > 0 {
					// The other way, should it get through too, is closed.
					go func() {
						if o := <-results; o.conn != nil {
							closeQuietly(o.conn)
						}
					}()
				}
				return r.conn, nil
			}
			if r.pinned {
				pinnedErr = r.err
				route()
			} else {
				routeErr = r.err
			}
		}
		if left == 0 {
			return nil, fmt.Errorf("%s through %s: %w; by the routing table: %w", addr, ifname, pinnedErr, routeErr)
		}
	}
}

// open dials e one way and does the TLS handshake. A connection pinned to
// an interface is watched until the server answers on it.
func (c *Client) open(ctx context.Context, network string, e *entrance, ifname string) (net.Conn, error) {
	addr := e.addr
	conn, err := c.dial(ctx, network, addr, ifname)
	if err != nil {
		return nil, err
	}
	var w *watchedConn
	if ifname != "" {
		w = &watchedConn{Conn: conn, c: c, ifname: ifname}
		conn = w
	}
	if e.tls == nil {
		if w != nil {
			w.handshook.Store(true)
		}
		return conn, nil
	}
	// As net/http would: the host for SNI (none for an address), and h2
	// offered, which the transport hands to http2 (New).
	cfg := c.tlsFor(e)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		cfg.ServerName = host
	}
	cfg.NextProtos = []string{"h2", "http/1.1"}
	tc := tls.Client(conn, cfg)
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		if w != nil {
			w.quiet.Store(true) // connect counts this one
		}
		tc.Close()
		return nil, err
	}
	if w != nil {
		w.handshook.Store(true)
	}
	return tc, nil
}

// closeQuietly closes a connection the client does not want after all: not
// a failure of its way.
func closeQuietly(conn net.Conn) {
	if w := watched(conn); w != nil {
		w.quiet.Store(true)
	}
	conn.Close()
}

// watched is the pinned connection under conn, if it is one.
func watched(conn net.Conn) *watchedConn {
	if tc, ok := conn.(*tls.Conn); ok {
		conn = tc.NetConn()
	}
	w, _ := conn.(*watchedConn)
	return w
}

// answerTrace marks the connection a request goes on as answered when the
// server's response begins (send): its way works. Not anything read before:
// TLS 1.3 sends its session tickets right after the handshake, on a way
// that may carry nothing more. used is the connection the request went on,
// if it got one: a request that fails with no answer counts against its
// way there and then (send), not when http2 gets round to closing it,
// after the caller has heard of the failure and maybe tried again.
func answerTrace() (trace *httptrace.ClientTrace, used func() net.Conn) {
	var mu sync.Mutex
	var conn net.Conn
	return &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				mu.Lock()
				conn = info.Conn
				mu.Unlock()
			},
			GotFirstResponseByte: func() {
				mu.Lock()
				w := watched(conn)
				mu.Unlock()
				if w != nil {
					w.answered.Store(true)
				}
			},
		}, func() net.Conn {
			mu.Lock()
			defer mu.Unlock()
			return conn
		}
}

// watchedConn is a connection pinned to an interface. It works once the
// server's first response came over it (answerTrace); until then, an error
// reading or its closing (net/http closes a connection whose request timed
// out, http2 one that answers no ping) counts against its way.
type watchedConn struct {
	net.Conn
	c         *Client
	ifname    string
	handshook atomic.Bool // past TLS: what fails now fails the server's answer
	answered  atomic.Bool
	quiet     atomic.Bool // counted already, or closed by the client
}

func (w *watchedConn) Read(b []byte) (int, error) {
	n, err := w.Conn.Read(b)
	if err != nil {
		w.unanswered(err)
	}
	return n, err
}

func (w *watchedConn) Close() error {
	w.unanswered(net.ErrClosed)
	return w.Conn.Close()
}

func (w *watchedConn) unanswered(err error) {
	if w.handshook.Load() && !w.answered.Load() && w.quiet.CompareAndSwap(false, true) {
		w.c.pinnedFailed(w.ifname, err)
	}
}

// pinnedFailed has new connections go by the routing table for a while.
func (c *Client) pinnedFailed(ifname string, why error) {
	now := time.Now()
	if old := c.avoidPinned.Swap(now.Add(pinnedAvoidFor).UnixNano()); old <= now.UnixNano() {
		c.logf("[control] no answer through %s (%v): by the routing table for %s", ifname, why, pinnedAvoidFor)
	}
}

// pinnedAvoided: new connections go by the routing table now.
func (c *Client) pinnedAvoided() bool { return time.Now().UnixNano() < c.avoidPinned.Load() }
