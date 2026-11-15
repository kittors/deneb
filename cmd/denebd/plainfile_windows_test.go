//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestReadPlainWindows: a plain file on this computer's drive is read; a
// hard link, a file too big, a directory, a path through a junction or a
// symbolic link, a share or a device path is refused. Windows has no FIFO
// with a name on a drive: its pipes are device paths (\\.\pipe\…).
func TestReadPlainWindows(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("deneb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := readPlain(plain, 1<<10); err != nil || string(b) != "deneb" {
		t.Fatalf("a plain file: %q %v", b, err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("a private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hard")
	if err := os.Link(secret, hard); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(2 << 10)
	f.Close()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "denebd.exe"), []byte("deneb"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, path string }{
		{"hard link", hard},
		{"too big", big},
		{"directory", real},
		{"share", `\\example.com\share\Deneb\denebd.exe`},
		{"share by its device path", `\\?\UNC\example.com\share\denebd.exe`},
		{"device path", `\\?\` + filepath.Join(real, "denebd.exe")},
		{"pipe", `\\.\pipe\denebd`},
	}
	junction := filepath.Join(dir, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, real).CombinedOutput(); err == nil {
		cases = append(cases, struct{ name, path string }{"through a junction", filepath.Join(junction, "denebd.exe")})
	} else {
		t.Logf("no junction here (%v %s): not tried", err, out)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(secret, link); err == nil {
		cases = append(cases, struct{ name, path string }{"symbolic link", link})
	} else {
		t.Logf("no symbolic link for this account (%v): not tried", err)
	}
	for _, c := range cases {
		if _, err := readPlain(c.path, 1<<10); !errors.Is(err, errNotPlain) {
			t.Errorf("%s: %v, want it refused", c.name, err)
		}
	}
	if _, err := readPlain(filepath.Join(dir, "none"), 1<<10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no file: %v, want not-exist", err)
	}
}

// TestSelfUpdateRefusesShares: a window naming an engine on a share or by
// a device path gets bad-signature before the service reads anything or
// writes anything beside itself.
func TestSelfUpdateRefusesShares(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Dir(self))
	for _, src := range []string{
		`\\example.com\share\Deneb\denebd.exe`,
		`\\?\UNC\example.com\share\Deneb\denebd.exe`,
		`\\?\C:\Deneb\denebd.exe`,
		`\\.\C:\Deneb\denebd.exe`,
	} {
		if out := selfUpdate(context.Background(), src); out.Result != apitype.UpdateBadSignature {
			t.Errorf("%s: %+v, want %s", src, out, apitype.UpdateBadSignature)
		}
	}
	if after, _ := os.ReadDir(filepath.Dir(self)); len(after) != len(before) {
		t.Errorf("beside the service: %d files, then %d", len(before), len(after))
	}
}

// TestWritePrivate: what the service writes before Windows checks its
// signature only SYSTEM and administrators may open.
func TestWritePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wintun.update.dll")
	for range 2 { // the second time over the first one's
		if err := writePrivate(path, []byte("wintun")); err != nil {
			t.Fatal(err)
		}
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	got := sd.String()
	if !strings.HasPrefix(got, "D:P") || strings.Count(got, "(") != 2 || !strings.Contains(got, "(A;;FA;;;SY)") || !strings.Contains(got, "(A;;FA;;;BA)") {
		t.Errorf("who may open it: %s, want SYSTEM and administrators only", got)
	}
}
