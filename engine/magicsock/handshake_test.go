package magicsock

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/types/key"
)

// A relay coming up starts over every handshake unanswered for staleInit:
// its response may have gone through the relay while we were not on it,
// and the same initiation again would be a replay. A handshake just sent,
// or one older than WireGuard's own retry, is left alone.
func TestRelayUpRestartsUnansweredHandshakes(t *testing.T) {
	var mu sync.Mutex
	var got []proto.NodePublic
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}, OnStaleHandshake: func(k proto.NodePublic) {
		mu.Lock()
		got = append(got, k)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stale, fresh, old := proto.NodePublic{1}, proto.NodePublic{2}, proto.NodePublic{3}
	var cfgs []PeerConfig
	for i, k := range []proto.NodePublic{stale, fresh, old} {
		cfgs = append(cfgs, PeerConfig{ID: proto.DeviceID(string(rune('a' + i))), Label: string(rune('a' + i)), NodeKey: k, DiscoKey: proto.DiscoPublic{byte(i + 1)}})
	}
	c.SetPeers(cfgs)
	now := time.Now()
	init := make([]byte, 148)
	init[0] = 1
	c.mu.Lock()
	for k, at := range map[proto.NodePublic]time.Time{stale: now.Add(-300 * time.Millisecond), fresh: now.Add(-20 * time.Millisecond), old: now.Add(-6 * time.Second)} {
		p := c.peers[k]
		if p == nil {
			c.mu.Unlock()
			t.Fatalf("peer %x not set up", k[:1])
		}
		p.pendingInit, p.pendingInitAt = init, at
	}
	c.mu.Unlock()

	c.restartUnansweredHandshakes()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != stale {
		t.Fatalf("started over %v, want only the one unanswered for 300 ms", got)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peers[stale].pendingInit != nil {
		t.Error("the handshake started over is still pending: it would be sent again as a replay")
	}
	if c.peers[fresh].pendingInit == nil {
		t.Error("a handshake sent 20 ms ago was dropped: its response may still come")
	}
}

// Just after the path came back (a wake, a resume, a network change), an
// initiation sent again and still unanswered is not sent a third time: the
// peer most likely answered and the response was lost, and a copy would
// be a replay. A new handshake starts. Away from such a moment the same
// bytes go again: there it is the initiation that was likely lost.
func TestUnansweredAfterResendStartsOver(t *testing.T) {
	var mu sync.Mutex
	var got []proto.NodePublic
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}, OnStaleHandshake: func(k proto.NodePublic) {
		mu.Lock()
		got = append(got, k)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fresh, steady := proto.NodePublic{1}, proto.NodePublic{2}
	c.SetPeers([]PeerConfig{
		{ID: "a", Label: "a", NodeKey: fresh, DiscoKey: proto.DiscoPublic{1}},
		{ID: "b", Label: "b", NodeKey: steady, DiscoKey: proto.DiscoPublic{2}},
	})
	now := time.Now()
	init := make([]byte, 148)
	init[0] = 1
	c.mu.Lock()
	for _, k := range []proto.NodePublic{fresh, steady} {
		p := c.peers[k]
		p.pendingInit, p.pendingInitAt, p.initResentAt = init, now.Add(-700*time.Millisecond), now.Add(-350*time.Millisecond)
	}
	c.peers[fresh].freshAt = now.Add(-time.Second)
	c.mu.Unlock()

	c.resendInits()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("started over %v, want only the peer whose path just came back", got)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peers[steady].pendingInit == nil || !c.peers[steady].initResentAt.After(now) {
		t.Error("away from a wake the initiation was not sent again")
	}
}

// With the relay connection not up yet and the direct path not confirmed
// (just after a network change or a sleep), a packet goes to the last
// direct address: through the relay it would wait for the dial or be lost
// with it. Handshake messages go there only.
func TestNoRelayYetSendsToLastDirectAddress(t *testing.T) {
	peerSock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peerSock.Close()
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	k := proto.NodePublic{7}
	c.SetPeers([]PeerConfig{{ID: "a", Label: "a", NodeKey: k, DiscoKey: proto.DiscoPublic{7}}})
	c.mu.Lock()
	c.peers[k].best = peerSock.LocalAddr().(*net.UDPAddr).AddrPort()
	c.mu.Unlock()

	data := make([]byte, 64)
	data[0] = 4 // a data message
	init := make([]byte, 148)
	init[0] = 1
	for _, b := range [][]byte{data, init} {
		c.sendWG([][]byte{b}, k)
		peerSock.SetReadDeadline(time.Now().Add(time.Second))
		got := make([]byte, 256)
		n, _, err := peerSock.ReadFromUDPAddrPort(got)
		if err != nil || n != len(b) || got[0] != b[0] {
			t.Fatalf("message type %d did not go to the last direct address with no relay up: %v (%d bytes)", b[0], err, n)
		}
	}
}

// A pending initiation ends when WireGuard sends data on the session its
// response made, not when a response comes: a late one to an earlier
// initiation, or one WireGuard refuses (it dropped the initiation, or
// answered the peer's crossing one), must leave it to be started over.
func TestPendingInitEndsWhenItsSessionIsUsed(t *testing.T) {
	p := &peerState{}
	init := make([]byte, 148)
	init[0] = 1
	copy(init[4:8], []byte{1, 2, 3, 4}) // our sender index
	p.pendingInit = init
	resp := make([]byte, 92)
	resp[0] = 2
	copy(resp[4:8], []byte{5, 6, 7, 8})  // the peer's index for the session
	copy(resp[8:12], []byte{9, 9, 9, 9}) // answers another initiation
	noteHandshakeResponseLocked(p, resp)
	if p.respSeen {
		t.Fatal("a response to another initiation was taken for this one")
	}
	copy(resp[8:12], []byte{1, 2, 3, 4})
	noteHandshakeResponseLocked(p, resp)
	if p.pendingInit == nil {
		t.Fatal("the response alone ended the initiation: WireGuard may yet refuse it")
	}
	data := make([]byte, 32)
	data[0] = 4
	copy(data[4:8], []byte{7, 7, 7, 7}) // an older session
	noteSessionInUseLocked(p, data)
	if p.pendingInit == nil {
		t.Fatal("data on another session ended the initiation")
	}
	copy(data[4:8], []byte{5, 6, 7, 8})
	noteSessionInUseLocked(p, data)
	if p.pendingInit != nil {
		t.Fatal("data on its session left the initiation pending")
	}
}

// The same handshake message twice (a peer claiming CapHandshakeDedupe
// sends it both direct and through the relay): WireGuard gets it once.
// Another message passes; with the dropping off, as in an engine from
// before the capability, both copies go through.
func TestHandshakeDedupe(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	init := make([]byte, 148)
	init[0], init[4] = 1, 7
	other := append([]byte(nil), init...)
	other[4] = 8
	got := func() int {
		n := 0
		for {
			select {
			case <-c.rx:
				n++
			default:
				return n
			}
		}
	}
	c.deliver(rxPacket{data: init})
	c.deliver(rxPacket{data: append([]byte(nil), init...)})
	c.deliver(rxPacket{data: other})
	if n := got(); n != 2 || c.HandshakeDupes() != 1 {
		t.Fatalf("handed over %d, dropped %d; want 2 and 1", n, c.HandshakeDupes())
	}
	data := make([]byte, 64)
	data[0] = 4
	c.deliver(rxPacket{data: data})
	c.deliver(rxPacket{data: data})
	if n := got(); n != 2 {
		t.Fatalf("data handed over %d times, want 2: only handshake messages are dropped", n)
	}
	// WireGuard's queue full: the copy that came first is dropped there,
	// and the one that came the other way must still go in.
	for len(c.rx) < cap(c.rx) {
		c.rx <- rxPacket{data: data}
	}
	late := append([]byte(nil), init...)
	late[4] = 10
	c.deliver(rxPacket{data: late})
	got()
	dupes := c.HandshakeDupes()
	c.deliver(rxPacket{data: append([]byte(nil), late...)})
	if n := got(); n != 1 || c.HandshakeDupes() != dupes {
		t.Fatalf("after a drop on a full queue: handed over %d of 1, dropped as a repeat %d", n, c.HandshakeDupes()-dupes)
	}
	c.SetHandshakeDedupe(false)
	again := append([]byte(nil), init...)
	again[4] = 9
	c.deliver(rxPacket{data: again})
	c.deliver(rxPacket{data: again})
	if n := got(); n != 2 {
		t.Fatalf("with the dropping off, handed over %d of 2", n)
	}
}
