package localclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestEnsureRunning starts a real engine detached and talks to it. It needs
// a built denebd: DENEB_TEST_DENEBD=/path/to/denebd.
func TestEnsureRunning(t *testing.T) {
	bin := os.Getenv("DENEB_TEST_DENEBD")
	if bin == "" {
		t.Skip("set DENEB_TEST_DENEBD to a built denebd")
	}
	dir, _ := os.MkdirTemp("", "dnbsp")
	defer os.RemoveAll(dir)
	c := New(filepath.Join(dir, "d.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.EnsureRunning(ctx, SpawnOptions{Binary: bin, Args: []string{"run", "-state", dir, "-socks=", "-bind-if", "none"}, LogPath: filepath.Join(dir, "denebd.log")})
	if err != nil {
		b, _ := os.ReadFile(filepath.Join(dir, "denebd.log"))
		t.Fatalf("%v\n%s", err, b)
	}
	defer exec.Command("pkill", "-f", "run -state "+dir).Run()
	st, err := c.State(ctx)
	if err != nil || st.LoggedIn || st.Mode != "userspace" {
		t.Fatalf("state %+v %v", st, err)
	}
	// A second call finds it running and starts nothing.
	if err := c.EnsureRunning(ctx, SpawnOptions{Binary: "/nonexistent"}); err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
}
