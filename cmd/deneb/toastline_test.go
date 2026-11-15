package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A toast about no device in particular keeps off the names on the canvas
// (DESIGN §6.5): over the tray it would sit on 阿杰的 NAS's second line,
// "经香港中转", so it shows at the top.
func TestToastKeepsOffNames(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(1200 * time.Millisecond)
	const say = "网卡没更新成功，用回了原来的版本"
	// Over the tray, where a toast shows, it would cover the NAS: what this
	// test is about.
	tw := toastWidth(say, false)
	over := ui.Rect{X: (a.cw-a.bottomRight(a.ch))/2 - tw/2, Y: a.ch - toastBottom(true) - 44, W: tw, H: 44}
	if nas := a.device("jnas"); nas == nil || !overlaps(over, footprint(nas.at())) {
		t.Fatal("the demo moved: over the tray, a toast no longer sits on 阿杰的 NAS")
	}
	a.post(func(c *ui.Context) { a.notify(c, nil, say, nil) })
	for range 50 { // frame by frame: the post is taken in a frame
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if a.toast == nil || !a.toast.top {
		t.Error("the toast is not at the top")
	}
	toast, ok := tt.Find(say)
	if !ok {
		t.Fatalf("no toast: %q", tt.Texts())
	}
	for _, d := range a.devices {
		if !d.gone.IsZero() {
			continue
		}
		// The toast's words and the room of each device: its tile and the
		// two lines under it.
		at := d.at()
		room := footprint(ui.Point{X: a.origin.X + at.X, Y: a.origin.Y + at.Y})
		if overlaps(toast, room) {
			t.Errorf("the toast %v covers %s's name, %v", toast, d.name, room)
		}
	}
	if r, ok := tt.Find("经香港中转"); ok && overlaps(toast, r) {
		t.Errorf("the toast %v covers 经香港中转 %v", toast, r)
	}
}

// The line under the LAN's name changes one at a time (DESIGN §6.5): the
// one showing fades out before the next shows, never both at once.
func TestLANLineOneAtATime(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(1200 * time.Millisecond)
	const was, now = "4 个人，5 台设备都连上了", "已暂停，打开开关就恢复"
	if !tt.HasText(was) {
		t.Fatalf("the LAN's line is not %q: %q", was, tt.Texts())
	}
	sw, ok := tt.Find("暂停周末联机")
	if !ok {
		t.Fatalf("no switch to pause 周末联机: %q", tt.Texts())
	}
	tt.Move(sw.X+sw.W/2, sw.Y+sw.H/2)
	tt.Press(sw.X+sw.W/2, sw.Y+sw.H/2)
	tt.Advance(16 * time.Millisecond)
	tt.Release(sw.X+sw.W/2, sw.Y+sw.H/2)
	tt.Advance(40 * time.Millisecond)
	if tt.HasText(now) {
		t.Errorf("%q shows while %q still fades out", now, was)
	}
	if !tt.HasText(was) {
		t.Errorf("%q went at once", was)
	}
	tt.Advance(500 * time.Millisecond)
	if !tt.HasText(now) || tt.HasText(was) {
		t.Errorf("not %q yet: %q", now, tt.Texts())
	}
}
