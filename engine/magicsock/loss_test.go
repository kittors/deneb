package magicsock

import (
	"net/netip"
	"testing"
	"time"
)

// TestLossShownNeedsEnoughPings: the loss a path shows is over 30 s and
// waits for 20 judged heartbeat pings. Right after build's engine was
// swapped (11-08 14:06), eight pings with one or two lost read as "12%"
// and "25%"; the extra pings of a held path, all lost at once in a short
// silence, would read worse still.
func TestLossShownNeedsEnoughPings(t *testing.T) {
	now := time.Now()
	best := netip.MustParseAddrPort("192.0.2.1:41641")
	match := func(pr probe) bool { return !pr.relay && pr.to == best }
	var ps []probe
	add := func(ago time.Duration, acked, fast bool) {
		ps = append(ps, probe{to: best, at: now.Add(-ago), acked: acked, fast: fast})
	}
	for i := 0; i < 8; i++ { // eight heartbeat pings, two lost
		add(time.Duration(3+i)*time.Second, i != 2 && i != 5, false)
	}
	if l := lossOf(ps, now, match); l != 0 {
		t.Fatalf("8 pings: loss %v, want 0 (too few to tell)", l)
	}
	ps = nil
	for i := 0; i < 28; i++ { // 28 heartbeat pings, two in a row lost
		add(time.Duration(3+i)*time.Second, i != 5 && i != 6, false)
	}
	for i := 0; i < 8; i++ { // the held path's extra pings in that silence
		add(8*time.Second+time.Duration(i)*250*time.Millisecond, false, true)
	}
	if l := lossOf(ps, now, match); l < 0.06 || l > 0.08 {
		t.Fatalf("28 pings with 2 lost, 8 extra lost: loss %v, want about 0.07", l)
	}
}

// TestDupFollowsLoss: small packets go twice once the direct path's loss
// over 30 s reaches 5% with at least 3 pings lost, and stop below 2%; "on"
// and "off" override.
func TestDupFollowsLoss(t *testing.T) {
	now := time.Now()
	best := netip.MustParseAddrPort("192.0.2.1:41641")
	c := &Conn{opts: Options{DupSmall: "auto"}, logf: func(string, ...any) {}}
	p := &peerState{best: best}
	pings := func(lostEvery int) { // 28 heartbeat pings, every lostEvery-th lost
		p.probes = nil
		for i := 0; i < 28; i++ {
			p.probes = append(p.probes, probe{to: best, at: now.Add(-time.Duration(3+i) * time.Second), acked: lostEvery == 0 || i%lostEvery != 0})
		}
	}
	pings(0)
	if c.judgeDupLocked(p, now); p.dup {
		t.Fatal("clean line: sent twice")
	}
	pings(14) // 2 of 28: 7%, but two unlucky pings are not a lossy line
	if c.judgeDupLocked(p, now); p.dup {
		t.Fatal("2 pings lost of 28: sent twice")
	}
	pings(7) // 4 of 28 lost: 14%, the home line to Hong Kong
	if c.judgeDupLocked(p, now); !p.dup {
		t.Fatal("14% loss: not sent twice")
	}
	pings(14) // 2 of 28: 7%, still over 2%
	if c.judgeDupLocked(p, now); !p.dup {
		t.Fatal("7% loss after 14%: stopped")
	}
	pings(0)
	if c.judgeDupLocked(p, now); p.dup {
		t.Fatal("clean again: still sent twice")
	}
	c.SetDupSmall("on")
	if c.judgeDupLocked(p, now); !p.dup {
		t.Fatal(`"on" on a clean line: not sent twice`)
	}
	c.SetDupSmall("off")
	pings(7)
	if c.judgeDupLocked(p, now); p.dup {
		t.Fatal(`"off" at 14% loss: sent twice`)
	}
}

// TestCountBad: a ping is bad if nobody answered it, or if the answer came
// lateBy over the fastest answer of the window: the relay is TCP, and on a
// line that loses TCP its pings come back late rather than not at all.
func TestCountBad(t *testing.T) {
	now := time.Now()
	match := func(pr probe) bool { return pr.relay }
	var ps []probe
	add := func(ago time.Duration, acked bool, rtt time.Duration) {
		ps = append(ps, probe{relay: true, at: now.Add(-ago), acked: acked, rtt: rtt})
	}
	add(7*time.Second, true, 60*time.Millisecond)  // the baseline
	add(6*time.Second, true, 80*time.Millisecond)  // a bit slower: fine
	add(5*time.Second, true, 420*time.Millisecond) // a retransmission: late
	add(4*time.Second, false, 0)                   // lost
	add(3*time.Second, true, 70*time.Millisecond)
	add(500*time.Millisecond, false, 0) // past the baseline and lateBy unanswered: late
	add(100*time.Millisecond, false, 0) // too young to judge
	sent, bad := countBad(ps, now, 8*time.Second, match)
	if sent != 6 || bad != 3 {
		t.Fatalf("sent %d bad %d, want 6 and 3", sent, bad)
	}
}
