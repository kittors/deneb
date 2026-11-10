//go:build !windows

package cli

import (
	"errors"
	"syscall"
	"time"
)

// waitExit waits up to d for process pid to end; true once it has.
func waitExit(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
