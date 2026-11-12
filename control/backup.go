package control

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Backups: once a day the server writes a consistent copy of its database
// into <data>/backup, keeping a week of daily copies and two months of
// weekly ones (M7). A copy is only the server's to read, and has no signing
// key in it: restored, the server makes a new key, and every device signs
// in again by itself with its machine key (as when the key was changed on
// 2026-11-09), so a copy that gets out cannot be used to pass for a
// device. Copying them off the machine is done from outside
// (design/plan/m7/control-ops.md).

const (
	// backupsDaily is how many of the newest daily copies stay; older
	// ones stay one a week (the newest of each week), backupsWeekly of
	// them.
	backupsDaily  = 7
	backupsWeekly = 8
	// BackupDir is the backups' directory in the data directory.
	BackupDir = "backup"
)

// RunBackups writes the day's copy at start, if it is missing, and checks
// every hour after; once a day it also runs the cleanup.
func (s *Server) RunBackups(ctx context.Context) {
	cleanedOn := ""
	for {
		name, err := s.backup()
		if err != nil {
			s.logf("backup: %v", err)
		} else if name != "" {
			s.logf("backup: wrote %s", name)
		}
		s.health.mu.Lock()
		s.health.backupErr = err
		s.health.mu.Unlock()
		// The daily cleanup (cleanup.go), once a day as the backup is,
		// and after it: the day's copy still has what it removes, for a
		// rollback of the cleanup itself.
		if day := s.now().UTC().Format("20060102"); day != cleanedOn {
			if _, err := s.cleanup(); err != nil {
				s.logf("cleanup: %v", err)
			} else {
				cleanedOn = day
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// backupName is the day's copy's file name, by the date in UTC.
func backupName(t time.Time) string { return "deneb-" + t.UTC().Format("20060102") + ".db" }

// backup writes the day's copy if there is none yet, and returns its name
// ("" when it was there already). It goes into a temporary file first and
// only takes its name once it checked out, so a copy cut short by a crash
// never stands for the day.
func (s *Server) backup() (string, error) {
	dir := filepath.Join(s.cfg.DataDir, BackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	name := backupName(s.now())
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", nil
	}
	tmp := path + ".tmp"
	os.Remove(tmp) // a copy cut short before
	// VACUUM INTO writes a consistent copy while the server keeps running.
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := finishBackup(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	pruneBackups(dir, s.now())
	return name, nil
}

// finishBackup readies a fresh copy: no one else may read it, the signing
// key leaves it, and it must check out whole.
func finishBackup(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	// secure_delete overwrites the key's bytes rather than leaving them in
	// a free page; with the journal in memory no file keeps them either.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=secure_delete(1)&_pragma=journal_mode(MEMORY)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`DELETE FROM settings WHERE key = 'secret'`); err != nil {
		return fmt.Errorf("taking the signing key out: %w", err)
	}
	var check string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("the copy does not check out: %s", check)
	}
	if err := db.Close(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// pruneBackups keeps the newest backupsDaily copies, and of the older ones
// the newest of each week, backupsWeekly weeks of them. Those it keeps are
// made only the server's to read, copies from before M7 too, which were
// written 0644.
func pruneBackups(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var copies []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "deneb-") && strings.HasSuffix(n, ".db") {
			copies = append(copies, n)
		}
	}
	drop := backupsToDrop(copies)
	for _, n := range copies {
		path := filepath.Join(dir, n)
		if slices.Contains(drop, n) {
			os.Remove(path)
		} else if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
			os.Chmod(path, 0o600)
		}
	}
}

// backupsToDrop is which of the copies (deneb-YYYYMMDD.db) go: all but the
// newest backupsDaily, and the newest of each ISO week before them, up to
// backupsWeekly weeks. A name that is not a date is left alone.
func backupsToDrop(copies []string) []string {
	type dated struct {
		name string
		day  time.Time
	}
	var ds []dated
	for _, n := range copies {
		d, err := time.Parse("20060102", strings.TrimSuffix(strings.TrimPrefix(n, "deneb-"), ".db"))
		if err == nil {
			ds = append(ds, dated{n, d})
		}
	}
	// Newest first.
	slices.SortFunc(ds, func(a, b dated) int { return b.day.Compare(a.day) })
	var drop []string
	weeks := map[string]bool{}
	for i, d := range ds {
		if i < backupsDaily {
			continue
		}
		y, w := d.day.ISOWeek()
		week := fmt.Sprintf("%d-%02d", y, w)
		if !weeks[week] && len(weeks) < backupsWeekly {
			weeks[week] = true
			continue
		}
		drop = append(drop, d.name)
	}
	return drop
}

// Backup is one copy, as deneb-control backups lists it.
type Backup struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Written time.Time `json:"written"`
}

// backups lists the copies, newest first.
func (s *Server) backups() ([]Backup, error) {
	dir := filepath.Join(s.cfg.DataDir, BackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	out := []Backup{}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "deneb-") || !strings.HasSuffix(n, ".db") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: n, Size: fi.Size(), Written: fi.ModTime().UTC()})
	}
	slices.SortFunc(out, func(a, b Backup) int { return strings.Compare(b.Name, a.Name) })
	return out, nil
}
