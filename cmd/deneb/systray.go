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

// The bar Deneb's mark sits in, as each system has it (DESIGN §7.1, §7.2):
//
//   - macOS: an item of the menu bar, and under it the panel a click opens,
//     a flyout of MyGo's drawing menubar.go's panel.
//   - Windows: an icon of the notification area, and over it the same
//     panel, which a click or a right-click opens, and which Windows rounds,
//     outlines and shadows.
//   - Linux: an AppIndicator among the top bar's icons: a click opens the
//     system's menu (traymenu.go), a double click the window.
//
// The window closes to the bar: Deneb stays there, as the engine stays
// (§2.4), and the panel's "打开 Deneb", the menu's, the Dock icon or a
// double click on the mark bring it back. The first time it closes on
// Windows a tip over the mark says where Deneb is (traytip.go). A Linux
// desktop without AppIndicator (GNOME as it comes) has no bar for the mark:
// closing the window quits it there, and the connections go on.
//
// All of it runs on the main thread, as the views' frames do, but open and
// the tip's window, which the window's wake and the fades read from any
// goroutine, and the fades' timers.
type menuBar struct {
	a    *app
	tray *mygo.Tray
	os   string       // runtime.GOOS: what a click does, and how the panel shows
	win  *mygo.Window // the window, once attached

	open     atomic.Pointer[mygo.Window] // the panel while it shows, or fades away
	closing  bool                        // the open panel fades away
	fades    atomic.Int64                // the latest fade, which stops those before it
	float    float                       // how the open panel floats as it fades (Windows)
	quitting bool

	mark   string          // the mark shown: "solid", "dot" or "hollow"
	seen   *model.Snapshot // the world the mark was last set for, and when
	seenAt time.Time

	menu *trayMenu // Linux: the system's menu, in place of the panel
	tip  trayTip   // Windows: the first close's tip
}

// newMenuBar puts Deneb's mark in the system's bar, or returns nil where
// the system has none to put it in.
func newMenuBar(a *app) *menuBar {
	goos := runtime.GOOS
	tray, err := mygo.NewTray(mygo.TrayOptions{ID: "deneb", Icon: trayIconFor(goos, "solid", false), IconIsTemplate: goos == "darwin", ToolTip: "Deneb"})
	if err != nil {
		log.Printf("deneb: no mark in the system's bar: %v", err)
		return nil
	}
	if !trayHost() {
		// No panel here shows the mark (GNOME without the AppIndicator
		// extension): there is nowhere to close the window to, so closing
		// it quits it, and the connections go on (DESIGN §7.2).
		log.Printf("deneb: no panel shows the mark here: closing the window quits it")
		tray.Destroy()
		return nil
	}
	m := &menuBar{a: a, tray: tray, os: goos, mark: "solid"}
	if goos == "linux" {
		// A click opens the system's menu, once a double click's time is
		// over; a double click opens the window, which GNOME's AppIndicator
		// extension sends as the icon's activation (DESIGN §7.2).
		m.menu = newTrayMenu(m)
		tray.OnClick(m.openWindow)
	} else {
		a.systemPanel = true
		// A click opens the panel or puts it away, and so does a
		// right-click: no menu of the system's there, whose "退出" would
		// read as the connections going too (DESIGN §7.1, §7.2).
		tray.OnClick(m.toggle)
		tray.OnRightClick(m.toggle)
	}
	if goos == "windows" {
		// In the taskbar's ink: near black on a light taskbar, white on a
		// dark one, turning as the system's mode does.
		m.setIcon()
		tray.OnAppearanceChange(m.setIcon)
	}
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

// attach lets w close to the bar, and gives the panel and the menu the way
// into it.
func (m *menuBar) attach(w *mygo.Window) {
	if m == nil {
		return
	}
	a := m.a
	m.win = w
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
		// Where Deneb went, the first time (Windows).
		m.firstClose()
	})
	// The Dock icon brings the window back.
	mygo.App.OnActivate(func(bool) {
		if !w.IsVisible() {
			w.Show()
		}
	})
	a.showMain = func() {
		m.close()
		m.closeTip()
		w.Show()
		w.Focus()
	}
	a.resizePanel = func(h int) {
		if p := m.open.Load(); p != nil {
			p.SetSize(panelWidth, h)
			// The system placed it again, for its new height.
			m.float.placed(p)
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

// openWindow brings the window to the front.
func (m *menuBar) openWindow() {
	if m.a.showMain != nil {
		m.a.showMain()
	}
}

// followStore sets the mark, and the menu, for the store's world and the
// ops on their way, as the views would show it.
func (m *menuBar) followStore() {
	if m == nil {
		return
	}
	base := m.a.store.Snapshot()
	s := base
	for _, p := range m.a.pend {
		// As sync lays them over: one turned down, or in the store's
		// world already, is not.
		if p.done && (p.err != nil || base.Version >= p.ver) {
			continue
		}
		s = s.With(p.op)
	}
	m.follow(s, time.Now())
}

// follow sets the mark, and the menu, for s.
func (m *menuBar) follow(s *model.Snapshot, now time.Time) {
	if s == m.seen && now.Sub(m.seenAt) < time.Second {
		return
	}
	m.seen, m.seenAt = s, now
	if st := trayState(s, now); st != m.mark {
		m.mark = st
		m.setIcon()
	}
	if m.menu != nil {
		m.menu.show(s, now)
	}
}

// setIcon shows the mark m.mark in the bar's ink.
func (m *menuBar) setIcon() {
	if m.tray == nil {
		return // a test's
	}
	dark := m.os == "windows" && m.tray.Dark()
	m.tray.SetIcon(trayIconFor(m.os, m.mark, dark), m.os == "darwin")
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

// show opens the panel at the mark, fading in: a new flyout each time, as
// MyGo has them, as tall as the panel last was. Under the macOS menu bar it
// hangs 6 below the bar; over a Windows taskbar its bottom is 12 over it,
// and it rises into place as it fades in (DESIGN §7.2).
func (m *menuBar) show() {
	a := m.a
	// The mark's click puts the first close's tip away.
	m.closeTip()
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
	gap := 6
	m.float = float{rise: m.os == "windows"}
	if m.float.rise {
		m.float.icon = m.tray.Bounds()
		gap = taskbarGap(m.float.icon, mygo.Screen.Displays(), 12)
	}
	p := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: m.tray, Placement: mygo.PlacementBottom, Gap: gap,
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
	m.float.placed(p)
	m.float.lift(p, 0)
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

// fade fades the panel (fadeFlyout); a later fade stops it.
func (m *menuBar) fade(p *mygo.Window, from, to float64, done func()) {
	fadeFlyout(p, &m.fades, &m.float, from, to, done)
}

// fadeFlyout takes a flyout's opacity from one value to the other in 160 ms
// (DESIGN §7.1), the whole of it, the system's shadow too, and then does
// done on the main thread. Taking the next turn of fades stops it: a later
// fade of the same flyout. A flyout that floats (f) moves as it fades.
func fadeFlyout(p *mygo.Window, fades *atomic.Int64, f *float, from, to float64, done func()) {
	gen := fades.Add(1)
	rise := f.rise
	go func() {
		const steps = 10
		for i := 1; i <= steps; i++ {
			time.Sleep(16 * time.Millisecond)
			if fades.Load() != gen {
				return
			}
			k := float64(i) / steps
			o := from + (to-from)*(1-(1-k)*(1-k)*(1-k))
			p.SetOpacity(o)
			if rise {
				p.Update(func() {
					if fades.Load() == gen {
						f.lift(p, o)
					}
				})
			}
		}
		if done != nil {
			p.Update(func() {
				if fades.Load() == gen {
					done()
				}
			})
		}
	}()
}

// float is how a flyout over a Windows taskbar floats as it fades: 8 short
// of where the system placed it while faded out, in place once faded in
// (DESIGN §7.2: it rises 8 as it fades in, and sinks as it fades out). Its
// methods run on the main thread.
type float struct {
	rise bool           // it floats: Windows
	icon mygo.Rectangle // where the mark is
	x, y int            // where the system placed it
	dir  int            // +1: it comes up into place, over the mark; -1: down, under it
}

// placed takes note of where the system placed p, the mark's flyout,
// which it does again as p changes size.
func (f *float) placed(p *mygo.Window) {
	if !f.rise {
		return
	}
	f.x, f.y = p.Position()
	f.dir = 1
	if f.icon.Height > 0 && f.y >= f.icon.Y+f.icon.Height {
		f.dir = -1
	}
}

// lift moves p to where it shows at opacity o.
func (f *float) lift(p *mygo.Window, o float64) {
	if !f.rise {
		return
	}
	off := int((1-o)*8 + 0.5)
	p.SetPosition(f.x, f.y+f.dir*off)
}

// taskbarGap is the gap from the mark to a flyout over it (or under it,
// on a taskbar at the top) that leaves want between the flyout and the
// taskbar: the icon sits in the taskbar, some way in from its edge. The
// taskbar is what the work area of the display with the icon leaves out.
func taskbarGap(icon mygo.Rectangle, displays []mygo.Display, want int) int {
	if icon.Width <= 0 || icon.Height <= 0 {
		return want
	}
	cx, cy := icon.X+icon.Width/2, icon.Y+icon.Height/2
	for _, d := range displays {
		b, wa := d.Bounds, d.WorkArea
		if cx < b.X || cx >= b.X+b.Width || cy < b.Y || cy >= b.Y+b.Height {
			continue
		}
		switch {
		case icon.Y >= wa.Y+wa.Height:
			return want + icon.Y - (wa.Y + wa.Height)
		case icon.Y+icon.Height <= wa.Y:
			return want + wa.Y - (icon.Y + icon.Height)
		}
		return want
	}
	return want
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

// trayIconFor returns the mark of state st as the bar of system goos shows
// it, dark telling whether a Windows taskbar is.
//
//   - macOS: the marks are templates, black on transparent, which macOS
//     tints to its menu bar.
//   - Windows: the taskbar's ink, near black on a light taskbar and white
//     on a dark one (DESIGN §7.2), 16 points: the 18 of the menu bar's
//     marks cut to the 16 the star is drawn in, so that the system, which
//     shows the icon at 16, keeps the star the size it is on the Mac.
//   - Linux: a panel shows an AppIndicator's icon as it is, with no
//     template, and MyGo gives it no icon of the theme's to follow either.
//     The mark is white, for GNOME's top bar, dark in the light style and in
//     the dark one; cut to 16 points as Windows', which GNOME shows its
//     indicators at.
func trayIconFor(goos, st string, dark bool) []byte {
	switch goos {
	case "linux":
		return trayIconsWhite[st]
	case "windows":
		if dark {
			return trayIconsWhite[st]
		}
		return trayIconsInk[st]
	}
	return trayIcons[st]
}

// taskbarInk is the ink of a light Windows taskbar's icons and text.
var taskbarInk = color.NRGBA{R: 0x1b, G: 0x1b, B: 0x1b}

// trayIconsWhite and trayIconsInk are trayIcons in white and in a light
// taskbar's ink: the same marks, the user's (design/brand/final/tray), with
// the same transparency, cut to the 16 points the star is drawn in.
var (
	trayIconsWhite = tinted(trayIcons, color.NRGBA{R: 255, G: 255, B: 255}, trayInset)
	trayIconsInk   = tinted(trayIcons, taskbarInk, trayInset)
)

// trayInset is what the menu bar's marks, 18 points of 2 pixels, have
// around the 16 the star is drawn in.
const trayInset = 2

// tinted returns the marks in col, each cut by inset pixels on every side.
func tinted(marks map[string][]byte, col color.NRGBA, inset int) map[string][]byte {
	out := map[string][]byte{}
	for st, b := range marks {
		w, err := tint(b, col, inset)
		if err != nil {
			panic(err)
		}
		out[st] = w
	}
	return out
}

// tint returns the PNG image b in col, each pixel keeping its alpha, cut
// by inset pixels on every side.
func tint(b []byte, col color.NRGBA, inset int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r := img.Bounds().Inset(inset)
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := col
			c.A = color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA).A
			out.SetNRGBA(x-r.Min.X, y-r.Min.Y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
