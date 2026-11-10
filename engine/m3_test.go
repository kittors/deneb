package engine

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"fmt"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
	"net"
)

// TestShareOnlyOneWay is M3's share in miniature: kittors shares build
// with jie, so jie's nas gets the scope share/s1 with NoInbound set. nas
// uses build's shared service and pings it; build cannot ping nas or
// start anything to it ("只有他能连你，反过来不行").
func TestShareOnlyOneWay(t *testing.T) {
	w, _, build, nas := newWorld(t)
	w.members["share/s1"] = []*tnode{build, nas}
	web := proto.Service{Kind: proto.ServiceWeb, Name: "网页", Ports: []proto.PortRange{{Proto: proto.TCP, First: 8090, Last: 8090}}}
	w.open["share/s1"] = map[*tnode][]proto.Service{build: {web}}
	w.noInbound = map[proto.ScopeID]map[*tnode]bool{"share/s1": {nas: true}}
	w.push()

	ln, err := build.e.stack.ListenTCP(8090)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("hello from build\n"))
			c.Close()
		}
	}()
	nl, err := nas.e.stack.ListenTCP(7300) // something on nas build must not reach
	if err != nil {
		t.Fatal(err)
	}
	defer nl.Close()

	pingOK(t, nas, "build", 5*time.Second) // the receiver pings the shared device
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	c, err := nas.e.dialPeer(ctx, netip.AddrPortFrom(build.addr, 8090))
	cancel()
	if err != nil {
		t.Fatalf("nas cannot use build's shared service: %v", err)
	}
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	c.Close()
	if string(buf[:n]) != "hello from build\n" {
		t.Fatalf("nas read %q from build's shared service", buf[:n])
	}

	// The other way: not even a ping, and no connection.
	if sum, err := build.e.Ping(context.Background(), "nas", 2, 100*time.Millisecond, 400*time.Millisecond, nil); err == nil && sum.Received != 0 {
		t.Fatalf("build pinged nas through the share: %+v", sum)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	if c, err := build.e.dialPeer(ctx, netip.AddrPortFrom(nas.addr, 7300)); err == nil {
		c.Close()
		t.Fatal("build connected to nas through the share")
	}
	cancel()
	if d := nas.e.f.Drops()["in:no-inbound"]; d == 0 {
		t.Error("nas's filter counted no no-inbound drops")
	}

	var sc *apitype.Scope
	for i, s := range nas.e.State().Scopes {
		if s.ID == "share/s1" {
			sc = &nas.e.State().Scopes[i]
		}
	}
	if sc == nil || sc.Kind != "share" || !sc.NoInbound {
		t.Fatalf("nas's share scope in the local API: %+v", sc)
	}
}

// A share taken back and given again: the connection the receiver had
// open is gone on the shared device's side (its flow cut), and the next
// thing the receiver sends on it draws a reset, so it connects anew at
// once; before, the shared device dropped it in silence and the receiver
// waited out its own timeout (Web line, 11-09: about 12 s to use the share
// again).
func TestShareGivenAgainResetsTheOldConnection(t *testing.T) {
	w, _, build, nas := newWorld(t)
	web := proto.Service{Kind: proto.ServiceWeb, Name: "网页", Ports: []proto.PortRange{{Proto: proto.TCP, First: 8090, Last: 8090}}}
	share := func(on bool) {
		if on {
			w.members["share/s1"] = []*tnode{build, nas}
			w.open["share/s1"] = map[*tnode][]proto.Service{build: {web}}
			w.noInbound = map[proto.ScopeID]map[*tnode]bool{"share/s1": {nas: true}}
		} else {
			delete(w.members, "share/s1")
			delete(w.open, "share/s1")
			w.noInbound = nil
		}
		w.push()
	}
	share(true)
	ln, err := build.e.stack.ListenTCP(8090)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // an echo server: each line back
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}()
		}
	}()
	pingOK(t, nas, "build", 5*time.Second)
	dial := func() net.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c, err := nas.e.dialPeer(ctx, netip.AddrPortFrom(build.addr, 8090))
		if err != nil {
			t.Fatalf("nas cannot use build's shared service: %v", err)
		}
		return c
	}
	echoOK := func(c net.Conn, s string) error {
		c.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write([]byte(s)); err != nil {
			return err
		}
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		if err != nil {
			return err
		}
		if string(buf[:n]) != s {
			return fmt.Errorf("read %q", buf[:n])
		}
		return nil
	}
	old := dial()
	defer old.Close()
	if err := echoOK(old, "one\n"); err != nil {
		t.Fatalf("through the share: %v", err)
	}

	share(false) // taken back: build cuts the flow
	time.Sleep(300 * time.Millisecond)
	share(true) // and given again
	time.Sleep(300 * time.Millisecond)

	t0 := time.Now()
	err = echoOK(old, "two\n")
	took := time.Since(t0)
	if err == nil {
		t.Logf("the old connection carried on (%s)", took.Round(time.Millisecond))
	} else {
		t.Logf("the old connection ended %s after its next write: %v", took.Round(time.Millisecond), err)
		if took > time.Second {
			t.Fatalf("the old connection hung %s before it ended (%v): no reset from build", took, err)
		}
	}
	c := dial()
	defer c.Close()
	if err := echoOK(c, "three\n"); err != nil {
		t.Fatalf("a new connection after the share came back: %v", err)
	}
}

// Connections cut by a policy change end at once on both sides, with TCP
// resets from the side that cut them, instead of hanging until the
// programs' own timeouts (an open SSH session froze rather than closed,
// Web line 11-09): the share taken back, the service closed, the person
// refused, the LAN paused.
func TestCutConnectionsEndAtOnce(t *testing.T) {
	web := proto.Service{Kind: proto.ServiceWeb, Name: "网页", Ports: []proto.PortRange{{Proto: proto.TCP, First: 8090, Last: 8090}}}
	cases := []struct {
		name   string
		client string // "mac" or "nas"; the server is build
		setup  func(w *world, mac, build, nas *tnode)
		cut    func(t *testing.T, w *world, mac, build, nas *tnode)
	}{
		{"share taken back", "nas",
			func(w *world, mac, build, nas *tnode) {
				w.members["share/s1"] = []*tnode{build, nas}
				w.open["share/s1"] = map[*tnode][]proto.Service{build: {web}}
				w.noInbound = map[proto.ScopeID]map[*tnode]bool{"share/s1": {nas: true}}
			},
			func(t *testing.T, w *world, mac, build, nas *tnode) {
				delete(w.members, "share/s1")
				delete(w.open, "share/s1")
				w.noInbound = nil
				w.push()
			}},
		{"service closed", "mac",
			func(w *world, mac, build, nas *tnode) { w.open["lan/studio"][build] = []proto.Service{web} },
			func(t *testing.T, w *world, mac, build, nas *tnode) {
				w.open["lan/studio"][build] = nil
				w.push()
			}},
		{"person refused", "nas",
			func(w *world, mac, build, nas *tnode) {
				w.members["share/s1"] = []*tnode{build, nas}
				w.open["share/s1"] = map[*tnode][]proto.Service{build: {web}}
			},
			func(t *testing.T, w *world, mac, build, nas *tnode) {
				w.refuses[[2]*tnode{build, nas}] = true
				w.push()
			}},
		{"LAN paused", "mac",
			func(w *world, mac, build, nas *tnode) { w.open["lan/studio"][build] = []proto.Service{web} },
			func(t *testing.T, w *world, mac, build, nas *tnode) {
				if err := mac.e.Pause(context.Background(), apitype.PauseRequest{Scope: "lan/studio", Paused: true}); err != nil {
					t.Fatal(err)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, mac, build, nas := newWorld(t)
			c.setup(w, mac, build, nas)
			w.push()
			client := mac
			if c.client == "nas" {
				client = nas
			}
			ln, err := build.e.stack.ListenTCP(8090)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			serverEnded := make(chan time.Time, 1)
			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						serverEnded <- time.Now()
						return
					}
					c.Write(buf[:n])
				}
			}()
			pingOK(t, client, "build", 5*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			conn, err := client.e.dialPeer(ctx, netip.AddrPortFrom(build.addr, 8090))
			cancel()
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			conn.Write([]byte("hi\n"))
			buf := make([]byte, 64)
			if _, err := conn.Read(buf); err != nil {
				t.Fatalf("before the cut: %v", err)
			}
			// Both quiet now, as an SSH session between keystrokes.
			clientEnded := make(chan time.Time, 1)
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			go func() {
				if _, err := conn.Read(buf); err != nil {
					clientEnded <- time.Now()
				}
			}()
			time.Sleep(200 * time.Millisecond)
			t0 := time.Now()
			c.cut(t, w, mac, build, nas)
			for _, end := range []struct {
				who string
				ch  chan time.Time
			}{{c.client, clientEnded}, {"build", serverEnded}} {
				select {
				case at := <-end.ch:
					t.Logf("%s's end of the connection ended %s after the cut", end.who, at.Sub(t0).Round(time.Millisecond))
					if d := at.Sub(t0); d > time.Second {
						t.Errorf("%s's end ended only %s after the cut", end.who, d)
					}
				case <-time.After(3 * time.Second):
					t.Errorf("%s's end of the connection still open 3 s after the cut", end.who)
				}
			}
		})
	}
}
