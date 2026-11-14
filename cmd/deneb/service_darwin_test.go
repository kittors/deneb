package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// The installed macOS app used to return nil here unconditionally, so a
// signed-in computer could never leave userspace from its own window.
func TestPackagedMacOffersSystemAdapter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "denebd"), []byte("test helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	mygo.App.SetPath(mygo.PathResources, dir)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathResources, "") })
	if adapterInstaller(localclient.New(filepath.Join(dir, "unused.sock"))) == nil {
		t.Fatal("packaged macOS app has no system network installation action")
	}
}
