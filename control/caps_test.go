package control

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestCaps: what an engine says it can do when it logs in reaches its
// peers' maps (and its own Self), cleaned up; a new claim is a new version,
// the same claim in another order is not; logging in again after a logout
// takes the new claim; and the claim survives a restart of the server, as
// the engine only claims again when it starts.
func TestCaps(t *testing.T) {
	dir := t.TempDir()
	s, ts := newTestServer(t, dir)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.caps = []string{"zz.later-2", proto.CapHandshakeDedupe, "Not A Cap", "", proto.CapHandshakeDedupe, strings.Repeat("a", 65)}
	mac.login("kittors@deneb.test")
	build := newEngine(t, ts.URL, "build", proto.Linux) // an engine from before caps
	build.login("kittors@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: build.id})

	check := func(s *Server, want []string) {
		t.Helper()
		if got := peerOf(netMapOf(s, build.id), mac.id).Caps; !slices.Equal(got, want) {
			t.Fatalf("build sees the Mac's caps as %q, want %q", got, want)
		}
		if got := netMapOf(s, mac.id).Self.Caps; !slices.Equal(got, want) {
			t.Fatalf("the Mac's own map echoes caps %q, want %q", got, want)
		}
	}
	check(s, []string{proto.CapHandshakeDedupe, "zz.later-2"})
	// The old engine claimed nothing: its entry carries no caps at all,
	// the way a server from before caps sends it.
	p := peerOf(netMapOf(s, mac.id), build.id)
	if b, _ := json.Marshal(p); p.Caps != nil || strings.Contains(string(b), `"caps"`) {
		t.Fatalf("an engine that claimed nothing: %s", b)
	}

	relogin := func(caps ...string) {
		t.Helper()
		mac.caps = caps
		var st proto.LoginState
		if _, err := mac.call("POST", proto.PathLogin, mac.loginRequest(), &st); err != nil || !st.LoggedIn {
			t.Fatalf("login while logged in: %+v, %v", st, err)
		}
	}
	// Started again with the same claim in another order: nothing new.
	v := s.Version()
	relogin("zz.later-2", proto.CapHandshakeDedupe)
	if s.Version() != v {
		t.Fatalf("the same claim moved the version from %d to %d", v, s.Version())
	}
	// Downgraded to an engine that claims nothing: peers stop relying on
	// it at once, in a new version.
	relogin()
	if s.Version() == v {
		t.Fatal("dropping the claim made no new version")
	}
	check(s, nil)

	// After a logout, logging in again takes the new claim, at most 32.
	var many []string
	for i := range 40 {
		many = append(many, fmt.Sprintf("cap-%02d", i))
	}
	var ls proto.LoginState
	mac.call("POST", proto.PathLogout, nil, &ls)
	mac.caps = many
	mac.login("kittors@deneb.test")
	check(s, many[:maxCaps])

	ts.Close()
	s.Close()
	s2, err := New(Config{DataDir: dir, PublicURL: "http://x", Dev: true, Seed: DevSeed(), Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	check(s2, many[:maxCaps])
}
