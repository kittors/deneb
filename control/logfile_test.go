package control

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLogFiles: the log goes into a file a day, only the server's to read;
// the files of the 30 days up to today stay, older ones go when the day
// changes; deneb-control logs reads the end back, across the days.
func TestLogFiles(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 11, 12, 23, 59, 0, 0, time.UTC)
	// Files from before: 31, 30 and 29 days back, and one that is no log.
	for _, back := range []int{31, 30, 29} {
		name := logFileName(day.AddDate(0, 0, -back).Format("20060102"))
		os.WriteFile(filepath.Join(dir, name), []byte("old\n"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0o600)

	var mu sync.Mutex
	now := day
	l, err := openLogFiles(dir, func() time.Time { mu.Lock(); defer mu.Unlock(); return now })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fmt.Fprintln(l, "a line on the 12th")
	mu.Lock()
	now = day.Add(2 * time.Minute) // the 13th
	mu.Unlock()
	fmt.Fprintln(l, "a line on the 13th")
	fmt.Fprintln(l, "another on the 13th")

	names := func() []string {
		entries, _ := os.ReadDir(dir)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	got := names()
	// On the 13th the 30 days are the 13th back to Oct 15th: the files of
	// Oct 12, 13 went (31 and 30 days before the 12th), Oct 14 too.
	want := []string{"deneb-control-20261112.log", "deneb-control-20261113.log", "notes.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("the files: %v, want %v", got, want)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "deneb-control-20261113.log")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("a log file is %v", fi.Mode())
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("the directory is %v", fi.Mode())
	}

	lines, err := lastLogLines(dir, 2, "")
	if err != nil || !slices.Equal(lines, []string{"a line on the 13th", "another on the 13th"}) {
		t.Fatalf("the last two: %q, %v", lines, err)
	}
	lines, _ = lastLogLines(dir, 10, "line on")
	if !slices.Equal(lines, []string{"a line on the 12th", "a line on the 13th"}) {
		t.Fatalf("the lines with 'line on', across the days: %q", lines)
	}

	// Through the admin socket.
	s, _ := newTestServer(t, "")
	s.cfg.LogDir = dir
	sock := serveAdmin(t, s)
	if got, err := AdminLogs(sock, 1, ""); err != nil || len(got) != 1 || got[0] != "another on the 13th" {
		t.Fatalf("deneb-control logs -n 1: %q, %v", got, err)
	}
	s.cfg.LogDir = ""
	if _, err := AdminLogs(sock, 1, ""); err == nil || !strings.Contains(err.Error(), "标准输出") {
		t.Fatalf("with no log files: %v", err)
	}
}
