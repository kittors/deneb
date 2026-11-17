package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// slowStore is the demo answering as a store far away does. While slow, a
// Do, Login, Open or Preview, a refresh of the numbers, and the checks and
// downloads the test hands to the app's updater wait till the test lets
// them through (let), so that what the window shows while it waits is in
// the frames. Snapshots come at once.
type slowStore struct {
	d    *client.Demo
	a    *app
	slow bool

	mu      sync.Mutex
	held    []chan struct{}
	fail    error
	loading bool                    // a refresh of the numbers held
	snapOf  *model.Snapshot         // the demo's snapshot loadingSnap is of
	loadSn  *model.Snapshot         // it, saying the numbers are coming
	subs    []func(*model.Snapshot) // the window's, told of loading
}

func (s *slowStore) Snapshot() *model.Snapshot {
	snap := s.d.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loading {
		return snap
	}
	if s.snapOf != snap {
		n := *snap
		n.MetricsLoading = true
		s.snapOf, s.loadSn = snap, &n
	}
	return s.loadSn
}

func (s *slowStore) Subscribe(fn func(*model.Snapshot)) func() { return s.d.Subscribe(fn) }

// hold waits, while slow, till the test lets the call through; it says
// what the test let it through with.
func (s *slowStore) hold(ctx context.Context) error {
	s.mu.Lock()
	if !s.slow {
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.held = append(s.held, ch)
	s.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fail
}

func (s *slowStore) Do(ctx context.Context, op model.Op) (model.Result, error) {
	if err := s.hold(ctx); err != nil {
		return model.Result{}, err
	}
	return s.d.Do(ctx, op)
}

func (s *slowStore) Login(ctx context.Context) (string, error) {
	if err := s.hold(ctx); err != nil {
		return "", err
	}
	return s.d.Login(ctx)
}

func (s *slowStore) Open(ctx context.Context, dev model.DeviceID, svc model.Service) (string, error) {
	if err := s.hold(ctx); err != nil {
		return "", err
	}
	return s.d.Open(ctx, dev, svc)
}

func (s *slowStore) Preview(ctx context.Context, link string) (*model.Invite, error) {
	if err := s.hold(ctx); err != nil {
		return nil, err
	}
	return s.d.Preview(ctx, link)
}

func (s *slowStore) MonitorDevices(on bool) { s.d.MonitorDevices(on) }

// RefreshDeviceMetrics says the numbers are coming, as the live store does,
// till the test lets the refresh through.
func (s *slowStore) RefreshDeviceMetrics() {
	s.mu.Lock()
	slow := s.slow
	s.loading = slow
	s.mu.Unlock()
	if !slow {
		s.d.RefreshDeviceMetrics()
		return
	}
	s.a.post(func(*ui.Context) {})
	go func() {
		s.hold(context.Background())
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
		s.d.RefreshDeviceMetrics()
		s.a.post(func(*ui.Context) {})
	}()
}

// let lets the calls held through, failing them with fail if given, and
// gives their answers a moment (real time) to reach the window's inbox.
func (s *slowStore) let(fail error) {
	s.mu.Lock()
	held := s.held
	s.held, s.fail = nil, fail
	s.mu.Unlock()
	for _, ch := range held {
		close(ch)
	}
	time.Sleep(60 * time.Millisecond)
}

// waitRig is the demo at the window's usual size behind a slowStore, not
// slow yet, recording into <root>/<theme>-<key>/ once begin is called.
type waitRig struct {
	*m3rig
	s    *slowStore
	root string
	key  string
	// marks are the frames things happened in, as "press 41": written to
	// marks.txt beside the frames, for the frame check.
	marks []string
}

// mark notes that what happened in the frame recorded next.
func (w *waitRig) mark(what string) {
	w.marks = append(w.marks, fmt.Sprintf("%s %d", what, w.n))
}

func newWaitRig(t *testing.T, root, theme string, dark bool, key string, plain bool) *waitRig {
	t.Helper()
	d := client.NewDemo()
	d.Manual()
	if !plain {
		// The demo as far as M7, as the window is now.
		for _, e := range []string{"m3", "m4", "m5", "m6", "m7"} {
			d.Simulate(e)
		}
	}
	s := &slowStore{d: d}
	a := newApp(s, demoPlaces())
	s.a = a
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.tt.SetDark(dark)
	r.a.selected, r.a.more = "", false
	r.move(1000, 660)
	r.run(1200 * time.Millisecond)
	return &waitRig{m3rig: r, s: s, root: root, key: theme + "-" + key}
}

// begin starts the recording, the store slow from now.
func (w *waitRig) begin() {
	w.s.mu.Lock()
	w.s.slow = true
	w.s.mu.Unlock()
	w.dir = filepath.Join(w.root, w.key)
	w.still = w.dir
	reset(w.t, w.dir)
	w.run(300 * time.Millisecond)
}

// press glides to what shows s and clicks it, as a hand does.
func (w *waitRig) press(s string) {
	w.t.Helper()
	b, ok := w.tt.Find(s)
	if !ok {
		w.t.Fatalf("%s: no %q to press: %q", w.key, s, w.tt.Texts())
	}
	w.glide(at(b.X+b.W/2, b.Y+b.H/2), 18)
	w.run(200 * time.Millisecond)
	w.shot("hover")
	w.mark("press")
	w.tt.Press(w.px, w.py)
	w.run(80 * time.Millisecond)
	w.shot("press")
	w.mark("release")
	w.tt.Release(w.px, w.py)
}

// answer has the store take d over the call, then lets it through.
func (w *waitRig) answer(d time.Duration, fail error) {
	w.run(d)
	w.shot("waiting")
	w.mark("let")
	w.s.let(fail)
	w.run(900 * time.Millisecond)
	w.shot("done")
}

// TestWaitFrames records what the window shows while it waits for the
// store, the updater or the engine, each place one scene, when DENEB_WAITS
// names a folder (the 13th batch: design/plan/m7/loading-and-dividers.md). Its
// frames, a 60th of a second apart, go to <theme>-<scene>/, the stills
// beside them. DENEB_WAITS_ONLY picks scenes; DENEB_WAITS_THEMES the themes
// (light by default).
func TestWaitFrames(t *testing.T) {
	root := os.Getenv("DENEB_WAITS")
	if root == "" {
		t.Skip("set DENEB_WAITS to a folder to record the waits")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_WAITS_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	themes := strings.Split(os.Getenv("DENEB_WAITS_THEMES"), ",")
	if themes[0] == "" {
		themes = []string{"light"}
	}
	for _, theme := range themes {
		for _, sc := range waitScenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			w := newWaitRig(t, root, theme, theme == "dark", sc.key, sc.plain)
			sc.play(w)
			if w.dir != "" {
				os.WriteFile(filepath.Join(w.dir, "marks.txt"), []byte(strings.Join(w.marks, "\n")+"\n"), 0o644)
			}
		}
	}
}

var errOffline = errors.New("dial tcp: no route to host")

var waitScenes = []struct {
	key  string
	play func(w *waitRig)
	// plain: the demo before M3, where an invite by name still comes (as
	// far as M7, the person is in 桌游夜 already).
	plain bool
}{
	// 设置 · 软件更新: a check that finds this the latest, as most do.
	{"update-current", func(w *waitRig) {
		w.updaterHeld(nil)
		w.openWords("设置")
		w.begin()
		w.press("软件更新")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// The same, quick: the server near.
	{"update-fast", func(w *waitRig) {
		w.updaterHeld(nil)
		w.openWords("设置")
		w.begin()
		w.press("软件更新")
		w.answer(150*time.Millisecond, nil)
	}, false},
	// A new version: the update's panel, then its download in ten steps,
	// the signature checked, ready.
	{"update-new", func(w *waitRig) {
		w.updaterHeld(&appRelease{version: "0.1.9", notes: "连不上 Deneb 服务器时，窗口不再一直停在「正在连接」。", install: w.installHeld()})
		w.openWords("设置")
		w.begin()
		w.press("软件更新")
		w.answer(1200*time.Millisecond, nil)
		w.press("下载更新")
		for i := 0; i < 10; i++ {
			w.run(150 * time.Millisecond)
			if i == 4 {
				w.shot("downloading")
			}
			w.mark("let")
			w.s.let(nil)
		}
		w.run(600 * time.Millisecond)
		w.shot("verifying")
		w.mark("let")
		w.s.let(nil)
		w.run(900 * time.Millisecond)
		w.shot("ready")
	}, false},
	// The check fails: no network to the download site.
	{"update-fail", func(w *waitRig) {
		w.updaterHeld(nil)
		w.openWords("设置")
		w.begin()
		w.press("软件更新")
		w.answer(1200*time.Millisecond, errOffline)
	}, false},
	// 重命名: the name saved; before a change, 确认 disabled.
	{"rename", func(w *waitRig) {
		w.openAt(w.device("game"))
		w.openWords("重命名")
		w.dir = filepath.Join(w.root, w.key)
		w.still = w.dir
		w.shot("disabled")
		w.dir = ""
		w.tt.Type("2")
		w.run(300 * time.Millisecond)
		w.begin()
		w.press("确认")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// The same, refused: the server out of reach.
	{"rename-fail", func(w *waitRig) {
		w.openAt(w.device("game"))
		w.openWords("重命名")
		w.tt.Type("2")
		w.run(300 * time.Millisecond)
		w.begin()
		w.press("确认")
		w.answer(1200*time.Millisecond, errOffline)
	}, false},
	// 新建局域网: named, and Return.
	{"newlan", func(w *waitRig) {
		w.openWords("新建局域网")
		w.tt.Type("读书会")
		w.run(300 * time.Millisecond)
		w.begin()
		w.mark("release")
		w.tt.Key(0, ui.KeyEnter)
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// An invite by name: 加入.
	{"join", func(w *waitRig) {
		w.d.Simulate("invitation")
		w.run(1500 * time.Millisecond)
		w.begin()
		w.press("加入")
		w.answer(1200*time.Millisecond, nil)
	}, true},
	// The same, refused: the server out of reach. The card stays, and a
	// toast says why.
	{"join-fail", func(w *waitRig) {
		w.d.Simulate("invitation")
		w.run(1500 * time.Millisecond)
		w.begin()
		w.press("加入")
		w.answer(1200*time.Millisecond, errOffline)
	}, true},
	// 我的设备 · 添加服务: a name and a port, then 添加.
	{"add-service", func(w *waitRig) {
		me := w.a.snap.Device("mac").Name
		w.openMine()
		w.tap(w.text(say("给", me, "添加服务")))
		w.run(600 * time.Millisecond)
		w.field("名称", "我的网站")
		w.field("端口", "8080")
		w.run(300 * time.Millisecond)
		w.begin()
		w.press("添加")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// A service's 打开 on a device's card.
	{"open", func(w *waitRig) {
		w.openAt(w.device("game"))
		w.begin()
		w.press("打开")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// 登出, on the account card.
	{"logout", func(w *waitRig) {
		w.openWords("账号")
		w.begin()
		w.press("登出")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// 邀请朋友, in the header.
	{"invite", func(w *waitRig) {
		w.begin()
		w.press("邀请朋友")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// My Devices, a device's 刷新 of its numbers.
	{"refresh", func(w *waitRig) {
		w.openWords("我的设备")
		w.openWords("详情kittors 的 MacBook")
		w.begin()
		w.press("刷新监控数据")
		w.answer(1200*time.Millisecond, nil)
	}, false},
	// 导出诊断包, from this computer's 更多: the demo pretends 1.3 s.
	{"diag", func(w *waitRig) {
		w.openAt(w.device("mac"))
		w.openWords("更多")
		w.begin()
		w.press("导出诊断包")
		w.run(800 * time.Millisecond)
		w.shot("waiting")
		w.run(420 * time.Millisecond)
		w.mark("let")
		w.run(1180 * time.Millisecond)
		w.shot("done")
	}, false},
}

// installHeld is a download in ten steps, each when the test lets it, and
// then its signature checked, when the test lets it once more.
func (w *waitRig) installHeld() func(context.Context, func(int64, int64)) error {
	return func(ctx context.Context, progress func(int64, int64)) error {
		const total = 18 << 20
		for i := int64(1); i <= 10; i++ {
			if err := w.s.hold(ctx); err != nil {
				return err
			}
			progress(i*total/10, total)
		}
		return w.s.hold(ctx)
	}
}

// updaterHeld gives the app an updater whose checks wait for the test, as
// one far away does, and find release (nil: this is the latest); it has
// checked once already, at the start, and found this the latest.
func (w *waitRig) updaterHeld(release *appRelease) {
	u := newAppUpdater(w.a)
	u.stage = updateCurrent
	u.check = func(ctx context.Context) (*appRelease, error) {
		if err := w.s.hold(ctx); err != nil {
			return nil, err
		}
		return release, nil
	}
	w.a.updates = u
}
