package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// This computer without a network (DESIGN §6.10): the header says so
// calmly, every other device waits for it, greyed, and nothing says 连不上;
// the menu bar's mark is the outline, its panel says so too. Back, each
// device is being connected to first, then connected, and 连不上 never
// shows in between.
func TestNoNetworkSaysSo(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.view, 1180, 740)
	frames := func(dt time.Duration) {
		for end := tt.Now().Add(dt); tt.Now().Before(end); {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
			if s := strings.Join(tt.Texts(), " "); strings.Contains(s, "连不上") {
				t.Fatalf("连不上 shows: %q", tt.Texts())
			}
		}
	}
	frames(800 * time.Millisecond)
	d.Simulate("nonet")
	frames(time.Second)
	if _, line := a.summary(&lightPalette); line != "这台电脑没联网，联网后会自动连上" {
		t.Errorf("the header says %q", line)
	}
	for _, dv := range a.devices {
		want := "等这台电脑联网"
		if dv.id == a.self {
			want = "你正在用"
		}
		if got := a.status(dv); got != want {
			t.Errorf("%s says %q, want %q", dv.name, got, want)
		}
	}
	if st := trayState(a.snap, time.Now()); st != "hollow" {
		t.Errorf("the mark is %q", st)
	}
	if _, head := a.panelSummary(&lightPalette); head != "这台电脑没联网" {
		t.Errorf("the panel's head says %q", head)
	}
	if l := a.snap.LAN("weekend"); a.lanLine(l) != "等这台电脑联网" {
		t.Errorf("the panel's 周末联机 says %q", a.lanLine(l))
	}
	d.Simulate("unreachable")
	frames(500 * time.Millisecond)
	if ch := choresOf(a.snap, time.Now()); len(ch) > 0 {
		t.Errorf("chores without a network: %+v", ch)
	}
	d.Simulate("nonet")
	frames(500 * time.Millisecond)
	if jd := a.device("jdesk"); a.status(jd) != "正在连接…" {
		t.Errorf("back, 阿杰的台式机 says %q", a.status(jd))
	}
	d.Simulate("nonet-back")
	frames(2500 * time.Millisecond)
	if jd := a.device("jdesk"); a.status(jd) != "直接连上" {
		t.Errorf("connected again, 阿杰的台式机 says %q", a.status(jd))
	}
}

// TestM2NoNetworkFrames records §6.10 in the demo, both themes, the window
// and the menu bar's panel, for review, when DENEB_M2 names a folder.
func TestM2NoNetworkFrames(t *testing.T) {
	root := os.Getenv("DENEB_M2")
	if root == "" {
		t.Skip("set DENEB_M2 to a folder to record M2's demo")
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		recorder := func(tt *ui.Tester, name string) (func(time.Duration), func(string)) {
			dir := filepath.Join(root, theme+"-"+name)
			os.MkdirAll(dir, 0o755)
			n := 0
			rec := func(dt time.Duration) {
				for end := tt.Now().Add(dt); tt.Now().Before(end); n++ {
					tt.Advance(time.Second / 60)
					tt.Frame()
					f, _ := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.jpg", n)))
					jpeg.Encode(f, tt.Image(), &jpeg.Options{Quality: 88})
					f.Close()
				}
			}
			shot := func(state string) {
				tt.SetScale(2)
				tt.Frame()
				f, _ := os.Create(filepath.Join(root, theme+"-"+name+"_"+state+".png"))
				png.Encode(f, tt.Image())
				f.Close()
				tt.SetScale(1)
				tt.Frame()
			}
			return rec, shot
		}
		// The window: off, a device's card, back, connected.
		a, d := newDemoApp()
		d.Manual()
		tt := ui.NewTester(a.view, 1180, 740)
		tt.SetDark(dark)
		rec, shot := recorder(tt, "nonet")
		rec(time.Second)
		d.Simulate("nonet")
		rec(2500 * time.Millisecond)
		shot("没联网")
		p := a.device("jdesk").at()
		tt.Move(a.origin.X+p.X, a.origin.Y+p.Y)
		rec(300 * time.Millisecond)
		tt.ClickAt(a.origin.X+p.X, a.origin.Y+p.Y)
		rec(900 * time.Millisecond)
		shot("卡片")
		tt.Move(a.origin.X+300, a.origin.Y+40)
		rec(300 * time.Millisecond)
		tt.ClickAt(a.origin.X+300, a.origin.Y+40)
		rec(800 * time.Millisecond)
		d.Simulate("nonet")
		rec(1200 * time.Millisecond)
		shot("正在连接")
		d.Simulate("nonet-back")
		rec(3 * time.Second)
		shot("连上了")

		// The menu bar's panel: off and back.
		pa, pd := newDemoApp()
		pd.Manual()
		m := &menubarScreen{a: pa, open: true}
		pa.panelShown = true
		pt := ui.NewTester(m.view, screenW, screenH)
		pt.SetDark(dark)
		prec, pshot := recorder(pt, "nonet-panel")
		prec(800 * time.Millisecond)
		pd.Simulate("nonet")
		prec(1500 * time.Millisecond)
		pshot("面板没联网")
		pd.Simulate("nonet")
		prec(1000 * time.Millisecond)
		pd.Simulate("nonet-back")
		prec(2000 * time.Millisecond)
		pshot("面板连上了")
	}
}
