package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// upstream is a stand-in control server over TLS: it sets a Secure cookie,
// echoes the cookie it gets, and streams two lines a moment apart.
func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Sec-Fetch-Site"); got != "same-origin" {
			t.Errorf("Sec-Fetch-Site = %q, want it passed on", got)
		}
		http.SetCookie(w, &http.Cookie{Name: proto.WebSessionCookie, Value: "tok", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		_, _ = io.WriteString(w, `{"step":"done"}`)
	})
	mux.HandleFunc("GET /api/v1/userview", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(proto.WebSessionCookie)
		if err != nil {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"me":"`+c.Value+`"}`)
	})
	mux.HandleFunc("GET /api/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "{\"version\":1}\n")
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "{\"version\":2}\n")
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func appDir(t *testing.T) string {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "index.html"), []byte("<!doctype html><title>app</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "main.js"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProxyPinsTheKey(t *testing.T) {
	up := upstream(t)
	u, _ := url.Parse(up.URL)
	wrong := "sha256:" + strings.Repeat("ab", 32)
	p, err := newProxy(u, wrong)
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(newServer(appDir(t), p))
	defer web.Close()
	resp, err := http.Get(web.URL + "/api/v1/userview")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("a wrong pin: status %d, want 502", resp.StatusCode)
	}
	if _, err := newProxy(&url.URL{Scheme: "http", Host: "203.0.113.5:47443"}, ""); err == nil {
		t.Fatal("plain http to a control server elsewhere was taken")
	}
	if _, err := newProxy(&url.URL{Scheme: "https", Host: u.Host}, ""); err == nil {
		t.Fatal("https without a pin was taken")
	}
	if _, err := newProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:47080"}, ""); err != nil {
		t.Fatalf("deneb-control -dev on this machine: %v", err)
	}
}

func TestProxyCookiesAndStream(t *testing.T) {
	up := upstream(t)
	u, _ := url.Parse(up.URL)
	p, err := newProxy(u, proto.Fingerprint(up.Certificate()))
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(newServer(appDir(t), p))
	defer web.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	req, _ := http.NewRequest("POST", web.URL+"/api/v1/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	sc := resp.Header.Get("Set-Cookie")
	if !strings.Contains(sc, proto.WebSessionCookie+"=tok") || strings.Contains(strings.ToLower(sc), "secure") || !strings.Contains(sc, "HttpOnly") {
		t.Fatalf("Set-Cookie through the proxy = %q: want it, HttpOnly, without Secure", sc)
	}
	resp, err = c.Get(web.URL + "/api/v1/userview")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != `{"me":"tok"}` {
		t.Fatalf("the cookie did not go back: %s", b)
	}

	resp, err = c.Get(web.URL + "/api/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	start := time.Now()
	line, err := br.ReadString('\n')
	if err != nil || line != "{\"version\":1}\n" {
		t.Fatalf("first frame %q, %v", line, err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatalf("the first frame waited for the second: the stream is buffered")
	}
}

func TestAppFallsBackToIndex(t *testing.T) {
	web := httptest.NewServer(newServer(appDir(t), newMock().handler()))
	defer web.Close()
	for path, want := range map[string]int{"/": 200, "/i/k3xQ9": 200, "/lan/l_weekend": 200, "/main.js": 200, "/missing.js": 404} {
		resp, err := http.Get(web.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
		if path == "/i/k3xQ9" && resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("the invite page leaks its link through Referer")
		}
	}
}

// client is a browser on the mock: a cookie jar, JSON in and out.
type client struct {
	t   *testing.T
	url string
	c   *http.Client
}

func newClient(t *testing.T) *client {
	web := httptest.NewServer(newServer(appDir(t), newMock().handler()))
	t.Cleanup(web.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, url: web.URL, c: &http.Client{Jar: jar}}
}

func (c *client) post(path string, body, out any) (int, *proto.Error) {
	c.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.url+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var eb proto.ErrorBody
		_ = json.Unmarshal(data, &eb)
		return resp.StatusCode, eb.Error
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s: %v: %s", path, err, data)
		}
	}
	return resp.StatusCode, nil
}

func (c *client) get(path string, out any) int {
	c.t.Helper()
	resp, err := c.c.Get(c.url + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func (c *client) login(email string) {
	c.t.Helper()
	var r proto.WebLoginResult
	if st, e := c.post(proto.PathWebLoginEmail, proto.WebLoginRequest{Email: email}, &r); e != nil || r.Step != "code" {
		c.t.Fatalf("login email: %d %v %+v", st, e, r)
	}
	if st, e := c.post(proto.PathWebLoginCode, proto.WebLoginRequest{Email: email, Code: "123456"}, nil); e == nil || st != 400 {
		c.t.Fatalf("a wrong code was taken: %d", st)
	}
	if st, e := c.post(proto.PathWebLoginCode, proto.WebLoginRequest{Email: email, Code: "000000"}, &r); e != nil || r.Step != "done" || r.Person == "" {
		c.t.Fatalf("login code: %d %v %+v", st, e, r)
	}
}

func (c *client) op(o proto.Op) (*proto.OpResult, *proto.Error) {
	c.t.Helper()
	var r proto.OpResult
	if _, e := c.post(proto.PathWebOps, o, &r); e != nil {
		return nil, e
	}
	return &r, nil
}

func TestMockShareTwiceAndStream(t *testing.T) {
	c := newClient(t)
	if st := c.get(proto.PathWebUserView, nil); st != 401 {
		t.Fatalf("logged out: userview %d, want 401", st)
	}
	c.login("kittors@deneb.test")
	var v proto.UserView
	if st := c.get(proto.PathWebUserView, &v); st != 200 || v.Me != "p_kittors" {
		t.Fatalf("userview %d, me %q", st, v.Me)
	}

	// The stream: the view at once, then a new one after the op.
	resp, err := c.c.Get(c.url + proto.PathWebStream + "?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	var f proto.StreamFrame
	if err := dec.Decode(&f); err != nil || f.UserView == nil || f.Version != v.Version {
		t.Fatalf("first frame: %v %+v", err, f.Version)
	}

	share := proto.Op{ID: "o1", Op: proto.OpShareCreate, Args: proto.OpArgs{Device: "d_build", Person: "p_jie"}}
	r, e := c.op(share)
	if e != nil || r.Result == nil || r.Result.Share == "" {
		t.Fatalf("share.create: %v %+v", e, r)
	}
	if err := dec.Decode(&f); err != nil || f.Version != r.Version {
		t.Fatalf("no frame after the op: %v %d, want %d", err, f.Version, r.Version)
	}
	var has bool
	for _, d := range f.UserView.Devices {
		if d.ID == "d_build" {
			for _, s := range d.Shares {
				has = has || s.Person == "p_jie"
			}
		}
	}
	if !has {
		t.Fatal("the build server's card does not list 阿杰")
	}
	// The same op again (a retry) is answered as before; a new one is
	// "已经有了".
	if r2, e := c.op(share); e != nil || r2.Version != r.Version {
		t.Fatalf("a retry: %v %+v", e, r2)
	}
	share.ID = "o2"
	if _, e := c.op(share); e == nil || e.Code != proto.ErrConflict || e.Message != "已经有了" {
		t.Fatalf("sharing twice: %+v, want conflict 已经有了", e)
	}
	// Taken back, then given again: as it was (undo).
	if _, e := c.op(proto.Op{ID: "o3", Op: proto.OpShareRevoke, Args: proto.OpArgs{Share: r.Result.Share}}); e != nil {
		t.Fatal(e)
	}
	share.ID = "o4"
	if r4, e := c.op(share); e != nil || r4.Result.Share != r.Result.Share {
		t.Fatalf("undo: %v %+v, want share %s back", e, r4, r.Result.Share)
	}
}

func TestMockInviteAndSharedView(t *testing.T) {
	c := newClient(t)
	var p proto.InvitePreview
	if st, e := c.post(proto.PathWebInvite, proto.InviteLookup{Invite: "https://deneb.app/i/k3xQ9"}, &p); e != nil || p.LANName != "周末联机" || p.Inviter.DisplayName != "kittors" || p.Inviter.Email != "" {
		t.Fatalf("preview logged out: %d %v %+v", st, e, p)
	}
	if st := c.get(proto.PathWebInvites+"old42", &p); st != 200 || p.State != proto.InviteExpired {
		t.Fatalf("an old link: %d %+v", st, p)
	}
	if st := c.get(proto.PathWebInvites+"nope", nil); st != 404 {
		t.Fatalf("no such link: %d", st)
	}
	c.login("leo.chen@gmail.com")
	if _, e := c.op(proto.Op{ID: "a", Op: proto.OpInviteAccept, Args: proto.OpArgs{Invite: "k3xQ9"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.op(proto.Op{ID: "b", Op: proto.OpInviteAccept, Args: proto.OpArgs{Invite: "k3xQ9"}}); e == nil || e.Code != proto.ErrConflict {
		t.Fatalf("accepting twice: %+v", e)
	}
	var v proto.UserView
	c.get(proto.PathWebUserView, &v)
	var weekend, studio *proto.LANView
	for i := range v.LANs {
		switch v.LANs[i].ID {
		case "l_weekend":
			weekend = &v.LANs[i]
		case "l_studio":
			studio = &v.LANs[i]
		}
	}
	if weekend == nil || len(weekend.Devices) != 5 {
		t.Fatalf("Leo in 周末联机: %+v", weekend)
	}
	// A member of the device-sharing studio sees its name, its owner and
	// himself, and nothing of its devices.
	if studio == nil || len(studio.Devices) != 0 || len(studio.Members) != 2 {
		t.Fatalf("Leo's 工作室: %+v", studio)
	}
	for _, d := range v.Devices {
		if d.PublicLAN != "" {
			t.Fatalf("Leo sees the studio's %s", d.Name)
		}
		if d.Owner != v.Me && len(d.Services) > 0 {
			t.Fatalf("another's catalog shown: %s", d.ID)
		}
	}
	// Installed and logged in: a device of his appears in 周末联机.
	if st, e := c.post("/api/v1/_mock/device-up", struct{}{}, nil); e != nil {
		t.Fatalf("device-up: %d %v", st, e)
	}
	c.get(proto.PathWebUserView, &v)
	var mine int
	for _, d := range v.Devices {
		if d.Owner == v.Me {
			mine++
		}
	}
	if mine != 1 {
		t.Fatalf("Leo's devices after install: %d", mine)
	}
}

func TestMockRelays(t *testing.T) {
	c := newClient(t)
	c.login("kittors@deneb.test")
	r, e := c.op(proto.Op{ID: "j", Op: proto.OpRelayJoin})
	if e != nil || r.Result == nil || r.Result.RelayJoin == nil || !strings.Contains(r.Result.RelayJoin.Command, "--join "+proto.RelayJoinPrefix) {
		t.Fatalf("relay.join: %v %+v", e, r)
	}
	var u proto.RelayUsage
	c.get(proto.PathWebRelayUsage, &u)
	if len(u.Relays) != 1 || u.Relays[0].Relay != "r_tokyo" || u.Relays[0].Load == nil || len(u.Relays[0].Connected) != 3 {
		t.Fatalf("usage: %+v", u)
	}
	k, e := c.op(proto.Op{ID: "k", Op: proto.OpRelayKick, Args: proto.OpArgs{Relay: "r_tokyo", Person: "p_jie"}})
	if e != nil || k.Result.Kick == "" {
		t.Fatalf("kick: %v %+v", e, k)
	}
	c.get(proto.PathWebRelayUsage, &u)
	if n := len(u.Relays[0].Connected); n != 2 || u.Relays[0].Load.People != 2 || len(u.Relays[0].Kicks) != 1 {
		t.Fatalf("after the kick: %d on it, %+v", n, u.Relays[0])
	}
	if _, e := c.op(proto.Op{ID: "u", Op: proto.OpRelayUnkick, Args: proto.OpArgs{Kick: k.Result.Kick}}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.post("/api/v1/_mock/relay-up", map[string]string{}, nil); e != nil {
		t.Fatal(e)
	}
	var v proto.UserView
	c.get(proto.PathWebUserView, &v)
	if len(v.MyRelays) != 2 || len(v.RelayJoins) != 0 {
		t.Fatalf("after the install: %d relays, %d codes waiting", len(v.MyRelays), len(v.RelayJoins))
	}
	// Not JSON: refused, as a form another site posts would be.
	resp, err := c.c.Post(c.url+proto.PathWebOps, "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("a text/plain op was taken: %d", resp.StatusCode)
	}
}

// The app's scripts go out compressed to a browser that takes gzip, and
// whole to one that does not; the API never does.
func TestGzip(t *testing.T) {
	dir := t.TempDir()
	js := strings.Repeat("console.log('deneb');\n", 200)
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(newServer(dir, api))
	defer srv.Close()
	get := func(p string, gz bool) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		if gz {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		res, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	res, b := get("/main.js", true)
	if res.Header.Get("Content-Encoding") != "gzip" || len(b) >= len(js) {
		t.Fatalf("main.js with gzip: encoding %q, %d bytes of %d", res.Header.Get("Content-Encoding"), len(b), len(js))
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); string(plain) != js {
		t.Fatal("main.js does not unzip to itself")
	}
	if res, b := get("/main.js", false); res.Header.Get("Content-Encoding") != "" || string(b) != js {
		t.Fatalf("main.js without gzip: encoding %q", res.Header.Get("Content-Encoding"))
	}
	if res, _ := get("/api/v1/userview", true); res.Header.Get("Content-Encoding") != "" {
		t.Fatal("the API was compressed")
	}
}
