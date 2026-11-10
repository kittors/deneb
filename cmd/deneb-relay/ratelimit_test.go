package main

import (
	"context"
	"testing"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/controltest"
	"tailscale.com/derp"
)

// TestRateLimitHolds: a public relay's limit holds what a client gets
// through it, counted where the relay reads.
func TestRateLimitHolds(t *testing.T) {
	ctl := controltest.New(t)
	tok, err := control.AdminRelayAdd(ctl.Sock, "限速", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	rr := startRelay(t, ctl, t.TempDir(), tok.Token, nil)
	an, bn := newNode(), newNode()
	ctl.Login(t, "mia@deneb.test", "a", an.pub)
	ctl.Login(t, "leo@deneb.test", "b", bn.pub)
	a, b := rr.dial(t, an), rr.dial(t, bn)
	admitted(t, a)
	admitted(t, b)
	got := make(chan int, 1)
	go func() {
		n := 0
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			m, err := recv(b)
			if err != nil {
				break
			}
			if p, ok := m.(derp.ReceivedPacket); ok {
				n += len(p.Data)
			}
		}
		got <- n
	}()
	payload := make([]byte, 1200)
	readNow := func() int64 {
		rr.r.mu.Lock()
		defer rr.r.mu.Unlock()
		var read int64
		for c := range rr.r.conns {
			c.mu.Lock()
			if c.node == an.pub {
				read += c.sent.Load()
			}
			c.mu.Unlock()
		}
		return read
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if err := a.Send(bn.priv.Public(), payload); err != nil {
				return
			}
		}
	}()
	<-ctx.Done()
	read := readNow()
	took := time.Since(start)
	arrived := <-got
	t.Logf("in %s at 100 KB/s: the relay read %d KB (%d KB/s), %d KB arrived in 4 s", took.Round(time.Millisecond), read>>10, int(float64(read)/took.Seconds())>>10, arrived>>10)
	// Three seconds at the rate, the burst, and a frame or two.
	if limit := int64(100_000*3 + 100_000 + 10_000); read > limit {
		t.Errorf("the relay read %d bytes in 3 s, more than %d", read, limit)
	}
}
