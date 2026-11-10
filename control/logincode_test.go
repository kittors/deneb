package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// Logging in with a code by mail (M7, design/plan/m7/control-security.md
// §6), against a fake mail server on this machine.

// mailSeed is the development seed and one real account: 小林, whose codes
// go by mail. (example.net is reserved, so no mail could reach it anyway.)
func mailSeed() Seed {
	s := DevSeed()
	s.Accounts = append(s.Accounts, SeedAccount{Email: "lin@example.net", DisplayName: "小林"})
	return s
}

// newMailServer is a development server that mails its codes with m, on a
// clock the test moves.
func newMailServer(t *testing.T, m Mailer, open bool) (*Server, *httptest.Server, *clock) {
	t.Helper()
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: mailSeed(), Now: clk.now, Mailer: m, OpenSignup: open, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	return s, ts, clk
}

// answer is a response as a person's browser gets it, whole.
type answer struct {
	Status int
	Header http.Header
	Body   string
}

// rawPost posts body and returns the whole answer, with every mention of
// email (in any case) replaced, so answers about two emails compare.
func rawPost(t *testing.T, u, contentType, body, email string) answer {
	t.Helper()
	resp, err := http.Post(u, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	h := resp.Header.Clone()
	h.Del("Date")
	h.Del("Content-Length")
	return answer{resp.StatusCode, h, strings.ReplaceAll(string(b), strings.ToLower(email), "EMAIL")}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestMailedLoginCode: 小林 asks for a code in a browser; it comes by mail,
// over TLS with the deployment's account; a wrong one is refused, the
// right one logs him in, once. A device's login page takes a mailed code
// the same way.
func TestMailedLoginCode(t *testing.T) {
	f := newFakeSMTP(t, withStartTLS, withAuth("PLAIN LOGIN"))
	s, ts, _ := newMailServer(t, f.mailer(t), false)
	lin := s.state.Load().w.byEmail["lin@example.net"]

	b := newWebBrowser(t, ts.URL)
	if r := b.login(" Lin@Example.net ", ""); r.Step != "code" || r.Account != "lin@example.net" {
		t.Fatalf("the email step: %+v", r)
	}
	m := f.next(t)
	if !m.TLS || m.User != fakeUser || m.To != "lin@example.net" || m.From != "noreply@example.com" {
		t.Fatalf("the mail: %+v", m)
	}
	code := codeIn(t, m)
	wrong := fmt.Sprintf("%06d", (mustAtoi(code)+1)%1_000_000)
	if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "lin@example.net", Code: wrong}, nil, nil); st != 400 {
		t.Fatalf("a wrong code: %d", st)
	}
	if r := b.login("lin@example.net", code); r.Step != "done" || r.Person != lin.ID {
		t.Fatalf("the right code: %+v", r)
	}
	var v proto.UserView
	if st := b.do("GET", proto.PathWebUserView, nil, &v, nil); st != 200 || v.Me != lin.ID {
		t.Fatalf("logged in: %d %s", st, v.Me)
	}
	// Used once, it works no more.
	if st := newWebBrowser(t, ts.URL).do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "lin@example.net", Code: code}, nil, nil); st != 400 {
		t.Fatalf("the code again: %d", st)
	}
	// Test accounts get no mail, and keep their code.
	b.login("jie@deneb.test", "")
	f.none(t, 200*time.Millisecond)
	if r := newWebBrowser(t, ts.URL).login("jie@deneb.test", DevCode); r.Step != "done" {
		t.Fatalf("a test account: %+v", r)
	}

	// A device's login page.
	nas := newEngine(t, ts.URL, "lin-nas", proto.Linux)
	c := codeOf(startLogin(t, nas))
	pb := newBrowser(t)
	if _, page := pb.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"lin@example.net"}}); !strings.Contains(page, `name="code"`) {
		t.Fatalf("the code step:\n%s", page)
	}
	code = codeIn(t, f.next(t))
	if _, page := pb.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"lin@example.net"}, "code": {code}}); !strings.Contains(page, "「lin-nas」现在在小林名下") {
		t.Fatalf("the done step:\n%s", page)
	}
	if d := s.state.Load().w.byMachine[nas.mk.Public()]; d == nil || d.Owner != lin.ID {
		t.Fatal("the NAS is not 小林's")
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// TestLoginCodeLife: a code works ten minutes. Asking again within a
// minute mails nothing; later, the same code again while it has most of
// its life left, then a new one, which ends the old. One email gets five
// mails an hour at most.
func TestLoginCodeLife(t *testing.T) {
	f := newFakeSMTP(t)
	_, ts, clk := newMailServer(t, f.mailer(t), false)
	b := newWebBrowser(t, ts.URL)
	ask := func() { t.Helper(); b.login("lin@example.net", "") }
	works := func(code string) bool {
		t.Helper()
		var r proto.WebLoginResult
		return newWebBrowser(t, ts.URL).do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "lin@example.net", Code: code}, &r, nil) == 200 && r.Step == "done"
	}

	ask()
	a := codeIn(t, f.next(t))
	ask() // within the minute: nothing
	f.none(t, 200*time.Millisecond)
	clk.add(61 * time.Second)
	ask()
	if again := codeIn(t, f.next(t)); again != a {
		t.Fatalf("a resend with most of the code's life left carries %s, not %s", again, a)
	}
	clk.add(5 * time.Minute) // 6 minutes old: replaced
	ask()
	b2 := codeIn(t, f.next(t))
	if b2 != a && works(a) {
		t.Fatal("the replaced code still works")
	}
	clk.add(codeLife + time.Second)
	if works(b2) {
		t.Fatal("a code past its ten minutes works")
	}
	// The fourth and fifth mails of the hour go; the sixth does not, until
	// the hour has passed.
	ask()
	c := codeIn(t, f.next(t))
	clk.add(61 * time.Second)
	ask()
	codeIn(t, f.next(t))
	clk.add(61 * time.Second)
	ask()
	f.none(t, 200*time.Millisecond)
	clk.add(time.Hour)
	ask()
	d := codeIn(t, f.next(t))
	if !works(d) || (c != d && works(c)) {
		t.Fatal("after the hour, the new code")
	}
}

// TestLoginCodeDropped: after five wrong codes a mailed code is dropped,
// even with time left: only a new one works.
func TestLoginCodeDropped(t *testing.T) {
	lc := newLoginCodes()
	now := time.Now()
	code, why := lc.issue("lin@example.net", now)
	if code == "" || why != "sent" {
		t.Fatalf("issue: %q %q", code, why)
	}
	wrong := fmt.Sprintf("%06d", (mustAtoi(code)+1)%1_000_000)
	for range maxAttempts {
		if lc.check("lin@example.net", wrong, now) {
			t.Fatal("a wrong code checked out")
		}
	}
	if lc.check("lin@example.net", code, now) {
		t.Fatal("the code still works after five wrong ones")
	}
	// Used once: no more.
	code, _ = lc.issue("lin@example.net", now.Add(2*time.Minute))
	if !lc.check("lin@example.net", code, now.Add(2*time.Minute)) || lc.check("lin@example.net", code, now.Add(2*time.Minute)) {
		t.Fatal("a code works once")
	}
	if lc.check("nobody@example.net", noCode, now) {
		t.Fatal("no code matched the placeholder")
	}
}

// TestLoginAnswersAlike: an email with an account that gets codes by mail,
// a test account, and an email with no account get the same answers from
// both entries, byte for byte, and as fast; only the first gets a mail.
// The limits answer alike too.
func TestLoginAnswersAlike(t *testing.T) {
	f := newFakeSMTP(t)
	s, ts, _ := newMailServer(t, f.mailer(t), false)
	emails := []string{"lin@example.net", "kittors@deneb.test", "nobody@example.net"}
	alike := func(what string, as []answer) {
		t.Helper()
		for i := 1; i < len(as); i++ {
			if a, b := jsonOf(as[0]), jsonOf(as[i]); a != b {
				t.Fatalf("%s: %s and %s are told apart:\n%s\n%s", what, emails[0], emails[i], a, b)
			}
		}
	}
	each := func(fn func(e string) answer) []answer {
		var as []answer
		for _, e := range emails {
			as = append(as, fn(e))
		}
		return as
	}
	api := func(path string, req proto.WebLoginRequest) answer {
		return rawPost(t, ts.URL+path, "application/json", jsonOf(req), req.Email)
	}

	// The email step, on the Web, in its two forms.
	for _, path := range []string{proto.PathWebLogin, proto.PathWebLoginEmail} {
		as := each(func(e string) answer { return api(path, proto.WebLoginRequest{Email: strings.ToUpper(e)}) })
		alike("the email step at "+path, as)
		if as[0].Status != 200 || as[0].Header.Get("Set-Cookie") != "" {
			t.Fatalf("the email step: %+v", as[0])
		}
	}
	if m := f.next(t); m.To != "lin@example.net" {
		t.Fatalf("a mail to %s", m.To)
	}
	f.none(t, 300*time.Millisecond) // and none to the others, nor a second within the minute
	// A wrong code.
	alike("a wrong code", each(func(e string) answer {
		return api(proto.PathWebLoginCode, proto.WebLoginRequest{Email: e, Code: "999999"})
	}))

	// A device's login page: the same pages.
	eng := newEngine(t, ts.URL, "box", proto.Linux)
	c := codeOf(startLogin(t, eng))
	page := func(step, e string, extra ...string) answer {
		form := url.Values{"c": {c}, "email": {e}}
		for i := 0; i+1 < len(extra); i += 2 {
			form.Set(extra[i], extra[i+1])
		}
		return rawPost(t, ts.URL+"/login/"+step, "application/x-www-form-urlencoded", form.Encode(), e)
	}
	alike("the page's email step", each(func(e string) answer { return page("email", e) }))
	alike("the page's wrong code", each(func(e string) answer { return page("code", e, "code", "999999") }))

	// Wrong codes up to the limit, then the same refusal, the right code
	// too (the test account's).
	for range maxAttempts - 2 {
		alike("a wrong code", each(func(e string) answer {
			return api(proto.PathWebLoginCode, proto.WebLoginRequest{Email: e, Code: "999999"})
		}))
	}
	as := each(func(e string) answer {
		code := "999999"
		if e == "kittors@deneb.test" {
			code = DevCode
		}
		return api(proto.PathWebLoginCode, proto.WebLoginRequest{Email: e, Code: code})
	})
	if as[0].Status != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %+v", as[0])
	}
	alike("over the limit", as)

	// The address's asks run out alike: the same refusal for every email.
	for i := 0; ; i++ {
		if a := api(proto.PathWebLoginEmail, proto.WebLoginRequest{Email: fmt.Sprintf("x%d@example.net", i)}); a.Status == http.StatusTooManyRequests {
			break
		} else if i > maxAsksPerAddress {
			t.Fatal("asks from one address never run out")
		}
	}
	alike("asks from one address over the limit", each(func(e string) answer { return api(proto.PathWebLoginEmail, proto.WebLoginRequest{Email: e}) }))
	if s.state.Load().w.byEmail["nobody@example.net"] != nil {
		t.Fatal("sign-up is closed, yet an account was made")
	}

	// As fast: the mail goes out in the background, so a slow mail server
	// slows no answer.
	slow := newFakeSMTP(t, withGreetDelay(1500*time.Millisecond))
	_, ts2, _ := newMailServer(t, slow.mailer(t), false)
	for _, e := range emails {
		start := time.Now()
		rawPost(t, ts2.URL+proto.PathWebLoginEmail, "application/json", jsonOf(proto.WebLoginRequest{Email: e}), e)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("the email step for %s took %v", e, d)
		}
	}
	if m := slow.next(t); m.To != "lin@example.net" {
		t.Fatalf("the slow server's mail to %s", m.To)
	}
}

// TestOpenSignup: with sign-up open, an email with no account gets a code,
// and checking it out makes the account, in a browser or on a device's
// login page. Closed (TestLoginAnswersAlike), nothing goes out.
func TestOpenSignup(t *testing.T) {
	f := newFakeSMTP(t)
	s, ts, _ := newMailServer(t, f.mailer(t), true)
	b := newWebBrowser(t, ts.URL)
	b.login("New.Person+deneb@example.net", "")
	code := codeIn(t, f.next(t))
	if s.state.Load().w.byEmail["new.person+deneb@example.net"] != nil {
		t.Fatal("the account was made before its code checked out")
	}
	r := b.login("new.person+deneb@example.net", code)
	p := s.state.Load().w.people[r.Person]
	if r.Step != "done" || p == nil || p.Email != "new.person+deneb@example.net" || p.DisplayName != "new.person" || p.Initial != "N" || !proto.ValidLabel(p.Name) {
		t.Fatalf("the new account: %+v %+v", r, p)
	}
	var v proto.UserView
	if st := b.do("GET", proto.PathWebUserView, nil, &v, nil); st != 200 || v.Me != p.ID || len(v.LANs) != 0 {
		t.Fatalf("its view: %d %+v", st, v.Me)
	}
	// Logging in again is the same account.
	b.login("new.person+deneb@example.net", "")
	if r2 := newWebBrowser(t, ts.URL).login("new.person+deneb@example.net", codeIn(t, f.next(t))); r2.Person != p.ID {
		t.Fatalf("again: %+v", r2)
	}

	// A device signs its owner up.
	box := newEngine(t, ts.URL, "box", proto.Linux)
	c := codeOf(startLogin(t, box))
	pb := newBrowser(t)
	pb.post(ts.URL+"/login/email", url.Values{"c": {c}, "email": {"wang@example.org"}})
	if _, page := pb.post(ts.URL+"/login/code", url.Values{"c": {c}, "email": {"wang@example.org"}, "code": {codeIn(t, f.next(t))}}); !strings.Contains(page, "现在在 wang 名下") {
		t.Fatalf("the done step:\n%s", page)
	}
	w := s.state.Load().w
	if d := w.byMachine[box.mk.Public()]; d == nil || w.people[d.Owner].Email != "wang@example.org" {
		t.Fatal("the box is not wang's")
	}
}

// TestSeedFixedCodes: only test accounts, at domains where no one receives
// mail, may have a fixed code; an account without one gets its codes by
// mail.
func TestSeedFixedCodes(t *testing.T) {
	for _, tc := range []struct {
		email, code string
		ok          bool
	}{
		{"jie@deneb.test", "123456", true},
		{"lin@example.net", "123456", true},
		{"lin@mail.example.com", "123456", true},
		{"lin@corp.invalid", "123456", true},
		{"lin@mail.c", "", true},
		{"lin@mail.c", "123456", false},
		{"not an email", "", false},
	} {
		seed := Seed{Accounts: []SeedAccount{{Email: tc.email, DisplayName: "Lin", Code: tc.code}}}
		s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://x", Seed: seed, Logf: func(string, ...any) {}})
		if (err == nil) != tc.ok {
			t.Errorf("%s with code %q: %v", tc.email, tc.code, err)
		}
		if err == nil {
			s.Close()
		}
	}
	for e, want := range map[string]bool{
		"a@b.co": true, "a.b+c@example.net": true, "": false, "a@": false, "@b.co": false, "a b@c.co": false,
		"a@b.co\r\nBcc: x@y.z": false, "Lin <a@b.co>": false, "张@b.co": false, strings.Repeat("a", 250) + "@b.co": false,
	} {
		if validEmail(e) != want {
			t.Errorf("validEmail(%q) = %v", e, !want)
		}
	}
	for ip, want := range map[string]string{"192.0.2.7": "192.0.2.7", "::ffff:192.0.2.7": "192.0.2.7", "2001:db8:1:2:3::9": "2001:db8:1:2::/64"} {
		if got := addressKey(ip); got != want {
			t.Errorf("addressKey(%q) = %q, want %q", ip, got, want)
		}
	}
}
