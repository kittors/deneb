package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/derp"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/netutil"
)

// relay is a running Deneb relay: Tailscale's DERP server, with each
// client asked about at the control server, counted, and closed when it
// may no longer use the relay.
type relay struct {
	logf func(string, ...any)
	ctl  *controlClient
	derp *derpserver.Server

	mu    sync.Mutex
	st    state
	conns map[*clientConn]bool // open, or closed with bytes not yet reported
	rate  int64                // bytes a second per connection; zero is none
	// info goes with the next sync, until one gets through: where the
	// relay is now.
	info *proto.RelayInfo

	total   atomic.Int64 // bytes carried, in and out, since the start
	meter   *healthMeter
	syncNow chan struct{} // a watch saw a change
}

// newRelay makes the relay; serve starts it.
func newRelay(ctl *controlClient, st state, info proto.RelayInfo, d *derpserver.Server, logf func(string, ...any)) *relay {
	r := &relay{logf: logf, ctl: ctl, derp: d, st: st, conns: map[*clientConn]bool{}, info: &info,
		meter: newHealthMeter(time.Now()), syncNow: make(chan struct{}, 1)}
	d.SetVerifyClientURL(ctl.base + proto.PathRelayAdmit)
	// If the control server cannot be asked, a public relay lets the
	// client in (better than no one getting through) and someone's own
	// refuses (their traffic is not for strangers): implementation.md
	// §2.6.3. A refusal from the server is a refusal either way.
	d.SetVerifyClientURLFailOpen(st.Source == proto.RelayPublic)
	installAdmitTransport()
	return r
}

// handler serves DERP, and the probes netcheck makes.
func (r *relay) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/derp", r.serveDERP)
	m.HandleFunc("/derp/probe", derpserver.ProbeHandler)
	m.HandleFunc("/derp/latency-check", derpserver.ProbeHandler)
	m.HandleFunc("/generate_204", derpserver.ServeNoContent)
	m.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "Deneb relay\n")
	})
	return m
}

// connKey carries a connection to the admission request derpserver makes
// about it, through the context Accept is given.
type connKey struct{}

// serveDERP is derpserver.Handler, but with the connection wrapped, so
// its bytes are counted and it can be closed, and with the connection in
// the context, so the admission request can be tied to it.
func (r *relay) serveDERP(w http.ResponseWriter, req *http.Request) {
	if strings.ToLower(req.Header.Get("Upgrade")) != "derp" {
		http.Error(w, "DERP requires connection upgrade", http.StatusUpgradeRequired)
		return
	}
	fastStart := req.Header.Get(derp.FastStartHeader) == "1"
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijacking", http.StatusInternalServerError)
		return
	}
	nc, brw, err := h.Hijack()
	if err != nil {
		r.logf("hijack: %v", err)
		return
	}
	if !fastStart {
		nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(nc, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: DERP\r\nConnection: Upgrade\r\nDerp-Version: %v\r\nDerp-Public-Key: %s\r\n\r\n",
			derp.ProtocolVersion, r.derp.PublicKey().UntypedHexString()); err != nil {
			nc.Close()
			return
		}
	}
	c := &clientConn{Conn: nc, r: r}
	r.mu.Lock()
	r.conns[c] = true
	r.mu.Unlock()
	// The request's context ends with this handler; Accept gets a new
	// one, carrying the connection.
	ctx := context.WithValue(context.Background(), connKey{}, c)
	var readConn net.Conn = c
	if brw.Reader.Buffered() != 0 {
		readConn = netutil.NewDrainBufConn(c, brw.Reader)
	}
	br := bufio.NewReaderSize(readConn, 1<<10)
	go func() {
		defer func() {
			if e := recover(); e != nil {
				r.logf("panic serving %v: %v\n%s", nc.RemoteAddr(), e, debug.Stack())
				c.Close()
			}
		}()
		// Accept closes c when it returns.
		r.derp.Accept(ctx, c, bufio.NewReadWriter(br, nil), nc.RemoteAddr().String())
	}()
}

// clientConn is one client's connection: what it sent and received, and
// which node key it said it was.
type clientConn struct {
	net.Conn
	r *relay
	// sent and received are from the client's side: what the relay read
	// from it, and wrote to it.
	sent, received atomic.Int64

	mu       sync.Mutex
	node     proto.NodePublic
	keyed    bool // its admission request went out
	refused  bool // the server said no
	closed   bool
	reported [2]int64 // sent and received that syncs took already
}

func (c *clientConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.sent.Add(int64(n))
	c.r.total.Add(int64(n))
	return n, err
}

func (c *clientConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.received.Add(int64(n))
	c.r.total.Add(int64(n))
	return n, err
}

func (c *clientConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}

// refuse tells the client why, before derpserver closes it: a DERP health
// message, which the client's Recv returns before the end of the
// connection (proto.RelayRefusedPrefix). It runs on Accept's goroutine,
// in the middle of the handshake, so nothing else writes to the
// connection.
func (c *clientConn) refuse(reason string) {
	c.mu.Lock()
	c.refused = true
	c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	bw := bufio.NewWriterSize(c.Conn, 64)
	derp.WriteFrame(bw, derp.FrameHealth, []byte(proto.RelayRefusedPrefix+reason))
}

var installOnce sync.Once

// installAdmitTransport puts admitTransport under http.DefaultClient, which
// derpserver asks the admission URL with.
func installAdmitTransport() {
	installOnce.Do(func() { http.DefaultClient.Transport = admitTransport{} })
}

// admitTransport carries derpserver's admission requests to the control
// server: with the relay's secret, over the pinned connection. It notes
// the node key on the connection asked about, and on a refusal tells the
// client why. Any other request (there should be none) goes the usual
// way.
type admitTransport struct{}

func (admitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c, _ := req.Context().Value(connKey{}).(*clientConn)
	if c == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	r := c.r
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, 4<<10))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	var ar proto.RelayAdmitRequest
	if err := json.Unmarshal(body, &ar); err == nil {
		c.mu.Lock()
		c.node, c.keyed = ar.NodePublic, true
		c.mu.Unlock()
	}
	out, err := r.ctl.request(req.Context(), proto.PathRelayAdmit, body)
	if err != nil {
		return nil, err
	}
	resp, err := r.ctl.hc.Transport.RoundTrip(out)
	if err != nil {
		r.logf("admission of %s: the control server cannot be asked: %v", short(ar.NodePublic), err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var res proto.RelayAdmitResult
	if json.Unmarshal(b, &res) == nil && !res.Allow {
		c.refuse(res.Reason)
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	resp.ContentLength = int64(len(b))
	return resp, nil
}

// sync tells the control server who is connected and what each client
// carried, closes the ones it says may no longer use the relay, and
// takes its rate limit and name.
func (r *relay) sync(ctx context.Context) error {
	type taken struct {
		c              *clientConn
		sent, received int64
		reported       bool // its bytes go in this sync
		done           bool // closed and with nothing more to report
	}
	r.mu.Lock()
	info := r.info
	open := 0
	for c := range r.conns {
		c.mu.Lock()
		if !c.closed {
			open++
		}
		c.mu.Unlock()
	}
	health := r.meter.measure(time.Now(), r.total.Load(), open)
	var takes []taken
	connected := map[proto.NodePublic]bool{}
	usage := map[proto.NodePublic]*proto.RelayClientUsage{}
	for c := range r.conns {
		c.mu.Lock()
		node, keyed, refused, closed, rep := c.node, c.keyed, c.refused, c.closed, c.reported
		c.mu.Unlock()
		t := taken{c: c, sent: c.sent.Load(), received: c.received.Load()}
		// A connection refused, or closed before it said who it was,
		// has nothing to report.
		t.done = closed && (!keyed || refused)
		if keyed && !refused {
			if !closed {
				connected[node] = true
			}
			if ds, dr := t.sent-rep[0], t.received-rep[1]; ds > 0 || dr > 0 {
				u := usage[node]
				if u == nil {
					u = &proto.RelayClientUsage{Node: node}
					usage[node] = u
				}
				u.Sent += ds
				u.Received += dr
			}
			t.reported = true
			t.done = closed
		}
		takes = append(takes, t)
	}
	r.mu.Unlock()

	req := proto.RelaySyncRequest{Connected: []proto.NodePublic{}, Info: info, Health: &health}
	for k := range connected {
		req.Connected = append(req.Connected, k)
	}
	for _, u := range usage {
		req.Usage = append(req.Usage, *u)
	}
	slices.SortFunc(req.Connected, func(a, b proto.NodePublic) int { return strings.Compare(a.String(), b.String()) })
	var res proto.RelaySyncResult
	if err := r.ctl.call(ctx, proto.PathRelaySync, req, &res); err != nil {
		return err
	}

	deny := map[proto.NodePublic]bool{}
	for _, k := range res.Deny {
		deny[k] = true
	}
	r.mu.Lock()
	if r.info == info {
		r.info = nil
	}
	var closing []*clientConn
	for _, t := range takes {
		c := t.c
		c.mu.Lock()
		if t.reported {
			c.reported = [2]int64{t.sent, t.received}
		}
		node, closed := c.node, c.closed
		c.mu.Unlock()
		switch {
		case t.done:
			// Gone, and its bytes are in: forget it.
			delete(r.conns, c)
		case !closed && deny[node]:
			closing = append(closing, c)
		}
	}
	rateChanged := res.RateLimit != r.rate
	r.rate = res.RateLimit
	if res.Name != "" && res.Name != r.st.Name {
		r.logf("renamed %q", res.Name)
		r.st.Name = res.Name
	}
	r.mu.Unlock()
	for _, c := range closing {
		c.Close()
	}
	if len(closing) > 0 {
		r.logf("closed %d connections that may no longer use the relay", len(closing))
	}
	if rateChanged {
		r.applyRate(res.RateLimit)
	}
	return nil
}

// applyRate sets each connection's limit: what it may send through the
// relay a second, with a second's worth of burst.
func (r *relay) applyRate(rate int64) {
	applied := r.derp.UpdateRateLimits(derpserver.RateConfig{
		PerClientRateLimitBytesPerSec: uint64(rate), PerClientRateBurstBytes: uint64(rate),
	})
	if rate == 0 {
		r.logf("no rate limit")
		return
	}
	r.logf("each connection limited to %d bytes/s (burst %d)", applied.PerClientRateLimitBytesPerSec, applied.PerClientRateBurstBytes)
}

// syncLoop syncs every interval, and when a watch saw a change (a
// second apart at least), until ctx ends. A revoked secret ends it with
// errRevoked: the relay was deleted, or set up again elsewhere.
func (r *relay) syncLoop(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	failing := false
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-r.syncNow:
			if wait := time.Second - time.Since(last); wait > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
		}
		last = time.Now()
		err := r.sync(ctx)
		switch {
		case errors.Is(err, errRevoked):
			return err
		case err != nil && !failing:
			r.logf("sync: %v (retrying every %s)", err, every)
			failing = true
		case err == nil && failing:
			r.logf("sync: back")
			failing = false
		}
	}
}

// openConns counts the open connections; for tests and the log.
func (r *relay) openConns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for c := range r.conns {
		c.mu.Lock()
		if !c.closed {
			n++
		}
		c.mu.Unlock()
	}
	return n
}

// controlClient talks to the control server: HTTPS with its pin, or
// plain HTTP for a development server.
type controlClient struct {
	base string
	hc   *http.Client

	mu     sync.Mutex
	secret string
}

// errRevoked: the server does not know the relay's secret.
var errRevoked = errors.New("控制服务器不认这个中转站的密钥：它被删除了，或者在别处重新登记了")

// callError is a refusal from the control server.
type callError struct {
	status int
	msg    string
}

func (e *callError) Error() string { return fmt.Sprintf("%d: %s", e.status, e.msg) }

func newControlClient(base, pin string) (*controlClient, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("控制服务器地址不对：%q", base)
	}
	tr := &http.Transport{
		// Straight to the server: a proxy in the environment would see
		// the secret only through TLS, but it has no business there.
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
	if u.Scheme == "https" {
		if pin == "" {
			return nil, errors.New("HTTPS 的控制服务器要给 -control-pin")
		}
		cfg, err := proto.PinnedTLSConfig(pin)
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = cfg
	}
	return &controlClient{base: base, hc: &http.Client{Transport: tr, Timeout: 30 * time.Second}}, nil
}

func (c *controlClient) setSecret(s string) {
	c.mu.Lock()
	c.secret = s
	c.mu.Unlock()
}

// request is a POST of body to path, with the secret.
func (c *controlClient) request(ctx context.Context, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.mu.Lock()
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	c.mu.Unlock()
	return req, nil
}

// call POSTs in to path and decodes the answer into out. A 401 is
// errRevoked once the relay has a secret.
func (c *controlClient) call(ctx context.Context, path string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := c.request(ctx, path, b)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		msg := resp.Status
		var eb proto.ErrorBody
		if json.Unmarshal(body, &eb) == nil && eb.Error != nil {
			msg = eb.Error.Message
		}
		if resp.StatusCode == http.StatusUnauthorized && path != proto.PathRelayRegister {
			return fmt.Errorf("%w（%s）", errRevoked, msg)
		}
		return &callError{status: resp.StatusCode, msg: msg}
	}
	return json.Unmarshal(body, out)
}

// short is a node key for the log.
func short(k proto.NodePublic) string { return k.String()[:len("nodekey:")+8] }

// tlsConfig serves the relay's certificate. HTTP/2 is off: DERP upgrades
// an HTTP/1.1 connection.
func tlsConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
}
