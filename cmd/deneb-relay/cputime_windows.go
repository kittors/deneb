package main

import (
	"time"

	"golang.org/x/sys/windows"
)

// cpuTime is the CPU time the process used, user and system: Windows has
// no getrusage, and counts it in 100 ns units.
func cpuTime() time.Duration {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return 0
	}
	ft := func(t windows.Filetime) time.Duration {
		return time.Duration(uint64(t.HighDateTime)<<32|uint64(t.LowDateTime)) * 100
	}
	return ft(kernel) + ft(user)
}
