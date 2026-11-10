package tun

// What only macOS does: Preflight asks the routing table about the block
// (route get), and the undo record's resolver file is in /etc/resolver.
// Linux hands names to systemd-resolved and Windows to an NRPT rule, so
// there is no file to remove there (tun_linux.go, tun_windows.go).

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	old := resolverDir
	resolverDir = dir
	defer func() { resolverDir = old }()
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
