package control

import (
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

func init() {
	// The tests watch frames come and go within a few hundred
	// milliseconds: devices coming and going are published at once, more
	// or less. TestPresenceCoalesces sets its own.
	defaultPresenceDelay = 20 * time.Millisecond
}

// TestPresenceCoalesces: many devices coming online within the delay are
// one change, one new version for their peers, not one each; going offline
// is published the same way, and stamps when each was last seen.
func TestPresenceCoalesces(t *testing.T) {
	s, ts := newTestServer(t, "")
	s.presenceDelay = 300 * time.Millisecond
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	var boxes []*engine
	for range 5 {
		e := newEngine(t, ts.URL, "box", proto.Linux)
		e.login("kittors@deneb.test")
		mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: e.id})
		boxes = append(boxes, e)
	}
	st := mac.stream(t, false)
	st.next(t)
	v := s.Version()
	var streams []*stream
	for _, e := range boxes {
		streams = append(streams, e.stream(t, false))
	}
	// The Mac hears of all five at once, in one version.
	f := st.next(t)
	online := 0
	for _, p := range f.NetMap.Peers {
		if p.Online {
			online++
		}
	}
	if online != 5 || s.Version() != v+1 {
		t.Fatalf("five came online: %d online in the frame, version %d → %d", online, v, s.Version())
	}
	// Going: after the grace and the delay, one more version, last seen
	// stamped.
	for _, st := range streams {
		st.cancel()
	}
	deadline := time.Now().Add(offlineGrace + 5*time.Second)
	for s.Version() == v+1 {
		if time.Now().After(deadline) {
			t.Fatal("they never went offline")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	w := s.state.Load().w
	for _, e := range boxes {
		if d := w.devices[e.id]; d.LastSeen.IsZero() {
			t.Fatalf("%s's last seen was not stamped", e.id)
		}
	}
	if s.Version() != v+2 {
		t.Fatalf("five went offline in %d versions", s.Version()-v-1)
	}
}
