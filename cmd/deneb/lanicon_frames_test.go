package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestLANIconFrames records the LANs' pictures (design/plan/m7/
// switch-motion.md §5) in the demo, both themes, when DENEB_LANICON names
// a folder: the sidebar and the header with them at three window sizes;
// the "全部" view; the card; "局域网头像" opened from it, a preset picked,
// saved; a picture of one's own; a server without the feature. Each
// scene's frames go in <theme>-<scene>/, its stills beside them.
// DENEB_LANICON_ONLY picks scenes.
func TestLANIconFrames(t *testing.T) {
	root := os.Getenv("DENEB_LANICON")
	if root == "" {
		t.Skip("set DENEB_LANICON to a folder to record the LANs' pictures")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_LANICON_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key  string
		play func(t *testing.T, root, theme string, dark bool)
	}{
		{"sizes", playLANIconSizes},
		{"pick", playLANIconPick},
		{"own", playLANIconOwn},
		{"none", playLANIconNone},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			sc.play(t, root, theme, dark)
		}
	}
}

// iconRig is the demo with two more LANs of mine, 家里 and 桌游夜, at w×h,
// as on the Mac, recording into <theme>-<key>/ (no frames when key is "").
func iconRig(t *testing.T, root, theme string, dark bool, key string, w, h float32) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	ctx := context.Background()
	d.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: "家里"})
	d.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: "大学室友"})
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, int(w), int(h))}
	r.tt.SetTitleBar(ui.TitleBar{Height: macTitleBar, Left: 70})
	r.tt.SetDark(dark)
	r.run(800 * time.Millisecond)
	a.post(func(c *ui.Context) { a.show(c, "weekend", false) })
	r.run(1200 * time.Millisecond)
	a.selected, a.more = "", false
	r.run(400 * time.Millisecond)
	r.dir = filepath.Join(root, theme+"-"+key)
	r.still = r.dir
	reset(t, r.dir)
	r.move(w-120, h-60)
	r.run(300 * time.Millisecond)
	return r
}

// The window at three sizes with the LANs' pictures: the sidebar, the
// header; the "全部" view's regions.
func playLANIconSizes(t *testing.T, root, theme string, dark bool) {
	for _, size := range []struct {
		name string
		w, h float32
	}{{"窄窗口", 980, 640}, {"中窗口", 1180, 740}, {"宽窗口", 1440, 900}} {
		r := iconRig(t, root, theme, dark, "sizes-"+size.name, size.w, size.h)
		r.shot("局域网")
		r.pick("工作室", 900*time.Millisecond)
		r.glide(r.row("家里"), 12)
		r.run(400 * time.Millisecond)
		r.shot("悬停")
		r.pick("全部", 1200*time.Millisecond)
		r.shot("全部")
	}
}

// The owner opens the LAN's card, its picture, "局域网头像"; points at a
// preset, presses it, picks it; saves: the sidebar and the header show it.
func playLANIconPick(t *testing.T, root, theme string, dark bool) {
	r := iconRig(t, root, theme, dark, "pick", 1180, 740)
	// The picture beside the title, which opens the card as the name does.
	r.tap(at(sidebarWidth+28+lanIconTitle/2, headerTop+titleLine/2))
	r.run(700 * time.Millisecond)
	r.shot("卡片")
	r.glide(r.text("换头像"), 18)
	r.run(400 * time.Millisecond)
	r.shot("指着头像")
	r.tapOnly(r.text("换头像"))
	r.run(800 * time.Millisecond)
	r.shot("对话框")
	r.glide(r.text("小猫"), 18)
	r.run(350 * time.Millisecond)
	r.shot("指着小猫")
	r.tt.Press(r.px, r.py)
	r.run(120 * time.Millisecond)
	r.shot("按着小猫")
	r.tt.Release(r.px, r.py)
	r.run(500 * time.Millisecond)
	r.shot("选了小猫")
	r.tapOnly(r.text("保存"))
	r.run(1200 * time.Millisecond)
	r.shot("保存了")
	if got := r.d.Snapshot().LAN("weekend").Icon.Preset; got != "fluent3d/cat" {
		r.t.Errorf("周末联机's picture is %q after saving 小猫", got)
	}
}

// A picture of one's own: chosen, cut square, shown in the dialog, saved.
func playLANIconOwn(t *testing.T, root, theme string, dark bool) {
	r := iconRig(t, root, theme, dark, "own", 1180, 740)
	r.a.pickAvatar = func() ([]byte, error) { return samplePhoto(), nil }
	r.a.openLANIcon("weekend")
	r.run(800 * time.Millisecond)
	r.tapOnly(r.text("上传图片"))
	r.run(600 * time.Millisecond)
	r.shot("选了自己的图")
	r.tapOnly(r.text("保存"))
	r.run(1200 * time.Millisecond)
	r.shot("保存了")
	if len(r.d.Snapshot().LAN("weekend").Icon.Image) == 0 {
		r.t.Error("周末联机 has no picture of its own after saving one")
	}
}

// A server without LANs' pictures: the members' balls, as before.
func playLANIconNone(t *testing.T, root, theme string, dark bool) {
	r := iconRig(t, root, theme, dark, "none", 1180, 740)
	r.d.Simulate("no-lan-icons")
	r.run(600 * time.Millisecond)
	r.shot("没有这项功能")
}

// samplePhoto is a picture of one's own, wider than high, as a photo is:
// soft bands of color the cut keeps the middle of.
func samplePhoto() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.NRGBA{uint8(90 + x*120/400), uint8(150 + y*80/300), 220, 255})
		}
	}
	for y := 110; y < 190; y++ {
		for x := 160; x < 240; x++ {
			if (x-200)*(x-200)+(y-150)*(y-150) < 40*40 {
				img.Set(x, y, color.NRGBA{255, 214, 102, 255})
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}
