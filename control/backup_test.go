package control

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestBackup: the day's copy is consistent, only the server's to read, and
// has no signing key in it, not even its bytes; a second try the same day
// writes nothing; a copy cut short before is no obstacle. Restored, the
// server makes a new key and a device signs in again with its machine key.
func TestBackup(t *testing.T) {
	s, ts, clk := newClockServer(t, 0)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN

	dir := filepath.Join(s.cfg.DataDir, BackupDir)
	os.MkdirAll(dir, 0o755)
	stale := filepath.Join(dir, backupName(clk.now())+".tmp")
	os.WriteFile(stale, []byte("cut short"), 0o644)
	name, err := s.backup()
	if err != nil || name != backupName(clk.now()) {
		t.Fatalf("backup: %q, %v", name, err)
	}
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the copy: %v, %v", fi.Mode(), err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("the backup directory: %v", fi.Mode())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the copy cut short is still there")
	}
	// For trying deploy/control/restore-drill.sh here: a copy to drill on.
	if out := os.Getenv("DENEB_BACKUP_OUT"); out != "" {
		if err := copyFile(path, out); err != nil {
			t.Fatal(err)
		}
	}
	if again, err := s.backup(); err != nil || again != "" {
		t.Fatalf("a second backup the same day: %q, %v", again, err)
	}

	// The key is in the live database and nowhere in the copy. (Never print
	// it: compare only.)
	key, ok, err := getSetting(s.db, "secret")
	if err != nil || !ok || key == "" {
		t.Fatal("the live database has no signing key")
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(key)) {
		t.Fatal("the signing key's bytes are in the copy")
	}
	cp, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var keys, lans int
	var check string
	cp.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'secret'`).Scan(&keys)
	cp.QueryRow(`SELECT COUNT(*) FROM lans WHERE id = ?`, studio).Scan(&lans)
	cp.QueryRow(`PRAGMA integrity_check`).Scan(&check)
	cp.Close()
	if keys != 0 || lans != 1 || check != "ok" {
		t.Fatalf("the copy: %d keys, %d of the LAN, %s", keys, lans, check)
	}

	// Restored: a new key, so the old token is no good, and the device gets
	// a new one with its machine key, as an engine does on 401.
	restored := t.TempDir()
	if err := copyFile(path, filepath.Join(restored, "deneb.db")); err != nil {
		t.Fatal(err)
	}
	s2, ts2 := newTestServer(t, restored)
	key2, _, _ := getSetting(s2.db, "secret")
	if key2 == "" || key2 == key {
		t.Fatal("the restored server did not make a key of its own")
	}
	mac.url = ts2.URL
	if _, err := mac.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrUnauthorized {
		t.Fatalf("the old token on the restored server: %v", err)
	}
	mac.auth()
	if v := mac.view(); lanByName(v, "工作室") == nil {
		t.Fatalf("after signing in again: %+v", v.LANs)
	}
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o600)
}

// TestBackupsKept: a week of daily copies, then the newest of each week,
// eight weeks of them.
func TestBackupsKept(t *testing.T) {
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	var names []string
	for i := range 100 {
		names = append(names, backupName(day.AddDate(0, 0, -i)))
	}
	names = append(names, "deneb-notadate.db")
	drop := backupsToDrop(names)
	var kept []string
	for _, n := range names {
		if !slices.Contains(drop, n) {
			kept = append(kept, n)
		}
	}
	if len(kept) != backupsDaily+backupsWeekly+1 {
		t.Fatalf("kept %d: %v", len(kept), kept)
	}
	for i := range backupsDaily {
		if !slices.Contains(kept, backupName(day.AddDate(0, 0, -i))) {
			t.Fatalf("day %d went", i)
		}
	}
	weeks := map[string]bool{}
	for _, n := range kept[backupsDaily:] {
		d, err := time.Parse("20060102", n[len("deneb-"):len("deneb-")+8])
		if err != nil {
			continue
		}
		y, w := d.ISOWeek()
		if k := fmt.Sprint(y, w); weeks[k] {
			t.Fatalf("two copies from week %s", k)
		} else {
			weeks[k] = true
		}
	}
	if !slices.Contains(kept, "deneb-notadate.db") {
		t.Fatal("a file that is not a dated copy went")
	}
}
