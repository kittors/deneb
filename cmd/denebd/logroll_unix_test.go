//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestRollLog: past its limit the log becomes .1, in place of the one
// before, and the descriptors that wrote to it write to a new file, for
// this user only; what was written before is in .1. Under the limit
// nothing moves.
func TestRollLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "denebd.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Dup(int(f.Fd())) // stands in for stdout and stderr
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// takeOverStd's way, on fd rather than on this test's own 1 and 2.
	takeOver := func(nf *os.File) error {
		defer nf.Close()
		return unix.Dup2(int(nf.Fd()), fd)
	}
	write := func(s string) {
		if _, err := unix.Write(fd, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return string(b)
	}
	os.WriteFile(path+".1", []byte("older\n"), 0o600)
	write("before\n")
	if rolled, err := rollIfLarge(path, 100, takeOver); rolled || err != nil {
		t.Fatalf("under the limit: rolled %v, %v", rolled, err)
	}
	write(strings.Repeat("x", 100) + "\n")
	if rolled, err := rollIfLarge(path, 100, takeOver); !rolled || err != nil {
		t.Fatalf("over the limit: rolled %v, %v", rolled, err)
	}
	write("after\n")
	if got := read(path); got != "after\n" {
		t.Errorf("the new log has %q", got)
	}
	if got := read(path + ".1"); !strings.HasPrefix(got, "before\n") || strings.Contains(got, "older") {
		t.Errorf("the old log has %q", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the new log: %v, %v", fi.Mode(), err)
	}
}
