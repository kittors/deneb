package control

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
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
	if err := s.keys.useChallenge(req.MachineKey, req.Challenge, now); err != nil {
		s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "挑战无效或已过期，请重新获取"))
		return
	}
	if !req.MachineKey.VerifyChallenge(req.Challenge, req.Signature) {
		s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "签名不对"))
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
	return nil
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
		// key is news for every peer, so it goes out as a new version.
		d := c.dev
		if d.NodeKey != req.NodeKey || d.DiscoKey != req.DiscoKey || d.Hostname != req.Hostname ||
			d.OS != req.OS || d.EngineVersion != req.EngineVersion {
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
	pl, fresh := s.logins.start(c.machine, req, remoteIP(r), s.now())
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
		_, err := tx.Exec(`UPDATE devices SET logged_out = 1, last_seen = ? WHERE id = ?`, ms(s.now()), c.dev.ID)
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
				return updateDeviceFacts(tx, d.ID, pl.req, now.Add(s.cfg.LoginLifetime))
			}
			// Someone else on this machine: the old device goes, with
			// what it opened and whom it refused, and its address cools
			// down before anyone gets it again.
			if _, err := tx.Exec(`DELETE FROM devices WHERE id = ?`, d.ID); err != nil {
				return err
			}
			if err := releaseAddr(tx, d.IPv4, now); err != nil {
				return err
			}
		}
		var err error
		id, err = insertDevice(tx, w, p, pl, now, s.cfg.LoginLifetime)
		return err
	})
	if err != nil {
		s.logf("login: link %s for %q as %s %s failed: %v", shortCode(pl.code), pl.req.Hostname, p, how, err)
		return "", keyConflict(err)
	}
	s.logins.finish(pl)
	st := s.state.Load()
	if d := st.w.devices[id]; d != nil {
		s.logf("login: device %s (%s, %q) logged in as %s %s", d.ID, d.Name, d.Hostname, st.w.people[p].Email, how)
	}
	return id, nil
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
	q := `UPDATE devices SET node_key = ?, disco_key = ?, hostname = ?, os = ?, engine_version = ?`
	args := []any{req.NodeKey.String(), req.DiscoKey.String(), req.Hostname, req.OS, req.EngineVersion}
	if !expires.IsZero() {
		q += `, logged_out = 0, expires_at = ?`
		args = append(args, ms(expires))
	}
	_, err := tx.Exec(q+` WHERE id = ?`, append(args, id)...)
	return err
}

func insertDevice(tx *sql.Tx, w *World, owner proto.PersonID, pl *pendingLogin, now time.Time, life time.Duration) (proto.DeviceID, error) {
	req := pl.req
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
			ipv4, ipv6, engine_version, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, owner, pl.machine.String(), req.NodeKey.String(), req.DiscoKey.String(), label, display, kind, req.OS, req.Hostname,
		v4.String(), v6.String(), req.EngineVersion, ms(now), ms(now.Add(life)))
	return id, err
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
	if s.hub.add(sb) {
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

// markOnline publishes a device coming online or going offline. Going
// offline also stamps when it was last seen.
func (s *Server) markOnline(id proto.DeviceID, on bool) {
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		if on || w.devices[id] == nil {
			return nil
		}
		_, err := tx.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, ms(s.now()), id)
		return err
	})
	if err != nil {
		s.logf("mark %s online=%v: %v", id, on, err)
	}
}

// maxEndpoints bounds what a report may carry; an engine has a handful.
const maxEndpoints = 32

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
		if slices.Equal(d.Endpoints, eps) && d.HomeRelay == home && slices.Equal(was, paused) {
			return errNoChange
		}
		epsJSON, _ := json.Marshal(nonNil(eps))
		pausedJSON, _ := json.Marshal(nonNil(paused))
		_, err := tx.Exec(`UPDATE devices SET endpoints = ?, home_relay = ?, paused = ? WHERE id = ?`,
			string(epsJSON), home, string(pausedJSON), d.ID)
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
