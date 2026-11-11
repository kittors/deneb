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

// TestUndoRecordIsChecked: the undo record is acted on as root or SYSTEM;
// one Deneb would not have written is refused whole, so a state directory
// someone else can write cannot have the engine delete their choice of
// file, route, NRPT rule or firewall rule (M7 review).
func TestUndoRecordIsChecked(t *testing.T) {
	dir := t.TempDir()
	old := resolverDir
	resolverDir = filepath.Join(dir, "resolver")
	defer func() { resolverDir = old }()
	os.MkdirAll(resolverDir, 0o755)
	victim := filepath.Join(dir, "precious")
	body := ResolverBody(netip.MustParseAddr("10.77.0.53"))
	os.WriteFile(victim, []byte(body), 0o644)
	for _, u := range []Undo{
		{ResolverFile: victim, ResolverBody: body},
		{ResolverFile: filepath.Join(resolverDir, "x"), ResolverBody: "nameserver 1.1.1.1"},
		{Interface: "en0", Route: netip.MustParsePrefix("10.77.0.0/16")},
		{Interface: "utun9", Route: netip.MustParsePrefix("0.0.0.0/0")},
		{NRPT: "{00000000-0000-0000-0000-000000000000}"},
		{Firewall: "Remote Desktop - User Mode (TCP-In)"},
	} {
		if err := u.check(); err == nil {
			t.Errorf("record accepted: %+v", u)
		}
	}
	if err := saveUndo(dir, &Undo{ResolverFile: victim, ResolverBody: body}); err != nil {
		t.Fatal(err)
	}
	CleanupStale(dir, func(string, ...any) {})
	if _, err := os.Stat(victim); err != nil {
		t.Error("a file outside the resolver directory was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, undoFile)); !os.IsNotExist(err) {
		t.Error("the refused record should be gone")
	}
	for _, u := range []Undo{
		{Interface: "utun7", Route: netip.MustParsePrefix("10.77.0.0/16"), ResolverFile: filepath.Join(resolverDir, "deneb.internal"), ResolverBody: body},
		{Interface: "deneb0", Route: netip.MustParsePrefix("10.77.0.0/16"), Resolved: true},
		{Interface: "Deneb", Route: netip.MustParsePrefix("10.77.0.0/16"), NRPT: nrptRuleName, Firewall: FirewallRule},
	} {
		if err := u.check(); err != nil {
			t.Errorf("Deneb's own record refused: %v", err)
		}
	}
}

// TestFirewallRuleArgs: Deneb's inbound rule is only for this device's
// address, from the block, on private and domain networks; and no argument
// needs quoting on netsh's command line.
func TestFirewallRuleArgs(t *testing.T) {
	args := firewallRuleArgs(netip.MustParseAddr("10.48.1.5"), netip.MustParsePrefix("10.48.0.0/16"))
	have := map[string]bool{}
	for _, a := range args {
		have[a] = true
		if strings.ContainsAny(a, " \t\"") {
			t.Errorf("argument %q would need quotes", a)
		}
	}
	for _, want := range []string{"add", "rule", "name=Deneb-In", "dir=in", "action=allow", "localip=10.48.1.5", "remoteip=10.48.0.0/16", "profile=private,domain"} {
		if !have[want] {
			t.Errorf("no %q in %q", want, args)
		}
	}
}
