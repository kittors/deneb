package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// Two engines on one state would be one device logged in twice: the second
// is refused, or, when told to wait, starts once the first has stopped
// (a kernel-mode engine taking over from the user-space one).
func TestStateLock(t *testing.T) {
	dir := t.TempDir()
	opts := Options{StateDir: dir, Mode: apitype.ModeUserspace, BindInterface: "none", Logf: t.Logf, Version: "test"}
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(opts); !errors.Is(err, ErrStateBusy) {
		t.Fatalf("second engine on the same state: %v, want ErrStateBusy", err)
	}
	if err := UndoKernel(dir, t.Logf); !errors.Is(err, ErrStateBusy) {
		t.Fatalf("kernel-undo while an engine runs: %v, want ErrStateBusy", err)
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		a.Close()
	}()
	wait := opts
	wait.LockWait = 5 * time.Second
	start := time.Now()
	b, err := New(wait)
	if err != nil {
		t.Fatalf("waiting engine: %v", err)
	}
	defer b.Close()
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Errorf("took over after %v, want just after the first stopped", d)
	}
	b.Close()
	b.Close() // twice is fine
	if err := UndoKernel(dir, t.Logf); err != nil {
		t.Errorf("kernel-undo with no engine: %v", err)
	}
}
