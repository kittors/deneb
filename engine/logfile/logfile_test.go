package logfile

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRenameWhileOpen: a log Open opened can be renamed, and a new one put
// where it was, while it is still open and written to (on Windows, unlike
// a file os.OpenFile opened); what is written after goes on into the
// renamed file. Twice: the second time over the first's .1.
func TestRenameWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "denebd.log")
	for round := range 2 {
		f, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("one\n")
		if err := os.Rename(path, path+".1"); err != nil {
			f.Close()
			t.Fatalf("round %d: renaming the open log: %v", round, err)
		}
		f.WriteString("two\n")
		f.Close()
		if b, _ := os.ReadFile(path + ".1"); string(b) != "one\ntwo\n" {
			t.Fatalf("round %d: the renamed log has %q", round, b)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("round %d: something is left where the log was: %v", round, err)
		}
	}
}
