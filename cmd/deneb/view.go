package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

const sidebarWidth = 232
const headerHeight = 76

// macTitleBar is the height of the title bar the traffic lights take in
// the Mac's window (c.TitleBar there): 32 on macOS 26, 28 before it.
const macTitleBar = 32

// view builds the window: the sidebar, the LAN's header, and the canvas
// with its card, tray and toast (design/DESIGN.md §7).
func (a *app) view(c *ui.Context) {
	// First what came from elsewhere: work done off the main thread, and
	// the store's snapshot, which the canvas is brought to.
	a.drain(c)
	a.sync(c)
	a.checkOnboard()
	a.tickAdapter(c)
	p := colors(c)
	if o := a.onboard; o != nil && o.leaving.IsZero() {
		// The first time's steps, the whole window (DESIGN §7.3).
		a.onboardView(c, p)
		return
	}
	switch {
	case !a.snap.Engine && a.snap.Version == 0, a.snap.LoggedIn && !a.snap.Loaded:
		a.startingView(c, p)
		return
	case !a.snap.LoggedIn && a.deleting == nil:
		// (The account just deleted: its page stays, scrolled where it was,
		// while the window fades to the sign-in, DESIGN §6.15.)
		a.loginView(c, p)
		return
	}
	bar := c.TitleBar()
	// Window buttons at the top right (Windows, Linux): the main column
	// leaves them a strip of their own, above the header, which moves down
	// by it (DESIGN §7.2). At the top left (macOS) they sit over the
	// sidebar, and nothing moves.
	a.strip = 0
	if bar.Right > 0 {
		a.strip = bar.Height
	}
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	for k, fn := range a.sims {
		if root.Shortcut(ui.Cmd|ui.Alt, k) {
			fn(c, a)
		}
	}
	root.Children(func() {
		a.sidebar(c, p, bar)
		ui.Column(c).Grow(1).Background(p.bg).Children(func() {
			if a.strip > 0 {
				ui.Box(c.Key("title-strip")).Height(a.strip).Shrink(0).DragWindow()
			}
			if a.all() {
				a.allHeader(c, p)
			} else {
				a.header(c, p)
			}
			a.canvas(c, p)
			a.inviteLayer(c, p)
		})
		a.personChip(c, p)
		a.accountLayer(c, p)
		// Deleting the account, over the window (DESIGN §6.15).
		a.accountDeleteLayer(c, p)
		if a.chrome != nil {
			// The window buttons as the system draws them, for the
			// recordings of Windows and Linux (DESIGN §7.2).
			a.chrome(c, p, bar)
		}
		if a.onboard != nil && !a.onboard.leaving.IsZero() {
			// The first time's steps fading away over the window. (Steps
			// set out this frame, as logging out does, show from the next.)
			a.onboardOver(c, p)
		}
		a.curtainView(c, p, false)
	})
}

// ball is a person: a glossy ball of their color with their initial.
func ball(c *ui.Context, who *person, size float32, off bool) ui.Element {
	k := float32(0)
	if off {
		k = 1
	}
	return ballOff(c, who, size, k)
}

// ballOff is a person's ball k of the way to refused: grey, its initial
// gone, and a slash across it, drawn out from its middle.
func ballOff(c *ui.Context, who *person, size, k float32) ui.Element {
	col := who.color.Mix(ui.Hex("#a1a1aa"), k)
	b := ui.Box(c).Size(size, size).Radius(999).Center().
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.34, Y: 0.28}, Oklab: true,
			Stops: []ui.GradientStop{{Color: col.Mix(white, 0.45), At: 0}, {Color: col, At: 0.52}, {Color: col.Mix(black, 0.18), At: 1}},
		}).
		InsetShadow(0, -1.5, 3, 0, ui.RGBA(0, 0, 0, 0.14)).
		Shadow(0, 2, 6, 0, col.Alpha(0.3))
	b.Children(func() {
		if who.ch != "" && size >= 20 && k < 1 {
			ui.Text(c, who.ch).FontSize(size * 0.4).FontWeight(600).TextColor(white.Alpha(1 - k)).SingleLine()
		}
		if k > 0 {
			l := size * 0.84 * k
			ui.Box(c).Absolute().Size(2, l).Top(size/2 - l/2).Left(size/2 - 1).Radius(1).
				Background(white).Transform(ui.Rotation(-45))
		}
	})
	return b
}

func (a *app) sidebar(c *ui.Context, p *palette, bar ui.TitleBar) {
	// Its top keeps clear of the window buttons over it (macOS). Where they
	// are at the top right instead (Windows, Linux), it starts where it does
	// on the Mac, under a title bar of the Mac's height: the same to the
	// pixel on every system (DESIGN §7.2, §8).
	h := bar.Height
	if bar.Right > 0 && bar.Left == 0 {
		h = macTitleBar
	}
	top := max(h, 16) + 6
	ui.Column(c).Width(sidebarWidth).Background(p.well).Padding(top, 12, 12, 12).Gap(2).DragWindow().Children(func() {
		ui.Row(c).Gap(8).Padding(6, 8, 18, 8).Children(func() {
			ui.Icon(c, starGlyph).Size(18, 18).TextColor(p.primary)
			ui.Text(c, "Deneb").FontSize(15).FontWeight(600)
		})
		group(c, p, "局域网")
		// Each of my LANs, with the balls of its first members; a click puts
		// it on the canvas.
		dots := func(ids []model.PersonID) func() {
			return func() {
				ui.Row(c).Width(18).Children(func() {
					for _, id := range ids[:min(3, len(ids))] {
						ball(c, a.people[string(id)], 10, false).Margin(0, -4, 0, 0).Shadow(0, 0, 0, 1.5, p.well)
					}
				})
			}
		}
		if len(a.shownLANs()) > 1 {
			// Every LAN at once, each in a region (scene 3).
			people := func() { ui.Icon(c, uiGlyphs["people"]).Size(18, 18).TextColor(p.ink3) }
			if navRowWith(c, p, "all", "全部", a.all(), people, rowOpts{}) {
				a.show(c, allLANs, false)
			}
		}
		over := model.LANID("")
		for _, l := range a.snap.LANs {
			count := ""
			if l.Waiting {
				count = "等同意"
			}
			// Something waits for me there: a small blue dot at the row's end,
			// until I let it in or turn it away (DESIGN §6.7).
			waits := l.Owner == a.snap.Me && len(l.Pending)+len(l.PendingMembers) > 0
			o := rowOpts{count: count, flash: a.flash(c, l.ID), dot: waits, ring: a.personOver == l.ID, at: a.rowAt(l.ID)}
			if !l.Waiting && l.Has(a.snap.Self) {
				// Its switch: on, it connects; off, it is paused on this
				// computer, which must be in it (scene 3).
				lan, name, paused := l.ID, l.Name, l.Paused
				o.trail = func() bool {
					flipped := miniToggle(c, p, "pause-"+string(lan), "暂停"+name, !paused)
					if flipped {
						a.pause(c, lan, !paused)
					}
					return flipped
				}
			}
			hover := false
			o.hover = &hover
			if navRowWith(c, p, "lan-"+string(l.ID), l.Name, l.ID == a.lan, dots(l.Members), o) {
				a.show(c, l.ID, false)
			}
			if hover {
				over = l.ID
			}
		}
		a.updateFocus(c, over)
		// Invites sent to me by name that I put off: a grey row each, which
		// shows the invite again (DESIGN §6.7).
		for _, inv := range a.snap.Invitations {
			if a.invite != nil && a.invite.invitation == inv.ID {
				continue
			}
			who := a.people[string(inv.Inviter.ID)]
			if who == nil {
				who = &person{id: string(inv.Inviter.ID), name: inv.Inviter.Name, ch: inv.Inviter.Initial, color: ownerColors[inv.Inviter.Color%model.NumColors]}
			}
			label := strings.TrimLeft(spaced(short(who.name)), " ") + "邀请你 · " + short(inv.LANName)
			if navRow(c, p, "invitation-"+inv.ID, label, false, func() { ball(c, who, 16, false) }, "", 0) {
				a.showInvitation(inv)
			}
		}
		a.newLANRow(c, p)
		ui.Box(c).Height(16)
		group(c, p, "设备")
		navRow(c, p, "我的设备", "我的设备", false, func() { ui.Icon(c, uiGlyphs["home"]).Size(18, 18).TextColor(p.ink3) }, fmt.Sprint(len(a.snap.Mine)), 0)
		shared := ""
		if n := len(a.snap.Shared); n > 0 {
			shared = fmt.Sprint(n)
		}
		// The devices given to me, on a canvas of their own (DESIGN §6.11).
		inbox := func() { ui.Icon(c, uiGlyphs["inbox"]).Size(18, 18).TextColor(p.ink3) }
		if navRowWith(c, p, "共享给我", "共享给我", a.shared(), inbox, rowOpts{count: shared, flash: a.flash(c, model.SharedLANID)}) &&
			a.snap.Can(model.FeatureShare) {
			a.show(c, model.SharedLANID, false)
		}
		ui.Spacer(c)
		// Me: the account card (M7, DESIGN §6.15).
		a.accountRowView(c, p)
	})
}

func group(c *ui.Context, p *palette, s string) {
	ui.Text(c, s).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(0, 8, 6, 8)
}

// navRow is a row of the sidebar; the chosen one is a white pill. A row
// that just came in opens out and lights up, flash (1 to 0) of the way. It
// reports whether it was clicked.
func navRow(c *ui.Context, p *palette, key, label string, on bool, lead func(), count string, flash float32) bool {
	return navRowDot(c, p, key, label, on, lead, count, flash, false)
}

// navRowDot is navRow with a small blue dot at its end, as dot asks.
func navRowDot(c *ui.Context, p *palette, key, label string, on bool, lead func(), count string, flash float32, dot bool) bool {
	return navRowAt(c, p, key, label, on, lead, count, flash, dot, false, nil)
}

// navRowAt is navRowDot that rings blue while what the hand holds can be
// let go on it (ring), and tells where it is in the window (at).
func navRowAt(c *ui.Context, p *palette, key, label string, on bool, lead func(), count string, flash float32, dot, ring bool, at *ui.Rect) bool {
	return navRowWith(c, p, key, label, on, lead, rowOpts{count: count, flash: flash, dot: dot, ring: ring, at: at})
}

// rowOpts are what a row of the sidebar shows besides its label: a count, a
// blue dot, a blue ring while what the hand holds can be let go on it, and
// at its end a switch (trail), which reports whether it was flipped; at and
// hover tell where the row is in the window and whether the pointer is on
// it.
type rowOpts struct {
	count     string
	flash     float32
	dot, ring bool
	at        *ui.Rect
	trail     func() bool
	hover     *bool
}

// navRowWith is a row of the sidebar as o asks.
func navRowWith(c *ui.Context, p *palette, key, label string, on bool, lead func(), o rowOpts) bool {
	count, flash, dot := o.count, o.flash, o.dot
	row := ui.ButtonBase(c.Key(key)).Height(36).Padding(0, 10).Gap(10).Radius(10).FocusRing(false)
	if o.at != nil {
		*o.at = row.Bounds()
	}
	if o.hover != nil {
		*o.hover = row.Hovered()
	}
	if o.ring {
		row.Border(1.5, p.primary)
	}
	if flash > 0 {
		row.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Collapse: true}})
	}
	switch {
	case on && flash > 0:
		row.Background(p.bg.Mix(p.primary, 0.18*flash)).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	case on:
		row.Background(p.bg).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	case flash > 0:
		row.Background(p.well.Mix(p.primary, 0.18*flash))
	case row.Hovered():
		row.Background(p.well2)
	}
	color := p.ink2
	if on {
		color = p.ink
	}
	flipped := false
	row.Children(func() {
		lead()
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(color).Grow(1).SingleLine()
		if count != "" {
			ui.Text(c, count).FontSize(12).TextColor(p.ink3)
		}
		if dot {
			ui.Box(c.Key("dot")).Size(7, 7).Radius(999).Background(p.primary).
				Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.3}, Exit: &ui.Motion{Scale: 0.3}})
		}
		if o.trail != nil && o.trail() {
			flipped = true
		}
	})
	return row.Clicked() && !flipped
}

func iconButton(c *ui.Context, p *palette, icon, label string) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Size(30, 30).Radius(999).Center().Label(label).FocusRing(false)
	if b.Hovered() {
		b.Background(p.well2)
	}
	b.AnimatePose(pressPose(b, 0.92), ui.Snappy)
	b.Children(func() { ui.Icon(c, uiGlyphs[icon]).Size(18, 18).TextColor(p.ink2) })
	return b
}

// pressPose shrinks what the pointer presses, so a press answers at once.
func pressPose(e ui.Element, scale float32) ui.Pose {
	if e.Pressed() {
		return ui.Pose{Scale: scale}
	}
	return ui.Pose{}
}

func (a *app) header(c *ui.Context, p *palette) {
	l := a.snap.LAN(a.lan)
	if l == nil {
		// No LAN yet: the header keeps its place, with nothing in it.
		ui.Row(c).Height(headerHeight).DragWindow()
		return
	}
	name := l.Name
	ui.Row(c).Height(headerHeight).Padding(22, 24, 0, 28).AlignItems(ui.Start).Gap(16).DragWindow().Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			// A click on the LAN's name shows its card (DESIGN §6.7).
			title := ui.ButtonBase(c.Key("lan-title")).FocusRing(false).Cursor(ui.CursorPointer).AlignSelf(ui.Start)
			title.Children(func() {
				ui.Text(c, name).FontSize(20).FontWeight(600).SingleLine().MaxWidth(lanNameWidth)
			})
			if title.Clicked() && !a.shared() {
				if a.selected == lanCard {
					a.selected, a.renaming = "", nil
				} else {
					a.selected, a.more, a.picking, a.renaming = lanCard, false, false, nil
				}
			}
			ui.Row(c).Gap(6).Children(func() {
				dot, line := a.summary(p)
				ui.Box(c).Size(7, 7).Radius(999).Background(dot).Shadow(0, 0, 0, 3, dot.Alpha(0.18))
				ui.Text(c, line).FontSize(13).TextColor(p.ink2)
			})
		})
		ui.Spacer(c)
		// The engine's notice and the balls with "邀请朋友" share one middle
		// line: the header's row keeps its things at the top, and a pill
		// centered on all of it sat lower than the button (the user's
		// report, 2026-11-13).
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			a.noticePill(c, p)
			a.ballRects = map[string]ui.Rect{}
			ui.Row(c).Gap(14).Children(func() {
				ui.Row(c).Children(func() {
					// Everyone in the stack keeps one ball: a member's solid, one
					// who waits for me, or for my invite's answer, dashed (DESIGN
					// §6.7). Letting someone in fills theirs where it is. On the
					// canvas of the devices given to me, they are who gave them.
					i := 0
					for _, id := range a.members {
						if a.shared() && id == a.me {
							continue
						}
						a.stackBall(c, p, l, id, member, i > 0, model.Invited{})
						i++
					}
					for _, id := range l.PendingMembers {
						a.stackBall(c, p, l, string(id), waitsForMe, i > 0, model.Invited{})
						i++
					}
					for _, v := range l.Invited {
						a.stackBall(c, p, l, string(v.Person), invitedByMe, i > 0, v)
						i++
					}
				})
				if l.Waiting || a.shared() {
					// Not in yet: there is no one to invite into it. Nor into
					// the devices given to me.
					return
				}
				a.inviteButton(c, p)
			})
		})
		a.headerLine(c, p)
	})
}

// inviteHeight is how high "邀请朋友" is: the header's right side, whose
// middle line the notice's pill keeps, in 全部's header too.
const inviteHeight = 34

// inviteButton is "邀请朋友". My seats full, it is a dashed outline, grey,
// and a click shows the seats instead (pricing-and-relays §1.5 item 2).
func (a *app) inviteButton(c *ui.Context, p *palette) {
	full := a.seatsFull()
	btn := ui.ButtonBase(c.Key("invite")).Height(inviteHeight).Padding(0, 14).Gap(6).Radius(999).FocusRing(false)
	k := btn.AnimateTo("full", map[bool]float32{false: 0, true: 1}[full], ui.Animation{Duration: 200 * time.Millisecond})
	fill := p.primary
	if btn.Hovered() {
		fill = p.primary2
	}
	if k < 1 {
		btn.Background(fill.Alpha(1-k)).Shadow(0, 1, 2, 0, p.primary.Alpha(0.3*(1-k))).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18*(1-k)))
	}
	if k > 0 {
		hover := btn.Hovered()
		btn.Draw(func(pt *ui.Painter, r ui.Rect) {
			var path ui.Path
			roundRect(&path, r.X+0.75, r.Y+0.75, r.W-1.5, r.H-1.5, (r.H-1.5)/2)
			if hover {
				pt.FillPath(&path, p.well.Alpha(k))
			}
			path.Dash(0, 4, 3)
			pt.StrokePath(&path, 1.5, p.ink3.Alpha(k))
		})
	}
	pose := pressPose(btn, 0.96)
	if full && btn.Hovered() && !btn.Pressed() {
		pose = ui.Pose{Y: -1}
	}
	btn.AnimatePose(pose, ui.Snappy)
	ink := white.Mix(p.ink2, k)
	btn.Children(func() {
		ui.Icon(c, uiGlyphs["plus"]).Size(16, 16).TextColor(ink)
		ui.Text(c, "邀请朋友").FontSize(13).FontWeight(500).TextColor(ink)
	})
	if btn.Clicked() {
		if full {
			a.selected, a.seatFreeing, a.seatFor, a.more = seatCard, false, "", false
			return
		}
		a.inviteFriends(c)
	}
}

// headerLine is the small line at the header's right, under the balls and
// "邀请朋友": while one of my devices is in the hand, what letting it go on
// a ball does (DESIGN §6.11); else, my seats nearly full, how many more
// people can come. One fades into the other.
func (a *app) headerLine(c *ui.Context, p *palette) {
	text := a.aimLine()
	if l := a.snap.LAN(a.lan); text == "" && l != nil && !l.Waiting && !a.shared() {
		// Under "邀请朋友", where it shows.
		text = a.seatLine()
	}
	holder := ui.Row(c.Key("header-line")).Absolute().Right(24).Top(60).PassThrough()
	holder.Children(func() {
		if text == "" {
			return
		}
		ink := p.ink3
		if a.aim.ok {
			ink = p.primary
		}
		// New words take the old ones' place at once and fade in: two
		// lines fading across each other in one place read as a muddle.
		t := ui.Text(c.Key("header-line-"+text), text).FontSize(12).TextColor(ink).SingleLine()
		t.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{Y: -3}})
	})
}

// flash is how lit a LAN's row of the sidebar is, as it comes in: from 1,
// the frame it first shows in, to 0 in 1.4 seconds (as the design demo's
// k-flash).
func (a *app) flash(c *ui.Context, id model.LANID) float32 {
	t, ok := a.flashFrom[id]
	if !ok {
		return 0
	}
	now := c.Now()
	if t.IsZero() {
		t = now
		a.flashFrom[id] = t
	}
	k := float32(now.Sub(t)) / float32(1400*time.Millisecond)
	if k >= 1 {
		delete(a.flashFrom, id)
		return 0
	}
	c.AnimationFrame()
	return max(1-ui.EaseOut(k), 0.001)
}

// lanNameWidth is the widest a LAN's name shows in the header, clear of the
// members' balls and the invite button.
const lanNameWidth = 420

// summary is the line under the LAN's name and the color of its dot: how
// many people and devices, and whether all of them are connected.
func (a *app) summary(p *palette) (ui.Color, string) {
	if !a.adapterJob.asking.IsZero() {
		// The engine stops while the adapter goes in, and starts again as
		// the service: no trouble to tell of (DESIGN §7.3).
		return p.ink3, "正在装网卡…"
	}
	if a.snap.NoNetwork && a.snap.Engine {
		// The trouble is here, not over there (DESIGN §6.10).
		return p.ink3, "这台电脑没联网，联网后会自动连上"
	}
	if a.all() {
		return a.allSummary(p)
	}
	if a.shared() {
		return a.sharedSummary(p)
	}
	n, up, waiting := 0, 0, 0
	for _, d := range a.devices {
		if !d.gone.IsZero() || d.pending {
			continue
		}
		n++
		switch {
		case d.path == "self" || linked(d.path):
			up++
		case d.path == "connecting":
			waiting++
		}
	}
	people := len(a.members)
	l := a.snap.LAN(a.lan)
	switch {
	case !a.snap.Engine:
		return p.ink3, "连不上这台电脑上的 Deneb，正在重试…"
	case l != nil && l.Waiting:
		// I am not in yet: nothing here is mine to count (DESIGN §6.7).
		return p.ink3, "等" + spaced(a.ownerName()) + "同意你加入"
	case l != nil && l.Paused:
		// Paused on this computer: the canvas shows it as it is, and can
		// be used as ever (scene 3).
		return p.ink3, "已暂停，打开开关就恢复"
	case l != nil && a.self != "" && !l.Has(model.DeviceID(a.self)):
		// Made on another of my devices: this computer waits in the tray,
		// and nothing here connects until it is in.
		return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，这台电脑还没加进来", people, n)
	case up == n:
		return p.ok, fmt.Sprintf("%d 个人，%d 台设备都连上了", people, n)
	case up+waiting == n:
		return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，正在连接…", people, n)
	}
	return p.ink3, fmt.Sprintf("%d 个人，%d 台设备，%d 台连上了", people, n, up)
}

// rowAt is where the sidebar's row of a LAN was in the last frame, in the
// window: what a person dragged is let go on.
func (a *app) rowAt(id model.LANID) *ui.Rect {
	if a.rows == nil {
		a.rows = map[model.LANID]*ui.Rect{}
	}
	r, ok := a.rows[id]
	if !ok {
		r = &ui.Rect{}
		a.rows[id] = r
	}
	return r
}

// personHandle lets a member's ball in the header be dragged onto a LAN's
// row in the sidebar, which invites them there (DESIGN §6.4, §6.7). While
// it is dragged, a chip of their ball and name follows the hand in its
// place (personChip); let go anywhere else, the ball springs back.
func (a *app) personHandle(c *ui.Context, b ui.Element, id string) {
	if a.ballOffs == nil {
		a.ballOffs = map[string]*ui.Point{}
	}
	off, ok := a.ballOffs[id]
	if !ok {
		off = &ui.Point{}
		a.ballOffs[id] = off
	}
	b.Draggable(off, ui.DraggableOptions{Animation: ui.Snappy})
	b.Cursor(ui.CursorGrab)
	if a.ballHome == nil {
		a.ballHome = map[string]ui.Point{}
	}
	if !b.Dragging() && *off == (ui.Point{}) {
		// Where it rests: its bounds move with the drag, its home does not.
		if r := b.Bounds(); r.W > 0 {
			a.ballHome[id] = ui.Point{X: r.X + r.W/2, Y: r.Y + r.H/2}
		}
	}
	if b.Dragging() {
		b.Cursor(ui.CursorGrabbing).Opacity(0)
		home, d := a.ballHome[id], b.DragOffset()
		at := ui.Point{X: home.X + d.X, Y: home.Y + d.Y}
		a.person = &personDrag{id: id, at: at}
		a.personOver, a.personFull = "", ""
		for lan, row := range a.rows {
			if at.X >= row.X && at.X <= row.X+row.W && at.Y >= row.Y && at.Y <= row.Y+row.H {
				if a.fullFor(lan, model.PersonID(id)) {
					// No seat for them in it: it does not light, and the
					// chip says why (DESIGN §6.11).
					a.personFull = lan
				} else {
					a.personOver = lan
				}
			}
		}
		return
	}
	if _, _, ended := b.DragEnded(); ended {
		if lan := a.personOver; lan != "" {
			a.inviteTo(c, lan, model.PersonID(id))
		}
		*off = ui.Point{}
		a.person, a.personOver, a.personFull = nil, "", ""
	}
}

// personChip is the chip that follows the hand as it drags a person: their
// ball and name, above everything; over a row it can be let go on, it
// shrinks around the pointer, so that the row shows (DESIGN §6.1).
func (a *app) personChip(c *ui.Context, p *palette) {
	holder := ui.Box(c.Key("person-chip-layer")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
	holder.Children(func() {
		d := a.person
		if d == nil {
			return
		}
		who := a.people[d.id]
		chip := ui.Row(c.Key("person-chip-"+d.id)).Absolute().Left(d.at.X-17).Top(d.at.Y-17).Height(34).Padding(0, 12, 0, 4).Gap(8).
			Radius(999).Background(p.bg).Shadow(0, 8, 20, 0, ui.RGBA(9, 9, 11, 0.16)).Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.08)).
			TransformOrigin(0.1, 0.5).PassThrough()
		pose, name := ui.Pose{Scale: 1.04}, float32(1)
		if a.personOver != "" {
			// Over the row: only the ball, smaller, so the row's name shows.
			pose, name = ui.Pose{Scale: 0.8}, 0
		}
		chip.AnimatePose(pose, ui.Snappy)
		k := chip.AnimateTo("name", name, ui.Animation{Duration: 120 * time.Millisecond})
		if k < 1 {
			chip.Padding(0, 4+8*k, 0, 4).Background(p.bg.Alpha(k))
		}
		chip.Children(func() {
			ball(c, who, 26, false)
			if k > 0.02 {
				ui.Text(c, who.name).FontSize(13).FontWeight(500).Opacity(k)
			}
		})
		if a.personFull != "" {
			// Over a LAN of mine with no seat for them: said beside the
			// chip, clear of its name.
			right := d.at.X - 17 + 4 + 26 + 8 + textWidth(who.name, 13) + 12
			note := ui.Row(c.Key("person-full")).Absolute().Left(right+8).Top(d.at.Y-12).Height(24).Padding(0, 10).
				AlignItems(ui.Center).Radius(999).Background(p.bg).Border(1, p.line).Shadow(0, 4, 12, 0, ui.RGBA(9, 9, 11, 0.1)).PassThrough()
			note.Transition(ui.ElementTransition{Duration: 140 * time.Millisecond, Enter: &ui.Motion{X: -4}, Exit: &ui.Motion{}})
			note.Children(func() {
				ui.Text(c, fmt.Sprintf("%d 个位置都用上了", a.seats().Limit)).FontSize(12).TextColor(p.ink2).SingleLine()
			})
		}
	})
}

// inviteTo invites a person into a LAN by name (DESIGN §6.7): they wait for
// their answer in its ball stack; undo takes the invite back.
func (a *app) inviteTo(c *ui.Context, lan model.LANID, who model.PersonID) {
	l := a.snap.LAN(lan)
	if l == nil {
		return
	}
	name := strings.TrimLeft(spaced(a.people[string(who)].name), " ")
	if slices.Contains(l.Members, who) {
		a.notify(c, nil, name+"已经在「"+short(l.Name)+"」里了", nil)
		return
	}
	a.send(c, model.Op{Kind: model.OpLANInvite, LAN: lan, Person: who})
	a.notify(c, nil, "已邀请"+spaced(a.people[string(who)].name)+"加入「"+short(l.Name)+"」", func() {
		a.send(c, model.Op{Kind: model.OpInviteRevoke, LAN: lan, Person: who})
	})
}
