package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// browser2 is the Web app in a browser: a cookie jar and JSON.
type browser2 struct {
	t   *testing.T
	url string
	c   *http.Client
	n   int
}

func newWebBrowser(t *testing.T, url string) *browser2 {
	jar, _ := cookiejar.New(nil)
	return &browser2{t: t, url: url, c: &http.Client{Jar: jar}}
}

// do sends a request as the app would; out gets the answer, or the error
// body on a refusal.
func (b *browser2) do(method, path string, in, out any, header map[string]string) int {
	b.t.Helper()
	var body *bytes.Reader
	if in != nil {
		j, _ := json.Marshal(in)
		body = bytes.NewReader(j)
	} else {
		body = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, b.url+path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (b *browser2) login(email, code string) proto.WebLoginResult {
	b.t.Helper()
	var r proto.WebLoginResult
	if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: email, Code: code}, &r, nil); st != 200 {
		b.t.Fatalf("login %s: %d", email, st)
	}
	return r
}

func (b *browser2) op(op proto.OpType, a proto.OpArgs) (int, proto.OpResult) {
	b.t.Helper()
	b.n++
	var r proto.OpResult
	st := b.do("POST", proto.PathWebOps, proto.Op{ID: fmt.Sprintf("web-%d", b.n), Op: op, Args: a}, &r, nil)
	return st, r
}

// TestWebAPI is the browser's way in: log in with an email and its code,
// get a session cookie, and see and change what a device would, as a
// person with no device.
func TestWebAPI(t *testing.T) {
	s, ts, clk := newClockServer(t, 0)
	b := newWebBrowser(t, ts.URL)

	// Not logged in: nothing but the login and an invite's preview.
	if st := b.do("GET", proto.PathWebUserView, nil, nil, nil); st != 401 {
		t.Fatalf("the view logged out: %d", st)
	}
	var eb proto.ErrorBody
	if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "nobody@deneb.test"}, &eb, nil); st != 404 || !strings.Contains(eb.Error.Message, "没有开通") {
		t.Fatalf("an unknown email: %d %+v", st, eb.Error)
	}
	if r := b.login(" Jie@deneb.test ", ""); r.Step != "code" || r.Account != "登录到阿杰（jie@deneb.test）" || r.Person != "" {
		t.Fatalf("the email step: %+v", r)
	}
	if st := b.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "jie@deneb.test", Code: "999999"}, nil, nil); st != 400 {
		t.Fatalf("a wrong code: %d", st)
	}
	r := b.login("jie@deneb.test", DevCode)
	if r.Step != "done" || r.Person == "" {
		t.Fatalf("logged in: %+v", r)
	}
	u, _ := http.NewRequest("GET", ts.URL, nil)
	cs := b.c.Jar.Cookies(u.URL)
	if len(cs) != 1 || cs[0].Name != proto.WebSessionCookie {
		t.Fatalf("cookies: %+v", cs)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM web_sessions WHERE id = ?`, cs[0].Value).Scan(&count)
	if count != 0 {
		t.Fatal("the session's token is kept as it is, not as a hash")
	}

	var v proto.UserView
	if st := b.do("GET", proto.PathWebUserView, nil, &v, nil); st != 200 || v.Me != r.Person {
		t.Fatalf("the view: %d %+v", st, v.Me)
	}

	// A LAN made in a browser has no device in it; an invite accepted in
	// one brings no device either.
	st, res := b.op(proto.OpLANCreate, proto.OpArgs{Name: "网页里建的"})
	if st != 200 || res.Result == nil || res.Result.LAN == "" {
		t.Fatalf("lan.create: %d %+v", st, res)
	}
	b.do("GET", proto.PathWebUserView, nil, &v, nil)
	if lv := lanByName(&v, "网页里建的"); lv == nil || len(lv.Devices) != 0 || len(lv.Members) != 1 {
		t.Fatalf("the LAN made in a browser: %+v", lv)
	}
	// The login on its two paths, as the invite card does it.
	mia := newWebBrowser(t, ts.URL)
	var step proto.WebLoginResult
	if st := mia.do("POST", proto.PathWebLoginEmail, proto.WebLoginRequest{Email: "mia@deneb.test"}, &step, nil); st != 200 || step.Step != "code" {
		t.Fatalf("login/email: %d %+v", st, step)
	}
	if st := mia.do("POST", proto.PathWebLoginCode, proto.WebLoginRequest{Email: "mia@deneb.test"}, nil, nil); st != 400 {
		t.Fatalf("login/code with no code: %d", st)
	}
	if st := mia.do("POST", proto.PathWebLoginCode, proto.WebLoginRequest{Email: "mia@deneb.test", Code: DevCode}, &step, nil); st != 200 || step.Step != "done" || step.Person == "" {
		t.Fatalf("login/code: %d %+v", st, step)
	}
	_, inv := b.op(proto.OpLANInvite, proto.OpArgs{LAN: res.Result.LAN})
	var pre proto.InvitePreview
	if st := newWebBrowser(t, ts.URL).do("POST", proto.PathWebInvite, proto.InviteLookup{Invite: inv.Result.Invite.URL}, &pre, nil); st != 200 || pre.LANName != "网页里建的" {
		t.Fatalf("an invite's preview, logged out: %d %+v", st, pre)
	}
	token := inv.Result.Invite.URL[strings.LastIndex(inv.Result.Invite.URL, "/")+1:]
	pre = proto.InvitePreview{}
	if st := newWebBrowser(t, ts.URL).do("GET", proto.PathWebInvites+token, nil, &pre, nil); st != 200 || pre.LANName != "网页里建的" || pre.AlreadyIn ||
		len(pre.Members) != 1 || pre.Members[0].ID != r.Person || pre.Members[0].Email != "" || pre.Devices != 0 {
		t.Fatalf("GET invites/<token>, logged out: %d %+v", st, pre)
	}
	var bad proto.ErrorBody
	if st := newWebBrowser(t, ts.URL).do("GET", proto.PathWebInvites+"nope", nil, &bad, nil); st/100 != 4 || bad.Error == nil {
		t.Fatalf("an unknown token: %d %+v", st, bad)
	}
	if st, _ := mia.op(proto.OpInviteAccept, proto.OpArgs{Invite: token}); st != 200 {
		t.Fatalf("invite.accept in a browser: %d", st)
	}
	b.do("GET", proto.PathWebUserView, nil, &v, nil)
	if lv := lanByName(&v, "网页里建的"); len(lv.Members) < 2 {
		t.Fatalf("after Mia accepted: %+v", lv.Members)
	}
	// Used, the link shows nothing of the LAN to a stranger any more.
	pre = proto.InvitePreview{}
	if newWebBrowser(t, ts.URL).do("GET", proto.PathWebInvites+token, nil, &pre, nil); pre.State != proto.InviteAccepted || len(pre.Members) != 0 || pre.Devices != 0 {
		t.Fatalf("a used link's preview: %+v", pre)
	}

	// The same op id twice: the first answer again, and done once.
	var r1, r2 proto.OpResult
	st1 := b.do("POST", proto.PathWebOps, proto.Op{ID: "retry", Op: proto.OpLANRename, Args: proto.OpArgs{LAN: res.Result.LAN, Name: "改过的"}}, &r1, nil)
	st2 := b.do("POST", proto.PathWebOps, proto.Op{ID: "retry", Op: proto.OpLANRename, Args: proto.OpArgs{LAN: res.Result.LAN, Name: "又改的"}}, &r2, nil)
	b.do("GET", proto.PathWebUserView, nil, &v, nil)
	if st1 != 200 || st2 != 200 || r1.Version != r2.Version || lanByName(&v, "改过的") == nil {
		t.Fatalf("a retried op: %d %+v, %d %+v", st1, r1, st2, r2)
	}

	// Another site's page cannot post with the cookie, and a form cannot.
	if st := b.do("POST", proto.PathWebOps, proto.Op{ID: "x", Op: proto.OpLANCreate, Args: proto.OpArgs{Name: "跨站"}}, nil,
		map[string]string{"Sec-Fetch-Site": "cross-site"}); st != 403 {
		t.Fatalf("a cross-site post: %d", st)
	}
	if st := b.do("POST", proto.PathWebOps, proto.Op{ID: "y", Op: proto.OpLANCreate, Args: proto.OpArgs{Name: "表单"}}, nil,
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); st != 415 {
		t.Fatalf("a form post: %d", st)
	}

	// The relay usage, for the cards.
	var ru proto.RelayUsage
	if st := b.do("GET", proto.PathWebRelayUsage, nil, &ru, nil); st != 200 || ru.Month == "" {
		t.Fatalf("relay usage: %d %+v", st, ru)
	}

	// The stream: the view first, then each change; no network map.
	req, _ := http.NewRequest("GET", ts.URL+proto.PathWebStream, nil)
	resp, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("the stream: %s", resp.Status)
	}
	frames := make(chan proto.StreamFrame, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var f proto.StreamFrame
			json.Unmarshal(sc.Bytes(), &f)
			frames <- f
		}
		close(frames)
	}()
	next := func() proto.StreamFrame {
		select {
		case f := <-frames:
			return f
		case <-time.After(5 * time.Second):
			t.Fatal("no frame")
		}
		return proto.StreamFrame{}
	}
	if f := next(); f.UserView == nil || f.NetMap != nil || f.UserView.Me != r.Person {
		t.Fatalf("the first frame: %+v", f)
	}
	b.op(proto.OpLANRename, proto.OpArgs{LAN: res.Result.LAN, Name: "推过来的"})
	if f := next(); lanByName(f.UserView, "推过来的") == nil {
		t.Fatalf("after the rename: %+v", f.UserView.LANs)
	}
	resp.Body.Close()

	// Logged out: the cookie goes, and the session with it.
	if st := b.do("POST", proto.PathWebLogout, nil, nil, nil); st != 200 {
		t.Fatalf("logout: %d", st)
	}
	if st := b.do("GET", proto.PathWebUserView, nil, nil, nil); st != 401 {
		t.Fatalf("after logout: %d", st)
	}
	// A session runs out after 30 days.
	mia2 := newWebBrowser(t, ts.URL)
	mia2.login("mia@deneb.test", DevCode)
	clk.add(webSessionLife)
	if st := mia2.do("GET", proto.PathWebUserView, nil, nil, nil); st != 401 {
		t.Fatalf("a session past 30 days: %d", st)
	}
	s.sweep()
	s.db.QueryRow(`SELECT COUNT(*) FROM web_sessions`).Scan(&count)
	if count != 0 {
		t.Fatalf("%d sessions after the sweep", count)
	}
	// Too many wrong codes: wait.
	x := newWebBrowser(t, ts.URL)
	for range maxAttempts {
		x.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "leo@deneb.test", Code: "111111"}, nil, nil)
	}
	if st := x.do("POST", proto.PathWebLogin, proto.WebLoginRequest{Email: "leo@deneb.test", Code: DevCode}, nil, nil); st != 429 {
		t.Fatalf("after %d wrong codes: %d", maxAttempts, st)
	}
}

// TestSeatsForOne: the operator gives one test account 3 seats on the
// running server; it fills up at 3 while everyone else keeps the
// deployment's, and nothing in already is put out; reset, 6 again.
func TestSeatsForOne(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	s, ts, sock := adminServer(t, t.TempDir(), DevSeed(), &lines, &mu)
	login := func(host, email string) *engine {
		e := newEngine(t, ts.URL, host, proto.Linux)
		if _, err := AdminApprove(sock, startLogin(t, e), email); err != nil {
			t.Fatal(err)
		}
		var st proto.LoginState
		e.call("POST", proto.PathLogin, e.loginRequest(), &st)
		e.id = st.Device
		return e
	}
	mia, leo, jie, kit := login("mia", "mia@deneb.test"), login("leo", "leo@deneb.test"), login("jie", "jie@deneb.test"), login("kit", "kittors@deneb.test")
	ids := map[string]proto.PersonID{}
	for name, e := range map[string]*engine{"mia": mia, "leo": leo, "jie": jie, "kit": kit} {
		ids[name] = e.view().Me
	}

	if res, err := AdminSetSeats(sock, "Mia@deneb.test", 3); err != nil || res.Limit != 3 || !res.Own || res.Used != 1 {
		t.Fatalf("seats -limit 3: %+v, %v", res, err)
	}
	if v := mia.view(); v.Seats.Limit != 3 || v.Seats.Left != 2 {
		t.Fatalf("Mia's seats: %+v", v.Seats)
	}
	if v := kit.view(); v.Seats.Limit != DefaultSeatLimit {
		t.Fatalf("someone else's seats changed: %+v", v.Seats)
	}
	l := mia.must(proto.OpLANCreate, proto.OpArgs{Name: "位置测试"}).Result.LAN
	for _, name := range []string{"leo", "jie"} {
		inv := mia.must(proto.OpLANInvite, proto.OpArgs{LAN: l, Person: ids[name]}).Result.Invite
		map[string]*engine{"leo": leo, "jie": jie}[name].must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	}
	if v := mia.view(); v.Seats.Used != 3 || v.Seats.Left != 0 {
		t.Fatalf("full: %+v", v.Seats)
	}
	// The fourth waits, as with any full seats.
	inv := mia.must(proto.OpLANInvite, proto.OpArgs{LAN: l, Person: ids["kit"]}).Result.Invite
	kit.must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	if lv := lanByName(mia.view(), "位置测试"); lv == nil || len(lv.Members) != 4 || !memberPending(lv, ids["kit"]) {
		t.Fatalf("the fourth: %+v", lv)
	}
	// Down to 2 below what is used: no one in is put out.
	AdminSetSeats(sock, "mia@deneb.test", 2)
	if peerOf(netMapOf(s, leo.id), jie.id) == nil {
		t.Fatal("lowering the limit put someone out")
	}
	// Reset: the deployment's 6, and the fourth can be let in.
	if res, err := AdminSetSeats(sock, "mia@deneb.test", 0); err != nil || res.Limit != DefaultSeatLimit || res.Own {
		t.Fatalf("reset: %+v, %v", res, err)
	}
	mia.must(proto.OpMemberApprove, proto.OpArgs{LAN: l, Person: ids["kit"]})
	if ss, err := AdminListSeats(sock); err != nil || len(ss) != 4 {
		t.Fatalf("seats: %+v, %v", ss, err)
	}
	if _, err := AdminSetSeats(sock, "nobody@deneb.test", 3); err == nil {
		t.Fatal("an unknown account")
	}
}

func memberPending(lv *proto.LANView, p proto.PersonID) bool {
	for _, m := range lv.Members {
		if m.Person == p {
			return m.State == proto.MemberPending
		}
	}
	return false
}
