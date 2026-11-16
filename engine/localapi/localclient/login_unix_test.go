//go:build !windows

package localclient

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kittors/deneb/engine/localapi/socket"
)

// TestLoginLinks: Login is the link for a browser, the Web version's page
// when the server has one; LoginLink the control server's own, for the
// window's sign-in. An older server or engine has one link: both are it.
func TestLoginLinks(t *testing.T) {
	for _, c := range []struct {
		name, answer, browser, link string
	}{
		{"with the Web version", `{"url":"https://203.0.113.10:47443/login?c=x","browserURL":"https://web.example.com/device-login?c=x"}`,
			"https://web.example.com/device-login?c=x", "https://203.0.113.10:47443/login?c=x"},
		{"an older one", `{"url":"https://203.0.113.10:47443/login?c=x"}`,
			"https://203.0.113.10:47443/login?c=x", "https://203.0.113.10:47443/login?c=x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "dnb") // short: socket paths are limited
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			path := filepath.Join(dir, "d.sock")
			ln, err := socket.Listen(path, socket.ListenOptions{AllowUID: -1})
			if err != nil {
				t.Fatal(err)
			}
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(c.answer))
			})}
			go srv.Serve(ln)
			t.Cleanup(func() { srv.Close() })
			lc := New(path)
			if got, err := lc.Login(context.Background(), ""); err != nil || got != c.browser {
				t.Errorf("Login: %q %v, want %q", got, err, c.browser)
			}
			if got, err := lc.LoginLink(context.Background(), ""); err != nil || got != c.link {
				t.Errorf("LoginLink: %q %v, want %q", got, err, c.link)
			}
		})
	}
}
