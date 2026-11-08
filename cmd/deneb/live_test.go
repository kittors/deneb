package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// The view on the live store, the M1 acceptance steps played in the fake
// world (implementation.md §3 M1, steps 2, 3, 5, 7, 9): the store watches a
// fake engine over a socket, as it would this computer's, and the world
// behind it decides ops as the control server does.

// liveRig is the view on a live store and the world it watches.
type liveRig struct {
	t      *testing.T
	a      *app
	tt     *ui.Tester
	w      *fakeworld.World
	mu     sync.Mutex
	copied []string
	opened []string
	frames string // where to write frames, when DENEB_FRAMES names a folder
	n      int
	name   string
}

func newLiveRig(t *testing.T, start fakeworld.Start, name string) *liveRig {
	t.Helper()
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
	r := &liveRig{t: t, w: w, name: name}
	r.a = newApp(l, &places{at: map[model.LANID]map[string]ui.Point{}})
	r.a.copyText = func(s string) { r.mu.Lock(); r.copied = append(r.copied, s); r.mu.Unlock() }
	r.a.openURL = func(s string) error { r.mu.Lock(); r.opened = append(r.opened, s); r.mu.Unlock(); return nil }
	r.tt = ui.NewTester(r.a.view, 1180, 740)
	if dir := os.Getenv("DENEB_FRAMES"); dir != "" {
		r.frames = dir
		r.tt.SetScale(2)
	}
	return r
}

// until shows frames as time goes by, until ok holds.
func (r *liveRig) until(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		r.tt.Frame()
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s; the window shows %q", what, r.tt.Texts())
		}
		time.Sleep(8 * time.Millisecond)
		r.tt.Advance(16 * time.Millisecond)
	}
}

// settle lets the view's motion end, with the store's news taken in.
func (r *liveRig) settle(d time.Duration) {
	for end := r.tt.Now().Add(d); r.tt.Now().Before(end); {
		r.tt.Frame()
		r.tt.Advance(16 * time.Millisecond)
	}
}

// shot writes the frame for review, when DENEB_FRAMES names a folder.
func (r *liveRig) shot(step string) {
	if r.frames == "" {
		return
	}
	r.n++
	f, err := os.Create(filepath.Join(r.frames, fmt.Sprintf("live-%s_%02d_%s.png", r.name, r.n, step)))
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, r.tt.Image())
}

func (r *liveRig) click(text string) {
	r.t.Helper()
	if err := r.tt.Click(text); err != nil {
		r.t.Fatalf("click %q: %v; the window shows %q", text, err, r.tt.Texts())
	}
}

// at is where d shows in the window.
func (r *liveRig) at(d *device) ui.Point {
	return ui.Point{X: sidebarWidth + d.live.X, Y: headerHeight + d.live.Y}
}

// login logs in from the window's one button.
func (r *liveRig) login() {
	r.t.Helper()
	r.until("the login button", func() bool { return r.tt.HasText("登录") })
	r.click("登录")
	r.until("the LANs", func() bool { return r.tt.HasText("新建局域网") })
}

func (r *liveRig) store() *model.Snapshot { return r.a.store.Snapshot() }

const (
	build   = string(fakeworld.Build)
	jiePC   = string(fakeworld.JiePC)
	jieNAS  = string(fakeworld.JieNAS)
	weekend = model.LANID(fakeworld.Weekend)
)

// The first time the window opens, there is only the button to log in, and
// the login goes on in the browser (§3 M1 step 1).
func TestLiveFirstOpen(t *testing.T) {
	r := newLiveRig(t, fakeworld.Fresh, "1-login")
	r.until("the login button", func() bool { return r.tt.HasText("登录") })
	r.shot("only-the-button")
	for _, s := range r.tt.Texts() {
		if strings.TrimSpace(s) != "" && s != "登录" {
			t.Errorf("%q shows before the login", s)
		}
	}
	r.click("登录")
	r.until("the browser", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.opened) == 1 })
	r.tt.Frame()
	r.shot("waiting-for-the-browser")
	r.until("the LANs", func() bool { return r.tt.HasText("新建局域网") })
	r.settle(400 * time.Millisecond)
	r.shot("logged-in")
	if !r.tt.HasText("kittors") || !r.tt.HasText("还没有局域网") {
		t.Errorf("after the login the window shows %q", r.tt.Texts())
	}
}

// Step 2: make 工作室; this computer is in it, and 构建服务器 waits in the
// tray. Dragged in, its link shows 正在连接… and then 直接连上.
func TestLiveMakeALAN(t *testing.T) {
	r := newLiveRig(t, fakeworld.Fresh, "2-make-a-lan")
	r.login()
	r.click("新建局域网")
	r.tt.Frame()
	r.tt.Type("工作室")
	r.tt.Frame()
	r.shot("naming")
	r.tt.Key(0, ui.KeyEnter)
	r.until("工作室 on the canvas", func() bool {
		l := r.a.snap.LAN(r.a.lan)
		return l != nil && l.Name == "工作室" && r.a.device(r.a.self) != nil
	})
	r.settle(600 * time.Millisecond)
	r.shot("made")
	if !slices.ContainsFunc(r.a.tray, func(d *device) bool { return d.id == build }) {
		t.Fatalf("构建服务器 is not in the tray: %d there", len(r.a.tray))
	}
	tray, ok := r.tt.Find("拖出来，加进局域网")
	if !ok {
		t.Fatal("no tray")
	}
	drag(r.tt, ui.Point{X: tray.X + tray.W + 34, Y: tray.Y + tray.H/2}, ui.Point{X: 760, Y: 260})
	// What its link shows on the way: 正在连接… first, then direct, never
	// the relay the engine goes through for a moment. (Judged by what
	// shows, frame after frame: the store debounces in real time, and a busy
	// machine renders the view's frames slower than they are timed.)
	seen := map[string]bool{}
	shot := false
	r.until("构建服务器 connected", func() bool {
		d := r.a.device(build)
		if d == nil {
			return false
		}
		seen[d.path] = true
		if d.path == "connecting" && !shot {
			shot = true
			r.settle(200 * time.Millisecond)
			r.shot("connecting")
		}
		return d.path == "direct"
	})
	if !seen["connecting"] || seen["relay"] {
		t.Errorf("on its way in, its link showed %v: want 正在连接… and never the relay", seen)
	}
	r.settle(900 * time.Millisecond)
	r.shot("connected")
	if !r.tt.HasText("直接连上") {
		t.Errorf("its second line is not 直接连上: %q", r.tt.Texts())
	}
	if l := r.store().LAN(r.a.lan); l == nil || !l.Has(model.DeviceID(build)) {
		t.Error("the store does not have 构建服务器 in 工作室")
	}
}

// Step 3: 阿杰's invite link opens the card "阿杰邀请你加入「周末联机」";
// 加入 adds the sidebar's row, and his two devices come onto the canvas.
func TestLiveInvite(t *testing.T) {
	r := newLiveRig(t, fakeworld.Fresh, "3-invite")
	r.login()
	r.a.post(func(c *ui.Context) { r.a.openLink(c, fakeworld.InviteLink) })
	r.until("the invite card", func() bool { return r.tt.HasText("阿杰邀请你加入「周末联机」") })
	r.settle(400 * time.Millisecond)
	r.shot("card")
	r.click("加入")
	r.until("周末联机 on the canvas", func() bool {
		return r.a.lan == weekend && r.a.device(jiePC) != nil && r.a.device(jieNAS) != nil
	})
	r.settle(150 * time.Millisecond)
	r.shot("joining")
	r.settle(1200 * time.Millisecond)
	r.shot("joined")
	if !r.tt.HasText("周末联机") || r.tt.HasText("阿杰邀请你加入「周末联机」") {
		t.Errorf("after joining the window shows %q", r.tt.Texts())
	}
	r.until("both connected", func() bool {
		return r.a.device(jiePC).path == "direct" && r.a.device(jieNAS).path == "relay"
	})
	r.settle(900 * time.Millisecond)
	r.shot("connected")
}

// The sidebar's LANs switch the canvas, and switching touches nothing but
// what the window shows.
func TestLiveSwitchLANs(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "4-switch")
	r.until("工作室", func() bool { return r.a.device(build) != nil })
	before := r.store().Version
	for i := 0; i < 10; i++ {
		r.click("周末联机")
		r.until("周末联机", func() bool { return r.a.lan == weekend && r.a.device(jieNAS) != nil })
		r.click("工作室")
		r.until("工作室", func() bool { return r.a.device(build) != nil })
	}
	if v := r.store().Version; v != before {
		t.Errorf("switching LANs changed the world: version %d, was %d", v, before)
	}
}

// Step 7: 构建服务器 waits in 周末联机's tray; dragged in, it is in the LAN;
// dragged back to the tray, it is out; the toast's undo brings it back.
func TestLiveDeviceInAndOut(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "7-in-and-out")
	r.until("the LANs", func() bool { return r.tt.HasText("周末联机") })
	r.click("周末联机")
	r.until("周末联机", func() bool { return r.a.lan == weekend && r.a.device(jieNAS) != nil })
	r.settle(600 * time.Millisecond)
	r.shot("tray")
	in := func() bool { l := r.store().LAN(weekend); return l != nil && l.Has(model.DeviceID(build)) }
	tray, ok := r.tt.Find("拖出来，加进局域网")
	if !ok {
		t.Fatal("no tray")
	}
	drag(r.tt, ui.Point{X: tray.X + tray.W + 34, Y: tray.Y + tray.H/2}, ui.Point{X: 720, Y: 250})
	r.until("构建服务器 in 周末联机", in)
	r.settle(1200 * time.Millisecond)
	r.shot("in")
	b := r.a.device(build)
	if b == nil {
		t.Fatal("构建服务器 is not on the canvas")
	}
	drag(r.tt, r.at(b), ui.Point{X: r.at(b).X, Y: 740 - 40})
	r.until("构建服务器 out", func() bool { return !in() })
	r.settle(1500 * time.Millisecond)
	r.shot("out")
	if r.a.device(build) != nil {
		t.Fatal("构建服务器 is still on the canvas")
	}
	r.click("撤销")
	r.until("构建服务器 back", in)
	r.settle(1200 * time.Millisecond)
	r.shot("undone")
	if r.a.device(build) == nil {
		t.Error("undo did not bring it back onto the canvas")
	}
}

// Step 9: refusing 阿杰 on this computer cuts his links to it and says so on
// his devices; a second click lets him back.
func TestLiveRefuse(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "9-refuse")
	r.until("the LANs", func() bool { return r.tt.HasText("周末联机") })
	r.click("周末联机")
	r.until("周末联机", func() bool { return r.a.lan == weekend && r.a.device(jieNAS) != nil })
	r.settle(600 * time.Millisecond)
	me := r.a.device(r.a.self)
	r.tt.ClickAt(r.at(me).X, r.at(me).Y)
	r.until("this computer's card", func() bool { return r.tt.HasText("谁能用") })
	r.settle(400 * time.Millisecond)
	r.shot("card")
	refused := func() bool { return r.store().Devices[model.DeviceID(r.a.self)].Refused[model.PersonID(fakeworld.Jie)] }
	r.click("阿杰")
	r.until("阿杰 refused", refused)
	r.settle(700 * time.Millisecond)
	r.shot("refused")
	for _, id := range []string{jiePC, jieNAS} {
		d := r.a.device(id)
		if r.a.status(d) != "用不了这台电脑" || d.cut < 0.99 {
			t.Errorf("%s says %q, its link cut %.2f", id, r.a.status(d), d.cut)
		}
	}
	r.click("阿杰")
	r.until("阿杰 back", func() bool { return !refused() })
	r.settle(700 * time.Millisecond)
	r.shot("let-back")
	if d := r.a.device(jiePC); d.cut > 0.01 {
		t.Errorf("his link is still cut %.2f", d.cut)
	}
}

// Step 5: 阿杰 takes his NAS out of 周末联机 on his computer: it shrinks away
// here, its track drawn back in, and the rest stay as they are.
func TestLiveLeftElsewhere(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "5-left-elsewhere")
	r.until("the LANs", func() bool { return r.tt.HasText("周末联机") })
	r.click("周末联机")
	r.until("周末联机", func() bool { return r.a.lan == weekend && r.a.device(jieNAS) != nil })
	r.settle(600 * time.Millisecond)
	pc := r.a.device(jiePC).at()
	if err := r.w.JieTakesOut(fakeworld.JieNAS, false); err != nil {
		t.Fatal(err)
	}
	r.until("the NAS going", func() bool { d := r.a.device(jieNAS); return d == nil || !d.gone.IsZero() })
	r.settle(120 * time.Millisecond)
	r.shot("going")
	r.until("the NAS gone", func() bool { return r.a.device(jieNAS) == nil })
	r.settle(300 * time.Millisecond)
	r.shot("gone")
	if d := r.a.device(jiePC); d == nil || d.at() != pc {
		t.Error("阿杰的台式机 moved, or went too")
	}
	if err := r.w.JieTakesOut(fakeworld.JieNAS, true); err != nil {
		t.Fatal(err)
	}
	r.until("the NAS back", func() bool { return r.a.device(jieNAS) != nil })
	r.settle(1500 * time.Millisecond)
	r.shot("back")
}

// An op the store turns down comes off again: the device goes back where it
// was, and the toast says why in the server's words.
func TestLiveTurnedDown(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "turned-down")
	r.until("the LANs", func() bool { return r.tt.HasText("周末联机") })
	r.click("周末联机")
	r.until("周末联机", func() bool { return r.a.lan == weekend && r.a.device(jieNAS) != nil })
	r.settle(600 * time.Millisecond)
	// The control server goes out of reach: the engine says so to every op.
	r.w.E.Update(func(st *apitype.State) { st.ControlConnected = false })
	tray, ok := r.tt.Find("拖出来，加进局域网")
	if !ok {
		t.Fatal("no tray")
	}
	drag(r.tt, ui.Point{X: tray.X + tray.W + 34, Y: tray.Y + tray.H/2}, ui.Point{X: 720, Y: 250})
	r.until("the toast", func() bool { return r.tt.HasText("连不上 Deneb 服务器，稍后再试") })
	r.settle(1500 * time.Millisecond)
	r.shot("back-in-the-tray")
	if r.a.device(build) != nil || !slices.ContainsFunc(r.a.tray, func(d *device) bool { return d.id == build }) {
		t.Error("构建服务器 did not go back into the tray")
	}
}
