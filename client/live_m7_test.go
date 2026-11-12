package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// TestLiveM7 is deleting a device, deleting the account and logging out
// (DESIGN §6.15) on the real store: what goes to the engine, and what the
// window gets back.
func TestLiveM7(t *testing.T) {
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
	e.Update(func(s *apitype.State) { s.LoggedIn, s.User, s.SelfDeviceID = true, "me", "mac" })
	raw, _ := json.Marshal(proto.UserView{Version: 3, Me: "me",
		People: []proto.Person{{ID: "me", Name: "kittors", Email: "me@example.com"}, {ID: "jie", Name: "jie"}},
		LANs: []proto.LANView{{ID: "weekend", Name: "周末联机", Owner: "me",
			Members: []proto.Member{{Person: "me", State: proto.MemberJoined}, {Person: "jie", State: proto.MemberJoined}},
			Devices: []proto.LANDevice{{Device: "mac"}, {Device: "pi"}}}},
		Devices: []proto.DeviceView{{ID: "mac", Owner: "me", Name: "mac"}, {ID: "pi", Owner: "me", Name: "pi"}},
	})
	e.SetUserView(raw, 3)

	var mu sync.Mutex
	var sent []apitype.OpRequest
	last := func() (apitype.OpRequest, proto.OpArgs, int) {
		mu.Lock()
		defer mu.Unlock()
		var a proto.OpArgs
		if len(sent) == 0 {
			return apitype.OpRequest{}, a, 0
		}
		json.Unmarshal(sent[len(sent)-1].Args, &a)
		return sent[len(sent)-1], a, len(sent)
	}
	e.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		mu.Lock()
		sent = append(sent, op)
		mu.Unlock()
		if op.Op != string(model.OpAccountDelete) {
			return nil, nil
		}
		var a proto.OpArgs
		json.Unmarshal(op.Args, &a)
		if a.Confirm == "seed@example.com" {
			return nil, &apitype.Error{Code: "forbidden", Message: "这个账号写在部署的种子文件里。"}
		}
		res, _ := json.Marshal(proto.OpOutput{Account: &proto.AccountDeletion{DryRun: a.DryRun,
			Devices: []proto.DeviceID{"mac", "pi"}, LANs: []proto.LANID{}, Left: []proto.LANID{},
			Transferred: []proto.LANTransfer{{LAN: "weekend", To: "jie"}},
			Relays:      []proto.RelayID{"home"}, RelaysInUse: []proto.RelayID{"home"}, Shares: 2, Invites: 3}})
		return &apitype.OpResult{Result: res}, nil
	})

	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)
	s := waitFor(t, l, "the view", func(s *model.Snapshot) bool { return s.Loaded && len(s.LANs) == 1 })
	if s.Email != "me@example.com" {
		t.Errorf("my email %q", s.Email)
	}
	ctx := context.Background()

	// Deleting another device of mine names it, as any op on a device does.
	if _, err := l.Do(ctx, model.Op{Kind: model.OpDeviceDelete, Device: "pi"}); err != nil {
		t.Fatal(err)
	}
	op, a, _ := last()
	if op.Op != "device.delete" || a.Device != "pi" {
		t.Errorf("sent %s %s", op.Op, op.Args)
	}

	// Deleting the account: the dry run, then for real with my email.
	r, err := l.Do(ctx, model.Op{Kind: model.OpAccountDelete, DryRun: true, OwnedLANs: model.AccountTransfer})
	if err != nil {
		t.Fatal(err)
	}
	op, a, _ = last()
	if !a.DryRun || a.OwnedLANs != "transfer" || a.Confirm != "" {
		t.Errorf("dry run sent %s", op.Args)
	}
	acc := r.Account
	if acc == nil || !acc.DryRun || !slices.Equal(acc.Devices, []model.DeviceID{"mac", "pi"}) ||
		!slices.Equal(acc.Transferred, []model.Transfer{{LAN: "weekend", To: "jie"}}) ||
		!slices.Equal(acc.RelaysInUse, []model.RelayID{"home"}) || acc.Shares != 2 || acc.Invites != 3 {
		t.Errorf("dry run answered %+v", acc)
	}
	r, err = l.Do(ctx, model.Op{Kind: model.OpAccountDelete, OwnedLANs: model.AccountTransfer, Confirm: "me@example.com"})
	op, a, _ = last()
	if err != nil || r.Account == nil || r.Account.DryRun || a.DryRun || a.Confirm != "me@example.com" {
		t.Errorf("deleting sent %s, answered %+v, %v", op.Args, r.Account, err)
	}

	// The server's refusal reaches the window in its own words.
	_, err = l.Do(ctx, model.Op{Kind: model.OpAccountDelete, Confirm: "seed@example.com"})
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrForbidden || me.Message != "这个账号写在部署的种子文件里" {
		t.Errorf("refused: %v", err)
	}

	// Logging out is the engine's own, not an op for the server.
	_, _, n := last()
	if _, err := l.Do(ctx, model.Op{Kind: model.OpLogout}); err != nil {
		t.Fatal(err)
	}
	if op, _, m := last(); m != n {
		t.Errorf("logging out sent %s", op.Op)
	}
	waitFor(t, l, "logged out", func(s *model.Snapshot) bool { return !s.LoggedIn })
}
