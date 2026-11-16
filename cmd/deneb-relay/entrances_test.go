package main

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestEntranceTransport: a relay whose join code names Deneb's own server
// goes by its ways in (design/plan/control-reachability-20261116.md §9.3):
// the first refuses, so the second, and it stays; a server's answer, even
// an error, is no reason to try another way; someone's own server is
// reached by its address alone.
func TestEntranceTransport(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	pin := proto.Fingerprint(srv.Certificate())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "https://" + ln.Addr().String()
	ln.Close()
	js, _ := json.Marshal(proto.ControlEntrances{Version: 1, Entrances: []proto.ControlEntrance{{URL: refused, Pin: pin}, {URL: srv.URL, Pin: pin}}})
	old := builtinEntrances
	builtinEntrances = base64.StdEncoding.EncodeToString(js)
	t.Cleanup(func() { builtinEntrances = old })

	c, err := newControlClient(refused, pin)
	if err != nil {
		t.Fatal(err)
	}
	et, ok := c.hc.Transport.(*entranceTransport)
	if !ok {
		t.Fatalf("Deneb's own server by %T", c.hc.Transport)
	}
	get := func(path string) int {
		t.Helper()
		resp, err := c.hc.Get(c.base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/x"); code != http.StatusOK || et.cur.Load() != 1 {
		t.Fatalf("got %d by way %d; want 200 by the second", code, et.cur.Load())
	}
	if code := get("/fail"); code != http.StatusInternalServerError || et.cur.Load() != 1 {
		t.Errorf("an error answered: %d, by way %d", code, et.cur.Load())
	}

	own, err := newControlClient("https://203.0.113.5:47443", pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := own.hc.Transport.(*http.Transport); !ok {
		t.Errorf("someone's own server by %T", own.hc.Transport)
	}
}
