package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/proto"
)

// selfMap is a network map of e alone, of version v.
func selfMap(e *Engine, v uint64) *proto.NetMap {
	return &proto.NetMap{
		Version: v,
		Network: proto.Network{IPv4: netip.MustParsePrefix("10.77.0.0/16"), DNS: []netip.Addr{netip.MustParseAddr("10.77.0.53")}, Domain: proto.Domain},
		Self: proto.Node{
			ID: "dev-mac", Name: "mac", DisplayName: "mac", Owner: "p-kittors", OwnerName: "kittors", FQDN: "mac.kittors.deneb.internal",
			NodeKey:  proto.NodePublic(e.nodePriv.Public().Raw32()),
			DiscoKey: proto.DiscoPublic(magicsock.DiscoPublic(e.discoPriv)),
			Addrs:    []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
		},
	}
}

// viewOf is a user view of version v, as the server sends it.
func viewOf(v uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"version":%d,"me":"p-kittors","people":[],"lans":[]}`, v))
}

// TestRestartKeepsTheUserView: a frame's user view is saved with its
// network map, and an engine started again with no control server to ask
// has both at once: the window has its LANs (design/plan/m7/
// engine-restart-offline.md §2). A view saved with another map is left
// out; with no view and no server, UserView says so at once; logging out
// forgets both.
func TestRestartKeepsTheUserView(t *testing.T) {
	o := Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test"}
	start := func() *Engine {
		t.Helper()
		e, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := start()
	e.applyFrame(controlclient.Frame{Version: 7, NetMap: selfMap(e, 7), UserView: viewOf(7)})
	e.mu.Lock()
	e.st.LoggedIn = true
	e.saveState()
	e.mu.Unlock()
	e.Close()

	e = start()
	if got := e.State().UserView; string(got) != string(viewOf(7)) {
		t.Fatalf("started again: user view %s, want %s", got, viewOf(7))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got, err := e.UserView(ctx); err != nil || string(got) != string(viewOf(7)) {
		t.Errorf("UserView: %s %v", got, err)
	}
	e.Close()

	// Saved with another map: cut off between writing the two.
	if err := os.WriteFile(filepath.Join(o.StateDir, "userview.json"), viewOf(6), 0o600); err != nil {
		t.Fatal(err)
	}
	e = start()
	defer e.Close()
	if got := e.State().UserView; len(got) != 0 {
		t.Errorf("the view of another map taken: %s", got)
	}
	begin := time.Now()
	var ae *apitype.Error
	if _, err := e.UserView(ctx); !errors.As(err, &ae) || ae.Code != apitype.ErrOffline {
		t.Errorf("UserView with no view and no server: %v", err)
	}
	if d := time.Since(begin); d > 500*time.Millisecond {
		t.Errorf("UserView took %s", d)
	}

	e.localLogout()
	for _, f := range []string{"netmap.json", "userview.json"} {
		if _, err := os.Stat(filepath.Join(o.StateDir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left after logging out", f)
		}
	}
}
