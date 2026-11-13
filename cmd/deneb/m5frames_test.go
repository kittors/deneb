package main

import (
	"context"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// M5 in the demo (DESIGN §7.2, §6.13): Deneb's window on Windows and on
// Linux, the window buttons at the top right over a strip of their own;
// the star in the Windows notification area and its panel; the star in
// the Ubuntu top bar and its menu; and, on the Mac, the notice of the
// local network permission. The window buttons, the taskbar, the top bar
// and the system's menu are drawn as the systems draw them, only to see
// where Deneb's parts sit: in the product they are the systems' own.

const (
	deskW, deskH = 1440, 900
	winW, winH   = 1180, 740
	winTitleH    = 32 // Windows' title bar, as MyGo draws its buttons
	gtkTitleH    = 40 // Linux's: the GTK buttons are centered in it
	taskbarH     = 48
	topbarH      = 32
)

// winBar and gtkBar are the room the window buttons take, as MyGo tells
// the view (c.TitleBar): three of 46 on Windows; on Ubuntu three round
// ones of 24, 8 apart, 10 from the edge.
var (
	winBar = ui.TitleBar{Height: winTitleH, Right: 3 * 46}
	gtkBar = ui.TitleBar{Height: gtkTitleH, Right: 3*24 + 2*8 + 10 + 6}
)

// winButtons draws the window buttons as Windows 11 draws its own: at the
// top right, 46 wide and as tall as the title bar, glyphs of hairlines;
// the pointer on one tints it, red under close.
func winButtons(c *ui.Context, p *palette, bar ui.TitleBar) {
	ink := ui.Hex("#1a1a1a")
	hover := ui.RGBA(0, 0, 0, 0.06)
	if p.dark {
		ink, hover = white, ui.RGBA(255, 255, 255, 0.08)
	}
	ui.Row(c.Key("win-buttons")).Absolute().Top(0).Right(0).Height(bar.Height).Children(func() {
		for _, kind := range []string{"min", "max", "close"} {
			kind := kind
			b := ui.ButtonBase(c.Key("win-"+kind)).Size(46, bar.Height).FocusRing(false).Label(map[string]string{"min": "最小化", "max": "最大化", "close": "关闭窗口"}[kind])
			col := ink
			if b.Hovered() {
				if kind == "close" {
					b.Background(ui.Hex("#c42b1c"))
					col = white
				} else {
					b.Background(hover)
				}
			}
			b.Transition(ui.ElementTransition{Duration: 100 * time.Millisecond, Colors: true})
			b.Draw(func(pt *ui.Painter, r ui.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				var path ui.Path
				switch kind {
				case "min":
					path.MoveTo(cx-5, cy+0.5)
					path.LineTo(cx+5, cy+0.5)
				case "max":
					path.MoveTo(cx-5, cy-5).LineTo(cx+5, cy-5).LineTo(cx+5, cy+5).LineTo(cx-5, cy+5).Close()
				default:
					path.MoveTo(cx-5, cy-5)
					path.LineTo(cx+5, cy+5)
					path.MoveTo(cx+5, cy-5)
					path.LineTo(cx-5, cy+5)
				}
				pt.StrokePath(&path, 1, col)
			})
		}
	})
}

// gtkButtons draws the window buttons as GTK draws them on Ubuntu: three
// round ones at the right, centered in the title bar.
func gtkButtons(c *ui.Context, p *palette, bar ui.TitleBar) {
	ink, well, hover := ui.Hex("#3d3d3d"), ui.RGBA(0, 0, 0, 0.07), ui.RGBA(0, 0, 0, 0.14)
	if p.dark {
		ink, well, hover = ui.Hex("#eeeeee"), ui.RGBA(255, 255, 255, 0.1), ui.RGBA(255, 255, 255, 0.18)
	}
	ui.Row(c.Key("gtk-buttons")).Absolute().Top(0).Right(10).Height(bar.Height).Gap(8).AlignItems(ui.Center).Children(func() {
		for _, kind := range []string{"min", "max", "close"} {
			kind := kind
			b := ui.ButtonBase(c.Key("gtk-"+kind)).Size(24, 24).Radius(999).Background(well).FocusRing(false).Label(map[string]string{"min": "最小化", "max": "最大化", "close": "关闭窗口"}[kind])
			if b.Hovered() {
				b.Background(hover)
			}
			b.Transition(ui.ElementTransition{Duration: 100 * time.Millisecond, Colors: true})
			b.Draw(func(pt *ui.Painter, r ui.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				var path ui.Path
				switch kind {
				case "min":
					path.MoveTo(cx-4, cy+2.5)
					path.LineTo(cx+4, cy+2.5)
				case "max":
					path.MoveTo(cx-3.5, cy-3.5).LineTo(cx+3.5, cy-3.5).LineTo(cx+3.5, cy+3.5).LineTo(cx-3.5, cy+3.5).Close()
				default:
					path.MoveTo(cx-3.5, cy-3.5)
					path.LineTo(cx+3.5, cy+3.5)
					path.MoveTo(cx+3.5, cy-3.5)
					path.LineTo(cx-3.5, cy+3.5)
				}
				pt.StrokePath(&path, 1.5, ink)
			})
		}
	})
}

// deskScreen is a Windows or an Ubuntu screen as the recordings show it:
// the wallpaper, Deneb's window on it (a picture of the window's own
// frames, with the system's corners and shadow), and the taskbar with the
// notification area at the bottom, or the top bar at the top, Deneb's star
// in it; over them the tray's panel (Windows) or the system's menu
// (Linux), and the tip the first close shows.
type deskScreen struct {
	a        *app
	os       string // "windows" or "linux"
	win      *ui.Bitmap
	shown    bool // the window shows
	open     bool // the panel, or the menu
	tip      bool // the first close's tip
	tipUntil time.Time
	icon     ui.Rect // where the star is
}

// screenInk is the ink of the bar the star sits on: dark on Windows' light
// taskbar, white on its dark one and on Ubuntu's black top bar.
func (s *deskScreen) screenInk(p *palette) ui.Color {
	if s.os == "windows" && !p.dark {
		return ui.Hex("#1b1b1b")
	}
	return white
}

// trayMarkImages are the bar's colour marks, decoded once.
var trayMarkImages = map[string]ui.ImageSource{}

// trayMarkImage returns the colour mark of state st as system goos shows it
// on a dark or light bar, at 2x.
func trayMarkImage(goos, st string, dark bool) ui.ImageSource {
	k := fmt.Sprint(goos, st, dark)
	if img, ok := trayMarkImages[k]; ok {
		return img
	}
	img, err := ui.DecodeImage(trayIconFor(goos, st, dark, 2))
	if err != nil {
		panic(err)
	}
	trayMarkImages[k] = img
	return img
}

func (s *deskScreen) view(c *ui.Context) {
	a := s.a
	a.drain(c)
	a.sync(nil)
	p := colors(c)
	now := c.Now()
	wall := []ui.GradientStop{{Color: ui.Hex("#dbe5f3"), At: 0}, {Color: ui.Hex("#b4c7e3"), At: 1}}
	if s.os == "linux" {
		wall = []ui.GradientStop{{Color: ui.Hex("#e9e2ee"), At: 0}, {Color: ui.Hex("#cfc1dc"), At: 1}}
	}
	if p.dark {
		wall = []ui.GradientStop{{Color: ui.Hex("#0f1a2c"), At: 0}, {Color: ui.Hex("#1c2d4b"), At: 1}}
		if s.os == "linux" {
			wall = []ui.GradientStop{{Color: ui.Hex("#22132a"), At: 0}, {Color: ui.Hex("#3a2244"), At: 1}}
		}
	}
	root := ui.Box(c).Fill().LinearGradient(ui.LinearGradient{Angle: 160, Stops: wall})
	root.Children(func() {
		radius, x, y := float32(8), float32(110), float32(56)
		if s.os == "linux" {
			// Square: GTK3 decorates MyGo's window with its shadow, but
			// does not round what it draws (DESIGN §7.2).
			radius, x, y = 0, 130, topbarH+40
		}
		holder := ui.Box(c.Key("win-holder")).Absolute().Left(x).Top(y).Size(winW, winH).PassThrough()
		holder.Children(func() {
			if !s.shown || s.win == nil {
				return
			}
			w := ui.Box(c.Key("window")).Size(winW, winH).Radius(radius).Clip().
				Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.12))
			if p.dark {
				w.Border(1, ui.RGBA(255, 255, 255, 0.08))
			}
			// Hidden and shown as the systems do: a little smaller, faded.
			w.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{Scale: 0.96}, Exit: &ui.Motion{Scale: 0.96}})
			w.Children(func() { ui.Image(c, s.win).Size(winW, winH) })
		})
		// A click anywhere else, the window too, puts the panel or the menu
		// away, as the systems do.
		if s.open && ui.Box(c.Key("desk")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Clicked() {
			s.open = false
			a.panelShown = false
		}
		if s.os == "windows" {
			s.taskbar(c, p, now)
		} else {
			s.topbar(c, p, now)
		}
	})
}

// star is Deneb's mark in the bar, a button: the pointer on it lights it
// up; a click does what the system lets it. The mark is the one the system
// gets (trayIconFor): in colour, the taskbar's mode's, the 32 pixels of a
// 2x display at 16 points, as the frames are drawn.
func (s *deskScreen) star(c *ui.Context, p *palette, size float32) {
	ink := s.screenInk(p)
	b := ui.ButtonBase(c.Key("star")).Size(size, size).Radius(4).Center().FocusRing(false).Label("Deneb")
	if b.Hovered() || s.open {
		b.Background(ink.Alpha(0.1))
	}
	b.Transition(ui.ElementTransition{Duration: 100 * time.Millisecond, Colors: true})
	mark := trayMarkImage(s.os, trayState(s.a.snap, c.Now()), p.dark)
	b.Children(func() { ui.Image(c, mark).Size(16, 16) })
	if r := b.Bounds(); r.W > 0 {
		s.icon = r
	}
	if b.Clicked() {
		s.click(c)
	}
}

// click is a click on the star: on Windows the panel opens or goes; on
// Linux the system's menu, at once: GNOME's AppIndicator extension opens it
// on every click and hands Deneb none, so a double click does nothing more
// (DESIGN §7.2).
func (s *deskScreen) click(c *ui.Context) {
	s.open = !s.open
	if s.os == "windows" {
		s.tip = false
		s.a.panelShown = s.open
	}
}

// taskbar is Windows 11's taskbar: a few pinned apps in the middle, and at
// the right the notification area, the clock and Deneb's star.
func (s *deskScreen) taskbar(c *ui.Context, p *palette, now time.Time) {
	bg, line, ink := ui.RGBA(243, 243, 243, 0.94), ui.RGBA(0, 0, 0, 0.08), ui.Hex("#1b1b1b")
	if p.dark {
		bg, line, ink = ui.RGBA(32, 32, 32, 0.94), ui.RGBA(255, 255, 255, 0.06), white
	}
	bar := ui.Row(c.Key("taskbar")).Absolute().Left(0).Right(0).Bottom(0).Height(taskbarH).Background(bg).Border(0, line).
		Padding(0, 12).AlignItems(ui.Center)
	bar.Children(func() {
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(1).Background(line)
		ui.Spacer(c)
		ui.Row(c).Gap(6).Children(func() {
			for i, col := range []string{"#3b82f6", "#f59e0b", "#10b981", "#8b5cf6"} {
				t := ui.Box(c.Key(fmt.Sprint("app-", i))).Size(40, 40).Radius(6).Center()
				t.Children(func() { ui.Box(c).Size(22, 22).Radius(5).Background(ui.Hex(col).Alpha(0.85)) })
			}
		})
		ui.Spacer(c)
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("overflow")).Size(28, 40).Center().Children(func() {
				ui.Icon(c, uiGlyphs["chevron"]).Size(12, 12).TextColor(ink.Alpha(0.85)).Rotate(-90)
			})
			s.star(c, p, 32)
			ui.Box(c).Size(6, 1)
			ui.Row(c).Height(40).Padding(0, 8).Gap(10).AlignItems(ui.Center).Radius(4).Children(func() {
				ui.Icon(c, uiGlyphs["globe"]).Size(16, 16).TextColor(ink.Alpha(0.9))
				ui.Icon(c, uiGlyphs["bolt"]).Size(16, 16).TextColor(ink.Alpha(0.9))
			})
			ui.Column(c).Padding(0, 10).AlignItems(ui.End).Children(func() {
				ui.Text(c, "20:41").FontSize(12).TextColor(ink).FontFeatures("tnum")
				ui.Text(c, "2026/11/9").FontSize(12).TextColor(ink).FontFeatures("tnum")
			})
		})
	})
	// The panel above the star, 12 above the taskbar, its right edge where
	// the screen lets it be; Windows rounds it, outlines it, shadows it.
	x := min(max(s.icon.X+s.icon.W/2-panelWidth/2, 12), deskW-12-panelWidth)
	holder := ui.Column(c.Key("flyout-holder")).Absolute().Left(x).Bottom(taskbarH + 12).PassThrough()
	holder.Children(func() {
		if s.open {
			f := ui.Column(c.Key("flyout")).Radius(8).Clip().Border(1, p.line).
				Shadow(0, 16, 36, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.1))
			f.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 8}})
			f.Children(func() { s.a.panelBody(c, p) })
		}
	})
	// The first close's tip, above the star, in the panel's look.
	tx := min(max(s.icon.X+s.icon.W/2-150, 12), deskW-12-300)
	tips := ui.Column(c.Key("tip-holder")).Absolute().Left(tx).Bottom(taskbarH + 12).PassThrough()
	tips.Children(func() {
		if !s.tip || !now.Before(s.tipUntil) {
			if s.tip {
				s.tip = false
			}
			return
		}
		c.After(s.tipUntil.Sub(now))
		t := ui.Row(c.Key("tip")).Width(300).Padding(14, 16).Gap(12).AlignItems(ui.Start).Background(p.bg).Radius(8).Border(1, p.line).
			Shadow(0, 16, 36, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.1))
		t.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 8}})
		t.Children(func() {
			ui.Box(c).Size(28, 28).Radius(999).Background(p.well).Center().Children(func() { trayGlyph(c, "solid", p.ink) })
			ui.Text(c, "Deneb 还在这儿，连接照常。点这颗星就能打开窗口").FontSize(13).LineHeight(1.5).Grow(1).Shrink(1)
		})
	})
}

// topbar is Ubuntu's top bar: the workspaces at the left, the clock in the
// middle, at the right Deneb's star (an AppIndicator) and the system's
// indicators; under the star, its menu.
func (s *deskScreen) topbar(c *ui.Context, p *palette, now time.Time) {
	a := s.a
	bar := ui.Row(c.Key("topbar")).Absolute().Left(0).Right(0).Top(0).Height(topbarH).Background(ui.RGBA(0, 0, 0, 0.94)).
		Padding(0, 10).AlignItems(ui.Center)
	bar.Children(func() {
		ui.Row(c).Height(22).Padding(0, 10).Radius(999).Gap(5).AlignItems(ui.Center).Background(white.Alpha(0.12)).Children(func() {
			ui.Box(c).Size(18, 7).Radius(999).Background(white)
			ui.Box(c).Size(7, 7).Radius(999).Background(white.Alpha(0.5))
		})
		ui.Spacer(c)
		ui.Text(c, "11月9日 20:41").FontSize(13).FontWeight(600).TextColor(white).FontFeatures("tnum")
		ui.Spacer(c)
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			s.star(c, p, 28)
			ui.Row(c).Height(26).Padding(0, 10).Gap(10).Radius(999).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, uiGlyphs["globe"]).Size(15, 15).TextColor(white)
				ui.Icon(c, uiGlyphs["bolt"]).Size(15, 15).TextColor(white)
			})
		})
	})
	// The menu: the system's (GNOME Shell's), with Deneb's items in it.
	x := min(max(s.icon.X+s.icon.W/2-150, 8), deskW-8-300)
	holder := ui.Column(c.Key("menu-holder")).Absolute().Left(x).Top(topbarH + 4).PassThrough()
	holder.Children(func() {
		if !s.open {
			return
		}
		bg, ink, dim, hi, sep := ui.Hex("#fafafa"), ui.Hex("#2e2e2e"), ui.Hex("#2e2e2e").Alpha(0.55), ui.RGBA(0, 0, 0, 0.07), ui.RGBA(0, 0, 0, 0.1)
		if p.dark {
			bg, ink, dim, hi, sep = ui.Hex("#2f2f2f"), ui.Hex("#ffffff"), white.Alpha(0.55), ui.RGBA(255, 255, 255, 0.1), ui.RGBA(255, 255, 255, 0.1)
		}
		m := ui.Column(c.Key("menu")).Width(300).Padding(6).Radius(12).Background(bg).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.12))
		if p.dark {
			m.Border(1, ui.RGBA(255, 255, 255, 0.08))
		}
		m.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Enter: &ui.Motion{Scale: 0.96, Y: -4}, Exit: &ui.Motion{}})
		m.Children(func() {
			item := func(key, text string, check, on bool, do func()) {
				r := ui.ButtonBase(c.Key("menu-"+key)).Height(34).Padding(0, 10).Gap(8).Radius(8).AlignItems(ui.Center).FocusRing(false).Label(text)
				if do == nil {
					r.Children(func() { ui.Text(c, text).FontSize(13).TextColor(dim).SingleLine().Grow(1) })
					return
				}
				if r.Hovered() {
					r.Background(hi)
				}
				r.Transition(ui.ElementTransition{Duration: 100 * time.Millisecond, Colors: true})
				r.Children(func() {
					if check {
						tick := ui.Box(c).Size(14, 14)
						if on {
							tick.Draw(func(pt *ui.Painter, r ui.Rect) {
								var path ui.Path
								path.MoveTo(r.X+2.5, r.Y+7.5).LineTo(r.X+5.8, r.Y+10.6).LineTo(r.X+11.5, r.Y+3.8)
								path.Cap(ui.RoundCap).Join(ui.RoundJoin)
								pt.StrokePath(&path, 1.8, ink)
							})
						}
					}
					ui.Text(c, text).FontSize(13).TextColor(ink).SingleLine().Grow(1).Shrink(1)
				})
				if r.Clicked() {
					do()
					s.open = false
				}
			}
			rule := func() { ui.Box(c).Height(1).Margin(5, 8, 5, 8).Background(sep) }
			_, line := a.panelSummary(p)
			item("status", line, false, false, nil)
			if ch := choresOf(a.snap, now); len(ch) > 0 {
				rule()
				for i, x := range ch[:min(3, len(ch))] {
					item(fmt.Sprint("chore-", i), x.text+"…", false, false, func() { s.shown = true })
				}
			}
			rule()
			for _, l := range a.snap.LANs {
				if l.Waiting || !l.Has(a.snap.Self) {
					continue
				}
				lan, paused := l.ID, l.Paused
				item("lan-"+string(lan), l.Name+" · "+a.lanLine(l), true, !paused, func() {
					a.post(func(c *ui.Context) { a.pause(c, lan, !paused) })
				})
			}
			rule()
			item("open", "打开 Deneb", false, false, func() { s.shown = true })
		})
	})
}

// deskRig records a deskScreen: each frame the window draws its own frame
// first, which the screen shows as a picture of it.
type deskRig struct {
	t          *testing.T
	a          *app
	d          *client.Demo
	app, desk  *ui.Tester
	s          *deskScreen
	dir, still string
	n          int
	px, py     float32
}

func newDeskRig(t *testing.T, os string, dark bool) *deskRig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	d.Simulate("m3")
	d.Simulate("m4")
	m5World(d, os)
	a.platform = os
	bar, chrome := winBar, winButtons
	if os == "linux" {
		bar, chrome = gtkBar, gtkButtons
	}
	a.chrome = chrome
	r := &deskRig{t: t, a: a, d: d, s: &deskScreen{a: a, os: os, shown: true}}
	// "打开 Deneb": the window comes up, and the panel, which loses the
	// keyboard to it, goes.
	a.showMain = func() { r.s.shown, r.s.open, a.panelShown = true, false, false }
	r.app = ui.NewTester(a.view, winW, winH)
	r.app.SetTitleBar(bar)
	r.app.SetDark(dark)
	r.desk = ui.NewTester(r.s.view, deskW, deskH)
	r.desk.SetDark(dark)
	a.selected = ""
	r.run(900 * time.Millisecond)
	return r
}

func (r *deskRig) run(dt time.Duration) {
	for end := r.desk.Now().Add(dt); r.desk.Now().Before(end); {
		r.app.Advance(time.Second / 60)
		r.app.Frame()
		r.s.win = ui.NewBitmap(r.app.Image())
		r.desk.Advance(time.Second / 60)
		r.desk.Frame()
		if r.dir != "" {
			f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%04d.jpg", r.n)))
			if err != nil {
				r.t.Fatal(err)
			}
			jpeg.Encode(f, r.desk.Image(), &jpeg.Options{Quality: 88})
			f.Close()
			r.n++
		}
	}
}

func (r *deskRig) shot(state string) {
	if r.dir == "" {
		return
	}
	r.app.SetScale(2)
	r.app.Frame()
	r.s.win = ui.NewBitmap(r.app.Image())
	r.desk.SetScale(2)
	r.desk.Frame()
	f, err := os.Create(r.still + "_" + state + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	png.Encode(f, r.desk.Image())
	f.Close()
	r.app.SetScale(1)
	r.desk.SetScale(1)
	r.app.Frame()
	r.s.win = ui.NewBitmap(r.app.Image())
	r.desk.Frame()
}

// glide moves the pointer to to, as a hand does.
func (r *deskRig) glide(to ui.Point, steps int) {
	from := ui.Point{X: r.px, Y: r.py}
	for i := 1; i <= steps; i++ {
		k := ui.EaseInOut(float32(i) / float32(steps))
		r.px, r.py = from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k
		r.desk.Move(r.px, r.py)
		r.run(time.Second / 60)
	}
}

// tap clicks at to, the pointer gliding there first.
func (r *deskRig) tap(to ui.Point) {
	r.glide(to, 22)
	r.run(150 * time.Millisecond)
	r.desk.Press(r.px, r.py)
	r.run(70 * time.Millisecond)
	r.desk.Release(r.px, r.py)
}

func (r *deskRig) star() ui.Point {
	return ui.Point{X: r.s.icon.X + r.s.icon.W/2, Y: r.s.icon.Y + r.s.icon.H/2}
}

func (r *deskRig) text(s string) ui.Point {
	r.t.Helper()
	b, ok := r.desk.Find(s)
	if !ok {
		r.t.Fatalf("nothing shows %q: %q", s, r.desk.Texts())
	}
	return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
}

// TestM5Frames records M5 in the demo, both themes, when DENEB_M5 names a
// folder: each scene's frames, a 60th of a second apart, in
// <theme>-<scene>/, and its stills beside them.
func TestM5Frames(t *testing.T) {
	root := os.Getenv("DENEB_M5")
	if root == "" {
		t.Skip("set DENEB_M5 to a folder to record M5's demo")
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("DENEB_M5_ONLY"), ",") {
		if k != "" {
			only[k] = true
		}
	}
	type scene struct {
		key  string
		play func(t *testing.T, root, theme string, dark bool)
	}
	scenes := []scene{
		{"win-window", func(t *testing.T, root, theme string, dark bool) { playWindow(t, root, theme, dark, "windows") }},
		{"win-tray", playWinTray},
		{"linux-window", func(t *testing.T, root, theme string, dark bool) { playWindow(t, root, theme, dark, "linux") }},
		{"linux-tray", playLinuxTray},
		{"local-network", playLocalNetwork},
	}
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			if len(only) > 0 && !only[sc.key] {
				continue
			}
			sc.play(t, root, theme, dark)
		}
	}
}

// m5Window is the window alone, as M1–M4's scenes record it, with the
// system's window buttons where the platform has them.
func m5Window(t *testing.T, root, theme string, dark bool, os, key string) *m3rig {
	r := newM4Rig(t)
	m5World(r.d, os)
	r.a.platform = os
	switch os {
	case "windows":
		r.tt.SetTitleBar(winBar)
		r.a.chrome = winButtons
	case "linux":
		r.tt.SetTitleBar(gtkBar)
		r.a.chrome = gtkButtons
	}
	r.tt.SetDark(dark)
	r.dir = filepath.Join(root, theme+"-"+key)
	r.still = r.dir
	reset(t, r.dir)
	r.run(600 * time.Millisecond)
	return r
}

// m5World is the demo's world as M5's scenes have it: no device at home
// relays (after the public beta, the user 11-09), my server 公司机房 does;
// on Windows and Linux, this computer is a desktop there, not the MacBook.
func m5World(d *client.Demo, os string) {
	d.Simulate("m5")
	if os == "windows" || os == "linux" {
		ctx := context.Background()
		d.Do(ctx, model.Op{Kind: model.OpDeviceRename, Device: "mac", Name: "kittors 的台式机"})
		d.Do(ctx, model.Op{Kind: model.OpDeviceSetKind, Device: "mac", DevKind: "desktop"})
	}
}

func reset(t *testing.T, dir string) {
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Scenes M5-01 and M5-03: the window on Windows, then on Linux. The window
// buttons at the top right over a strip of their own, the header under it;
// the pointer on them (on Windows, close turns red); the canvas as on the
// Mac: a device moves, its card opens and goes.
func playWindow(t *testing.T, root, theme string, dark bool, os string) {
	key := map[string]string{"windows": "win-window", "linux": "linux-window"}[os]
	r := m5Window(t, root, theme, dark, os, key)
	r.run(700 * time.Millisecond)
	r.shot("窗口")
	for _, b := range []string{"最小化", "最大化", "关闭窗口"} {
		r.glide(r.text(b), 18)
		r.run(450 * time.Millisecond)
		if b == "关闭窗口" {
			r.shot("指着关闭")
		}
	}
	// Along the strip, clear of the header's buttons, then down.
	r.glide(at(700, 16), 20)
	r.glide(at(700, 300), 16)
	nas := r.tile("jnas")
	r.drag(r.device("jnas"), at(nas.X-120, nas.Y+30), 40)
	r.run(500 * time.Millisecond)
	r.tap(r.device("jdesk"))
	r.run(1300 * time.Millisecond)
	r.shot("卡片")
	r.tap(r.text("关闭"))
	r.run(900 * time.Millisecond)
}

// Scene M5-02: the star in Windows' notification area, its dot telling of
// a device that waits for me. A click opens the panel over it: letting the
// device in there takes the dot away; the window's switch and the panel's
// are one; a click on the desktop puts it away. Closed for the
// first time, the window goes to the notification area, and a tip over
// the star says where Deneb is; a click on the star opens the panel, whose
// "打开 Deneb" brings the window back.
func playWinTray(t *testing.T, root, theme string, dark bool) {
	r := newDeskRig(t, "windows", dark)
	// Something waits for me: the star has its dot.
	r.d.Simulate("pending-device")
	r.dir = filepath.Join(root, theme+"-win-tray")
	r.still = r.dir
	reset(t, r.dir)
	r.px, r.py = 900, 500
	r.run(600 * time.Millisecond)
	r.shot("通知区")
	r.tap(r.star())
	r.run(1200 * time.Millisecond)
	r.shot("面板")
	r.tap(r.text("同意"))
	r.run(1300 * time.Millisecond)
	r.shot("面板里同意")
	r.run(3800 * time.Millisecond)
	r.tap(r.text("暂停工作室"))
	r.run(1500 * time.Millisecond)
	r.shot("面板里暂停")
	r.tap(r.text("暂停工作室"))
	r.run(1200 * time.Millisecond)
	r.tap(ui.Point{X: 700, Y: 300})
	r.run(900 * time.Millisecond)
	// Closed for the first time: the tip.
	r.s.shown, r.s.tip, r.s.tipUntil = false, true, r.desk.Now().Add(6*time.Second)
	r.run(1500 * time.Millisecond)
	r.shot("第一次关窗口")
	r.run(1500 * time.Millisecond)
	r.tap(r.star())
	r.run(1200 * time.Millisecond)
	r.tap(r.text("打开 Deneb"))
	r.run(1500 * time.Millisecond)
	r.shot("窗口回来")
	r.glide(ui.Point{X: 900, Y: 500}, 20)
	r.run(600 * time.Millisecond)
}

// Scene M5-04: the star in Ubuntu's top bar. A click opens the system's
// menu: how things are, the LANs with their ticks (one unticked pauses
// it), "打开 Deneb". The window closed, the menu's "打开 Deneb" brings it
// back: the star takes no double click (DESIGN §7.2).
func playLinuxTray(t *testing.T, root, theme string, dark bool) {
	r := newDeskRig(t, "linux", dark)
	// Something waits for me: the star has its dot, the menu a row.
	r.d.Simulate("pending-device")
	r.dir = filepath.Join(root, theme+"-linux-tray")
	r.still = r.dir
	reset(t, r.dir)
	r.px, r.py = 900, 500
	r.run(600 * time.Millisecond)
	r.shot("顶栏")
	r.tap(r.star())
	r.run(1300 * time.Millisecond)
	r.shot("菜单")
	r.tap(r.text("工作室 · 4 台都连上了"))
	r.run(1500 * time.Millisecond)
	r.shot("菜单里暂停")
	r.tap(r.star())
	r.run(1200 * time.Millisecond)
	r.shot("菜单里看到已暂停")
	r.tap(r.text("工作室 · 已暂停"))
	r.run(1200 * time.Millisecond)
	// The window closed: the menu's "打开 Deneb" brings it back.
	r.s.shown = false
	r.run(900 * time.Millisecond)
	r.tap(r.star())
	r.run(1200 * time.Millisecond)
	r.shot("窗口关着时的菜单")
	r.tap(r.text("打开 Deneb"))
	r.run(1300 * time.Millisecond)
	r.shot("菜单里打开窗口")
	r.glide(ui.Point{X: 900, Y: 500}, 20)
	r.run(600 * time.Millisecond)
}

// Scene M5-05: on the Mac, the engine finds it may not reach the devices
// behind this router: 我的游戏主机 goes the long way round, and the amber
// pill says why; its card says what it means, and what will fix it;
// "知道了" puts it away. Then it comes back, and goes by itself as the
// engine reaches them again.
func playLocalNetwork(t *testing.T, root, theme string, dark bool) {
	r := m5Window(t, root, theme, dark, "darwin", "local-network")
	r.run(600 * time.Millisecond)
	r.d.Simulate("local-network")
	r.run(1500 * time.Millisecond)
	r.shot("胶囊")
	r.tap(r.text("本地网络权限没拿到"))
	r.run(1400 * time.Millisecond)
	r.shot("卡片")
	r.tap(r.text("知道了"))
	r.run(1200 * time.Millisecond)
	r.shot("知道了")
	// Another time: it goes by itself once the engine reaches them.
	r.d.Simulate("local-network")
	r.run(900 * time.Millisecond)
	delete(r.a.noticesDone, model.LocalNetwork.Code)
	r.d.Simulate("local-network")
	r.run(2000 * time.Millisecond)
	r.d.Simulate("local-network")
	r.run(1500 * time.Millisecond)
	r.shot("自己好了")
	r.park()
	r.run(600 * time.Millisecond)
}
