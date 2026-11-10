package client_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// M3's user view in the live store (DESIGN §6.11): my device's shares,
// every service with the shared ones on; the devices given to me, from a
// person and from a LAN that only hands out devices, which is not among
// my LANs; the seats; and the ops, which name a share by its device and
// person until the view has its id, share.release one taken back.
func TestLiveM3View(t *testing.T) {
	dir, err := os.MkdirTemp("", "dnb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := fake.New()
	path := filepath.Join(dir, "d.sock")
	stop, err := e.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	e.Update(func(s *apitype.State) { s.LoggedIn, s.User, s.SelfDeviceID = true, "me", "mac" })
	web := proto.Service{Kind: "web", Name: "网站", Ports: []proto.PortRange{{Proto: "tcp", First: 80, Last: 80}}}
	shell := proto.Service{Kind: "shell", Ports: []proto.PortRange{{Proto: "tcp", First: 22, Last: 22}}}
	view := proto.UserView{Version: 5, Me: "me", Features: proto.M3Features,
		People: []proto.Person{{ID: "me", Name: "kittors"}, {ID: "jie", Name: "jie", DisplayName: "阿杰"}, {ID: "ming", Name: "ming", DisplayName: "阿明"}},
		LANs: []proto.LANView{
			{ID: "weekend", Name: "周末联机", Owner: "me", Mode: proto.LANModeLAN,
				Members: []proto.Member{{Person: "me", State: proto.MemberJoined}, {Person: "jie", State: proto.MemberJoined}},
				Devices: []proto.LANDevice{{Device: "mac"}}},
			{ID: "design", Name: "设计部", Owner: "ming", Mode: proto.LANModeShare,
				Members: []proto.Member{{Person: "ming", State: proto.MemberJoined}, {Person: "me", State: proto.MemberJoined}}},
		},
		Devices: []proto.DeviceView{
			{ID: "mac", Owner: "me", Name: "mac", DisplayName: "kittors 的 MacBook"},
			{ID: "build", Owner: "me", Name: "build", DisplayName: "构建服务器", Services: []proto.Service{web, shell},
				Shares: []proto.ShareView{{ID: "s1", Person: "jie", Sharer: "me", Services: []proto.Service{web}, CreatedAt: t0}}},
		},
		SharedWithMe: []proto.SharedDevice{
			{Share: "s7", Device: proto.DeviceView{ID: "gpu", Owner: "ming", Name: "gpu", DisplayName: "GPU 主机", Online: true, PublicLAN: "design"},
				From: "ming", FromLAN: "设计部", Services: []proto.Service{shell}, SharedAt: t0},
		},
		Seats: proto.Seats{Used: 2, Limit: 3, Enforced: true, People: []proto.PersonID{"me", "jie"}, Left: 1},
	}
	var mu sync.Mutex
	var sent []proto.OpArgs
	var kinds []string
	e.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		var a proto.OpArgs
		json.Unmarshal(op.Args, &a)
		mu.Lock()
		defer mu.Unlock()
		sent, kinds = append(sent, a), append(kinds, op.Op)
		out := []byte(`{}`)
		switch proto.OpType(op.Op) {
		case proto.OpShareCreate:
			out, _ = json.Marshal(proto.OpOutput{Share: "s9"})
		case proto.OpShareRevoke:
			// Taken back: the view lists it no more.
			view.Devices[1].Shares = nil
			view.Version++
			raw, _ := json.Marshal(view)
			e.SetUserView(raw, view.Version)
		}
		return &apitype.OpResult{Version: view.Version, Result: out}, nil
	})
	raw, _ := json.Marshal(view)
	e.SetUserView(raw, view.Version)
	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)
	s := waitFor(t, l, "the M3 view", func(s *model.Snapshot) bool { return s.Loaded && len(s.Shared) == 1 })

	if !s.Can(model.FeatureShare) || !s.Can(model.FeatureSeats) || !s.Can(model.FeatureShareMode) || !s.Can(model.FeaturePublicDevice) {
		t.Errorf("an M3 server does %v", s.Features)
	}
	if len(s.LANs) != 1 || s.LANs[0].ID != "weekend" || len(s.ShareLANs) != 1 || s.ShareLANs[0].ID != "design" || s.ShareLANs[0].Mode != model.ModeShare {
		t.Errorf("LANs %d, sharing LANs %+v", len(s.LANs), s.ShareLANs)
	}
	b := s.Devices["build"]
	sh := b.ShareTo("jie")
	if sh == nil || sh.ID != "s1" || len(sh.Services) != 2 || !sh.Services[0].On || sh.Services[1].On {
		t.Fatalf("构建服务器's share %+v", sh)
	}
	g := s.SharedOf("gpu")
	if g == nil || g.Share != "s7" || g.From != "ming" || g.FromLAN != "设计部" || len(g.Services) != 1 || !g.Services[0].On {
		t.Errorf("GPU 主机 given to me: %+v", g)
	}
	if d := s.Devices["gpu"]; d == nil || d.Name != "GPU 主机" || d.PublicLAN != "design" || d.Owner != "ming" {
		t.Errorf("GPU 主机: %+v", d)
	}
	if !s.Seats.Enforced || s.Seats.Limit != 3 || s.Seats.Left != 1 || !slices.Equal(s.Seats.People, []model.PersonID{"me", "jie"}) {
		t.Errorf("seats %+v", s.Seats)
	}

	ctx := context.Background()
	// Given, by the services named; the answer has the share.
	r, err := l.Do(ctx, model.Op{Kind: model.OpShareCreate, Device: "mac", Person: "jie", Services: []string{"tcp/80"}})
	if err != nil || r.Share != "s9" {
		t.Fatalf("share.create: %+v, %v", r, err)
	}
	// A service switched in a share named by its device and person.
	if _, err := l.Do(ctx, model.Op{Kind: model.OpServiceSet, Device: "build", Person: "jie", Service: string(shell.Key()), On: true}); err != nil {
		t.Fatalf("service.set: %v", err)
	}
	// Taken back by device and person, then let go once the toast went:
	// the view lists it no more by then.
	if _, err := l.Do(ctx, model.Op{Kind: model.OpShareRevoke, Device: "build", Person: "jie"}); err != nil {
		t.Fatalf("share.revoke: %v", err)
	}
	waitFor(t, l, "the share gone", func(s *model.Snapshot) bool { return s.Devices["build"].ShareTo("jie") == nil })
	if _, err := l.Do(ctx, model.Op{Kind: model.OpShareRelease, Device: "build", Person: "jie"}); err != nil {
		t.Fatalf("share.release: %v", err)
	}
	if _, err := l.Do(ctx, model.Op{Kind: model.OpShareLeave, Share: "s7", Device: "gpu"}); err != nil {
		t.Fatalf("share.leave: %v", err)
	}
	if _, err := l.Do(ctx, model.Op{Kind: model.OpLANSetMode, LAN: "weekend", Mode: model.ModeShare}); err != nil {
		t.Fatalf("lan.setMode: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"share.create", "service.set", "share.revoke", "share.release", "share.leave", "lan.setMode"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("sent %v, want %v", kinds, want)
	}
	if a := sent[0]; a.Device != "mac" || !slices.Equal(a.Services, []proto.ServiceKey{"tcp/80"}) || a.Person != "jie" {
		t.Errorf("share.create args %+v", a)
	}
	if a := sent[1]; a.Share != "s1" || a.LAN != "" || a.Person != "" || a.Service == nil || a.Service.Key() != shell.Key() || !a.On {
		t.Errorf("service.set args %+v", a)
	}
	if sent[2].Share != "s1" || sent[3].Share != "s1" || sent[4].Share != "s7" {
		t.Errorf("shares named: revoke %q, release %q, leave %q", sent[2].Share, sent[3].Share, sent[4].Share)
	}
	if a := sent[5]; a.LAN != "weekend" || a.Mode != proto.LANModeShare {
		t.Errorf("lan.setMode args %+v", a)
	}
}
