//go:build !windows

package localclient

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// serve answers /v1/version on a socket at path, as an engine would.
func serve(t *testing.T, path, version string) {
	t.Helper()
	ln, err := socket.Listen(path, socket.ListenOptions{AllowUID: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"api":` + "1" + `,"engine":"` + version + `"}`))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// TestNotAllowedIsNotNotRunning: an engine this user may not use (the
// socket's permissions keep them out, as for another user's daemon) is
// ErrNotAllowed, not ErrNotRunning, so the window does not say the engine
// is down, nor start one of its own; and a system engine that refuses this
// user is not passed over for the user's own (M6).
func TestNotAllowedIsNotNotRunning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may connect whatever the permissions")
	}
	dir, err := os.MkdirTemp("", "dnb") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sys, user := filepath.Join(dir, "sys.sock"), filepath.Join(dir, "user.sock")
	serve(t, sys, "system")
	serve(t, user, "user")
	ctx := context.Background()

	if v, err := New(sys).Version(ctx); err != nil || v.Engine != "system" {
		t.Fatalf("an allowed engine: %+v, %v", v, err)
	}
	if err := os.Chmod(sys, 0); err != nil {
		t.Fatal(err)
	}
	_, err = New(sys).Version(ctx)
	if !errors.Is(err, ErrNotAllowed) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("a socket this user may not open: %v, want ErrNotAllowed", err)
	}
	if _, err := New(filepath.Join(dir, "none.sock")).Version(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("no engine: %v, want ErrNotRunning", err)
	}

	restore := socket.SetPathsForTest(sys, user)
	defer restore()
	var v *apitype.VersionInfo
	v, err = New("").Version(ctx)
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("the system engine refuses this user: %+v, %v; want ErrNotAllowed, not this user's own", v, err)
	}
	os.Chmod(sys, 0o600)
	if v, err := New("").Version(ctx); err != nil || v.Engine != "system" {
		t.Fatalf("the system engine allowed again: %+v, %v", v, err)
	}
}
