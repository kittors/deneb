package magicsock

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/types/key"
)

// A public relay's two ways in (proto.Relay.Tunnel; design/plan/
// control-reachability-20261116.md §10.4 甲): its own address first, as
// ever, and its Cloudflare tunnel when that does not get through, as the
// control server's entrances do (§9.3): 3 s to connect, or TCP through and
// TLS not in relayTLSTimeout (12:10: the handshake let through, every
// answer dropped). The way that worked last is the one a new connection
// takes; while it is the tunnel, the relay's own address is tried again in
// the background every tunnelProbeEvery, and taken back once it gets
// through. Through the tunnel the relay is wss://<Tunnel>/derp on 443,
// subprotocol "derp", its certificate checked by the system's roots
// (RelayRoots in tests); inside, DERP as usual. derphttp does not do it
// here: its WebSocket client is built only with a tag and turned on for
// the whole process, and dials around SetURLDialer.

var (
	relayTLSTimeout  = 10 * time.Second
	tunnelProbeEvery = 30 * time.Minute
	tunnelHeadStart  = 3 * time.Second
	errDirectOff     = errors.New("the relay's own address is turned off for a test (deneb debug relay-direct)")
)

// relayClient is a relay connection's DERP client: derphttp's, the
// tunnel's, or the two ways of a public relay.
type relayClient interface {
	Recv() (derp.ReceivedMessage, error)
	Send(dst key.NodePublic, b []byte) error
	NotePreferred(bool)
	Close() error
}

// wsClient speaks DERP over a WebSocket to a relay's tunnel, connecting
// when first used and again after the connection broke, as derphttp does.
type wsClient struct {
	priv  key.NodePrivate
	url   string // wss://<Tunnel>/derp
	dial  func(ctx context.Context, network, addr string) (net.Conn, error)
	roots *x509.CertPool
	logf  func(string, ...any)
	ctx   context.Context
	stop  context.CancelFunc

	mu        sync.Mutex
	client    *derp.Client
	conn      net.Conn
	preferred bool
	closed    bool
}

func newWSClient(priv key.NodePrivate, host string, dial func(ctx context.Context, network, addr string) (net.Conn, error), roots *x509.CertPool, logf func(string, ...any)) *wsClient {
	ctx, stop := context.WithCancel(context.Background())
	return &wsClient{priv: priv, url: "wss://" + host + "/derp", dial: dial, roots: roots, logf: logf, ctx: ctx, stop: stop}
}

// connected is the DERP client over the tunnel, connecting first if there
// is none.
func (w *wsClient) connected() (*derp.Client, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, derphttp.ErrClientClosed
	}
	if w.client != nil {
		return w.client, nil
	}
	ctx, cancel := context.WithTimeout(w.ctx, relayTLSTimeout+tunnelHeadStart)
	defer cancel()
	hc := &http.Client{Transport: &http.Transport{
		Proxy:             nil,
		DialContext:       w.dial,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: w.roots},
		ForceAttemptHTTP2: false, // a WebSocket upgrade is HTTP/1.1
	}}
	wc, _, err := websocket.Dial(ctx, w.url, &websocket.DialOptions{HTTPClient: hc, Subprotocols: []string{"derp"}})
	if err != nil {
		return nil, err
	}
	wc.SetReadLimit(1 << 20)
	conn := websocket.NetConn(w.ctx, wc, websocket.MessageBinary)
	brw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	dc, err := derp.NewClient(w.priv, conn, brw, w.logf)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if w.preferred {
		if err := dc.NotePreferred(true); err != nil {
			conn.Close()
			return nil, err
		}
	}
	w.client, w.conn = dc, conn
	return dc, nil
}

// drop closes the connection under dc, if it is still the one: the next
// use connects again.
func (w *wsClient) drop(dc *derp.Client) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if dc == nil || w.client == dc {
		if w.conn != nil {
			w.conn.Close()
		}
		w.client, w.conn = nil, nil
	}
}

func (w *wsClient) Recv() (derp.ReceivedMessage, error) {
	dc, err := w.connected()
	if err != nil {
		return nil, err
	}
	m, err := dc.Recv()
	if err != nil {
		w.drop(dc)
	}
	return m, err
}

func (w *wsClient) Send(dst key.NodePublic, b []byte) error {
	dc, err := w.connected()
	if err != nil {
		return err
	}
	if err := dc.Send(dst, b); err != nil {
		w.drop(dc)
		return err
	}
	return nil
}

func (w *wsClient) NotePreferred(v bool) {
	w.mu.Lock()
	w.preferred = v
	dc := w.client
	w.mu.Unlock()
	if dc != nil {
		dc.NotePreferred(v)
	}
}

func (w *wsClient) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.stop()
	w.drop(nil)
	return nil
}

// twoWays is a public relay's client: by its own address (direct, made
// anew by newDirect when one is closed) or by its tunnel, as the type's
// comment says.
type twoWays struct {
	name      string
	newDirect func() *derphttp.Client
	tunnel    *wsClient
	logf      func(string, ...any)
	// probe tries the relay's own address: a connection through TLS, no
	// more (the relay's pin checked), closed again.
	probe func(ctx context.Context) error
	// moved is told the way in use when it changes (Conn.viaTunnel).
	moved func(tunnel bool)

	mu        sync.Mutex
	direct    *derphttp.Client
	preferred bool
	closed    bool
	viaTunnel atomic.Bool
	since     atomic.Int64 // when the tunnel was last taken or the own address tried (Unix nanoseconds)
	// directFailed: the last dial of the own address did not get through
	// (set by its dialer, see Conn.newRelayConn).
	directFailed atomic.Bool
	back         atomic.Bool   // the own address answers again: the tunnel's Recv ends for it
	wake         chan struct{} // tries the own address now (SetRelayDirectOff turned it on)
	done         chan struct{} // closed with the client
}

// newTwoWays is the two ways of a public relay; tunnelSince, when not
// zero, is when a connection last went by the tunnel: this one starts
// there.
func newTwoWays(name string, newDirect func() *derphttp.Client, tunnel *wsClient, probe func(ctx context.Context) error, moved func(bool), tunnelSince time.Time, logf func(string, ...any)) *twoWays {
	p := &twoWays{name: name, newDirect: newDirect, tunnel: tunnel, probe: probe, moved: moved, logf: logf,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	if !tunnelSince.IsZero() {
		p.viaTunnel.Store(true)
		p.since.Store(tunnelSince.UnixNano())
		go p.probeLoop()
	}
	return p
}

func (p *twoWays) directClient() *derphttp.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.direct == nil {
		p.direct = p.newDirect()
		p.direct.NotePreferred(p.preferred)
	}
	return p.direct
}

func (p *twoWays) Recv() (derp.ReceivedMessage, error) {
	for {
		if p.viaTunnel.Load() {
			m, err := p.tunnel.Recv()
			if err != nil && p.back.Swap(false) {
				continue // the own address answers again: by it
			}
			return m, err
		}
		dc := p.directClient()
		p.directFailed.Store(false)
		m, err := dc.Recv()
		if err == nil {
			return m, nil
		}
		if p.isClosed() || !p.directFailed.Load() || p.tunnel == nil {
			return nil, err
		}
		// The own address did not get through. The tunnel is the way only
		// once it does: connected, the DERP handshake done. Not either is no
		// network at all, not a blocked address: the own address stays the
		// way, and is tried first again (§10.4 甲: through Cloudflare only
		// what is blocked).
		if _, terr := p.tunnel.connected(); terr != nil {
			return nil, err
		}
		p.use(true)
		p.logf("[relay] %s: its own address does not get through (%v); by its tunnel", p.name, err)
	}
}

func (p *twoWays) Send(dst key.NodePublic, b []byte) error {
	if p.viaTunnel.Load() {
		return p.tunnel.Send(dst, b)
	}
	return p.directClient().Send(dst, b)
}

func (p *twoWays) NotePreferred(v bool) {
	p.mu.Lock()
	p.preferred = v
	dc := p.direct
	p.mu.Unlock()
	if dc != nil {
		dc.NotePreferred(v)
	}
	p.tunnel.NotePreferred(v)
}

func (p *twoWays) Close() error {
	p.mu.Lock()
	if !p.closed {
		close(p.done)
	}
	p.closed = true
	dc := p.direct
	p.mu.Unlock()
	if dc != nil {
		dc.Close()
	}
	return p.tunnel.Close()
}

func (p *twoWays) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// use makes the tunnel, or the own address, the way in use. Leaving the own
// address, its client is closed: a new one is made to come back to it.
func (p *twoWays) use(tunnel bool) {
	if p.viaTunnel.Swap(tunnel) == tunnel {
		return
	}
	p.since.Store(time.Now().UnixNano())
	if tunnel {
		p.mu.Lock()
		dc := p.direct
		p.direct = nil
		p.mu.Unlock()
		if dc != nil {
			dc.Close()
		}
		go p.probeLoop()
	}
	if p.moved != nil {
		p.moved(tunnel)
	}
}

// leaveDirect has the own address given up now (a test turned it off): its
// connection closes, and Recv goes on by the tunnel.
func (p *twoWays) leaveDirect() {
	if p.tunnel == nil || p.viaTunnel.Load() {
		return
	}
	p.directFailed.Store(true)
	p.mu.Lock()
	dc := p.direct
	p.direct = nil
	p.mu.Unlock()
	if dc != nil {
		dc.Close() // its Recv fails; directFailed sends Recv to the tunnel
	}
}

// probeLoop tries the own address every tunnelProbeEvery from when the
// tunnel was taken, or at once when woken, while the tunnel is in use; once
// it gets through, the tunnel's connection is closed and Recv goes back to
// it.
func (p *twoWays) probeLoop() {
	for p.viaTunnel.Load() {
		wait := tunnelProbeEvery - time.Since(time.Unix(0, p.since.Load()))
		select {
		case <-p.done:
			return
		case <-p.wake:
		case <-time.After(max(wait, 0)):
		}
		if !p.viaTunnel.Load() {
			return
		}
		p.since.Store(time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), tunnelHeadStart+relayTLSTimeout)
		err := p.probe(ctx)
		cancel()
		if err != nil {
			continue
		}
		p.logf("[relay] %s: its own address gets through again; back to it", p.name)
		p.back.Store(true)
		p.use(false)
		p.tunnel.drop(nil) // the tunnel's Recv ends; Recv goes on by the own address
		return
	}
}

// probeNow wakes probeLoop.
func (p *twoWays) probeNow() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
