package engine

import (
	"testing"
	"time"

	"github.com/kittors/deneb/engine/magicsock"
)

// TestOneWayUDPShowsInTheCounters: a network that drops UDP from build to
// mac but lets mac's through (hk-spare to hk-155, 11-10: STUN to the relay
// and a bare datagram lost too, TCP and ICMP fine). The line stays on the
// relay, and the counters tell which way is lost: build hears mac's pings
// (pingsRx), mac hears neither build's pings nor its answers. Once UDP gets
// through again, the line goes direct by itself.
func TestOneWayUDPShowsInTheCounters(t *testing.T) {
	_, mac, build, _ := newWorld(t)
	kb, km := build.node().NodeKey, mac.node().NodeKey
	mac.e.ms.SetLoss(kb, 0, 1) // whatever build sends direct is lost on the way to mac
	status := func() (atMac, atBuild magicsock.PathStatus) {
		atMac, _ = mac.e.ms.Status(kb)
		atBuild, _ = build.e.ms.Status(km)
		return
	}
	// On the relay both ways: at once, or once a direct path made before
	// the loss began runs out of Hold.
	deadline := time.Now().Add(20 * time.Second)
	for {
		pingOK(t, mac, "build", 5*time.Second) // through the relay
		atMac, atBuild := status()
		if atMac.Kind == "relay" && atBuild.Kind == "relay" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with UDP lost from build to mac: mac shows %s, build %s", atMac.Kind, atBuild.Kind)
		}
		time.Sleep(200 * time.Millisecond)
	}
	m0, b0 := status()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		pingOK(t, mac, "build", 5*time.Second)
		time.Sleep(200 * time.Millisecond)
	}
	m1, b1 := status()
	if b1.PingsRx <= b0.PingsRx {
		t.Errorf("build heard none of mac's pings (%d, then %d)", b0.PingsRx, b1.PingsRx)
	}
	if m1.PingsRx != m0.PingsRx || m1.PongsRx != m0.PongsRx || b1.PongsRx != b0.PongsRx {
		t.Errorf("with UDP lost from build to mac: mac heard %d pings and %d answers, build %d answers",
			m1.PingsRx-m0.PingsRx, m1.PongsRx-m0.PongsRx, b1.PongsRx-b0.PongsRx)
	}
	if m1.Kind != "relay" || b1.Kind != "relay" {
		t.Errorf("mac shows %s, build %s, with UDP lost one way", m1.Kind, b1.Kind)
	}
	t.Logf("build heard %d of mac's pings, mac nothing: UDP lost from build to mac", b1.PingsRx-b0.PingsRx)

	mac.e.ms.SetLoss(kb, 0, 0)
	pingOK(t, mac, "build", 5*time.Second)
	waitPath(t, mac, build, "direct", 10*time.Second)
	waitPath(t, build, mac, "direct", 10*time.Second)
	if st, _ := mac.e.ms.Status(kb); st.PongsRx <= m1.PongsRx {
		t.Error("direct, and mac counted no answers")
	}
}
