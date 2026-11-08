package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// shortSock keeps the socket path under the 104-byte limit of macOS, which
// t.TempDir() alone can exceed.
func shortSock(t *testing.T) string {
	dir, err := os.MkdirTemp("", "dnb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func start(t *testing.T) (*fake.Engine, *localclient.Client) {
	e := fake.New()
	path := shortSock(t)
	stop, err := e.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return e, localclient.New(path)
}

func TestStateAndWatch(t *testing.T) {
	e, c := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LoggedIn || s.SelfDeviceID != fake.ThisMac || len(s.Peers) != 3 || s.Mode != apitype.ModeUserspace {
		t.Fatalf("unexpected state: %+v", s)
	}

	got := make(chan apitype.Event, 8)
	go c.Watch(ctx, func(ev apitype.Event) error { got <- ev; return nil })
	first := <-got
	if first.Type != apitype.EventState || first.State == nil || first.State.Version != 1 {
		t.Fatalf("first event should be the whole state, got %+v", first)
	}
	e.PushPaths(apitype.PeerPath{DeviceID: fake.JiePC, Kind: apitype.PathDirect, RTTMs: 40})
	ev := <-got
	if ev.Type != apitype.EventPaths || len(ev.Paths) != 1 || ev.Paths[0].Kind != apitype.PathDirect || ev.Paths[0].RTTMs != 40 {
		t.Fatalf("expected one path event, got %+v", ev)
	}
}

func TestOpsIdempotentAndErrors(t *testing.T) {
	e, c := start(t)
	ctx := context.Background()
	op := apitype.OpRequest{ID: "k1", Op: "lan.invite"}
	r1, err := c.Do(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c.Do(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != r2.Version || string(r1.Result) != string(r2.Result) {
		t.Fatalf("same id must give the same answer: %+v vs %+v", r1, r2)
	}
	e.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		return nil, &apitype.Error{Code: apitype.ErrForbidden, Message: "只有拥有者能改名。"}
	})
	_, err = c.Do(ctx, apitype.OpRequest{Op: "lan.rename"})
	ae, ok := localclient.AsError(err)
	if !ok || ae.Code != apitype.ErrForbidden || ae.Message == "" {
		t.Fatalf("want forbidden, got %v", err)
	}
	e.Update(func(s *apitype.State) { s.ControlConnected = false })
	_, err = c.Do(ctx, apitype.OpRequest{Op: "lan.create"})
	if ae, ok := localclient.AsError(err); !ok || ae.Code != apitype.ErrOffline {
		t.Fatalf("want offline, got %v", err)
	}
}

func TestPingStreamAndResolve(t *testing.T) {
	_, c := start(t)
	ctx := context.Background()
	var n int
	sum, err := c.Ping(ctx, "nas", localclient.PingOptions{N: 3}, func(apitype.PingResult) { n++ })
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || sum.Received != 3 || sum.Device != fake.JieNAS {
		t.Fatalf("ping: n=%d summary=%+v", n, sum)
	}
	if _, err := c.Resolve(ctx, "nope"); err == nil {
		t.Fatal("resolving an unknown name must fail")
	}
}

func TestNotRunning(t *testing.T) {
	c := localclient.New(filepath.Join(os.TempDir(), "no-such-denebd.sock"))
	_, err := c.State(context.Background())
	if !errors.Is(err, localclient.ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
}

func TestPreviewInvite(t *testing.T) {
	e, c := start(t)
	ctx := context.Background()
	if _, err := c.PreviewInvite(ctx, "https://x/i/t"); err == nil {
		t.Fatal("without a handler a preview must fail")
	}
	e.HandlePreview(func(inv string) (json.RawMessage, *apitype.Error) {
		return json.RawMessage(`{"lanName":"周末联机","invite":"` + inv + `"}`), nil
	})
	raw, err := c.PreviewInvite(ctx, "tok")
	if err != nil || !strings.Contains(string(raw), "周末联机") {
		t.Fatalf("preview: %s %v", raw, err)
	}
}
