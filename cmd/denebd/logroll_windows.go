package main

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
)

// takeOverStd makes f this process's stdout and stderr: the handles Windows
// gives out for them, which Go's runtime writes a crash's report to (it
// asks for them each time), the os package's, and the log package's. The
// old ones, on the file now renamed .1, are closed: the window opened it
// so that it could be renamed while open (logfile.Open).
func takeOverStd(f *os.File) error {
	h := windows.Handle(f.Fd())
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h); err != nil {
		return err
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, h); err != nil {
		return err
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = f, f
	log.SetOutput(f)
	if oldOut != nil && oldOut != f {
		oldOut.Close()
	}
	if oldErr != nil && oldErr != f && oldErr != oldOut {
		oldErr.Close()
	}
	return nil
}
