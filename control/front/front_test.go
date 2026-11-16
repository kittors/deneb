package front

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestHeader(t *testing.T) {
	for _, src := range []string{"203.0.113.7:51234", "[2001:db8::7]:443", "[::ffff:198.51.100.2]:80"} {
		from := netip.MustParseAddrPort(src)
		b := Header(from, netip.MustParseAddrPort("198.51.100.1:47444"))
		r := bufio.NewReader(io.MultiReader(bytes.NewReader(b), strings.NewReader("rest")))
		got, err := ReadHeader(r)
		want := netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if rest, _ := io.ReadAll(r); err != nil || got != want || string(rest) != "rest" {
			t.Fatalf("%s: %v %v, rest %q", src, got, err, rest)
		}
	}
	// v1, as nginx and haproxy send it; and the headers that name no one.
	for in, want := range map[string]string{
		"PROXY TCP4 203.0.113.7 198.51.100.1 51234 47444\r\n": "203.0.113.7:51234",
		"PROXY TCP6 2001:db8::7 2001:db8::1 443 47444\r\n":    "[2001:db8::7]:443",
		"PROXY UNKNOWN\r\n":                    "invalid AddrPort",
		string(signature) + "\x20\x00\x00\x00": "invalid AddrPort", // v2 LOCAL
	} {
		got, err := ReadHeader(bufio.NewReader(strings.NewReader(in + "rest")))
		if err != nil || got.String() != want {
			t.Fatalf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"\x16\x03\x01\x02\x00\x01\x00\x01\xfc\x03\x03", "PROXY TCP4 nonsense\r\n", "GET / HTTP/1.1\r\n\r\n",
		string(signature) + "\x22\x11\x00\x0c" + strings.Repeat("\x00", 12), string(signature) + "\x21\x11\x00\x04abcd"} {
		if got, err := ReadHeader(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Fatalf("%q read as %v", in, got)
		}
	}
}

// TestListener: TLS through a front is the control server's own, and its
// requests are from whom the header names. A stranger's connection is
// closed before any of it is read.
func TestListener(t *testing.T) {
	serve := func(from string) (*httptest.Server, string) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, r.RemoteAddr)
		}))
		addr := srv.Listener.Addr().String()
		srv.Listener = Listener(srv.Listener, []netip.Prefix{netip.MustParsePrefix(from)}, t.Logf)
		srv.EnableHTTP2 = true
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv, addr
	}
	get := func(addr string, header []byte) (string, error) {
		tr := &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: true,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err == nil {
					_, err = c.Write(header)
				}
				return c, err
			},
		}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr}).Get("https://control.example/")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.Proto + " " + string(b), err
	}
	_, addr := serve("127.0.0.0/8")
	device := netip.MustParseAddrPort("203.0.113.7:4321")
	if got, err := get(addr, Header(device, netip.MustParseAddrPort("127.0.0.1:47444"))); err != nil || got != "HTTP/2.0 203.0.113.7:4321" {
		t.Fatalf("through the front: %q %v", got, err)
	}
	// A front's own check names no one: its own address stands.
	if got, err := get(addr, append(append([]byte{}, signature...), 0x20, 0, 0, 0)); err != nil || !strings.HasPrefix(got, "HTTP/2.0 127.0.0.1:") {
		t.Fatalf("a front's check: %q %v", got, err)
	}
	if got, err := get(addr, nil); err == nil {
		t.Fatalf("no header: %q", got)
	}
	_, other := serve("10.0.0.0/8")
	if got, err := get(other, Header(device, netip.MustParseAddrPort("127.0.0.1:47444"))); err == nil {
		t.Fatalf("from a stranger: %q", got)
	}
}

func TestTunnelHandler(t *testing.T) {
	var seen *http.Request
	h := TunnelHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r }))
	ask := func(remote, cf string) int {
		seen = nil
		r := httptest.NewRequest("POST", "http://127.0.0.1:47450/device/v1/report", nil)
		r.RemoteAddr = remote
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		r.Header.Set("X-Real-IP", "192.0.2.66")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := ask("127.0.0.1:51000", "203.0.113.9"); code != 200 || seen == nil || seen.RemoteAddr != "203.0.113.9:0" || seen.Header.Get("X-Real-IP") != "" {
		t.Fatalf("from cloudflared: %d %+v", code, seen)
	}
	if code := ask("[::1]:51000", "2001:db8::9"); code != 200 || seen.RemoteAddr != "[2001:db8::9]:0" {
		t.Fatalf("over IPv6: %d", code)
	}
	if code := ask("198.51.100.4:51000", "203.0.113.9"); code != http.StatusForbidden || seen != nil {
		t.Fatalf("not from this machine: %d", code)
	}
	if code := ask("127.0.0.1:51000", ""); code != http.StatusForbidden || seen != nil {
		t.Fatalf("no CF-Connecting-IP: %d", code)
	}
}
