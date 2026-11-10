package main

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The menu bar as the system has it (DESIGN §7.1): Deneb's mark, an item
// of the macOS menu bar, and under it the panel a click opens, a flyout of
// MyGo's drawing menubar.go's panel. The window closes to the menu bar:
// Deneb stays there, as the engine stays (§2.4), and the panel's
// "打开 Deneb", or the Dock icon, brings the window back.
//
// All of it runs on the main thread, as the views' frames do, but open,
// which the window's wake reads from any goroutine, and the fades' timers.
type menuBar struct {
	a    *app
	tray *mygo.Tray

	open     atomic.Pointer[mygo.Window] // the panel while it shows, or fades away
	closing  bool                        // the open panel fades away
	fades    atomic.Int64                // the latest fade, which stops those before it
	quitting bool

	mark   string          // the mark shown: "solid", "dot" or "hollow"
	seen   *model.Snapshot // the world the mark was last set for, and when
	seenAt time.Time
}

// newMenuBar puts Deneb's mark in the menu bar, or returns nil where the
// system has no menu bar to put it in.
func newMenuBar(a *app) *menuBar {
	tray, err := mygo.NewTray(mygo.TrayOptions{ID: "deneb", Icon: trayIcon("solid"), IconIsTemplate: true, ToolTip: "Deneb"})
	if err != nil {
		log.Printf("deneb: no menu bar mark: %v", err)
		return nil
	}
	m := &menuBar{a: a, tray: tray, mark: "solid"}
	a.systemPanel = true
	// A click opens the panel or puts it away, and so does a right-click:
	// M2 has no menu of its own there yet.
	tray.OnClick(m.toggle)
	tray.OnRightClick(m.toggle)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { m.quitting = true })
	return m
}

// view is v, the mark following what v shows at the end of each frame:
// ops on their way included, as letting someone in from the panel.
func (m *menuBar) view(v func(*ui.Context)) func(*ui.Context) {
	if m == nil {
		return v
	}
	return func(c *ui.Context) {
		v(c)
		m.follow(m.a.snap, c.Now())
	}
}

// attach lets w close to the menu bar, and gives the panel the way into it.
func (m *menuBar) attach(w *mygo.Window) {
	if m == nil {
		return
	}
	a := m.a
	w.OnClose(func(e *mygo.CloseEvent) {
		if m.quitting {
			return
		}
		e.PreventDefault()
		w.Hide()
		// What its toast held back goes now, as when the canvas changes:
		// out of sight, the toast's moment would never be over.
		if t := a.toast; t != nil && t.commit != nil {
			t.commit(nil)
		}
		a.toast = nil
	})
	// The Dock icon brings the window back.
	mygo.App.OnActivate(func(bool) {
		if !w.IsVisible() {
			w.Show()
		}
	})
	a.showMain = func() {
		m.close()
		w.Show()
		w.Focus()
	}
	a.resizePanel = func(h int) {
		if p := m.open.Load(); p != nil {
			p.SetSize(panelWidth, h)
		}
	}
	// While no view draws, the mark follows the store, and the clock: a
	// login comes due with no news.
	go func() {
		for range time.Tick(30 * time.Second) {
			w.Update(m.followStore)
		}
	}()
	m.followStore()
}

// followStore sets the mark for the store's world and the ops on their
// way, as the views would show it.
func (m *menuBar) followStore() {
	if m == nil {
		return
	}
	s := m.a.store.Snapshot()
	for _, p := range m.a.pend {
		s = s.With(p.op)
	}
	m.follow(s, time.Now())
}

// follow sets the mark for s.
func (m *menuBar) follow(s *model.Snapshot, now time.Time) {
	if s == m.seen && now.Sub(m.seenAt) < time.Second {
		return
	}
	m.seen, m.seenAt = s, now
	if st := trayState(s, now); st != m.mark {
		m.mark = st
		m.tray.SetIcon(trayIcon(st), true)
	}
}

// update asks the open panel for a frame built anew.
func (m *menuBar) update() {
	if m == nil {
		return
	}
	if p := m.open.Load(); p != nil {
		p.Update(func() {})
	}
}

// toggle opens the panel, or puts it away.
func (m *menuBar) toggle() {
	if m.open.Load() != nil && !m.closing {
		m.close()
		return
	}
	m.show()
}

// show opens the panel under the mark, fading in: a new flyout each time,
// as MyGo has them, as tall as the panel last was.
func (m *menuBar) show() {
	a := m.a
	if p := m.open.Load(); p != nil {
		// It was fading away: it comes back.
		m.closing = false
		m.fade(p, p.Opacity(), 1, nil)
		return
	}
	h := a.panelHeight
	if h == 0 {
		h = 360
	}
	p := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: m.tray, Placement: mygo.PlacementBottom, Gap: 6,
		Width: panelWidth, Height: h, Focusable: true, Shadow: true, CornerRadius: 20, Hidden: true,
		Content: ui.View(m.view(a.panel)),
	})
	// A click elsewhere, or Escape, puts it away: it fades out first.
	p.OnClose(func(e *mygo.CloseEvent) {
		if !m.quitting && m.open.Load() == p {
			e.PreventDefault()
			m.close()
		}
	})
	p.OnClosed(func() {
		if m.open.CompareAndSwap(p, nil) {
			m.closing, a.panelShown = false, false
		}
		// What its rows held goes now, whether or not a view draws.
		a.flushPanel(nil)
	})
	m.open.Store(p)
	m.closing, a.panelShown = false, true
	if a.panelHeight != 0 && a.panelHeight != h {
		// Its first frame found it taller or shorter than it was.
		p.SetSize(panelWidth, a.panelHeight)
	}
	p.SetOpacity(0)
	p.Show()
	m.fade(p, 0, 1, nil)
}

// close puts the panel away, fading out.
func (m *menuBar) close() {
	p := m.open.Load()
	if p == nil || m.closing {
		return
	}
	m.closing = true
	m.fade(p, p.Opacity(), 0, p.Destroy)
}

// fade takes the panel's opacity from one value to the other in 160 ms
// (DESIGN §7.1), the whole of it, the system's shadow too, and then does
// done on the main thread. A later fade stops it.
func (m *menuBar) fade(p *mygo.Window, from, to float64, done func()) {
	gen := m.fades.Add(1)
	go func() {
		const steps = 10
		for i := 1; i <= steps; i++ {
			time.Sleep(16 * time.Millisecond)
			if m.fades.Load() != gen {
				return
			}
			k := float64(i) / steps
			p.SetOpacity(from + (to-from)*(1-(1-k)*(1-k)*(1-k)))
		}
		if done != nil {
			p.Update(func() {
				if m.fades.Load() == gen {
					done()
				}
			})
		}
	}()
}

// trayIcons are the menu bar's marks, drawn by TestTrayIcons.
var trayIcons = func() map[string][]byte {
	out := map[string][]byte{}
	for _, st := range []string{"solid", "dot", "hollow"} {
		b, err := trayIconFiles.ReadFile("trayicons/" + st + ".png")
		if err != nil {
			panic(err)
		}
		out[st] = b
	}
	return out
}()

//go:embed trayicons/*.png
var trayIconFiles embed.FS

// trayIcon returns the mark of state st as this system's bar shows it. The
// marks are templates, black on transparent: macOS tints them to its menu
// bar, and Windows shows them white on a dark taskbar. A Linux panel shows
// an AppIndicator's icon as it is: no template, and MyGo gives it no icon
// of the theme's to follow either. There the mark is white, for GNOME's top
// bar, dark in the light style and in the dark one.
func trayIcon(st string) []byte {
	if runtime.GOOS == "linux" {
		return trayIconsWhite[st]
	}
	return trayIcons[st]
}

// trayIconsWhite are trayIcons in white: the same marks, the user's
// (design/brand/final/tray), with the same transparency.
var trayIconsWhite = func() map[string][]byte {
	out := map[string][]byte{}
	for st, b := range trayIcons {
		w, err := whiten(b)
		if err != nil {
			panic(err)
		}
		out[st] = w
	}
	return out
}()

// whiten returns the PNG image b in white, each pixel keeping its alpha.
func whiten(b []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r := img.Bounds()
	out := image.NewNRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			a := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA).A
			out.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
