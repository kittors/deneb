package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// palette holds the design's colors (design/DESIGN.md §3.3). The client
// keeps its own palette instead of the desktop's accent color so that it
// looks the same on macOS, Windows and Linux (§8).
type palette struct {
	dark     bool
	bg       ui.Color // window and cards
	well     ui.Color // sidebar, groups inside cards
	well2    ui.Color // hover on the well
	press    ui.Color // a whole row pressed (a menu's): the well 8% toward the ink
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
	press:    ui.Hex("#e2e2e4"),
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
	press:    ui.Hex("#2b2b2e"),
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

// The design's scale (DESIGN §3.9, AGENTS.md §2), in points: the sizes of
// type with their line heights, the spacing and the radii. The desktop
// draws with the smaller ones; type of 40 and up is the website's
// headlines only. web/tokens/gen writes them into web/tokens.css as
// --type-<n> and --leading-<n>, --space-<n> and --radius-<n>.
var (
	typeScale   = [...]float32{12, 13, 15, 20, 28, 40, 56, 80}
	typeLeading = [...]float32{1.5, 1.5, 1.7, 1.4, 1.3, 1.2, 1.15, 1.1}
	spaceScale  = [...]float32{4, 8, 12, 16, 24, 32, 48, 64, 96, 128, 160}
	radiusScale = [...]float32{4, 8, 12, 16, 20, 24, 28}
)

// Shadows (DESIGN §3.9), two soft layers each: the y offset, blur and
// strength of one layer, then of the other. The strengths are the light
// palette's; the dark one's shadow color, much stronger, takes them up in
// proportion (web/tokens/gen).
var (
	shadowSmall  = [...]float32{1, 2, 0.06, 1, 1, 0.04}
	shadowMedium = [...]float32{8, 24, 0.08, 2, 6, 0.05}
	shadowLarge  = [...]float32{32, 80, 0.14, 8, 24, 0.06}
)

// The motion beside the springs (DESIGN §5): a fade, and the step between
// things that come one after another. web/tokens/gen writes them, and the
// waiting and pressing below, with the springs into web/tokens.css:
// --motion-<name> and --ease-<spring>.
var (
	fadeTime    = 160 * time.Millisecond
	staggerTime = 70 * time.Millisecond
)

// Waiting and pressing (DESIGN §5, design/components.md):
//   - busyDelay: work done sooner says nothing; past it, the place that was
//     pressed says it is under way, with a ring;
//   - busyHold: once shown, that stays at least so long, not to flash;
//   - spinTime: one turn of the ring;
//   - swapOut, swapIn: one thing at a time (swap.go), the shown one fading
//     out before the next fades in;
//   - pressGive: how far a pressed button gives (its scale). A whole row,
//     a menu's, does not give: its ground darkens to the palette's press.
const (
	busyDelay = 200 * time.Millisecond
	busyHold  = 400 * time.Millisecond
	spinTime  = time.Second
	swapOut   = 90 * time.Millisecond
	swapIn    = 160 * time.Millisecond
	pressGive = float32(0.96)
)

// foldTime is a group of the menu bar's panel opening out or folding away
// by its height (menubar.go's fold); the panel's waiting ring, coming over
// groups that fold away, waits as long for them to. Not busyDelay: this
// waits for a motion to end, not for work. The desktop's own: the web has
// no such panel.
var foldTime = 220 * time.Millisecond

// The website's widths (DESIGN §14.16): its words, its pictures, the docs'
// contents beside them, and the window at its own size, as the first
// screen shows it. web/tokens/gen writes them as --width-<name>.
var siteWidth = struct{ Text, Content, Toc, Window float32 }{Text: 680, Content: 980, Toc: 224, Window: 1180}

// A frame, at sixty a second (DESIGN §5): what fades out is kept a frame
// longer than its fade, so that the frame showing it clear is drawn before
// it is let go; let go sooner, a frame a little late cuts its fade short.
const aFrame = time.Second / 60

// How far new words come down into their place as they fade in, taking
// over from the words before (the line under a LAN's title, DESIGN §6.5):
// enough to read as new words, too little to read as moving.
const wordsDrop = 3
