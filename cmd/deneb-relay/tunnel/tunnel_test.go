package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"tailscale.com/derp"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/wsconn"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// TestDERPThroughTheTunnel: two devices reach each other through the
// tunnel port, DERP inside the WebSocket, through the same derpserver; its
// admission is asked about each from the address in its CF-Connecting-IP.
func TestDERPThroughTheTunnel(t *testing.T) {
	// Cleanups run last first: the devices hang up, then the servers close.
	d := derpserver.New(key.NewNode(), t.Logf)
	t.Cleanup(func() { d.Close() })
	var mu sync.Mutex
	asked := map[key.NodePublic]netip.Addr{}
	admission := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req tailcfg.DERPAdmitClientRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		asked[req.NodePublic] = req.Source
		mu.Unlock()
		json.NewEncoder(w).Encode(tailcfg.DERPAdmitClientResponse{Allow: true})
	}))
	t.Cleanup(admission.Close)
	d.SetVerifyClientURL(admission.URL)
	srv := httptest.NewServer(Handler(ServerAccept(d), t.Logf))
	t.Cleanup(srv.Close)

	dial := func(cf string) (*derp.Client, key.NodePrivate) {
		t.Helper()
		priv := key.NewNode()
		c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http")+Path, &websocket.DialOptions{
			Subprotocols: []string{Subprotocol},
			HTTPHeader:   http.Header{"CF-Connecting-IP": {cf}},
		})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { c.CloseNow() })
		nc := wsconn.NetConn(t.Context(), c, websocket.MessageBinary, "")
		dc, err := derp.NewClient(priv, nc, bufio.NewReadWriter(bufio.NewReader(nc), bufio.NewWriter(nc)), t.Logf)
		if err != nil {
			t.Fatalf("DERP: %v", err)
		}
		return dc, priv
	}
	a, ak := dial("203.0.113.7")
	b, bk := dial("2001:db8::8")

	// Each sends to the other until it comes through: the server takes a
	// client once its admission answers.
	got := make(chan string, 2)
	recv := func(c *derp.Client, from key.NodePublic) {
		for {
			m, err := c.Recv()
			if err != nil {
				return
			}
			if p, ok := m.(derp.ReceivedPacket); ok && p.Source == from {
				got <- string(p.Data)
				return
			}
		}
	}
	go recv(b, ak.Public())
	go recv(a, bk.Public())
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; n < 2; {
		a.Send(bk.Public(), []byte("from a"))
		b.Send(ak.Public(), []byte("from b"))
		select {
		case m := <-got:
			if m != "from a" && m != "from b" {
				t.Fatalf("got %q", m)
			}
			n++
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of 2 packets came through", n)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if asked[ak.Public()] != netip.MustParseAddr("203.0.113.7") || asked[bk.Public()] != netip.MustParseAddr("2001:db8::8") {
		t.Fatalf("admission was asked about %v", asked)
	}
}

// TestRefused: the tunnel port is for cloudflared on this machine, and for
// DERP over WebSocket only.
func TestRefused(t *testing.T) {
	served := false
	h := Handler(func(context.Context, net.Conn, string) { served = true }, t.Logf)
	ask := func(path, remote string, header ...string) int {
		r := httptest.NewRequest("GET", "http://127.0.0.1:47451"+path, nil)
		r.RemoteAddr = remote
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	ws := []string{"Upgrade", "websocket", "Connection", "Upgrade", "Sec-WebSocket-Version", "13", "Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Protocol", Subprotocol}
	for name, c := range map[string]struct {
		path, remote string
		header       []string
		want         int
	}{
		"no CF-Connecting-IP":   {Path, "127.0.0.1:51000", ws, http.StatusForbidden},
		"not from this machine": {Path, "198.51.100.4:51000", append([]string{"CF-Connecting-IP", "203.0.113.9"}, ws...), http.StatusForbidden},
		"not a WebSocket":       {Path, "127.0.0.1:51000", []string{"CF-Connecting-IP", "203.0.113.9"}, http.StatusUpgradeRequired},
		"another subprotocol":   {Path, "127.0.0.1:51000", append([]string{"CF-Connecting-IP", "203.0.113.9"}, append(ws[:len(ws)-1:len(ws)-1], "chat")...), http.StatusUpgradeRequired},
		"another path":          {"/derp/probe", "127.0.0.1:51000", append([]string{"CF-Connecting-IP", "203.0.113.9"}, ws...), http.StatusNotFound},
	} {
		if got := ask(c.path, c.remote, c.header...); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	if served {
		t.Fatal("a refused request reached the DERP server")
	}
}

func TestCheck(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:47451": true, "[::1]:47451": true, "0.0.0.0:47451": false, ":47451": false, "198.51.100.4:47451": false} {
		if err := CheckListen(addr); (err == nil) != ok {
			t.Errorf("CheckListen(%q) = %v", addr, err)
		}
	}
	for h, ok := range map[string]bool{"relay.deneb.app": true, "hk-relay.example.com": true, "https://relay.deneb.app": false, "relay.deneb.app:443": false,
		"relay.deneb.app/derp": false, "localhost": false, "Relay.Deneb.App": false, "": false} {
		if err := CheckHost(h); (err == nil) != ok {
			t.Errorf("CheckHost(%q) = %v", h, err)
		}
	}
}
