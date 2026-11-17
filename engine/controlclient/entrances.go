package controlclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"time"
)

// Several ways to the same control server, the most direct first
// (Options.Entrances; design/plan/control-reachability-20261116.md §9.3).
// A request goes by the one that worked last; when its connection does not
// get through (connect, or TLS within handshakeTimeout), the others are
// tried, in the list's order, and the first that gets through is the one
// from then on. While it is not the first, the ones before it are tried
// again in the background every probeEvery: one that gets through again is
// taken back, so that a CDN that ends the TLS is used no longer than it has
// to be. A request that all fail leaves the one in use as it was: no
// network at all is no reason to move to the last.

var probeEvery = 30 * time.Minute

// entrance is one way in.
type entrance struct {
	url    string // https://host[:port]
	addr   string // host:port, as dialed
	tls    *tls.Config
	pinned bool        // the TLS reaches the server, and its key is checked; else the system's roots and the name
	off    atomic.Bool // a test turned it off (SetEntranceOff): it does not get through
}

// errEntranceOff: a test turned the entrance off.
var errEntranceOff = errors.New("this entrance is turned off for a test (deneb debug control-entrance)")

// EntranceState is one way in, as `deneb debug control-entrance` lists it.
type EntranceState struct {
	URL    string
	Pinned bool
	InUse  bool
	Off    bool
}

// Entrances are the ways in, in the list's order.
func (c *Client) Entrances() []EntranceState {
	cur := int(c.cur.Load())
	out := make([]EntranceState, len(c.ents))
	for i, e := range c.ents {
		out[i] = EntranceState{URL: e.url, Pinned: e.pinned, InUse: i == cur, Off: e.off.Load()}
	}
	return out
}

// SetEntranceOff turns entrance n (from 1, in the list's order) off for a
// test on a real machine, as if it did not get through, or on again; not
// saved. Off, the one in use gives way at once: requests go to the next
// that gets through, and the ones that stay open (the stream) start again
// there (OnBack). On, the ones before the one in use are tried again at
// once, as after a network change.
func (c *Client) SetEntranceOff(n int, off bool) error {
	if n < 1 || n > len(c.ents) {
		return fmt.Errorf("没有第 %d 个入口：一共 %d 个", n, len(c.ents))
	}
	c.ents[n-1].off.Store(off)
	if !off {
		c.NetworkChanged()
		return nil
	}
	if int(c.cur.Load()) == n-1 {
		c.ResetConnections()
		if c.onBack != nil {
			c.onBack()
		}
	}
	return nil
}

// dialError is a connection that did not get through to the server: the
// request never reached it, and the next entrance may be tried (send).
type dialError struct{ err error }

func (e dialError) Error() string { return e.err.Error() }
func (e dialError) Unwrap() error { return e.err }

// newEntrance reads one way in; New sets how its TLS is checked.
func newEntrance(raw string) (*entrance, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	e := &entrance{url: u.Scheme + "://" + u.Host, addr: u.Host}
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		e.addr = net.JoinHostPort(u.Hostname(), port)
	}
	return e, nil
}

// tlsFor is the TLS config to dial e with.
func (c *Client) tlsFor(e *entrance) *tls.Config {
	cfg := e.tls.Clone()
	if !e.pinned {
		cfg.RootCAs = c.roots
	}
	return cfg
}

// entranceFor is the entrance dialed at addr.
func (c *Client) entranceFor(addr string) *entrance {
	for _, e := range c.ents {
		if e.addr == addr {
			return e
		}
	}
	return nil
}

// order is the entrances to try a request by: the one in use, then the
// others in the list's order.
func (c *Client) order() []int32 {
	cur := c.cur.Load()
	o := []int32{cur}
	for i := range int32(len(c.ents)) {
		if i != cur {
			o = append(o, i)
		}
	}
	return o
}

// moved makes entrance to the one in use, a request having got through on
// it; a later one waits probeEvery before the ones before it are tried.
func (c *Client) moved(to int32) {
	from := c.cur.Swap(to)
	if from == to {
		return
	}
	if to > from {
		c.probed.Store(time.Now().UnixNano())
	}
	c.logf("[control] through %s now", c.ents[to].url)
	if c.onEntrance != nil {
		c.onEntrance(c.ents[to].url)
	}
}

// maybeProbe tries, in the background, the entrances before the one in use,
// every probeEvery; the first that gets through is taken back, and new
// connections go by it.
func (c *Client) maybeProbe() {
	cur := c.cur.Load()
	now := time.Now().UnixNano()
	if cur == 0 || now-c.probed.Load() < int64(probeEvery) || !c.probing.CompareAndSwap(false, true) {
		return
	}
	c.probed.Store(now)
	go func() {
		defer c.probing.Store(false)
		for i := range cur {
			ctx, cancel := context.WithTimeout(context.Background(), 2*handshakeTimeout)
			conn, err := c.connect(ctx, "tcp", c.ents[i].addr)
			cancel()
			if err == nil {
				closeQuietly(conn)
				c.moved(i)
				c.ResetConnections()
				if c.onBack != nil {
					c.onBack()
				}
				return
			}
		}
	}()
}

// NetworkChanged is told of a new network (the engine's netChanged): the
// entrances before the one in use are tried again at once, not in
// probeEvery, to be on a CDN, which sees what it carries, no longer than
// it has to be.
func (c *Client) NetworkChanged() {
	c.probed.Store(0)
	c.maybeProbe()
}

// Through is link, from the server, by the entrance in use when its origin
// is one of the entrances: a login link the server writes with its own
// address works by the way in that works.
func (c *Client) Through(link string) string {
	u, err := url.Parse(link)
	if err != nil || len(c.ents) < 2 {
		return link
	}
	origin := u.Scheme + "://" + u.Host
	for _, e := range c.ents {
		if e.url == origin {
			cur, _ := url.Parse(c.ents[c.cur.Load()].url)
			u.Scheme, u.Host = cur.Scheme, cur.Host
			return u.String()
		}
	}
	return link
}

// rootsFor makes a test's roots the ones an entrance without a pin is
// checked by.
func (c *Client) rootsFor(pool *x509.CertPool) { c.roots = pool }
