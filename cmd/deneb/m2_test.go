package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
)

// TestM2Frames records M2's new states in the demo, at 60 frames a second
// of the view's clock, both themes, when DENEB_M2 names a folder (DESIGN
// §6.7–§6.9, §11): each scene in its own folder, and a still at twice the
// size where it ends.
func TestM2Frames(t *testing.T) {
	root := os.Getenv("DENEB_M2")
	if root == "" {
		t.Skip("set DENEB_M2 to a folder to record M2's demo")
	}
	type rig struct {
		a    *app
		d    *client.Demo
		tt   *ui.Tester
		rec  func(ms int)
		at   func(id string) ui.Point
		move func(to ui.Point, ms int)
		shot func(state string) // a still of this moment, at twice the size
		// drag presses at from, carries it to `to` over ms as a hand would,
		// holds a moment (held, if any, takes a still then), and lets go.
		drag func(from, to ui.Point, ms int, held func())
		// dragVia is drag by way of the points between.
		dragVia func(path []ui.Point, ms int, held func())
	}
	click := func(r *rig, text string) {
		t.Helper()
		if err := r.tt.Click(text); err != nil {
			t.Fatalf("click %q: %v; the window shows %q", text, err, r.tt.Texts())
		}
	}
	// row is the middle of a sidebar row's label, switch is a LAN row's
	// switch, undo the toast's undo.
	row := func(r *rig, text string) ui.Point {
		t.Helper()
		b, ok := r.tt.Find(text)
		if !ok {
			t.Fatalf("no %q", text)
		}
		return ui.Point{X: b.X + 12, Y: b.Y + b.H/2}
	}
	switchOf := func(r *rig, lan string) ui.Point {
		t.Helper()
		b, ok := r.tt.Find("暂停" + lan)
		if !ok {
			t.Fatalf("no switch for %q", lan)
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
	undo := func(r *rig) ui.Point {
		t.Helper()
		b, ok := r.tt.Find("撤销")
		if !ok {
			t.Fatal("no undo")
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
	// toAll opens "全部", dev-db's count going.
	toAll := func(r *rig) {
		t.Helper()
		click(r, "全部")
		r.d.Simulate("using")
		r.rec(700)
	}
	scenes := []struct {
		name string
		play func(r *rig)
	}{
		// 阿杰's new device waits; a click lets it in; undo; the "×" turns it away.
		{"approve-device", func(r *rig) {
			r.rec(300)
			r.d.Simulate("pending-device")
			r.rec(900)
			r.move(r.at("jpi"), 300)
			r.rec(500)
			r.shot("等你同意")
			p := r.at("jpi")
			r.tt.ClickAt(p.X, p.Y)
			r.rec(1400)
			r.shot("同意了")
			click(r, "撤销")
			r.rec(1200)
			r.move(r.at("jpi"), 200)
			r.rec(400)
			if b, ok := r.tt.Find("撤销"); ok {
				_ = b
			}
			p = r.at("jpi")
			r.tt.ClickAt(p.X+27, p.Y-27)
			r.rec(1200)
		}},
		// 小林 waits in the ball stack; a click lets him in.
		{"approve-member", func(r *rig) {
			r.rec(300)
			r.d.Simulate("pending-member")
			r.rec(900)
			b, ok := r.tt.Find("林")
			if !ok {
				t.Fatal("no 林")
			}
			r.move(ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}, 300)
			r.rec(300)
			r.shot("等你同意")
			r.tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
			r.rec(1400)
			r.shot("同意了")
		}},
		// The LAN's card, from its name; a switch.
		{"lan-card", func(r *rig) {
			r.rec(300)
			b, _ := r.tt.Find("周末联机")
			for _, l := range r.tt.Texts() {
				_ = l
			}
			title := ui.Point{X: sidebarWidth + 60, Y: 36}
			r.move(title, 300)
			r.tt.ClickAt(title.X, title.Y)
			_ = b
			r.rec(900)
			r.shot("局域网卡片")
			sw, _ := r.tt.Find("审批新设备")
			r.move(ui.Point{X: 1180 - 20 - 16 - 12 - 19, Y: sw.Y + sw.H/2}, 300)
			r.tt.ClickAt(1180-20-16-12-19, sw.Y+sw.H/2)
			r.rec(900)
		}},
		// 我的游戏主机's login runs out soon: the amber dot renews it.
		{"renew", func(r *rig) {
			r.rec(300)
			r.d.Simulate("login-due")
			r.rec(900)
			r.shot("需要重新登录")
			p := r.at("game")
			r.move(ui.Point{X: p.X + 30, Y: p.Y - 30}, 400)
			r.rec(300)
			r.tt.ClickAt(p.X+30, p.Y-30)
			r.rec(1400)
		}},
		// 阿杰的台式机 is offline: its card says since when, and tells me as it
		// comes online.
		{"offline", func(r *rig) {
			r.rec(300)
			r.d.Simulate("offline")
			r.rec(700)
			p := r.at("jdesk")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(800)
			r.shot("不在线的原因")
			click(r, "上线时告诉我")
			r.rec(900)
			r.d.Simulate("offline")
			r.rec(1600)
			r.shot("上线了")
		}},
		// Nothing reaches Mia's MacBook: "加一个中转站" sends it through 香港.
		{"unreachable", func(r *rig) {
			r.rec(300)
			r.d.Simulate("unreachable")
			r.rec(800)
			p := r.at("mia")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(800)
			r.shot("连不上的原因")
			click(r, "加一个中转站")
			r.rec(1800)
			r.shot("经中转连上")
		}},
		// 阿杰的 NAS's login ran out: "提醒阿杰" copies a word to send him.
		{"expired", func(r *rig) {
			r.rec(300)
			r.d.Simulate("others-expired")
			r.rec(800)
			p := r.at("jnas")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(800)
			r.shot("别人的登录过期")
			click(r, "提醒阿杰")
			r.rec(1200)
		}},
		// 我的游戏主机 gets a new name, then a new kind, in its card.
		{"rename-kind", func(r *rig) {
			r.rec(300)
			p := r.at("game")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(800)
			// The name in the card, not the one under the tile.
			name := ui.Point{X: r.a.origin.X + r.a.cardAt.X + 16 + 40 + 12 + 30, Y: r.a.origin.Y + r.a.cardAt.Y + 16 + 9}
			r.move(name, 300)
			r.tt.ClickAt(name.X, name.Y)
			r.rec(400)
			r.tt.Key(0, ui.KeyBackspace)
			r.tt.Type("客厅的游戏机")
			r.rec(400)
			r.shot("就地改名")
			r.tt.Key(0, ui.KeyEnter)
			r.rec(800)
			// The tile at the card's top left shows the kinds.
			tile := ui.Point{X: r.a.origin.X + r.a.cardAt.X + 16 + 20, Y: r.a.origin.Y + r.a.cardAt.Y + 16 + 20}
			r.move(tile, 300)
			r.tt.ClickAt(tile.X, tile.Y)
			r.rec(700)
			r.shot("选种类")
			if k, ok := r.tt.Find("台式机"); ok {
				r.move(ui.Point{X: k.X + k.W/2, Y: k.Y + k.H/2}, 300)
			}
			click(r, "台式机")
			r.rec(1000)
		}},
		// Mia's new MacBook, named as her old one: a small 2.
		{"twin", func(r *rig) {
			r.rec(300)
			r.d.Simulate("twin")
			r.rec(900)
			p := r.at("mia2")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(1000)
			r.shot("同名设备")
		}},
		// Clash takes Deneb's connections: the header's pill, its card.
		{"notice", func(r *rig) {
			r.rec(300)
			r.d.Simulate("notice")
			r.rec(800)
			r.shot("提示胶囊")
			click(r, "Clash 可能挡住连接")
			r.rec(900)
			r.shot("提示卡片")
			click(r, "复制规则")
			r.rec(900)
			click(r, "知道了")
			r.rec(900)
		}},
		// Leo invites me by name: the card, put off to the sidebar, back,
		// and joined.
		{"invitation", func(r *rig) {
			r.rec(300)
			r.d.Simulate("invitation")
			r.rec(900)
			r.shot("邀请卡片")
			click(r, "以后再说")
			r.rec(900)
			r.shot("以后再说")
			click(r, "Leo 邀请你 · 桌游夜")
			r.rec(800)
			click(r, "加入")
			r.rec(1800)
		}},
		// Leo's ball, dragged onto 工作室's row, invites him there.
		{"invite-drag", func(r *rig) {
			r.rec(300)
			from, _ := r.tt.Find("L")
			to, _ := r.tt.Find("工作室")
			f := ui.Point{X: from.X + from.W/2, Y: from.Y + from.H/2}
			g := ui.Point{X: to.X + 30, Y: to.Y + to.H/2}
			r.move(f, 300)
			r.tt.Press(f.X, f.Y)
			for i := 1; i <= 50; i++ {
				k := float32(i) / 50
				r.tt.Move(f.X+(g.X-f.X)*k, f.Y+(g.Y-f.Y)*k)
				r.rec(17)
			}
			r.rec(400)
			r.shot("拖到局域网上")
			r.tt.Release(g.X, g.Y)
			r.rec(900)
			click(r, "工作室")
			r.rec(1000)
			r.shot("等他接受")
		}},
		// I wait for Leo to let me into 读书会: its row says so, its canvas
		// holds only him.
		{"waiting", func(r *rig) {
			r.rec(300)
			r.d.Simulate("waiting")
			r.rec(900)
			click(r, "读书会")
			r.rec(1200)
			r.shot("等拥有者同意")
		}},
		// An old 我的游戏主机, gone for 40 days: "旧的那台", and taken out.
		{"stale", func(r *rig) {
			r.rec(300)
			r.d.Simulate("stale")
			r.rec(900)
			p := r.at("game0")
			r.move(p, 300)
			r.tt.ClickAt(p.X, p.Y)
			r.rec(900)
			r.shot("旧的那台")
			click(r, "移出")
			r.rec(1500)
		}},
		// This computer uses dev-db's database: its count goes up.
		{"using", func(r *rig) {
			click(r, "工作室")
			r.rec(600)
			r.d.Simulate("using")
			r.rec(3200)
			r.shot("已连接计时")
		}},
		// ---- Batch 2: scene 3 in the client (DESIGN §9) ----
		// "全部" in the sidebar: every LAN at once, each in its region.
		{"all-view", func(r *rig) {
			r.rec(400)
			r.move(row(r, "全部"), 400)
			r.rec(200)
			click(r, "全部")
			r.d.Simulate("using")
			r.rec(400)
			r.move(ui.Point{X: 700, Y: 690}, 400)
			r.rec(1800)
			r.shot("全部局域网")
		}},
		// Hovering a LAN's row dims the others; off the rows, all come back.
		{"focus", func(r *rig) {
			toAll(r)
			r.move(row(r, "工作室"), 500)
			r.rec(900)
			r.shot("聚焦工作室")
			r.move(row(r, "周末联机"), 300)
			r.rec(900)
			r.shot("聚焦周末联机")
			r.move(ui.Point{X: 700, Y: 690}, 400)
			r.rec(1100)
		}},
		// 周末联机's switch pauses it on this computer, and resumes it.
		{"pause", func(r *rig) {
			toAll(r)
			sw := switchOf(r, "周末联机")
			r.move(sw, 500)
			r.rec(200)
			r.tt.ClickAt(sw.X, sw.Y)
			r.rec(1000)
			r.move(ui.Point{X: 700, Y: 690}, 400)
			r.rec(900)
			r.shot("已暂停")
			r.move(sw, 400)
			r.rec(200)
			r.tt.ClickAt(sw.X, sw.Y)
			r.rec(1000)
			r.move(ui.Point{X: 700, Y: 690}, 400)
			r.rec(900)
			r.shot("恢复了")
		}},
		// Paused on its own canvas: the line under the title says how to resume.
		{"pause-single", func(r *rig) {
			r.rec(300)
			sw := switchOf(r, "周末联机")
			r.move(sw, 500)
			r.rec(200)
			r.tt.ClickAt(sw.X, sw.Y)
			r.rec(800)
			r.move(ui.Point{X: 700, Y: 690}, 400)
			r.rec(900)
			r.shot("已暂停")
			r.move(sw, 400)
			r.rec(200)
			r.tt.ClickAt(sw.X, sw.Y)
			r.rec(1400)
		}},
		// My game console into 工作室's region: a move; undo takes it back.
		{"move", func(r *rig) {
			toAll(r)
			from := r.at("weekend/game")
			me := r.at("mac")
			to := r.at("work/lindb")
			to.X, to.Y = to.X-108, to.Y+196
			r.move(from, 400)
			// Under this computer, not over it, into the region's corner.
			r.dragVia([]ui.Point{from, {X: me.X, Y: me.Y + 236}, to}, 1300, func() { r.shot("松手挪到工作室") })
			r.rec(1300)
			r.shot("挪到了工作室")
			r.move(undo(r), 500)
			click(r, "撤销")
			r.rec(1500)
			r.shot("撤销以后")
		}},
		// My game console out of every region: it leaves its LAN; undo.
		{"leave", func(r *rig) {
			toAll(r)
			from := r.at("weekend/game")
			to := ui.Point{X: from.X + 250, Y: from.Y + 200}
			r.move(from, 400)
			r.drag(from, to, 1000, func() { r.shot("松手移出周末联机") })
			r.rec(1300)
			r.shot("移出了")
			r.move(undo(r), 500)
			click(r, "撤销")
			r.rec(1500)
		}},
		// 阿杰's desktop stops at its region's edge, and says why.
		{"others-edge", func(r *rig) {
			toAll(r)
			from := r.at("weekend/jdesk")
			to := ui.Point{X: from.X + 380, Y: from.Y + 30}
			r.move(from, 400)
			r.drag(from, to, 1200, func() { r.shot("停在区域边上") })
			r.rec(1300)
		}},
		// A region by its ground, its devices and links with it; let go on
		// this computer, it slides clear.
		{"region-drag", func(r *rig) {
			toAll(r)
			rr := r.a.regionRects["work"]
			from := ui.Point{X: sidebarWidth + rr.X + rr.W - 24, Y: headerHeight + rr.Y + 24}
			to := ui.Point{X: from.X - 50, Y: from.Y + 60}
			r.move(from, 400)
			r.drag(from, to, 800, func() { r.shot("抬起区域") })
			r.rec(1000)
			from = to
			to = ui.Point{X: from.X - 260, Y: from.Y}
			r.drag(from, to, 900, nil)
			r.rec(1400)
			r.shot("让开这台电脑")
		}},
		// Leo's invite, accepted in "全部": 桌游夜 comes in as a region.
		{"join", func(r *rig) {
			toAll(r)
			r.d.Simulate("invitation")
			r.rec(900)
			b, _ := r.tt.Find("加入")
			r.move(ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}, 500)
			r.rec(200)
			click(r, "加入")
			r.rec(1800)
			r.shot("新的区域")
		}},
		// This computer moves where it is put, and keeps out of the regions.
		{"this-computer", func(r *rig) {
			toAll(r)
			from := r.at("mac")
			to := r.at("work/lindb")
			r.move(from, 400)
			r.drag(from, to, 1100, nil)
			r.rec(1400)
			r.shot("停在区域外")
		}},
	}
	if only := os.Getenv("DENEB_M2_ONLY"); only != "" {
		keep := map[string]bool{}
		for _, n := range strings.Split(only, ",") {
			keep[n] = true
		}
		var some = scenes[:0:0]
		for _, sc := range scenes {
			if keep[sc.name] {
				some = append(some, sc)
			}
		}
		scenes = some
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			dir := filepath.Join(root, theme+"-"+sc.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			a, d := newDemoApp()
			d.Manual()
			a.copyText = func(string) {}
			var woken atomic.Bool
			a.setWake(func() { woken.Store(true) })
			tt := ui.NewTester(a.view, 1180, 740)
			tt.SetDark(dark)
			// The demo opens with this computer's card; the scenes start from
			// the canvas alone, the card gone before the first frame.
			a.selected = ""
			tt.Frame()
			tt.Advance(1200 * time.Millisecond)
			n := 0
			r := &rig{a: a, d: d, tt: tt}
			pointer := ui.Point{X: 700, Y: 600}
			r.rec = func(ms int) {
				for end := n + ms*60/1000; n < end; n++ {
					if woken.Swap(false) {
						tt.Frame()
					}
					tt.Advance(time.Second / 60)
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%04d.jpg", n)))
					if err != nil {
						t.Fatal(err)
					}
					jpeg.Encode(f, tt.Image(), &jpeg.Options{Quality: 88})
					f.Close()
				}
			}
			r.at = func(id string) ui.Point {
				dv := a.device(id)
				if dv == nil {
					t.Fatalf("%s: %s is not on the canvas", sc.name, id)
				}
				p := dv.at()
				return ui.Point{X: sidebarWidth + p.X, Y: headerHeight + p.Y}
			}
			r.shot = func(state string) {
				tt.SetScale(2)
				tt.Frame()
				if f, err := os.Create(filepath.Join(root, theme+"-"+sc.name+"_"+state+".png")); err == nil {
					png.Encode(f, tt.Image())
					f.Close()
				}
				tt.SetScale(1)
				tt.Frame()
			}
			r.move = func(to ui.Point, ms int) {
				from := pointer
				steps := max(1, ms*60/1000)
				for i := 1; i <= steps; i++ {
					k := float32(i) / float32(steps)
					tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
					r.rec(17)
				}
				pointer = to
			}
			r.dragVia = func(path []ui.Point, ms int, held func()) {
				from, to := path[0], path[len(path)-1]
				var lens []float32
				total := float32(0)
				for i := 1; i < len(path); i++ {
					l := dist(path[i-1], path[i])
					lens = append(lens, l)
					total += l
				}
				tt.Press(from.X, from.Y)
				r.rec(100)
				pointer = from
				steps := max(1, ms*60/1000)
				for i := 1; i <= steps; i++ {
					// Eased along the whole way, the points in between met
					// in passing.
					s := ui.EaseInOut(float32(i)/float32(steps)) * total
					j := 0
					for j < len(lens)-1 && s > lens[j] {
						s -= lens[j]
						j++
					}
					k := min(1, s/max(lens[j], 1))
					a, b := path[j], path[j+1]
					tt.Move(a.X+(b.X-a.X)*k, a.Y+(b.Y-a.Y)*k)
					r.rec(17)
				}
				pointer = to
				r.rec(300)
				if held != nil {
					held()
				}
				tt.Release(to.X, to.Y)
			}
			r.drag = func(from, to ui.Point, ms int, held func()) {
				r.dragVia([]ui.Point{from, to}, ms, held)
			}
			sc.play(r)
			tt.SetScale(2)
			tt.Frame()
			if f, err := os.Create(filepath.Join(root, theme+"-"+sc.name+".png")); err == nil {
				png.Encode(f, tt.Image())
				f.Close()
			}
		}
	}
}
