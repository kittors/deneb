package magicsock

import (
	"net"
	"net/netip"
	"testing"

	"tailscale.com/types/key"
)

// TestRelayOnly: the Windows window's own engine opens no UDP port on the
// network, which Windows would ask the person about. Its socket is on the
// loopback address whatever port and interface it is given; it offers no
// endpoint, not even a static one, and tries no direct path.
func TestRelayOnly(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{1}, Options{
		Logf: func(string, ...any) {}, RelayOnly: true, Port: 41799, BindInterface: "lo0",
		StaticEndpoints: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:41799")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if a := c.sock().LocalAddr().(*net.UDPAddr); !a.IP.IsLoopback() {
		t.Errorf("socket on %s, want the loopback address", a)
	}
	if b, _ := c.BoundInterface(); b != "" {
		t.Errorf("pinned to %q", b)
	}
	if eps := c.Endpoints(); len(eps) != 0 {
		t.Errorf("endpoints offered: %v", eps)
	}
	p := &peerState{pending: map[string]pendingPing{}}
	c.ping(p, netip.MustParseAddrPort("192.0.2.1:41799"), false)
	if len(p.pending) != 0 || p.pingsTx != 0 {
		t.Errorf("a direct ping went out: %d pending, %d sent", len(p.pending), p.pingsTx)
	}
	if err := c.Rebind("lo0"); err != nil {
		t.Fatal(err)
	}
	if a := c.sock().LocalAddr().(*net.UDPAddr); !a.IP.IsLoopback() {
		t.Errorf("after a rebind the socket is on %s", a)
	}
}
