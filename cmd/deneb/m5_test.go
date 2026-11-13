package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// M5's first batch in the client (DESIGN §7.2, §6.13): the system's menu of
// the mark on Linux, the panel and the first close's tip over the Windows
// taskbar, the strip the window buttons take at the top right, and the
// notice of the local network permission.

// linuxMenu is the mark's menu on Linux, its system's menu a test's: what it
// was given last, and the items changed in place since.
type linuxMenu struct {
	t       *trayMenu
	sets    int
	menu    *mygo.Menu
	changed []string
	shown   int // times the window was brought to the front
}

func newLinuxMenu(a *app) *linuxMenu {
	lm := &linuxMenu{}
	m := &menuBar{a: a, os: "linux", mark: "solid"}
	lm.t = &trayMenu{m: m, set: func(menu *mygo.Menu) { lm.sets, lm.menu = lm.sets+1, menu }, change: func(it *mygo.MenuItem, l, was menuLine) {
		if l.label != was.label || l.enabled != was.enabled || (l.check && it.Checked != l.on) {
			lm.changed = append(lm.changed, l.key)
			it.Label, it.Disabled = l.label, !l.enabled
			if l.check {
				it.Checked = l.on
			}
		}
	}}
	m.menu = lm.t
	a.showMain = func() { lm.shown++ }
	return lm
}

// labels are the menu's items as the system shows them: a separator as
// "—", a check item with its tick, an item clicks do not reach in [].
func (lm *linuxMenu) labels() []string {
	var out []string
	for _, it := range lm.menu.Items() {
		switch {
		case it.Type == mygo.MenuItemSeparator:
			out = append(out, "—")
		case it.Type == mygo.MenuItemCheckbox && it.Checked:
			out = append(out, "✓ "+it.Label)
		case it.Type == mygo.MenuItemCheckbox:
			out = append(out, "  "+it.Label)
		case it.Disabled:
			out = append(out, "["+it.Label+"]")
		default:
			out = append(out, it.Label)
		}
	}
	return out
}

// The menu on Linux: how things are in grey, each LAN this computer is in
// with its tick, "打开 Deneb"; what waits for me comes in when it does. A
// click on a LAN pauses it at once, the window's switch and the menu's
// tick following, changed in place; a click on a chore opens the window on
// it; "打开 Deneb" opens the window.
func TestM5LinuxMenu(t *testing.T) {
	a, d := newDemoApp()
	lm := newLinuxMenu(a)
	m := lm.t.m
	m.followStore()
	want := []string{"[都连上了]", "—", "✓ 周末联机 · 5 台都连上了", "✓ 工作室 · 4 台都连上了", "—", "打开 Deneb"}
	if got := lm.labels(); !slices.Equal(got, want) {
		t.Fatalf("the menu:\n  %q\nwant\n  %q", got, want)
	}

	// Pausing 周末联机 from the menu: in place, the tick off.
	lm.t.click("lan/weekend")
	if l := a.snap.LAN("weekend"); l == nil || !l.Paused {
		t.Fatal("the click did not pause 周末联机")
	}
	want[2] = "  周末联机 · 已暂停"
	if got := lm.labels(); !slices.Equal(got, want) || lm.sets != 1 {
		t.Errorf("after pausing (%d menus made): %q, want %q", lm.sets, got, want)
	}
	if !slices.Contains(lm.changed, "lan/weekend") {
		t.Errorf("周末联机's item was not changed in place: %q", lm.changed)
	}
	// Both paused: the mark goes hollow; and back.
	lm.t.click("lan/work")
	if m.mark != "hollow" {
		t.Errorf("both LANs paused, the mark is %q", m.mark)
	}
	if _, line := summaryOf(a.snap, &lightPalette); line != "都暂停了" || lm.labels()[0] != "[都暂停了]" {
		t.Errorf("both paused, the menu says %q", lm.labels()[0])
	}
	lm.t.click("lan/weekend")
	lm.t.click("lan/work")
	if a.snap.LAN("weekend").Paused || a.snap.LAN("work").Paused || m.mark != "solid" {
		t.Error("the LANs did not resume from the menu")
	}

	// What waits for me comes in: the menu is made anew, the chores between
	// how things are and the LANs.
	d.Simulate("pending-device")
	d.Simulate("login-due")
	m.seen = nil
	m.followStore()
	got := lm.labels()
	if lm.sets != 2 || len(got) < 5 || got[2] != "阿杰的树莓派想加入周末联机…" || got[3] != "我的游戏主机需要重新登录…" || got[4] != "—" {
		t.Fatalf("with chores (%d menus made): %q", lm.sets, got)
	}
	if m.mark != "dot" {
		t.Errorf("something waits, the mark is %q", m.mark)
	}
	// A click on one opens the window on it: 周末联机, the Raspberry Pi's
	// card open, nothing let in.
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	a.show(nil, "work", false)
	tt.Advance(500 * time.Millisecond)
	lm.t.click("chore/device/weekend/jpi")
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	if lm.shown != 1 || a.lan != "weekend" || a.selected != "jpi" {
		t.Errorf("the chore's click: window shown %d times, on %q, %q chosen", lm.shown, a.lan, a.selected)
	}
	if l := a.snap.LAN("weekend"); !slices.Contains(l.Pending, "jpi") {
		t.Error("the menu let the Raspberry Pi in")
	}
	lm.t.click("open")
	if lm.shown != 2 {
		t.Error("打开 Deneb did not bring the window")
	}
	// Grey items do nothing.
	lm.t.click("status")
	if lm.shown != 2 {
		t.Error("the grey line did something")
	}
}

// Not logged in, or the engine not running: how it is, and the way into the
// window, as the panel has it.
func TestM5LinuxMenuBefore(t *testing.T) {
	for _, tc := range []struct {
		s    *model.Snapshot
		head string
	}{
		{&model.Snapshot{}, "[Deneb 没在运行]"},
		{&model.Snapshot{Engine: true}, "[还没登录]"},
		{&model.Snapshot{Engine: true, LoggedIn: true}, "[正在连接…]"},
	} {
		a, _ := newDemoApp()
		lm := newLinuxMenu(a)
		lm.t.show(tc.s, time.Now())
		if got, want := lm.labels(), []string{tc.head, "—", "打开 Deneb"}; !slices.Equal(got, want) {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

// The panel over a Windows taskbar: 12 over the taskbar whatever way into
// it the icon sits, and under a taskbar at the top; a mark nowhere to be
// seen leaves the gap as it is.
func TestTaskbarGap(t *testing.T) {
	// 1920×1080 at 125%: 1536×864 DIPs, a taskbar of 48 at the bottom.
	displays := []mygo.Display{{Bounds: mygo.Rectangle{Width: 1536, Height: 864}, WorkArea: mygo.Rectangle{Width: 1536, Height: 816}, Primary: true}}
	for _, tc := range []struct {
		icon mygo.Rectangle
		want int
	}{
		{mygo.Rectangle{X: 1310, Y: 820, Width: 24, Height: 40}, 16},
		{mygo.Rectangle{X: 1310, Y: 816, Width: 24, Height: 48}, 12},
		{mygo.Rectangle{}, 12},
		{mygo.Rectangle{X: 4000, Y: 820, Width: 24, Height: 40}, 12},
	} {
		if got := taskbarGap(tc.icon, displays, 12); got != tc.want {
			t.Errorf("icon %+v: gap %d, want %d", tc.icon, got, tc.want)
		}
	}
	top := []mygo.Display{{Bounds: mygo.Rectangle{Width: 1536, Height: 864}, WorkArea: mygo.Rectangle{Y: 48, Width: 1536, Height: 816}}}
	if got := taskbarGap(mygo.Rectangle{X: 1310, Y: 4, Width: 24, Height: 40}, top, 12); got != 16 {
		t.Errorf("a taskbar at the top: gap %d, want 16", got)
	}
}

// The first close's tip says it once: the file it leaves says it did.
func TestTipOnce(t *testing.T) {
	file := filepath.Join(t.TempDir(), "Deneb", "closed-once")
	if !firstTime(file) {
		t.Fatal("not the first time, the first time")
	}
	if firstTime(file) {
		t.Error("the first time, twice")
	}
	if _, err := os.Stat(file); err != nil {
		t.Error(err)
	}
	if firstTime("") {
		t.Error("with nowhere to keep it, the tip would show at every close")
	}
}

// The tip: the mark in its well and the words, two lines in 300; with the
// mark in the "^", where to drag it.
func TestTipBody(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		var h float32
		tt := ui.NewTester(func(c *ui.Context) {
			p := colors(c)
			h = tipBody(c, p, hidden).Bounds().H
		}, tipWidth, 200)
		tt.Frame()
		tt.Frame()
		if !tt.HasText(tipWords(hidden)) {
			t.Errorf("hidden=%v: the tip says %q", hidden, tt.Texts())
		}
		if want := float32(28 + 2*13*3/2); h < want-2 || h > want+2*13*1.5 {
			t.Errorf("hidden=%v: %v tall, want two or three lines of words", hidden, h)
		}
	}
	if !strings.Contains(tipWords(true), "可以把它拖到任务栏上") || strings.Contains(tipWords(false), "拖") {
		t.Error("the words for the mark in the \"^\"")
	}
}

// The window buttons at the top right (Windows, Linux) take a strip of
// their own over the main column: the header and the canvas move down by
// it, the canvas as much shorter. At the top left (macOS), nothing moves.
func TestM5Strip(t *testing.T) {
	var brand ui.Rect // the sidebar's "Deneb", as the Mac has it
	at := func(bar ui.TitleBar) (ui.Rect, float32, float32) {
		a, _ := newDemoApp()
		tt := ui.NewTester(a.view, 1180, 740)
		tt.SetTitleBar(bar)
		tt.Frame()
		tt.Advance(800 * time.Millisecond)
		// The header's line: the LAN's name is in the sidebar too.
		r, ok := tt.Find("4 个人，5 台设备都连上了")
		if !ok {
			t.Fatal("no header")
		}
		// The sidebar is the same on every system, to the pixel.
		b, _ := tt.Find("Deneb")
		if bar.Left > 0 {
			brand = b
		} else if b != brand {
			t.Errorf("title bar %+v: the sidebar's Deneb at %v, the Mac's at %v", bar, b, brand)
		}
		return r, a.strip, a.ch
	}
	mac, macStrip, macH := at(ui.TitleBar{Height: macTitleBar, Left: 70})
	if macStrip != 0 {
		t.Errorf("macOS: a strip of %v", macStrip)
	}
	for _, tc := range []struct {
		name string
		bar  ui.TitleBar
	}{
		{"Windows", ui.TitleBar{Height: 32, Right: 3 * 46}},
		{"Linux", ui.TitleBar{Height: 40, Right: 104}},
	} {
		r, strip, h := at(tc.bar)
		if strip != tc.bar.Height {
			t.Errorf("%s: a strip of %v, want %v", tc.name, strip, tc.bar.Height)
		}
		if d := r.Y - mac.Y; d < tc.bar.Height-1 || d > tc.bar.Height+1 || r.X != mac.X {
			t.Errorf("%s: the header at %v, macOS's at %v: want it %v lower", tc.name, r, mac, tc.bar.Height)
		}
		if d := macH - h; d < tc.bar.Height-1 || d > tc.bar.Height+1 {
			t.Errorf("%s: the canvas %v tall, macOS's %v", tc.name, h, macH)
		}
	}
	if titleBarHeight("windows") != 32 || titleBarHeight("linux") != 40 || titleBarHeight("darwin") != 0 {
		t.Error("the title bars' heights")
	}
}

// The local network permission (DESIGN §6.13): the amber pill as the engine
// says it; its card says what it means in two short paragraphs, with
// "知道了" only, no "去系统设置" in this batch; and when the engine takes
// it back, the pill goes by itself, the card with it.
func TestM5LocalNetwork(t *testing.T) {
	a, d := newDemoApp()
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	d.Simulate("local-network")
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	if !tt.HasText("本地网络权限没拿到") {
		t.Fatal("no pill")
	}
	if err := tt.Click("本地网络权限没拿到"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(500 * time.Millisecond)
	for _, s := range []string{"Deneb 还没拿到本地网络权限", "知道了"} {
		if !tt.HasText(s) {
			t.Errorf("the card does not say %q", s)
		}
	}
	if tt.HasText("去系统设置") {
		t.Error("the card has 去系统设置")
	}
	n := model.LocalNetwork
	if paras := strings.Split(n.Detail, "\n"); len(paras) != 2 || !strings.Contains(paras[0], "连着同一个路由器") || !strings.Contains(paras[1], "正式版") || n.Settings != "" {
		t.Errorf("the notice: %+v", n)
	}
	// The engine reaches the devices at home: the pill goes, the card too.
	d.Simulate("local-network")
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	if tt.HasText("本地网络权限没拿到") || tt.HasText("Deneb 还没拿到本地网络权限") || a.selected == noticeCard {
		t.Error("the pill or its card stayed")
	}
}
