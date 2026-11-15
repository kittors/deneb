package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestMyDevFrames records My Devices' three new parts in the demo, both
// themes, when DENEB_MYDEV names a folder (design/plan/m7/mydevices.md
// §10): each scene's frames, a 60th of a second apart, in <theme>-<scene>/,
// and its stills beside them; "matrix" is stills only, at three window
// sizes and three amounts of data. DENEB_MYDEV_ONLY picks scenes.
func TestMyDevFrames(t *testing.T) {
	root := os.Getenv("DENEB_MYDEV")
	if root == "" {
		t.Skip("set DENEB_MYDEV to a folder to record My Devices' scenes")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_MYDEV_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key  string
		play func(t *testing.T, root, theme string, dark bool)
	}{
		{"cards", playMyCards},
		{"lans", playMyLANs},
		{"services", playMyServices},
		{"add-one", playAddOne},
		{"add-closed", playAddClosed},
		{"tray", playTrayAdd},
		{"matrix", playMyMatrix},
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

// myRig is the demo (M4's world) at w×h, recording into <theme>-<key>/.
func myRig(t *testing.T, root, theme string, dark bool, key string, w, h float32, events ...string) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	d.Simulate("m3")
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, int(w), int(h))}
	r.run(800 * time.Millisecond)
	d.Simulate("m4")
	for _, e := range events {
		d.Simulate(e)
	}
	// Into another LAN, a device brings its services closed, as live.
	d.Simulate("catalogs-closed")
	r.a.selected, r.a.more = "", false
	r.tt.SetDark(dark)
	r.run(1200 * time.Millisecond)
	if key != "" {
		r.dir = filepath.Join(root, theme+"-"+key)
		r.still = r.dir
		reset(t, r.dir)
	}
	r.move(w-60, h-40)
	r.run(300 * time.Millisecond)
	return r
}

// open goes to My Devices from the sidebar.
func (r *m3rig) openMine() {
	r.tap(r.text("我的设备"))
	r.run(1100 * time.Millisecond)
}

// tapOnly clicks where to says and leaves what it opens open: tap answers
// a confirmation itself, which a recording of one must show.
func (r *m3rig) tapOnly(to func() ui.Point) {
	r.glide(to, 20)
	r.run(150 * time.Millisecond)
	r.tt.Press(r.px, r.py)
	r.run(70 * time.Millisecond)
	r.tt.Release(r.px, r.py)
}

// hover rests the pointer on what label names.
func (r *m3rig) hover(label string) {
	r.glide(r.text(label), 18)
	r.run(350 * time.Millisecond)
}

// field types s into the field labelled label, after clearing it.
func (r *m3rig) field(label, s string) {
	r.tap(r.text(label))
	r.run(150 * time.Millisecond)
	r.tt.Key(0, ui.KeyEnd)
	for i := 0; i < 8; i++ {
		r.tt.Key(0, ui.KeyBackspace)
	}
	r.run(100 * time.Millisecond)
	for _, ch := range s {
		r.tt.Type(string(ch))
		r.run(70 * time.Millisecond)
	}
	r.run(250 * time.Millisecond)
}

// D1: the cards' faces, then 详情 of one.
func playMyCards(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "cards", 1180, 740)
	r.openMine()
	r.shot("正面")
	r.hover("添加设备")
	r.shot("悬停-添加设备")
	r.tap(r.text("详情我的游戏主机"))
	r.run(900 * time.Millisecond)
	r.shot("详情")
	r.tap(r.text("收起我的游戏主机"))
	r.run(900 * time.Millisecond)
}

// D2: the LAN row, toggle chips: into 工作室, undo; out of 周末联机 after
// asking in place, undo; the tooltip on a chip.
func playMyLANs(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "lans", 1180, 740)
	r.openMine()
	in := say("把", "我的游戏主机", "加入", "工作室")
	r.hover(in)
	r.run(900 * time.Millisecond)
	r.shot("悬停提示")
	r.tap(r.text(in))
	r.run(1100 * time.Millisecond)
	r.shot("加入了")
	r.tap(r.text("撤销"))
	r.run(1100 * time.Millisecond)
	r.shot("撤销了")
	out := say("把", "我的游戏主机", "移出", "周末联机")
	r.tapOnly(r.text(out))
	r.run(700 * time.Millisecond)
	r.shot("再点一下移出")
	r.tapOnly(r.text(out))
	r.run(1100 * time.Millisecond)
	r.shot("移出了")
	r.tap(r.text("撤销"))
	r.run(1100 * time.Millisecond)
	r.shot("撤销移出")
}

// D3: a service opened in a LAN, closed there after asking in place; one
// set up by hand, wrong twice first; opened; one taken off, asked in place;
// the 命令行 word.
func playMyServices(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "services", 1180, 740)
	me := r.a.snap.Device("mac").Name
	r.openMine()
	r.tap(r.text(say("把", me, "的", "共享文件夹", "开放给", "周末联机")))
	r.run(800 * time.Millisecond)
	r.shot("开放给周末联机")
	closeRDP := say("在", "工作室", "关闭", me, "的", "远程桌面")
	r.tapOnly(r.text(closeRDP))
	r.run(700 * time.Millisecond)
	r.shot("再点一下关闭")
	r.tapOnly(r.text(closeRDP))
	r.run(900 * time.Millisecond)
	r.shot("关闭了")
	r.tap(r.text(say("给", me, "添加服务")))
	r.run(600 * time.Millisecond)
	r.shot("添加服务")
	r.field("名称", "我的网站")
	r.field("端口", "70000")
	r.tt.Key(0, ui.KeyEnter)
	r.run(600 * time.Millisecond)
	r.shot("端口不对")
	r.field("端口", "3389")
	r.tt.Key(0, ui.KeyEnter)
	r.run(600 * time.Millisecond)
	r.shot("端口被占用")
	r.field("端口", "8080")
	r.tt.Key(0, ui.KeyEnter)
	r.run(1100 * time.Millisecond)
	r.shot("添加了")
	r.tap(r.text(say("把", me, "的", "我的网站", "开放给", "周末联机")))
	r.run(900 * time.Millisecond)
	r.shot("网站开放给周末联机")
	del := say("删除", me, "的", "我的网站")
	r.hover(del)
	r.shot("悬停出叉")
	r.tapOnly(r.text(del))
	r.run(700 * time.Millisecond)
	r.shot("再点一下删除")
	r.tapOnly(r.text(del))
	r.run(1100 * time.Millisecond)
	r.shot("删除了")
	// The 工作室主机's 命令行, closed and opened again: the word, once.
	studio := r.a.snap.Device("studio").Name
	r.tt.Scroll(r.a.origin.X+500, r.a.origin.Y+400, 0, 260)
	r.run(700 * time.Millisecond)
	closeShell := say("在", "工作室", "关闭", studio, "的", "命令行")
	r.tapOnly(r.text(closeShell))
	r.run(500 * time.Millisecond)
	r.tapOnly(r.text(closeShell))
	r.run(900 * time.Millisecond)
	r.tap(r.text(say("把", studio, "的", "命令行", "开放给", "工作室")))
	r.run(900 * time.Millisecond)
	r.shot("命令行的提醒")
	r.run(5200 * time.Millisecond)
	r.shot("提醒淡掉了")
}

// D4: "添加设备" open: copy, "首次打开时被系统阻止", the other computer logs
// in, the tick, the dialog goes, the new card lights up in view.
func playAddOne(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "add-one", 1180, 740)
	r.openMine()
	r.tap(r.text("添加设备"))
	r.run(800 * time.Millisecond)
	r.shot("三步")
	r.tap(r.text("复制网址"))
	r.run(400 * time.Millisecond)
	r.shot("已复制")
	r.tap(r.text("首次打开时被系统阻止"))
	r.run(800 * time.Millisecond)
	r.shot("被阻止时")
	r.run(1500 * time.Millisecond)
	r.d.Simulate("device-up")
	r.run(700 * time.Millisecond)
	r.shot("已加入")
	r.run(1600 * time.Millisecond)
	r.shot("新卡片亮一下")
	r.run(1500 * time.Millisecond)
}

// D4b: "添加设备" closed, the other computer comes: a toast says so.
func playAddClosed(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "add-closed", 1180, 740)
	r.openMine()
	r.tap(r.text("添加设备"))
	r.run(800 * time.Millisecond)
	r.tap(r.text("关闭添加设备"))
	r.run(800 * time.Millisecond)
	r.d.Simulate("device-up")
	r.run(900 * time.Millisecond)
	r.shot("关着时的提示条")
	r.run(1500 * time.Millisecond)
}

// D5: every device of mine in 周末联机: the tray says where others show,
// and "添加设备" opens the dialog over the canvas.
func playTrayAdd(t *testing.T, root, theme string, dark bool) {
	r := myRig(t, root, theme, dark, "", 1180, 740)
	ctx := context.Background()
	for _, id := range []model.DeviceID{"studio", "mynas"} {
		r.d.Do(ctx, model.Op{Kind: model.OpLANAddDevice, LAN: "weekend", Device: id})
	}
	r.run(1500 * time.Millisecond)
	r.dir = filepath.Join(root, theme+"-tray")
	r.still = r.dir
	reset(t, r.dir)
	r.run(600 * time.Millisecond)
	r.shot("托盘")
	// On the "+", the words say what it does; "添加设备" fades in its place.
	r.hover("用我自己的服务器")
	r.run(300 * time.Millisecond)
	r.shot("指着加号")
	r.hover("添加设备")
	r.run(300 * time.Millisecond)
	r.shot("悬停-添加设备")
	r.tap(r.text("添加设备"))
	r.run(900 * time.Millisecond)
	r.shot("从托盘打开")
	r.tap(r.text("关闭添加设备"))
	r.run(800 * time.Millisecond)
}

// The stills of AGENTS.md §1.3: three window sizes; only this computer;
// many devices and LANs with long names, narrow and wide; 详情 of a long
// one.
func playMyMatrix(t *testing.T, root, theme string, dark bool) {
	dir := filepath.Join(root, theme+"-matrix")
	reset(t, dir)
	shot := func(r *m3rig, name string) {
		r.dir, r.still = dir, filepath.Join(dir, "m")
		r.shot(name)
		r.dir = ""
	}
	for _, s := range []struct {
		name string
		w, h float32
	}{{"窄", 980, 640}, {"中", 1180, 740}, {"宽", 1600, 1000}} {
		r := myRig(t, root, theme, dark, "", s.w, s.h)
		r.openMine()
		shot(r, s.name)
	}
	r := myRig(t, root, theme, dark, "", 1180, 740, "alone")
	r.openMine()
	shot(r, "只有这台电脑")
	// A new one, let into 周末联机 with nothing set up yet: "还没有服务".
	r = myRig(t, root, theme, dark, "", 1180, 740, "device-up")
	r.d.Do(context.Background(), model.Op{Kind: model.OpLANAddDevice, LAN: "weekend", Device: "pc"})
	r.run(600 * time.Millisecond)
	r.openMine()
	r.tt.Scroll(r.a.origin.X+500, r.a.origin.Y+400, 0, 2000)
	r.run(800 * time.Millisecond)
	shot(r, "还没有服务")
	for _, s := range []struct {
		name string
		w, h float32
	}{{"很多-窄", 980, 640}, {"很多-宽", 1600, 1000}} {
		r := myRig(t, root, theme, dark, "", s.w, s.h, "many")
		r.openMine()
		r.tt.Scroll(r.a.origin.X+500, r.a.origin.Y+400, 0, 900)
		r.run(800 * time.Millisecond)
		shot(r, s.name)
	}
	r = myRig(t, root, theme, dark, "", 1180, 740, "many")
	r.openMine()
	r.tt.Scroll(r.a.origin.X+500, r.a.origin.Y+400, 0, 900)
	r.run(800 * time.Millisecond)
	long := r.a.snap.Device("many0").Name
	if _, ok := r.tt.Find("详情" + long); ok {
		r.tap(r.text("详情" + long))
		r.run(900 * time.Millisecond)
	}
	shot(r, "长名字")
}
