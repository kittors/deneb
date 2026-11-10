package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// allRig opens the demo in the "全部" view (DESIGN §9 scene 3).
type allRig struct {
	t     *testing.T
	a     *app
	tt    *ui.Tester
	woken atomic.Bool
}

func newAllRig(t *testing.T) *allRig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	r := &allRig{t: t, a: a}
	a.setWake(func() { r.woken.Store(true) })
	r.tt = ui.NewTester(a.view, 1180, 740)
	r.tt.Frame()
	r.step(1200)
	if err := r.tt.Click("全部"); err != nil {
		t.Fatal(err)
	}
	r.step(800)
	return r
}

func (r *allRig) step(ms int) {
	for i := 0; i < ms*60/1000; i++ {
		if r.woken.Swap(false) {
			r.tt.Frame()
		}
		r.tt.Advance(time.Second / 60)
	}
}

func (r *allRig) at(id string) ui.Point {
	r.t.Helper()
	d := r.a.device(id)
	if d == nil {
		r.t.Fatalf("%s is not on the canvas", id)
	}
	p := d.at()
	return ui.Point{X: sidebarWidth + p.X, Y: headerHeight + p.Y}
}

func (r *allRig) drag(from, to ui.Point) {
	r.tt.Press(from.X, from.Y)
	r.step(50)
	for i := 1; i <= 30; i++ {
		k := float32(i) / 30
		r.tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		r.step(17)
	}
	r.step(200)
	r.tt.Release(to.X, to.Y)
	r.step(900)
}

func TestAllViewLaysOutRegions(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	if !a.all() {
		t.Fatal("the canvas does not show 全部")
	}
	for _, id := range []string{"mac", "weekend/game", "weekend/mia", "work/studio", "work/lindb"} {
		r.at(id)
	}
	if !r.tt.HasText("在 2 个局域网里") || !r.tt.HasText("2 个局域网，8 台设备都连上了") {
		t.Errorf("the words are %q", r.tt.Texts())
	}
	// The regions keep clear of each other and of this computer.
	me := a.device(a.self)
	for lan, q := range a.regionRects {
		if overlaps(q, a.foot(me, me.at())) {
			t.Errorf("%s's region %v is under this computer", lan, q)
		}
		for other, o := range a.regionRects {
			if other != lan && overlaps(q, o) {
				t.Errorf("%s's region %v is under %s's %v", lan, q, other, o)
			}
		}
	}
	// Every device is in its region.
	for _, d := range a.devices {
		if d.id != a.self && a.regionAt(d.at()) != d.lan {
			t.Errorf("%s rests at %v, out of its region %v", d.id, d.at(), a.regionRects[d.lan])
		}
	}
}

func TestAllViewShowsADeviceOncePerLAN(t *testing.T) {
	r := newAllRig(t)
	r.a.send(nil, model.Op{Kind: model.OpLANAddDevice, LAN: "work", Device: "game"})
	r.step(900)
	w, k := r.a.device("weekend/game"), r.a.device("work/game")
	if w == nil || k == nil {
		t.Fatal("the console does not show in both regions")
	}
	if w.twin != 0 || k.twin != 0 || r.a.sameName(w) {
		t.Errorf("one device in two regions is numbered as two of a name: %d, %d", w.twin, k.twin)
	}
	if !r.tt.HasText("2 个局域网，8 台设备都连上了") {
		t.Errorf("a device in two LANs counts twice: %q", r.tt.Texts())
	}
}

func TestAllViewMoveAndUndo(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	rest := a.device("weekend/game").at()
	to := r.at("work/lindb")
	to.X, to.Y = to.X-108, to.Y+196
	r.drag(r.at("weekend/game"), to)
	if a.snap.LAN("weekend").Has("game") || !a.snap.LAN("work").Has("game") {
		t.Fatal("the console did not move to 工作室")
	}
	d := a.device("work/game")
	if d == nil || d.lan != "work" || a.regionAt(d.at()) != "work" {
		t.Fatalf("the console is not in 工作室's region: %+v", d)
	}
	if !r.tt.HasText("已把我的游戏主机挪到工作室") {
		t.Errorf("the toast says %q", r.tt.Texts())
	}
	if err := r.tt.Click("撤销"); err != nil {
		t.Fatal(err)
	}
	r.step(1200)
	if !a.snap.LAN("weekend").Has("game") || a.snap.LAN("work").Has("game") {
		t.Fatal("undo did not move the console back")
	}
	d = a.device("weekend/game")
	if d == nil || dist(d.at(), rest) > 1 {
		t.Errorf("the console is back at %v, not where it rested, %v", d.at(), rest)
	}
}

func TestAllViewDropOutLeaves(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	from := r.at("weekend/game")
	r.drag(from, ui.Point{X: from.X + 250, Y: from.Y + 200})
	if a.snap.LAN("weekend").Has("game") {
		t.Fatal("let go out of every region, the console stays in 周末联机")
	}
	if !r.tt.HasText("已把我的游戏主机移出周末联机") {
		t.Errorf("the toast says %q", r.tt.Texts())
	}
	if err := r.tt.Click("撤销"); err != nil {
		t.Fatal(err)
	}
	r.step(1200)
	if !a.snap.LAN("weekend").Has("game") || a.device("weekend/game") == nil {
		t.Fatal("undo did not bring the console back")
	}
}

func TestAllViewOthersStayInTheirRegion(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	from := r.at("weekend/jdesk")
	to := ui.Point{X: from.X + 400, Y: from.Y}
	r.tt.Press(from.X, from.Y)
	r.step(50)
	for i := 1; i <= 30; i++ {
		k := float32(i) / 30
		r.tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		r.step(17)
	}
	r.step(200)
	if !r.tt.HasText("这是阿杰的设备，只有阿杰能移出") {
		t.Errorf("at its region's edge, 阿杰's desktop says nothing: %q", r.tt.Texts())
	}
	r.tt.Release(to.X, to.Y)
	r.step(900)
	if !a.snap.LAN("weekend").Has("jdesk") {
		t.Fatal("阿杰's desktop left 周末联机")
	}
	if d := a.device("weekend/jdesk"); a.regionAt(d.at()) != "weekend" {
		t.Errorf("阿杰's desktop rests at %v, out of its region %v", d.at(), a.regionRects["weekend"])
	}
}

func TestPauseIsPerLAN(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	a.send(nil, model.Op{Kind: model.OpLANAddDevice, LAN: "work", Device: "game"})
	r.step(600)
	if err := r.tt.Click("暂停周末联机"); err != nil {
		t.Fatal(err)
	}
	r.tt.Move(700, 700)
	r.step(900)
	if !a.all() {
		t.Fatal("the switch took the canvas off 全部")
	}
	if p := a.device("weekend/game").path; p != "paused" {
		t.Errorf("paused, 周末联机's console is %q", p)
	}
	if p := a.device("work/game").path; p != "direct" {
		t.Errorf("the console in 工作室 is %q, as 周末联机 is paused", p)
	}
	if !r.tt.HasText("周末联机已暂停，其他照常") || !r.tt.HasText("已暂停") {
		t.Errorf("the words are %q", r.tt.Texts())
	}
	if err := r.tt.Click("暂停周末联机"); err != nil {
		t.Fatal(err)
	}
	r.step(900)
	if p := a.device("weekend/game").path; p != "direct" {
		t.Errorf("resumed, 周末联机's console is %q", p)
	}
}

func TestFocusFromTheSidebar(t *testing.T) {
	r := newAllRig(t)
	a := r.a
	b, ok := r.tt.Find("工作室")
	if !ok {
		t.Fatal("no 工作室 row")
	}
	r.tt.Move(b.X+10, b.Y+b.H/2)
	r.step(700)
	if a.focus != "work" || a.device("weekend/mia").faded < 0.99 || a.device("work/studio").faded > 0.01 {
		t.Errorf("hovering 工作室's row: focus %q, Mia's MacBook faded %v, the studio's %v", a.focus, a.device("weekend/mia").faded, a.device("work/studio").faded)
	}
	r.tt.Move(700, 700)
	r.step(700)
	if a.focus != "" || a.device("weekend/mia").faded > 0.01 {
		t.Errorf("off the rows: focus %q, Mia's MacBook faded %v", a.focus, a.device("weekend/mia").faded)
	}
}
