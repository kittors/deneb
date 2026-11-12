//go:build !darwin && !linux && !windows

package main

import (
	"errors"
	"os"
)

// takeOverStd: no platform Deneb ships on lands here.
func takeOverStd(f *os.File) error {
	f.Close()
	return errors.ErrUnsupported
}
