//go:build !windows

package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// within runs fn, failing the test if it has not returned in 5 seconds: a
// FIFO opened to wait for its writer would hang.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: still waiting", what)
	}
}

// smallLimit makes maxEngineSize 4 KB for the test.
func smallLimit(t *testing.T) {
	old := maxEngineSize
	maxEngineSize = 4 << 10
	t.Cleanup(func() { maxEngineSize = old })
}

// bigFile writes a file of size zero bytes at path.
func bigFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

// TestReadPlain: a regular file with no other name, not reached through a
// link at its end, not too big, is read; a symbolic or hard link to a file
// only its owner may read, a FIFO, a file too big, a directory or a device
// is refused, the FIFO at once.
func TestReadPlain(t *testing.T) {
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
	link, hard, fifo, big := filepath.Join(dir, "link"), filepath.Join(dir, "hard"), filepath.Join(dir, "fifo"), filepath.Join(dir, "big")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, hard); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	bigFile(t, big, 2<<10)
	for _, c := range []struct{ name, path string }{
		{"symbolic link", link},
		{"hard link", hard},
		{"FIFO", fifo},
		{"too big", big},
		{"directory", dir},
		{"device", "/dev/zero"},
	} {
		var err error
		within(t, c.name, func() { _, err = readPlain(c.path, 1<<10) })
		if !errors.Is(err, errNotPlain) {
			t.Errorf("%s: %v, want it refused", c.name, err)
		}
	}
	if _, err := readPlain(filepath.Join(dir, "none"), 1<<10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no file: %v, want not-exist", err)
	}
}

// TestStageRefusesWhatIsNotPlain: the engine a window names is a symbolic
// or hard link to a file only the service may read (signed even, to show
// it is refused for what it is), a FIFO, a file too big, or its signature
// is a link: bad-signature, and nothing is written. The folder the service
// stages in stays empty.
func TestStageRefusesWhatIsNotPlain(t *testing.T) {
	key := testKey(t)
	runningVersion(t, "0.1.5")
	smallLimit(t)
	dir := t.TempDir()
	self := filepath.Join(dir, "service", "denebd")
	fakeEngine(t, self, "0.1.5")
	signEngine(t, self, "0.1.5", key)
	secret := filepath.Join(dir, "root-only", "engine-key")
	fakeEngine(t, secret, "0.2.0")
	signEngine(t, secret, "0.2.0", key)
	if err := os.Chmod(secret, 0o600); err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(sigPath(secret))
	if err != nil {
		t.Fatal(err)
	}
	plainSig := func(src string) {
		if err := os.WriteFile(sigPath(src), sig, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		make func(src string) error
	}{
		{"symbolic link", func(src string) error { plainSig(src); return os.Symlink(secret, src) }},
		{"hard link", func(src string) error { plainSig(src); return os.Link(secret, src) }},
		{"FIFO", func(src string) error { plainSig(src); return syscall.Mkfifo(src, 0o755) }},
		{"too big", func(src string) error {
			bigFile(t, src, maxEngineSize+1)
			signEngine(t, src, "0.2.0", key)
			return nil
		}},
		{"signature a link", func(src string) error {
			fakeEngine(t, src, "0.2.0")
			signEngine(t, src, "0.2.0", key)
			elsewhere := filepath.Join(filepath.Dir(src), "elsewhere.sig")
			if err := os.Rename(sigPath(src), elsewhere); err != nil {
				return err
			}
			return os.Symlink(elsewhere, sigPath(src))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "denebd")
			if err := c.make(src); err != nil {
				t.Fatal(err)
			}
			stage := t.TempDir()
			var st *staged
			var out apitype.UpdateResult
			within(t, c.name, func() {
				st, out = stageEngine(context.Background(), src, filepath.Join(stage, "denebd.update"), self, version, "")
			})
			if st != nil || out.Result != apitype.UpdateBadSignature {
				t.Fatalf("got %+v (%v), want %s", out, st, apitype.UpdateBadSignature)
			}
			if left, _ := os.ReadDir(stage); len(left) > 0 {
				t.Errorf("left behind: %v", left)
			}
		})
	}
}

// TestStageChecksBeforeWriting: a signature that does not check out is
// answered before anything is written, so that what is not checked never
// reaches the disk: the folder the service stages in may as well be
// read-only.
func TestStageChecksBeforeWriting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes in a read-only folder")
	}
	testKey(t)
	runningVersion(t, "0.1.5")
	_, other, _ := ed25519.GenerateKey(nil)
	src := filepath.Join(t.TempDir(), "denebd")
	fakeEngine(t, src, "0.2.0")
	signEngine(t, src, "0.2.0", other)
	stage := t.TempDir()
	if err := os.Chmod(stage, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(stage, 0o700) })
	st, out := stageEngine(context.Background(), src, filepath.Join(stage, "denebd.update"), filepath.Join(stage, "denebd"), version, "")
	if st != nil || out.Result != apitype.UpdateBadSignature {
		t.Fatalf("got %+v (%v), want %s before any writing", out, st, apitype.UpdateBadSignature)
	}
}
