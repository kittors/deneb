package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/logfile"
)

// TestRollLogWhileOpen: a log still open where it is written, as the
// engine's stdout and stderr are (the window opens it with logfile.Open),
// rolls over all the same: on Windows too, where a file Go opens itself
// cannot be renamed while open. The writer goes on in the new file once it
// takes it over; what came before is in .1, in place of the one before.
func TestRollLogWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "denebd.log")
	w, err := logfile.Open(path) // stands in for the engine's stderr
	if err != nil {
		t.Fatal(err)
	}
	defer func() { w.Close() }()
	if err := os.WriteFile(path+".1", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.WriteString("before\n" + strings.Repeat("x", 100) + "\n")
	rolled, err := rollIfLarge(path, 100, func(f *os.File) error {
		old := w
		w = f
		return old.Close()
	})
	if !rolled || err != nil {
		t.Fatalf("rolled %v, %v", rolled, err)
	}
	w.WriteString("after\n")
	if b, _ := os.ReadFile(path); string(b) != "after\n" {
		t.Errorf("the new log has %q", b)
	}
	if b, _ := os.ReadFile(path + ".1"); !strings.HasPrefix(string(b), "before\n") || strings.Contains(string(b), "older") {
		t.Errorf("the old log has %q", b)
	}
}

// TestRollLogOnlyItsOwn: a file the engine's stderr does not go to is
// never moved, however large.
func TestRollLogOnlyItsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "denebd.log")
	os.WriteFile(path, make([]byte, 1024), 0o600)
	if isStderr(path) {
		t.Fatal("a temporary file taken for stderr")
	}
	rollLog(path)
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("rolled a file it does not write to")
	}
}
