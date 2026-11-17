package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The window's other faces, before and around the canvas: logging in, the
// engine not answering yet, no LAN yet; and the sidebar's new LAN and
// scene 3's invite card.

// markSize is the mark's size on the starting and login views. The star of
// DESIGN §15 has no round core: at 52 points it lays down about the ink the
// earlier one did at 44, and weighs as much beside the button.
const markSize = 52

// startingPatience is how long the starting page says only "正在连接
// Deneb…" (engine-restart-offline.md §2.3): a control server that can be
// reached answers well within it, in a few seconds.
const startingPatience = 10 * time.Second

// The starting page's line past startingPatience: the control server out of
// reach; or this computer without a network, which DESIGN §6.10 never has
// the window call 连不上.
const (
	startNoControl = "连不上 Deneb 服务器，正在重试…"
	startNoNetwork = "这台电脑没联网，联网后会自动连上"
)

// startingRoom is the room under the starting page's line for what a long
// wait adds there: a line 4 under it, then the button 8 under that. As much
// is kept over the mark, so that the mark and the line stay where they are
// as it comes.
const startingRoom = 56

// startingView is the window while this computer's engine has not answered
// yet, or answers without the person's view: the mark, greyed, and one
// line, as the store keeps trying. With the control server out of reach
// past startingPatience, the line says so, the new words fading in after
// the old fade out (DESIGN §6.5); under it, in the same fade, that the
// devices connected still work, if some are, and the way to the
// diagnostics (DESIGN §6.14). The view coming, the page fades out as it
// is, and the window comes (startingLeaves).
func (a *app) startingView(c *ui.Context, p *palette) {
	now := a.now()
	line := "正在连接 Deneb…"
	switch {
	case a.engineErr != "" && !a.snap.Engine:
		line = a.engineErr
	case a.longWait(c, now) && a.snap.NoNetwork:
		line = startNoNetwork
	case a.longWait(c, now):
		line = startNoControl
	}
	// Leaving, the page keeps what it says as it fades out.
	page := float32(1)
	if !a.startOut.IsZero() {
		page = 1 - frac(now.Sub(a.startOut), swapOut)
		if a.startWords.shown != "" {
			line = a.startWords.shown
		}
	}
	shown, alpha, busy := a.startWords.step(line, now)
	if busy {
		c.AnimationFrame()
	}
	// No card on this page: a toast here keeps to the middle.
	a.cardAt = ui.Rect{}
	ui.Column(c).Fill().Background(p.bg).Center().DragWindow().Children(func() {
		ui.Box(c.Key("starting-over")).Height(startingRoom).Shrink(0)
		ui.Icon(c, markGlyph).Size(markSize, markSize).TextColor(p.ink3).Opacity(page)
		// Waiting, a ring before the words (waitingWords); the engine that
		// cannot start says why, with none.
		ui.Row(c.Key("starting-line")).Margin(16, 0, 0, 0).Opacity(alpha * page).Children(func() {
			if a.engineErr != "" && shown == a.engineErr {
				ui.Text(c, shown).FontSize(13).TextColor(p.ink2)
				return
			}
			waitingWords(c, p, shown, now)
		})
		ui.Column(c.Key("starting-under")).Height(startingRoom).Shrink(0).AlignItems(ui.Center).Opacity(alpha * page).Children(func() {
			if shown != startNoControl {
				return
			}
			if a.someConnected() {
				ui.Text(c, "已经连上的设备照常能用").FontSize(12).TextColor(p.ink3).Margin(4, 0, 0, 0)
			}
			a.startingDiag(c, p, now)
		})
	})
	ww, wh := c.Size()
	a.toastView(c, p, ww, wh, now)
}

// startingLeaves reports whether the starting page is still on, the view
// having come: one thing at a time (DESIGN §5), its mark and line fade out
// first (swapOut); then the window comes up through the background, the
// curtain lifting off it (DESIGN §6.6). starting is whether the window is
// the starting page now (starting).
func (a *app) startingLeaves(c *ui.Context, starting bool) bool {
	now := a.now()
	switch {
	case starting:
		a.startOut, a.wasStarting = time.Time{}, true
		return false
	case a.wasStarting:
		a.wasStarting, a.startOut = false, now
	}
	if a.startOut.IsZero() {
		return false
	}
	if now.Sub(a.startOut) < swapOut {
		c.AnimationFrame()
		return true
	}
	a.startOut = time.Time{}
	if a.curtain == nil && a.snap.LoggedIn {
		// Down already, it only lifts (the window draws it).
		a.curtain = &curtain{done: true, back: true}
	}
	return false
}

// starting reports whether the window is the starting page: this
// computer's engine not answering yet, or answering without the person's
// view.
func (a *app) starting() bool {
	return !a.snap.Engine && a.snap.Version == 0 || a.snap.LoggedIn && !a.snap.Loaded
}

// longWait reports whether the control server has been out of reach past
// startingPatience, this computer's engine answering without the person's
// view; till then it has the frame built anew as that comes.
func (a *app) longWait(c *ui.Context, now time.Time) bool {
	if a.waitSince.IsZero() || !a.snap.Engine {
		return false
	}
	if left := startingPatience - now.Sub(a.waitSince); left > 0 {
		c.After(left)
		return false
	}
	return true
}

// noteWait keeps waitSince from s, the store's own snapshot: since when it
// has this computer's engine answering, logged in, without the person's
// view and without the control server.
func (a *app) noteWait(s *model.Snapshot) {
	if s == nil || !s.Engine || !s.LoggedIn || s.Loaded || s.Control {
		a.waitSince = time.Time{}
		return
	}
	if a.waitSince.IsZero() {
		a.waitSince = a.now()
	}
}

// someConnected reports whether this computer's engine has some other
// device connected now, as the network map it kept has them.
func (a *app) someConnected() bool {
	for id, p := range a.snap.Paths {
		if id != a.snap.Self && p.Kind != model.PathSelf && p.Kind.Connected() {
			return true
		}
	}
	return false
}

// startingDiag is the starting page's way to the diagnostics package
// (DESIGN §6.14), 8 under the words over it: a text button, the page's one
// thing to do; while the package is put together, "正在收集…" turning in
// its place. The toast then says where it went, as from the card.
func (a *app) startingDiag(c *ui.Context, p *palette, now time.Time) {
	// The button and 正在收集 take turns, as on the card (diagTurn).
	shown, alpha := a.diagTurn(c, a.diagWorking(c, now), now)
	ui.Row(c.Key("starting-diag")).Height(28).Margin(8, 0, 0, 0).Gap(4).AlignItems(ui.Center).Opacity(alpha).Children(func() {
		if shown {
			busyValue(c, "正在收集…", p.ink2, now)
			return
		}
		if textButton(c, p, "starting-diag-button", startDiagLabel, true).Clicked() && !a.diagWork.working(now) {
			a.exportDiag(now)
		}
	})
}

// startDiagLabel is what the starting page's button to the diagnostics
// says.
const startDiagLabel = "导出诊断包"

// loginView is the window before anyone logged in on this computer: the
// mark and one button (§3 M1: 首次打开时只有一个"登录"按钮). The button
// opens the login page in the browser, and the window turns into the LANs
// once the login is done there; there is no field for a password here.
func (a *app) loginView(c *ui.Context, p *palette) {
	ui.Column(c).Fill().Background(p.bg).Center().Gap(24).DragWindow().Children(func() {
		ui.Icon(c, markGlyph).Size(markSize, markSize).TextColor(p.primary)
		btn := primaryButton(c, p, "login", "登录", 40, 28, 14, "")
		if btn.Clicked() && !a.loggingIn {
			a.startLogin(c)
		}
		// One line under the button while the browser has the login, or
		// when it did not start: always there, so that the button stays put.
		line, col := " ", p.ink3
		switch {
		case a.loginErr != "":
			line = a.loginErr
		case a.loggingIn || a.snap.LoginURL != "":
			line = "在浏览器里登录完，这里会自动打开"
		}
		ui.Text(c, line).FontSize(12).TextColor(col).Height(16)
	})
}

// startLogin asks the engine for the login page and opens it in the browser.
func (a *app) startLogin(c *ui.Context) {
	a.loggingIn, a.loginErr = true, ""
	run(a, c, func(ctx context.Context) (string, error) { return a.store.Login(ctx) },
		func(c *ui.Context, url string, err error) {
			if err != nil {
				a.loggingIn, a.loginErr = false, message(err)
				return
			}
			if url != "" && a.openURL != nil && a.openURL(url) != nil {
				a.loggingIn, a.loginErr = false, "打不开浏览器，请把这个链接复制到浏览器里："+url
			}
		})
}

// primaryButton is the design's primary pill: white words on the primary
// color, which presses in a little; icon, if not "", leads them ("+ 添加设备").
// Given work, the work it starts: while that is working it does not press
// in, and past busyDelay its words give way to a ring (busyLabel).
func primaryButton(c *ui.Context, p *palette, key, label string, height, pad, size float32, icon string, work ...*busy) ui.Element {
	ring, working := underWay(c, work)
	btn := ui.ButtonBase(c.Key(key)).Height(height).Padding(0, pad).Radius(999).Background(p.primary).FocusRing(false).
		Shadow(0, 1, 2, 0, p.primary.Alpha(0.3)).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18))
	btn.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	if btn.Hovered() {
		btn.Background(p.primary2)
	}
	if !working {
		btn.AnimatePose(pressPose(btn, pressGive), ui.Snappy)
	}
	btn.Children(func() {
		busyLabel(c, ring, white, 16, func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if icon != "" {
					ui.Icon(c, uiGlyphs[icon]).Size(16, 16).TextColor(white)
				}
				ui.Text(c, label).FontSize(size).FontWeight(500).TextColor(white).SingleLine()
			})
		})
	})
	return btn
}

// textButton is the design's text button (design/components.md): an
// action that does not take the eye, as 重命名, 详情, 复制, 刷新; accent
// shows it in the primary color, where it is the line's one thing to do.
// Given work, as primaryButton: past busyDelay its words give way to a
// ring of 12.
func textButton(c *ui.Context, p *palette, key, label string, accent bool, work ...*busy) ui.Element {
	ring, working := underWay(c, work)
	btn := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 8).Radius(999).FocusRing(false).Shrink(0).Label(label)
	btn.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	if btn.Hovered() {
		btn.Background(p.well2)
	}
	if !working {
		btn.AnimatePose(pressPose(btn, pressGive), ui.Snappy)
	}
	ink := p.ink2
	if accent {
		ink = p.primary
	}
	btn.Children(func() {
		busyLabel(c, ring, ink, 12, func() { ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(ink).SingleLine() })
	})
	return btn
}

// ghostButton is the design's secondary pill: ink on the well.
func ghostButton(c *ui.Context, p *palette, key, label string) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 16).Radius(999).Background(p.well).FocusRing(false)
	btn.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	if btn.Hovered() {
		btn.Background(p.well2)
	}
	btn.AnimatePose(pressPose(btn, pressGive), ui.Snappy)
	btn.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(p.ink).SingleLine() })
	return btn
}

// wellButton is a button on a well, a grey box: white, a hair of shadow
// under it, as the services' 打开 and the chores' 同意 are. A ghostButton,
// grey, would not show on the grey. Given work, as primaryButton: past
// busyDelay its words give way to a ring of 12.
func wellButton(c *ui.Context, p *palette, key, label string, work ...*busy) ui.Element {
	ring, working := underWay(c, work)
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 12).Radius(999).Background(p.bg).FocusRing(false).
		Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06))
	if !working {
		b.AnimatePose(pressPose(b, pressGive), ui.Snappy)
	}
	b.Children(func() {
		busyLabel(c, ring, p.ink, 12, func() { ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(p.ink).SingleLine() })
	})
	return b
}

// cardPrimary is a card's main button, the ghostButton's pair: as high, as
// much room at its sides, its words as large. Given work, as primaryButton.
func cardPrimary(c *ui.Context, p *palette, key, label string, work ...*busy) ui.Element {
	return primaryButton(c, p, key, label, 34, 16, 13, "", work...)
}

// emptyCanvas is the canvas with no LAN to show yet: one line, saying how to
// have one.
func (a *app) emptyCanvas(c *ui.Context, p *palette) {
	ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
		ui.Text(c, "还没有局域网").FontSize(15).FontWeight(600).TextColor(p.ink)
		ui.Text(c, "点左边的「新建局域网」，或者打开朋友发来的邀请链接").FontSize(13).TextColor(p.ink2)
	})
	a.toastView(c, p, a.cw, a.ch, c.Now())
}

// ---- A new LAN ------------------------------------------------------------------------

// naming is a new LAN being named in the sidebar.
type naming struct {
	name    string
	focused bool // the field had the focus, so losing it now ends the naming
	busy    bool // sent, waiting for the store
	work    busy // that, for the ring in the + 's place
}

// newLANRow is the sidebar's "新建局域网". A click turns it into a field to
// name the LAN in, a white pill as the chosen row is. Enter, or a click
// elsewhere, makes the LAN, with this computer in it (DESIGN §6.2), and
// puts it on the canvas; Escape, or no name, puts the row back.
func (a *app) newLANRow(c *ui.Context, p *palette) {
	n := a.naming
	if n == nil {
		if navRow(c, p, "新建局域网", "新建局域网", false, func() { ui.Icon(c, uiGlyphs["plus"]).Size(18, 18).TextColor(p.ink3) }, "", 0) {
			a.naming = &naming{}
		}
		return
	}
	// As a row of the sidebar (navRowWith): the name goes where the label was.
	row := ui.Row(c.Key("new-lan-name")).Height(36).Padding(0, 8).Gap(12).Radius(10).Background(p.bg).
		Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	row.Children(func() {
		// Sent, past busyDelay, a ring takes the + 's place.
		busyLabel(c, n.work.on(c, c.Now()), p.primary, 16, func() { ui.Icon(c, uiGlyphs["plus"]).Size(18, 18).TextColor(p.primary) })
		field := ui.TextInputBase(c.Key("name"), &n.name).FontSize(13).FontWeight(500).Grow(1).
			Placeholder("给局域网起个名字").AutoFocus()
		if n.busy {
			return
		}
		if field.Focused() {
			n.focused = true
		}
		if field.Submitted() || n.focused && !field.Focused() {
			a.createLAN(c, n)
		}
	})
	if row.Shortcut(0, ui.KeyEscape) && !n.busy {
		a.naming = nil
	}
}

// createLAN makes the LAN named, and puts it on the canvas once the store
// has it.
func (a *app) createLAN(c *ui.Context, n *naming) {
	name := strings.TrimSpace(n.name)
	if name == "" {
		a.naming = nil
		return
	}
	n.busy = true
	n.work.begin(c.Now())
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: name})
	}, func(c *ui.Context, r model.Result, err error) {
		n.work.end()
		if a.naming == n {
			a.naming = nil
		}
		if err != nil {
			a.problem(c, err)
			return
		}
		a.goTo(c, r.LAN)
	})
}

// goTo puts the LAN of id, made or joined here, on the canvas, its devices
// popping in: now, or as soon as the snapshot has it, as an op that made it
// or joined it may answer before the snapshot that holds it comes.
func (a *app) goTo(c *ui.Context, id model.LANID) {
	if a.snap.LAN(id) != nil {
		a.show(c, id, true)
		return
	}
	a.wantLAN = id
}

// ---- An invite (scene 3) --------------------------------------------------------------

// inviteCard is scene 3's card: someone asks me into a LAN.
type inviteCard struct {
	inv *model.Invite
	// invitation is the id of an invite sent to me by name, rather than by
	// a link (DESIGN §6.7): joining takes that.
	invitation string
	// joining is 加入's work: the card stays, its ring in 加入, till the
	// store has the LAN.
	joining busy
}

// inviteSpring is how the card comes in: as the design demo's, a little
// past its place and back.
var inviteSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.2}}

// openLink takes a link the app was opened with: an invite's, which shows
// its card once the store says what it is. One opened before the login waits
// for it.
func (a *app) openLink(c *ui.Context, link string) {
	if !strings.HasPrefix(link, "deneb:") {
		return
	}
	if !a.snap.LoggedIn {
		a.laterLink = link
		return
	}
	run(a, c, func(ctx context.Context) (*model.Invite, error) { return a.store.Preview(ctx, link) },
		func(c *ui.Context, inv *model.Invite, err error) {
			switch {
			case err != nil:
				a.problem(c, err)
			case inv.Gone:
				a.notify(c, nil, "这个邀请链接已经用过，或者过期了", nil)
			case inv.AlreadyIn:
				a.goTo(c, inv.LAN)
			default:
				a.invite = &inviteCard{inv: inv}
				// The card takes the window's top corner, where the device's
				// card would cover it.
				a.selected, a.more = "", false
			}
		})
}

// inviteLayer holds the invite card in the window's top right corner, over
// the header's right side, clear of the canvas (DESIGN scene 3). It is
// built in every frame, as Enter and Exit play only in a parent that
// stays: the first card ever comes in as the others do, and goes out.
func (a *app) inviteLayer(c *ui.Context, p *palette) {
	holder := ui.Column(c.Key("invite-layer")).Absolute().Top(18).Right(20).Width(cardWidth).PassThrough()
	holder.Children(func() {
		card := a.invite
		if card == nil {
			return
		}
		inv := card.inv
		box := ui.Column(c.Key("invite-"+inv.Link)).Width(cardWidth).Padding(16).Radius(20).Background(p.bg).
			Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05)).TransformOrigin(1, 0)
		if p.dark {
			box.Border(1, p.line)
		}
		box.Transition(ui.ElementTransition{Animation: &inviteSpring, Enter: &ui.Motion{Y: -8, Scale: 0.97},
			Exit: &ui.Motion{Y: -6, Scale: 0.97}})
		box.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				who := &person{id: string(inv.Inviter.ID), name: inv.Inviter.Name, ch: inv.Inviter.Initial,
					color: ownerColors[inv.Inviter.Color%model.NumColors]}
				ball(c, who, 40, false)
				ui.Column(c).Gap(4).Grow(1).Children(func() {
					ui.Text(c, strings.TrimLeft(spaced(short(inv.Inviter.Name)), " ")+"邀请你加入「"+short(inv.LANName)+"」").
						FontSize(15).FontWeight(600).MaxLines(2)
					sub := "这台电脑会跟着加进去"
					if inv.SeatsFull {
						// The inviter's seats are full: joining waits for one
						// (pricing-and-relays §1.5 item 5).
						sub = say(short(inv.Inviter.Name), "的位置满了，腾出来就能加入")
					}
					ui.Text(c, sub).FontSize(12).TextColor(p.ink2).MaxLines(2)
				})
			})
			ui.Row(c).Gap(8).Margin(16, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if ghostButton(c, p, "later", "以后再说").Clicked() {
					a.invite = nil
				}
				if cardPrimary(c, p, "join", "加入", &card.joining).Clicked() && !card.joining.working(c.Now()) {
					a.join(c, card)
				}
			})
		})
	})
}

// join accepts the invite. The card stays while the store takes it, past
// busyDelay a ring in 加入 (DESIGN §5), and goes once the store has the
// LAN, which comes onto the canvas, this computer in it; refused, the card
// stays as it was, and a toast says why. The demo's store answers at once:
// the card goes at once, as in the design demo.
func (a *app) join(c *ui.Context, card *inviteCard) {
	inv := card.inv
	card.joining.begin(c.Now())
	op := model.Op{Kind: model.OpInviteAccept, Invite: inv.Link, Invitation: card.invitation}
	if card.invitation != "" {
		op.Invite = ""
	}
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, op)
	}, func(c *ui.Context, r model.Result, err error) {
		card.joining.end()
		if err != nil {
			a.problem(c, err)
			return
		}
		if a.invite == card {
			a.invite = nil
		}
		lan := r.LAN
		if lan == "" {
			lan = inv.LAN
		}
		if !a.all() {
			// In the "全部" view, it comes in as a region of its own, there.
			a.goTo(c, lan)
		}
	})
}

// message is what the view says of an error, in a sentence.
func message(err error) string {
	var e *model.Error
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return "没办成，等一下再试试"
}

// showInvitation shows the card of an invite sent to me by name.
func (a *app) showInvitation(inv model.Invitation) {
	a.invite = &inviteCard{inv: &model.Invite{LAN: inv.LAN, LANName: inv.LANName, Inviter: inv.Inviter, SeatsFull: inv.SeatsFull}, invitation: inv.ID}
	a.selected, a.more = "", false
}

// waitingCanvas is the canvas of a LAN whose owner has not let me in yet:
// only the owner, and a word on what I wait for (DESIGN §6.7).
func (a *app) waitingCanvas(c *ui.Context, p *palette, l *model.LAN) {
	owner := a.people[string(l.Owner)]
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(16).Children(func() {
		ball(c, owner, 44, false)
		ui.Text(c, "等"+spaced(owner.name)+"同意你加入「"+short(l.Name)+"」").FontSize(15).FontWeight(500).TextColor(p.ink2)
	})
}

// notice is the first of the engine's notices not put away; else the
// window's own, of the network adapter not in yet (DESIGN §7.3); or nil.
func (a *app) notice() *model.Notice {
	for i := range a.snap.Notices {
		if !a.noticesDone[a.snap.Notices[i].Code] {
			return &a.snap.Notices[i]
		}
	}
	if a.adapterMissing() && !a.noticesDone[adapterNotice.Code] {
		return &adapterNotice
	}
	return nil
}

// noticePill tells of what the engine found, as another program taking
// Deneb's connections, in a small pill in the header, clear of the canvas:
// a click shows its card, with the rule to add to that program (DESIGN
// §6.8). Deneb never changes the other program itself.
func (a *app) noticePill(c *ui.Context, p *palette) {
	n := a.notice()
	holder := ui.Row(c.Key("notice-holder")).AlignSelf(ui.Center)
	holder.Children(func() {
		if n == nil {
			return
		}
		// Its dot as far from the round end as from the top and bottom.
		pill := ui.ButtonBase(c.Key("notice-"+n.Code)).Height(30).Padding(0, 12, 0, 12).Gap(8).Radius(999).
			Background(ui.Hex("#F59E0B").Alpha(0.1)).FocusRing(false).Cursor(ui.CursorPointer)
		if pill.Hovered() {
			pill.Background(ui.Hex("#F59E0B").Alpha(0.16))
		}
		pill.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.9}, Exit: &ui.Motion{Scale: 0.9}})
		pill.AnimatePose(pressPose(pill, 0.96), ui.Snappy)
		pill.Children(func() {
			ui.Box(c).Size(6, 6).Radius(999).Background(ui.Hex("#F59E0B"))
			ui.Text(c, n.Short).FontSize(12).FontWeight(500).TextColor(p.ink2).SingleLine()
		})
		if pill.Clicked() {
			if a.selected == noticeCard {
				a.selected = ""
			} else {
				a.selected, a.more, a.picking, a.renaming = noticeCard, false, false, nil
			}
		}
	})
}

// noticeCard is what a.selected is while the notice's card shows.
const noticeCard = "\x00notice"

// noticeCard says what the engine found in full, the rule to add to the
// other program with a button to copy it, and "知道了", which puts it away
// for good. It takes the device card's place, in the same way.
func (a *app) noticeCard(c *ui.Context, p *palette) {
	n := a.notice()
	if n == nil {
		a.selected = ""
		return
	}
	card := ui.Column(c.Key("card")).Width(cardWidth).Radius(20).Background(p.bg).Clip().
		Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05)).TransformOrigin(1, 0)
	if p.dark {
		card.Border(1, p.line)
	}
	if b := card.Bounds(); b.W > 0 {
		a.cardAt = ui.Rect{X: b.X - a.origin.X, Y: b.Y - a.origin.Y, W: b.W, H: b.H}
	}
	card.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Size: true,
		Enter: &ui.Motion{Scale: 0.96, Y: -8}, Exit: &ui.Motion{Scale: 0.97, Y: -6}})
	card.Children(func() {
		body := ui.Column(c.Key("body-notice-" + n.Code)).Padding(16).Gap(12)
		body.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
		body.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, n.Message).FontSize(15).FontWeight(600).Grow(1).Shrink(1)
				if iconButton(c, p, "close", "关闭").Background(p.well).Clicked() {
					a.selected = ""
				}
			})
			for _, para := range strings.Split(n.Detail, "\n") {
				if para != "" {
					ui.Text(c, para).FontSize(12).TextColor(p.ink2).LineHeight(1.5)
				}
			}
			if n.Code == adapterNotice.Code {
				// The window's own: its card puts the adapter in.
				a.adapterCard(c, p)
				return
			}
			if n.Rule != "" && n.Detail == "" {
				// Clash's (M2); the others say where in their own words.
				ui.Text(c, "在它的规则里加上这一条，局域网里的连接就不经过它：").FontSize(12).TextColor(p.ink2)
			}
			if n.Rule != "" {
				ui.Text(c, n.Rule).FontSize(12).FontWeight(500).FontFeatures("tnum").Padding(8, 12).Radius(12).Background(p.well).SingleLine()
			}
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if ghostButton(c, p, "notice-ok", "知道了").Clicked() {
					if a.noticesDone == nil {
						a.noticesDone = map[string]bool{}
					}
					a.noticesDone[n.Code], a.selected = true, ""
				}
				if n.Settings != "" && cardPrimary(c, p, "settings", "去系统设置").Clicked() && a.openURL != nil {
					// Where the system's switch for Deneb is (M6 on).
					a.openURL(n.Settings)
				}
				if n.Rule != "" && cardPrimary(c, p, "copy-rule", "复制规则").Clicked() {
					if a.copyText != nil {
						a.copyText(n.Rule)
					}
					a.notify(c, nil, "已复制，去那个软件里加上就行", nil)
				}
			})
		})
	})
}
