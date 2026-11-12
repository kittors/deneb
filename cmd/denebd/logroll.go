package main

import (
	"log"
	"os"
	"time"

	"github.com/kittors/deneb/engine/logfile"
)

// logMax is how large the log of an engine the window started may grow
// before it rolls over: 10 MB, as the Windows service's.
const logMax = 10 << 20

// logCheck is how often a running engine looks at its log's size.
const logCheck = 30 * time.Second

// rollLog keeps path, the log the window started this engine with (its
// stdout and stderr: localclient.EnsureRunning), under logMax. Past it, at
// the start or later on (an engine the window started may run for days),
// the log becomes path.1, the one before it goes, and a new path takes
// over stdout and stderr (takeOverStd): the log package's lines, anything
// else written there, and a crash's report all go on into the new one.
func rollLog(path string) {
	if !isStderr(path) {
		return // this engine does not write there: none of its business
	}
	check := func() {
		if rolled, err := rollIfLarge(path, logMax, takeOverStd); err != nil {
			log.Printf("[log] rolling %s over: %v", path, err)
		} else if rolled {
			log.Printf("[log] the log passed %d MB: what came before is in %s.1", logMax>>20, path)
		}
	}
	check()
	go func() {
		for range time.Tick(logCheck) {
			check()
		}
	}()
}

// isStderr: path is the file this process's stderr goes to.
func isStderr(path string) bool {
	a, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	b, err := os.Stat(path)
	return err == nil && os.SameFile(a, b)
}

// rollIfLarge rolls path over if it is larger than max: it becomes path.1,
// in place of the one before, and a new path is opened and handed to
// takeOver, to be written to where the old one was. Lines written meanwhile
// go to one or the other; none is lost.
func rollIfLarge(path string, max int64, takeOver func(*os.File) error) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= max {
		return false, err
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return false, err
	}
	f, err := logfile.Open(path)
	if err != nil {
		return false, err
	}
	if err := takeOver(f); err != nil {
		f.Close()
		return false, err
	}
	return true, nil
}
