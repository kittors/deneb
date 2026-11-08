package main

import (
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// The M1 recordings in real mode, looked through frame by frame (DESIGN
// §11), found these; each test holds one fixed.

// stepStore is a store whose world changes only when the test sets it.
type stepStore struct {
	fixedStore
	subs []func(*model.Snapshot)
}

func (s *stepStore) Subscribe(fn func(*model.Snapshot)) (cancel func()) {
	s.subs = append(s.subs, fn)
	return func() {}
}

func (s *stepStore) set(n *model.Snapshot) {
	s.s = n
	for _, fn := range s.subs {
		fn(n)
	}
}

// In a LAN made on another of my devices, this computer waits in the tray,
// and the engine connects to nothing there that it is in no other LAN
// with: such a device says it is online, never 正在连接…, and the header
// says why there is no link.
func TestApartDevicesSayOnline(t *testing.T) {
	s := *client.NewDemo().Snapshot()
	s.Devices = maps.Clone(s.Devices)
	box := *s.Devices["mynas"]
	box.ID, box.Name, box.Online = "box", "机房小盒子", true
	s.Devices["box"] = &box
	s.Mine = append(slices.Clone(s.Mine), "box")
	s.LANs = append(slices.Clone(s.LANs), &model.LAN{ID: "lab", Name: "实验室", Owner: s.Me,
		Members: []model.PersonID{s.Me}, Devices: []model.DeviceID{"box"}, Services: map[model.DeviceID][]model.Service{}})
	a := newApp(&fixedStore{&s}, demoPlaces())
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	if err := tt.Click("实验室"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(800 * time.Millisecond)
	d := a.device("box")
	if d == nil {
		t.Fatal("机房小盒子 is not on the canvas")
	}
	if got := a.status(d); got != "在线" {
		t.Errorf("机房小盒子 says %q", got)
	}
	if _, line := a.summary(&lightPalette); !strings.Contains(line, "这台电脑还没加进来") {
		t.Errorf("the header says %q", line)
	}
	if tt.HasText("正在连接") {
		t.Errorf("the window says 正在连接: %q", tt.Texts())
	}
	// Its card says why there is no link, and what brings one.
	p := d.at()
	tt.ClickAt(sidebarWidth+p.X, headerHeight+p.Y)
	tt.Advance(600 * time.Millisecond)
	if !tt.HasText("这台电脑还没加进来") || tt.HasText("直接连上") {
		t.Errorf("机房小盒子's card shows %q", tt.Texts())
	}
}

// The first invite card comes in as every other one does: it slides down
// into place, rather than being there in one frame.
func TestFirstInviteCardComesIn(t *testing.T) {
	a, _ := newDemoApp()
	var woken atomic.Bool
	a.setWake(func() { woken.Store(true) })
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(time.Second)
	a.post(func(c *ui.Context) { a.openLink(c, client.DemoInvite) })
	var ys []float32
	for i := 0; i < 40; i++ {
		if woken.Swap(false) {
			tt.Frame()
		}
		tt.Advance(time.Second / 60)
		if r, ok := tt.Find("这台电脑会跟着加进去"); ok {
			ys = append(ys, r.Y)
		}
	}
	if len(ys) < 2 || ys[0] > ys[len(ys)-1]-3 {
		t.Errorf("the first invite card did not come in: its line went %v", ys)
	}
}

// A toast that comes in while the tray shows rises into its place above
// the tray, and never through it.
func TestToastOverTheTrayKeepsClear(t *testing.T) {
	a, _ := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	rest := a.device("game").at()
	from := ui.Point{X: sidebarWidth + rest.X, Y: headerHeight + rest.Y}
	drag(tt, from, ui.Point{X: from.X + 150, Y: 740 - 40})
	const text = "已把我的游戏主机移出局域网"
	seen := 0
	for i := 0; i < 40; i++ {
		tt.Advance(time.Second / 60)
		r, ok := tt.Find(text)
		if !ok || !a.trayUp {
			continue
		}
		seen++
		bottom := r.Y + r.H/2 + 22 // the toast is 44 high, its words in the middle
		if top := headerHeight + a.trayAt.Y; bottom > top+1 {
			t.Fatalf("frame %d: the toast reaches %v, into the tray from %v", i, bottom, top)
		}
	}
	if seen == 0 {
		t.Fatal("no toast over the tray")
	}
}

// When the last link through the relay goes direct, its old line fades out
// through the relay: the relay stays until the line is nearly gone, and
// shrinks away with its end, so the line never bends at nothing.
func TestRelayStaysWhileItsOldLineFades(t *testing.T) {
	s := *client.NewDemo().Snapshot()
	s.Paths = maps.Clone(s.Paths)
	for id, p := range s.Paths {
		if p.Kind == model.PathRelay {
			s.Paths[id] = model.Path{Kind: model.PathDirect, RTT: 9 * time.Millisecond}
		}
	}
	s.Paths["mia"] = model.Path{Kind: model.PathRelay, Relay: "hk", RTT: 46 * time.Millisecond}
	st := &stepStore{fixedStore: fixedStore{&s}}
	a := newApp(st, demoPlaces())
	var woken atomic.Bool
	a.setWake(func() { woken.Store(true) })
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(time.Second)
	if a.relay == nil || !a.relay.shown {
		t.Fatal("the relay does not show while Mia's link goes through it")
	}
	n := s
	n.Version++
	n.Paths = maps.Clone(s.Paths)
	n.Paths["mia"] = model.Path{Kind: model.PathDirect, RTT: 9 * time.Millisecond}
	st.set(&n)
	step := func(d time.Duration) {
		for end := time.Duration(0); end < d; end += time.Second / 60 {
			if woken.Swap(false) {
				tt.Frame()
			}
			tt.Advance(time.Second / 60)
		}
	}
	step(trackIn - relayFade/2 - 40*time.Millisecond)
	if !a.relay.shown || !a.relay.leftAt.IsZero() {
		t.Errorf("the relay goes while Mia's old line still fades through it (shown %v, leaving since %v)", a.relay.shown, a.relay.leftAt)
	}
	step(relayFade/2 + relayFade + 140*time.Millisecond)
	if a.relay.shown {
		t.Error("the relay stays after the old line has gone")
	}
}
