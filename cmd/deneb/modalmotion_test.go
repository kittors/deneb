package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// Pin both directions of the original defect: a fresh modal used to appear
// fully painted immediately, and removing its parent skipped Exit entirely.
func TestModalPanelFadesInAndOut(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "reduced"}[reduced], func(t *testing.T) {
			open := false
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#404040"))
				modalPanel(c, &lightPalette, "motion-test", "测试", 420, open, func() { open = false }, func() {
					if dialogButton(c, &lightPalette, "cancel", "取消", false, false, false).Clicked() {
						open = false
					}
				})
			}, 980, 640)
			tt.SetPreferences(ui.Preferences{ReduceMotion: reduced})
			tt.Frame()
			sample := func() uint8 { return tt.Image().RGBAAt(310, 310).R }
			baseline := sample()
			open = true
			tt.Frame()
			if got := sample(); got > baseline+5 {
				t.Fatalf("opening jumps to painted dialog: first=%d baseline=%d", got, baseline)
			}
			tt.Advance(80 * time.Millisecond)
			middle := sample()
			if middle <= baseline+20 || middle >= 250 {
				t.Fatalf("opening has no intermediate fade: %d", middle)
			}
			tt.Advance(600 * time.Millisecond)
			if got := sample(); got < 250 {
				t.Fatalf("opening did not settle: %d", got)
			}
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if open {
				t.Fatal("cancel did not close modal")
			}
			if got := sample(); got < 245 {
				t.Fatalf("closing disappeared on its first frame: %d", got)
			}
			tt.Advance(80 * time.Millisecond)
			closing := sample()
			if closing <= baseline+5 || closing >= 245 {
				t.Fatalf("closing has no intermediate fade: %d", closing)
			}
			if closing < 160 {
				t.Fatalf("closing loses the panel before its retreat can read: 80ms=%d", closing)
			}
			tt.Advance(600 * time.Millisecond)
			if got := sample(); got != baseline {
				t.Fatalf("closing left a backdrop: %d, want %d", got, baseline)
			}
		})
	}
}

func TestModalReopenAndNestedEscape(t *testing.T) {
	parent, child := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(white)
		modalPanel(c, &lightPalette, "parent", "设备", 540, parent, func() { parent = false }, func() {
			if dialogButton(c, &lightPalette, "open", "编辑", false, false, false).Clicked() {
				child = true
			}
		})
		modalPanel(c, &lightPalette, "child", "头像", 420, child, func() { child = false }, func() { ui.Text(c, "预览") })
	}, 980, 640)
	tt.Frame()
	parent = true
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Click("编辑")
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if child || !parent {
		t.Fatal("Esc did not close only the top modal")
	}
	tt.Advance(60 * time.Millisecond)
	child = true
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText("预览") {
		t.Fatal("reopen during dismissal lost content")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	if parent || child {
		t.Fatal("modal stack did not close")
	}
}

func TestRenameDismissalKeepsCenter(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	a.myDevicesOpen = true
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: a.snap.Self}, a.snap.Device(a.snap.Self).Name)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	bounds := func() (int, int) {
		im := tt.Image()
		top, bottom := -1, -1
		for y := 120; y < 650; y++ {
			px := im.RGBAAt(420, y)
			if px.R > 225 && px.G > 225 && px.B > 225 {
				if top < 0 {
					top = y
				}
				bottom = y
			}
		}
		return top, bottom
	}
	top, bottom := bounds()
	center := (top + bottom) / 2
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	for _, step := range []time.Duration{16, 16, 16, 16, 16, 16, 16} {
		tt.Advance(step * time.Millisecond)
		y0, y1 := bounds()
		if y1-y0 > 80 {
			current := (y0 + y1) / 2
			if current < center-3 || current > center+12 {
				t.Fatalf("closing jumped: center %d -> %d, bounds %d..%d", center, current, y0, y1)
			}
		}
	}
}
