package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/localclient"
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
