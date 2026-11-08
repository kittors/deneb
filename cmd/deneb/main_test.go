package main

import (
	"testing"
	"time"

	"github.com/kittors/deneb/client"
)

// DENEB_FAKE=ready, as the dev window runs before the engine does, starts
// the live store on a fake engine in this process, with the M1 topology
// behind it; DENEB_DEMO=1 is the demo.
func TestPickStore(t *testing.T) {
	t.Setenv("DENEB_FAKE", "ready")
	s, _, sims, lc := pickStore()
	l, ok := s.(*client.Live)
	if !ok || lc != nil || len(sims) != 5 {
		t.Fatalf("DENEB_FAKE=ready gives %T, an engine to start %v, %d events", s, lc != nil, len(sims))
	}
	defer l.Close()
	deadline := time.Now().Add(10 * time.Second)
	for snap := l.Snapshot(); !(snap.Engine && snap.Loaded && len(snap.LANs) == 2); snap = l.Snapshot() {
		if time.Now().After(deadline) {
			t.Fatalf("the fake world did not come: %+v", snap)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Setenv("DENEB_FAKE", "")
	t.Setenv("DENEB_DEMO", "1")
	if s, _, _, lc := pickStore(); lc != nil {
		t.Error("the demo starts an engine")
	} else if _, ok := s.(*client.Demo); !ok {
		t.Errorf("DENEB_DEMO=1 gives %T", s)
	}
}
