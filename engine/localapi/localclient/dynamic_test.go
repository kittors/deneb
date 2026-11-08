package localclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// A client made with New("") follows its engine: to the system endpoint
// while a kernel-mode engine answers there, back to the user's own after it
// stops, and past a socket file nobody answers on.
func TestDefaultClientFollowsEngine(t *testing.T) {
	t.Setenv(socket.EnvPath, "")
	dir, err := os.MkdirTemp("", "dnb") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sys, user := filepath.Join(dir, "s.sock"), filepath.Join(dir, "u.sock")
	defer socket.SetPathsForTest(sys, user)()

	serve := func(path, who string) func() {
		f := fake.New()
		f.Update(func(s *apitype.State) { s.SelfDeviceID = apitype.DeviceID(who) })
		stop, err := f.Listen(path)
		if err != nil {
			t.Fatal(err)
		}
		return stop
	}
	c := New("")
	ctx := context.Background()
	who := func() string {
		t.Helper()
		st, err := c.State(ctx)
		if err != nil {
			t.Fatalf("state: %v", err)
		}
		return string(st.SelfDeviceID)
	}

	stopUser := serve(user, "user")
	defer stopUser()
	if got := who(); got != "user" || c.Path() != user {
		t.Fatalf("with only the user engine: %s on %s", got, c.Path())
	}
	stopSys := serve(sys, "kernel")
	if got := who(); got != "kernel" || c.Path() != sys {
		t.Fatalf("with a kernel-mode engine: %s on %s", got, c.Path())
	}
	stopSys()
	if got := who(); got != "user" {
		t.Fatalf("after the kernel-mode engine stopped: %s", got)
	}
	// A crashed kernel-mode engine leaves its socket file behind.
	if err := os.WriteFile(sys, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := who(); got != "user" || c.Path() != user {
		t.Fatalf("past a dead system socket: %s on %s", got, c.Path())
	}
	if c.spawnPath() != user {
		t.Errorf("would start an engine on %s, want the user's endpoint", c.spawnPath())
	}

	t.Setenv(socket.EnvPath, sys)
	if p := New("").Path(); p != sys {
		t.Errorf("$DENEB_SOCKET ignored: %s", p)
	}
}
