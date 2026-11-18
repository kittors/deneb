package magicsock

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
	"tailscale.com/types/key"
)

// fakeSTUN is a STUN server on the loopback address that tells whoever asks
// they are at seen, and counts the requests.
func fakeSTUN(t *testing.T, seen netip.AddrPort) (netip.AddrPort, *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	var n atomic.Int32
	go func() {
		buf := make([]byte, 1500)
		for {
			k, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			tx, err := stun.ParseBindingRequest(buf[:k])
			if err != nil {
				continue
			}
			n.Add(1)
			pc.WriteTo(stun.Response(tx, seen), from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).AddrPort(), &n
}

// silentUDP is a UDP port that takes requests and never answers: a STUN
// server whose answers are blocked on the way.
func silentUDP(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// logLines keeps what a Conn logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}

func (l *logLines) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.ContainsFunc(l.lines, func(s string) bool { return strings.Contains(s, sub) })
}

func waitUntil(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("after %s: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSTUNServersOfTheMap: the network map's STUN servers are asked at the
// address it gives, besides the relays; what they answer is this device's
// public address, offered to peers, and the log says who saw it. A
// server that never answers (a blocked one) holds nothing up; one taken
// off the map is forgotten.
func TestSTUNServersOfTheMap(t *testing.T) {
	var lines logLines
	c, err := New(key.NewNode(), [32]byte{2}, Options{Logf: lines.logf})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Start()

	seen := netip.MustParseAddrPort("203.0.113.7:41641")
	at, asked := fakeSTUN(t, seen)
	blocked := silentUDP(t)
	cf := proto.STUNServer{HostName: "stun.example.com", Port: at.Port(), IPv4: at.Addr(), IPv6: netip.MustParseAddr("2001:db8::1")}
	dead := proto.STUNServer{HostName: "blocked.example.com", Port: blocked.Port(), IPv4: blocked.Addr()}
	c.SetSTUNServers([]proto.STUNServer{dead, cf})

	waitUntil(t, "the STUN server's answer is a public address", 3*time.Second, func() bool {
		return slices.Contains(c.Endpoints(), seen)
	})
	if eps := c.Endpoints(); eps[0] != seen {
		t.Errorf("endpoints %v: the public address should come first", eps)
	}
	want := []PublicAddr{{Addr: seen, Server: cf.String()}}
	if got := c.PublicAddrs(); !slices.Equal(got, want) {
		t.Errorf("public addresses %+v, want %+v", got, want)
	}
	if msg := fmt.Sprintf("[stun] public address as %s at %s sees it: %s", cf, at, seen); !lines.has(msg) {
		t.Errorf("log %q lacks %q", lines.lines, msg)
	}

	// The next round asks again (the 1.5 s retry after the start).
	n := asked.Load()
	waitUntil(t, "the retry after the start", 3*time.Second, func() bool { return asked.Load() > n })

	// Requests to the one that never answers are given up on, not kept.
	c.mu.Lock()
	for tx, r := range c.stunTx {
		r.at = r.at.Add(-time.Minute)
		c.stunTx[tx] = r
	}
	c.mu.Unlock()
	c.stunOnce()
	c.mu.Lock()
	pending := len(c.stunTx)
	c.mu.Unlock()
	if pending > 2 {
		t.Errorf("%d requests waiting after a round of two", pending)
	}

	// Off the map: forgotten, and no longer offered.
	c.SetSTUNServers(nil)
	if got := c.PublicAddrs(); len(got) != 0 {
		t.Errorf("public addresses after the list went: %+v", got)
	}
	if slices.Contains(c.Endpoints(), seen) {
		t.Errorf("endpoints %v still offer what a server off the map saw", c.Endpoints())
	}
	// An answer to a request sent before is not kept either.
	c.mu.Lock()
	tx := stun.NewTxID()
	c.stunTx[tx] = stunReq{from: stunFrom{server: cf.String()}, to: at, at: time.Now()}
	c.mu.Unlock()
	c.handleSTUN(stun.Response(tx, seen))
	if got := c.PublicAddrs(); len(got) != 0 {
		t.Errorf("a late answer from a server off the map was kept: %+v", got)
	}
}

// TestSTUNAfterNetworkChange: a change of network forgets what STUN said,
// and the requests in flight, then asks again: a late answer from the old
// network does not bring its address back.
func TestSTUNAfterNetworkChange(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{3}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Start()
	seen := netip.MustParseAddrPort("203.0.113.8:41641")
	at, _ := fakeSTUN(t, seen)
	cf := proto.STUNServer{HostName: "stun.example.com", Port: at.Port(), IPv4: at.Addr()}
	c.SetSTUNServers([]proto.STUNServer{cf})
	waitUntil(t, "the answer", 3*time.Second, func() bool { return len(c.PublicAddrs()) == 1 })

	c.mu.Lock()
	old := stun.NewTxID()
	c.stunTx[old] = stunReq{from: stunFrom{server: cf.String()}, to: at, at: time.Now()}
	c.mu.Unlock()
	c.ResetPaths("test")
	c.handleSTUN(stun.Response(old, netip.MustParseAddrPort("198.51.100.1:1")))
	for _, pa := range c.PublicAddrs() {
		if pa.Addr != seen {
			t.Fatalf("an answer from before the change was kept: %+v", pa)
		}
	}
	waitUntil(t, "asked again after the change", 3*time.Second, func() bool { return len(c.PublicAddrs()) == 1 })
}
