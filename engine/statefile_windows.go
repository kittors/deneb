//go:build windows

package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// lockState takes the state directory for this engine alone: two engines on
// one state would be one device logged in twice, fighting over its keys and
// its port. The Windows service taking over from the user's own engine
// shares its state directory (M6), so the named pipe is no lock: the
// service runs as SYSTEM, and starts networking before it listens. The lock
// is engine.lock opened with no sharing, which Windows lets go with the
// process, crash or not. wait > 0 waits that long for the other engine to
// stop.
func lockState(dir string, wait time.Duration) (func(), error) {
	path := filepath.Join(dir, "engine.lock")
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return func() { windows.CloseHandle(h) }, nil
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w（%s）", ErrStateBusy, dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func chownToDir(path, dir string) {}
