package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// live starts a world, its fake engine on a socket of its own, and the live
// store watching it.
func live(t *testing.T, start fakeworld.Start) (*fakeworld.World, *client.Live) {
	t.Helper()
	// Under the 104 bytes macOS allows a socket's path, which t.TempDir()
	// alone can pass.
	dir, err := os.MkdirTemp("", "dnb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	w := fakeworld.New(start)
	path := filepath.Join(dir, "d.sock")
	stop, err := w.E.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)
	return w, l
}

// waitFor waits until the store's snapshot is as ok wants it, and returns it.
func waitFor(t *testing.T, l *client.Live, what string, ok func(*model.Snapshot) bool) *model.Snapshot {
	t.Helper()
	changed := make(chan struct{}, 1)
	cancel := l.Subscribe(func(*model.Snapshot) {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer cancel()
	deadline := time.After(8 * time.Second)
	for {
		if s := l.Snapshot(); ok(s) {
			return s
		}
		select {
		case <-changed:
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
			return nil
		}
	}
}

func do(t *testing.T, l *client.Live, op model.Op) model.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := l.Do(ctx, op)
	if err != nil {
		t.Fatalf("%s: %v", op.Kind, err)
	}
	return r
}

// The snapshot is the user view and the engine's state put together: the
// people with their colors, the LANs in the order I joined them, the tray,
// the services, and how this Mac reaches each device.
func TestLiveSnapshot(t *testing.T) {
	_, l := live(t, fakeworld.Ready)
	s := waitFor(t, l, "the world", func(s *model.Snapshot) bool {
		return s.Engine && s.LoggedIn && len(s.LANs) == 2 && s.Path(model.DeviceID(fakeworld.JieNAS)).Kind == model.PathRelay
	})
	if s.Me != model.PersonID(fakeworld.Me) || s.Self != model.DeviceID(fakeworld.ThisMac) {
		t.Errorf("me %q on %q", s.Me, s.Self)
	}
	if s.People[s.Me].Color != 0 || s.People[model.PersonID(fakeworld.Jie)].Color == 0 {
		t.Error("I am not blue, or 阿杰 is")
	}
	if got := s.LANs[0].Name + "," + s.LANs[1].Name; got != "工作室,周末联机" {
		t.Errorf("LANs %s", got)
	}
	if tray := s.Tray(model.LANID(fakeworld.Weekend)); !slices.Equal(tray, []model.DeviceID{model.DeviceID(fakeworld.Build)}) {
		t.Errorf("周末联机's tray holds %v", tray)
	}
	nas := s.Devices[model.DeviceID(fakeworld.JieNAS)]
	if nas.Name != "阿杰的 NAS" || nas.Addr != "10.77.0.4" || nas.FQDN != "nas.jie.deneb.internal" {
		t.Errorf("the NAS is %+v", nas)
	}
	// Another's device shows only what it opens; mine shows all of it.
	weekend := s.LAN(model.LANID(fakeworld.Weekend))
	if svcs := weekend.Services[model.DeviceID(fakeworld.JieNAS)]; len(svcs) != 1 || svcs[0].Name != "共享文件夹" || !svcs[0].On {
		t.Errorf("the NAS opens %+v", svcs)
	}
	if svcs := weekend.Services[model.DeviceID(fakeworld.ThisMac)]; len(svcs) != 2 {
		t.Errorf("this Mac lists %+v", svcs)
	}
	if p := s.Path(model.DeviceID(fakeworld.Build)); p.Kind != model.PathDirect || p.RTT != 9*time.Millisecond {
		t.Errorf("构建服务器 is reached %+v", p)
	}
}

// Ops take effect, and the snapshot that answers carries them; a refused one
// says why in the server's words.
func TestLiveOps(t *testing.T) {
	_, l := live(t, fakeworld.Ready)
	waitFor(t, l, "the world", func(s *model.Snapshot) bool { return len(s.LANs) == 2 })
	weekend, build, mac := model.LANID(fakeworld.Weekend), model.DeviceID(fakeworld.Build), model.DeviceID(fakeworld.ThisMac)
	jie := model.PersonID(fakeworld.Jie)

	r := do(t, l, model.Op{Kind: model.OpLANAddDevice, LAN: weekend, Device: build})
	s := waitFor(t, l, "构建服务器 in 周末联机", func(s *model.Snapshot) bool { return s.Version >= r.Version })
	if !s.LAN(weekend).Has(build) || len(s.Tray(weekend)) != 0 {
		t.Fatalf("构建服务器 did not join: %v", s.LAN(weekend).Devices)
	}
	r = do(t, l, model.Op{Kind: model.OpLANRemoveDevice, LAN: weekend, Device: build})
	s = waitFor(t, l, "构建服务器 out", func(s *model.Snapshot) bool { return s.Version >= r.Version })
	if s.LAN(weekend).Has(build) {
		t.Fatal("构建服务器 did not leave")
	}

	r = do(t, l, model.Op{Kind: model.OpDeviceRefuse, Device: mac, Person: jie, Refuse: true})
	s = waitFor(t, l, "the refusal", func(s *model.Snapshot) bool { return s.Version >= r.Version })
	if !s.Devices[mac].Refused[jie] {
		t.Fatal("阿杰 is not refused on this Mac")
	}

	files := s.LAN(weekend).Services[mac][1]
	r = do(t, l, model.Op{Kind: model.OpServiceSet, LAN: weekend, Device: mac, Service: files.Key, On: !files.On})
	s = waitFor(t, l, "the switch", func(s *model.Snapshot) bool { return s.Version >= r.Version })
	if got := s.LAN(weekend).Services[mac][1]; got.Key != files.Key || got.On == files.On {
		t.Fatalf("共享文件夹 is %+v, was %+v", got, files)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.Do(ctx, model.Op{Kind: model.OpLANRemoveDevice, LAN: weekend, Device: model.DeviceID(fakeworld.JieNAS)})
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrForbidden || me.Message != "这是阿杰的设备，只有阿杰能移出" {
		t.Errorf("taking 阿杰's NAS out: %v", err)
	}
	r = do(t, l, model.Op{Kind: model.OpLANInvite, LAN: model.LANID(fakeworld.Studio)})
	if r.Link == "" {
		t.Error("no invite link")
	}
}

// A device this Mac starts to reach shows 正在连接… for its first 2 seconds,
// through the relay and then direct, and then direct: never the relay.
func TestLiveDebouncesPaths(t *testing.T) {
	_, l := live(t, fakeworld.Fresh)
	waitFor(t, l, "the engine", func(s *model.Snapshot) bool { return s.Engine && !s.LoggedIn })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.Login(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, l, "logged in", func(s *model.Snapshot) bool { return s.LoggedIn && s.Self != "" })
	r := do(t, l, model.Op{Kind: model.OpLANCreate, Name: "工作室"})
	s := waitFor(t, l, "工作室", func(s *model.Snapshot) bool { return s.LAN(r.LAN) != nil })
	if l := s.LAN(r.LAN); !l.Has(s.Self) || l.Name != "工作室" {
		t.Fatalf("工作室 is %+v, without this Mac", l)
	}
	build := model.DeviceID(fakeworld.Build)
	if tray := s.Tray(r.LAN); !slices.Contains(tray, build) {
		t.Fatalf("构建服务器 is not in the tray: %v", tray)
	}
	added := time.Now()
	do(t, l, model.Op{Kind: model.OpLANAddDevice, LAN: r.LAN, Device: build})
	seen := map[model.PathKind]time.Duration{}
	done := time.After(4 * time.Second)
	for {
		k := l.Snapshot().Path(build).Kind
		if _, ok := seen[k]; !ok {
			seen[k] = time.Since(added)
		}
		if k == model.PathDirect {
			break
		}
		select {
		case <-done:
			t.Fatalf("构建服务器 never showed direct; saw %v", seen)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, ok := seen[model.PathRelay]; ok {
		t.Errorf("the relay showed for a moment: %v", seen)
	}
	if d := seen[model.PathDirect]; d < 1900*time.Millisecond {
		t.Errorf("direct showed after %v, within the first seconds", d)
	}
}

// An invite link says who asks me into what; accepting it brings me in with
// this Mac, and 阿杰's devices with it.
func TestLiveInvite(t *testing.T) {
	_, l := live(t, fakeworld.Fresh)
	waitFor(t, l, "the engine", func(s *model.Snapshot) bool { return s.Engine })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l.Login(ctx)
	waitFor(t, l, "logged in", func(s *model.Snapshot) bool { return s.LoggedIn })
	inv, err := l.Preview(ctx, fakeworld.InviteLink)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Inviter.Name != "阿杰" || inv.LANName != "周末联机" || inv.AlreadyIn || inv.Gone {
		t.Errorf("the invite says %+v", inv)
	}
	if _, err := l.Preview(ctx, "deneb://join/nosuchthing"); err == nil {
		t.Error("a made-up invite previews")
	}
	r := do(t, l, model.Op{Kind: model.OpInviteAccept, Invite: fakeworld.InviteLink})
	s := waitFor(t, l, "周末联机", func(s *model.Snapshot) bool { return s.LAN(r.LAN) != nil })
	w := s.LAN(r.LAN)
	if !w.Has(s.Self) || !w.Has(model.DeviceID(fakeworld.JiePC)) || !w.Has(model.DeviceID(fakeworld.JieNAS)) {
		t.Errorf("周末联机 holds %v", w.Devices)
	}
	if !slices.Contains(w.Members, s.Me) {
		t.Errorf("I am not among %v", w.Members)
	}
}

// With no engine to answer, the snapshot says so, and the store tries again
// until one does.
func TestLiveWithoutEngine(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dnb")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "d.sock")
	l := client.NewLive(localclient.New(path))
	l.Start()
	defer l.Close()
	time.Sleep(300 * time.Millisecond)
	if l.Snapshot().Engine {
		t.Fatal("the snapshot says the engine answers")
	}
	w := fakeworld.New(fakeworld.Ready)
	stop, err := w.E.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	waitFor(t, l, "the engine to answer", func(s *model.Snapshot) bool { return s.Engine && len(s.LANs) == 2 })
}

// M2's fields come through as the server sends them: the LAN's switches,
// who and what waits, the invites made out to people, the logins, the
// invites made out to me. A server that names no features does none.
func TestLiveM2View(t *testing.T) {
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
	raw, _ := json.Marshal(proto.UserView{Version: 3, Me: "me",
		People: []proto.Person{{ID: "me", Name: "kittors"}, {ID: "jie", Name: "jie", DisplayName: "阿杰"}, {ID: "lin", Name: "lin", DisplayName: "小林"}},
		LANs: []proto.LANView{{ID: "weekend", Name: "周末联机", Owner: "me", ApproveDevices: true,
			Members: []proto.Member{{Person: "me", State: proto.MemberJoined}, {Person: "jie", State: proto.MemberJoined}, {Person: "lin", State: proto.MemberPending}},
			Devices: []proto.LANDevice{{Device: "mac"}, {Device: "jpi", Pending: true}},
			Invited: []proto.InvitedPerson{{Person: "leo", InvitedBy: "me", Invite: "inv1", ExpiresAt: t0}}}},
		Devices: []proto.DeviceView{
			{ID: "mac", Owner: "me", Name: "mac", DisplayName: "kittors 的 MacBook", Login: proto.LoginInfo{ExpiresAt: t0.Add(48 * time.Hour), RenewFrom: t0}},
			{ID: "jpi", Owner: "jie", Name: "pi", DisplayName: "阿杰的树莓派", LastSeen: t0, Login: proto.LoginInfo{Expired: true}}},
		Invitations: []proto.InvitePreview{{Invite: "inv9", LAN: "boardgame", LANName: "桌游夜",
			Inviter: proto.Person{ID: "leo", Name: "leo", DisplayName: "Leo"}, State: proto.InvitePending, ExpiresAt: t0}},
	})
	e.SetUserView(raw, 3)
	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)
	s := waitFor(t, l, "the M2 view", func(s *model.Snapshot) bool { return s.Loaded && len(s.LANs) == 1 })
	w := s.LANs[0]
	if !w.ApproveDevices || w.ApproveMembers || w.Waiting {
		t.Errorf("周末联机's switches: devices %v, members %v; waiting %v", w.ApproveDevices, w.ApproveMembers, w.Waiting)
	}
	if !slices.Equal(w.Members, []model.PersonID{"me", "jie"}) || !slices.Equal(w.PendingMembers, []model.PersonID{"lin"}) {
		t.Errorf("members %v, waiting %v", w.Members, w.PendingMembers)
	}
	if !slices.Equal(w.Devices, []model.DeviceID{"mac"}) || !slices.Equal(w.Pending, []model.DeviceID{"jpi"}) {
		t.Errorf("devices %v, waiting %v", w.Devices, w.Pending)
	}
	if len(w.Invited) != 1 || w.Invited[0] != (model.Invited{Person: "leo", By: "me", Invite: "inv1", Expires: t0}) {
		t.Errorf("invited %+v", w.Invited)
	}
	if m := s.Devices["mac"].Login; !m.Expires.Equal(t0.Add(48*time.Hour)) || !m.RenewFrom.Equal(t0) || m.Expired {
		t.Errorf("this Mac's login %+v", m)
	}
	if pi := s.Devices["jpi"]; !pi.Login.Expired || !pi.LastSeen.Equal(t0) {
		t.Errorf("the Pi's login %+v, last seen %v", pi.Login, pi.LastSeen)
	}
	if len(s.Invitations) != 1 || s.Invitations[0].ID != "inv9" || s.Invitations[0].LANName != "桌游夜" || s.Invitations[0].Inviter.Name != "Leo" {
		t.Errorf("invitations %+v", s.Invitations)
	}
	if len(s.Features) > 0 {
		t.Errorf("a view without features lists %v", s.Features)
	}
	// An M2 server lists what it does.
	var v proto.UserView
	json.Unmarshal(raw, &v)
	v.Features = proto.M2Features
	raw, _ = json.Marshal(v)
	e.SetUserView(raw, 4)
	s = waitFor(t, l, "the features", func(s *model.Snapshot) bool { return len(s.Features) > 0 })
	if !s.Can(model.FeatureApprove) || !s.Can(model.FeatureInvitePerson) || !s.Can(model.FeatureRenew) || s.Can(model.FeatureAddRelay) {
		t.Errorf("an M2 server does %v", s.Features)
	}
}

// An op the server does not know is turned down in plain words, never by
// its name.
func TestLiveOldServerSaysPlainly(t *testing.T) {
	w, l := live(t, fakeworld.Ready)
	waitFor(t, l, "the world", func(s *model.Snapshot) bool { return len(s.LANs) == 2 })
	w.E.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "不认识的操作：" + op.Op}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.Do(ctx, model.Op{Kind: model.OpDeviceApprove, LAN: model.LANID(fakeworld.Weekend), Device: "x"})
	var e *model.Error
	if !errors.As(err, &e) || e.Message != "服务器还没更新，暂时用不了" {
		t.Errorf("turned down as %v", err)
	}
}

// Pausing a LAN goes to this computer's engine, as `deneb lan pause` does:
// the LAN shows paused at once, the devices only it shares stop, and
// resuming brings them back.
func TestLivePause(t *testing.T) {
	_, l := live(t, fakeworld.Ready)
	waitFor(t, l, "the world", func(s *model.Snapshot) bool {
		return len(s.LANs) == 2 && s.Path(model.DeviceID(fakeworld.JiePC)).Kind == model.PathDirect
	})
	weekend := model.LANID(fakeworld.Weekend)
	if r := do(t, l, model.Op{Kind: model.OpLANPause, LAN: weekend, On: true}); r.Version != 0 {
		t.Errorf("pausing answers version %d", r.Version)
	}
	if !l.Snapshot().LAN(weekend).Paused {
		t.Fatal("周末联机 does not show paused at once")
	}
	waitFor(t, l, "阿杰's desktop paused", func(s *model.Snapshot) bool {
		return s.LAN(weekend).Paused && s.Path(model.DeviceID(fakeworld.JiePC)).Kind == model.PathPaused
	})
	if s := l.Snapshot(); s.LAN(model.LANID(fakeworld.Studio)).Paused || s.Path(model.DeviceID(fakeworld.Build)).Kind == model.PathPaused {
		t.Error("pausing 周末联机 paused 工作室 too")
	}
	do(t, l, model.Op{Kind: model.OpLANPause, LAN: weekend, On: false})
	waitFor(t, l, "阿杰's desktop back", func(s *model.Snapshot) bool {
		return !s.LAN(weekend).Paused && s.Path(model.DeviceID(fakeworld.JiePC)).Kind != model.PathPaused
	})
}

// What this computer uses now comes through as since when, for the "已连接"
// count, the oldest connection to each device; another program's tunnel
// taking Deneb's connections comes through as the amber pill's notice, its
// rule with it (DESIGN §6.7, §6.8).
func TestLiveUsingAndProxy(t *testing.T) {
	w, l := live(t, fakeworld.Ready)
	// Clash runs here (M6, DESIGN §6.14: the store names the program it
	// finds running).
	l.SetRunningPrograms(func(context.Context) ([]string, error) { return []string{"launchd", "verge-mihomo"}, nil })
	waitFor(t, l, "the world", func(s *model.Snapshot) bool { return len(s.LANs) == 2 })
	t0 := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	build := apitype.DeviceID(fakeworld.Build)
	w.E.Update(func(s *apitype.State) {
		s.Using = []apitype.ServiceUse{
			{DeviceID: build, Proto: "tcp", Port: 80, Conns: 1, Since: t0.Add(time.Minute)},
			{DeviceID: build, Proto: "tcp", Port: 22, Conns: 2, Since: t0},
		}
		s.Engine.Problems = append(s.Engine.Problems, apitype.Problem{Code: apitype.ProblemProxy, Message: "别的程序的网卡 utun7 接走了…", Rule: "IP-CIDR,10.48.0.0/16,DIRECT"})
	})
	s := waitFor(t, l, "the using and the notice", func(s *model.Snapshot) bool { return len(s.Using) > 0 && len(s.Notices) > 0 })
	if got := s.Using[model.DeviceID(build)]; !got.Equal(t0) {
		t.Errorf("构建服务器 used since %v, want the oldest, %v", got, t0)
	}
	if n := s.Notices[0]; n.Code != "proxy" || n.Short != "Clash 可能挡住连接" || n.Rule != "IP-CIDR,10.48.0.0/16,DIRECT" || strings.Contains(n.Message, "utun") {
		t.Errorf("the notice: %+v", n)
	}
	w.E.Update(func(s *apitype.State) { s.Using, s.Engine.Problems = nil, nil })
	waitFor(t, l, "nothing used, nothing to tell", func(s *model.Snapshot) bool { return len(s.Using) == 0 && len(s.Notices) == 0 })
}

// This computer without a network comes through as the engine says it
// (no-network), and goes as the engine takes it back; from an engine that
// does not say it, as the control server and every device out of reach at
// once, but never while anything connects (DESIGN §6.10).
func TestLiveNoNetwork(t *testing.T) {
	w, l := live(t, fakeworld.Ready)
	waitFor(t, l, "the world", func(s *model.Snapshot) bool { return len(s.LANs) == 2 && !s.NoNetwork })
	w.E.Update(func(s *apitype.State) {
		s.Engine.Problems = append(s.Engine.Problems, apitype.Problem{Code: "no-network", Message: "这台电脑没联网"})
	})
	waitFor(t, l, "no network", func(s *model.Snapshot) bool { return s.NoNetwork })
	w.E.Update(func(s *apitype.State) { s.Engine.Problems = nil })
	waitFor(t, l, "the network back", func(s *model.Snapshot) bool { return !s.NoNetwork })

	// An engine that does not say it: the server out of reach, and every
	// device too.
	w.E.Update(func(s *apitype.State) {
		s.ControlConnected = false
		for i := range s.Paths {
			s.Paths[i].Kind = apitype.PathUnreachable
		}
	})
	waitFor(t, l, "no network, as everything is out of reach", func(s *model.Snapshot) bool { return s.NoNetwork })
	w.E.Update(func(s *apitype.State) { s.Paths[0].Kind = apitype.PathDirect })
	waitFor(t, l, "a device connects: there is a network", func(s *model.Snapshot) bool { return !s.NoNetwork })
	w.E.Update(func(s *apitype.State) {
		s.ControlConnected = true
		for i := range s.Paths {
			s.Paths[i].Kind = apitype.PathUnreachable
		}
	})
	waitFor(t, l, "the server answers: there is a network", func(s *model.Snapshot) bool { return !s.NoNetwork })
	// As an engine starts: the server not yet reached, every device being
	// connected to. That is no sign of a missing network.
	w.E.Update(func(s *apitype.State) {
		s.ControlConnected = false
		for i := range s.Paths {
			s.Paths[i].Kind = apitype.PathConnecting
		}
	})
	waitFor(t, l, "every device being connected to", func(s *model.Snapshot) bool {
		return s.Path(model.DeviceID(fakeworld.Build)).Kind == model.PathConnecting
	})
	if l.Snapshot().NoNetwork {
		t.Error("an engine starting up shows no network")
	}
}
