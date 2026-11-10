package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// menubarScreen is the menu bar's panel as the recordings show it: a strip
// of menu bar at the top with Deneb's mark on it, and the panel under the
// mark while it is open (DESIGN §7.1: only to see where the panel comes
// from; the product's menu bar is the system's).
type menubarScreen struct {
	a    *app
	open bool
	mark ui.Rect // where the mark is, in the window
}

const (
	screenW, screenH = 560, 620
	barHeight        = 26
)

func (m *menubarScreen) view(c *ui.Context) {
	a := m.a
	a.drain(c)
	a.sync(nil)
	p := colors(c)
	desk := []ui.GradientStop{{Color: ui.Hex("#e8ebf1"), At: 0}, {Color: ui.Hex("#dde1e9"), At: 1}}
	bar, ink := white.Alpha(0.72), black.Alpha(0.85)
	if p.dark {
		desk = []ui.GradientStop{{Color: ui.Hex("#1d1e23"), At: 0}, {Color: ui.Hex("#141519"), At: 1}}
		bar, ink = ui.RGBA(30, 30, 34, 0.86), white.Alpha(0.9)
	}
	root := ui.Box(c).Fill().LinearGradient(ui.LinearGradient{Angle: 180, Stops: desk})
	root.Children(func() {
		ui.Row(c).Absolute().Left(0).Top(0).Right(0).Height(barHeight).Background(bar).Padding(0, 14).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			mark := ui.ButtonBase(c.Key("mark")).Size(30, 22).Radius(5).Center().FocusRing(false)
			if m.open {
				mark.Background(ink.Alpha(0.12))
			}
			mark.Children(func() { trayGlyph(c, trayState(a.snap, c.Now()), ink) })
			if b := mark.Bounds(); b.W > 0 {
				m.mark = b
			}
			if mark.Clicked() {
				m.toggle(c)
			}
			ui.Text(c, "13:05").FontSize(13).FontWeight(500).TextColor(ink).FontFeatures("tnum")
		})
		// The desktop under the panel: a click on it puts the panel away.
		if m.open && ui.Box(c.Key("desk")).Absolute().Left(0).Top(barHeight).Right(0).Bottom(0).Clicked() {
			m.toggle(c)
		}
		x := min(max(m.mark.X+m.mark.W/2-panelWidth/2, 12), screenW-12-panelWidth)
		holder := ui.Column(c.Key("panel-holder")).Absolute().Left(x).Top(barHeight + 6).PassThrough()
		holder.Children(func() {
			if !m.open {
				return
			}
			wrap := ui.Column(c.Key("panel-wrap")).TransformOrigin(0.5, 0)
			wrap.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Scale: 0.96, Y: -6}, Exit: &ui.Motion{Scale: 0.97, Y: -4}})
			// The system's shadow, as the flyout has it, on the panel itself:
			// it is as tall as the panel in every frame.
			wrap.Children(func() {
				a.panelBody(c, p).Shadow(0, 12, 32, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.06))
			})
		})
	})
}

func (m *menubarScreen) toggle(c *ui.Context) {
	if m.open {
		m.a.flushPanel(c)
	}
	m.open = !m.open
	m.a.panelShown = m.open
}

// The panel lists what waits for me, and its mark tells it: a dot while
// anything does, an outline while nothing connects here.
func TestPanelChores(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	now := time.Now()
	if st := trayState(a.store.Snapshot(), now); st != "solid" {
		t.Errorf("the sample's mark is %q", st)
	}
	for _, ev := range []string{"pending-device", "pending-member", "login-due", "invitation", "unreachable"} {
		d.Simulate(ev)
	}
	s := a.store.Snapshot()
	var got []string
	for _, ch := range choresOf(s, now) {
		got = append(got, ch.text+" · "+ch.button)
	}
	want := []string{"阿杰的树莓派想加入周末联机 · 同意", "小林想加入周末联机 · 同意", "我的游戏主机需要重新登录 · 续上",
		"Leo 邀请你加入桌游夜 · 看看", "Mia 的 MacBook 连不上 · 看看"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("chores:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if st := trayState(s, now); st != "dot" {
		t.Errorf("with chores the mark is %q", st)
	}
	d.Simulate("engine")
	if st := trayState(a.store.Snapshot(), now); st != "hollow" {
		t.Errorf("without the engine the mark is %q", st)
	}
}

// Letting a device in from the panel: its row says so, with undo, and the
// device is let in as the row folds; undo puts the row back unsent.
func TestPanelApproveAndUndo(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	d.Simulate("pending-device")
	d.Simulate("pending-member")
	m := &menubarScreen{a: a, open: true}
	tt := ui.NewTester(m.view, screenW, screenH)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	b, ok := tt.Find("同意")
	if !ok {
		t.Fatalf("no 同意: %q", tt.Texts())
	}
	tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
	if !tt.HasText("已同意阿杰的树莓派") {
		t.Fatalf("the row does not say it: %q", tt.Texts())
	}
	if l := a.store.Snapshot().LAN("weekend"); !l.HasPending("jpi") {
		t.Fatal("letting it in went before the row folded")
	}
	for i := 0; i < 270; i++ {
		tt.Advance(time.Second / 60)
		tt.Frame()
	}
	if l := a.store.Snapshot().LAN("weekend"); l.HasPending("jpi") || !l.Has("jpi") {
		t.Fatal("the Pi was not let in as the row folded")
	}
	// 小林 now: undo keeps him waiting.
	b, _ = tt.Find("同意")
	tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
	if err := tt.Click("撤销"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		tt.Advance(time.Second / 60)
		tt.Frame()
	}
	if l := a.store.Snapshot().LAN("weekend"); len(l.PendingMembers) != 1 || !tt.HasText("小林想加入周末联机") {
		t.Errorf("undo did not keep 小林 waiting: %v; %q", l.PendingMembers, tt.Texts())
	}
}

// TestM2MenuBarFrames records the menu bar's panel in the demo, both
// themes, as TestM2Frames does M2's other scenes (DESIGN §7.1, §11).
func TestM2MenuBarFrames(t *testing.T) {
	root := os.Getenv("DENEB_M2")
	if root == "" {
		t.Skip("set DENEB_M2 to a folder to record M2's demo")
	}
	type rig struct {
		a    *app
		d    *client.Demo
		m    *menubarScreen
		tt   *ui.Tester
		rec  func(ms int)
		move func(to ui.Point, ms int)
		shot func(state string)
	}
	at := func(r *rig, text string) ui.Point {
		t.Helper()
		b, ok := r.tt.Find(text)
		if !ok {
			t.Fatalf("no %q: %q", text, r.tt.Texts())
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
	click := func(r *rig, p ui.Point) {
		r.move(p, 350)
		r.rec(120)
		r.tt.ClickAt(p.X, p.Y)
	}
	mark := func(r *rig) ui.Point { return ui.Point{X: r.m.mark.X + r.m.mark.W/2, Y: r.m.mark.Y + r.m.mark.H/2} }
	desk := ui.Point{X: 80, Y: screenH - 60}
	scenes := []struct {
		name string
		play func(r *rig)
	}{
		// A click on the mark opens the panel; a click elsewhere puts it away.
		{"menubar-open", func(r *rig) {
			r.rec(500)
			click(r, mark(r))
			r.rec(700)
			r.shot("面板")
			r.move(at(r, "工作室"), 400)
			r.rec(500)
			click(r, desk)
			r.rec(700)
		}},
		// What waits for me: the mark's dot, the rows, and 同意 with undo.
		{"menubar-approve", func(r *rig) {
			r.rec(300)
			r.d.Simulate("pending-device")
			r.d.Simulate("pending-member")
			r.d.Simulate("unreachable")
			r.rec(700)
			click(r, mark(r))
			r.rec(700)
			r.shot("要你处理")
			click(r, at(r, "同意"))
			r.rec(900)
			r.shot("已同意")
			r.rec(3600)
			click(r, at(r, "同意"))
			r.rec(800)
			click(r, at(r, "撤销"))
			r.rec(1200)
		}},
		// My console's login runs out soon: 续上, and the dot goes.
		{"menubar-renew", func(r *rig) {
			r.rec(300)
			r.d.Simulate("login-due")
			r.rec(600)
			click(r, mark(r))
			r.rec(700)
			r.shot("需要重新登录")
			click(r, at(r, "续上"))
			r.rec(3200)
			r.shot("续上了")
		}},
		// The switches: one LAN paused, then both, and the mark goes hollow.
		{"menubar-pause", func(r *rig) {
			r.rec(300)
			click(r, mark(r))
			r.rec(700)
			click(r, at(r, "暂停周末联机"))
			r.rec(900)
			r.shot("暂停一个")
			click(r, at(r, "暂停工作室"))
			r.rec(900)
			r.shot("都暂停了")
			click(r, at(r, "暂停周末联机"))
			r.rec(1000)
		}},
		// This computer's Deneb stops: the panel says so, and the mark goes
		// hollow; it comes back.
		{"menubar-engine", func(r *rig) {
			r.rec(300)
			click(r, mark(r))
			r.rec(700)
			r.d.Simulate("engine")
			r.rec(1000)
			r.shot("引擎停了")
			r.d.Simulate("engine")
			r.rec(1000)
		}},
	}
	if only := os.Getenv("DENEB_M2_ONLY"); only != "" {
		keep := map[string]bool{}
		for _, n := range strings.Split(only, ",") {
			keep[n] = true
		}
		var some = scenes[:0:0]
		for _, sc := range scenes {
			if keep[sc.name] {
				some = append(some, sc)
			}
		}
		scenes = some
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			dir := filepath.Join(root, theme+"-"+sc.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			a, d := newDemoApp()
			d.Manual()
			var woken atomic.Bool
			a.setWake(func() { woken.Store(true) })
			m := &menubarScreen{a: a}
			tt := ui.NewTester(m.view, screenW, screenH)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(600 * time.Millisecond)
			tt.Frame()
			n := 0
			r := &rig{a: a, d: d, m: m, tt: tt}
			pointer := desk
			r.rec = func(ms int) {
				for end := n + ms*60/1000; n < end; n++ {
					if woken.Swap(false) {
						tt.Frame()
					}
					tt.Advance(time.Second / 60)
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.jpg", n)))
					if err != nil {
						t.Fatal(err)
					}
					jpeg.Encode(f, tt.Image(), &jpeg.Options{Quality: 88})
					f.Close()
				}
			}
			r.move = func(to ui.Point, ms int) {
				from := pointer
				steps := max(1, ms*60/1000)
				for i := 1; i <= steps; i++ {
					k := ui.EaseInOut(float32(i) / float32(steps))
					tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
					r.rec(17)
				}
				pointer = to
			}
			r.shot = func(state string) {
				tt.SetScale(2)
				tt.Frame()
				if f, err := os.Create(filepath.Join(root, theme+"-"+sc.name+"_"+state+".png")); err == nil {
					png.Encode(f, tt.Image())
					f.Close()
				}
				tt.SetScale(1)
				tt.Frame()
			}
			sc.play(r)
		}
	}
}

// What the panel says while there is little to say: this computer's Deneb
// not running, no one logged in on it yet, the world on its way. Only the
// way into the window is left; the mark is an outline while nothing can
// connect here.
func TestPanelBeforeTheWorld(t *testing.T) {
	sample := client.NewDemo().Snapshot()
	for _, tc := range []struct {
		name, line, mark string
		snap             model.Snapshot
	}{
		{"engine", "Deneb 没在运行", "hollow", func() model.Snapshot { s := *sample; s.Engine = false; return s }()},
		{"logged out", "还没登录", "hollow", model.Snapshot{Engine: true}},
		{"loading", "正在连接…", "solid", model.Snapshot{Engine: true, LoggedIn: true}},
	} {
		a := newApp(&fixedStore{&tc.snap}, demoPlaces())
		tt := ui.NewTester(a.panel, panelWidth, panelMaxHeight)
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
		if !tt.HasText(tc.line) || !tt.HasText("打开 Deneb") || tt.HasText("局域网") || tt.HasText("要你处理") {
			t.Errorf("%s: the panel shows %q", tc.name, tt.Texts())
		}
		if st := trayState(&tc.snap, time.Now()); st != tc.mark {
			t.Errorf("%s: the mark is %q, want %q", tc.name, st, tc.mark)
		}
	}
}

// Many LANs and chores: the panel stops at its height, and its middle
// scrolls, this computer and the way into the window staying in sight.
func TestPanelScrolls(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	for _, ev := range []string{"pending-device", "pending-member", "login-due", "invitation"} {
		d.Simulate(ev)
	}
	for i := 1; i <= 6; i++ {
		if _, err := d.Do(context.Background(), model.Op{Kind: model.OpLANCreate, Name: fmt.Sprintf("新局域网 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(a.panel, panelWidth, 900)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	open, ok := tt.Find("打开 Deneb")
	if !ok || open.Y+open.H > panelMaxHeight-8 {
		t.Fatalf("打开 Deneb is at %v, out of the panel's %v: %q", open, panelMaxHeight, tt.Texts())
	}
	if a.panelHeight != 0 {
		t.Errorf("without a flyout the panel asked for %d", a.panelHeight)
	}
	last, _ := tt.Find("新局域网 6")
	if last.Y < panelMaxHeight {
		t.Fatalf("the last LAN fits at %v: nothing to scroll", last)
	}
	tt.Wheel(panelWidth/2, 300, 0, 400)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	after, _ := tt.Find("新局域网 6")
	if after.Y >= last.Y || after.Y+after.H > open.Y {
		t.Errorf("scrolling took the last LAN from %v to %v, 打开 Deneb at %v", last, after, open)
	}
	if head, _ := tt.Find("都连上了"); head.Y > 40 {
		t.Errorf("this computer scrolled away: %v", head)
	}
}

// refusing is the demo, but for pausing, which this computer's Deneb turns
// down, as one too old to pause does.
type refusing struct{ *client.Demo }

func (r refusing) Do(ctx context.Context, op model.Op) (model.Result, error) {
	if op.Kind == model.OpLANPause {
		return model.Result{}, &model.Error{Code: model.ErrInvalid, Message: "这台电脑上的 Deneb 还不能暂停，更新以后再试"}
	}
	return r.Demo.Do(ctx, op)
}

// Something done from the panel that does not go through: the panel says
// why, in the store's words, for a moment, and the switch stays as it was.
func TestPanelSaysProblems(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(refusing{d}, demoPlaces())
	m := &menubarScreen{a: a, open: true}
	a.panelShown = true
	tt := ui.NewTester(m.view, screenW, screenH)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	const why = "这台电脑上的 Deneb 还不能暂停，更新以后再试"
	if err := tt.Click("暂停周末联机"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		tt.Advance(time.Second / 60)
		tt.Frame()
	}
	if !tt.HasText(why) {
		t.Fatalf("the panel does not say why: %q", tt.Texts())
	}
	if b, _ := tt.Find(why); b.X+b.W > float32(screenW) {
		t.Errorf("the note runs out of the panel: %v", b)
	}
	if a.snap.LAN("weekend").Paused {
		t.Error("周末联机 shows paused")
	}
	for i := 0; i < 300; i++ {
		tt.Advance(time.Second / 60)
		tt.Frame()
	}
	if tt.HasText(why) {
		t.Error("the note stays")
	}
}

// The panel on this computer's store (DESIGN §7.1, on real data): an M2
// world behind a fake engine, as TestLiveM2View has it. What waits for me
// shows with its button, and the button's op reaches the server, held
// until its row folds as in the window; the switch pauses the LAN on the
// engine, as `deneb lan pause` does; 打开 Deneb brings the window. On an M1
// server only what it does shows, and while the engine does not answer the
// panel says so, as the demo does.
func TestLivePanel(t *testing.T) {
	dir, err := os.MkdirTemp("", "dnb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")
	e := fake.New()
	weekend := apitype.ScopeID(proto.LANScope("weekend"))
	e.Update(func(s *apitype.State) {
		s.LoggedIn, s.User, s.SelfDeviceID, s.ControlConnected = true, "me", "mac", true
		s.Scopes, s.Peers, s.Paths = []apitype.Scope{{ID: weekend, Name: "周末联机"}}, nil, nil
	})
	now := time.Now()
	view := proto.UserView{Version: 3, Me: "me", Features: proto.M2Features,
		People: []proto.Person{{ID: "me", Name: "kittors"}, {ID: "jie", Name: "jie", DisplayName: "阿杰"}, {ID: "lin", Name: "lin", DisplayName: "小林"}},
		LANs: []proto.LANView{{ID: "weekend", Name: "周末联机", Owner: "me", ApproveDevices: true, ApproveMembers: true,
			Members: []proto.Member{{Person: "me", State: proto.MemberJoined}, {Person: "jie", State: proto.MemberJoined}, {Person: "lin", State: proto.MemberPending}},
			Devices: []proto.LANDevice{{Device: "mac"}, {Device: "jpi", Pending: true}}}},
		Devices: []proto.DeviceView{
			{ID: "mac", Owner: "me", Name: "mac", DisplayName: "kittors 的 MacBook", Login: proto.LoginInfo{ExpiresAt: now.Add(48 * time.Hour), RenewFrom: now.Add(-time.Hour)}},
			{ID: "jpi", Owner: "jie", Name: "pi", DisplayName: "阿杰的树莓派"}},
	}
	var mu sync.Mutex
	var ops []string
	publish := func() {
		raw, _ := json.Marshal(view)
		e.SetUserView(raw, view.Version)
	}
	publish()
	e.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		var args proto.OpArgs
		json.Unmarshal(op.Args, &args)
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, strings.Join(strings.Fields(op.Op+" "+string(args.LAN)+" "+string(args.Device)+" "+string(args.Person)), " "))
		// The server takes it: the next view has it.
		switch proto.OpType(op.Op) {
		case proto.OpDeviceApprove:
			view.LANs[0].Devices[1].Pending = false
		case proto.OpMemberApprove:
			view.LANs[0].Members[2].State = proto.MemberJoined
		case proto.OpDeviceRenew:
			view.Devices[0].Login = proto.LoginInfo{ExpiresAt: now.Add(90 * 24 * time.Hour), RenewFrom: now.Add(80 * 24 * time.Hour)}
		}
		view.Version++
		publish()
		return &apitype.OpResult{Version: view.Version}, nil
	})
	sent := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(ops) }
	stop, err := e.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)

	a := newApp(l, &places{at: map[model.LANID]map[string]ui.Point{}})
	var shown atomic.Int32
	a.showMain = func() { shown.Add(1) }
	m := &menubarScreen{a: a, open: true}
	a.panelShown = true
	tt := ui.NewTester(m.view, screenW, screenH)
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for tt.Frame(); !ok(); tt.Frame() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; the panel shows %q, sent %q", what, tt.Texts(), sent())
			}
			time.Sleep(5 * time.Millisecond)
			tt.Advance(16 * time.Millisecond)
		}
	}
	settle := func() {
		for i := 0; i < 30; i++ {
			tt.Advance(time.Second / 60)
			tt.Frame()
		}
	}
	click := func(text string) {
		t.Helper()
		settle()
		b, ok := tt.Find(text)
		if !ok {
			t.Fatalf("no %q: %q", text, tt.Texts())
		}
		tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
	}
	until("what waits for me", func() bool { return tt.HasText("阿杰的树莓派想加入周末联机") })
	for _, want := range []string{"小林想加入周末联机", "kittors 的 MacBook 需要重新登录", "周末联机", "1 台都连上了", "打开 Deneb"} {
		if !tt.HasText(want) {
			t.Errorf("the panel does not show %q: %q", want, tt.Texts())
		}
	}
	if st := trayState(a.snap, time.Now()); st != "dot" {
		t.Errorf("with chores the mark is %q", st)
	}

	// 同意: said in the row at once, sent as it folds.
	click("同意")
	if !tt.HasText("已同意阿杰的树莓派") || len(sent()) > 0 {
		t.Fatalf("the row says %q; sent %q before it folded", tt.Texts(), sent())
	}
	until("letting the Pi in", func() bool { return slices.Contains(sent(), "device.approve weekend jpi") })
	until("the Pi in", func() bool { return a.store.Snapshot().LAN("weekend").Has("jpi") })

	// 续上 goes at once.
	click("续上")
	until("renewing this Mac", func() bool { return slices.Contains(sent(), "device.renew mac") })
	until("the login renewed", func() bool { return !tt.HasText("kittors 的 MacBook 需要重新登录") && !tt.HasText("已续上") })

	// 同意 for 小林, and the panel put away before the row folds: it goes now.
	click("同意")
	if slices.Contains(sent(), "member.approve weekend lin") {
		t.Fatal("letting 小林 in went before the row folded")
	}
	m.toggle(nil)
	until("letting 小林 in", func() bool { return slices.Contains(sent(), "member.approve weekend lin") })
	m.toggle(nil)
	until("nothing waiting", func() bool { return !tt.HasText("要你处理") })
	if st := trayState(a.snap, time.Now()); st != "solid" {
		t.Errorf("with nothing waiting the mark is %q", st)
	}

	// The switch pauses 周末联机 on this computer's engine, and resumes it.
	click("暂停周末联机")
	until("周末联机 paused", func() bool { return tt.HasText("已暂停") && e.Snapshot().Scopes[0].Paused })
	if st := trayState(a.snap, time.Now()); st != "hollow" {
		t.Errorf("with every LAN paused the mark is %q", st)
	}
	click("暂停周末联机")
	until("周末联机 back", func() bool { return !tt.HasText("已暂停") && !e.Snapshot().Scopes[0].Paused })

	click("打开 Deneb")
	if shown.Load() != 1 {
		t.Errorf("打开 Deneb brought the window %d times", shown.Load())
	}

	// An M1 server: no letting in or renewing from the panel; the switch
	// stays, the engine doing it.
	mu.Lock()
	view.Features = nil
	view.LANs[0].Devices = append(view.LANs[0].Devices, proto.LANDevice{Device: "jpc", Pending: true})
	view.Devices = append(view.Devices, proto.DeviceView{ID: "jpc", Owner: "jie", Name: "pc", DisplayName: "阿杰的台式机"})
	view.Version++
	publish()
	mu.Unlock()
	until("the M1 view", func() bool {
		return len(a.store.Snapshot().Features) == 0 && a.store.Snapshot().LAN("weekend").HasPending("jpc")
	})
	tt.Advance(time.Second)
	tt.Frame()
	if tt.HasText("同意") || tt.HasText("要你处理") || !tt.HasText("周末联机") {
		t.Errorf("on an M1 server the panel shows %q", tt.Texts())
	}
	if _, ok := tt.Find("暂停周末联机"); !ok {
		t.Error("on an M1 server the switch is gone")
	}

	// The engine stops answering: the panel says so, the mark is an outline.
	stop()
	until("the engine gone", func() bool { return tt.HasText("Deneb 没在运行") && !tt.HasText("局域网") })
	if st := trayState(a.snap, time.Now()); st != "hollow" {
		t.Errorf("without the engine the mark is %q", st)
	}
	stop, err = e.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	until("the engine back", func() bool { return !tt.HasText("Deneb 没在运行") && tt.HasText("周末联机") })
}

// The flyout follows the panel's height, whatever its own is now: one too
// short for chores that came while it was open grows to them, one too
// tall shrinks, never past panelMaxHeight.
func TestPanelAsksItsHeight(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	h := 200
	var tt *ui.Tester
	a.resizePanel = func(n int) { h = n; tt.SetSize(panelWidth, n) }
	tt = ui.NewTester(a.panel, panelWidth, h)
	settle := func() {
		for i := 0; i < 40; i++ {
			tt.Frame()
			tt.Advance(time.Second / 60)
		}
	}
	settle()
	plain := h
	if plain <= 200 || plain >= panelMaxHeight {
		t.Fatalf("the sample's panel asked for %d", plain)
	}
	h = 500
	tt.SetSize(panelWidth, h)
	a.panelHeight = h
	settle()
	if h != plain {
		t.Errorf("a flyout 500 tall shrank to %d, not %d", h, plain)
	}
	d.Simulate("pending-device")
	d.Simulate("login-due")
	settle()
	if h <= plain+80 {
		t.Errorf("with two chores the panel asked for %d, without %d", h, plain)
	}
	if open, ok := tt.Find("打开 Deneb"); !ok || open.Y+open.H > float32(h) {
		t.Errorf("打开 Deneb at %v, in a flyout %d tall", open, h)
	}
	for i := 1; i <= 8; i++ {
		d.Do(context.Background(), model.Op{Kind: model.OpLANCreate, Name: fmt.Sprintf("新局域网 %d", i)})
	}
	settle()
	if h != panelMaxHeight {
		t.Errorf("with many LANs the panel asked for %d", h)
	}
}

// The panel's head says how things are, in the card's words: 都连上了 only
// when every device is; a device off says 不在线; one that cannot be reached
// says 连不上, which goes first when both are so (as the card puts it).
func TestPanelSaysWhatIsNotConnected(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.panel, panelWidth, panelMaxHeight)
	settle := func() {
		for i := 0; i < 30; i++ {
			tt.Advance(time.Second / 60)
			tt.Frame()
		}
	}
	head := func(want string, dot ui.Color) {
		t.Helper()
		settle()
		if c, line := a.panelSummary(&lightPalette); line != want || c != dot || !tt.HasText(want) {
			t.Errorf("the head says %q in %v, want %q in %v", line, c, want, dot)
		}
	}
	head("都连上了", lightPalette.ok)
	d.Simulate("offline")
	head("1 台不在线", lightPalette.ink3)
	d.Simulate("unreachable")
	head("1 台连不上", ui.Hex("#F59E0B"))
}
