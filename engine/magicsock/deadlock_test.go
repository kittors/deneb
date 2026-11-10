package magicsock

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/types/key"
)

// TestRelayConnectDoesNotHoldMu: while a relay connection is being set up,
// the relay client holds its own lock for the whole dial and handshake.
// Code under mu must not wait on that lock, and the dialer must not wait on
// mu: on 11-08 an engine froze for good after two network changes, a relay
// dial (client lock, then mu for the bind interface) meeting SetRelays (mu,
// then the client's lock for NotePreferred).
func TestRelayConnectDoesNotHoldMu(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0") // takes the connection, never answers TLS
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	stuck := proto.Relay{ID: "stuck", Name: "stuck", HostName: "example.com",
		IPv4: netip.MustParseAddr("127.0.0.1"), DERPPort: uint16(ln.Addr().(*net.TCPAddr).Port)}
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetRelays([]proto.Relay{stuck}, nil, "stuck") // home: its connect starts now and hangs in TLS
	time.Sleep(300 * time.Millisecond)
	go c.SetRelays([]proto.Relay{stuck}, []RelayList{{Scope: "lan/x", Relays: []proto.RelayID{"stuck"}}}, "") // lists change: home recomputed, NotePreferred waits for the client's lock
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		c.BoundInterface() // anything under mu
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("magicsock's lock stayed held while a relay connection was being set up")
	}
}
