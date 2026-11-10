package control

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// browser is a cookie-keeping client, as a person's browser would be.
type browser struct {
	t *testing.T
	c *http.Client
}

func newBrowser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, c: &http.Client{Jar: jar}}
}

func (b *browser) get(u string) (int, string) {
	b.t.Helper()
	resp, err := b.c.Get(u)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (b *browser) post(u string, form url.Values, header ...string) (int, string) {
	b.t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// startLogin has an engine ask for a login and returns its link.
func startLogin(t *testing.T, e *engine) string {
	e.auth()
	var st proto.LoginState
	if _, err := e.call("POST", proto.PathLogin, e.loginRequest(), &st); err != nil {
		t.Fatal(err)
	}
	return st.LoginURL
}

func codeOf(link string) string { return link[strings.Index(link, "c=")+2:] }

func TestLoginPages(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "kittors-MacBook-Pro.local", proto.MacOS)
	link := startLogin(t, mac)
	c := codeOf(link)
	b := newBrowser(t)

	if _, page := b.get(link); !strings.Contains(page, "登录 Deneb") || !strings.Contains(page, "kittors-MacBook-Pro") || !strings.Contains(page, "macOS") {
		t.Fatalf("login page:\n%s", page)
	}
	if _, page := b.get(ts.URL + "/login?c=NOPENOPENOPE"); !strings.Contains(page, "过期") || !strings.Contains(page, "deneb login") || !strings.Contains(page, "deneb status") {
		t.Fatalf("an unknown link:\n%s", page)
	}
	// An email with no account gets the same code step as one with: the
	// page does not tell who has an account (TestLoginAnswersAlike).
	if _, page := b.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"nobody@deneb.test"}}); !strings.Contains(page, `name="code"`) || strings.Contains(page, "开通测试账号") {
		t.Fatalf("an unknown email:\n%s", page)
	}
	if _, page := b.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"Kittors@deneb.test"}}); !strings.Contains(page, `name="code"`) || !strings.Contains(page, "000000") ||
		!strings.Contains(page, "如果 kittors@deneb.test 能登录 Deneb") || strings.Contains(page, "登录到") {
		t.Fatalf("code step:\n%s", page)
	}
	if _, page := b.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"not an email"}}); !strings.Contains(page, "邮箱的格式不对") || !strings.Contains(page, `name="email"`) {
		t.Fatalf("a malformed email:\n%s", page)
	}
	if _, page := b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"kittors@deneb.test"}, "code": {"123456"}}); !strings.Contains(page, "验证码不对") {
		t.Fatalf("a wrong code:\n%s", page)
	}
	// Another site cannot post the form for the person's browser.
	if status, _ := b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"kittors@deneb.test"}, "code": {DevCode}},
		"Sec-Fetch-Site", "cross-site"); status != http.StatusForbidden {
		t.Fatalf("a cross-site post got %d", status)
	}

	waited := make(chan proto.LoginState, 1)
	go func() {
		var w proto.LoginState
		mac.call("POST", proto.PathLoginWait, nil, &w)
		waited <- w
	}()
	_, page := b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"kittors@deneb.test"}, "code": {DevCode}})
	if !strings.Contains(page, "登录好了") || !strings.Contains(page, "现在在 kittors 名下") {
		t.Fatalf("done step:\n%s", page)
	}
	select {
	case w := <-waited:
		if !w.LoggedIn {
			t.Fatalf("the engine heard %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the engine's wait did not end")
	}
	// The link is spent.
	if _, page := b.get(link); !strings.Contains(page, "过期") {
		t.Fatalf("a used link:\n%s", page)
	}

	// The next device in the same browser asks for an email again: the
	// browser is not always the owner's, so no account is one click away.
	desk := newEngine(t, ts.URL, "DESKTOP-JIE", proto.Windows)
	link2 := startLogin(t, desk)
	_, page = b.get(link2)
	if !strings.Contains(page, `name="email"`) || strings.Contains(page, "kittors") || !strings.Contains(page, "DESKTOP-JIE") {
		t.Fatalf("a second device's login page:\n%s", page)
	}
	b.post(ts.URL+"/login/email", url.Values{"c": {codeOf(link2)}, "email": {"jie@deneb.test"}})
	if _, page := b.post(ts.URL+"/login/code", url.Values{"c": {codeOf(link2)}, "email": {"jie@deneb.test"}, "code": {DevCode}}); !strings.Contains(page, "「DESKTOP-JIE」现在在阿杰名下") {
		t.Fatalf("阿杰's device:\n%s", page)
	}
	if d := s.state.Load().w.byMachine[desk.mk.Public()]; d == nil || s.state.Load().w.people[d.Owner].Email != "jie@deneb.test" {
		t.Fatal("阿杰's desktop is not his")
	}
}

func TestLoginCodeAttempts(t *testing.T) {
	_, ts := newTestServer(t, "")
	e := newEngine(t, ts.URL, "x", proto.Linux)
	c := codeOf(startLogin(t, e))
	b := newBrowser(t)
	for range maxAttempts {
		b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"mia@deneb.test"}, "code": {"999999"}})
	}
	status, page := b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"mia@deneb.test"}, "code": {DevCode}})
	if status != http.StatusTooManyRequests || !strings.Contains(page, "次数太多") {
		t.Fatalf("after %d wrong codes the right one got %d:\n%s", maxAttempts, status, page)
	}
}

func TestInvitePage(t *testing.T) {
	_, ts := newTestServer(t, "")
	jdesk := newEngine(t, ts.URL, "DESKTOP-JIE", proto.Windows)
	jdesk.login("jie@deneb.test")
	lan := jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	inv := jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: lan}).Result.Invite
	token, _ := proto.ParseInvite(inv.URL)
	b := newBrowser(t)

	status, page := b.get(inv.URL)
	for _, want := range []string{"阿杰邀请你加入「周末联机」", `href="deneb://join/` + token + `"`, "deneb lan join " + inv.URL, "前有效"} {
		if status != 200 || !strings.Contains(page, want) {
			t.Fatalf("invite page lacks %q (%d):\n%s", want, status, page)
		}
	}
	if strings.Contains(page, "jie@deneb.test") {
		t.Fatal("the invite page shows the inviter's email")
	}
	if status, page := b.get(ts.URL + "/i/NOSUCHTOKEN22"); status != 404 || !strings.Contains(page, "不存在") {
		t.Fatalf("an unknown invite: %d\n%s", status, page)
	}
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	if _, page := b.get(inv.URL); !strings.Contains(page, "已经有人用过了") || strings.Contains(page, "deneb://") {
		t.Fatalf("a used invite:\n%s", page)
	}
}

func TestPangu(t *testing.T) {
	for in, want := range map[string]string{
		"Mia邀请你加入「周末联机」":    "Mia 邀请你加入「周末联机」",
		"阿杰邀请你加入「周末联机」":     "阿杰邀请你加入「周末联机」",
		"这个邀请11 月 14 日前有效":  "这个邀请 11 月 14 日前有效",
		"你现在是kittors（k@x）。": "你现在是 kittors（k@x）。",
	} {
		if got := pangu(in); got != want {
			t.Errorf("pangu(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLoginLogs checks that every turn of a login leaves a line in the log:
// the link handed out, an unknown email, a wrong code, a dead link, and
// who logged in what, so a failed login can be traced from the server.
func TestLoginLogs(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Seed: DevSeed(),
		Logf: func(f string, a ...any) { mu.Lock(); lines = append(lines, fmt.Sprintf(f, a...)); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	defer ts.Close()
	defer s.Close()

	e := newEngine(t, ts.URL, "hk-155", proto.Linux)
	link := startLogin(t, e)
	c := codeOf(link)
	b := newBrowser(t)
	b.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"nobody@deneb.test"}})
	b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"jie@deneb.test"}, "code": {"111111"}})
	b.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"jie@deneb.test"}, "code": {DevCode}})
	b.get(link)

	mu.Lock()
	all := strings.Join(lines, "\n")
	mu.Unlock()
	for _, want := range []string{
		`login: link ` + c[:4] + `… for "hk-155" (linux) from 127.0.0.1`,
		`login: code asked for "nobody@deneb.test" (code ` + c[:4] + `…, from 127.0.0.1): no account, nothing sent`,
		`login: wrong code for "jie@deneb.test"`,
		`logged in as jie@deneb.test in a browser at 127.0.0.1`,
		`login page: no login waits for code ` + c[:4] + `…`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the log lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, c) {
		t.Error("the log carries a whole login code")
	}
}
