package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// adminServer starts a production-mode server (no /dev/approve) with its
// admin socket, logging into lines.
func adminServer(t *testing.T, dataDir string, seed Seed, lines *[]string, mu *sync.Mutex) (*Server, *httptest.Server, string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: dataDir, PublicURL: "http://" + ts.Listener.Addr().String(), Seed: seed,
		Logf: func(f string, a ...any) { mu.Lock(); *lines = append(*lines, fmt.Sprintf(f, a...)); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	return s, ts, serveAdmin(t, s)
}

// serveAdmin serves s's admin socket until the test ends, and returns its
// path. The socket goes before the server: cleanups run last first.
func serveAdmin(t *testing.T, s *Server) string {
	t.Helper()
	// A short directory: a Unix socket's path has to fit in about 100
	// bytes, and test directories are long.
	sockDir, err := os.MkdirTemp("", "dca")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, AdminSocketName)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.ServeAdmin(ctx, sock); close(done) }()
	for range 100 {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		<-done
		os.RemoveAll(sockDir)
	})
	return sock
}

func TestAdminApprove(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	dir := t.TempDir()
	s, ts, sock := adminServer(t, dir, DevSeed(), &lines, &mu)

	// Only the server's own user reaches the socket.
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v, %v", fi.Mode(), err)
	}
	// Production mode has no /dev/approve: the socket is the only way round
	// the browser.
	e := newEngine(t, ts.URL, "build", proto.Linux)
	link := startLogin(t, e)
	resp, err := http.Post(ts.URL+"/dev/approve", "application/json", strings.NewReader(`{"login":"`+link+`","email":"kittors@deneb.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("/dev/approve answered %s outside dev mode", resp.Status)
	}

	ls, err := AdminLogins(sock)
	if err != nil || len(ls) != 1 || ls[0].Hostname != "build" || ls[0].Code != codeOf(link) || ls[0].From != "127.0.0.1" {
		t.Fatalf("logins: %+v, %v", ls, err)
	}
	if _, err := AdminApprove(sock, link, "nobody@deneb.test"); err == nil || !strings.Contains(err.Error(), "种子") {
		t.Fatalf("an account outside the seed: %v", err)
	}
	if _, err := AdminApprove(sock, "NOSUCHCODE12", "kittors@deneb.test"); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("an unknown code: %v", err)
	}

	waited := make(chan proto.LoginState, 1)
	go func() {
		var w proto.LoginState
		e.call("POST", proto.PathLoginWait, nil, &w)
		waited <- w
	}()
	res, err := AdminApprove(sock, " "+strings.ToLower(link)+" ", " Kittors@deneb.test")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-waited:
		if !w.LoggedIn || w.Device != res.Device {
			t.Fatalf("the engine heard %+v, approve said %+v", w, res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the engine's wait did not end")
	}
	d := s.state.Load().w.devices[res.Device]
	if d == nil || s.state.Load().w.people[d.Owner].Email != "kittors@deneb.test" || res.Hostname != "build" || res.Address != d.IPv4.String() {
		t.Fatalf("approved %+v; device %+v", res, d)
	}
	if ls, _ := AdminLogins(sock); len(ls) != 0 {
		t.Fatalf("still waiting: %+v", ls)
	}
	if _, err := AdminApprove(sock, link, "kittors@deneb.test"); err == nil {
		t.Fatal("a used code was approved again")
	}

	mu.Lock()
	all := strings.Join(lines, "\n")
	mu.Unlock()
	for _, want := range []string{
		`admin: approving login ` + codeOf(link)[:4] + `… as "nobody@deneb.test" refused`,
		`logged in as kittors@deneb.test by the admin socket`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the log lacks %q:\n%s", want, all)
		}
	}
}

// TestAdminApproveSeedOnly: an account still in the database but taken out
// of the seed is no longer a test account, and cannot be approved for.
func TestAdminApproveSeedOnly(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	dir := t.TempDir()
	seed := DevSeed()
	s, _, _ := adminServer(t, dir, seed, &lines, &mu)
	s.Close()

	seed.Accounts = seed.Accounts[:1] // kittors only
	_, ts, sock := adminServer(t, dir, seed, &lines, &mu)
	e := newEngine(t, ts.URL, "nas", proto.Linux)
	link := startLogin(t, e)
	if _, err := AdminApprove(sock, link, "jie@deneb.test"); err == nil || !strings.Contains(err.Error(), "种子") {
		t.Fatalf("an account no longer in the seed: %v", err)
	}
	if _, err := AdminApprove(sock, link, "kittors@deneb.test"); err != nil {
		t.Fatal(err)
	}
}

// TestAdminExpire moves a login's expiry for a test of "需要重新登录" and
// "登录过期", and device.renew brings it back.
func TestAdminExpire(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	s, ts, sock := adminServer(t, t.TempDir(), DevSeed(), &lines, &mu)
	_ = s
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	nas := newEngine(t, ts.URL, "nas", proto.Linux)
	for _, e := range []*engine{mac, nas} {
		link := startLogin(t, e)
		if _, err := AdminApprove(sock, link, "kittors@deneb.test"); err != nil {
			t.Fatal(err)
		}
		var st proto.LoginState
		e.call("POST", proto.PathLogin, e.loginRequest(), &st)
		e.id = st.Device
	}
	res, err := AdminExpire(sock, nas.id, "72h")
	if err != nil {
		t.Fatal(err)
	}
	if !res.RenewFrom.Before(time.Now()) || time.Until(res.ExpiresAt) < 71*time.Hour {
		t.Fatalf("after expire 72h: %+v", res)
	}
	if dv := deviceView(mac.view(), nas.id); dv == nil || dv.Login.Expired || !dv.Login.RenewFrom.Before(time.Now()) {
		t.Fatalf("the Mac's view of the NAS: %+v", dv)
	}
	if _, err := AdminExpire(sock, nas.id, "-1m"); err != nil {
		t.Fatal(err)
	}
	if dv := deviceView(mac.view(), nas.id); !dv.Login.Expired {
		t.Fatalf("after expire -1m the NAS is not expired: %+v", dv.Login)
	}
	mac.must(proto.OpDeviceRenew, proto.OpArgs{Device: nas.id})
	if dv := deviceView(mac.view(), nas.id); dv.Login.Expired || dv.Login.RenewFrom.Before(time.Now()) {
		t.Fatalf("after renewing: %+v", dv.Login)
	}
	if _, err := AdminExpire(sock, "d_nope", "1h"); err == nil {
		t.Fatal("an unknown device was moved")
	}
	if _, err := AdminExpire(sock, nas.id, "soon"); err == nil {
		t.Fatal("a bad duration passed")
	}
	mu.Lock()
	all := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(all, "admin: login of device "+string(nas.id)) {
		t.Errorf("the log lacks the expire:\n%s", all)
	}
}
