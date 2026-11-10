package control

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestProdCopy opens a copy of a deployed server's data directory with
// this server, migrations and all, and checks that nothing changed for the
// devices: every device logged in before is usable, nothing waits for
// approval, the LANs from before keep "new devices need no approval", and
// each device's map lists the peers it should. It never runs against the
// live directory: point it at a copy.
//
//	DENEB_PROD_COPY=/tmp/copy-of-data go test ./control -run TestProdCopy -v
func TestProdCopy(t *testing.T) {
	dir := os.Getenv("DENEB_PROD_COPY")
	if dir == "" {
		t.Skip("DENEB_PROD_COPY not set")
	}
	seed, err := LoadSeed("../deploy/control/seed.hk155.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: dir, PublicURL: "https://103.42.31.155:47443", Seed: seed, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ver int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&ver)
	st := s.state.Load()
	w := st.w
	t.Logf("schema %d, version %d, network %s, %d people, %d LANs, %d devices", ver, st.v, w.Network.IPv4, len(w.people), len(w.lans), len(w.devices))
	t.Logf("default relays %v, fallback %v", w.defaults, w.fallback)
	// No LAN asked for approval before M3 went live (11-09), and a
	// migration must not turn it on for them; those made since may, by
	// their owner's choice.
	m3 := time.Date(2026, 11, 9, 13, 0, 0, 0, time.UTC)
	for _, l := range w.lans {
		if l.ApproveDevices || len(l.pending) > 0 {
			if l.CreatedAt.Before(m3) {
				t.Errorf("LAN %s (%s) wants devices approved or has some waiting", l.ID, l.Name)
			} else {
				t.Logf("LAN %s (%s), made %s, asks for approval: %d waiting", l.ID, l.Name, l.CreatedAt.Format(time.RFC3339), len(l.pending))
			}
		}
		for _, m := range l.members {
			if m.State != proto.MemberJoined {
				t.Errorf("LAN %s: member %s is %s", l.Name, m.Person, m.State)
			}
		}
		t.Logf("LAN %s %q owner %s: %d members, %d devices", l.ID, l.Name, w.people[l.Owner].Email, len(l.members), len(l.devices))
	}
	// M3 starts to count seats: no one here may be held back by it.
	for _, p := range w.people {
		seats := w.seats(p.ID, s.now())
		t.Logf("seats of %s: %d of %d (enforced %v)", p.Email, seats.Used, seats.Limit, seats.Enforced)
		if seats.Enforced && seats.Used >= seats.Limit {
			t.Errorf("%s's seats are full: %+v", p.Email, seats)
		}
	}
	for _, l := range w.lans {
		if l.Mode != proto.LANModeLAN {
			t.Errorf("LAN %s came out of the migration in mode %q", l.Name, l.Mode)
		}
	}
	if len(w.shares) != 0 {
		t.Logf("%d shares on record", len(w.shares))
	}
	for _, d := range w.devices {
		if d.LoggedOut {
			t.Logf("device %s %q (%s): logged out", d.ID, d.DisplayName, w.people[d.Owner].Email)
			continue
		}
		if !d.usable() {
			t.Errorf("device %s %q is not usable after migrating", d.ID, d.DisplayName)
		}
		nm := w.netMap(d.ID, st.v, nil)
		var peers []string
		for _, p := range nm.Peers {
			peers = append(peers, p.DisplayName+" "+string(p.Scopes[0])+map[bool]string{true: " (paused)", false: ""}[len(p.Scopes) == 0])
		}
		t.Logf("device %s %q (%s) %s: %d scopes, peers %v", d.ID, d.DisplayName, w.people[d.Owner].Email, nm.Self.Addrs[0], len(nm.Scopes), peers)
		// Until relays are dragged into LANs, every scope goes through the
		// fallback in its order (the seed's relays as the seed lists them,
		// then the other public ones), and the map lists those relays,
		// sorted by ID.
		var relays []proto.RelayID
		for _, rl := range nm.Relays {
			relays = append(relays, rl.ID)
		}
		if want := slices.Sorted(slices.Values(w.fallback)); !slices.Equal(relays, want) {
			t.Errorf("device %s gets relays %v, want %v", d.ID, relays, want)
		}
		for _, sc := range nm.Scopes {
			if !slices.Equal(sc.Relays, w.fallback) {
				t.Errorf("device %s: scope %s goes through %v, want %v", d.ID, sc.ID, sc.Relays, w.fallback)
			}
		}
		for _, p := range nm.Peers {
			if pd := w.devices[p.ID]; pd.HomeRelay != "" && p.HomeRelay != pd.HomeRelay {
				t.Errorf("device %s sees peer %s's home as %q, it says %q", d.ID, p.ID, p.HomeRelay, pd.HomeRelay)
			}
		}
	}
}
