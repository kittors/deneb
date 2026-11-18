package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
)

// TestChangeFrames records the window's words and numbers as they change,
// each change a scene, when DENEB_CHANGES names a folder (the 16th batch:
// the user, 11-18, "所有的内容的变化都得有微动效，要录屏逐帧检查效果"). Its
// frames, a 60th of a second apart, go to <theme>-<scene>/, the stills
// beside them, and marks.txt the frames the changes start in; cut-check.py
// (design/tools) finds what changes in one frame there. DENEB_CHANGES_ONLY
// picks scenes; DENEB_CHANGES_THEMES the themes (light by default);
// DENEB_CHANGES_SIZES the windows' sizes, as 980x640,1440x900, each then
// in <theme>-<w>x<h>-<scene>/ (the usual 1180x740 alone by default).
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
	sizes := strings.Split(os.Getenv("DENEB_CHANGES_SIZES"), ",")
	for _, theme := range themes {
		for _, size := range sizes {
			for _, sc := range changeScenes {
				if len(only) > 0 && !only[sc.key] {
					continue
				}
				var w *waitRig
				if size == "" {
					w = newWaitRig(t, root, theme, theme == "dark", sc.key, false)
				} else {
					var ww, wh int
					if _, err := fmt.Sscanf(size, "%dx%d", &ww, &wh); err != nil {
						t.Fatalf("DENEB_CHANGES_SIZES: %q is not <w>x<h>", size)
					}
					w = newSizedRig(t, root, theme, sc.key, ww, wh)
				}
				sc.play(w)
				if w.dir != "" {
					os.WriteFile(filepath.Join(w.dir, "marks.txt"), []byte(strings.Join(w.marks, "\n")+"\n"), 0o644)
				}
			}
		}
	}
}

// newSizedRig is newWaitRig with a window w by h, recording into
// <root>/<theme>-<w>x<h>-<key>/.
func newSizedRig(t *testing.T, root, theme, key string, w, h int) *waitRig {
	t.Helper()
	d := client.NewDemo()
	d.Manual()
	for _, e := range []string{"m3", "m4", "m5", "m6", "m7"} {
		d.Simulate(e)
	}
	s := &slowStore{d: d}
	a := newApp(s, demoPlaces())
	s.a = a
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, w, h)}
	r.tt.SetDark(theme == "dark")
	r.a.selected, r.a.more = "", false
	r.move(float32(w-180), float32(h-80))
	r.run(1200 * time.Millisecond)
	return &waitRig{m3rig: r, s: s, root: root, key: fmt.Sprintf("%s-%dx%d-%s", theme, w, h, key)}
}

// flip presses what s labels, noting the frame it lets go in as a change.
func (w *waitRig) flip(s string) {
	w.press(s)
	w.mark("change")
}

// rec starts the recording, the store answering at once; what was noted
// before it is not of it.
func (w *waitRig) rec() {
	w.marks = nil
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
	// 周末联机 paused on this computer from its switch, then on again: its
	// links drawn back into this computer, then out again (the 19th batch).
	{"pause", func(w *waitRig) {
		w.rec()
		w.flip("暂停周末联机")
		w.run(1500 * time.Millisecond)
		w.shot("paused")
		w.flip("暂停周末联机")
		w.run(1800 * time.Millisecond)
	}},
	// From 周末联机 to 工作室, to 全部 and back, 我的游戏主机 put in 工作室
	// too first: the links on both pages run on as their devices move, the
	// others' fade with theirs (the 19th batch).
	{"lan-switch", func(w *waitRig) {
		w.openWords("我的设备")
		w.press("把我的游戏主机加入工作室")
		w.run(900 * time.Millisecond)
		w.openWords("周末联机")
		w.rec()
		w.flip("工作室")
		w.run(1800 * time.Millisecond)
		w.shot("studio")
		w.flip("全部")
		w.run(1800 * time.Millisecond)
		w.shot("all")
		w.flip("周末联机")
		w.run(1800 * time.Millisecond)
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
