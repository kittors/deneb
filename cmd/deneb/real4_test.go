package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestRealAcceptance4 plays the window's part in M4's acceptance (r6 in
// design/plan/m4-acceptance.md): the relays' new cards on this computer's
// real engine, when DENEB_REAL4 lists the items, as "mine,public". Frames
// and log go as M2's (DENEB_FRAMES, DENEB_DARK).
//
//   - mine: my own server, in no LAN, waits in 工作室's tray. Dragged out
//     onto the canvas, 工作室 uses it (relay.add); its card says whose it
//     is, that it is up and since when, who is on it, what it carried this
//     month. Dragged back to the tray, 工作室 no longer uses it, as before.
//   - public: this computer keeps its direct path to 阿杰's desktop shut (a
//     test build's debug block), so the two go through 周末联机's relay,
//     the public one: its ball comes up on the canvas; its card says it is
//     ours, always there, and limited; the desktop's card says through
//     which. With the direct path back, the ball goes.
func TestRealAcceptance4(t *testing.T) {
	items := os.Getenv("DENEB_REAL4")
	if items == "" {
		t.Skip("set DENEB_REAL4 to the M4 items to play on this computer's engine, as mine,public")
	}
	r := newRealRig(t)
	r.until("the engine, logged in", func() bool { return r.a.snap.Engine && r.a.snap.LoggedIn && r.a.snap.Loaded })
	if !r.a.snap.Can(model.FeatureRelays) {
		t.Fatal("the server has no relays, or this store does not take them")
	}
	for _, s := range strings.Split(items, ",") {
		fn := map[string]func(*realRig){"mine": m4mine, "public": m4public}[strings.TrimSpace(s)]
		if fn == nil {
			t.Fatalf("no item %q", s)
		}
		r.step = strings.TrimSpace(s)
		r.logf("item %s begins", r.step)
		fn(r)
		r.rec = false
		r.logf("item %s ends", r.step)
	}
}

// relayStop is where relay id shows on the canvas, in the window.
func (r *realRig) relayStop(id model.RelayID) func() ui.Point {
	return func() ui.Point {
		s := r.a.stopOf(id)
		if s == nil {
			r.t.Fatalf("step %s: no relay %s on the canvas: %q", r.step, id, r.tt.Texts())
		}
		p := s.at()
		return ui.Point{X: r.a.origin.X + p.X, Y: r.a.origin.Y + p.Y}
	}
}

func m4mine(r *realRig) {
	work := r.lan("工作室")
	var id model.RelayID
	for _, rl := range r.a.snap.MyRelays() {
		if l := r.a.snap.LAN(work); l != nil && !l.Added(rl.ID) && !rl.Down {
			id = rl.ID
			break
		}
	}
	if id == "" {
		r.t.Fatalf("mine: no server of mine up and out of 工作室: %+v", r.a.snap.MyRelays())
	}
	r.t.Cleanup(func() {
		if l := r.a.base.LAN(work); l != nil && l.Added(id) {
			exec.Command("/bin/sh", "-c", "denebd "+opArgs("relay.remove", map[string]any{"lan": string(work), "relay": string(id)})).Run()
		}
	})
	key := "relay:" + string(id)
	r.until("my server in 工作室's tray", func() bool { _, ok := r.a.slots[key]; return ok && r.a.trayShows() })
	r.hold(1500 * time.Millisecond)
	r.still("托盘里的服务器")
	r.record()
	home := r.a.slots[key]
	// Clear of where its card opens, on the right.
	to := ui.Point{X: r.a.origin.X + r.a.cw*0.5, Y: r.a.origin.Y + r.a.ch*0.24}
	r.liftTo(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, func() ui.Point { return to }, func() bool { return true })
	let := time.Now()
	r.until("工作室 uses it, on the server", func() bool { l := r.a.base.LAN(work); return l != nil && l.Added(id) })
	r.logf("工作室 uses my server, %s after letting go", time.Since(let).Round(time.Millisecond))
	r.hold(1500 * time.Millisecond)
	r.still("拖进来了")
	r.record()
	r.hold(4500 * time.Millisecond) // the toast goes
	at := r.relayStop(id)()
	r.tt.ClickAt(at.X, at.Y)
	r.tick()
	r.until("its card", func() bool {
		return r.tt.HasText("我的服务器") && (r.tt.HasText("在线") || r.tt.HasText("连不上"))
	})
	r.hold(1200 * time.Millisecond)
	r.logf("its card: %q", r.tt.Texts())
	for _, want := range []string{"在线", "现在", "在用"} {
		if !r.tt.HasText(want) {
			r.t.Errorf("mine: its card lacks %q", want)
		}
	}
	if !r.tt.HasText("已经运行") && !r.tt.HasText("刚上线") {
		r.t.Errorf("mine: its card says nothing of how long it has run")
	}
	r.still("我的服务器的卡片")
	r.record()
	if r.tt.HasText("更多") {
		r.click("更多")
		r.hold(1000 * time.Millisecond)
		r.still("更多")
		r.record()
	}
	r.click("关闭")
	r.hold(800 * time.Millisecond)
	// Back to the tray: 工作室 no longer uses it.
	tray := func() ui.Point {
		z := r.a.trayZone(r.a.cw, r.a.ch)
		return ui.Point{X: r.a.origin.X + z.X + z.W/2, Y: r.a.origin.Y + z.Y + z.H/2}
	}
	r.liftTo(r.relayStop(id)(), tray, func() bool { return true })
	r.until("工作室 no longer uses it, on the server", func() bool { l := r.a.base.LAN(work); return l != nil && !l.Added(id) })
	r.hold(1200 * time.Millisecond)
	r.still("拖回托盘")
	r.record()
	r.until("the toast gone", func() bool { return r.a.toast == nil })
	r.hold(1200 * time.Millisecond)
	r.still("提示条走了")
}

func m4public(r *realRig) {
	pc := r.named("阿杰的台式机")
	desk := r.a.snap.Devices[model.DeviceID(pc)]
	if pc == "" || desk == nil || desk.Addr == "" {
		r.t.Fatal("public: no 阿杰的台式机, or no address of it")
	}
	r.lan("周末联机")
	r.until("the desktop on the canvas", func() bool { return r.a.device(pc) != nil })
	r.hold(time.Second)
	r.record()
	r.shellOut("denebd debug block " + desk.Addr)
	r.t.Cleanup(func() { exec.Command("/bin/sh", "-c", "denebd debug unblock "+desk.Addr).Run() })
	// An idle peer keeps the path it had: some traffic makes the engine
	// find the direct one gone, as anything used over it would.
	pings := background("denebd ping " + desk.Addr + " -c 40")
	var rl model.RelayID
	r.until("the desktop through a public relay", func() bool {
		d := r.a.device(pc)
		if d == nil || d.path != "relay" {
			return false
		}
		rl = r.a.relayOf(d)
		x := r.a.snap.Relays[rl]
		return x != nil && x.Public() && r.a.stopOf(rl) != nil
	})
	r.logf("the desktop goes through %s (%s)", rl, r.a.snap.Relays[rl].Label())
	r.hold(1500 * time.Millisecond)
	r.still("经公共中转站")
	r.record()
	at := r.relayStop(rl)()
	r.tt.ClickAt(at.X, at.Y)
	r.tick()
	r.until("its card", func() bool { return r.tt.HasText("公共中转站") || r.tt.HasText("自动兜底") })
	r.hold(1200 * time.Millisecond)
	r.logf("its card: %q", r.tt.Texts())
	if !r.tt.HasText("内容全程加密，中转站看不到。") {
		r.t.Errorf("public: its card lacks what it cannot see: %q", r.tt.Texts())
	}
	r.still("公共中转站的卡片")
	r.record()
	if r.tt.HasText("更多") {
		r.click("更多")
		r.hold(1000 * time.Millisecond)
		if !r.tt.HasText("每条连接限速 2 MB/s") {
			r.t.Errorf("public: its limit is not said: %q", r.tt.Texts())
		}
		r.still("限速")
		r.record()
	}
	r.click("关闭")
	r.hold(800 * time.Millisecond)
	d := r.a.device(pc)
	r.tt.ClickAt(r.at(d).X, r.at(d).Y)
	r.tick()
	r.until("the desktop's card", func() bool { return r.tt.HasText("经" + r.a.snap.Relays[rl].Label() + "中转") })
	r.hold(1200 * time.Millisecond)
	r.still("设备卡片")
	r.record()
	r.click("关闭")
	r.hold(800 * time.Millisecond)
	// The direct path back: the public relay's ball goes.
	r.shellOut("denebd debug unblock " + desk.Addr)
	pings()
	pings = background("denebd ping " + desk.Addr + " -c 30")
	r.until("direct again", func() bool { d := r.a.device(pc); return d != nil && d.path == "direct" })
	r.until("the public relay's ball gone", func() bool { s := r.a.stopOf(rl); return s == nil || !s.shown })
	r.hold(1500 * time.Millisecond)
	r.still("直连回来了")
	pings()
}
