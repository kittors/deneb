package magicsock

import (
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/types/key"
)

// TestConnectingSettlesBeforeIdle: a peer still shown "connecting" does
// not go idle, however long it has been without traffic. Idle, nothing
// would ask it again, and it would show "connecting" for as long as nobody
// used the line: on CI (11-11) two devices on different relays, with Idle
// 1.5 s, idled before the one relay pong came, and showed "connecting" for
// the rest of the test. Once its path is known it idles: here nothing
// answers, so it shows "unreachable" after connectingFor, then idles.
func TestConnectingSettlesBeforeIdle(t *testing.T) {
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}, Idle: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	k := proto.NodePublic{7}
	c.SetPeers([]PeerConfig{{ID: "a", Label: "a", NodeKey: k, DiscoKey: proto.DiscoPublic{7}}})
	state := func() (bool, pathKind) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.peers[k].idle, c.peers[k].kind
	}
	c.mu.Lock()
	c.peers[k].lastActive = time.Now().Add(-3 * time.Second) // past Idle
	c.mu.Unlock()
	c.heartbeat()
	if idle, kind := state(); idle || kind != kindConnecting {
		t.Fatalf("idle %v, shown %v: want still connecting, and awake", idle, kind)
	}
	c.mu.Lock()
	c.peers[k].activatedAt = time.Now().Add(-connectingFor - time.Second)
	c.mu.Unlock()
	c.heartbeat() // nothing answered within connectingFor: unreachable
	if idle, kind := state(); idle || kind != kindUnreachable {
		t.Fatalf("idle %v, shown %v: want unreachable, and awake for this heartbeat", idle, kind)
	}
	c.heartbeat()
	if idle, kind := state(); !idle || kind != kindUnreachable {
		t.Fatalf("idle %v, shown %v: want idle on unreachable", idle, kind)
	}
}
