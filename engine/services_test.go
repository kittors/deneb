package engine

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

func listenBanner(t *testing.T, banner string) string {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			fmt.Fprint(c, banner)
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestProbeService(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) }))
	defer web.Close()
	airplay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "AirTunes/980.77.5")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer airplay.Close()
	tlsWeb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tlsWeb.Close()
	host := func(u string) string { return strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://") }

	cases := []struct {
		name string
		addr string
		kind proto.ServiceKind
		port uint16
		want bool
	}{
		{"a web app", host(web.URL), proto.ServiceWeb, 8080, true},
		{"AirPlay on 5000 is not a website", host(airplay.URL), proto.ServiceWeb, 5000, false},
		{"https", host(tlsWeb.URL), proto.ServiceWeb, 443, true},
		{"plain HTTP is not https", host(web.URL), proto.ServiceWeb, 443, false},
		{"sshd", listenBanner(t, "SSH-2.0-OpenSSH_9.8\r\n"), proto.ServiceShell, 22, true},
		{"something else on 22", listenBanner(t, "220 ftp ready\r\n"), proto.ServiceShell, 22, false},
		{"screen sharing", listenBanner(t, "RFB 003.889\n"), proto.ServiceScreen, 5900, true},
		{"nothing listening", "127.0.0.1:1", proto.ServiceFiles, 445, false},
	}
	for _, c := range cases {
		if got := probeService(c.addr, c.kind, c.port); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
