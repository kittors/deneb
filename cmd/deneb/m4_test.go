package main

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// M4's relays in the demo (DESIGN §6.12): a relay of mine dragged out of
// the tray is used by the LAN, and takes over what went through the public
// one; dragged back, or "不用这个中转站", it is not; people are kept off it,
// and let back; one that stops hands its links to the public one at once,
// and gets them back; a friend's and the public one stay where they are;
// the "+" gives an install command, which sets up a relay or is taken back.

// lan is the canvas's LAN as the store has it.
func (r *m3rig) lan() *model.LAN { return r.d.Snapshot().LAN(r.a.lan) }

// pathOf is how the store says this computer reaches dev.
func (r *m3rig) pathOf(dev string) model.Path { return r.d.Snapshot().Path(model.DeviceID(dev)) }

func TestRelayFromTheTray(t *testing.T) {
	r := newM4Rig(t, "m4-fresh")
	r.d.Do(t.Context(), model.Op{Kind: model.OpRelayJoin})
	r.d.Simulate("relay-joined")
	r.run(1200 * time.Millisecond)
	if p := r.pathOf("jnas"); p.Relay != "hk" {
		t.Fatalf("阿杰's NAS goes through %q, not the public relay", p.Relay)
	}
	nas := r.tile("jnas")
	r.drag(func() ui.Point { return r.traySlot("relay:srv") }, at(nas.X+190, nas.Y-150), 30)
	r.run(time.Second)
	if !r.lan().Added("srv") {
		t.Fatal("dropped on the canvas, my server is not in 周末联机")
	}
	if p := r.pathOf("jnas"); p.Relay != "srv" {
		t.Errorf("阿杰's NAS goes through %q, not my server", p.Relay)
	}
	if !r.has("周末联机用上了我的服务器") || !r.has("经我的服务器中转") {
		t.Errorf("no word of it: %q", r.tt.Texts())
	}
	// The public relay, through which nothing goes now, has shrunk away.
	if s := r.a.stopOf("hk"); s != nil && s.shown {
		t.Error("香港 stays on the canvas with nothing through it")
	}
	r.click("撤销")
	r.run(time.Second)
	if r.lan().Added("srv") || r.pathOf("jnas").Relay != "hk" {
		t.Errorf("undone, 周末联机 still uses my server (%v), the NAS through %q", r.lan().AddedRelays, r.pathOf("jnas").Relay)
	}
}

func TestRelayOutOfTheLAN(t *testing.T) {
	r := newM4Rig(t, "m4-in")
	r.tap(r.relay("srv"))
	r.run(800 * time.Millisecond)
	for _, s := range []string{"198.51.100.4", "我的服务器", "已经运行 12 天", "3 个人 · 5 台设备", "12.4 GB", "3.1 GB", "不让用的", "不用这个中转站"} {
		if !r.has(s) {
			t.Errorf("my server's card does not say %q: %q", s, r.tt.Texts())
		}
	}
	r.click("不用这个中转站")
	r.run(1500 * time.Millisecond)
	if r.lan().Added("srv") || r.pathOf("jnas").Relay != "hk" {
		t.Fatalf("周末联机 still uses my server (%v), the NAS through %q", r.lan().AddedRelays, r.pathOf("jnas").Relay)
	}
	if _, ok := r.a.slots["relay:srv"]; !ok {
		t.Error("my server is not back in the tray")
	}
	if s := r.a.stopOf("srv"); s != nil && (s.shown || s.flight != nil) {
		t.Errorf("my server stays on the canvas (shown %v, in flight %v)", s.shown, s.flight != nil)
	}
}

func TestRelayKeepsSomeoneOff(t *testing.T) {
	r := newM4Rig(t, "m4-in")
	r.tap(r.relay("srv"))
	r.run(800 * time.Millisecond)
	r.click("不让阿杰用")
	r.run(time.Second)
	rl := r.d.Snapshot().Relays["srv"]
	if rl.Kick("jie") == nil || slices.Contains(rl.Using, "jie") {
		t.Fatalf("阿杰 is not kept off my server: kicks %v, using %v", rl.Kicks, rl.Using)
	}
	if p := r.pathOf("jnas"); p.Relay != "hk" {
		t.Errorf("阿杰's NAS goes through %q, kept off my server", p.Relay)
	}
	if !r.has("已不让阿杰用我的服务器") || !r.has("2 个人 · 3 台设备") {
		t.Errorf("no word of it: %q", r.tt.Texts())
	}
	r.click("撤销")
	r.run(time.Second)
	if rl := r.d.Snapshot().Relays["srv"]; rl.Kick("jie") != nil || r.pathOf("jnas").Relay != "srv" {
		t.Errorf("undone, 阿杰 is still kept off (%v), the NAS through %q", rl.Kicks, r.pathOf("jnas").Relay)
	}
}

func TestRelayStopsAndComesBack(t *testing.T) {
	r := newM4Rig(t, "m4-in")
	r.d.Simulate("relay-down")
	r.run(1500 * time.Millisecond)
	if !r.has("改走公共中转站") || !r.has("连不上") || r.has("断开") {
		t.Errorf("my server stopped: %q", r.tt.Texts())
	}
	if s := r.a.stopOf("srv"); s == nil || !s.down || !s.shown {
		t.Error("my server is not grey on the canvas")
	}
	if s := r.a.stopOf("hk"); s == nil || !s.shown {
		t.Error("香港 does not carry the NAS")
	}
	r.d.Simulate("relay-down")
	r.run(1500 * time.Millisecond)
	if r.has("改走公共中转站") || !r.has("经我的服务器中转") {
		t.Errorf("my server back: %q", r.tt.Texts())
	}
}

func TestRelayNotMine(t *testing.T) {
	r := newM4Rig(t)
	tray := func() ui.Point {
		z := r.a.trayZone(r.a.cw, r.a.ch)
		return ui.Point{X: r.a.origin.X + z.X + z.W/2, Y: r.a.origin.Y + z.Y + z.H/2}
	}
	for _, c := range []struct{ id, says string }{
		{"jiesrv", "这是阿杰的中转站，只有阿杰能拿走"},
		{"hk", "公共中转站一直都在，拿不掉"},
	} {
		r.glide(r.relay(c.id), 12)
		r.press()
		r.glide(tray, 30)
		r.run(400 * time.Millisecond)
		if !r.has(c.says) {
			t.Errorf("%s held to the tray: %q", c.id, r.tt.Texts())
		}
		r.tt.Release(r.px, r.py)
		r.run(time.Second)
	}
	if !r.lan().Added("jiesrv") {
		t.Error("阿杰's server left 周末联机")
	}
	r.tap(r.relay("jiesrv"))
	r.run(800 * time.Millisecond)
	if !r.has("内容全程加密，阿杰看不到；阿杰能看到谁在经它连、用了多少。") || r.has("不用这个中转站") {
		t.Errorf("阿杰's server's card: %q", r.tt.Texts())
	}
}

func TestRelayInstallCommand(t *testing.T) {
	r := newM4Rig(t, "m4-fresh")
	r.click("用我自己的服务器")
	r.run(800 * time.Millisecond)
	s := r.d.Snapshot()
	if len(s.RelayJoins) != 1 || !r.has("在服务器上运行这一行") {
		t.Fatalf("no command: joins %v, %q", s.RelayJoins, r.tt.Texts())
	}
	r.click("复制")
	if got := r.tt.Clipboard(); got != "" && got != s.RelayJoins[0].Command {
		t.Errorf("copied %q", got)
	}
	r.d.Simulate("relay-joined")
	r.run(1200 * time.Millisecond)
	if !r.has("刚上线") || !r.has("我的服务器刚上线，拖进来就能用") {
		t.Errorf("my server came up: %q", r.tt.Texts())
	}
	// Another, not wanted: taken back once the toast goes.
	r.click("用我自己的服务器")
	r.click("关闭")
	r.click("等待安装")
	r.click("收回这行命令")
	if n := len(r.d.Snapshot().RelayJoins); n != 1 {
		t.Errorf("taken back at once, before the toast went: %d waiting", n)
	}
	r.run(5 * time.Second)
	if n := len(r.d.Snapshot().RelayJoins); n != 0 {
		t.Errorf("the toast went, %d commands still wait", n)
	}
}

func TestM4NeedsTheServer(t *testing.T) {
	r := newM3Rig(t)
	r.a.selected = ""
	r.run(800 * time.Millisecond)
	for _, s := range []string{"用我自己的服务器", "等待安装"} {
		if _, ok := r.tt.Find(s); ok {
			t.Errorf("an M3 server's canvas shows %q", s)
		}
	}
	r.tap(r.relay("hk"))
	r.run(800 * time.Millisecond)
	if r.has("公共中转站") {
		t.Errorf("an M3 server's relay has a card: %q", r.tt.Texts())
	}
}

// Every word M4's scenes show keeps a space between Latin letters or
// digits and Chinese (DESIGN §2: "Mia 的设备", "经 198.51.100.4 中转"), as
// the rest of the window does; a date keeps its own ("11月6日").
func TestM4Words(t *testing.T) {
	han := func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }
	latin := func(r rune) bool { return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }
	date := func(r rune) bool { return strings.ContainsRune("月日年号", r) }
	scenes := []struct {
		key    string
		events []string
		play   func(r *m3rig)
	}{
		{"public-card", nil, playPublicCard},
		{"my-server", []string{"m4-in"}, playMyServer},
		{"friend", nil, playFriend},
		{"fallback", []string{"m4-in"}, playFallback},
		{"own-server", []string{"m4-fresh"}, playOwnServer},
	}
	seen := map[string]bool{}
	for _, sc := range scenes {
		r := newM4Rig(t, sc.events...)
		r.onShot = func(state string) {
			for _, s := range r.tt.Texts() {
				rs := []rune(s)
				for i := 1; i < len(rs); i++ {
					a, b := rs[i-1], rs[i]
					if latin(a) && han(b) && !(unicode.IsDigit(a) && date(b)) || han(a) && latin(b) && !(date(a) && unicode.IsDigit(b)) {
						if !seen[s] {
							seen[s] = true
							t.Errorf("%s, %s: %q has no space between %q and %q", sc.key, state, s, a, b)
						}
						break
					}
				}
			}
		}
		sc.play(r)
	}
}
