package main

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// fixedStore is a store whose world never changes: the snapshot it is given.
type fixedStore struct{ s *model.Snapshot }

func (f *fixedStore) Snapshot() *model.Snapshot                       { return f.s }
func (f *fixedStore) Subscribe(func(*model.Snapshot)) (cancel func()) { return func() {} }
func (f *fixedStore) Do(context.Context, model.Op) (model.Result, error) {
	return model.Result{Version: f.s.Version}, nil
}
func (f *fixedStore) Login(context.Context) (string, error) { return "", nil }
func (f *fixedStore) Open(context.Context, model.DeviceID, model.Service) (string, error) {
	return "", nil
}
func (f *fixedStore) Preview(context.Context, string) (*model.Invite, error) { return nil, nil }
func (*fixedStore) Immediate()                                               {}

// Real names run long. One too long for its place ends in "…" there, and
// never runs under what is next to it.
func TestLongNamesEndShort(t *testing.T) {
	s := *client.NewDemo().Snapshot()
	const device = "Mia 的工作用 MacBook Pro 16 英寸（2023 年款，公司配的那台）"
	const lan = "周末一起打游戏、看电影、传照片和备份文件的大家庭局域网"
	devices := map[model.DeviceID]*model.Device{}
	for id, d := range s.Devices {
		devices[id] = d
	}
	mia := *devices["mia"]
	mia.Name = device
	devices["mia"] = &mia
	s.Devices = devices
	lans := make([]*model.LAN, len(s.LANs))
	copy(lans, s.LANs)
	weekend := *lans[0]
	weekend.Name = lan
	lans[0] = &weekend
	s.LANs = lans

	a := newApp(&fixedStore{&s}, demoPlaces())
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	width := func(text string) float32 {
		t.Helper()
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("%q does not show", text)
		}
		return r.W
	}
	if w := width(device); w > nameWidth+0.5 {
		t.Errorf("the device's name is %v wide on the canvas, over %v", w, nameWidth)
	}
	if w := width(lan); w > lanNameWidth+0.5 {
		t.Errorf("the LAN's name is %v wide in the header, over %v", w, lanNameWidth)
	}
	// The LAN's row in the sidebar ends it too, inside the row.
	if r, ok := tt.Find(lan); !ok || r.X+r.W > sidebarWidth {
		t.Errorf("the LAN's name runs out of the sidebar: %v", r)
	}
	m := a.device("mia")
	tt.ClickAt(sidebarWidth+m.at().X, headerHeight+m.at().Y)
	tt.Advance(600 * time.Millisecond)
	if !tt.HasText("可用服务") {
		t.Fatal("Mia's card did not open")
	}
	// The name shows twice now, on the canvas and in the card: each within
	// its own room.
	n := 0
	for _, txt := range tt.Texts() {
		if txt == device {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the name shows %d times, want on the canvas and in the card", n)
	}
	if a.cardAt.W > cardWidth+0.5 {
		t.Errorf("the card is %v wide, over %v", a.cardAt.W, cardWidth)
	}
}

// A name in a sentence of the toast is cut to what reads at a glance.
func TestShort(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"我的游戏主机", "我的游戏主机"},
		{"Mia 的 MacBook", "Mia 的 MacBook"},
		{"一二三四五六七八九十一二三", "一二三四五六七八九十一…"},
		{strings.Repeat("a", 24), strings.Repeat("a", 24)},
		// "…" is as wide as a letter: 24 in all.
		{strings.Repeat("a", 25), strings.Repeat("a", 23) + "…"},
	} {
		got := short(c.in)
		if got != c.want {
			t.Errorf("short(%q) = %q, want %q", c.in, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("short(%q) cut a character in two", c.in)
		}
	}
}
