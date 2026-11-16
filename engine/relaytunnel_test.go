package engine

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// TestRelayDirectSwitch: the test switch for a real machine (deneb debug
// relay-direct) takes a public relay by its name or ID; a relay without a
// tunnel, or one the network map does not name, is refused.
func TestRelayDirectSwitch(t *testing.T) {
	e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	nm := selfMap(e, 3)
	pin := "sha256:" + strings.Repeat("ef", 32)
	// On no scope's list: nothing connects to them.
	nm.Relays = []proto.Relay{
		{ID: "hk", Name: "香港", HostName: "example.com", IPv4: netip.MustParseAddr("192.0.2.1"), DERPPort: 47445, Pin: pin, Tunnel: "relay.example.com"},
		{ID: "own", Name: "我的", HostName: "example.com", IPv4: netip.MustParseAddr("192.0.2.2"), DERPPort: 47445, Pin: pin},
	}
	e.applyFrame(controlclient.Frame{Version: 3, NetMap: nm})
	ctx := context.Background()
	if err := e.SetRelayDirectOff(ctx, "香港", true); err != nil {
		t.Errorf("by name: %v", err)
	}
	if err := e.SetRelayDirectOff(ctx, "hk", false); err != nil {
		t.Errorf("by ID: %v", err)
	}
	var ae *apitype.Error
	if err := e.SetRelayDirectOff(ctx, "own", true); !errors.As(err, &ae) || ae.Code != apitype.ErrInvalid {
		t.Errorf("a relay without a tunnel: %v", err)
	}
	if err := e.SetRelayDirectOff(ctx, "nosuch", true); !errors.As(err, &ae) || ae.Code != apitype.ErrNotFound {
		t.Errorf("a relay the map does not name: %v", err)
	}
}
