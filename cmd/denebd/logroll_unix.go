//go:build darwin || linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// takeOverStd puts f on stdout and stderr (descriptors 1 and 2), in place
// of the log there before, and closes f: the descriptors hold it.
func takeOverStd(f *os.File) error {
	defer f.Close()
	for _, fd := range []int{1, 2} {
		if err := unix.Dup2(int(f.Fd()), fd); err != nil {
			return err
		}
	}
	return nil
}
