package control

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// backupsKept is how many daily copies of the database stay on disk.
const backupsKept = 7

// RunBackups copies the database into <data>/backup once a day (and once
// at the start, if today's copy is missing), keeping the last few. Copying
// them off the machine is done from outside: scp the newest file
// (implementation.md §2.2).
func (s *Server) RunBackups(ctx context.Context) {
	for {
		if err := s.backup(); err != nil {
			s.logf("backup: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func (s *Server) backup() error {
	dir := filepath.Join(s.cfg.DataDir, "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(dir, "deneb-"+s.now().UTC().Format("20060102")+".db")
	if _, err := os.Stat(name); err == nil {
		return nil
	}
	// VACUUM INTO writes a consistent copy while the server keeps running.
	if _, err := s.db.Exec(`VACUUM INTO ?`, name); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var copies []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "deneb-") && strings.HasSuffix(e.Name(), ".db") {
			copies = append(copies, e.Name())
		}
	}
	slices.Sort(copies)
	for len(copies) > backupsKept {
		os.Remove(filepath.Join(dir, copies[0]))
		copies = copies[1:]
	}
	return nil
}
