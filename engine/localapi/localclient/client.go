// Package localclient talks to a running engine over its local API. The
// desktop client's liveStore and the `deneb` command line both use it.
package localclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// ErrNotRunning means nothing listens on the socket: the engine is not
// started, or it is listening somewhere else.
var ErrNotRunning = errors.New("deneb engine is not running")

// Client is safe for concurrent use.
type Client struct {
	path    string // the endpoint, unless dynamic
	dynamic bool   // made with New("") and no $DENEB_SOCKET: look each time
	hc      *http.Client
}

// New returns a client for the endpoint at path. "" means the default: the
// path in $DENEB_SOCKET if set; otherwise the client looks again at every
// connection, taking the system daemon's endpoint (a kernel-mode engine)
// when one answers there and this user's own otherwise. So a window follows
// its engine from user-space mode to kernel mode and back without restarting.
func New(path string) *Client {
	c := &Client{path: path}
	if path == "" {
		if p := os.Getenv(socket.EnvPath); p != "" {
			c.path = p
		} else {
			c.dynamic = true
		}
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, _, err := c.dial(ctx)
			return conn, err
		},
		// One engine, a handful of requests: keep it simple and avoid
		// surprises from pooled connections to a restarted engine.
		MaxIdleConns:    2,
		IdleConnTimeout: 30 * time.Second,
		// Looking each time means dialling each time: a pooled connection
		// would keep talking to the engine that answered before.
		DisableKeepAlives: c.dynamic,
	}
	c.hc = &http.Client{Transport: tr}
	return c
}

// dial connects to the engine and says which endpoint answered.
func (c *Client) dial(ctx context.Context) (net.Conn, string, error) {
	if !c.dynamic {
		conn, err := socket.Dial(ctx, c.path)
		return conn, c.path, err
	}
	user := socket.UserPath()
	if sys := socket.SystemPath(); sys != "" && sys != user && socket.Exists(sys) {
		if conn, err := socket.Dial(ctx, sys); err == nil {
			return conn, sys, nil
		}
		// A socket file nobody answers on: a kernel-mode engine that did not
		// stop cleanly. Fall back to this user's engine.
	}
	conn, err := socket.Dial(ctx, user)
	return conn, user, err
}

// Path is the endpoint the client talks to now.
func (c *Client) Path() string {
	if !c.dynamic {
		return c.path
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, p, err := c.dial(ctx)
	if err == nil {
		conn.Close()
		return p
	}
	return socket.UserPath()
}

// spawnPath is where an engine this client starts should listen: the
// client's own endpoint, or this user's when it looks each time (a client
// never starts the root engine).
func (c *Client) spawnPath() string {
	if !c.dynamic {
		return c.path
	}
	return socket.UserPath()
}

// The host part is ignored by the dialer; it only makes valid URLs.
const base = "http://denebd"

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return decodeError(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		*raw = b
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func wrapDialErr(err error) error {
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	return err
}

func decodeError(resp *http.Response) error {
	var eb apitype.ErrorBody
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &eb) == nil && eb.Error != nil {
		return eb.Error
	}
	return &apitype.Error{Code: apitype.ErrInternal, Message: fmt.Sprintf("引擎返回 %s：%s", resp.Status, bytes.TrimSpace(b))}
}

// AsError returns the API error inside err, if there is one.
func AsError(err error) (*apitype.Error, bool) {
	var ae *apitype.Error
	ok := errors.As(err, &ae)
	return ae, ok
}

func (c *Client) Version(ctx context.Context) (*apitype.VersionInfo, error) {
	var v apitype.VersionInfo
	return &v, c.do(ctx, "GET", "/v1/version", nil, &v)
}

func (c *Client) State(ctx context.Context) (*apitype.State, error) {
	var s apitype.State
	return &s, c.do(ctx, "GET", "/v1/state", nil, &s)
}

// Watch calls fn for every event until ctx ends, the engine goes away, or
// fn returns an error (which Watch then returns). The first event is always
// a whole State. Reconnecting is the caller's choice.
func (c *Client) Watch(ctx context.Context, fn func(apitype.Event) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/v1/watch", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<20) // a State with a big user view is one line
	for sc.Scan() {
		var ev apitype.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("bad event from engine: %w", err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.EOF
}

// Do runs one op. An empty op.ID gets a fresh idempotency key; a caller that
// retries must reuse the key it got the first time (NewOpID).
func (c *Client) Do(ctx context.Context, op apitype.OpRequest) (*apitype.OpResult, error) {
	if op.ID == "" {
		op.ID = NewOpID()
	}
	var res apitype.OpResult
	if err := c.do(ctx, "POST", "/v1/ops", op, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// DoArgs is Do with args marshalled from a Go value.
func (c *Client) DoArgs(ctx context.Context, op string, args any) (*apitype.OpResult, error) {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return c.Do(ctx, apitype.OpRequest{Op: op, Args: raw})
}

// NewOpID returns a random idempotency key.
func NewOpID() string {
	var b [12]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Login asks the engine for a login link. The engine finishes the login by
// itself once the person confirms in a browser; watch State.LoggedIn.
func (c *Client) Login(ctx context.Context, controlURL string) (string, error) {
	var res apitype.LoginResponse
	if err := c.do(ctx, "POST", "/v1/login", apitype.LoginRequest{ControlURL: controlURL}, &res); err != nil {
		return "", err
	}
	return res.URL, nil
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "POST", "/v1/logout", struct{}{}, nil)
}

// Pause pauses or resumes one scope on this device only.
func (c *Client) Pause(ctx context.Context, scope apitype.ScopeID, paused bool) error {
	return c.do(ctx, "POST", "/v1/lan/pause", apitype.PauseRequest{Scope: scope, Paused: paused}, nil)
}

func (c *Client) Resolve(ctx context.Context, name string) (*apitype.ResolveResult, error) {
	var res apitype.ResolveResult
	return &res, c.do(ctx, "GET", "/v1/resolve?name="+url.QueryEscape(name), nil, &res)
}

// PingOptions tune Ping; zero values mean the engine's defaults
// (4 probes, 1 s apart, 2 s timeout).
type PingOptions struct {
	N        int
	Interval time.Duration
	Timeout  time.Duration
}

// Ping probes a peer by name, address or device id. each, if not nil, is
// called after every probe as it happens.
func (c *Client) Ping(ctx context.Context, to string, o PingOptions, each func(apitype.PingResult)) (*apitype.PingSummary, error) {
	q := url.Values{"to": {to}}
	if o.N > 0 {
		q.Set("n", strconv.Itoa(o.N))
	}
	if o.Interval > 0 {
		q.Set("interval", o.Interval.String())
	}
	if o.Timeout > 0 {
		q.Set("timeout", o.Timeout.String())
	}
	if each == nil {
		var res apitype.PingSummary
		return &res, c.do(ctx, "GET", "/v1/ping?"+q.Encode(), nil, &res)
	}
	q.Set("stream", "1")
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/v1/ping?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var line apitype.PingLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, err
		}
		if line.Result != nil {
			each(*line.Result)
		}
		if line.Summary != nil {
			return line.Summary, nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.ErrUnexpectedEOF
}

func (c *Client) Services(ctx context.Context) ([]apitype.LocalService, error) {
	var res []apitype.LocalService
	return res, c.do(ctx, "GET", "/v1/services", nil, &res)
}

func (c *Client) Open(ctx context.Context, req apitype.OpenRequest) (*apitype.OpenResult, error) {
	var res apitype.OpenResult
	return &res, c.do(ctx, "POST", "/v1/open", req, &res)
}

// UserView fetches the control server's view for this device's owner. On a
// headless node the engine pulls it from the control server on demand.
func (c *Client) UserView(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.do(ctx, "GET", "/v1/userview", nil, &raw)
}

// PreviewInvite asks what an invite link is for, without accepting it. The
// answer is proto.InvitePreview as JSON. Read-only, so no idempotency key.
func (c *Client) PreviewInvite(ctx context.Context, invite string) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.do(ctx, "POST", "/v1/invite/preview", apitype.InvitePreviewRequest{Invite: invite}, &raw)
}

// RawStream sends a GET and calls fn for each line of the answer: the test
// build's streaming endpoints (/v1/test/probe) use it.
func (c *Client) RawStream(ctx context.Context, path string, fn func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if err := fn(sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Raw sends an arbitrary request; the test build's /v1/test/... endpoints
// use it.
func (c *Client) Raw(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, body, out)
}
