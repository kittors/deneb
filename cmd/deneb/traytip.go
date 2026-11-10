package main

import (
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The tip the window's first close shows over the mark on Windows (DESIGN
// §7.2). The window goes, and Windows keeps a new icon in the "^" of the
// notification area, out of sight: the tip says Deneb is still here, the
// connections too, and where to click. It looks as the panel does, a line
// of words, and goes after 6 seconds, or at a click on it, or on the mark.
// It shows once, ever: a file in Deneb's config folder says it did.

// tipWidth is the tip's width; it is as tall as its words.
const tipWidth = 300

// trayTip is the first close's tip while it shows. win is read from any
// goroutine (its timer); the rest on the main thread.
type trayTip struct {
	win     atomic.Pointer[mygo.Window]
	fades   atomic.Int64
	float   float
	closing bool
	height  int  // as tall as it last laid out
	hidden  bool // the mark is in the "^", out of sight
}

// tipWords is what the tip says: with the mark in the "^", where Windows
// keeps a new icon until it is dragged out, it says that too.
func tipWords(hidden bool) string {
	if hidden {
		return "Deneb 还在这儿，连接照常。点这颗星就能打开窗口，可以把它拖到任务栏上"
	}
	return "Deneb 还在这儿，连接照常。点这颗星就能打开窗口"
}

// firstClose shows the tip, the first time the window closes on Windows.
func (m *menuBar) firstClose() {
	if m.os != "windows" || !firstTime(tipFile()) {
		return
	}
	m.showTip()
}

// showTip shows the tip over the mark, 12 over the taskbar, fading in and
// rising into place, as the panel does.
func (m *menuBar) showTip() {
	t := &m.tip
	if t.win.Load() != nil {
		return
	}
	icon := m.tray.Bounds()
	t.hidden = trayHidden()
	t.float = float{rise: true, icon: icon}
	t.closing = false
	h := t.height
	if h == 0 {
		h = 68
	}
	log.Printf("deneb: the tip shows over the mark at %+v (in the \"^\": %v)", icon, t.hidden)
	w := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: m.tray, Placement: mygo.PlacementBottom, Gap: taskbarGap(icon, mygo.Screen.Displays(), 12),
		Width: tipWidth, Height: h, Shadow: true, Hidden: true,
		Content: ui.View(m.tipView),
	})
	t.win.Store(w)
	w.SetOpacity(0)
	w.Show()
	t.float.placed(w)
	t.float.lift(w, 0)
	fadeFlyout(w, &t.fades, &t.float, 0, 1, nil)
	time.AfterFunc(6*time.Second, func() {
		w.Update(func() {
			if t.win.Load() == w {
				m.closeTip()
			}
		})
	})
}

// closeTip puts the tip away, fading out and sinking.
func (m *menuBar) closeTip() {
	t := &m.tip
	w := t.win.Load()
	if w == nil || t.closing {
		return
	}
	t.closing = true
	fadeFlyout(w, &t.fades, &t.float, w.Opacity(), 0, func() {
		t.win.CompareAndSwap(w, nil)
		t.closing = false
		w.Destroy()
	})
}

// tipView is the tip's flyout: the tip, sized to its words.
func (m *menuBar) tipView(c *ui.Context) {
	t := &m.tip
	p := colors(c)
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("tip-at")).Absolute().Left(0).Top(0).Children(func() {
		body := tipBody(c, p, t.hidden)
		if body.Clicked() {
			m.closeTip()
		}
		b := body.Bounds()
		if b.H <= 0 {
			// Not laid out yet: the next frame measures it.
			c.After(0)
			return
		}
		if h := int(b.H + 0.5); h != t.height {
			t.height = h
			if w := t.win.Load(); w != nil {
				w.SetSize(tipWidth, h)
				t.float.placed(w)
			}
		}
	})
}

// tipBody is the tip: Deneb's mark in a little well, and the words. Windows
// rounds, outlines and shadows the flyout it is in, as it does the panel's.
func tipBody(c *ui.Context, p *palette, hidden bool) ui.Element {
	body := ui.Row(c.Key("tip")).Width(tipWidth).Padding(14, 16).Gap(12).AlignItems(ui.Start).Background(p.bg)
	body.Children(func() {
		ui.Box(c).Size(28, 28).Radius(999).Background(p.well).Center().Children(func() { trayGlyph(c, "solid", p.ink) })
		ui.Text(c, tipWords(hidden)).FontSize(13).LineHeight(1.5).Grow(1).Shrink(1)
	})
	return body
}

// tipFile says the tip has shown: Deneb's config folder, as the canvas's
// places are kept there.
func tipFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Deneb", "closed-once")
}

// firstTime reports whether file is not there yet, and leaves it there: it
// is the first time. Without a file to keep (no config folder) it never
// is, so the tip never shows every time.
func firstTime(file string) bool {
	if file == "" {
		return false
	}
	if _, err := os.Stat(file); err == nil {
		return false
	}
	if os.MkdirAll(filepath.Dir(file), 0o700) != nil {
		return false
	}
	return os.WriteFile(file, nil, 0o600) == nil
}
