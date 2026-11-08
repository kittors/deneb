package tun

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRouteGet(t *testing.T) {
	def := "   route to: 10.77.0.9\ndestination: default\n       mask: default\n    gateway: 192.168.1.1\n  interface: en0\n"
	if b, i := ParseRouteGet(def); b != 0 || i != "en0" {
		t.Errorf("default route: got /%d %s", b, i)
	}
	clash := "   route to: 10.77.0.9\ndestination: 8.0.0.0\n       mask: 248.0.0.0\n  interface: utun4\n"
	if b, i := ParseRouteGet(clash); b != 5 || i != "utun4" {
		t.Errorf("proxy's 8/5: got /%d %s", b, i)
	}
	ours := "destination: 10.77.0.0\n       mask: 255.255.0.0\n  interface: utun10\n"
	if b, i := ParseRouteGet(ours); b != 16 || i != "utun10" {
		t.Errorf("our /16: got /%d %s", b, i)
	}
}

func TestCheckBlock(t *testing.T) {
	for _, bad := range []string{"100.86.0.0/16", "198.18.0.0/16", "100.64.0.0/10"} {
		if CheckBlock(netip.MustParsePrefix(bad)) == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	if err := CheckBlock(netip.MustParsePrefix("10.77.0.0/16")); err != nil {
		t.Errorf("10.77/16: %v", err)
	}
}

func TestPreflightRefusesCoveredBlock(t *testing.T) {
	block := netip.MustParsePrefix("10.77.0.0/16")
	catchAll := func(netip.Addr) (int, string, error) { return 5, "utun4", nil }
	if err := Preflight(block, catchAll); err != nil {
		t.Errorf("only a proxy's 8/5 covers it, which is fine: %v", err)
	}
	office := func(netip.Addr) (int, string, error) { return 16, "en0", nil }
	if err := Preflight(block, office); err == nil {
		t.Error("an existing /16 on the LAN must make kernel mode refuse")
	}
}

func TestUndoRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	res := filepath.Join(dir, "deneb.internal")
	u := &Undo{Interface: "utun99", Route: netip.MustParsePrefix("10.77.0.0/16"), ResolverFile: res, ResolverBody: ResolverBody(netip.MustParseAddr("10.77.0.53"))}
	if err := saveUndo(dir, u); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(res, []byte(u.ResolverBody), 0o644)
	CleanupStale(dir, func(string, ...any) {})
	if _, err := os.Stat(res); !os.IsNotExist(err) {
		t.Error("stale resolver file should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, undoFile)); !os.IsNotExist(err) {
		t.Error("undo record should be gone")
	}
	// A resolver file someone else rewrote is left alone.
	saveUndo(dir, u)
	os.WriteFile(res, []byte("nameserver 1.1.1.1\n"), 0o644)
	CleanupStale(dir, func(string, ...any) {})
	if b, _ := os.ReadFile(res); !strings.Contains(string(b), "1.1.1.1") {
		t.Error("someone else's resolver file was removed")
	}
}
