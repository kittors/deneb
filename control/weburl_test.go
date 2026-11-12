package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestWebURL is the Web version going up behind its own name
// (design/plan/m7/web-deploy.md): invite links open it, this server's own
// invite links redirect there, the browser API takes its origin as its own
// (for browsers that send no Sec-Fetch-Site), and a browser's address comes
// from X-Real-IP, set by nginx and passed on by deneb-web.
func TestWebURL(t *testing.T) {
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: DevSeed(), Logf: func(string, ...any) {}, WebURL: "https://deneb.example.com/", TrustLocalProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })

	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	home := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "家"}).Result.LAN
	inv := mac.must(proto.OpLANInvite, proto.OpArgs{LAN: home}).Result.Invite
	token, ok := strings.CutPrefix(inv.URL, "https://deneb.example.com/i/")
	if !ok || token == "" {
		t.Fatalf("the invite link %q does not open the Web version", inv.URL)
	}
	// A device's login link still names this server: devices reach it by
	// its own address and key.
	nas := newEngine(t, ts.URL, "nas", proto.Linux)
	nas.auth()
	var ls proto.LoginState
	if _, err := nas.call("POST", proto.PathLogin, nas.loginRequest(), &ls); err != nil || !strings.HasPrefix(ls.LoginURL, ts.URL+"/login?c=") {
		t.Fatalf("the login link: %q, %v", ls.LoginURL, err)
	}

	// A link sent before: this server sends it on, the token kept.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ts.URL + proto.InvitePath + token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != inv.URL || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("this server's invite link: %s to %q", resp.Status, resp.Header.Get("Location"))
	}

	// A browser with no Sec-Fetch-Site, through deneb-web: its Origin is the
	// Web version's. Another site's is refused as before.
	post := func(origin, realIP string, body string) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+proto.PathWebLogin, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header
	}
	login := `{"email":"kittors@deneb.test","code":"` + DevCode + `"}`
	if st, _ := post("https://evil.example.net", "", login); st != http.StatusForbidden {
		t.Fatalf("a post from another site: %d", st)
	}
	st, h := post("https://deneb.example.com", "203.0.113.9", login)
	if st != http.StatusOK {
		t.Fatalf("a post from the Web version: %d", st)
	}
	// The session remembers the address nginx saw, not deneb-web's.
	var from string
	if err := s.db.QueryRow(`SELECT from_ip FROM web_sessions ORDER BY created_at DESC LIMIT 1`).Scan(&from); err != nil || from != "203.0.113.9" {
		t.Fatalf("the session is from %q (%v); cookie %q", from, err, h.Get("Set-Cookie"))
	}
}

// TestLocalProxy: X-Real-IP counts only on a request from this machine,
// and only when it is an address.
func TestLocalProxy(t *testing.T) {
	var got string
	h := localProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
	for _, c := range []struct{ peer, header, want string }{
		{"127.0.0.1:5000", "203.0.113.9", "203.0.113.9:0"},
		{"[::1]:5000", "2001:db8::9", "[2001:db8::9]:0"},
		{"[::ffff:127.0.0.1]:5000", " 203.0.113.9 ", "203.0.113.9:0"},
		{"198.51.100.7:5000", "203.0.113.9", "198.51.100.7:5000"}, // from outside
		{"127.0.0.1:5000", "203.0.113.9, 198.51.100.1", "127.0.0.1:5000"},
		{"127.0.0.1:5000", "", "127.0.0.1:5000"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.header != "" {
			r.Header.Set("X-Real-IP", c.header)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("from %s with X-Real-IP %q: %s, want %s", c.peer, c.header, got, c.want)
		}
	}
}
