package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// M7 in the demo (DESIGN §6.15): the account card, deleting a device,
// deleting the account (the LANs with others handed over, or deleted; my
// relay in use), an account the seed names, logging this computer out.

// newM7Rig is the demo as an M7 server and engine have it.
func newM7Rig(t *testing.T) *m3rig {
	t.Helper()
	r := newM4Rig(t)
	r.d.Simulate("m5")
	r.d.Simulate("m6")
	r.d.Simulate("m7")
	r.run(600 * time.Millisecond)
	return r
}

// TestM7Frames records M7 in the demo, both themes, when DENEB_M7 names a
// folder: each scene's frames, a 60th of a second apart, in
// <theme>-<scene>/, and its stills beside them.
func TestM7Frames(t *testing.T) {
	root := os.Getenv("DENEB_M7")
	if root == "" {
		t.Skip("set DENEB_M7 to a folder to record M7's demo")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_M7_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	scenes := []struct {
		key  string
		play func(r *m3rig)
	}{
		{"account", playAccountCard},
		{"delete-device", playDeleteDevice},
		{"delete-account", playDeleteAccount},
		{"delete-account-all", playDeleteAccountAll},
		{"seed", playSeedAccount},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			r := newM7Rig(t)
			r.tt.SetDark(dark)
			r.dir = filepath.Join(root, theme+"-"+sc.key)
			r.still = r.dir
			reset(t, r.dir)
			r.run(600 * time.Millisecond)
			sc.play(r)
		}
	}
}

// Scene M7-01: the sidebar's last row opens the account card: who I am,
// this computer with 登出, the privacy notes, 注销账号; the pointer on the
// notes, then on deleting the account; Esc puts it away. Then 登出: the
// window goes to the sign-in.
func playAccountCard(r *m3rig) {
	r.run(400 * time.Millisecond)
	r.glide(r.text("账号"), 26)
	r.run(400 * time.Millisecond)
	r.tap(r.text("账号"))
	r.run(900 * time.Millisecond)
	r.shot("账号卡片")
	r.glide(r.text("隐私说明"), 20)
	r.run(700 * time.Millisecond)
	r.shot("指着隐私说明")
	r.glide(r.text("注销账号"), 16)
	r.run(700 * time.Millisecond)
	r.tt.Key(0, ui.KeyEscape)
	r.run(700 * time.Millisecond)
	r.tap(r.text("账号"))
	r.run(900 * time.Millisecond)
	r.tap(r.text("登出"))
	r.run(1200 * time.Millisecond)
	r.shot("登出了")
	r.park()
	r.run(500 * time.Millisecond)
}

// Scene M7-02: 我的游戏主机's card, "更多", 删除这台设备 and how that differs
// from logging it out; it goes, the toast offers undo, undo brings it back;
// deleted again, the toast goes and the server hears of it. This
// computer's "更多" says 删除这台电脑.
func playDeleteDevice(r *m3rig) {
	r.run(400 * time.Millisecond)
	r.tap(r.device("game"))
	r.run(1000 * time.Millisecond)
	r.tap(r.text("更多"))
	r.run(900 * time.Millisecond)
	r.shot("更多里的删除")
	r.tap(r.text("删除这台设备"))
	r.run(900 * time.Millisecond)
	r.shot("删掉了")
	r.tap(r.text("撤销"))
	r.run(1200 * time.Millisecond)
	r.shot("撤销")
	r.tap(r.device("game"))
	r.run(1000 * time.Millisecond)
	r.tap(r.text("更多"))
	r.run(800 * time.Millisecond)
	r.tap(r.text("删除这台设备"))
	r.run(800 * time.Millisecond)
	r.park()
	r.run(4600 * time.Millisecond)
	r.shot("提示条走了")
	r.tap(r.device("mac"))
	r.run(1000 * time.Millisecond)
	r.tap(r.text("更多"))
	r.run(900 * time.Millisecond)
	// The card is taller than the window now: it scrolls in itself.
	r.glide(at(1010, 520), 16)
	r.wheelAt(1010, 520, 300, 24)
	r.run(600 * time.Millisecond)
	r.shot("删除这台电脑")
	r.park()
	r.run(500 * time.Millisecond)
}

// scrollPage turns the deletion page's wheel by dy, as a hand does.
func (r *m3rig) scrollPage(dy float32, steps int) { r.wheelAt(590, 420, dy, steps) }

// wheelAt turns the wheel by dy over x, y, a little each frame.
func (r *m3rig) wheelAt(x, y, dy float32, steps int) {
	for i := 0; i < steps; i++ {
		r.tt.Wheel(x, y, 0, dy/float32(steps))
		r.run(time.Second / 60)
	}
}

// Scene M7-03: 注销账号 from the account card: the dry run, then what goes,
// my two LANs with others in them handed to who joined first (Mia, 小林),
// my relay in use; the email typed again turns the button red; deleted,
// the window goes to the sign-in, which says so, and how to stop my relay.
func playDeleteAccount(r *m3rig) {
	r.run(400 * time.Millisecond)
	r.tap(r.text("账号"))
	r.run(900 * time.Millisecond)
	r.tap(r.text("注销账号"))
	r.run(300 * time.Millisecond)
	r.shot("正在看会删掉什么")
	r.run(1200 * time.Millisecond)
	r.shot("会删掉的")
	r.scrollPage(420, 30)
	r.run(600 * time.Millisecond)
	r.shot("转给最早加入的人")
	field := r.text("你的邮箱")()
	r.tap(at(field.X, field.Y))
	r.run(300 * time.Millisecond)
	r.typeSlowly("kittors@example.com", 55*time.Millisecond)
	r.run(600 * time.Millisecond)
	r.shot("邮箱对上了")
	// The red button under the field: its words are the title's too.
	r.tap(at(590, field.Y+23+14+22))
	r.run(500 * time.Millisecond)
	r.shot("正在注销")
	r.run(2000 * time.Millisecond)
	r.shot("注销了")
	r.park()
	r.run(600 * time.Millisecond)
}

// Scene M7-04: the other way with my LANs: 整个删掉, its card ringed in red,
// and what it means for the people in them; back to handing them over.
func playDeleteAccountAll(r *m3rig) {
	r.run(400 * time.Millisecond)
	r.tap(r.text("账号"))
	r.run(900 * time.Millisecond)
	r.tap(r.text("注销账号"))
	r.run(1500 * time.Millisecond)
	r.scrollPage(300, 24)
	r.run(500 * time.Millisecond)
	r.tap(r.text("整个删掉"))
	r.run(1000 * time.Millisecond)
	r.shot("整个删掉")
	r.tap(r.text("转给最早加入的人"))
	r.run(1000 * time.Millisecond)
	r.tap(r.text("算了"))
	r.run(900 * time.Millisecond)
	r.park()
	r.run(500 * time.Millisecond)
}

// Scene M7-05: an account the deployment's seed names: the dry run is
// refused, and the page says the server's words, and the way back.
func playSeedAccount(r *m3rig) {
	r.d.Simulate("m7-seed")
	r.run(400 * time.Millisecond)
	r.tap(r.text("账号"))
	r.run(900 * time.Millisecond)
	r.tap(r.text("注销账号"))
	r.run(1600 * time.Millisecond)
	r.shot("种子账号")
	r.tap(r.text("返回"))
	r.run(900 * time.Millisecond)
	r.park()
	r.run(500 * time.Millisecond)
}
