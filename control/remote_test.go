package control

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestRemoteSmoke runs against a deployed server, the way a real engine
// and a real browser would: pinned TLS, a login through the web pages with
// a seed's verification code, a LAN, an invite, a stream frame. It is
// skipped unless DENEB_REMOTE is set:
//
//	DENEB_REMOTE=https://103.42.31.155:47443 DENEB_PIN=sha256:… \
//	DENEB_SEED=deploy/control/seed.hk155.json go test ./control -run TestRemoteSmoke -v
//
// It logs in as the seed's mia and leo, from machine keys derived from fixed
// strings, so each run reuses the same two devices; it deletes its LAN and
// logs both out at the end.
func TestRemoteSmoke(t *testing.T) {
	base := os.Getenv("DENEB_REMOTE")
	if base == "" {
		t.Skip("DENEB_REMOTE not set")
	}
	cfg, err := proto.PinnedTLSConfig(os.Getenv("DENEB_PIN"))
	if err != nil {
		t.Fatal(err)
	}
	seedPath := os.Getenv("DENEB_SEED")
	if !strings.HasPrefix(seedPath, "/") {
		seedPath = "../" + seedPath // tests run in control/
	}
	seed, err := LoadSeed(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, a := range seed.Accounts {
		codes[a.Email] = a.Code
	}
	pinned := &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}
	http.DefaultClient.Transport = pinned // the fake engine's calls go through the pin too
	t.Cleanup(func() { http.DefaultClient.Transport = nil })

	login := func(host, email string) *engine {
		e := newEngine(t, base, host, proto.Linux)
		sum := sha256.Sum256([]byte("deneb remote smoke " + host))
		e.mk = proto.MachinePrivate(sum)
		e.nk = proto.NodePublic(sha256.Sum256([]byte("node " + host)))
		e.dk = proto.DiscoPublic(sha256.Sum256([]byte("disco " + host)))
		e.auth()
		var st proto.LoginState
		if _, err := e.call("POST", proto.PathLogin, e.loginRequest(), &st); err != nil {
			t.Fatal(err)
		}
		if !st.LoggedIn {
			jar, _ := cookiejar.New(nil)
			b := &browser{t: t, c: &http.Client{Jar: jar, Transport: pinned}}
			c := codeOf(st.LoginURL)
			b.post(base+"/login/email", url.Values{"c": {c}, "email": {email}})
			if _, page := b.post(base+"/login/code", url.Values{"c": {c}, "email": {email}, "code": {codes[email]}}); !strings.Contains(page, "登录好了") {
				t.Fatalf("web login as %s:\n%s", email, page)
			}
			var w proto.LoginState
			e.call("POST", proto.PathLoginWait, nil, &w)
			if !w.LoggedIn {
				t.Fatalf("login wait: %+v", w)
			}
			st = w
		}
		e.id = st.Device
		return e
	}
	a := login("smoke-a", "mia@deneb.test")
	b := login("smoke-b", "leo@deneb.test")

	lan := a.must(proto.OpLANCreate, proto.OpArgs{Name: "冒烟测试"}).Result.LAN
	defer func() {
		a.must(proto.OpLANDelete, proto.OpArgs{LAN: lan})
		var ls proto.LoginState
		a.call("POST", proto.PathLogout, nil, &ls)
		b.call("POST", proto.PathLogout, nil, &ls)
	}()
	inv := a.must(proto.OpLANInvite, proto.OpArgs{LAN: lan}).Result.Invite
	if !strings.HasPrefix(inv.URL, base+"/i/") {
		t.Fatalf("invite link %q", inv.URL)
	}
	resp, err := (&http.Client{Transport: pinned}).Get(inv.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "Mia 邀请你加入「冒烟测试」") {
		t.Fatalf("landing page:\n%s", page)
	}
	b.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})

	// Renaming works against the deployed server (the client's card uses
	// it); a Chinese name keeps the DNS label, and the old name comes back.
	a.must(proto.OpDeviceRename, proto.OpArgs{Device: a.id, Name: "冒烟测试机"})
	a.must(proto.OpDeviceRename, proto.OpArgs{Device: a.id, Name: "smoke-a"})

	st := b.stream(t, true)
	f := st.next(t)
	if f.NetMap == nil || len(f.NetMap.Peers) != 1 || f.NetMap.Peers[0].ID != a.id || lanByName(f.UserView, "冒烟测试") == nil {
		fb, _ := json.Marshal(f)
		t.Fatalf("stream frame: %s", fb)
	}
	if p := f.NetMap.Peers[0]; p.DisplayName != "smoke-a" || p.FQDN != "smoke-a.mia.deneb.internal" {
		t.Fatalf("after renaming back b sees a as %q, %q", p.DisplayName, p.FQDN)
	}
	if !slices.Equal(f.UserView.Features, proto.M2Features) {
		t.Logf("features %v (an M1 server sends none)", f.UserView.Features)
	}
	n := f.NetMap.Network
	t.Logf("network %s %s, dns %v; relays %+v; version %d", n.IPv4, n.IPv6, n.DNS, f.NetMap.Relays, f.Version)
	st.cancel()
	if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 || resp.ProtoMajor != 2 {
		t.Fatalf("connection: TLS %v, HTTP/%d", resp.TLS, resp.ProtoMajor)
	}
}
