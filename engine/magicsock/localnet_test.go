package magicsock

import (
	"net/netip"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/types/key"
)

func TestBetterAddr(t *testing.T) {
	pub := netip.MustParseAddrPort("203.0.113.172:32267") // the home's public address: a hairpin
	pub2 := netip.MustParseAddrPort("103.42.31.155:41720")
	lan := netip.MustParseAddrPort("192.168.1.17:41722")
	lan2 := netip.MustParseAddrPort("10.0.0.5:41722")
	cgnat := netip.MustParseAddrPort("100.101.1.2:41722") // Tailscale's or a carrier's: not the LAN
	ms := time.Millisecond
	for _, c := range []struct {
		src     netip.AddrPort
		rtt     time.Duration
		best    netip.AddrPort
		bestRTT time.Duration
		want    bool
	}{
		{lan, 6 * ms, pub, 11 * ms, true},   // 11-09: the same LAN, through the router's NAT
		{lan, 27 * ms, pub, 11 * ms, true},  // a slow Wi-Fi sample: still the LAN
		{lan, 28 * ms, pub, 11 * ms, false}, // much slower: something else is in the way
		{pub, 2 * ms, lan, 6 * ms, false},   // never away from a LAN address that answers
		{pub2, 7 * ms, pub, 10 * ms, true},  // public to public: clearly faster
		{pub2, 9 * ms, pub, 10 * ms, false}, // and not on jitter
		{lan2, 4 * ms, lan, 6 * ms, true},   // LAN to LAN: the same rule
		{lan2, 5 * ms, lan, 6 * ms, false},
		{cgnat, 9 * ms, pub, 10 * ms, false}, // the CGNAT block is not the LAN
	} {
		if got := betterAddr(c.src, c.rtt, c.best, c.bestRTT); got != c.want {
			t.Errorf("betterAddr(%s %s, was %s %s) = %v, want %v", c.src, c.rtt, c.best, c.bestRTT, got, c.want)
		}
	}
}

// Pings go to every address at once; whichever answers first becomes the
// path, and a LAN address answering a moment later takes over from a
// public one (11-09: the hairpin through the home's public address held a
// few seconds at a time).
func TestPongPrefersTheLocalAddress(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.mu.Lock()
	p := c.newPeerLocked(PeerConfig{Label: "desktop", NodeKey: proto.NodePublic{2}, DiscoKey: proto.DiscoPublic{3}})
	p.active = true
	c.mu.Unlock()
	pub := netip.MustParseAddrPort("203.0.113.172:32267")
	lan := netip.MustParseAddrPort("192.168.1.17:41722")
	pong := func(tx string, to netip.AddrPort, rtt time.Duration) {
		c.mu.Lock()
		p.pending[tx] = pendingPing{to: to, at: time.Now().Add(-rtt)}
		c.mu.Unlock()
		c.handlePong(p, discoMsg{T: "pong", Tx: tx}, to)
	}
	best := func() netip.AddrPort {
		c.mu.Lock()
		defer c.mu.Unlock()
		return p.best
	}
	pong("a", pub, 11*time.Millisecond)
	if best() != pub {
		t.Fatalf("first answer: path %s, want %s", best(), pub)
	}
	pong("b", lan, 6*time.Millisecond)
	if best() != lan {
		t.Fatalf("the LAN address answered: path %s, want %s", best(), lan)
	}
	pong("c", pub, 2*time.Millisecond)
	if best() != lan {
		t.Fatalf("a public address answering faster took the path from the LAN: %s", best())
	}
}

// An idle line shown on the relay is asked again 30 s after it idled, then
// at 60 s, 120 s, 240 s, and every 5 minutes after that.
func TestReprobeBacksOff(t *testing.T) {
	c := &Conn{opts: Options{IdleReprobe: 30 * time.Second}}
	p := &peerState{kind: kindRelay}
	t0 := time.Now()
	var at []time.Duration
	for s := 0; s <= 1800; s++ {
		now := t0.Add(time.Duration(s) * time.Second)
		if c.reprobeDueLocked(p, now) {
			at = append(at, now.Sub(t0))
		}
	}
	want := []time.Duration{30, 90, 210, 450, 750, 1050, 1350, 1650}
	if len(at) != len(want) {
		t.Fatalf("asked at %v, want %v s", at, want)
	}
	for i := range want {
		if at[i] != want[i]*time.Second {
			t.Fatalf("asked at %v, want %v s", at, want)
		}
	}
	// Shown direct: left alone.
	q := &peerState{kind: kindDirect}
	for s := 0; s <= 600; s++ {
		if c.reprobeDueLocked(q, t0.Add(time.Duration(s)*time.Second)) {
			t.Fatalf("an idle line shown direct was asked again at %d s", s)
		}
	}
}

// A direct address that keeps dropping soon after being taken up is left
// alone 2 minutes after its second drop, then 4, 8, 16, and 30 at most; a
// drop that comes long after it was taken up, or long after the last one,
// starts over.
func TestFlakyBackoffDoubles(t *testing.T) {
	c := &Conn{opts: Options{FlakyBackoff: 2 * time.Minute}}
	a := netip.MustParseAddrPort("203.0.113.172:32267")
	p := &peerState{best: a}
	now := time.Now()
	drop := func(after time.Duration) time.Duration {
		p.upSince, p.holdFrom = now, now.Add(after)
		now = now.Add(after + 7*time.Second)
		c.noteDropLocked(p, now)
		if f := p.flaky[a]; f != nil && now.Before(f.until) {
			return f.backoff
		}
		return 0
	}
	var got []time.Duration
	for i := 0; i < 7; i++ {
		got = append(got, drop(5*time.Second))
		now = now.Add(time.Minute)
	}
	want := []time.Duration{0, 2, 4, 8, 16, 30, 30}
	for i := range want {
		if got[i] != want[i]*time.Minute {
			t.Fatalf("backoffs %v, want %v minutes", got, want)
		}
	}
	if b := drop(2 * time.Minute); b != 30*time.Minute {
		t.Fatalf("a drop 2 minutes after being taken up changed the backoff to %s", b)
	}
	now = now.Add(time.Hour)
	if b := drop(5 * time.Second); b != 0 {
		t.Fatalf("a first drop an hour later is backed off %s", b)
	}
}

// A pong after the direct path went silent (idle, or dropped) measures the
// line as it is now: the round trip is that pong's, not mixed with one
// from before the silence. 11-09 build to the NAS: 108-150 ms stored from
// 13:42, the line at 4 ms after 13 minutes idle; mixed one eighth at a
// time it stayed near 130 ms, and the handshake resend waited for it
// (0.9 s to a new handshake). While the path is trusted, pongs are averaged
// as before.
func TestPongAfterSilenceTakesTheRTTAfresh(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.mu.Lock()
	p := c.newPeerLocked(PeerConfig{Label: "nas", NodeKey: proto.NodePublic{2}, DiscoKey: proto.DiscoPublic{3}})
	p.active = true
	c.mu.Unlock()
	nas := netip.MustParseAddrPort("198.51.100.4:41721")
	pong := func(tx string, rtt time.Duration) time.Duration {
		c.mu.Lock()
		p.pending[tx] = pendingPing{to: nas, at: time.Now().Add(-rtt)}
		c.mu.Unlock()
		c.handlePong(p, discoMsg{T: "pong", Tx: tx}, nas)
		c.mu.Lock()
		defer c.mu.Unlock()
		return p.srtt
	}
	pong("a", 150*time.Millisecond) // the line before the idle
	c.mu.Lock()
	p.lastPong = time.Now().Add(-13 * time.Minute) // then 13 minutes without a pong
	c.mu.Unlock()
	if got := pong("b", 4*time.Millisecond); got > 5*time.Millisecond {
		t.Fatalf("after the silence, srtt %s from a 4 ms pong: the old 150 ms still in it", got)
	}
	if got := pong("c", 12*time.Millisecond); got < 4*time.Millisecond || got > 6*time.Millisecond {
		t.Fatalf("a trusted path's next pong: srtt %s, want averaged (5 ms)", got)
	}
}
