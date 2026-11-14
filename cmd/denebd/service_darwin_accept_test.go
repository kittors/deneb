package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// Explicit real-machine acceptance only. This replaces, restarts, removes
// and restores Deneb's own service. Run while the desktop client is closed:
// DENEB_MAC_SERVICE_ACCEPT=1 DENEB_MAC_HELPER=/path/to/denebd
// DENEB_MAC_UID=501 DENEB_MAC_IMPORT=/path/to/user/engine go test ...
// Neither another service nor any other network setting is changed.
func TestMacServiceLifecycle(t *testing.T) {
	if os.Getenv("DENEB_MAC_SERVICE_ACCEPT") != "1" {
		t.Skip("explicit macOS root service acceptance only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("real service acceptance requires root")
	}
	helper, uid, from := os.Getenv("DENEB_MAC_HELPER"), os.Getenv("DENEB_MAC_UID"), os.Getenv("DENEB_MAC_IMPORT")
	if !filepath.IsAbs(helper) || uid == "" || !filepath.IsAbs(from) {
		t.Fatal("set DENEB_MAC_HELPER, DENEB_MAC_UID and DENEB_MAC_IMPORT")
	}
	c := localclient.New(socket.SystemPath())
	state := func() *apitype.State {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := c.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := state()
	if before.Mode != apitype.ModeKernel || before.SelfDeviceID == "" {
		t.Fatal("start with this computer's installed kernel service")
	}
	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		b, err := exec.CommandContext(ctx, helper, append([]string{"service"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("service %v: %v: %s", args, err, b)
		}
	}
	install := func() { run("install", "-allow-uid", uid, "-import-state", from) }
	removed := false
	t.Cleanup(func() {
		if removed {
			install()
		}
	})
	check := func() {
		t.Helper()
		s := state()
		if s.Mode != apitype.ModeKernel || s.SelfDeviceID != before.SelfDeviceID || s.User != before.User {
			t.Fatalf("service changed identity or lost kernel mode: %+v", s.Self)
		}
	}
	install()
	check()
	for range 3 {
		// The system job owns the identity even before its replacement has
		// a socket. This is the gap the desktop keepEngine loop can hit.
		if err := stopMacService(); err != nil {
			t.Fatal(err)
		}
		if !systemServiceRunning() {
			t.Fatal("installed service lost identity ownership between restarts")
		}
		if err := macLaunchctl("bootstrap", "system", macServicePlist); err != nil {
			t.Fatal(err)
		}
		if err := waitMacService(); err != nil {
			t.Fatal(err)
		}
		run("restart")
		check()
	}
	run("uninstall")
	removed = true
	for _, p := range []string{macServicePlist, macServiceBin, socket.SystemPath(), "/etc/resolver/deneb.internal"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("uninstall left %s: %v", p, err)
		}
	}
	install()
	removed = false
	check()
}
