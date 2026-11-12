package control

import (
	"database/sql"
	"encoding/json"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kittors/deneb/proto"
)

// caller is who is calling the device interface.
type caller struct {
	machine proto.MachinePublic
	dev     *device // nil while no one is logged in on the machine
	st      *state
}

// device wraps a device-interface handler: it checks the bearer token and
// finds the machine's device. With needLogin it refuses machines no one is
// logged in on.
func (s *Server) device(h func(http.ResponseWriter, *http.Request, *caller), needLogin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "缺少令牌"))
			return
		}
		mk, err := s.keys.checkToken(strings.TrimSpace(tok), s.now())
		if err != nil {
			s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "令牌无效或已过期"))
			return
		}
		st := s.state.Load()
		c := &caller{machine: mk, st: st}
		if d := st.w.byMachine[mk]; d != nil && d.usable() {
			c.dev = d
		}
		if needLogin && c.dev == nil {
			s.writeErr(w, r, newErr(http.StatusForbidden, proto.ErrNotLoggedIn, "这台设备还没登录"))
			return
		}
		h(w, r, c)
	}
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var req proto.AuthChallengeRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.MachineKey.IsZero() {
		s.writeErr(w, r, errInvalid("缺少机器公钥"))
		return
	}
	c, exp := s.keys.newChallenge(req.MachineKey, s.now())
	writeJSON(w, http.StatusOK, proto.AuthChallenge{Challenge: c, ExpiresAt: exp.UTC()})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	var req proto.AuthTokenRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.now()
	if err := s.keys.checkChallenge(req.MachineKey, req.Challenge, now); err != nil {
		s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "挑战无效或已过期，请重新获取"))
		return
	}
	if !req.MachineKey.VerifyChallenge(req.Challenge, req.Signature) {
		s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "签名不对"))
		return
	}
	// Only now, signed by the machine key, is the challenge used up.
	if err := s.keys.useChallenge(req.MachineKey, req.Challenge, now); err != nil {
		s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "挑战无效或已过期，请重新获取"))
		return
	}
	tok, exp := s.keys.newToken(req.MachineKey, now)
	s.renewLogin(req.MachineKey, now)
	writeJSON(w, http.StatusOK, proto.AuthToken{Token: tok, ExpiresAt: exp.UTC()})
}

// renewLogin moves a device's login expiry forward when it comes back for a
// token: a device in use never runs out. It writes at most about once a day
// per device. A login that already ran out is not renewed here: that takes
// logging in again, or the owner renewing it.
func (s *Server) renewLogin(mk proto.MachinePublic, now time.Time) {
	d := s.state.Load().w.byMachine[mk]
	life := s.cfg.LoginLifetime
	step := min(24*time.Hour, life/10)
	if d == nil || !d.usable() || d.ExpiresAt.Sub(now) > life-step {
		return
	}
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		_, err := tx.Exec(`UPDATE devices SET expires_at = ? WHERE id = ? AND logged_out = 0`, ms(now.Add(life)), d.ID)
		return err
	})
	if err != nil {
		s.logf("renew login of %s: %v", d.ID, err)
	}
}

// checkLoginRequest cleans up what an engine says about its device.
func checkLoginRequest(req *proto.LoginRequest) error {
	if req.NodeKey.IsZero() || req.DiscoKey.IsZero() {
		return errInvalid("缺少节点密钥或探测密钥")
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	if len(req.Hostname) > 255 || !utf8.ValidString(req.Hostname) {
		return errInvalid("主机名太长")
	}
	if req.OS == "" {
		req.OS = proto.OtherOS
	}
	if !req.OS.Valid() {
		return errInvalid("不认识的系统：" + string(req.OS))
	}
	if req.Kind != "" && !req.Kind.Valid() {
		return errInvalid("不认识的设备种类：" + string(req.Kind))
	}
	if req.DisplayName != "" {
		name, err := checkName(req.DisplayName)
		if err != nil {
			return err
		}
		req.DisplayName = name
	}
	if len(req.EngineVersion) > 64 {
		req.EngineVersion = req.EngineVersion[:64]
	}
	req.Caps = checkCaps(req.Caps)
	return nil
}

// maxCaps bounds what one device may claim: the claim goes into the map of
// every peer it has.
const maxCaps = 32

// checkCaps keeps the well-formed capabilities an engine claims, sorted and
// without repeats, so the same claim always makes the same map and an
// engine listing them in another order changes nothing. A malformed name
// is dropped, not refused: a newer engine must not fail to log in over a
// name this server has no rule for. None comes out as nil.
func checkCaps(caps []string) []string {
	var out []string
	for _, c := range caps {
		if validCap(c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > maxCaps {
		out = out[:maxCaps]
	}
	return out
}

// validCap is the format of a capability's name (proto.LoginRequest.Caps).
func validCap(c string) bool {
	if c == "" || len(c) > 64 {
		return false
	}
	for i := 0; i < len(c); i++ {
		switch b := c[i]; {
		case 'a' <= b && b <= 'z', '0' <= b && b <= '9', b == '-', b == '.':
		default:
			return false
		}
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, c *caller) {
	var req proto.LoginRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := checkLoginRequest(&req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if c.dev != nil {
		// Someone is logged in: take the newer keys and facts. A new node
		// key is news for every peer, so it goes out as a new version;
		// so are new capabilities, which peers act on.
		d := c.dev
		if d.NodeKey != req.NodeKey || d.DiscoKey != req.DiscoKey || d.Hostname != req.Hostname ||
			d.OS != req.OS || d.EngineVersion != req.EngineVersion || !slices.Equal(d.Caps, req.Caps) {
			_, err := s.change("", func(tx *sql.Tx, w *World) error {
				return updateDeviceFacts(tx, d.ID, req, time.Time{})
			})
			if err != nil {
				s.writeErr(w, r, keyConflict(err))
				return
			}
		}
		writeJSON(w, http.StatusOK, proto.LoginState{LoggedIn: true, Device: d.ID, Person: d.Owner, LoginExpiresAt: s.state.Load().w.devices[d.ID].ExpiresAt})
		return
	}
	pl, fresh, err := s.logins.start(c.machine, req, remoteIP(r), s.now())
	if err != nil {
		s.logf("login: refused a new link for %q from %s: %v", req.Hostname, remoteIP(r), err)
		s.writeErr(w, r, err)
		return
	}
	if fresh {
		s.logf("login: link %s for %q (%s) from %s, good until %s", shortCode(pl.code), req.Hostname, req.OS, remoteIP(r),
			pl.expires.UTC().Format("15:04:05"))
	}
	writeJSON(w, http.StatusOK, s.loginState(pl))
}

func (s *Server) loginState(pl *pendingLogin) proto.LoginState {
	return proto.LoginState{LoginURL: s.cfg.PublicURL + "/login?c=" + pl.code, ExpiresAt: pl.expires.UTC()}
}

// loginWait is how long POST /device/v1/login/wait holds on.
const loginWait = 25 * time.Second

func (s *Server) handleLoginWait(w http.ResponseWriter, r *http.Request, c *caller) {
	deadline := time.NewTimer(loginWait)
	defer deadline.Stop()
	for {
		if d := s.state.Load().w.byMachine[c.machine]; d != nil && d.usable() {
			writeJSON(w, http.StatusOK, proto.LoginState{LoggedIn: true, Device: d.ID, Person: d.Owner, LoginExpiresAt: d.ExpiresAt})
			return
		}
		pl := s.logins.forMachine(c.machine, s.now())
		if pl == nil {
			// No login under way (it expired, or the server restarted):
			// the engine asks for a new link with POST /device/v1/login.
			writeJSON(w, http.StatusOK, proto.LoginState{})
			return
		}
		select {
		case <-pl.done:
			// Loop: the device is in the new world now.
		case <-deadline.C:
			writeJSON(w, http.StatusOK, s.loginState(pl))
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, c *caller) {
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		// The addresses it reported go with the login (PRIVACY.md); the
		// row stays, so the same person gets its address back.
		_, err := tx.Exec(`UPDATE devices SET logged_out = 1, last_seen = ?, endpoints = '[]' WHERE id = ?`, ms(s.now()), c.dev.ID)
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("device %s (%s) logged out", c.dev.ID, c.dev.Name)
	writeJSON(w, http.StatusOK, proto.LoginState{})
}

// completeLogin logs person p in on a waiting device: the browser part of a
// login, or /dev/approve. how says which, for the log.
func (s *Server) completeLogin(pl *pendingLogin, p proto.PersonID, how string) (proto.DeviceID, error) {
	var id proto.DeviceID
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		now := s.now()
		if w.people[p] == nil {
			return errNotFound("没有这个账号")
		}
		if d := w.byMachine[pl.machine]; d != nil {
			if d.Owner == p {
				// The same person again, after a logout: the device comes
				// back as it was, with its address, LANs and switches.
				id = d.ID
				return updateDeviceFacts(tx, d.ID, pl.facts.Load().req, now.Add(s.cfg.LoginLifetime))
			}
			// Someone else on this machine: the old device goes, with
			// what it opened and whom it refused, and its address cools
			// down before anyone gets it again.
			if err := deleteDevice(tx, d, now); err != nil {
				return err
			}
		}
		var err error
		id, err = insertDevice(tx, w, p, pl, now, s.cfg.LoginLifetime)
		return err
	})
	if err != nil {
		s.logf("login: link %s for %q as %s %s failed: %v", shortCode(pl.code), pl.facts.Load().req.Hostname, p, how, err)
		return "", keyConflict(err)
	}
	s.logins.finish(pl)
	st := s.state.Load()
	if d := st.w.devices[id]; d != nil {
		s.logf("login: device %s (%s, %q) logged in as %s %s", d.ID, d.Name, d.Hostname, st.w.people[p].Email, how)
	}
	return id, nil
}

// deleteDevice takes device d out for good: its place in its LANs (waiting
// ones too), its catalog and what it opened, whom it refused and its
// shares go with its row (ON DELETE CASCADE); its kicks, which name it
// with no key, go here. Its relay usage stays in the month's totals, as
// no one's. Its address cools down before anyone gets it again. Its
// streams end with LoggedOut when the change is published.
func deleteDevice(tx *sql.Tx, d *device, now time.Time) error {
	if _, err := tx.Exec(`DELETE FROM relay_kicks WHERE device = ?`, d.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM devices WHERE id = ?`, d.ID); err != nil {
		return err
	}
	return releaseAddr(tx, d.IPv4, now)
}

// keyConflict turns the database refusing a second device with the same
// node key into words.
func keyConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: devices.node_key") {
		return errConflict("这把节点密钥已经属于另一台设备")
	}
	return err
}

// updateDeviceFacts takes what an engine says about its device. A login
// (expires set) also brings a logged-out or expired device back, with a
// fresh lifetime.
func updateDeviceFacts(tx *sql.Tx, id proto.DeviceID, req proto.LoginRequest, expires time.Time) error {
	q := `UPDATE devices SET node_key = ?, disco_key = ?, hostname = ?, os = ?, engine_version = ?, caps = ?`
	args := []any{req.NodeKey.String(), req.DiscoKey.String(), req.Hostname, req.OS, req.EngineVersion, capsJSON(req.Caps)}
	if !expires.IsZero() {
		q += `, logged_out = 0, expires_at = ?`
		args = append(args, ms(expires))
	}
	_, err := tx.Exec(q+` WHERE id = ?`, append(args, id)...)
	return err
}

func insertDevice(tx *sql.Tx, w *World, owner proto.PersonID, pl *pendingLogin, now time.Time, life time.Duration) (proto.DeviceID, error) {
	req := pl.facts.Load().req
	kind := req.Kind
	if kind == "" {
		kind = guessKind(req.OS, req.Hostname)
	}
	taken := map[string]bool{}
	for _, d := range w.devices {
		if d.Owner == owner && d.MachineKey != pl.machine {
			taken[d.Name] = true
		}
	}
	base := proto.MakeLabel(req.Hostname, string(kind))
	label := base
	for n := 2; taken[label]; n++ {
		label = base + "-" + strconv.Itoa(n)
	}
	display := req.DisplayName
	if display == "" {
		display = strings.TrimSuffix(req.Hostname, ".local")
	}
	if display == "" {
		display = "新设备"
	}
	v4, v6, err := allocAddr(tx, w, now)
	if err != nil {
		return "", err
	}
	id := proto.DeviceID(newID("d"))
	_, err = tx.Exec(`INSERT INTO devices (id, owner, machine_key, node_key, disco_key, name, display_name, kind, os, hostname,
			ipv4, ipv6, engine_version, caps, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, owner, pl.machine.String(), req.NodeKey.String(), req.DiscoKey.String(), label, display, kind, req.OS, req.Hostname,
		v4.String(), v6.String(), req.EngineVersion, capsJSON(req.Caps), ms(now), ms(now.Add(life)))
	return id, err
}

// capsJSON is how the devices table keeps a device's capabilities.
func capsJSON(caps []string) string {
	if len(caps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(caps)
	return string(b)
}

// guessKind picks a device's kind when the engine did not say.
func guessKind(os proto.OS, hostname string) proto.DeviceKind {
	h := strings.ToLower(hostname)
	switch os {
	case proto.MacOS:
		switch {
		case strings.Contains(h, "mini"):
			return proto.KindMini
		case strings.Contains(h, "imac"), strings.Contains(h, "studio"):
			return proto.KindDesktop
		}
		return proto.KindLaptop
	case proto.Windows:
		return proto.KindDesktop
	case proto.Linux:
		return proto.KindServer
	case proto.IOS, proto.Android:
		return proto.KindPhone
	}
	return proto.KindOther
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, c *caller) {
	rc := http.NewResponseController(w)
	sb := &sub{device: c.dev.ID, view: r.URL.Query().Get("userView") == "1", notify: make(chan struct{}, 1)}
	cameOnline, ok := s.hub.add(sb)
	if !ok {
		s.writeErr(w, r, errTooManyStreams)
		return
	}
	if cameOnline {
		s.markOnline(c.dev.ID, true)
	}
	defer s.hub.remove(sb, func(id proto.DeviceID) { s.markOnline(id, false) })

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	st := s.state.Load()
	s.hub.prime(sb, st.w, st.v)
	// A stream can stay up for weeks without a new token: it renews the
	// login itself, now and with each keep-alive (a write at most daily).
	s.renewLogin(c.machine, s.now())

	write := func(b []byte) bool {
		rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := w.Write(append(b, '\n')); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	keepAlive, _ := json.Marshal(proto.StreamFrame{KeepAlive: true})
	tick := time.NewTicker(s.keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sb.notify:
			b, last := sb.take()
			if b == nil {
				continue
			}
			if !write(b) || last {
				return
			}
		case <-tick.C:
			s.renewLogin(c.machine, s.now())
			if !write(keepAlive) {
				return
			}
		}
	}
}

// Who comes and goes. A device's stream opening, or closing past the
// grace, changes its peers' maps and the views of those who see it: a new
// version. The flips are gathered and published together, at most once a
// second, rather than one change each: after a restart every device
// comes back within seconds, and one change each had the server work out
// every stream's frame once per device, the square of the devices (the
// load test, design/plan/m7/control-ops.md §4). A peer hears of a device
// coming or going up to a second late; "online" is a hint anyway.

// defaultPresenceDelay is how long flips gather before they are published
// (Server.presenceDelay; the tests shorten it).
var defaultPresenceDelay = time.Second

type presence struct {
	mu      sync.Mutex
	pending map[proto.DeviceID]bool // device → online, its last flip
	timer   *time.Timer
	closed  bool
}

// markOnline notes a device coming online or going offline, to publish with
// the others that come and go within presenceDelay.
func (s *Server) markOnline(id proto.DeviceID, on bool) {
	p := &s.presence
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.pending == nil {
		p.pending = map[proto.DeviceID]bool{}
	}
	p.pending[id] = on
	if p.timer == nil {
		p.timer = time.AfterFunc(s.presenceDelay, s.flushPresence)
	}
}

// flushPresence publishes the flips gathered so far, in one change. Going
// offline also stamps when the device was last seen.
func (s *Server) flushPresence() {
	p := &s.presence
	p.mu.Lock()
	flips := p.pending
	p.pending, p.timer = nil, nil
	closed := p.closed
	p.mu.Unlock()
	if closed || len(flips) == 0 {
		return
	}
	now := s.now()
	v, err := s.change("", func(tx *sql.Tx, w *World) error {
		for id, on := range flips {
			if on || w.devices[id] == nil {
				continue
			}
			if _, err := tx.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, ms(now), id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logf("publish %d devices coming and going: %v", len(flips), err)
		return
	}
	// Who comes and goes, with what engine: after a restart this is how one
	// sees every device come back.
	st := s.state.Load()
	ids := slices.Sorted(maps.Keys(flips))
	for _, id := range ids {
		d := st.w.devices[id]
		if d == nil {
			continue
		}
		owner := ""
		if p := st.w.people[d.Owner]; p != nil {
			owner = p.Email
		}
		state := "offline"
		if flips[id] {
			state = "online"
		}
		s.logf("device %s (%s, %q, %s) %s, engine %q; version %d", d.ID, d.Name, d.DisplayName, owner, state, d.EngineVersion, v)
	}
}

// stopPresence drops the flips not published yet: the server is closing.
func (s *Server) stopPresence() {
	p := &s.presence
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
}

// maxEndpoints bounds what a report may carry; an engine has a handful.
const maxEndpoints = 32

// maxReportRelays bounds the relays a report may carry: one per scope at
// most, and scopes share most of them.
const maxReportRelays = 16

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request, c *caller) {
	var rep proto.Report
	if err := readJSON(w, r, &rep); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var eps []netip.AddrPort
	for _, ep := range rep.Endpoints {
		if ep.IsValid() && ep.Port() != 0 && !slices.Contains(eps, ep) && len(eps) < maxEndpoints {
			eps = append(eps, ep)
		}
	}
	var paused []proto.ScopeID
	for _, sc := range rep.Paused {
		if l, ok := sc.LAN(); ok && c.st.w.lans[l] != nil && !slices.Contains(paused, sc) {
			paused = append(paused, sc)
		}
	}
	slices.Sort(paused)
	home := rep.HomeRelay
	if home != "" && c.st.w.relays[home] == nil {
		home = ""
	}
	// The relays it is connected to, home first; ones that no longer
	// exist are left out. Whom they let in is the views' business.
	var relays []proto.RelayID
	if home != "" {
		relays = append(relays, home)
	}
	for _, rl := range rep.Relays {
		if c.st.w.relays[rl] != nil && !slices.Contains(relays, rl) && len(relays) < maxReportRelays {
			relays = append(relays, rl)
		}
	}
	if len(rep.Relays) == 0 {
		// An engine from before M4 reports only its home.
		relays = nil
	}
	v, err := s.change(c.dev.ID, func(tx *sql.Tx, w *World) error {
		d := w.devices[c.dev.ID]
		if d == nil {
			return errNotFound("找不到这台设备")
		}
		var was []proto.ScopeID
		for sc := range d.Paused {
			was = append(was, sc)
		}
		slices.Sort(was)
		if slices.Equal(d.Endpoints, eps) && d.HomeRelay == home && slices.Equal(was, paused) && slices.Equal(d.Relays, relays) {
			return errNoChange
		}
		epsJSON, _ := json.Marshal(nonNil(eps))
		pausedJSON, _ := json.Marshal(nonNil(paused))
		relaysJSON, _ := json.Marshal(nonNil(relays))
		_, err := tx.Exec(`UPDATE devices SET endpoints = ?, home_relay = ?, relays = ?, paused = ? WHERE id = ?`,
			string(epsJSON), home, string(relaysJSON), string(pausedJSON), d.ID)
		return err
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.ReportResult{Version: v})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *Server) handleUserView(w http.ResponseWriter, r *http.Request, c *caller) {
	st := c.st
	uv := st.w.userView(c.dev.Owner, st.v, s.hub.onlineSnapshot(), s.now())
	if uv == nil {
		s.writeErr(w, r, errNotFound("没有这个账号"))
		return
	}
	writeJSON(w, http.StatusOK, uv)
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request, c *caller) {
	var req proto.InviteLookup
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.invitePreview(c.st.w, req.Invite, c.dev.Owner)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// invitePreview is what the holder of an invite link may know. asker is
// who asks, or empty for the landing page. An invite made out to someone
// is theirs alone to look at.
func (s *Server) invitePreview(w *World, link string, asker proto.PersonID) (*proto.InvitePreview, error) {
	i, err := findInvite(w, link, asker)
	if err != nil {
		return nil, err
	}
	p := w.preview(i, asker, s.now())
	w.withLAN(&p)
	return &p, nil
}

// findInvite reads an invite out of a link, a token, or (for the person it
// is made out to) its id.
func findInvite(w *World, link string, asker proto.PersonID) (*invite, error) {
	link = strings.TrimSpace(link)
	var i *invite
	if strings.HasPrefix(link, "i_") {
		i = w.invites[proto.InviteID(link)]
		if i == nil || i.Person == "" || i.Person != asker {
			return nil, errNotFound("找不到这个邀请")
		}
		return i, nil
	}
	token, err := proto.ParseInvite(link)
	if err != nil {
		return nil, errInvalid("这个邀请链接不对")
	}
	if i = w.byToken[token]; i == nil {
		return nil, errNotFound("这个邀请链接不存在")
	}
	if i.Person != "" && asker != "" && i.Person != asker {
		return nil, errForbidden("这个邀请是发给别人的")
	}
	return i, nil
}

func (s *Server) handleDevApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Login string `json:"login"` // the login link, or its code
		Email string `json:"email"`
	}
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	code := req.Login
	if i := strings.LastIndex(code, "c="); i >= 0 {
		code = code[i+2:]
	}
	pl := s.logins.byLoginCode(code, s.now())
	if pl == nil {
		s.writeErr(w, r, errNotFound("没有等着登录的这台设备，或者链接过期了"))
		return
	}
	p := s.state.Load().w.byEmail[normEmail(req.Email)]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个账号："+req.Email))
		return
	}
	id, err := s.completeLogin(pl, p.ID, "by /dev/approve")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.LoginState{LoggedIn: true, Device: id, Person: p.ID})
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
