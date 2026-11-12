package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// The Web API (/api/v1; the plan m4 §14): the device API's JSON for a
// browser. Logging in takes an email and its code, as the login pages do,
// and gives a session cookie; the session is a person, with no device.

// webSessionLife is how long a browser stays logged in.
const webSessionLife = 30 * 24 * time.Hour

func (s *Server) webAPIRoutes(m *http.ServeMux) {
	// Cross-site posts are refused, and every post is JSON, so a form on
	// another site cannot act with someone's cookie.
	cop := http.NewCrossOriginProtection()
	if s.cfg.WebURL != "" {
		// deneb-web passes requests on with this server's Host: a browser
		// that sends no Sec-Fetch-Site (Safari before 16.4) is checked by
		// its Origin, the Web version's, which matches no Host here.
		if err := cop.AddTrustedOrigin(s.cfg.WebURL); err != nil {
			s.logf("web: %s is no origin: %v", s.cfg.WebURL, err)
		}
	}
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(jsonOnly(h)) }
	m.Handle("POST "+proto.PathWebLogin, post(s.handleWebLogin))
	m.Handle("POST "+proto.PathWebLoginEmail, post(s.handleWebLoginStep(false)))
	m.Handle("POST "+proto.PathWebLoginCode, post(s.handleWebLoginStep(true)))
	m.Handle("POST "+proto.PathWebLogout, post(s.handleWebLogout))
	m.HandleFunc("GET "+proto.PathWebUserView, s.web(s.handleWebUserView))
	m.HandleFunc("GET "+proto.PathWebStream, s.web(s.handleWebStream))
	m.Handle("POST "+proto.PathWebOps, post(s.web(s.handleWebOps)))
	m.HandleFunc("GET "+proto.PathWebRelayUsage, s.web(s.handleWebRelayUsage))
	m.HandleFunc("GET "+proto.PathWebOperatorUsage, s.web(s.handleWebOperatorUsage))
	m.Handle("POST "+proto.PathWebInvite, post(s.handleWebInvite))
	m.HandleFunc("GET "+proto.PathWebInvites+"{token}", s.handleWebInviteGet)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.writeErr(w, r, errNotFound("没有这个接口"))
	})
}

// jsonOnly refuses a body that is not JSON: a plain form cannot reach the
// API.
func jsonOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); r.ContentLength != 0 || ct != "" {
			if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, proto.ErrorBody{Error: &proto.Error{Code: proto.ErrInvalid, Message: "要用 application/json"}})
				return
			}
		}
		h(w, r)
	}
}

// webSession is a logged-in browser.
type webSession struct {
	id     string // the hash of the cookie's token
	person proto.PersonID
}

// sessionOf is the request's session, or nil.
func (s *Server) sessionOf(r *http.Request) *webSession {
	c, err := r.Cookie(proto.WebSessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	id := hashSecret(c.Value)
	var person proto.PersonID
	var expires int64
	if err := s.db.QueryRow(`SELECT person, expires_at FROM web_sessions WHERE id = ?`, id).Scan(&person, &expires); err != nil {
		return nil
	}
	if !s.now().Before(fromMS(expires)) || s.state.Load().w.people[person] == nil {
		return nil
	}
	return &webSession{id: id, person: person}
}

// web wraps a handler that needs a logged-in browser.
func (s *Server) web(h func(http.ResponseWriter, *http.Request, *webSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := s.sessionOf(r)
		if ws == nil {
			s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "请先登录"))
			return
		}
		h(w, r, ws)
	}
}

func (s *Server) sessionCookie(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: proto.WebSessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"), SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) handleWebLogin(w http.ResponseWriter, r *http.Request) {
	var req proto.WebLoginRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.webLogin(w, r, req)
}

// handleWebLoginStep is one step of the login on its own path:
// login/email takes the email (no code), login/code the email and code.
func (s *Server) handleWebLoginStep(withCode bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req proto.WebLoginRequest
		if err := readJSON(w, r, &req); err != nil {
			s.writeErr(w, r, err)
			return
		}
		if !withCode {
			req.Code = ""
		} else if strings.TrimSpace(req.Code) == "" {
			s.writeErr(w, r, errInvalid("请输入验证码"))
			return
		}
		s.webLogin(w, r, req)
	}
}

// webLogin answers a login step. Without a code it mails one, if the email
// can log in, and answers the same either way (M7); with one, it logs in:
// the session.
func (s *Server) webLogin(w http.ResponseWriter, r *http.Request, req proto.WebLoginRequest) {
	email := normEmail(req.Email)
	code := strings.TrimSpace(req.Code)
	from := loginFrom{what: "web login", ip: remoteIP(r)}
	if code == "" {
		if err := s.askCode(email, from); err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, proto.WebLoginResult{Step: "code", Account: email,
			CodeLife: int(codeLife / time.Second), ResendAfter: int(codeResend / time.Second)})
		return
	}
	p, err := s.checkCode(email, code, from)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.now()
	token := newRelaySecret() // 256 random bits
	if _, err := s.db.Exec(`INSERT INTO web_sessions (id, person, created_at, expires_at, from_ip) VALUES (?, ?, ?, ?, ?)`,
		hashSecret(token), p, ms(now), ms(now.Add(webSessionLife)), remoteIP(r)); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("web login: %s in a browser at %s", email, remoteIP(r))
	http.SetCookie(w, s.sessionCookie(token, int(webSessionLife.Seconds())))
	writeJSON(w, http.StatusOK, proto.WebLoginResult{Step: "done", Account: email, Person: p})
}

func (s *Server) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	if ws := s.sessionOf(r); ws != nil {
		if _, err := s.db.Exec(`DELETE FROM web_sessions WHERE id = ?`, ws.id); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.logf("web logout: %s", ws.person)
	}
	http.SetCookie(w, s.sessionCookie("", -1))
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleWebUserView(w http.ResponseWriter, r *http.Request, ws *webSession) {
	st := s.state.Load()
	writeJSON(w, http.StatusOK, st.w.userView(ws.person, st.v, s.hub.onlineSnapshot(), s.now()))
}

// handleWebStream is the device stream for a browser: the view, every
// time it changes, and keep-alives.
func (s *Server) handleWebStream(w http.ResponseWriter, r *http.Request, ws *webSession) {
	rc := http.NewResponseController(w)
	sb := &sub{person: ws.person, view: true, notify: make(chan struct{}, 1)}
	if _, ok := s.hub.add(sb); !ok {
		s.writeErr(w, r, errTooManyStreams)
		return
	}
	defer s.hub.remove(sb, nil)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	st := s.state.Load()
	s.hub.prime(sb, st.w, st.v)
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
			// Logged out in another tab, run out, or revoked by the
			// server's admin: the stream ends with the session, within a
			// keep-alive (M7 review).
			if ws := s.sessionOf(r); ws == nil || ws.person != sb.person {
				return
			}
			if !write(keepAlive) {
				return
			}
		}
	}
}

// handleWebOps runs an op as the session's person; a retry with the same
// id gets the first answer, as on the device API.
func (s *Server) handleWebOps(w http.ResponseWriter, r *http.Request, ws *webSession) {
	var op proto.Op
	if err := readJSON(w, r, &op); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if op.ID == "" || len(op.ID) > 128 {
		s.writeErr(w, r, errInvalid("操作缺少 id，或者 id 太长"))
		return
	}
	key := "web\x00" + ws.id + "\x00" + op.ID
	now := s.now()
	s.mu.Lock()
	e, ok := s.ops.get(key, now)
	if !ok {
		e = s.applyOp("", ws.person, op)
		if e.status < 500 {
			s.ops.put(key, e)
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	w.Write(e.body)
}

func (s *Server) handleWebRelayUsage(w http.ResponseWriter, r *http.Request, ws *webSession) {
	u, err := s.relayUsage(s.state.Load().w, ws.person, s.now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleWebInvite previews an invite link, logged in or not: the landing
// page of someone with no Deneb yet.
func (s *Server) handleWebInvite(w http.ResponseWriter, r *http.Request) {
	var req proto.InviteLookup
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var asker proto.PersonID
	if ws := s.sessionOf(r); ws != nil {
		asker = ws.person
	}
	p, err := s.invitePreview(s.state.Load().w, req.Invite, asker)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleWebInviteGet is the preview by the link's token, for the landing
// page: GET /api/v1/invites/<token>.
func (s *Server) handleWebInviteGet(w http.ResponseWriter, r *http.Request) {
	var asker proto.PersonID
	if ws := s.sessionOf(r); ws != nil {
		asker = ws.person
	}
	p, err := s.invitePreview(s.state.Load().w, r.PathValue("token"), asker)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// sweepSessions drops the sessions that ran out.
func (s *Server) sweepSessions(now time.Time) {
	if _, err := s.db.Exec(`DELETE FROM web_sessions WHERE expires_at <= ?`, ms(now)); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("sweep web sessions: %v", err)
	}
}
