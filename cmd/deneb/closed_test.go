package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// closeScreenShare turns this computer's 远程桌面 off in 周末联机, from its
// card, as a hand would.
func closeScreenShare(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	if a.selected != "mac" {
		p := a.device("mac").at()
		tt.ClickAt(a.origin.X+p.X, a.origin.Y+p.Y)
		for i := 0; i < 40; i++ {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
		}
	}
	box, ok := tt.Find("远程桌面")
	if !ok || a.cardAt.W == 0 {
		t.Fatalf("no 远程桌面 in this computer's card: %q", tt.Texts())
	}
	tt.ClickAt(a.origin.X+a.cardAt.X+a.cardAt.W-16-12-19, box.Y+box.H/2)
	tt.Frame()
}

// Closing a service someone uses (scene 5): a short red light runs on
// their link to this computer, and their device's second line says the
// service was closed, for a moment, then what it said before.
func TestClosingAServiceInUse(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.view, 1180, 740)
	frames := func(dt time.Duration) {
		for end := tt.Now().Add(dt); tt.Now().Before(end); {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
		}
	}
	frames(800 * time.Millisecond)
	closeScreenShare(t, a, tt)
	jd := a.device("jdesk")
	if jd == nil || jd.closed != "远程桌面" || a.status(jd) != "远程桌面被关了" {
		t.Fatalf("阿杰的台式机 after 远程桌面 closed: %+v, %q", jd, a.status(jd))
	}
	if mia := a.device("mia"); mia.closed != "" {
		t.Errorf("Mia, who used nothing, shows %q closed", mia.closed)
	}
	frames(closedRun + 100*time.Millisecond)
	if a.status(jd) != "远程桌面被关了" {
		t.Errorf("the second line went before its moment: %q", a.status(jd))
	}
	frames(closedSay)
	if s := a.status(jd); s != "直接连上" {
		t.Errorf("after its moment the second line says %q", s)
	}
}

// TestM2ClosedFrames records closing a service in use, both themes, for
// review (DESIGN §11), when DENEB_M2 names a folder.
func TestM2ClosedFrames(t *testing.T) {
	root := os.Getenv("DENEB_M2")
	if root == "" {
		t.Skip("set DENEB_M2 to a folder to record M2's demo")
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		dir := filepath.Join(root, theme+"-closed")
		os.MkdirAll(dir, 0o755)
		a, d := newDemoApp()
		d.Manual()
		tt := ui.NewTester(a.view, 1180, 740)
		tt.SetDark(dark)
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
		shot := func(name string) {
			tt.SetScale(2)
			tt.Frame()
			f, _ := os.Create(filepath.Join(root, theme+"-closed_"+name+".png"))
			png.Encode(f, tt.Image())
			f.Close()
			tt.SetScale(1)
			tt.Frame()
		}
		rec(800 * time.Millisecond)
		closeScreenShare(t, a, tt)
		rec(450 * time.Millisecond)
		shot("红光")
		rec(450 * time.Millisecond)
		shot("门口的圈")
		rec(800 * time.Millisecond)
		shot("被关了")
		rec(4 * time.Second)
	}
}
