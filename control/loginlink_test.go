package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// recMailer keeps the mails instead of sending them: who, the code, the
// link.
type recMailer chan [3]string

func (m recMailer) SendCode(_ context.Context, to, code, link string) error {
	m <- [3]string{to, code, link}
	return nil
}

func (m recMailer) next(t *testing.T) (code, link string) {
	t.Helper()
	select {
	case got := <-m:
		return got[1], got[2]
	case <-time.After(5 * time.Second):
		t.Fatal("no mail")
	}
	panic("unreachable")
}

// TestLoginLink is logging in by the link in the code's mail
// (design/plan/m7/login-link.md): it goes with a browser's login when the
// Web version is up; its page sees whom it logs in without using it up;
// it logs in once, in whatever browser opens it, and uses up the code, as
// the code uses it up; each mail has its own, all good for one code, and a
// new code makes them old; it runs out with the code. Links that do not
// work count against the address, and no token is ever logged.
func TestLoginLink(t *testing.T) {
	mails := make(recMailer, 16)
	var mu sync.Mutex
	var logs []string
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: mailSeed(), Now: clk.now, Mailer: mails, WebURL: "https://deneb.example.com",
		Logf: func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	lin := s.state.Load().w.byEmail["lin@example.net"].ID
	var tokens []string

	ask := func(b *browser2) (code, token string) {
		t.Helper()
		if st := b.do("POST", proto.PathWebLoginEmail, proto.WebLoginRequest{Email: "lin@example.net"}, nil, nil); st != 200 {
			t.Fatalf("asking for a code: %d", st)
		}
		code, link := mails.next(t)
		token, ok := strings.CutPrefix(link, "https://deneb.example.com"+proto.LoginLinkPath+"#")
		if !ok || len(token) != 43 {
			t.Fatalf("the mail's link %q", link)
		}
		tokens = append(tokens, token)
		return code, token
	}
	info := func(b *browser2, token string) (proto.WebLoginLinkInfo, int) {
		t.Helper()
		var i proto.WebLoginLinkInfo
		st := b.do("POST", proto.PathWebLoginLinkInfo, proto.WebLoginLinkRequest{Token: token}, &i, nil)
		return i, st
	}
	use := func(b *browser2, token string) (proto.WebLoginResult, int) {
		t.Helper()
		var r proto.WebLoginResult
		st := b.do("POST", proto.PathWebLoginLink, proto.WebLoginLinkRequest{Token: token}, &r, nil)
		return r, st
	}
	loggedIn := func(b *browser2) bool {
		t.Helper()
		return b.do("GET", proto.PathWebUserView, nil, nil, nil) == 200
	}

	// 小林 asks in his browser; the link opens on his phone. Its page sees
	// whom it logs in, as often as it likes; then he says yes, once.
	desk, phone := newWebBrowser(t, ts.URL), newWebBrowser(t, ts.URL)
	code, token := ask(desk)
	for range 2 {
		if i, st := info(phone, token); st != 200 || i.Account != "lin@example.net" || !i.ExpiresAt.Equal(clk.now().Add(codeLife)) {
			t.Fatalf("the link's page: %d %+v", st, i)
		}
	}
	if r, st := use(phone, token); st != 200 || r.Step != "done" || r.Person != lin || r.Account != "lin@example.net" {
		t.Fatalf("logging in with the link: %d %+v", st, r)
	}
	if !loggedIn(phone) || loggedIn(desk) {
		t.Fatal("the phone, which opened the link, is to be logged in; the desk, which asked, is not")
	}
	if _, st := use(newWebBrowser(t, ts.URL), token); st != http.StatusNotFound {
		t.Fatalf("the link again: %d", st)
	}
	if _, st := info(phone, token); st != http.StatusNotFound {
		t.Fatalf("the used link's page: %d", st)
	}
	if st := desk.do("POST", proto.PathWebLoginCode, proto.WebLoginRequest{Email: "lin@example.net", Code: code}, nil, nil); st != http.StatusBadRequest {
		t.Fatalf("the code, after its link: %d", st)
	}

	// The code uses the link up.
	code, token = ask(desk)
	if r := desk.login("lin@example.net", code); r.Person != lin {
		t.Fatalf("the code: %+v", r)
	}
	if _, st := use(phone, token); st != http.StatusNotFound {
		t.Fatalf("the link, after its code: %d", st)
	}

	// Each mail has its own link, all good for the same code until one is
	// used; a newer code makes the old ones old.
	clk.add(codeResend)
	code1, a := ask(desk)
	clk.add(codeResend)
	code2, b := ask(desk)
	if code1 != code2 || a == b {
		t.Fatalf("a resend: the code %s then %s, the link the same: %v", code1, code2, a == b)
	}
	if _, st := info(phone, a); st != 200 {
		t.Fatalf("the first mail's link: %d", st)
	}
	clk.add(codeLife / 2)
	code3, c := ask(desk) // half the code's life gone: a new code
	if code3 == code1 {
		t.Fatal("no new code after half its life")
	}
	for _, old := range []string{a, b} {
		if _, st := info(phone, old); st != http.StatusNotFound {
			t.Fatalf("an old code's link: %d", st)
		}
	}
	// It runs out with its code.
	clk.add(codeLife)
	if _, st := use(phone, c); st != http.StatusNotFound {
		t.Fatalf("a link past its time: %d", st)
	}

	// A device's login page waits for its code: its mail has no link. (An
	// hour on, so the hour's five mails are not used up.)
	clk.add(time.Hour)
	if err := s.askCode("lin@example.net", loginFrom{what: "login", ip: "127.0.0.1", link: "AbCd"}); err != nil {
		t.Fatal(err)
	}
	if _, link := mails.next(t); link != "" {
		t.Fatalf("a device's mail has a link: %q", link)
	}

	// Links that do not work count against the address, as wrong codes do:
	// past the limit, even a good one waits.
	clk.add(codeResend)
	_, good := ask(desk)
	bad := newWebBrowser(t, ts.URL)
	for i := range maxWrongPerAddress {
		if _, st := info(bad, fmt.Sprintf("not-a-link-%02d-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", i)); st != http.StatusNotFound {
			t.Fatalf("a made-up link: %d", st)
		}
	}
	if _, st := use(bad, good); st != http.StatusTooManyRequests {
		t.Fatalf("past the limit: %d", st)
	}
	clk.add(attemptsWindow)
	if _, st := use(bad, good); st != http.StatusNotFound {
		// The window passed, and so did the code's life: dead, not blocked.
		t.Fatalf("after the window: %d", st)
	}

	// No token is ever logged.
	mu.Lock()
	defer mu.Unlock()
	for _, l := range logs {
		for _, tok := range tokens {
			if strings.Contains(l, tok) {
				t.Fatalf("a link's token in the log: %q", l)
			}
		}
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "web login: lin@example.net in a browser at 127.0.0.1 (with the mail's link)") ||
		!strings.Contains(joined, "with a login link") || !strings.Contains(joined, "a login link that does not work") {
		t.Fatalf("the log:\n%s", joined)
	}
}

// TestLoginLinkNeedsWebURL: with no Web version (no -web-url) to open a
// link, a browser's mail has none, only the code.
func TestLoginLinkNeedsWebURL(t *testing.T) {
	mails := make(recMailer, 4)
	_, ts, _ := newMailServer(t, mails, false)
	b := newWebBrowser(t, ts.URL)
	if st := b.do("POST", proto.PathWebLoginEmail, proto.WebLoginRequest{Email: "lin@example.net"}, nil, nil); st != 200 {
		t.Fatalf("asking for a code: %d", st)
	}
	if code, link := mails.next(t); code == "" || link != "" {
		t.Fatalf("the mail: code %q, link %q", code, link)
	}
}
