package main

import "github.com/egoist/mygo/ui"

// palette holds the design's colors (design/DESIGN.md §3.3). The client
// keeps its own palette instead of the desktop's accent color so that it
// looks the same on macOS, Windows and Linux (§8).
type palette struct {
	dark     bool
	bg       ui.Color // window and cards
	well     ui.Color // sidebar, groups inside cards
	well2    ui.Color // hover on the well
	line     ui.Color // hairlines
	line2    ui.Color // switch track when off
	ink      ui.Color // text
	ink2     ui.Color // secondary text
	ink3     ui.Color // faint text
	primary  ui.Color
	primary2 ui.Color // primary, pressed or hovered
	ok       ui.Color
	track    ui.Color // the faint line under the flowing light
	dots     ui.Color // a link that cannot connect yet
	ballHi   ui.Color // relay ball, lit side
	ballMid  ui.Color
	ballLo   ui.Color
	shadow   ui.Color // card shadows
}

var lightPalette = palette{
	bg:       ui.Hex("#ffffff"),
	well:     ui.Hex("#f4f4f6"),
	well2:    ui.Hex("#ececef"),
	line:     ui.RGBA(9, 9, 11, 0.07),
	line2:    ui.RGBA(9, 9, 11, 0.1),
	ink:      ui.Hex("#18181b"),
	ink2:     ui.Hex("#71717a"),
	ink3:     ui.Hex("#a1a1aa"),
	primary:  ui.Hex("#2f6bff"),
	primary2: ui.Hex("#2457e0"),
	ok:       ui.Hex("#22c55e"),
	track:    ui.RGBA(9, 9, 11, 0.09),
	dots:     ui.RGBA(9, 9, 11, 0.32),
	ballHi:   ui.Hex("#ffffff"),
	ballMid:  ui.Hex("#f1f2f4"),
	ballLo:   ui.Hex("#d6d8de"),
	shadow:   ui.RGBA(9, 9, 11, 0.09),
}

var darkPalette = palette{
	dark:     true,
	bg:       ui.Hex("#111113"),
	well:     ui.Hex("#19191c"),
	well2:    ui.Hex("#222226"),
	line:     ui.RGBA(255, 255, 255, 0.07),
	line2:    ui.RGBA(255, 255, 255, 0.1),
	ink:      ui.Hex("#fafafa"),
	ink2:     ui.Hex("#a1a1aa"),
	ink3:     ui.Hex("#6b6b74"),
	primary:  ui.Hex("#4c82ff"),
	primary2: ui.Hex("#3b6ff0"),
	ok:       ui.Hex("#34d399"),
	track:    ui.RGBA(255, 255, 255, 0.085),
	dots:     ui.RGBA(255, 255, 255, 0.34),
	ballHi:   ui.Hex("#6a6a73"),
	ballMid:  ui.Hex("#44444b"),
	ballLo:   ui.Hex("#2b2b30"),
	shadow:   ui.RGBA(0, 0, 0, 0.5),
}

// colors returns the palette for the system's appearance, and themes the
// window's widgets with it. Called first in every frame: each frame starts
// from the desktop's theme, so a change of appearance shows at once.
func colors(c *ui.Context) *palette {
	p := &lightPalette
	if c.Theme().Dark {
		p = &darkPalette
	}
	t := *c.Theme()
	t.Background, t.Text, t.TextMuted = p.bg, p.ink, p.ink2
	t.Accent, t.AccentHover, t.AccentPressed = p.primary, p.primary2, p.primary2
	t.Inverse, t.InverseText = p.ink, p.bg
	t.FontSize = 13
	c.SetTheme(&t)
	return p
}

var white = ui.Hex("#ffffff")
var black = ui.Hex("#000000")
