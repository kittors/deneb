package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/engine/names"
	"github.com/kittors/deneb/proto"
)

func (e *Engine) newControlClientLocked() (*controlclient.Client, error) {
	o := controlclient.Options{
		URL:           e.controlURLLocked(),
		Pin:           e.controlPinLocked(),
		Machine:       e.machine,
		BindInterface: e.bindIf,
		Logf:          e.logf,
		// A release build talks plain http to this machine only.
		PlainHTTPAnywhere: TestBuild,
		OnBack:            e.kickControl,
	}
	if e.ownControlLocked() {
		// Deneb's own server: all its ways in, the last that got through
		// first (design/plan/control-reachability-20261116.md §9.3).
		o.Entrances, o.Prefer = e.opts.ControlEntrances, e.st.ControlVia
		o.OnEntrance = e.keepEntrance
	}
	cc, err := controlclient.New(o)
	if err != nil {
		return nil, err
	}
	// The entrances a test turned off stay off for the new client.
	for i, s := range cc.Entrances() {
		if e.ctlOff[s.URL] {
			cc.SetEntranceOff(i+1, true)
		}
	}
	return cc, nil
}

// ControlEntrances lists the ways to the control server, for a test on a
// real machine (deneb debug control-entrance).
func (e *Engine) ControlEntrances(ctx context.Context) ([]apitype.ControlEntrance, error) {
	e.mu.Lock()
	cc := e.cc
	e.mu.Unlock()
	if cc == nil {
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "还没有连控制服务器。"}
	}
	var out []apitype.ControlEntrance
	for i, s := range cc.Entrances() {
		out = append(out, apitype.ControlEntrance{N: i + 1, URL: s.URL, Pinned: s.Pinned, InUse: s.InUse, Off: s.Off})
	}
	return out, nil
}

// SetControlEntranceOff turns the control server's entrance n (from 1, in
// the list's order) off for a test on a real machine, as if it did not get
// through, or on again: the engine goes by the next, or back. Not saved: a
// restart turns them all on again
// (design/plan/control-reachability-20261116.md §10.6).
func (e *Engine) SetControlEntranceOff(ctx context.Context, n int, off bool) error {
	e.mu.Lock()
	cc := e.cc
	e.mu.Unlock()
	if cc == nil {
		return &apitype.Error{Code: apitype.ErrInvalid, Message: "还没有连控制服务器。"}
	}
	ents := cc.Entrances()
	if n < 1 || n > len(ents) {
		return &apitype.Error{Code: apitype.ErrInvalid, Message: fmt.Sprintf("没有第 %d 个入口：一共 %d 个。", n, len(ents))}
	}
	e.mu.Lock()
	if off {
		if e.ctlOff == nil {
			e.ctlOff = map[string]bool{}
		}
		e.ctlOff[ents[n-1].URL] = true
	} else {
		delete(e.ctlOff, ents[n-1].URL)
	}
	e.mu.Unlock()
	if err := cc.SetEntranceOff(n, off); err != nil {
		return &apitype.Error{Code: apitype.ErrInvalid, Message: err.Error()}
	}
	state := "on again"
	if off {
		state = "off, for a test"
	}
	e.logf("[control] entrance %d (%s) %s (deneb debug control-entrance)", n, ents[n-1].URL, state)
	return nil
}

// ownControlLocked: the server given or saved is Deneb's own, which has
// several entrances; the address an older release saved counts too.
func (e *Engine) ownControlLocked() bool {
	if len(e.opts.ControlEntrances) == 0 {
		return false
	}
	u := strings.TrimRight(e.controlURLLocked(), "/")
	if slices.Contains(e.opts.OwnControls, u) {
		return true
	}
	return slices.ContainsFunc(e.opts.ControlEntrances, func(x proto.ControlEntrance) bool { return x.URL == u })
}

// keepEntrance keeps the entrance that got through, for the next start.
func (e *Engine) keepEntrance(url string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st.ControlVia != url {
		e.st.ControlVia = url
		e.saveState()
	}
}

// controlPinsLocked are the ways to Deneb's own server by origin, and their
// pins ("" where the system's roots check it): the window's sign-in talks
// to the one the login link names (apitype.State.ControlPins).
func (e *Engine) controlPinsLocked() map[string]string {
	if !e.ownControlLocked() {
		return nil
	}
	pins := map[string]string{}
	for _, x := range e.opts.ControlEntrances {
		pins[x.URL] = x.Pin
	}
	return pins
}

// startControlLocked runs the control loop if it is not running.
func (e *Engine) startControlLocked() {
	if e.ccRunning {
		return
	}
	cc, err := e.newControlClientLocked()
	if err != nil {
		e.problems["control"] = apitype.Problem{Code: "control", Message: err.Error()}
		return
	}
	e.cc = cc
	ctx, cancel := context.WithCancel(e.ctx)
	e.ccCancel = cancel
	e.ccRunning = true
	e.ccGen++
	go e.controlLoop(ctx, cc, e.ccGen)
}

// stopControlLocked stops the control loop; a new one may start at once.
func (e *Engine) stopControlLocked() {
	if e.ccCancel != nil {
		e.ccCancel()
	}
	e.ccRunning = false
	e.ccGen++ // the old loop's exit no longer touches the state
}

func (e *Engine) loginRequest() proto.LoginRequest {
	pub := e.nodePriv.Public().Raw32()
	return proto.LoginRequest{
		NodeKey:       proto.NodePublic(pub),
		DiscoKey:      proto.DiscoPublic(magicsock.DiscoPublic(e.discoPriv)),
		Hostname:      e.opts.Hostname,
		OS:            osName(),
		DisplayName:   e.opts.DisplayName,
		Kind:          e.opts.Kind,
		EngineVersion: e.opts.Version,
		// What peers may rely on (proto.Cap*): this engine drops repeated
		// handshake messages, so a peer may send them both ways.
		Caps: []string{proto.CapHandshakeDedupe},
	}
}

func osName() proto.OS {
	switch runtime.GOOS {
	case "darwin":
		return proto.OS("macos")
	}
	return proto.OS(runtime.GOOS)
}

// controlLoop: log in (at every start, so the server has the current keys),
// wait for the browser if no one is logged in, then hold the stream; when it
// breaks, keep the last network map in force and reconnect with back-off.
func (e *Engine) controlLoop(ctx context.Context, cc *controlclient.Client, gen int) {
	defer func() {
		e.mu.Lock()
		current := e.ccGen == gen
		if current {
			e.ccRunning = false
			e.controlUp = false
		}
		e.mu.Unlock()
		if current {
			e.publishState()
		}
	}()
	backoff := time.Second
	// A network change ends the wait at once: the attempts while the
	// network was gone grew the back-off, and the control server would
	// otherwise wait for its end (test Mac, 20:10: connected 8.4 s after
	// Wi-Fi had its address again).
	sleep := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-e.ctlWake:
			backoff = time.Second
			return true
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
		return true
	}
	for ctx.Err() == nil {
		st, err := cc.Login(ctx, e.loginRequest())
		if err != nil {
			e.noteControlErr(err)
			if !sleep() {
				return
			}
			continue
		}
		for !st.LoggedIn && ctx.Err() == nil {
			if st.LoginURL == "" {
				// No login under way: the link expired (it lasts 15
				// minutes) or the server restarted. Ask for a new one;
				// `deneb login` and the client always show the current link.
				st, err = cc.Login(ctx, e.loginRequest())
				if err != nil {
					e.noteControlErr(err)
					if !sleep() {
						return
					}
					st = &proto.LoginState{}
					continue
				}
				if !st.LoggedIn && st.LoginURL == "" && !sleep() {
					return // the server gave no link: wait, then ask again
				}
				continue
			}
			// The server writes the link with its own address: by the way
			// in that got through instead.
			link := cc.Through(st.LoginURL)
			e.mu.Lock()
			changed := e.loginURL != link || e.browserURL != st.BrowserURL
			// The browser's is the Web version's page, which every browser
			// trusts, by an address of its own: not an entrance.
			e.loginURL, e.browserURL = link, st.BrowserURL
			e.mu.Unlock()
			if changed {
				e.logf("[control] waiting for a login in the browser: %s", st.LoginURL)
				e.publishState()
			}
			st, err = cc.LoginWait(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				e.noteControlErr(err)
				if !sleep() {
					return
				}
				st = &proto.LoginState{}
			}
		}
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		first := !e.st.LoggedIn || e.st.Device != st.Device
		if first && e.st.Device != "" && e.st.Device != st.Device {
			// Another person (or device record) now: the old map is not ours,
			// nor the old view.
			e.nm, e.userView = nil, nil
			e.forgetMap()
		}
		e.st.LoggedIn, e.st.Device, e.st.Person = true, st.Device, st.Person
		e.st.ControlURL, e.st.ControlPin = e.controlURLLocked(), e.controlPinLocked()
		e.loginURL, e.browserURL = "", ""
		e.saveState()
		since := e.version
		e.mu.Unlock()
		if first {
			e.logf("[control] logged in: device %s of %s", st.Device, st.Person)
		}
		e.publishState()
		e.requestReport()

		sctx, kick := context.WithCancel(ctx)
		e.mu.Lock()
		e.streamKick = kick
		e.mu.Unlock()
		err = cc.Stream(sctx, since, e.opts.UserView, func(f controlclient.Frame) {
			backoff = time.Second
			e.applyFrame(f)
		})
		kicked := sctx.Err() != nil && ctx.Err() == nil
		kick()
		if errors.Is(err, controlclient.ErrLoggedOut) {
			e.logf("[control] the device was logged out")
			e.localLogout()
			// Start over in a fresh loop, so a new login link is ready.
			e.mu.Lock()
			e.stopControlLocked()
			e.startControlLocked()
			e.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			return
		}
		if kicked { // the network changed: reconnect now, no back-off
			backoff = time.Second
			continue
		}
		e.noteControlErr(err)
		if !sleep() {
			return
		}
	}
}

func (e *Engine) noteControlErr(err error) {
	e.mu.Lock()
	was := e.controlUp
	e.controlUp = false
	e.mu.Unlock()
	if was || !controlclient.IsOffline(err) {
		e.logf("[control] %v", err)
	}
	if was {
		e.publishState()
	}
}

// applyFrame puts a stream frame in force. A network map and its user view
// share the frame's version, and the client sees both at once.
func (e *Engine) applyFrame(f controlclient.Frame) {
	e.mu.Lock()
	wasUp := e.controlUp
	e.controlUp = true
	v := f.Version
	if f.NetMap != nil {
		if v == 0 {
			v = f.NetMap.Version
		}
		e.applyNetmapLocked(f.NetMap, v)
		if b, err := json.Marshal(f.NetMap); err == nil {
			writeAtomic(e.netmapPath(), b)
		}
		e.saveState()
	}
	if len(f.UserView) > 0 {
		e.userView = f.UserView
		e.bumpVersionLocked(v)
		// After the map, as it came: the two have the frame's version, and a
		// start without the control server takes them back together
		// (loadUserView).
		writeAtomic(e.userViewPath(), f.UserView)
	}
	e.mu.Unlock()
	if !wasUp {
		e.logf("[control] stream up (version %d)", v)
	}
	e.publishState()
	e.wakePaths()
}

// localLogout forgets the login here: tables emptied, map and user view
// dropped. The keys stay; the next login reuses them.
func (e *Engine) localLogout() {
	e.mu.Lock()
	e.st.LoggedIn, e.st.Device, e.st.Person, e.st.Paused = false, "", "", nil
	e.saveState()
	e.forgetMap()
	e.nm, e.userView, e.loginURL, e.browserURL = nil, nil, "", ""
	e.f.SetPolicy(&filter.Policy{})
	e.ms.SetPeers(nil)
	e.configureWGLocked(nil)
	e.names.Store(names.NewTable("", nil))
	e.mu.Unlock()
	e.publishState()
}

// requestReport asks the report loop to tell the server about this device.
func (e *Engine) requestReport() {
	select {
	case e.reportCh <- struct{}{}:
	default:
	}
}

// reportLoop sends the endpoints, the relays (those it is on, those it
// could not use) and paused scopes whenever they change, a moment after
// the last change, and retries until it lands.
func (e *Engine) reportLoop() {
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.reportCh:
		case <-retry.C:
		}
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(300 * time.Millisecond): // let a burst of changes settle
		}
		e.mu.Lock()
		cc, ok := e.cc, e.st.LoggedIn && e.ccRunning
		paused := slices.Clone(e.st.Paused)
		e.mu.Unlock()
		if !ok || cc == nil {
			continue
		}
		r := proto.Report{Endpoints: e.ms.Endpoints(), HomeRelay: e.ms.HomeRelay(), Relays: e.ms.ResidentRelays(), Paused: paused,
			RelayFailures: e.ms.FailedRelays()}
		if r.Paused == nil {
			r.Paused = []proto.ScopeID{}
		}
		b, _ := json.Marshal(r)
		e.mu.Lock()
		same := string(b) == e.lastReport && e.controlUp
		e.mu.Unlock()
		if same {
			continue
		}
		ctx, cancel := context.WithTimeout(e.ctx, 15*time.Second)
		_, err := cc.Report(ctx, r)
		cancel()
		if err != nil {
			e.logf("[control] report: %v (will retry)", err)
			retry.Reset(3 * time.Second)
			continue
		}
		e.mu.Lock()
		e.lastReport = string(b)
		e.mu.Unlock()
	}
}

// waitVersion waits until the engine enforces version v, or timeout.
func (e *Engine) waitVersion(ctx context.Context, v uint64, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		e.mu.Lock()
		cur, ch := e.version, e.versionCh
		e.mu.Unlock()
		if cur >= v {
			return true
		}
		select {
		case <-ch:
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}
