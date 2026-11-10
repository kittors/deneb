//go:build windows

package cli

import (
	"time"

	"golang.org/x/sys/windows"
)

// waitExit waits up to d for process pid to end; true once it has (or
// when it cannot be opened: already gone).
func waitExit(pid int, d time.Duration) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, uint32(d/time.Millisecond))
	return err == nil && ev == windows.WAIT_OBJECT_0
}
