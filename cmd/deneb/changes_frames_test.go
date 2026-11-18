package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestChangeFrames records the window's words and numbers as they change,
// each change a scene, when DENEB_CHANGES names a folder (the 16th batch:
// the user, 11-18, "所有的内容的变化都得有微动效，要录屏逐帧检查效果"). Its
// frames, a 60th of a second apart, go to <theme>-<scene>/, the stills
// beside them, and marks.txt the frames the changes start in; cut-check.py
// (design/tools) finds what changes in one frame there. DENEB_CHANGES_ONLY
// picks scenes; DENEB_CHANGES_THEMES the themes (light by default).
func TestChangeFrames(t *testing.T) {
	root := os.Getenv("DENEB_CHANGES")
	if root == "" {
		t.Skip("set DENEB_CHANGES to a folder to record the changes")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_CHANGES_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	themes := strings.Split(os.Getenv("DENEB_CHANGES_THEMES"), ",")
	if themes[0] == "" {
		themes = []string{"light"}
	}
	for _, theme := range themes {
		for _, sc := range changeScenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			w := newWaitRig(t, root, theme, theme == "dark", sc.key, false)
			sc.play(w)
			if w.dir != "" {
				os.WriteFile(filepath.Join(w.dir, "marks.txt"), []byte(strings.Join(w.marks, "\n")+"\n"), 0o644)
			}
		}
	}
}

// rec starts the recording, the store answering at once.
func (w *waitRig) rec() {
	w.dir = filepath.Join(w.root, w.key)
	w.still = w.dir
	reset(w.t, w.dir)
	w.run(300 * time.Millisecond)
}

// event has the demo change as the world would, noting the frame.
func (w *waitRig) event(e string, after time.Duration) {
	w.mark("change")
	w.d.Simulate(e)
	w.run(after)
}

var changeScenes = []struct {
	key  string
	play func(w *waitRig)
}{
	// The tray's words, as the pointer comes onto my relays' "+" and leaves
	// it (the user's 11-18 screenshot).
	{"tray-hint", func(w *waitRig) {
		w.rec()
		w.mark("change")
		w.glide(w.text("用我自己的服务器"), 18)
		w.run(1200 * time.Millisecond)
		w.shot("hint")
		w.mark("change")
		w.glide(func() ui.Point { return ui.Point{X: 700, Y: 420} }, 18)
		w.run(1200 * time.Millisecond)
	}},
	// A device's way changing: Mia's MacBook, 直接连上, then through 香港,
	// then direct again.
	{"path", func(w *waitRig) {
		w.rec()
		w.event("network", 1500*time.Millisecond)
		w.shot("relay")
		w.event("network", 1500*time.Millisecond)
	}},
	// 阿杰's desktop going offline and coming back: its line, the header's.
	{"offline", func(w *waitRig) {
		w.rec()
		w.event("offline", 1500*time.Millisecond)
		w.shot("offline")
		w.event("offline", 1500*time.Millisecond)
	}},
	// Connecting: 正在连接, then 直接连上.
	{"connect", func(w *waitRig) {
		w.rec()
		w.event("connect", 1500*time.Millisecond)
		w.shot("connecting")
		w.event("connected", 1500*time.Millisecond)
	}},
	// My Devices: another computer of mine comes; the count, the page's
	// line and its card.
	{"device-up", func(w *waitRig) {
		w.openWords("我的设备")
		w.rec()
		w.event("device-up", 2000*time.Millisecond)
		w.shot("came")
	}},
	// My Devices: 我的游戏主机 going offline and back: its card's line and
	// dot, the page's line.
	{"mine-offline", func(w *waitRig) {
		w.openWords("我的设备")
		w.rec()
		w.event("mine-offline", 1500*time.Millisecond)
		w.shot("offline")
		w.event("mine-offline", 1500*time.Millisecond)
	}},
	// The rename dialog's words as they go wrong and right: one word too
	// long (its 40 at most), then put right.
	{"rename-error", func(w *waitRig) {
		w.openAt(w.device("game"))
		w.openWords("重命名")
		w.rec()
		w.mark("change")
		w.tt.Type(strings.Repeat("长", 41))
		w.run(1200 * time.Millisecond)
		w.shot("too-long")
		w.mark("change")
		w.tt.Key(0, ui.KeyBackspace)
		w.run(1200 * time.Millisecond)
	}},
	// My Devices: a LAN chosen for a device, 工作室 for 我的游戏主机.
	{"chip", func(w *waitRig) {
		w.openWords("我的设备")
		w.rec()
		w.press("把我的游戏主机加入工作室")
		w.mark("change")
		w.run(1500 * time.Millisecond)
		w.shot("chosen")
	}},
}
