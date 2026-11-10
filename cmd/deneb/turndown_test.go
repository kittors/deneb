package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// waitStore is a step store whose ops come back a version ahead of its
// world, as a server's do: what the view sends stays laid over the world
// until the test sets the world that has it.
type waitStore struct{ stepStore }

func (s *waitStore) Do(context.Context, model.Op) (model.Result, error) {
	return model.Result{Version: s.s.Version + 1}, nil
}

// My device brought into a LAN whose owner lets new devices in waits for
// them from the first frame (DESIGN §6.7), and the toast says so, never
// that it is in. Turned away, the toast says that too, and it goes back to
// the tray as it waited, dashed. (The M2 acceptance in real mode, item 13.)
func TestMyDeviceWaitsAndIsTurnedAway(t *testing.T) {
	s0 := *client.NewDemo().Snapshot()
	books := &model.LAN{ID: "books", Name: "读书会", Owner: "leo", Members: []model.PersonID{"leo", s0.Me},
		Devices: []model.DeviceID{s0.Self}, ApproveDevices: true, Services: map[model.DeviceID][]model.Service{}}
	s0.LANs = append(slices.Clone(s0.LANs), books)
	st := &waitStore{stepStore{fixedStore: fixedStore{&s0}}}
	a := newApp(st, demoPlaces())
	tt := ui.NewTester(a.view, 1180, 740)
	frames := func(d time.Duration) {
		for end := tt.Now().Add(d); tt.Now().Before(end); {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
		}
	}
	tt.Frame()
	frames(800 * time.Millisecond)
	if err := tt.Click("读书会"); err != nil {
		t.Fatal(err)
	}
	frames(800 * time.Millisecond)
	home, ok := a.slots["mynas"]
	if !ok {
		t.Fatalf("我的 NAS is not in 读书会's tray: %q", tt.Texts())
	}
	drag(tt, ui.Point{X: a.origin.X + home.X, Y: a.origin.Y + home.Y}, ui.Point{X: a.origin.X + 520, Y: a.origin.Y + 300})
	tt.Frame()
	if d := a.device("mynas"); d == nil || !d.pending {
		t.Fatal("我的 NAS does not wait for Leo as it lands")
	}
	if !tt.HasText("我的 NAS 等 Leo 同意") || tt.HasText("已把我的 NAS加进局域网") {
		t.Errorf("the toast: %q", tt.Texts())
	}
	frames(time.Second)
	if d := a.device("mynas"); d == nil || !d.pending || a.status(d) != "等 Leo 同意" {
		t.Fatalf("我的 NAS on the canvas: %+v", d)
	}

	// The server has it waiting, and then Leo turns it away.
	s1 := s0
	s1.LANs = slices.Clone(s0.LANs)
	waiting := *books
	waiting.Pending = []model.DeviceID{"mynas"}
	s1.LANs[len(s1.LANs)-1] = &waiting
	s1.Version = s0.Version + 1
	st.set(&s1)
	frames(500 * time.Millisecond)
	s2 := s0
	s2.Version = s0.Version + 2
	st.set(&s2)
	tt.Frame()
	if !tt.HasText("Leo 没有同意我的 NAS 加入「读书会」") {
		t.Errorf("no word that Leo turned it away: %q", tt.Texts())
	}
	d := a.device("mynas")
	if d == nil || !d.turnedDown || a.landing != d {
		t.Fatalf("我的 NAS is not on its way back to the tray, dashed: %+v", d)
	}
	frames(120 * time.Millisecond)
	if d := a.device("mynas"); d != nil && !d.turnedDown {
		t.Error("it went solid on its way back")
	}
	frames(1500 * time.Millisecond)
	if a.device("mynas") != nil || !slices.ContainsFunc(a.tray, func(x *device) bool { return x.id == "mynas" }) {
		t.Error("我的 NAS is not back in the tray")
	}
}
