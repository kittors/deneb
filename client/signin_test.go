package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// loginServer is a control server's login page as the window meets it
// (control/web.go): the email step answers with the code step, the code
// step with done, or the error under the title; a link that ran out with
// its own page.
type loginServer struct {
	mu      sync.Mutex
	code    string // the device's login code the page knows
	right   string // the code that signs in
	expired bool
	posts   []string
}

func (s *loginServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ParseForm()
	s.posts = append(s.posts, r.URL.Path+" "+r.PostForm.Encode())
	page := func(title, errText string) {
		fmt.Fprintf(w, "<html><body><main><h1>%s</h1>", title)
		if errText != "" {
			fmt.Fprintf(w, `<p class="error">%s</p>`, errText)
		}
		fmt.Fprint(w, "</main></body></html>")
	}
	if s.expired || r.PostForm.Get("c") != s.code {
		page("这个登录链接过期了", "")
		return
	}
	switch r.URL.Path {
	case "/login/email":
		if !strings.Contains(r.PostForm.Get("email"), "@") {
			page("登录 Deneb", "邮箱的格式不对")
			return
		}
		page("输入验证码", "")
	case "/login/code":
		if r.PostForm.Get("code") != s.right {
			page("输入验证码", "验证码不对，或者已经用过、过期了")
			return
		}
		page("登录好了", "")
	default:
		http.NotFound(w, r)
	}
}

// newSignInFor is a SignIn to srv, whose pin it knows, logging in with the
// code the engine gives, logins counting the logins it starts.
func newSignInFor(t *testing.T, srv *httptest.Server, code string, logins *int) *SignIn {
	t.Helper()
	pin := proto.Fingerprint(srv.Certificate())
	return &SignIn{
		login: func(context.Context) (string, error) {
			*logins++
			return srv.URL + "/login?c=" + code, nil
		},
		pins:   map[string]string{srv.URL: pin},
		client: pinnedClient,
	}
}

// TestSignIn: the first step's sign in (DESIGN §7.3), to a server whose
// pin the window knows: the code is sent, a wrong one says so, the right
// one signs in; a link that ran out is begun again once; a malformed email
// says what the page says.
func TestSignIn(t *testing.T) {
	ls := &loginServer{code: "QKHAEORXXOMQ", right: "135790"}
	srv := httptest.NewTLSServer(ls)
	defer srv.Close()
	logins := 0
	s := newSignInFor(t, srv, "QKHAEORXXOMQ", &logins)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.SendCode(ctx, "mia@deneb.test"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := s.CheckCode(ctx, "mia@deneb.test", "000000"); !errors.Is(err, ErrWrongCode) {
		t.Errorf("a wrong code: %v", err)
	}
	if err := s.CheckCode(ctx, "mia@deneb.test", "135790"); err != nil {
		t.Errorf("the right code: %v", err)
	}
	if logins != 1 {
		t.Errorf("%d logins started, want 1", logins)
	}
	// Signed in, a new code begins a new login; one that ran out, again.
	ls.mu.Lock()
	ls.expired = true
	ls.mu.Unlock()
	if err := s.SendCode(ctx, "mia@deneb.test"); err == nil || logins != 3 {
		t.Errorf("an expired link: %v, %d logins", err, logins)
	}
	ls.mu.Lock()
	ls.expired = false
	ls.mu.Unlock()
	if err := s.SendCode(ctx, "mia"); err == nil || err.Error() != "邮箱的格式不对" {
		t.Errorf("a malformed email: %v", err)
	}
}

// TestSignInPins: a server whose pin the window does not know is left to
// the browser; one whose key is not the pinned one is refused.
func TestSignInPins(t *testing.T) {
	srv := httptest.NewTLSServer(&loginServer{code: "C", right: "1"})
	defer srv.Close()
	logins := 0
	s := newSignInFor(t, srv, "C", &logins)
	s.pins = map[string]string{}
	if err := s.SendCode(context.Background(), "mia@deneb.test"); !errors.Is(err, ErrUseBrowser) {
		t.Errorf("an unknown server: %v", err)
	}
	s = newSignInFor(t, srv, "C", &logins)
	s.pins[srv.URL] = "sha256:" + strings.Repeat("0", 64)
	if err := s.SendCode(context.Background(), "mia@deneb.test"); err == nil || errors.Is(err, ErrUseBrowser) {
		t.Errorf("a key not pinned: %v", err)
	}
}

// TestProxyNotice: another program's tunnel (DESIGN §6.14): Clash's as M2
// says it; Surge's and v2rayN's where in them, in their own terms (v2rayN
// takes the addresses alone); any other, for a proxy of any kind.
func TestProxyNotice(t *testing.T) {
	rule := "IP-CIDR,10.48.0.0/16,DIRECT"
	cases := []struct{ app, short, rule string }{
		{"Clash", "Clash 可能挡住连接", rule},
		{"Surge", "Surge 可能挡住连接", rule},
		{"v2rayN", "v2rayN 可能挡住连接", "10.48.0.0/16"},
		{"", "代理软件可能挡住连接", "10.48.0.0/16"},
	}
	for _, c := range cases {
		n := proxyNotice("proxy", rule, c.app)
		if n.Short != c.short || n.Rule != c.rule || (c.app != "Clash") != (n.Detail != "") {
			t.Errorf("%q: %+v", c.app, n)
		}
	}
}

// TestRunningProxy: which proxy runs, found among the programs' names in
// the background, and asked again only after half a minute.
func TestRunningProxy(t *testing.T) {
	asked := make(chan struct{}, 4)
	names := []string{"launchd", "verge-mihomo", "Finder"}
	r := &runningProxy{list: func(context.Context) ([]string, error) { asked <- struct{}{}; return names, nil }}
	changed := make(chan struct{}, 4)
	t0 := time.Now()
	if got, ok := r.known(t0, func() { changed <- struct{}{} }); got != "" || ok {
		t.Errorf("known at once: %q %v", got, ok)
	}
	<-changed
	if got, ok := r.known(t0.Add(time.Second), nil); got != "Clash" || !ok {
		t.Errorf("found %q %v, want Clash", got, ok)
	}
	if len(asked) != 1 {
		t.Errorf("asked %d times within half a minute", len(asked))
	}
	names = []string{"Surge"}
	r.known(t0.Add(31*time.Second), func() { changed <- struct{}{} })
	<-changed
	if got, _ := r.known(t0.Add(32*time.Second), nil); got != "Surge" {
		t.Errorf("found %q after Surge started, want Surge", got)
	}
}
