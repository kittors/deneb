//go:build !windows

package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockState takes the state directory for this engine alone: two engines on
// one state would be one device logged in twice, fighting over its keys and
// its port. The lock is a flock on engine.lock, so it goes with the process,
// crash or not. wait > 0 waits that long for the other engine to stop.
func lockState(dir string, wait time.Duration) (func(), error) {
	path := filepath.Join(dir, "engine.lock")
	// Opened read-only and readable by all: a root engine and the user's own
	// engine take turns on the same file, whichever of them created it.
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	chownToDir(path, dir)
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w（%s）", ErrStateBusy, dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// chownToDir gives a file that a root engine wrote to the owner of the
// state directory. Outside root, or on root's own state, it does nothing.
func chownToDir(path, dir string) {
	if os.Geteuid() != 0 {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return
	}
	os.Chown(path, int(st.Uid), int(st.Gid))
}
