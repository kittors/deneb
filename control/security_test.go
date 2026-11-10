package control

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// The M7 review's fixes (design/plan/m7/control-security.md).

// TestCodeGuessesAtOnce: guesses of a verification code sent all at once
// get no more tries than sent one by one. Checking the count and counting
// the try are one step, so the burst cannot pass the check before any of
// it is counted.
func TestCodeGuessesAtOnce(t *testing.T) {
	_, ts, _ := newClockServer(t, 0)
	var wrong, blocked, other atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := newWebBrowser(t, ts.URL)
			switch b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "jie@deneb.test", Code: fmt.Sprintf("9%05d", i)}, nil, nil) {
			case http.StatusBadRequest:
				wrong.Add(1)
			case http.StatusTooManyRequests:
				blocked.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	wg.Wait()
	if wrong.Load() > maxAttempts || wrong.Load()+blocked.Load() != 40 || other.Load() != 0 {
		t.Fatalf("40 guesses at once: %d wrong, %d blocked, %d else; at most %d may be tried", wrong.Load(), blocked.Load(), other.Load(), maxAttempts)
	}
	// Blocked is blocked, the right code too, until the window passes.
	if st := newWebBrowser(t, ts.URL).do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "jie@deneb.test", Code: DevCode}, nil, nil); st != http.StatusTooManyRequests {
		t.Fatalf("the right code after the burst: %d", st)
	}
	// One by one: wrong ones add up, a right one starts the count over.
	b := newWebBrowser(t, ts.URL)
	for range maxAttempts - 1 {
		if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "mia@deneb.test", Code: "999999"}, nil, nil); st != http.StatusBadRequest {
			t.Fatalf("a wrong code: %d", st)
		}
	}
	b.login("mia@deneb.test", DevCode)
	if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "mia@deneb.test", Code: "999999"}, nil, nil); st != http.StatusBadRequest {
		t.Fatalf("a wrong code after a right one: %d", st)
	}
}

// TestChallengeSpentOnlyWhenSigned: answers with a bad signature are turned
// away without using the challenge up or growing the table of answered
// ones; the right answer works once.
func TestChallengeSpentOnlyWhenSigned(t *testing.T) {
	s, ts := newTestServer(t, "")
	e := newEngine(t, ts.URL, "x", proto.Linux)
	var ch proto.AuthChallenge
	if _, err := e.call("POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: e.mk.Public()}, &ch); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		bad := proto.AuthTokenRequest{MachineKey: e.mk.Public(), Challenge: ch.Challenge, Signature: make([]byte, 64)}
		if _, err := e.call("POST", proto.PathAuthToken, bad, nil); err == nil || err.Code != proto.ErrUnauthorized {
			t.Fatalf("a bad signature: %v", err)
		}
	}
	s.keys.mu.Lock()
	n := len(s.keys.used)
	s.keys.mu.Unlock()
	if n != 0 {
		t.Fatalf("bad signatures left %d answers in the table", n)
	}
	good := proto.AuthTokenRequest{MachineKey: e.mk.Public(), Challenge: ch.Challenge, Signature: e.mk.SignChallenge(ch.Challenge)}
	var tok proto.AuthToken
	if _, err := e.call("POST", proto.PathAuthToken, good, &tok); err != nil || tok.Token == "" {
		t.Fatalf("the right answer: %v", err)
	}
	if _, err := e.call("POST", proto.PathAuthToken, good, nil); err == nil || err.Code != proto.ErrUnauthorized {
		t.Fatalf("the same answer again: %v", err)
	}
}

// TestPendingLoginLimit: one address may have so many logins waiting for a
// browser, and no more, since a machine key costs nothing to make; a
// machine asking again keeps its link; when the links run out there is
// room again.
func TestPendingLoginLimit(t *testing.T) {
	s, ts, clk := newClockServer(t, 0)
	ask := func(e *engine) (int, *proto.Error) {
		var st proto.LoginState
		return e.call("POST", proto.PathLogin, e.loginRequest(), &st)
	}
	var first *engine
	for i := range maxPendingPerIP {
		e := newEngine(t, ts.URL, fmt.Sprintf("m%d", i), proto.Linux)
		e.auth()
		if _, err := ask(e); err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
		if first == nil {
			first = e
		}
	}
	extra := newEngine(t, ts.URL, "one-too-many", proto.Linux)
	extra.auth()
	if code, err := ask(extra); err == nil || code != http.StatusTooManyRequests {
		t.Fatalf("one more from the same address: %d %v", code, err)
	}
	if _, err := ask(first); err != nil {
		t.Fatalf("a machine asking again: %v", err)
	}
	clk.add(loginTTL + time.Second)
	extra.auth()
	if _, err := ask(extra); err != nil {
		t.Fatalf("after the links ran out: %v", err)
	}
	s.logins.mu.Lock()
	n, per := len(s.logins.byCode), s.logins.perIP["127.0.0.1"]
	s.logins.mu.Unlock()
	if n != 1 || per != 1 {
		t.Fatalf("waiting after the hour: %d, %d from the address", n, per)
	}
}

// TestDataDirClosed: a data directory others may read is closed at start.
func TestDataDirClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: dir, PublicURL: "http://x", Dev: true, Seed: DevSeed(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the data directory: %v %v", fi.Mode(), err)
	}
}

// TestLogPathHidesTokens: an invite's token never goes into the log.
func TestLogPathHidesTokens(t *testing.T) {
	for in, want := range map[string]string{
		proto.InvitePath + "SECRET":     proto.InvitePath + "…",
		proto.PathWebInvites + "SECRET": proto.PathWebInvites + "…",
		proto.PathOps:                   proto.PathOps,
		proto.InvitePath:                proto.InvitePath,
	} {
		if got := logPath(in); got != want {
			t.Errorf("logPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRevokeWebSessions: the server's admin logs every browser of an
// account out; their requests are refused at once and their streams end
// within a keep-alive. Logging out in one tab ends the other tab's stream
// the same way.
func TestRevokeWebSessions(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	s, ts, sock := adminServer(t, t.TempDir(), DevSeed(), &lines, &mu)
	s.keepAlive = 20 * time.Millisecond
	a, b := newWebBrowser(t, ts.URL), newWebBrowser(t, ts.URL)
	a.login("jie@deneb.test", DevCode)
	b.login("jie@deneb.test", DevCode)
	if ss, err := AdminListSessions(sock); err != nil || len(ss) != 1 || ss[0].Email != "jie@deneb.test" || ss[0].Count != 2 {
		t.Fatalf("sessions: %+v, %v", ss, err)
	}
	ended := func(br *browser2) chan struct{} {
		t.Helper()
		resp, err := br.c.Get(ts.URL + proto.PathWebStream)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("stream: %v", err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer resp.Body.Close()
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
			}
		}()
		return done
	}
	wait := func(done chan struct{}, what string) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the stream goes on", what)
		}
	}

	streamA := ended(a)
	if n, err := AdminRevokeSessions(sock, "Jie@deneb.test"); err != nil || n != 2 {
		t.Fatalf("revoke: %d, %v", n, err)
	}
	for _, br := range []*browser2{a, b} {
		if st := br.do("GET", proto.PathWebUserView, nil, nil, nil); st != http.StatusUnauthorized {
			t.Fatalf("a revoked browser's view: %d", st)
		}
	}
	wait(streamA, "revoked")
	if _, err := AdminRevokeSessions(sock, "nobody@deneb.test"); err == nil {
		t.Fatal("revoked an account that does not exist")
	}

	// Logging out in one tab: the other tab shares the cookie, and its
	// stream ends too.
	a.login("jie@deneb.test", DevCode)
	streamA = ended(a)
	a.do("POST", proto.PathWebLogout, nil, nil, nil)
	wait(streamA, "logged out")
}

// TestStreamLimit: a device holds so many streams at once and no more;
// when one closes there is room again.
func TestStreamLimit(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	var open []*stream
	for range maxStreamsPerDevice {
		st := mac.stream(t, false)
		st.next(t)
		open = append(open, st)
	}
	req, _ := http.NewRequest("GET", ts.URL+proto.PathStream, nil)
	req.Header.Set("Authorization", "Bearer "+mac.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("one stream too many: %s", resp.Status)
	}
	open[0].cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.hub.mu.Lock()
		n := s.hub.streams[mac.id]
		s.hub.mu.Unlock()
		if n < maxStreamsPerDevice {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed stream still counts")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mac.stream(t, false).next(t)
	for _, st := range open[1:] {
		st.cancel()
	}
}
