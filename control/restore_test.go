package control

import (
	"cmp"
	"database/sql"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestRestoreDrill is the restore drill (deploy/control/restore-drill.sh):
// it brings a copy of a backup up the way a restore would, on this
// server's code, and checks that it holds together. It never runs against
// the live directory: point it at a copy. It prints no secret, only
// counts, and whether the signing key was in the copy.
//
//	DENEB_RESTORE_COPY=/tmp/drill DENEB_RESTORE_SEED=seed.json go test ./control -run TestRestoreDrill -v
func TestRestoreDrill(t *testing.T) {
	dir := os.Getenv("DENEB_RESTORE_COPY")
	if dir == "" {
		t.Skip("DENEB_RESTORE_COPY not set")
	}
	seedPath := cmp.Or(os.Getenv("DENEB_RESTORE_SEED"), "../deploy/control/seed.hk155.json")
	publicURL := cmp.Or(os.Getenv("DENEB_RESTORE_URL"), "https://103.42.31.155:47443")

	// Before the server opens it: whole, and was the key in it.
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "deneb.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var check string
	var schema, keys int
	if err := raw.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		t.Fatal(err)
	}
	raw.QueryRow(`PRAGMA user_version`).Scan(&schema)
	raw.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'secret'`).Scan(&keys)
	raw.Close()
	t.Logf("the copy: schema %d, integrity %s, signing key in it: %v", schema, check, keys > 0)
	if check != "ok" {
		t.Fatalf("the copy does not check out: %s", check)
	}
	if keys > 0 {
		t.Log("a backup made before M7 still has the signing key: keep it as safe as the server")
	}

	seed, err := LoadSeed(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: dir, PublicURL: publicURL, Seed: seed, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("the restored server does not start: %v", err)
	}
	defer s.Close()
	st := s.state.Load()
	w := st.w

	// It holds together: everything has its owner, every device in use
	// has its own address in the network and a map with only real peers,
	// every LAN's owner is in it.
	addrs := map[netip.Addr]proto.DeviceID{}
	usable := 0
	for _, d := range w.devices {
		if w.people[d.Owner] == nil {
			t.Errorf("device %s has no owner", d.ID)
		}
		if !d.usable() {
			continue
		}
		usable++
		if other, dup := addrs[d.IPv4]; dup {
			t.Errorf("devices %s and %s share %s", d.ID, other, d.IPv4)
		}
		addrs[d.IPv4] = d.ID
		if !w.Network.IPv4.Contains(d.IPv4) {
			t.Errorf("device %s at %s is outside %s", d.ID, d.IPv4, w.Network.IPv4)
		}
		nm := w.netMap(d.ID, st.v, nil)
		if nm == nil || nm.Self.ID != d.ID {
			t.Errorf("device %s gets no map", d.ID)
			continue
		}
		for _, p := range nm.Peers {
			if pd := w.devices[p.ID]; pd == nil || !pd.usable() {
				t.Errorf("device %s's map has %s, which is not in use", d.ID, p.ID)
			}
		}
	}
	for _, l := range w.lans {
		if m := l.memberOf[l.Owner]; w.people[l.Owner] == nil || m == nil || m.State != proto.MemberJoined {
			t.Errorf("LAN %s's owner is not in it", l.ID)
		}
	}
	if len(w.people) == 0 {
		t.Error("no one in the copy")
	}
	t.Logf("restored: version %d, network %s, %d people, %d LANs, %d devices (%d in use), %d relays, %d shares",
		st.v, w.Network.IPv4, len(w.people), len(w.lans), len(w.devices), usable, len(w.relays), len(w.shares))
}
