package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
)

// The admin socket is how the server's operator talks to a running server.
// It is a Unix socket in the data directory, mode 0600 in a 0700 directory,
// so only the user the server runs as (and root) can reach it: it is not a
// network interface, and nothing on it is reachable from outside the
// machine. Through it, deneb-control approve logs a seed test account in
// on a waiting device when no one is at a browser, and deneb-control logins
// lists the devices waiting.
//
// It cannot do anything the operator could not do anyway: they can read
// the seed's verification codes and the database.

// AdminSocketName is the admin socket's file name in the data directory.
const AdminSocketName = "admin.sock"

// AdminLogin is one device waiting for someone to log in on it.
type AdminLogin struct {
	Code        string    `json:"code"`
	Hostname    string    `json:"hostname"`
	DisplayName string    `json:"displayName,omitempty"`
	OS          proto.OS  `json:"os"`
	From        string    `json:"from"` // the address its login request came from
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
}

// AdminApproveRequest approves the waiting device whose login code (or
// login link) is Login for the seed account Email.
type AdminApproveRequest struct {
	Login string `json:"login"`
	Email string `json:"email"`
}

// AdminApproveResult is the device that was logged in.
type AdminApproveResult struct {
	Device   proto.DeviceID `json:"device"`
	Email    string         `json:"email"`
	Hostname string         `json:"hostname"`
	Name     string         `json:"name"` // what people see
	OS       proto.OS       `json:"os"`
	From     string         `json:"from"`
	Address  string         `json:"address"`
}

// ServeAdmin serves the admin socket at path until ctx ends.
func (s *Server) ServeAdmin(ctx context.Context, path string) error {
	// A socket file left by a server that died would make Listen fail.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /logins", s.handleAdminLogins)
	m.HandleFunc("POST /approve", s.handleAdminApprove)
	m.HandleFunc("POST /expire", s.handleAdminExpire)
	m.HandleFunc("GET /seats", s.handleAdminSeats)
	m.HandleFunc("POST /seats", s.handleAdminSetSeats)
	m.HandleFunc("GET /alerts", s.handleAdminAlerts)
	m.HandleFunc("GET /logs", s.handleAdminLogs)
	m.HandleFunc("GET /devices", s.handleAdminDevices)
	m.HandleFunc("POST /device-delete", s.handleAdminDeleteDevice)
	m.HandleFunc("POST /account-delete", s.handleAdminAccountDelete)
	m.HandleFunc("GET /backups", s.handleAdminBackups)
	m.HandleFunc("GET /sessions", s.handleAdminSessions)
	m.HandleFunc("POST /sessions/revoke", s.handleAdminRevokeSessions)
	m.HandleFunc("GET /operators", s.handleAdminOperators)
	m.HandleFunc("POST /operators", s.handleAdminSetOperator)
	s.adminRelayRoutes(m)
	hs := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		hs.Close()
	}()
	s.logf("admin socket at %s", path)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleAdminLogins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.logins.list(s.now()))
}

func (s *Server) handleAdminApprove(w http.ResponseWriter, r *http.Request) {
	var req AdminApproveRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.adminApprove(req.Login, req.Email)
	if err != nil {
		s.logf("admin: approving login %s as %q refused: %v", shortCode(loginCode(req.Login)), normEmail(req.Email), err)
		s.writeErr(w, r, err)
		return
	}
	name := res.Name
	if name == "" {
		name = res.Hostname
	}
	if err := s.recordAlone(adminSocket, "login.approve", string(res.Device), pangu(fmt.Sprintf("批准了「%s」登进 %s", name, res.Email))); err != nil {
		s.logf("admin: the record of approving %s: %v", res.Device, err)
	}
	writeJSON(w, http.StatusOK, res)
}

// AdminExpireRequest moves a device's login expiry to now + In: to see
// "需要重新登录" (In past the renew window) or "登录过期" (In zero or less)
// in a test. A device holding a stream renews itself within seconds, so
// stop its engine first.
type AdminExpireRequest struct {
	Device proto.DeviceID `json:"device"`
	In     string         `json:"in"` // a Go duration: "72h", "-1m"
}

// AdminExpireResult is where the device's login stands now.
type AdminExpireResult struct {
	Device    proto.DeviceID `json:"device"`
	Name      string         `json:"name"`
	ExpiresAt time.Time      `json:"expiresAt"`
	RenewFrom time.Time      `json:"renewFrom"`
	Online    bool           `json:"online"`
}

func (s *Server) handleAdminExpire(w http.ResponseWriter, r *http.Request) {
	var req AdminExpireRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	in, err := time.ParseDuration(req.In)
	if err != nil {
		s.writeErr(w, r, errInvalid("时长不对："+req.In))
		return
	}
	d := s.state.Load().w.devices[req.Device]
	if d == nil || d.LoggedOut {
		s.writeErr(w, r, errNotFound("找不到这台设备，或者它已经登出"))
		return
	}
	at := s.now().Add(in)
	if _, err := s.change("", func(tx *sql.Tx, w *World) error {
		_, err := tx.Exec(`UPDATE devices SET expires_at = ? WHERE id = ?`, ms(at), d.ID)
		return err
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	st := s.state.Load()
	nd := st.w.devices[d.ID]
	online := s.hub.isOnline(d.ID)
	s.logf("admin: login of device %s (%s) now runs out at %s; online %v", d.ID, d.DisplayName, at.UTC().Format(time.RFC3339), online)
	writeJSON(w, http.StatusOK, AdminExpireResult{
		Device: d.ID, Name: d.DisplayName, ExpiresAt: nd.ExpiresAt, RenewFrom: nd.ExpiresAt.Add(-st.w.renewWindow), Online: online,
	})
}

// AdminExpire moves a device's login expiry, through the admin socket at
// path.
func AdminExpire(path string, device proto.DeviceID, in string) (*AdminExpireResult, error) {
	var out AdminExpireResult
	if err := adminCall(path, "POST", "/expire", AdminExpireRequest{Device: device, In: in}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// loginCode takes the code out of a login link, or returns the code.
func loginCode(login string) string {
	if i := strings.LastIndex(login, "c="); i >= 0 {
		return login[i+2:]
	}
	return strings.TrimSpace(login)
}

// adminApprove logs the seed account email in on the waiting device. Only
// seed accounts: they are the test accounts, whose codes the operator holds
// anyway.
func (s *Server) adminApprove(login, email string) (*AdminApproveResult, error) {
	pl := s.logins.byLoginCode(loginCode(login), s.now())
	if pl == nil {
		return nil, errNotFound("没有等着登录的这台设备，或者链接过期了")
	}
	e := normEmail(email)
	if _, ok := s.testCodes[e]; !ok {
		return nil, errForbidden("只能批准给种子文件里的测试账号：" + e + " 不在里面")
	}
	p := s.state.Load().w.byEmail[e]
	if p == nil {
		return nil, errNotFound("没有这个账号：" + e)
	}
	id, err := s.completeLogin(pl, p.ID, "by the admin socket")
	if err != nil {
		return nil, err
	}
	f := pl.facts.Load()
	res := &AdminApproveResult{Device: id, Email: e, Hostname: f.req.Hostname, OS: f.req.OS, From: f.remoteIP}
	if d := s.state.Load().w.devices[id]; d != nil {
		res.Name, res.Address = d.DisplayName, d.IPv4.String()
	}
	return res, nil
}

// list returns the logins waiting, oldest first.
func (t *loginTable) list(now time.Time) []AdminLogin {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	out := []AdminLogin{}
	for _, pl := range t.byCode {
		if !now.Before(pl.expires) {
			continue
		}
		f := pl.facts.Load()
		out = append(out, AdminLogin{
			Code: pl.code, Hostname: f.req.Hostname, DisplayName: f.req.DisplayName, OS: f.req.OS,
			From: f.remoteIP, Created: pl.created.UTC(), Expires: pl.expires.UTC(),
		})
	}
	slices.SortFunc(out, func(a, b AdminLogin) int { return a.Created.Compare(b.Created) })
	return out
}

// adminClient talks to a server's admin socket.
func adminClient(path string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}},
	}
}

// adminCall does one admin request: the host in the URL is ignored, the
// socket is the address.
func adminCall(path, method, route string, in, out any) error {
	var body *strings.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = strings.NewReader(string(b))
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, "http://admin"+route, body)
	if err != nil {
		return err
	}
	resp, err := adminClient(path).Do(req)
	if err != nil {
		return fmt.Errorf("连不上管理套接字 %s（服务器在跑吗？）：%w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var eb proto.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&eb) == nil && eb.Error != nil {
			return errors.New(eb.Error.Message)
		}
		return errors.New(resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// AdminLogins lists the devices waiting for a login, through the admin
// socket at path.
func AdminLogins(path string) ([]AdminLogin, error) {
	var out []AdminLogin
	return out, adminCall(path, "GET", "/logins", nil, &out)
}

// AdminApprove logs the seed account email in on the waiting device whose
// login code or link is login, through the admin socket at path.
func AdminApprove(path, login, email string) (*AdminApproveResult, error) {
	var out AdminApproveResult
	if err := adminCall(path, "POST", "/approve", AdminApproveRequest{Login: login, Email: email}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminSeats is one person's seats, as the operator sees them.
type AdminSeats struct {
	Email string `json:"email"`
	Used  int    `json:"used"`
	// Limit is what holds for them now (0: none); Own, whether it is
	// their own limit (deneb-control seats) rather than the deployment's.
	Limit int  `json:"limit"`
	Own   bool `json:"own,omitempty"`
}

// AdminSeatsRequest sets one person's seat limit, for a test: Limit above
// zero, or -1 for none; 0 gives them the deployment's again.
type AdminSeatsRequest struct {
	Email string `json:"email"`
	Limit int    `json:"limit"`
}

func (s *Server) adminSeats(w *World, p *person) AdminSeats {
	st := w.seats(p.ID, s.now())
	return AdminSeats{Email: p.Email, Used: st.Used, Limit: st.Limit, Own: p.SeatLimit != 0}
}

func (s *Server) handleAdminSeats(w http.ResponseWriter, r *http.Request) {
	st := s.state.Load()
	out := []AdminSeats{}
	for _, p := range st.w.people {
		out = append(out, s.adminSeats(st.w, p))
	}
	slices.SortFunc(out, func(a, b AdminSeats) int { return strings.Compare(a.Email, b.Email) })
	writeJSON(w, http.StatusOK, out)
}

// handleAdminSetSeats changes one person's limit on the running server,
// for a test of "位置满了": no one else's seats change, and nothing that is
// in already is put out; only who may come in next.
func (s *Server) handleAdminSetSeats(w http.ResponseWriter, r *http.Request) {
	var req AdminSeatsRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	e := normEmail(req.Email)
	p := s.state.Load().w.byEmail[e]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个账号："+e))
		return
	}
	// As the console's seats.set: the same rules, and a line of the record.
	if err := s.opsSetSeats(nil, p.ID, req.Limit); err != nil {
		s.writeErr(w, r, err)
		return
	}
	st := s.state.Load()
	writeJSON(w, http.StatusOK, s.adminSeats(st.w, st.w.byEmail[e]))
}

// AdminListSeats lists everyone's seats, through the admin socket at path.
func AdminListSeats(path string) ([]AdminSeats, error) {
	var out []AdminSeats
	return out, adminCall(path, "GET", "/seats", nil, &out)
}

// AdminSetSeats sets one person's seat limit (0: the deployment's again).
func AdminSetSeats(path, email string, limit int) (*AdminSeats, error) {
	var out AdminSeats
	if err := adminCall(path, "POST", "/seats", AdminSeatsRequest{Email: email, Limit: limit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Browser sessions (the Web app's logins), for when an account's code got
// out: the operator logs all its browsers out at once, and their streams
// end within a keep-alive (M7 review).

// AdminSessions is one person's browsers that are logged in.
type AdminSessions struct {
	Email  string    `json:"email"`
	Count  int       `json:"count"`
	Newest time.Time `json:"newest"` // when the latest of them logged in
}

// AdminRevokeSessionsRequest logs every browser of the account with Email
// out.
type AdminRevokeSessionsRequest struct {
	Email string `json:"email"`
}

// AdminRevokeSessionsResult says how many browsers were logged out.
type AdminRevokeSessionsResult struct {
	Revoked int64 `json:"revoked"`
}

func (s *Server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT person, COUNT(*), MAX(created_at) FROM web_sessions WHERE expires_at > ? GROUP BY person`, ms(s.now()))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer rows.Close()
	people := s.state.Load().w.people
	out := []AdminSessions{}
	for rows.Next() {
		var p proto.PersonID
		var a AdminSessions
		var newest int64
		if err := rows.Scan(&p, &a.Count, &newest); err != nil {
			s.writeErr(w, r, err)
			return
		}
		if pp := people[p]; pp != nil {
			a.Email, a.Newest = pp.Email, fromMS(newest).UTC()
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		s.writeErr(w, r, err)
		return
	}
	slices.SortFunc(out, func(a, b AdminSessions) int { return strings.Compare(a.Email, b.Email) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminRevokeSessions(w http.ResponseWriter, r *http.Request) {
	var req AdminRevokeSessionsRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	e := normEmail(req.Email)
	p := s.state.Load().w.byEmail[e]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个账号："+e))
		return
	}
	n, err := s.opsRevokeSessions(nil, p.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminRevokeSessionsResult{Revoked: n})
}

// AdminListSessions lists who is logged in in a browser, through the
// admin socket at path.
func AdminListSessions(path string) ([]AdminSessions, error) {
	var out []AdminSessions
	return out, adminCall(path, "GET", "/sessions", nil, &out)
}

// AdminRevokeSessions logs every browser of the account with email out.
func AdminRevokeSessions(path, email string) (int64, error) {
	var out AdminRevokeSessionsResult
	err := adminCall(path, "POST", "/sessions/revoke", AdminRevokeSessionsRequest{Email: email}, &out)
	return out.Revoked, err
}

func (s *Server) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	bs, err := s.backups()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

// AdminBackups lists the database's daily copies, newest first, through
// the admin socket at path.
func AdminBackups(path string) ([]Backup, error) {
	var out []Backup
	return out, adminCall(path, "GET", "/backups", nil, &out)
}

// Devices, for the operator: who has which, in what state, and deleting
// one whoever's it is (a test device left behind, an abusive one).

// AdminDevice is one device, as deneb-control devices lists it.
type AdminDevice struct {
	ID          proto.DeviceID `json:"id"`
	Owner       string         `json:"owner"` // the owner's email
	DisplayName string         `json:"displayName"`
	Hostname    string         `json:"hostname"`
	OS          proto.OS       `json:"os"`
	Address     string         `json:"address"`
	// State is "online", "offline", "loggedOut" or "expired".
	State    string    `json:"state"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	Engine   string    `json:"engine,omitempty"`
}

// AdminDeviceDeleteRequest deletes a device, whoever's it is, as
// device.delete does for its owner.
type AdminDeviceDeleteRequest struct {
	Device proto.DeviceID `json:"device"`
}

func adminDevice(w *World, d *device, isOnline online) AdminDevice {
	a := AdminDevice{ID: d.ID, DisplayName: d.DisplayName, Hostname: d.Hostname, OS: d.OS,
		Address: d.IPv4.String(), LastSeen: d.LastSeen.UTC(), Engine: d.EngineVersion}
	if p := w.people[d.Owner]; p != nil {
		a.Owner = p.Email
	}
	switch {
	case d.LoggedOut:
		a.State = "loggedOut"
	case d.expired:
		a.State = "expired"
	case isOnline(d.ID):
		a.State = "online"
	default:
		a.State = "offline"
	}
	return a
}

// handleAdminDevices lists the devices, or (?person=<email>) one person's.
func (s *Server) handleAdminDevices(w http.ResponseWriter, r *http.Request) {
	st := s.state.Load()
	isOnline := s.hub.onlineSnapshot()
	email := normEmail(r.URL.Query().Get("person"))
	out := []AdminDevice{}
	for _, d := range st.w.devices {
		a := adminDevice(st.w, d, isOnline)
		if email == "" || a.Owner == email {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b AdminDevice) int {
		if c := strings.Compare(a.Owner, b.Owner); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	var req AdminDeviceDeleteRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	gone, err := s.opsDeleteDevice(nil, req.Device)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("admin: device %s (%q, %s) of %s deleted; version %d", gone.ID, gone.DisplayName, gone.State, gone.Owner, s.state.Load().v)
	writeJSON(w, http.StatusOK, gone)
}

// AdminDevices lists the devices (email: one person's), through the admin
// socket at path.
func AdminDevices(path, email string) ([]AdminDevice, error) {
	var out []AdminDevice
	q := ""
	if email != "" {
		q = "?person=" + url.QueryEscape(email)
	}
	return out, adminCall(path, "GET", "/devices"+q, nil, &out)
}

// AdminDeleteDevice deletes a device, whoever's it is, and says what it was.
func AdminDeleteDevice(path string, id proto.DeviceID) (*AdminDevice, error) {
	var out AdminDevice
	if err := adminCall(path, "POST", "/device-delete", AdminDeviceDeleteRequest{Device: id}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminAlerts are the problems firing now and the last alerts, sent or
// not (deneb-control alerts).
type AdminAlerts struct {
	Active []alert.Alert  `json:"active"`
	Recent []alert.Record `json:"recent"`
}

func (s *Server) handleAdminAlerts(w http.ResponseWriter, r *http.Request) {
	active, recent := s.Alerts()
	if active == nil {
		active = []alert.Alert{}
	}
	if recent == nil {
		recent = []alert.Record{}
	}
	writeJSON(w, http.StatusOK, AdminAlerts{Active: active, Recent: recent})
}

// AdminListAlerts lists the alerts, through the admin socket at path.
func AdminListAlerts(path string) (*AdminAlerts, error) {
	var out AdminAlerts
	if err := adminCall(path, "GET", "/alerts", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
