package main

import (
	"bufio"
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kittors/deneb/cmd/deneb-relay/tunnel"
	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/controltest"
	"github.com/kittors/deneb/proto"
	"tailscale.com/derp"
	"tailscale.com/net/wsconn"
)

// TestPublicRelayTunnel: a public relay with a Cloudflare tunnel tells the
// control server its host name, which goes to the devices; devices that
// come through the tunnel's port are let in by the same admission, from
// their CF-Connecting-IP, carry packets to each other and are counted.
func TestPublicRelayTunnel(t *testing.T) {
	ctl := controltest.New(t)
	tok, err := control.AdminRelayAdd(ctl.Sock, "香港", 0)
	if err != nil {
		t.Fatal(err)
	}
	rr := startRelay(t, ctl, t.TempDir(), tok.Token, func(c *config) {
		c.tunnelListen, c.tunnelHost = "127.0.0.1:0", "relay.deneb.app"
	})
	if rr.r.tunnel == nil || !strings.Contains(rr.log(), "tunnel relay.deneb.app") {
		t.Fatalf("no tunnel port:\n%s", rr.log())
	}
	macN, nasN := newNode(), newNode()
	mac := ctl.Login(t, "kittors@deneb.test", "mac", macN.pub)
	ctl.Login(t, "jie@deneb.test", "nas", nasN.pub)
	if rl := relayOf(mac.NetMap(), rr.r.st.Relay); rl == nil || rl.Tunnel != "relay.deneb.app" {
		t.Fatalf("the network map's relay: %+v", rl)
	}

	dial := func(n node, cf string) *derp.Client {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		c, _, err := websocket.Dial(ctx, "ws://"+rr.r.tunnel.String()+tunnel.Path, &websocket.DialOptions{
			Subprotocols: []string{tunnel.Subprotocol},
			HTTPHeader:   http.Header{"CF-Connecting-IP": {cf}},
		})
		if err != nil {
			cancel()
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { c.CloseNow(); cancel() })
		nc := wsconn.NetConn(ctx, c, websocket.MessageBinary, "")
		dc, err := derp.NewClient(n.priv, nc, bufio.NewReadWriter(bufio.NewReader(nc), bufio.NewWriter(nc)), t.Logf)
		if err != nil {
			t.Fatalf("DERP: %v", err)
		}
		return dc
	}
	next := func(dc *derp.Client) derp.ReceivedMessage {
		t.Helper()
		got := make(chan derp.ReceivedMessage, 1)
		go func() { m, _ := dc.Recv(); got <- m }()
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("nothing within 5s")
			return nil
		}
	}
	macC, nasC := dial(macN, "203.0.113.7"), dial(nasN, "198.51.100.8")
	for _, dc := range []*derp.Client{macC, nasC} {
		if m := next(dc); m == nil {
			t.Fatal("not let in")
		} else if _, ok := m.(derp.ServerInfoMessage); !ok {
			t.Fatalf("not let in: %T %+v", m, m)
		}
	}
	if err := macC.Send(nasN.priv.Public(), []byte("from the mac")); err != nil {
		t.Fatal(err)
	}
	if p, ok := next(nasC).(derp.ReceivedPacket); !ok || string(p.Data) != "from the mac" || p.Source != macN.priv.Public() {
		t.Fatalf("the nas got %+v", p)
	}

	// Admission was asked about each from its CF-Connecting-IP, and the
	// relay counts what they carry, as for a direct connection.
	want := map[proto.NodePublic]netip.Addr{macN.pub: netip.MustParseAddr("203.0.113.7"), nasN.pub: netip.MustParseAddr("198.51.100.8")}
	rr.r.mu.Lock()
	for c := range rr.r.conns {
		c.mu.Lock()
		if w, ok := want[c.node]; ok && c.source == w && c.sent.Load() > 0 {
			delete(want, c.node)
		}
		c.mu.Unlock()
	}
	rr.r.mu.Unlock()
	if len(want) != 0 {
		t.Fatalf("not asked about, or not counted: %v", want)
	}

	// Not from cloudflared: no CF-Connecting-IP.
	req, _ := http.NewRequest("GET", "http://"+rr.r.tunnel.String()+tunnel.Path, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Protocol", tunnel.Subprotocol)
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without CF-Connecting-IP: %s", resp.Status)
	}
}

// TestTunnelFlags: the tunnel's port is for cloudflared on this machine, and
// goes with the tunnel's host name; a relay told otherwise does not start.
func TestTunnelFlags(t *testing.T) {
	for name, c := range map[string]config{
		"not loopback":    {tunnelListen: "0.0.0.0:47451", tunnelHost: "relay.deneb.app"},
		"no host name":    {tunnelListen: "127.0.0.1:47451"},
		"no port":         {tunnelHost: "relay.deneb.app"},
		"not a host name": {tunnelListen: "127.0.0.1:47451", tunnelHost: "https://relay.deneb.app"},
	} {
		c.data, c.listen = t.TempDir(), "127.0.0.1:0"
		if err := run(context.Background(), c, t.Logf, nil); err == nil || !strings.Contains(err.Error(), "tunnel") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
