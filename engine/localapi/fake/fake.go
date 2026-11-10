// Package fake is an in-process engine that speaks the real local API with
// made-up data. The desktop client develops and tests against it before (and
// besides) the real engine: same handler, same wire format.
//
//	e := fake.New()
//	stop, _ := e.Listen(filepath.Join(t.TempDir(), "d.sock"))
//	defer stop()
//	e.PushPaths(apitype.PeerPath{DeviceID: "dev-jie-pc", Kind: apitype.PathDirect})
//
// It knows nothing about the user view's shape (that lives in proto/): set
// one with SetUserView, and make ops change it with HandleOps.
package fake

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// Demo ids, the M1 acceptance topology (plan §3 M1).
const (
	Me       apitype.PersonID = "person-kittors"
	Jie      apitype.PersonID = "person-jie"
	ThisMac  apitype.DeviceID = "dev-mac"
	Build    apitype.DeviceID = "dev-build"
	JiePC    apitype.DeviceID = "dev-jie-pc"
	JieNAS   apitype.DeviceID = "dev-jie-nas"
	Studio   apitype.ScopeID  = "lan/studio"
	Weekend  apitype.ScopeID  = "lan/weekend"
	HongKong apitype.RelayID  = "relay-hk"
)

// PreviewFunc answers an invite preview with proto.InvitePreview as JSON,
// or an error such as not_found for a bad link.
type PreviewFunc func(invite string) (json.RawMessage, *apitype.Error)

// OpFunc decides an op. Return a result (Version 0 means "bump it for me")
// or an error; return nil, nil to fall through to the default.
type OpFunc func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error)

// Engine is a fake engine. All methods are safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	preview  PreviewFunc
	st       apitype.State
	subs     map[int]func(apitype.Event)
	nextSub  int
	ops      OpFunc
	seen     map[string]*apitype.OpResult // idempotency: op id -> first answer
	handler  http.Handler
	nextPort uint16
}

// New returns a fake engine, logged in as kittors on this Mac, in two LANs
// with three peers: one direct, one via the relay, one direct.
func New() *Engine {
	now := time.Now()
	e := &Engine{subs: map[int]func(apitype.Event){}, seen: map[string]*apitype.OpResult{}, nextPort: 50100}
	e.st = apitype.State{
		Version:          1,
		LoggedIn:         true,
		User:             Me,
		SelfDeviceID:     ThisMac,
		ControlConnected: true,
		ControlURL:       "https://control.example:8443",
		Mode:             apitype.ModeUserspace,
		Self:             dev(ThisMac, "mac", "这台电脑", Me, "kittors", "10.77.0.1", "computer", "macos"),
		Scopes: []apitype.Scope{
			{ID: Studio, Name: "工作室", Open: []apitype.Service{{Kind: "web", Proto: "tcp", Port: 8080}}, Relays: []apitype.RelayID{HongKong}},
			{ID: Weekend, Name: "周末联机", Open: []apitype.Service{}, Relays: []apitype.RelayID{HongKong}},
		},
		Peers: []apitype.Peer{
			{Device: *dev(Build, "build", "构建服务器", Me, "kittors", "10.77.0.2", "server", "linux"), Scopes: []apitype.ScopeID{Studio}, Online: true},
			{Device: *dev(JiePC, "desktop", "阿杰的台式机", Jie, "jie", "10.77.0.3", "computer", "windows"), Scopes: []apitype.ScopeID{Weekend}, Online: true},
			{Device: *dev(JieNAS, "nas", "阿杰的 NAS", Jie, "jie", "10.77.0.4", "nas", "linux"), Scopes: []apitype.ScopeID{Weekend}, Online: true},
		},
		Relays: []apitype.Relay{{ID: HongKong, Name: "香港", Connected: true, Home: true}},
		Paths: []apitype.PeerPath{
			{DeviceID: Build, Kind: apitype.PathDirect, Endpoint: "203.0.113.5:41700", RTTMs: 46, Since: now.Add(-2 * time.Hour)},
			{DeviceID: JiePC, Kind: apitype.PathRelay, Relay: HongKong, RTTMs: 310, Since: now.Add(-14 * time.Minute)},
			{DeviceID: JieNAS, Kind: apitype.PathDirect, Endpoint: "198.51.100.4:41703", RTTMs: 53, Since: now.Add(-3 * time.Minute)},
		},
		Engine: apitype.EngineInfo{Version: "fake", ListenPort: 41701, BindInterface: "en0", SOCKS: "127.0.0.1:41080", Endpoints: []string{"192.168.1.20:41701"}},
	}
	e.handler = localapi.Handler(backend{e}, nil)
	return e
}

func dev(id apitype.DeviceID, name, display string, owner apitype.PersonID, ownerName, v4, kind, os string) *apitype.Device {
	return &apitype.Device{ID: id, Name: name, DisplayName: display, Owner: owner, OwnerName: ownerName,
		FQDN: name + "." + ownerName + ".deneb.internal", Addrs: []string{v4}, Kind: kind, OS: os}
}

// ServeHTTP serves the local API.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.handler.ServeHTTP(w, r) }

// Listen serves on a Unix socket (named pipe on Windows) until stop.
func (e *Engine) Listen(path string) (stop func(), err error) {
	ln, err := socket.Listen(path, socket.ListenOptions{AllowUID: -1})
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: e}
	go srv.Serve(ln)
	return func() { srv.Close() }, nil
}

// Snapshot returns a deep copy of the current State.
func (e *Engine) Snapshot() *apitype.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.copyLocked()
}

func (e *Engine) copyLocked() *apitype.State {
	b, _ := json.Marshal(e.st)
	var s apitype.State
	json.Unmarshal(b, &s)
	return &s
}

// Update changes the State and pushes it to watchers. It does not bump the
// version; do that in fn if the change stands for a new network map.
func (e *Engine) Update(fn func(*apitype.State)) {
	e.mu.Lock()
	fn(&e.st)
	s := e.copyLocked()
	e.mu.Unlock()
	e.publish(apitype.Event{Type: apitype.EventState, State: s})
}

// SetUserView replaces the forwarded user view; version is the network map
// version it belongs to (0 keeps the current one).
func (e *Engine) SetUserView(raw json.RawMessage, version uint64) {
	e.Update(func(s *apitype.State) {
		s.UserView = append(json.RawMessage(nil), raw...)
		if version != 0 {
			s.Version = version
		}
	})
}

// PushPaths sets the paths of some peers and sends one paths event.
func (e *Engine) PushPaths(paths ...apitype.PeerPath) {
	e.mu.Lock()
	for _, p := range paths {
		if p.Since.IsZero() {
			p.Since = time.Now()
		}
		found := false
		for i := range e.st.Paths {
			if e.st.Paths[i].DeviceID == p.DeviceID {
				e.st.Paths[i], found = p, true
			}
		}
		if !found {
			e.st.Paths = append(e.st.Paths, p)
		}
	}
	e.mu.Unlock()
	e.publish(apitype.Event{Type: apitype.EventPaths, Paths: paths})
}

// HandleOps installs the op handler; the default bumps the version and,
// for lan.invite, returns a made-up link.
func (e *Engine) HandleOps(fn OpFunc) {
	e.mu.Lock()
	e.ops = fn
	e.mu.Unlock()
}

// HandlePreview installs the invite preview handler. Without one, every
// preview answers not_found.
func (e *Engine) HandlePreview(fn PreviewFunc) {
	e.mu.Lock()
	e.preview = fn
	e.mu.Unlock()
}

func (e *Engine) publish(ev apitype.Event) {
	e.mu.Lock()
	subs := make([]func(apitype.Event), 0, len(e.subs))
	for _, fn := range e.subs {
		subs = append(subs, fn)
	}
	e.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
}

// Simulate plays a loop of believable events until ctx ends: the relayed
// peer punches through and falls back, the NAS goes offline and returns,
// round-trip times wobble. Use it for the dev window, not in tests.
func (e *Engine) Simulate(ctx context.Context) {
	steps := []func(){
		func() {
			e.PushPaths(apitype.PeerPath{DeviceID: JiePC, Kind: apitype.PathDirect, Endpoint: "198.51.100.9:41702", RTTMs: 38})
		},
		func() { e.PushPaths(apitype.PeerPath{DeviceID: JieNAS, Kind: apitype.PathOffline}) },
		func() { e.PushPaths(apitype.PeerPath{DeviceID: JieNAS, Kind: apitype.PathConnecting}) },
		func() {
			e.PushPaths(apitype.PeerPath{DeviceID: JieNAS, Kind: apitype.PathRelay, Relay: HongKong, RTTMs: 280})
		},
		func() {
			e.PushPaths(apitype.PeerPath{DeviceID: JieNAS, Kind: apitype.PathDirect, Endpoint: "198.51.100.4:41703", RTTMs: 55})
		},
		func() {
			e.PushPaths(apitype.PeerPath{DeviceID: JiePC, Kind: apitype.PathRelay, Relay: HongKong, RTTMs: 330})
		},
	}
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	jitter := time.NewTicker(2 * time.Second)
	defer jitter.Stop()
	for i := 0; ; {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			steps[i%len(steps)]()
			i++
		case <-jitter.C:
			var ps []apitype.PeerPath
			e.mu.Lock()
			for _, p := range e.st.Paths {
				if p.RTTMs > 0 {
					p.RTTMs = max(1, p.RTTMs+rand.IntN(11)-5)
					ps = append(ps, p)
				}
			}
			e.mu.Unlock()
			e.PushPaths(ps...)
		}
	}
}

// backend adapts Engine to localapi.Backend.
type backend struct{ e *Engine }

func (b backend) EngineVersion() string { return "fake" }

func (b backend) Shutdown(ctx context.Context) error { return nil }

func (b backend) UpdateEngine(ctx context.Context, path string) apitype.UpdateResult {
	return apitype.UpdateResult{Result: apitype.UpdateNotService, Message: "假引擎不是系统服务"}
}

// Diagnostics: a zip with one note, for the window's export to have
// something to save.
func (b backend) Diagnostics(ctx context.Context, w io.Writer) error {
	z := zip.NewWriter(w)
	f, err := z.Create("README.txt")
	if err != nil {
		return err
	}
	fmt.Fprintf(f, "Deneb 假引擎的诊断包（%s）：真引擎在这里放状态、路径、计数和日志。\n", time.Now().Format(time.RFC3339))
	return z.Close()
}

func (b backend) State() *apitype.State { return b.e.Snapshot() }

func (b backend) Subscribe(fn func(apitype.Event)) func() {
	b.e.mu.Lock()
	id := b.e.nextSub
	b.e.nextSub++
	b.e.subs[id] = fn
	b.e.mu.Unlock()
	return func() {
		b.e.mu.Lock()
		delete(b.e.subs, id)
		b.e.mu.Unlock()
	}
}

func (b backend) Do(ctx context.Context, op apitype.OpRequest) (*apitype.OpResult, error) {
	e := b.e
	e.mu.Lock()
	if prev := e.seen[op.ID]; prev != nil && op.ID != "" {
		e.mu.Unlock()
		return prev, nil
	}
	if !e.st.LoggedIn {
		e.mu.Unlock()
		return nil, &apitype.Error{Code: apitype.ErrNotLoggedIn, Message: "还没登录。"}
	}
	if !e.st.ControlConnected {
		e.mu.Unlock()
		return nil, &apitype.Error{Code: apitype.ErrOffline, Message: "连不上 Deneb 服务器，稍后再试。"}
	}
	fn := e.ops
	e.mu.Unlock()
	var res *apitype.OpResult
	if fn != nil {
		r, aerr := fn(op)
		if aerr != nil {
			return nil, aerr
		}
		res = r
	}
	if res == nil {
		res = &apitype.OpResult{}
		if op.Op == "lan.invite" {
			res.Result = json.RawMessage(`{"link":"deneb://invite/fake-token"}`)
		}
	}
	e.mu.Lock()
	if res.Version == 0 {
		e.st.Version++
		res.Version = e.st.Version
	} else if res.Version > e.st.Version {
		e.st.Version = res.Version
	}
	if op.ID != "" {
		e.seen[op.ID] = res
	}
	s := e.copyLocked()
	e.mu.Unlock()
	e.publish(apitype.Event{Type: apitype.EventState, State: s})
	return res, nil
}

func (b backend) Login(ctx context.Context, req apitype.LoginRequest) (*apitype.LoginResponse, error) {
	url := "https://control.example:8443/login/fake-code"
	b.e.Update(func(s *apitype.State) { s.LoginURL = url })
	// Pretend the person finishes in the browser a second later.
	time.AfterFunc(time.Second, func() {
		b.e.Update(func(s *apitype.State) {
			s.LoginURL, s.LoggedIn, s.User, s.SelfDeviceID = "", true, Me, ThisMac
		})
	})
	return &apitype.LoginResponse{URL: url}, nil
}

func (b backend) Logout(ctx context.Context) error {
	b.e.Update(func(s *apitype.State) {
		s.LoggedIn, s.User, s.SelfDeviceID, s.Peers, s.Paths, s.Scopes, s.UserView = false, "", "", nil, nil, nil, nil
	})
	return nil
}

func (b backend) Pause(ctx context.Context, req apitype.PauseRequest) error {
	e := b.e
	e.mu.Lock()
	found := false
	for i := range e.st.Scopes {
		if e.st.Scopes[i].ID == req.Scope {
			e.st.Scopes[i].Paused, found = req.Paused, true
		}
	}
	if !found {
		e.mu.Unlock()
		return &apitype.Error{Code: apitype.ErrNotFound, Message: "这台电脑不在这个局域网里。"}
	}
	paused := map[apitype.ScopeID]bool{}
	for _, sc := range e.st.Scopes {
		paused[sc.ID] = sc.Paused
	}
	var changed []apitype.PeerPath
	for _, p := range e.st.Peers {
		all := true
		for _, id := range p.Scopes {
			if !paused[id] {
				all = false
			}
		}
		for i := range e.st.Paths {
			pp := &e.st.Paths[i]
			if pp.DeviceID != p.ID {
				continue
			}
			switch {
			case all && pp.Kind != apitype.PathPaused:
				*pp = apitype.PeerPath{DeviceID: p.ID, Kind: apitype.PathPaused, Since: time.Now()}
				changed = append(changed, *pp)
			case !all && pp.Kind == apitype.PathPaused:
				*pp = apitype.PeerPath{DeviceID: p.ID, Kind: apitype.PathConnecting, Since: time.Now()}
				changed = append(changed, *pp)
			}
		}
	}
	s := e.copyLocked()
	e.mu.Unlock()
	e.publish(apitype.Event{Type: apitype.EventState, State: s})
	if len(changed) > 0 {
		e.publish(apitype.Event{Type: apitype.EventPaths, Paths: changed})
	}
	return nil
}

func (b backend) Resolve(ctx context.Context, name string) (*apitype.ResolveResult, error) {
	s := b.e.Snapshot()
	q := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(name), "."), ".deneb.internal")
	var hits []apitype.Device
	all := []apitype.Device{*s.Self}
	for _, p := range s.Peers {
		all = append(all, p.Device)
	}
	for _, d := range all {
		if q == d.Name || q == d.Name+"."+d.OwnerName {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 0:
		return nil, &apitype.Error{Code: apitype.ErrNotFound, Message: fmt.Sprintf("找不到 %s。", name)}
	case 1:
		return &apitype.ResolveResult{Name: name, FQDN: hits[0].FQDN, Addr: hits[0].Addrs[0], Device: hits[0].ID}, nil
	}
	ae := &apitype.Error{Code: apitype.ErrAmbiguous, Message: fmt.Sprintf("有 %d 台设备叫 %s。", len(hits), name)}
	for _, d := range hits {
		ae.Candidates = append(ae.Candidates, apitype.Candidate{Device: d.ID, FQDN: d.FQDN, Addr: d.Addrs[0]})
	}
	return nil, ae
}

func (b backend) Ping(ctx context.Context, to string, n int, interval, timeout time.Duration, each func(apitype.PingResult)) (*apitype.PingSummary, error) {
	r, err := b.Resolve(ctx, to)
	if err != nil {
		return nil, err
	}
	s := b.e.Snapshot()
	var path apitype.PeerPath
	for _, p := range s.Paths {
		if p.DeviceID == r.Device {
			path = p
		}
	}
	sum := &apitype.PingSummary{To: to, Addr: r.Addr, Device: r.Device}
	for i := 0; i < n; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(min(interval, 50*time.Millisecond)): // fast: it is a fake
			}
		}
		res := apitype.PingResult{Seq: i}
		switch path.Kind {
		case apitype.PathDirect, apitype.PathRelay:
			res.RTTMs = float64(path.RTTMs) + rand.Float64()*4
			res.Path, res.Endpoint = path.Kind, path.Endpoint
			sum.Received++
			if sum.MinMs == 0 || res.RTTMs < sum.MinMs {
				sum.MinMs = res.RTTMs
			}
			sum.MaxMs = max(sum.MaxMs, res.RTTMs)
			sum.AvgMs += res.RTTMs
		default:
			res.Err = "timeout"
		}
		sum.Sent++
		sum.Results = append(sum.Results, res)
		if each != nil {
			each(res)
		}
	}
	if sum.Received > 0 {
		sum.AvgMs /= float64(sum.Received)
	}
	return sum, nil
}

func (b backend) Services(ctx context.Context) ([]apitype.LocalService, error) {
	return []apitype.LocalService{
		{Kind: "screen", Proto: "tcp", Port: 5900},
		{Kind: "ssh", Proto: "tcp", Port: 22},
		{Kind: "web", Proto: "tcp", Port: 8080},
	}, nil
}

func (b backend) Open(ctx context.Context, req apitype.OpenRequest) (*apitype.OpenResult, error) {
	e := b.e
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.st.Peers {
		if p.ID != req.Device {
			continue
		}
		if e.st.Mode == apitype.ModeKernel {
			return &apitype.OpenResult{Host: p.FQDN, Port: req.Port, Addr: net.JoinHostPort(p.FQDN, fmt.Sprint(req.Port))}, nil
		}
		e.nextPort++
		return &apitype.OpenResult{Host: "127.0.0.1", Port: e.nextPort, Addr: fmt.Sprintf("127.0.0.1:%d", e.nextPort)}, nil
	}
	return nil, &apitype.Error{Code: apitype.ErrNotFound, Message: "看不到这台设备。"}
}

func (b backend) PreviewInvite(ctx context.Context, invite string) (json.RawMessage, error) {
	b.e.mu.Lock()
	fn := b.e.preview
	b.e.mu.Unlock()
	if fn == nil {
		return nil, &apitype.Error{Code: apitype.ErrNotFound, Message: "这个邀请链接无效，或者已经过期。"}
	}
	raw, aerr := fn(invite)
	if aerr != nil {
		return nil, aerr
	}
	return raw, nil
}

// RelayUsage: the fake world's relays carried nothing this month.
func (b backend) RelayUsage(ctx context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"month":"","relays":[]}`), nil
}

func (b backend) UserView(ctx context.Context) (json.RawMessage, error) {
	return b.e.Snapshot().UserView, nil
}
