package main

import (
	"context"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// TestRealAcceptance plays the part the window takes in the M1 acceptance
// steps (implementation.md §3 M1) on this computer's real engine, when
// DENEB_REAL lists the steps to play, as "2,3,4,7,8,9". The view runs in a
// tester, on the live store of the engine at the default socket (or
// DENEB_SOCKET): the engine the dev window shows, so what is done here shows
// there too, as done elsewhere. The control server is the real one.
//
// It drags and clicks as a hand would, waits for the real answers, and
// logs the moment of everything it does (for the engine line's timings of
// the connections), checking what the window shows on the way. With
// DENEB_FRAMES naming a folder it records each step at 60 frames a second
// of the view's own clock, and a frame at twice the size where each step
// ends (DESIGN §11), in the dark theme with DENEB_DARK set. Step 3 takes
// the invite link 阿杰 made from DENEB_REAL_INVITE. Steps 5 and 10 watch
// what is done elsewhere, and do it themselves to know its moment: step 5
// runs DENEB_REAL5_CMD, which takes 阿杰的 NAS out of 周末联机 on his
// desktop, then DENEB_REAL5_UNDO, which brings it back; step 10 runs
// denebd, from PATH, to block and unblock the UDP to 构建服务器.
func TestRealAcceptance(t *testing.T) {
	steps := os.Getenv("DENEB_REAL")
	if steps == "" {
		t.Skip("set DENEB_REAL to the steps to play on this computer's engine, as 2,3,4,7,8,9")
	}
	r := newRealRig(t)
	r.until("the engine, logged in", func() bool { return r.a.snap.Engine && r.a.snap.LoggedIn && r.a.snap.Loaded })
	for _, s := range strings.Split(steps, ",") {
		fn := map[string]func(*realRig){"2": real2, "3": real3, "4": real4, "5": real5, "7": real7, "8": real8, "9": real9, "10": real10}[strings.TrimSpace(s)]
		if fn == nil {
			t.Fatalf("no step %q", s)
		}
		r.step = strings.TrimSpace(s)
		r.logf("step %s begins", r.step)
		fn(r)
		r.logf("step %s ends", r.step)
	}
}

type realRig struct {
	t      *testing.T
	a      *app
	lc     *localclient.Client
	tt     *ui.Tester
	woken  atomic.Bool
	frames string
	step   string
	n      int
	rec    bool
	recIn  string // the step n numbers the frames of
	// watch, when set, looks at every frame as it is shown: the steps that
	// wait for something done elsewhere catch its moment there.
	watch func()
}

func newRealRig(t *testing.T) *realRig {
	lc := localclient.New(os.Getenv("DENEB_SOCKET"))
	l := client.NewLive(lc)
	l.Start()
	t.Cleanup(l.Close)
	// Where things rest, as the dev window keeps them, but never written
	// from here.
	at := loadPlaces(placesFile())
	at.file = ""
	r := &realRig{t: t, a: newApp(l, at), lc: lc, frames: os.Getenv("DENEB_FRAMES")}
	r.a.setWake(func() { r.woken.Store(true) })
	r.a.copyText = func(string) {}
	r.a.openURL = func(string) error { return nil }
	r.tt = ui.NewTester(r.a.view, 1180, 740)
	// DENEB_DARK records in the dark theme (DESIGN §11: both themes).
	r.tt.SetDark(os.Getenv("DENEB_DARK") != "")
	r.tt.Frame()
	return r
}

// logf logs at this moment, and while recording, at the frame that shows
// it: the frames of each moment are found from the log (DESIGN §11).
func (r *realRig) logf(format string, args ...any) {
	frame := ""
	if r.rec && r.frames != "" {
		frame = fmt.Sprintf(" [real-%s/%05d]", r.step, r.n)
	}
	r.t.Logf("%s%s  %s", time.Now().Format("15:04:05.000"), frame, fmt.Sprintf(format, args...))
}

// tick shows one frame, 1/60 s on the view's clock, with what came from the
// store, and records it when recording.
func (r *realRig) tick() {
	if r.woken.Swap(false) {
		r.tt.Frame()
	}
	r.tt.Advance(time.Second / 60)
	if r.rec && r.frames != "" {
		dir := filepath.Join(r.frames, "real-"+r.step)
		os.MkdirAll(dir, 0o755)
		if f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%05d.jpg", r.n))); err == nil {
			jpeg.Encode(f, r.tt.Image(), &jpeg.Options{Quality: 88})
			f.Close()
		}
		r.n++
	}
	if r.watch != nil {
		r.watch()
	}
}

// until shows frames until ok holds, for up to a minute: the real network.
func (r *realRig) until(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("step %s: timed out waiting for %s; the window shows %q", r.step, what, r.tt.Texts())
		}
		r.tick()
		time.Sleep(5 * time.Millisecond)
	}
}

// hold shows frames for d of real time.
func (r *realRig) hold(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		r.tick()
	}
}

// record starts recording the step's frames. A step recording again after
// a still numbers on from where it was, so it writes over none it took.
func (r *realRig) record() {
	if r.recIn != r.step {
		r.recIn, r.n = r.step, 0
	}
	r.rec = true
}

// still writes the frame the step ends on, at twice the size.
func (r *realRig) still(name string) {
	r.rec = false
	if r.frames == "" {
		return
	}
	r.tt.SetScale(2)
	r.tt.Frame()
	if f, err := os.Create(filepath.Join(r.frames, fmt.Sprintf("real-%s_%s.png", r.step, name))); err == nil {
		png.Encode(f, r.tt.Image())
		f.Close()
	}
	r.tt.SetScale(1)
	r.tt.Frame()
}

func (r *realRig) click(text string) {
	r.t.Helper()
	if err := r.tt.Click(text); err != nil {
		r.t.Fatalf("step %s: click %q: %v; the window shows %q", r.step, text, err, r.tt.Texts())
	}
	r.tick()
}

// drag presses at from, moves to `to` as a hand would, frame by frame, and
// lets go.
func (r *realRig) drag(from, to ui.Point) {
	r.tt.Press(from.X, from.Y)
	for i := 1; i <= 20; i++ {
		k := float32(i) / 20
		r.tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		r.tick()
	}
	r.tt.Release(to.X, to.Y)
	r.tick()
}

// toTray drags the device of id into the tray as a hand would: toward the
// middle of the tray, wherever it is in each frame as it rises, and lets go
// only once the tray says 松手，移出局域网. The drop counts only on the
// tray, so a hand aims there and waits to see it armed.
func (r *realRig) toTray(id string) {
	r.t.Helper()
	d := r.a.device(id)
	if d == nil {
		r.t.Fatalf("step %s: %s is not on the canvas", r.step, id)
	}
	aim := func() ui.Point {
		z := r.a.trayZone(r.a.cw, r.a.ch)
		return ui.Point{X: r.a.origin.X + z.X + z.W/2, Y: r.a.origin.Y + z.Y + z.H/2}
	}
	said := ""
	look := func() bool {
		for _, s := range []string{"松手，移出局域网", "拖到这里，移出局域网"} {
			if r.tt.HasText(s) {
				if s != said {
					said = s
					r.logf("the tray says %s", s)
				}
				return s == "松手，移出局域网"
			}
		}
		return false
	}
	from := r.at(d)
	at := from
	r.tt.Press(from.X, from.Y)
	for i := 1; i <= 20; i++ {
		k := float32(i) / 20
		to := aim()
		at = ui.Point{X: from.X + (to.X-from.X)*k, Y: from.Y + (to.Y-from.Y)*k}
		r.tt.Move(at.X, at.Y)
		r.tick()
		look()
	}
	for deadline := time.Now().Add(2 * time.Second); !look(); {
		if time.Now().After(deadline) {
			r.t.Fatalf("step %s: over the tray, it never says 松手，移出局域网; the window shows %q", r.step, r.tt.Texts())
		}
		at = aim()
		r.tt.Move(at.X, at.Y)
		r.tick()
	}
	r.tt.Release(at.X, at.Y)
	r.tick()
}

// shell runs command in sh as the frames go on, and returns the moment it
// came back. The step fails if the command does, or takes over 2 minutes.
func (r *realRig) shell(command string) time.Time {
	r.t.Helper()
	r.logf("running %s", command)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out []byte
	done := make(chan error, 1)
	go func() {
		var err error
		out, err = exec.CommandContext(ctx, "/bin/sh", "-c", command).CombinedOutput()
		done <- err
	}()
	for {
		select {
		case err := <-done:
			back := time.Now()
			if err != nil {
				r.t.Fatalf("step %s: %s: %v\n%s", r.step, command, err, out)
			}
			r.logf("it came back: %s", strings.TrimSpace(string(out)))
			return back
		default:
			r.tick()
		}
	}
}

// engineSays logs every path the engine reports for the device of id, raw,
// as the store gets it before debouncing it, until stop: what the window
// shows is then seen against what it was told.
func (r *realRig) engineSays(id string) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var last apitype.PeerPath
		err := r.lc.Watch(ctx, func(e apitype.Event) error {
			paths := e.Paths
			if e.State != nil {
				paths = e.State.Paths
			}
			for _, p := range paths {
				if string(p.DeviceID) != id || p.Kind == last.Kind && p.Relay == last.Relay && p.Endpoint == last.Endpoint {
					continue
				}
				last = p
				where := ""
				switch {
				case p.Relay != "":
					where = " via " + string(p.Relay)
				case p.Endpoint != "":
					where = " at " + p.Endpoint
				}
				// Not logf: the frame it counts is the test's own.
				r.t.Logf("%s  the engine says %s%s, since %s", time.Now().Format("15:04:05.000"), p.Kind, where, p.Since.Local().Format("15:04:05.000"))
			}
			return nil
		})
		if ctx.Err() == nil {
			r.t.Logf("%s  the engine's watch ended: %v", time.Now().Format("15:04:05.000"), err)
		}
	}()
	return func() { cancel(); <-done }
}

// since is how long after from at was, in milliseconds, or "-" if at never
// came.
func since(at, from time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%d ms", at.Sub(from).Milliseconds())
}

// lan shows the LAN named name on the canvas, from the sidebar.
func (r *realRig) lan(name string) model.LANID {
	r.t.Helper()
	for _, l := range r.a.snap.LANs {
		if l.Name == name {
			if r.a.lan != l.ID {
				r.click(name)
				r.until(name+" on the canvas", func() bool { return r.a.lan == l.ID })
			}
			return l.ID
		}
	}
	r.t.Fatalf("step %s: no LAN %q among %d", r.step, name, len(r.a.snap.LANs))
	return ""
}

// named is the id of the device named name, as the snapshot has it.
func (r *realRig) named(name string) string {
	for id, d := range r.a.snap.Devices {
		if d.Name == name {
			return string(id)
		}
	}
	return ""
}

func (r *realRig) at(d *device) ui.Point {
	return ui.Point{X: r.a.origin.X + d.live.X, Y: r.a.origin.Y + d.live.Y}
}

// fromTray drags the device of id out of the tray onto the canvas at to.
func (r *realRig) fromTray(id string, to ui.Point) {
	r.t.Helper()
	i := slices.IndexFunc(r.a.tray, func(d *device) bool { return d.id == id })
	home, ok := r.a.slots[id]
	if i < 0 || !ok {
		r.t.Fatalf("step %s: %s is not in the tray", r.step, id)
	}
	r.drag(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, to)
}

// card opens the card of the device of id.
func (r *realRig) card(id string) {
	r.t.Helper()
	d := r.a.device(id)
	if d == nil {
		r.t.Fatalf("step %s: %s is not on the canvas", r.step, id)
	}
	if r.a.selected != id {
		p := r.at(d)
		r.tt.ClickAt(p.X, p.Y)
	}
	r.until("its card", func() bool { return r.a.selected == id && r.a.cardAt.W > 0 })
	r.hold(500 * time.Millisecond)
}

// toggleService flips the switch of the service named name in the card.
func (r *realRig) toggleService(name string) {
	r.t.Helper()
	box, ok := r.tt.Find(name)
	if !ok {
		r.t.Fatalf("step %s: no %q in the card; it shows %q", r.step, name, r.tt.Texts())
	}
	// The switch sits at the row's right end: the card's padding and the
	// list's, then half the switch.
	x := r.a.origin.X + r.a.cardAt.X + r.a.cardAt.W - 16 - 12 - 19
	r.tt.ClickAt(x, box.Y+box.H/2)
	r.tick()
}

func linkedPath(d *device) bool { return d != nil && (d.path == "direct" || d.path == "relay") }

// Step 2: make 工作室, this computer in it; 构建服务器 waits in its tray;
// dragged in, its link goes from 正在连接… to direct or the relay.
func real2(r *realRig) {
	r.record()
	if !slices.ContainsFunc(r.a.snap.LANs, func(l *model.LAN) bool { return l.Name == "工作室" }) {
		r.click("新建局域网")
		r.hold(300 * time.Millisecond)
		r.tt.Type("工作室")
		r.tick()
		r.logf("made 工作室")
		r.tt.Key(0, ui.KeyEnter)
		r.until("工作室 on the canvas", func() bool { l := r.a.snap.LAN(r.a.lan); return l != nil && l.Name == "工作室" })
	}
	r.lan("工作室")
	r.until("this computer in 工作室", func() bool { return r.a.device(r.a.self) != nil })
	build := r.named("构建服务器")
	if build == "" {
		r.t.Fatal("step 2: no 构建服务器 among my devices")
	}
	if r.a.device(build) == nil {
		r.hold(800 * time.Millisecond)
		r.logf("dragging 构建服务器 in")
		r.fromTray(build, ui.Point{X: r.a.origin.X + 620, Y: r.a.origin.Y + 180})
	}
	seen := map[string]bool{}
	r.until("构建服务器 connected", func() bool {
		d := r.a.device(build)
		if d != nil {
			seen[d.path] = true
		}
		return linkedPath(d)
	})
	r.logf("构建服务器 shows %s; on the way %v", r.a.device(build).path, seen)
	r.hold(2500 * time.Millisecond)
	r.still("connected")
}

// Step 3: 阿杰's invite opens the card "阿杰邀请你加入「周末联机」"; 加入 adds
// the sidebar's row, and his two devices come onto the canvas.
func real3(r *realRig) {
	link := os.Getenv("DENEB_REAL_INVITE")
	if link == "" {
		r.t.Fatal("step 3: set DENEB_REAL_INVITE to the link 阿杰 made")
	}
	r.record()
	r.logf("opening the invite")
	r.a.post(func(c *ui.Context) { r.a.openLink(c, link) })
	r.until("the invite card", func() bool { return r.tt.HasText("阿杰邀请你加入「周末联机」") })
	r.hold(800 * time.Millisecond)
	r.logf("joining")
	r.click("加入")
	pc, nas := "", ""
	r.until("阿杰's devices on the canvas", func() bool {
		pc, nas = r.named("阿杰的台式机"), r.named("阿杰的 NAS")
		l := r.a.snap.LAN(r.a.lan)
		return l != nil && l.Name == "周末联机" && pc != "" && nas != "" && r.a.device(pc) != nil && r.a.device(nas) != nil
	})
	r.logf("in 周末联机")
	r.until("both connected", func() bool { return linkedPath(r.a.device(pc)) && linkedPath(r.a.device(nas)) })
	r.logf("阿杰的台式机 %s, 阿杰的 NAS %s", r.a.device(pc).path, r.a.device(nas).path)
	r.hold(2500 * time.Millisecond)
	r.still("joined")
}

// Step 4: switching between the two LANs ten times touches no connection:
// no device on either canvas shows anything but connected, and both
// headers say 都连上了. The moments go to the log, against the engine's
// timing of the long-lived connections.
func real4(r *realRig) {
	for i := 0; i < 10; i++ {
		for _, name := range []string{"工作室", "周末联机"} {
			r.lan(name)
			for _, d := range r.a.devices {
				if d.id != r.a.self && !linkedPath(d) {
					r.t.Errorf("step 4: after switching to %s, %s shows %s", name, d.name, d.path)
				}
			}
			if _, line := r.a.summary(&lightPalette); !strings.Contains(line, "都连上了") {
				r.t.Errorf("step 4: %s says %q", name, line)
			}
			r.hold(time.Second)
		}
	}
}

// Step 5: 阿杰 takes his NAS out of 周末联机 on his desktop, by
// DENEB_REAL5_CMD: the canvas starts its leaving no later than 2 s after
// the command comes back, and plays it out. DENEB_REAL5_UNDO brings it
// back: it pops in again and connects. 构建服务器, in 工作室, stays
// connected all along, as the engine line times its long-lived connection.
func real5(r *realRig) {
	out, back := os.Getenv("DENEB_REAL5_CMD"), os.Getenv("DENEB_REAL5_UNDO")
	if out == "" || back == "" {
		r.t.Fatal("step 5: set DENEB_REAL5_CMD to the command that takes 阿杰的 NAS out of 周末联机, and DENEB_REAL5_UNDO to the one that brings it back")
	}
	lan := r.lan("周末联机")
	nas, build := r.named("阿杰的 NAS"), r.named("构建服务器")
	if nas == "" || build == "" {
		r.t.Fatal("step 5: no 阿杰的 NAS or no 构建服务器")
	}
	r.until("阿杰的 NAS connected", func() bool { return linkedPath(r.a.device(nas)) })
	var stored, began time.Time
	was, broke := model.PathKind(""), ""
	r.watch = func() {
		if l := r.a.store.Snapshot().LAN(lan); stored.IsZero() && l != nil && !l.Has(model.DeviceID(nas)) {
			stored = time.Now()
			r.logf("the store has 阿杰的 NAS out of 周末联机")
		}
		if d := r.a.device(nas); began.IsZero() && (d == nil || !d.gone.IsZero()) {
			began = time.Now()
			r.logf("the canvas starts 阿杰的 NAS leaving")
		}
		if k := r.a.snap.Path(model.DeviceID(build)).Kind; k != was {
			was = k
			r.logf("构建服务器 is %s", k)
			if !k.Connected() && broke == "" {
				broke = fmt.Sprintf("%s at %s", k, time.Now().Format("15:04:05.000"))
			}
		}
	}
	defer func() { r.watch = nil }()
	r.record()
	r.hold(800 * time.Millisecond)
	returned := r.shell(out)
	r.until("阿杰的 NAS leaving", func() bool { return !began.IsZero() })
	r.logf("the leaving began %s after the command came back; the store had it out %s after", since(began, returned), since(stored, returned))
	if lag := began.Sub(returned); lag > 2*time.Second {
		r.t.Errorf("step 5: the canvas started 阿杰的 NAS leaving %v after the command came back", lag)
	}
	r.until("阿杰的 NAS off the canvas", func() bool { return r.a.device(nas) == nil })
	r.logf("阿杰的 NAS off the canvas")
	r.hold(2 * time.Second)
	r.still("left")
	r.record()
	r.shell(back)
	r.until("阿杰的 NAS back on the canvas", func() bool { d := r.a.device(nas); return d != nil && d.gone.IsZero() })
	r.logf("阿杰的 NAS back on the canvas, %s", r.a.device(nas).path)
	r.until("阿杰的 NAS connected again", func() bool { return linkedPath(r.a.device(nas)) })
	r.logf("阿杰的 NAS connected again, %s", r.a.device(nas).path)
	r.hold(2500 * time.Millisecond)
	r.still("back")
	if broke != "" {
		r.t.Errorf("step 5: 构建服务器 was %s", broke)
	}
}

// Step 7: 构建服务器 in 周末联机's tray goes in, back into the tray, and
// back in by the toast's undo, about 10 s apart, as the engine line watches
// 阿杰's NAS reach it.
func real7(r *realRig) {
	r.lan("周末联机")
	build := r.named("构建服务器")
	if r.a.device(build) != nil {
		r.t.Fatal("step 7: 构建服务器 is in 周末联机 already: take it out first")
	}
	r.record()
	r.hold(800 * time.Millisecond)
	r.logf("dragging 构建服务器 in")
	r.fromTray(build, ui.Point{X: r.a.origin.X + 640, Y: r.a.origin.Y + 470})
	r.until("构建服务器 in, connected", func() bool { return linkedPath(r.a.device(build)) })
	r.logf("构建服务器 in, %s", r.a.device(build).path)
	r.hold(10 * time.Second)
	r.logf("dragging 构建服务器 into the tray")
	r.toTray(build)
	r.until("构建服务器 in the tray", func() bool { return r.a.device(build) == nil })
	r.logf("构建服务器 out")
	// The pointer rests on the toast, which keeps it, on its way to undo.
	if box, ok := r.tt.Find("撤销"); ok {
		r.tt.Move(box.X+box.W/2, box.Y+box.H/2)
	}
	r.hold(8 * time.Second)
	r.logf("undo")
	r.click("撤销")
	r.until("构建服务器 back, connected", func() bool { return linkedPath(r.a.device(build)) })
	r.logf("构建服务器 back, %s", r.a.device(build).path)
	r.hold(10 * time.Second)
	r.still("undone")
}

// Step 8: 构建服务器's 网站 is opened to 工作室 only, then to 周末联机 too,
// from its card in each.
func real8(r *realRig) {
	build := r.named("构建服务器")
	r.record()
	for _, name := range []string{"工作室", "周末联机"} {
		lan := r.lan(name)
		if r.a.device(build) == nil {
			r.t.Fatalf("step 8: 构建服务器 is not in %s", name)
		}
		r.card(build)
		on := func() bool {
			for _, s := range r.a.device(build).services {
				if s.name == "网站" {
					return s.on
				}
			}
			r.t.Fatalf("step 8: 构建服务器 has no 网站 in %s", name)
			return false
		}
		if !on() {
			r.logf("opening 网站 to %s", name)
			r.toggleService("网站")
		}
		r.until("网站 on in "+name, func() bool {
			l := r.a.store.Snapshot().LAN(lan)
			return l != nil && slices.ContainsFunc(l.Services[model.DeviceID(build)], func(s model.Service) bool { return s.Name == "网站" && s.On })
		})
		r.logf("网站 on in %s", name)
		r.still("open-to-" + name)
		r.record()
		r.hold(10 * time.Second)
	}
}

// Step 9: refusing 阿杰 on this computer cuts his links to it and says so
// on his devices; a second click lets him back, about 10 s apart.
func real9(r *realRig) {
	r.lan("周末联机")
	r.record()
	r.card(r.a.self)
	refused := func() bool {
		me := r.a.store.Snapshot().Devices[model.DeviceID(r.a.self)]
		for who, on := range me.Refused {
			if on && r.a.people[string(who)] != nil && r.a.people[string(who)].name == "阿杰" {
				return true
			}
		}
		return false
	}
	r.logf("refusing 阿杰")
	r.click("阿杰")
	r.until("阿杰 refused", refused)
	r.hold(time.Second)
	for _, d := range r.a.devices {
		if d.owner != r.a.me && r.a.status(d) != "用不了这台电脑" {
			r.t.Errorf("step 9: %s says %q", d.name, r.a.status(d))
		}
	}
	r.logf("阿杰 refused")
	r.still("refused")
	r.record()
	r.hold(9 * time.Second)
	r.logf("letting 阿杰 back")
	r.click("阿杰")
	r.until("阿杰 back", func() bool { return !refused() })
	r.logf("阿杰 back")
	r.hold(10 * time.Second)
	r.still("let-back")
}

// Step 10: the engine drops every UDP packet between this computer and
// 构建服务器 (denebd debug block build), and lets them through again 15 s
// on. Its link in 工作室 goes over the relay and comes back direct, each
// change in the log at its moment, as are the engine's raw reports; in no
// frame does the window show it anything but connected, nor change it more
// often than that.
func real10(r *realRig) {
	r.lan("工作室")
	build := r.named("构建服务器")
	if r.a.device(build) == nil {
		r.t.Fatal("step 10: 构建服务器 is not in 工作室")
	}
	path := func() string {
		if d := r.a.device(build); d != nil {
			return d.path
		}
		return "off the canvas"
	}
	r.until("构建服务器 direct", func() bool { return path() == "direct" })
	stop := r.engineSays(build)
	defer stop()
	var went []string
	bad := ""
	r.watch = func() {
		d := r.a.device(build)
		if p := path(); len(went) == 0 || went[len(went)-1] != p {
			went = append(went, p)
			status := ""
			if d != nil {
				status = r.a.status(d)
			}
			r.logf("the window shows 构建服务器 %s: %s", p, status)
		}
		if bad != "" {
			return
		}
		_, line := r.a.summary(&lightPalette)
		switch {
		case !linkedPath(d):
			bad = "构建服务器 " + path()
		case !strings.Contains(line, "都连上了"):
			bad = fmt.Sprintf("the header %q", line)
		case r.tt.HasText("断开") || r.tt.HasText("正在连接") || r.tt.HasText("连不上"):
			bad = fmt.Sprintf("%q", r.tt.Texts())
		}
		if bad != "" {
			bad += " at " + time.Now().Format("15:04:05.000")
		}
	}
	defer func() { r.watch = nil }()
	r.record()
	r.hold(2 * time.Second)
	blocked := r.shell("denebd debug block build")
	r.hold(time.Until(blocked.Add(15 * time.Second)))
	r.shell("denebd debug unblock build")
	if len(went) > 1 {
		r.until("构建服务器 direct again", func() bool { return path() == "direct" })
	}
	r.hold(5 * time.Second)
	r.still("direct-again")
	r.watch = nil
	r.logf("the window showed 构建服务器 %s", strings.Join(went, ", "))
	if !slices.Equal(went, []string{"direct", "relay", "direct"}) {
		r.t.Errorf("step 10: the window showed 构建服务器 %s; want direct, relay, direct", strings.Join(went, ", "))
	}
	if bad != "" {
		r.t.Errorf("step 10: the window showed %s", bad)
	}
}
