package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/names"
	"github.com/kittors/deneb/proto"
)

var _ localapi.Backend = (*Engine)(nil)

func (e *Engine) EngineVersion() string { return e.opts.Version }

// Shutdown stops a user-space engine (POST /v1/shutdown): the window's,
// when the system service takes its place. A kernel-mode engine is the
// system's to stop: the service manager, launchd or systemd.
func (e *Engine) Shutdown(ctx context.Context) error {
	if e.opts.Mode == apitype.ModeKernel {
		return &apitype.Error{Code: apitype.ErrInvalid, Message: "系统服务里的引擎由系统来停"}
	}
	e.logf("stop asked for through the local API")
	e.requestStop()
	return nil
}

// UpdateEngine replaces the engine with the executable at path, when it
// runs as a system service that can (Options.SelfUpdate).
func (e *Engine) UpdateEngine(ctx context.Context, path string) apitype.UpdateResult {
	if e.opts.SelfUpdate == nil {
		return apitype.UpdateResult{Result: apitype.UpdateNotService, Message: "这个引擎不是系统服务：重启它就是更新"}
	}
	return e.opts.SelfUpdate(ctx, path)
}

// State builds a snapshot for the local API.
func (e *Engine) State() *apitype.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateLocked()
}

func (e *Engine) stateLocked() *apitype.State {
	s := &apitype.State{
		Version:          e.version,
		LoggedIn:         e.st.LoggedIn,
		LoginURL:         e.loginURL,
		User:             apitype.PersonID(e.st.Person),
		SelfDeviceID:     apitype.DeviceID(e.st.Device),
		ControlConnected: e.controlUp,
		ControlURL:       e.controlURLLocked(),
		Mode:             e.opts.Mode,
		Scopes:           []apitype.Scope{},
		Peers:            []apitype.Peer{},
		Relays:           []apitype.Relay{},
		Paths:            []apitype.PeerPath{},
		Usage:            usesFor(e.nm, e.f.Usage()),
		Using:            usingFor(e.nm, e.f.Using(), e.pausedLocked),
		UserView:         e.userView,
	}
	bound, _ := e.ms.BoundInterface()
	s.Engine = apitype.EngineInfo{Version: e.opts.Version, ListenPort: e.ms.Port(), BindInterface: bound, Watchers: len(e.subs)}
	if e.stack != nil && e.opts.SOCKS != "" {
		s.Engine.SOCKS = e.opts.SOCKS
	}
	for _, ep := range e.ms.Endpoints() {
		s.Engine.Endpoints = append(s.Engine.Endpoints, ep.String())
	}
	for _, p := range e.problems {
		s.Engine.Problems = append(s.Engine.Problems, p)
	}
	slices.SortFunc(s.Engine.Problems, func(a, b apitype.Problem) int { return strings.Compare(a.Code, b.Code) })
	nm := e.nm
	if nm == nil {
		return s
	}
	self := deviceOf(nm.Self)
	s.Self = &self
	var chosen map[proto.ScopeID]proto.RelayID
	if e.ms != nil {
		chosen = e.ms.ChosenRelays()
	}
	for _, sc := range nm.Scopes {
		as := apitype.Scope{ID: apitype.ScopeID(sc.ID), Name: sc.Name, Paused: e.pausedLocked(sc), Open: []apitype.Service{},
			Kind: string(sc.Kind), NoInbound: sc.NoInbound, Relay: apitype.RelayID(chosen[sc.ID])}
		for _, svc := range sc.Open {
			for _, pr := range svc.Ports {
				for port := uint32(pr.First); port <= uint32(pr.Last) && port-uint32(pr.First) < 64; port++ {
					as.Open = append(as.Open, apitype.Service{Kind: string(svc.Kind), Proto: string(pr.Proto), Port: uint16(port)})
				}
			}
		}
		for _, r := range sc.Relays {
			as.Relays = append(as.Relays, apitype.RelayID(r))
		}
		s.Scopes = append(s.Scopes, as)
	}
	for _, p := range nm.Peers {
		ap := apitype.Peer{Device: deviceOf(p.Node), Online: p.Online, RefusedByPeer: p.RefusedByPeer, RefusedByMe: p.RefusedByMe}
		for _, id := range p.Scopes {
			ap.Scopes = append(ap.Scopes, apitype.ScopeID(id))
		}
		for _, id := range p.PausedScopes {
			ap.PausedScopes = append(ap.PausedScopes, apitype.ScopeID(id))
		}
		s.Peers = append(s.Peers, ap)
		s.Paths = append(s.Paths, e.pathLocked(p))
	}
	names := map[proto.RelayID]string{}
	for _, r := range nm.Relays {
		names[r.ID] = r.Name
	}
	for _, r := range e.ms.Relays() {
		s.Relays = append(s.Relays, apitype.Relay{ID: apitype.RelayID(r.ID), Name: names[r.ID], Connected: r.Up, Home: r.Home})
	}
	return s
}

func deviceOf(n proto.Node) apitype.Device {
	d := apitype.Device{ID: apitype.DeviceID(n.ID), Name: n.Name, DisplayName: n.DisplayName, Owner: apitype.PersonID(n.Owner),
		OwnerName: n.OwnerName, FQDN: n.FQDN, Kind: string(n.Kind), OS: string(n.OS), Addrs: []string{}}
	for _, a := range n.Addrs {
		d.Addrs = append(d.Addrs, a.Addr().String())
	}
	return d
}

// visibleLocked is whether a peer shares a scope this device has not paused.
func (e *Engine) visibleLocked(p proto.Peer) bool {
	for _, id := range p.Scopes {
		for _, s := range e.nm.Scopes {
			if s.ID == id && !e.pausedLocked(s) {
				return true
			}
		}
	}
	return false
}

// pathLocked is the raw path to one peer; the client debounces it.
func (e *Engine) pathLocked(p proto.Peer) apitype.PeerPath {
	pp := apitype.PeerPath{DeviceID: apitype.DeviceID(p.ID)}
	prev, had := e.lastPaths[p.ID]
	keepSince := func(k apitype.PathKind) time.Time {
		if had && prev.Kind == k {
			return prev.Since
		}
		return time.Now()
	}
	switch {
	case !e.visibleLocked(p):
		pp.Kind = apitype.PathPaused
		pp.Since = keepSince(pp.Kind)
		return pp
	case p.RefusedByPeer:
		pp.Kind = apitype.PathRefused
		pp.Since = keepSince(pp.Kind)
		return pp
	}
	st, ok := e.ms.Status(p.NodeKey)
	if !ok {
		pp.Kind = apitype.PathConnecting
		pp.Since = keepSince(pp.Kind)
		return pp
	}
	pp.Since = st.Since
	pp.RTTMs = int(st.RTT.Round(time.Millisecond) / time.Millisecond)
	pp.Loss = st.Loss
	pp.Idle, pp.Reason = st.Idle, st.Reason
	pp.From = msKind(st.Prev)
	switch st.Kind {
	case "direct":
		pp.Kind, pp.Endpoint = apitype.PathDirect, st.Endpoint.String()
	case "relay":
		pp.Kind, pp.Relay = apitype.PathRelay, apitype.RelayID(st.Relay)
	case "connecting":
		pp.Kind = apitype.PathConnecting
	default:
		pp.Kind = apitype.PathUnreachable
		if !p.Online {
			pp.Kind = apitype.PathOffline
		}
		pp.Since = keepSince(pp.Kind)
	}
	return pp
}

// Subscribe registers fn for events.
func (e *Engine) Subscribe(fn func(apitype.Event)) func() {
	e.mu.Lock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
}

func (e *Engine) publish(ev apitype.Event) {
	e.mu.Lock()
	fns := make([]func(apitype.Event), 0, len(e.subs))
	for _, fn := range e.subs {
		fns = append(fns, fn)
	}
	e.mu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}

func (e *Engine) publishState() {
	e.mu.Lock()
	s := e.stateLocked()
	for _, p := range s.Paths {
		e.lastPaths[proto.DeviceID(p.DeviceID)] = p
	}
	e.mu.Unlock()
	e.publish(apitype.Event{Type: apitype.EventState, State: s})
}

func (e *Engine) wakePaths() {
	select {
	case e.pathWake <- struct{}{}:
	default:
	}
}

// pathLoop sends path changes: kinds as they happen (after 100 ms, to
// batch a burst), round-trip times and loss at most every 2 s.
// msKind is magicsock's path kind as the local API names it.
func msKind(k string) apitype.PathKind {
	switch k {
	case "direct":
		return apitype.PathDirect
	case "relay":
		return apitype.PathRelay
	case "connecting":
		return apitype.PathConnecting
	case "unreachable":
		return apitype.PathUnreachable
	}
	return ""
}

func (e *Engine) pathLoop() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		full := false
		select {
		case <-e.ctx.Done():
			return
		case <-e.pathWake:
			time.Sleep(100 * time.Millisecond)
		case <-tick.C:
			full = true
		}
		e.mu.Lock()
		var changed []apitype.PeerPath
		if e.nm != nil {
			for _, p := range e.nm.Peers {
				pp := e.pathLocked(p)
				old, had := e.lastPaths[p.ID]
				if !had || old.Kind != pp.Kind || old.Relay != pp.Relay || old.Endpoint != pp.Endpoint ||
					old.Idle != pp.Idle || old.Reason != pp.Reason ||
					(full && (absDiff(old.RTTMs, pp.RTTMs) >= max(3, old.RTTMs/10) || absF(old.Loss-pp.Loss) >= 0.05)) {
					e.lastPaths[p.ID] = pp
					changed = append(changed, pp)
				}
			}
		}
		var usage, using []apitype.ServiceUse
		if full {
			u, g := usesFor(e.nm, e.f.Usage()), usingFor(e.nm, e.f.Using(), e.pausedLocked)
			if k := usageKey(u) + "|" + usageKey(g); k != e.lastUsage {
				e.lastUsage, usage, using = k, u, g
			}
		}
		e.mu.Unlock()
		if len(changed) > 0 {
			e.publish(apitype.Event{Type: apitype.EventPaths, Paths: changed})
		}
		if usage != nil {
			e.publish(apitype.Event{Type: apitype.EventUsage, Usage: usage, Using: using})
		}
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func absF(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

var errNotLoggedIn = &apitype.Error{Code: apitype.ErrNotLoggedIn, Message: "这台设备还没登录 Deneb。"}

func offlineErr() *apitype.Error {
	return &apitype.Error{Code: apitype.ErrOffline, Message: "连不上 Deneb 服务器，检查网络后再试。"}
}

// apiErr turns a control server error into the local API's.
func apiErr(err error) error {
	if err == nil {
		return nil
	}
	var ce *controlclient.Error
	if errors.As(err, &ce) {
		if ce.Offline {
			return offlineErr()
		}
		code := apitype.ErrorCode(ce.Code)
		if ce.Code == proto.ErrUnauthorized {
			code = apitype.ErrNotLoggedIn
		}
		return &apitype.Error{Code: code, Message: ce.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &apitype.Error{Code: apitype.ErrTimeout, Message: "Deneb 服务器没有及时回应。"}
	}
	return err
}

func (e *Engine) loggedInClient() (*controlclient.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.st.LoggedIn || e.cc == nil {
		return nil, errNotLoggedIn
	}
	return e.cc, nil
}

// Do passes an op to the control server and replies once the engine
// enforces the version the op landed in, so the next State shows it.
func (e *Engine) Do(ctx context.Context, op apitype.OpRequest) (*apitype.OpResult, error) {
	cc, err := e.loggedInClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v, res, err := cc.Op(ctx, op.ID, op.Op, op.Args)
	if err != nil {
		return nil, apiErr(err)
	}
	e.waitVersion(ctx, v, 3*time.Second)
	return &apitype.OpResult{Version: v, Result: res}, nil
}

// Login returns the link to open in a browser, starting the control loop
// if it is not running. Logging in is finished by the control loop.
func (e *Engine) Login(ctx context.Context, req apitype.LoginRequest) (*apitype.LoginResponse, error) {
	e.mu.Lock()
	if e.st.LoggedIn && e.ccRunning {
		e.mu.Unlock()
		return nil, &apitype.Error{Code: apitype.ErrConflict, Message: "这台设备已经登录了。"}
	}
	if req.ControlURL != "" && !e.st.LoggedIn && req.ControlURL != e.controlURLLocked() {
		// A different server for the first login: restart the loop on it.
		e.opts.ControlURL, e.st.ControlURL, e.loginURL = req.ControlURL, "", ""
		e.stopControlLocked()
	}
	if e.controlURLLocked() == "" {
		e.mu.Unlock()
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "没有配置 Deneb 服务器地址。"}
	}
	e.startControlLocked()
	e.mu.Unlock()
	// The control loop asks the server; wait for its link.
	deadline := time.After(15 * time.Second)
	for {
		e.mu.Lock()
		url, in, running := e.loginURL, e.st.LoggedIn, e.ccRunning
		e.mu.Unlock()
		if url != "" {
			return &apitype.LoginResponse{URL: url}, nil
		}
		if in {
			return &apitype.LoginResponse{}, nil // the server already knew this machine
		}
		if !running {
			return nil, offlineErr()
		}
		select {
		case <-deadline:
			return nil, offlineErr()
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (e *Engine) Logout(ctx context.Context) error {
	e.mu.Lock()
	cc := e.cc
	e.mu.Unlock()
	if cc != nil {
		lctx, done := context.WithTimeout(ctx, 10*time.Second)
		if err := cc.Logout(lctx); err != nil {
			e.logf("[control] logout on the server: %v (logging out here anyway)", err)
		}
		done()
	}
	e.mu.Lock()
	e.stopControlLocked()
	e.mu.Unlock()
	e.localLogout()
	// Be ready for the next login at once, as at a fresh start.
	e.mu.Lock()
	if e.controlURLLocked() != "" {
		e.startControlLocked()
	}
	e.mu.Unlock()
	return nil
}

// Pause changes the tables at once, then tells the server.
func (e *Engine) Pause(ctx context.Context, req apitype.PauseRequest) error {
	e.mu.Lock()
	if e.nm == nil {
		e.mu.Unlock()
		return errNotLoggedIn
	}
	id := proto.ScopeID(req.Scope)
	if !slices.ContainsFunc(e.nm.Scopes, func(s proto.Scope) bool { return s.ID == id }) {
		e.mu.Unlock()
		return &apitype.Error{Code: apitype.ErrNotFound, Message: "这台设备不在这个局域网里。"}
	}
	was := slices.Contains(e.st.Paused, id)
	hidden := map[proto.DeviceID]bool{} // before the change
	for _, p := range e.nm.Peers {
		hidden[p.ID] = !e.visibleLocked(p)
	}
	if req.Paused && !was {
		e.st.Paused = append(e.st.Paused, id)
	} else if !req.Paused && was {
		e.st.Paused = slices.DeleteFunc(e.st.Paused, func(s proto.ScopeID) bool { return s == id })
	}
	var back []proto.NodePublic
	if was != req.Paused {
		e.saveState()
		e.applyNetmapLocked(e.nm, 0)
		for _, p := range e.nm.Peers {
			if hidden[p.ID] && e.visibleLocked(p) {
				back = append(back, p.NodeKey)
			}
		}
	}
	e.mu.Unlock()
	// The peers back from the pause kept our WireGuard session, but while
	// we were paused the control server took us out of their maps, and
	// they dropped theirs. Start a new handshake now rather than send into
	// a session they forgot until WireGuard gives up on it 15 s later.
	// Before returning: traffic sent after the resume must wait for the new
	// session (WireGuard stages it) rather than go out on the old one, which
	// the peer no longer has.
	for _, k := range back {
		e.ms.ResetPeer(k)
		e.rekey(k)
	}
	if was != req.Paused {
		e.logf("[engine] scope %s paused=%v", id, req.Paused)
		e.requestReport()
		e.publishState()
		e.wakePaths()
	}
	return nil
}

func (e *Engine) Resolve(ctx context.Context, name string) (*apitype.ResolveResult, error) {
	ent, err := e.resolve(name)
	if err != nil {
		return nil, err
	}
	return &apitype.ResolveResult{Name: name, FQDN: ent.FQDN, Addr: ent.Addr.String(), Device: apitype.DeviceID(ent.Device)}, nil
}

func (e *Engine) resolve(name string) (names.Entry, error) {
	ent, err := e.names.Load().Resolve(name)
	var amb *names.AmbiguousError
	var nf *names.NotFoundError
	switch {
	case err == nil:
		return ent, nil
	case errors.As(err, &amb):
		ae := &apitype.Error{Code: apitype.ErrAmbiguous, Message: fmt.Sprintf("有 %d 台设备叫 %s，请写全名。", len(amb.Candidates), name)}
		for _, c := range amb.Candidates {
			ca := apitype.Candidate{Device: apitype.DeviceID(c.Device), FQDN: c.FQDN, Addr: c.Addr.String()}
			for _, s := range c.Scopes {
				ca.Scopes = append(ca.Scopes, apitype.ScopeID(s))
			}
			ae.Candidates = append(ae.Candidates, ca)
		}
		return names.Entry{}, ae
	case errors.As(err, &nf):
		return names.Entry{}, &apitype.Error{Code: apitype.ErrNotFound, Message: fmt.Sprintf("在你的局域网里找不到 %s。", name)}
	}
	return names.Entry{}, err
}

// peerByAddr finds the network map's peer with this overlay address.
func (e *Engine) peerByAddr(a netip.Addr) (proto.Peer, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.nm == nil {
		return proto.Peer{}, false
	}
	for _, p := range e.nm.Peers {
		if firstV4(p.Addrs) == a {
			return p, true
		}
	}
	return proto.Peer{}, false
}

func (e *Engine) UserView(ctx context.Context) (json.RawMessage, error) {
	e.mu.Lock()
	uv := e.userView
	e.mu.Unlock()
	if len(uv) > 0 {
		return uv, nil
	}
	cc, err := e.loggedInClient()
	if err != nil {
		return nil, err
	}
	raw, err := cc.UserView(ctx)
	return raw, apiErr(err)
}

// RelayUsage passes the control server's proto.RelayUsage on: the client
// never talks to the control server itself (M4, the relay cards).
func (e *Engine) RelayUsage(ctx context.Context) (json.RawMessage, error) {
	cc, err := e.loggedInClient()
	if err != nil {
		return nil, err
	}
	raw, err := cc.RelayUsage(ctx)
	return raw, apiErr(err)
}

func (e *Engine) PreviewInvite(ctx context.Context, invite string) (json.RawMessage, error) {
	cc, err := e.loggedInClient()
	if err != nil {
		return nil, err
	}
	raw, err := cc.PreviewInvite(ctx, invite)
	return raw, apiErr(err)
}
