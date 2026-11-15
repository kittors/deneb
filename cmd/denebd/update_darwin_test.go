package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestMacPutAndGoBack: the service takes a signed newer engine in, keeping
// the old one and both signatures; the new one that does not get going is
// gone back from after maxStarts, and is then refused (§2).
func TestMacPutAndGoBack(t *testing.T) {
	key := testKey(t)
	runningVersion(t, "0.1.5")
	dir := t.TempDir()
	p := macPaths{bin: filepath.Join(dir, "denebd"), staged: filepath.Join(dir, "denebd.update"),
		prev: filepath.Join(dir, "denebd.prev"), note: filepath.Join(dir, "update.json")}
	fakeEngine(t, p.bin, "0.1.5")
	signEngine(t, p.bin, "0.1.5", key)
	oldSig, _ := os.ReadFile(sigPath(p.bin))
	src := filepath.Join(t.TempDir(), "Deneb.app", "denebd")
	fakeEngine(t, src, "0.2.0")
	signEngine(t, src, "0.2.0", key)

	out, ok := p.put(context.Background(), src)
	if !ok || out.Version != "0.2.0" {
		t.Fatalf("put: %+v", out)
	}
	if v, err := exeVersion(context.Background(), p.bin); err != nil || v != "0.2.0" {
		t.Errorf("in place: %q, %v", v, err)
	}
	if v, err := exeVersion(context.Background(), p.prev); err != nil || v != "0.1.5" {
		t.Errorf("kept: %q, %v", v, err)
	}
	if !signedEngine(p.bin) || !signedEngine(p.prev) {
		t.Error("the signatures did not go along")
	}
	if n := readNote(p.note); n == nil || n.To != "0.2.0" || n.Prev != p.prev {
		t.Fatalf("note: %+v", n)
	}
	if _, err := os.Stat(p.staged); err == nil {
		t.Error("the copy is still there")
	}

	// The new engine starts maxStarts times without getting going.
	runningVersion(t, "0.2.0")
	for i := 0; i < maxStarts; i++ {
		if startCheck(p.note, version, p.restore, p.leftovers, t.Logf) {
			t.Fatalf("went back at start %d", i+1)
		}
	}
	if !startCheck(p.note, version, p.restore, p.leftovers, t.Logf) {
		t.Fatal("did not go back")
	}
	if v, err := exeVersion(context.Background(), p.bin); err != nil || v != "0.1.5" {
		t.Errorf("after going back: %q, %v", v, err)
	}
	if b, _ := os.ReadFile(sigPath(p.bin)); string(b) != string(oldSig) {
		t.Error("the old one's signature did not come back")
	}

	// The old one runs again, and is offered the same one.
	runningVersion(t, "0.1.5")
	if out, ok := p.put(context.Background(), src); ok || out.Result != apitype.UpdateRolledBack {
		t.Errorf("the one that could not start: %+v", out)
	}
}
