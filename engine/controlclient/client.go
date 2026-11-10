// Package controlclient is the engine's side of the device interface
// (plan §2.6.1, proto/device.go): it proves the machine key for a token,
// logs the device in, holds the stream that brings network maps, reports
// endpoints and pauses, and passes ops on. Only the engine talks to the
// control server; the desktop client never does.
package controlclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"github.com/kittors/deneb/engine/bind"
	"github.com/kittors/deneb/proto"
)

// Client is safe for concurrent use.
type Client struct {
	base    *url.URL
	machine proto.MachinePrivate
	conns   atomic.Pointer[conns] // replaced whole when the network changes
	newTr   func() *http.Transport
	logf    func(format string, args ...any)
	bindIf  atomic.Pointer[string] // interface dials are pinned to; "" for none

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Options configure a Client.
type Options struct {
	// URL is the control server, "https://host:port" (or "http://..." for
	// the development server on this machine or in the test topology).
	URL string
	// PlainHTTPAnywhere lets a plain-http URL name any host (test builds:
	// the test topology). Otherwise plain http is for this machine only
	// (localhost, 127.0.0.0/8, ::1): anywhere else, a network in between
	// could rewrite the network map (M7 review).
	PlainHTTPAnywhere bool
	// Pin is the server key's fingerprint, "sha256:<hex>"; required for
	// https. The certificate is self-signed, so the pin is the whole trust.
	Pin     string
	Machine proto.MachinePrivate
	// BindInterface pins the connection to the physical interface, like the
	// relay's and the UDP socket: through a proxy's TUN the control plane
	// would rise and fall with the proxy node. If the pinned dial fails, the
	// routing table (and so the proxy) is tried, so the server stays
	// reachable in networks where only the proxy gets out.
	BindInterface string
	Logf          func(format string, args ...any)
}

// New returns a client; it does not connect yet.
func New(o Options) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(o.URL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("control server %q: want https://host:port", o.URL)
	}
	logf := o.Logf
	if logf == nil {
		logf = log.Printf
	}
	c := &Client{base: u, machine: o.Machine, logf: logf}
	c.SetBindInterface(o.BindInterface)
	plain := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if ifname := *c.bindIf.Load(); ifname != "" {
			if ctl, err := bind.Control(ifname); err == nil {
				pinned := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second, Control: ctl}
				conn, err := pinned.DialContext(ctx, network, addr)
				if err == nil || ctx.Err() != nil {
					return conn, err
				}
				logf("[control] dial %s pinned to %s failed (%v); trying the routing table", addr, ifname, err)
			}
		}
		return plain.DialContext(ctx, network, addr)
	}
	var tlsCfg *tls.Config
	if u.Scheme == "https" {
		if o.Pin == "" {
			return nil, errors.New("an https control server needs its pin (sha256:...)")
		}
		cfg, err := proto.PinnedTLSConfig(o.Pin)
		if err != nil {
			return nil, err
		}
		tlsCfg = cfg
	} else if !o.PlainHTTPAnywhere && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("control server %s: plain http only for this machine; use https and its pin", u.Redacted())
	} else if !isLocalDev(u.Hostname()) {
		// Plain http is for the development server only. Anything else
		// would let a network in between rewrite the network map.
		log.Printf("[control] WARNING: %s is plain http; only use this for development", u.Redacted())
	}
	c.newTr = func() *http.Transport {
		tr := &http.Transport{
			Proxy:               nil, // never through a system proxy: the pin is checked end to end
			DialContext:         dial,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        4,
			IdleConnTimeout:     90 * time.Second,
			TLSClientConfig:     tlsCfg,
		}
		if tlsCfg != nil {
			// One HTTP/2 connection carries the stream and every request.
			// When it dies quietly (the network under it changed and
			// nothing told us), pings find out within 20 s instead of
			// each request timing out on it for good.
			if h2, err := http2.ConfigureTransports(tr); err == nil {
				h2.ReadIdleTimeout, h2.PingTimeout = 15*time.Second, 5*time.Second
			} else {
				tr.ForceAttemptHTTP2 = true
			}
		}
		return tr
	}
	c.conns.Store(newConns(c.newTr()))
	return c, nil
}

// conns are the HTTP clients over one transport.
type conns struct {
	tr     *http.Transport
	hc     *http.Client // short requests
	stream *http.Client // the long-lived stream and long polls: no timeout
}

func newConns(tr *http.Transport) *conns {
	return &conns{tr: tr, hc: &http.Client{Transport: tr, Timeout: 30 * time.Second}, stream: &http.Client{Transport: tr}}
}

// SetBindInterface pins later connections to another interface (the
// default route moved, say from Wi-Fi to Ethernet) and starts over on new
// connections (see ResetConnections).
func (c *Client) SetBindInterface(ifname string) {
	c.bindIf.Store(&ifname)
	c.ResetConnections()
}

// ResetConnections makes later requests dial afresh, on a new transport:
// after a network change the old connection may be dead without knowing it
// (11-08: every request timed out on it for minutes after a move between
// networks, while the peers were back in 2.5 s). Requests under way on the
// old one finish or fail by themselves; its idle connections are closed.
func (c *Client) ResetConnections() {
	if c.newTr == nil {
		return
	}
	old := c.conns.Swap(newConns(c.newTr()))
	if old != nil {
		old.tr.CloseIdleConnections()
		time.AfterFunc(35*time.Second, old.tr.CloseIdleConnections) // once the last request on it is done
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalDev(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// URL is the control server's base URL.
func (c *Client) URL() string { return c.base.String() }

// Error is a refusal from the control server, or a transport failure
// (Offline set) when it could not be reached at all.
type Error struct {
	Status  int
	Code    proto.ErrorCode
	Message string
	Offline bool
	Err     error
}

func (e *Error) Error() string {
	if e.Offline {
		return fmt.Sprintf("control server unreachable: %v", e.Err)
	}
	return fmt.Sprintf("control server: %s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// IsOffline reports whether err means the control server could not be
// reached (as opposed to it saying no).
func IsOffline(err error) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.Offline
}

// authenticate trades a signed challenge for a token (proto/device.go step 1).
func (c *Client) authenticate(ctx context.Context) error {
	var ch proto.AuthChallenge
	pub := c.machine.Public()
	if err := c.call(ctx, c.conns.Load().hc, "POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: pub}, &ch, false); err != nil {
		return err
	}
	var tok proto.AuthToken
	req := proto.AuthTokenRequest{MachineKey: pub, Challenge: ch.Challenge, Signature: c.machine.SignChallenge(ch.Challenge)}
	if err := c.call(ctx, c.conns.Load().hc, "POST", proto.PathAuthToken, req, &tok, false); err != nil {
		return err
	}
	c.mu.Lock()
	c.token, c.expires = tok.Token, tok.ExpiresAt
	c.mu.Unlock()
	return nil
}

// bearer returns a token that is good for at least a minute.
func (c *Client) bearer(ctx context.Context) (string, error) {
	c.mu.Lock()
	tok, exp := c.token, c.expires
	c.mu.Unlock()
	if tok != "" && time.Until(exp) > time.Minute {
		return tok, nil
	}
	if err := c.authenticate(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, nil
}

func (c *Client) dropToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// call does one JSON request. With auth, a 401 gets a fresh token and one
// retry: tokens last about an hour and the server may forget them sooner.
func (c *Client) call(ctx context.Context, hc *http.Client, method, path string, body, out any, auth bool) error {
	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, hc, method, path, body, auth)
		if err != nil {
			return err
		}
		err = decode(resp, out)
		var ce *Error
		if auth && attempt == 0 && errors.As(err, &ce) && ce.Status == http.StatusUnauthorized {
			c.dropToken()
			continue
		}
		return err
	}
}

func (c *Client) send(ctx context.Context, hc *http.Client, method, path string, body any, auth bool) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		tok, err := c.bearer(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Offline: true, Err: err}
	}
	return resp, nil
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var eb proto.ErrorBody
		if json.Unmarshal(b, &eb) == nil && eb.Error != nil {
			return &Error{Status: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
		}
		code := proto.ErrInternal
		if resp.StatusCode == http.StatusUnauthorized {
			code = proto.ErrUnauthorized
		}
		return &Error{Status: resp.StatusCode, Code: code, Message: fmt.Sprintf("控制服务器返回 %s", resp.Status)}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		b, err := io.ReadAll(resp.Body)
		*raw = b
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Login tells the server about this device (sent at every start). If no one
// is logged in on it, the answer carries the link to open in a browser.
func (c *Client) Login(ctx context.Context, req proto.LoginRequest) (*proto.LoginState, error) {
	var st proto.LoginState
	return &st, c.call(ctx, c.conns.Load().hc, "POST", proto.PathLogin, req, &st, true)
}

// LoginWait long-polls until someone logs in in the browser, or about 25
// seconds pass (LoggedIn still false: call again).
func (c *Client) LoginWait(ctx context.Context) (*proto.LoginState, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var st proto.LoginState
	return &st, c.call(ctx, c.conns.Load().stream, "POST", proto.PathLoginWait, nil, &st, true)
}

// Logout logs the device out on the server.
func (c *Client) Logout(ctx context.Context) error {
	return c.call(ctx, c.conns.Load().hc, "POST", proto.PathLogout, nil, nil, true)
}

// Report replaces what the server knows of this device's endpoints, home
// relay and paused scopes.
func (c *Client) Report(ctx context.Context, r proto.Report) (uint64, error) {
	var res proto.ReportResult
	err := c.call(ctx, c.conns.Load().hc, "POST", proto.PathReport, r, &res, true)
	return res.Version, err
}

// Op passes an op on. args is the op's arguments as the client sent them.
func (c *Client) Op(ctx context.Context, id string, op string, args json.RawMessage) (version uint64, result json.RawMessage, err error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	body := struct {
		ID   string          `json:"id"`
		Op   string          `json:"op"`
		Args json.RawMessage `json:"args"`
	}{id, op, args}
	var res struct {
		Version uint64          `json:"version"`
		Result  json.RawMessage `json:"result,omitempty"`
	}
	if err := c.call(ctx, c.conns.Load().hc, "POST", proto.PathOps, body, &res, true); err != nil {
		return 0, nil, err
	}
	return res.Version, res.Result, nil
}

// UserView fetches the owner's view, for engines that do not stream it.
func (c *Client) UserView(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.call(ctx, c.conns.Load().hc, "GET", proto.PathUserView, nil, &raw, true)
}

// RelayUsage fetches what the owner's relays carried this month (M4), for
// the relay cards. Not pushed: the card asks when it opens.
func (c *Client) RelayUsage(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.call(ctx, c.conns.Load().hc, "GET", proto.PathRelayUsage, nil, &raw, true)
}

// PreviewInvite asks what an invite link is for.
func (c *Client) PreviewInvite(ctx context.Context, invite string) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.call(ctx, c.conns.Load().hc, "POST", proto.PathInvite, proto.InviteLookup{Invite: invite}, &raw, true)
}

// Frame is one stream frame, with the user view kept as raw JSON so the
// engine can pass it to the desktop client untouched.
type Frame struct {
	Version   uint64          `json:"version,omitempty"`
	NetMap    *proto.NetMap   `json:"netMap,omitempty"`
	UserView  json.RawMessage `json:"userView,omitempty"`
	KeepAlive bool            `json:"keepAlive,omitempty"`
	LoggedOut bool            `json:"loggedOut,omitempty"`
}

// ErrLoggedOut ends a stream whose last frame said the device was logged out.
var ErrLoggedOut = errors.New("device logged out")

// Stream holds the stream until it fails, calling fn for every frame but
// keep-alives. It returns ErrLoggedOut after a logged-out frame. A stream
// silent for proto.StreamDeadAfter counts as failed.
func (c *Client) Stream(ctx context.Context, since uint64, userView bool, fn func(Frame)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	q := url.Values{"since": {strconv.FormatUint(since, 10)}}
	if userView {
		q.Set("userView", "1")
	}
	resp, err := c.send(ctx, c.conns.Load().stream, "GET", proto.PathStream+"?"+q.Encode(), nil, true)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		c.dropToken()
		return &Error{Status: resp.StatusCode, Code: proto.ErrUnauthorized, Message: "token expired"}
	}
	if resp.StatusCode != http.StatusOK {
		return decode(resp, nil)
	}
	defer resp.Body.Close()

	// A watchdog closes the body if nothing arrives in time; the scanner
	// then fails and the caller reconnects.
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(proto.StreamDeadAfter)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-alive:
				if !t.Stop() {
					<-t.C
				}
				t.Reset(proto.StreamDeadAfter)
			case <-t.C:
				c.logf("[control] stream silent for %s; reconnecting", proto.StreamDeadAfter)
				resp.Body.Close()
				return
			}
		}
	}()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 256<<10), 64<<20)
	for sc.Scan() {
		select {
		case alive <- struct{}{}:
		default:
		}
		var f Frame
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			return fmt.Errorf("bad stream frame: %w", err)
		}
		if f.LoggedOut {
			return ErrLoggedOut
		}
		if f.KeepAlive && f.NetMap == nil && f.UserView == nil {
			continue
		}
		fn(f)
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return &Error{Offline: true, Err: err}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &Error{Offline: true, Err: io.EOF}
}

// TLSConfigFor is exposed for tests that run a pinned server.
func TLSConfigFor(pin string) (*tls.Config, error) { return proto.PinnedTLSConfig(pin) }
