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

// startingView is the window while this computer's engine has not answered
// yet: the mark, greyed, and one line, as the store keeps trying.
func (a *app) startingView(c *ui.Context, p *palette) {
	ui.Column(c).Fill().Background(p.bg).Center().Gap(18).DragWindow().Children(func() {
		ui.Icon(c, markGlyph).Size(44, 44).TextColor(p.ink3)
		if a.engineErr != "" && !a.snap.Engine {
			ui.Text(c, a.engineErr).FontSize(13).TextColor(p.ink2)
			return
		}
		ui.Text(c, "正在连接 Deneb…").FontSize(13).TextColor(p.ink2)
	})
}

// loginView is the window before anyone logged in on this computer: the
// mark and one button (§3 M1: 首次打开时只有一个"登录"按钮). The button
// opens the login page in the browser, and the window turns into the LANs
// once the login is done there; there is no field for a password here.
func (a *app) loginView(c *ui.Context, p *palette) {
	ui.Column(c).Fill().Background(p.bg).Center().Gap(22).DragWindow().Children(func() {
		ui.Icon(c, markGlyph).Size(44, 44).TextColor(p.primary)
		btn := primaryButton(c, p, "login", "登录", 40, 28, 14)
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
// color, which presses in a little.
func primaryButton(c *ui.Context, p *palette, key, label string, height, pad, size float32) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Height(height).Padding(0, pad).Radius(999).Background(p.primary).FocusRing(false).
		Shadow(0, 1, 2, 0, p.primary.Alpha(0.3)).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18))
	if btn.Hovered() {
		btn.Background(p.primary2)
	}
	btn.AnimatePose(pressPose(btn, 0.96), ui.Snappy)
	btn.Children(func() { ui.Text(c, label).FontSize(size).FontWeight(500).TextColor(white).SingleLine() })
	return btn
}

// ghostButton is the design's secondary pill: ink on the well.
func ghostButton(c *ui.Context, p *palette, key, label string) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 14).Radius(999).Background(p.well).FocusRing(false)
	if btn.Hovered() {
		btn.Background(p.well2)
	}
	btn.AnimatePose(pressPose(btn, 0.96), ui.Snappy)
	btn.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(p.ink).SingleLine() })
	return btn
}

// emptyCanvas is the canvas with no LAN to show yet: one line, saying how to
// have one.
func (a *app) emptyCanvas(c *ui.Context, p *palette) {
	ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
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
	row := ui.Row(c.Key("new-lan-name")).Height(36).Padding(0, 10).Gap(10).Radius(10).Background(p.bg).
		Shadow(0, 1, 2, 0, ui.RGBA(9, 9, 11, 0.06)).Shadow(0, 1, 1, 0, ui.RGBA(9, 9, 11, 0.04))
	row.Children(func() {
		ui.Icon(c, uiGlyphs["plus"]).Size(18, 18).TextColor(p.primary)
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
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: name})
	}, func(c *ui.Context, r model.Result, err error) {
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
				ui.Column(c).Gap(2).Grow(1).Children(func() {
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
				if primaryButton(c, p, "join", "加入", 34, 14, 13).Clicked() {
					a.join(c, card)
				}
			})
		})
	})
}

// join accepts the invite: the card goes at once, as in the design demo,
// and the LAN comes onto the canvas, this computer in it, once the store
// has it there.
func (a *app) join(c *ui.Context, card *inviteCard) {
	inv := card.inv
	a.invite = nil
	op := model.Op{Kind: model.OpInviteAccept, Invite: inv.Link, Invitation: card.invitation}
	if card.invitation != "" {
		op.Invite = ""
	}
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, op)
	}, func(c *ui.Context, r model.Result, err error) {
		if err != nil {
			a.problem(c, err)
			return
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
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Children(func() {
		ball(c, owner, 44, false)
		ui.Text(c, "等"+spaced(owner.name)+"同意你加入「"+short(l.Name)+"」").FontSize(15).FontWeight(500).TextColor(p.ink2)
	})
}

// notice is the first of the engine's notices not put away, or nil.
func (a *app) notice() *model.Notice {
	for i := range a.snap.Notices {
		if !a.noticesDone[a.snap.Notices[i].Code] {
			return &a.snap.Notices[i]
		}
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
		pill := ui.ButtonBase(c.Key("notice-"+n.Code)).Height(30).Padding(0, 12, 0, 10).Gap(8).Radius(999).
			Background(ui.Hex("#F59E0B").Alpha(0.1)).FocusRing(false).Cursor(ui.CursorPointer)
		if pill.Hovered() {
			pill.Background(ui.Hex("#F59E0B").Alpha(0.16))
		}
		pill.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.9}, Exit: &ui.Motion{Scale: 0.9}})
		pill.AnimatePose(pressPose(pill, 0.96), ui.Snappy)
		pill.Children(func() {
			ui.Box(c).Size(7, 7).Radius(999).Background(ui.Hex("#F59E0B"))
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
		body := ui.Column(c.Key("body-notice-" + n.Code)).Padding(16).Gap(10)
		body.Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}})
		body.Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
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
			if n.Rule != "" {
				ui.Text(c, "在它的规则里加上这一条，局域网里的连接就不经过它：").FontSize(12).TextColor(p.ink2)
				ui.Text(c, n.Rule).FontSize(12).FontWeight(500).FontFeatures("tnum").Padding(9, 12).Radius(12).Background(p.well).SingleLine()
			}
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if ghostButton(c, p, "notice-ok", "知道了").Clicked() {
					if a.noticesDone == nil {
						a.noticesDone = map[string]bool{}
					}
					a.noticesDone[n.Code], a.selected = true, ""
				}
				if n.Settings != "" && primaryButton(c, p, "settings", "去系统设置", 34, 14, 13).Clicked() && a.openURL != nil {
					// Where the system's switch for Deneb is (M6 on).
					a.openURL(n.Settings)
				}
				if n.Rule != "" && primaryButton(c, p, "copy-rule", "复制规则", 34, 14, 13).Clicked() {
					if a.copyText != nil {
						a.copyText(n.Rule)
					}
					a.notify(c, nil, "已复制，去那个软件里加上就行", nil)
				}
			})
		})
	})
}
