package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// The data directory's lock: one relay at a time on a data directory
// (two would answer devices with the same certificate and fight over the
// secret), and how install, status and cron tell whether it runs.
const (
	lockFile = "lock"
	pidFile  = "relay.pid"
)

// holdLock takes the data directory's lock for this process, and notes
// its pid; ok is false when another relay holds it.
func holdLock(dir string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if !tryLock(f) {
		f.Close()
		return nil, false, nil
	}
	pid := filepath.Join(dir, pidFile)
	os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	return func() {
		os.Remove(pid)
		f.Close() // the lock goes with it
	}, true, nil
}

// locked: a relay holds dir's lock.
func locked(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if tryLock(f) {
		return false // closing f lets it go again
	}
	return true
}

// logMax is how large the relay's own log grows before it rolls over; it
// keeps logKeep old ones, 30 MB in all (as the Docker container's log did).
var logMax int64 = 10 << 20

const logKeep = 2

// rollingLog is the relay's own log file, where no journal takes its
// output (OpenRC, cron): past logMax it becomes .1, .1 becomes .2, and a
// new one starts. The process's stderr follows it, so that a crash's
// report lands in the current file too.
type rollingLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openLog(path string) (*rollingLog, error) {
	l := &rollingLog{path: path}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *rollingLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, fi.Size()
	redirectStderr(f)
	return nil
}

func (l *rollingLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(b)) > logMax && l.size > 0 {
		l.roll()
	}
	n, err := l.f.Write(b)
	l.size += int64(n)
	return n, err
}

func (l *rollingLog) roll() {
	l.f.Close()
	for i := logKeep; i > 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", l.path, i-1), fmt.Sprintf("%s.%d", l.path, i))
	}
	os.Rename(l.path, l.path+".1")
	if err := l.open(); err != nil {
		// Nowhere to write: stderr, which goes wherever it went.
		l.f, l.size = os.Stderr, 0
	}
}
