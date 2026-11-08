package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

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
	writeJSON(w, http.StatusOK, res)
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
	if _, ok := s.codes[e]; !ok {
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
	res := &AdminApproveResult{Device: id, Email: e, Hostname: pl.req.Hostname, OS: pl.req.OS, From: pl.remoteIP}
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
		out = append(out, AdminLogin{
			Code: pl.code, Hostname: pl.req.Hostname, DisplayName: pl.req.DisplayName, OS: pl.req.OS,
			From: pl.remoteIP, Created: pl.created.UTC(), Expires: pl.expires.UTC(),
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
