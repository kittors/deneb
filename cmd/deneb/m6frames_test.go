package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
)

// M6 in the demo (DESIGN §7.3, §6.14): the first time's steps (sign in,
// the network adapter, the first LAN, of one's own or a friend's), the
// diagnostics package in this computer's card, and the notices of Surge
// and v2rayN.

// newM6Rig is the demo as an M6 server and engine have it; fresh, for a
// person who has no LAN yet; with the first time's steps from step, unless
// it is below 0.
func newM6Rig(t *testing.T, fresh bool, step int) *m3rig {
	t.Helper()
	r := newM4Rig(t)
	r.d.Simulate("m5")
	r.d.Simulate("m6")
	if fresh {
		r.d.Simulate("m6-fresh")
	}
	if step >= 0 {
		r.a.onboard = &onboarding{step: step}
	}
	r.run(600 * time.Millisecond)
	return r
}

// TestM6Frames records M6 in the demo, both themes, when DENEB_M6 names a
// folder: each scene's frames, a 60th of a second apart, in
// <theme>-<scene>/, and its stills beside them.
func TestM6Frames(t *testing.T) {
	root := os.Getenv("DENEB_M6")
	if root == "" {
		t.Skip("set DENEB_M6 to a folder to record M6's demo")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_M6_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key   string
		fresh bool
		step  int
		play  func(r *m3rig)
	}{
		{"signin", true, 0, playSignIn},
		{"adapter", true, 1, playAdapter},
		{"new-lan", true, 2, playNewLAN},
		{"join", true, 2, playJoin},
		{"diagnostics", false, -1, playDiagnostics},
		{"proxies", false, -1, playProxies},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			r := newM6Rig(t, sc.fresh, sc.step)
			r.tt.SetDark(dark)
			r.dir = filepath.Join(root, theme+"-"+sc.key)
			r.still = r.dir
			reset(t, r.dir)
			r.run(600 * time.Millisecond)
			sc.play(r)
		}
		// "先不装" as a still beside the adapter's scene.
		if len(only) == 0 || only["adapter"] {
			r := newM6Rig(t, true, 1)
			r.tt.SetDark(dark)
			r.dir = t.TempDir()
			r.still = filepath.Join(root, theme+"-adapter")
			r.tap(r.text("先不装"))
			r.run(500 * time.Millisecond)
			r.shot("先不装")
		}
	}
}

// typeSlowly types s a key at a time, as a hand does.
func (r *m3rig) typeSlowly(s string, each time.Duration) {
	for _, ch := range s {
		r.tt.Type(string(ch))
		r.run(each)
	}
}

// Scene M6-01: the email, the code sent to it; a wrong one shakes its
// boxes, the right one signs in and the next step comes in.
func playSignIn(r *m3rig) {
	r.run(500 * time.Millisecond)
	r.shot("登录")
	r.typeSlowly("kittors@example.com", 60*time.Millisecond)
	r.run(300 * time.Millisecond)
	r.tap(r.text("发验证码"))
	r.run(900 * time.Millisecond)
	r.shot("验证码")
	r.typeSlowly("123456", 140*time.Millisecond)
	r.run(900 * time.Millisecond)
	r.shot("验证码不对")
	r.typeSlowly(demoCode, 140*time.Millisecond)
	r.run(500 * time.Millisecond)
	r.shot("登录了")
	r.run(1600 * time.Millisecond)
	r.shot("下一步")
	r.run(500 * time.Millisecond)
}

// Scene M6-02: why the adapter, what the system asks; 装上, the system lets
// it in, and the last step comes in.
func playAdapter(r *m3rig) {
	r.run(600 * time.Millisecond)
	r.shot("装网卡")
	r.tap(r.text("装上"))
	r.run(900 * time.Millisecond)
	r.shot("等系统允许")
	r.run(1500 * time.Millisecond)
	r.shot("装好了")
	r.run(1600 * time.Millisecond)
	r.park()
	r.run(500 * time.Millisecond)
}

// Scene M6-03: a LAN of one's own: the steps fade away over the window,
// this computer pops in on the new LAN's canvas; a device of the tray goes
// in.
func playNewLAN(r *m3rig) {
	r.run(500 * time.Millisecond)
	r.shot("第一个局域网")
	r.glide(r.text("加入朋友的"), 22)
	r.run(500 * time.Millisecond)
	r.tap(r.text("新建局域网"))
	r.run(1600 * time.Millisecond)
	r.shot("建好了")
	slot := func() ui.Point { return r.traySlot("game") }
	me := r.tile("mac")
	r.drag(slot, at(me.X+200, me.Y+40), 44)
	r.run(1500 * time.Millisecond)
	r.shot("拖进一台")
	r.park()
	r.run(500 * time.Millisecond)
}

// Scene M6-04: a friend's LAN: the link pasted, the invite as its card says
// it, 加入, and the window on that LAN.
func playJoin(r *m3rig) {
	r.run(500 * time.Millisecond)
	r.tap(r.text("加入朋友的"))
	r.run(700 * time.Millisecond)
	r.typeSlowly(client.DemoInvite, 30*time.Millisecond)
	r.run(400 * time.Millisecond)
	r.shot("粘贴链接")
	r.tap(r.text("加入"))
	r.run(1200 * time.Millisecond)
	r.shot("邀请")
	r.tap(r.text("加入"))
	r.run(1800 * time.Millisecond)
	r.shot("加入了")
	r.park()
	r.run(500 * time.Millisecond)
}

// Scene M6-05: this computer's card, "更多", 导出诊断包: it is put together,
// and the toast says where it went.
func playDiagnostics(r *m3rig) {
	r.run(500 * time.Millisecond)
	r.tap(r.device("mac"))
	r.run(1000 * time.Millisecond)
	r.tap(r.text("更多"))
	r.run(900 * time.Millisecond)
	r.shot("更多")
	r.tap(r.text("导出诊断包"))
	r.run(700 * time.Millisecond)
	r.shot("正在收集")
	r.run(1300 * time.Millisecond)
	r.shot("存好了")
	r.park()
	r.run(1500 * time.Millisecond)
}

// Scene M6-06: Surge takes Deneb's connections: its pill, its card with
// the rule in Surge's words, copied; then v2rayN's.
func playProxies(r *m3rig) {
	r.run(400 * time.Millisecond)
	r.d.Simulate("notice-surge")
	r.run(1200 * time.Millisecond)
	r.shot("Surge胶囊")
	r.tap(r.text("Surge 可能挡住连接"))
	r.run(1200 * time.Millisecond)
	r.shot("Surge卡片")
	r.tap(r.text("复制规则"))
	r.run(1200 * time.Millisecond)
	r.tap(r.text("知道了"))
	r.run(900 * time.Millisecond)
	delete(r.a.noticesDone, "tun-capture")
	r.d.Simulate("notice-v2rayn")
	r.run(1200 * time.Millisecond)
	r.tap(r.text("v2rayN 可能挡住连接"))
	r.run(1200 * time.Millisecond)
	r.shot("v2rayN卡片")
	r.tap(r.text("知道了"))
	r.run(900 * time.Millisecond)
	r.park()
	r.run(500 * time.Millisecond)
}
