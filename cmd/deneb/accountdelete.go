package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// Deleting the account (M7, DESIGN §6.15; design/plan/m7/account-delete.md
// §5): the whole window, one column in the middle, as the first time's
// steps. A dry run first says what goes; the LANs I own that have others in
// them are handed to who joined first, or deleted, as the person chooses;
// my email typed again lets the red button delete it all, for good.

// accountDeleting is the page while it shows.
type accountDeleting struct {
	acc     *model.AccountDeletion // the last dry run's answer
	blocked []model.LANID          // mine with others in them, from the first dry run
	owned   string                 // what becomes of those: model.AccountTransfer or model.AccountDeleteLANs
	asking  bool                   // a dry run is under way
	refused string                 // the server's no to the dry run, in its words (an account the seed names)
	email   string
	sending bool   // the account is being deleted
	trouble string // what went wrong, in a sentence
	// The demo's server answers at once: it waits as long as a real one
	// takes, from since, so that the recordings show the wait.
	since             time.Time
	waitDry, waitSend bool
}

// demoAnswer is how long the demo's server takes to answer the dry run, and
// to delete the account.
const (
	demoDryRun = 700 * time.Millisecond
	demoDelete = 1200 * time.Millisecond
)

// relayStopCommand stops a relay of one's own on its machine, which the
// server cannot (account-delete.md §4.2).
const relayStopCommand = "docker rm -f deneb-relay && docker volume rm deneb-relay"

// accountGone is what the sign-in says once the account was deleted: that it
// was, and, for one who ran relays of their own, how to stop them.
type accountGone struct {
	relays []string  // the names of my relays that went
	copied time.Time // when its command was copied: its button says so a moment
}

// openAccountDelete shows the page, and asks the server what would go.
func (a *app) openAccountDelete(c *ui.Context) {
	a.selected, a.more, a.accountOpen = "", false, false
	dl := &accountDeleting{owned: model.AccountTransfer, since: c.Now()}
	a.deleting = dl
	if a.signer == nil {
		dl.waitDry = true
		return
	}
	a.dryRun(c, "")
}

// dryRun asks the server what deleting would delete, the LANs I own that
// have others in them as owned says; with owned empty, which they are.
func (a *app) dryRun(c *ui.Context, owned string) {
	dl := a.deleting
	dl.asking, dl.trouble = true, ""
	op := model.Op{Kind: model.OpAccountDelete, DryRun: true, OwnedLANs: owned}
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) },
		func(c *ui.Context, r model.Result, err error) {
			if a.deleting != dl {
				return
			}
			dl.asking = false
			var e *model.Error
			switch {
			case errors.As(err, &e) && e.Code == model.ErrForbidden:
				dl.refused = message(err)
				return
			case err != nil:
				dl.trouble = troubleWords(err)
				return
			case r.Account == nil:
				return
			}
			if owned == "" && len(r.Account.Blocked) > 0 {
				// Theirs to choose: what the default choice would do.
				dl.blocked = slices.Clone(r.Account.Blocked)
				a.dryRun(c, dl.owned)
				return
			}
			dl.acc = r.Account
		})
}

// deleteAccount deletes it, for good: the window goes to the sign-in, which
// says so.
func (a *app) deleteAccount(c *ui.Context) {
	dl := a.deleting
	dl.sending, dl.trouble, dl.since = true, "", c.Now()
	if a.signer == nil {
		dl.waitSend = true
		return
	}
	a.sendDelete(c)
}

// sendDelete asks the server to delete the account.
func (a *app) sendDelete(c *ui.Context) {
	dl := a.deleting
	op := model.Op{Kind: model.OpAccountDelete, Confirm: strings.TrimSpace(dl.email)}
	if len(dl.blocked) > 0 {
		op.OwnedLANs = dl.owned
	}
	var relays []string
	if dl.acc != nil {
		for _, id := range dl.acc.Relays {
			relays = append(relays, a.ownRelayName(id))
		}
	}
	run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) },
		func(c *ui.Context, _ model.Result, err error) {
			if err != nil {
				dl.sending = false
				dl.trouble = troubleWords(err)
				return
			}
			// Gone: the window fades to the sign-in, which says so.
			a.lowerCurtain(c, func(*ui.Context) {
				a.signedOut()
				a.gone = &accountGone{relays: relays}
			})
		})
}

// ownRelayName is one of my relays as the page names it.
func (a *app) ownRelayName(id model.RelayID) string {
	if r := a.snap.Relays[id]; r != nil && r.Name != "" {
		return r.Name
	}
	return "我的服务器"
}

// emailMatches reports whether typed is my email: case and spaces aside, as
// the server takes it.
func (a *app) emailMatches(typed string) bool {
	return a.snap.Email != "" && strings.EqualFold(strings.TrimSpace(typed), a.snap.Email)
}

// accountDeleteLayer is the page over the window, while it shows: it fades
// in over the window, and out again, the window under it as it was.
func (a *app) accountDeleteLayer(c *ui.Context, p *palette) {
	holder := ui.Box(c.Key("delete-layer")).Absolute().Left(0).Top(0).Right(0).Bottom(0)
	if a.deleting == nil {
		holder.PassThrough()
	}
	holder.Children(func() {
		if a.deleting != nil {
			a.accountDeleteView(c, p)
		}
	})
}

// accountDeleteView is the page, the whole window over the window.
func (a *app) accountDeleteView(c *ui.Context, p *palette) {
	dl := a.deleting
	now := c.Now()
	for _, w := range []struct {
		on    *bool
		after time.Duration
		do    func()
	}{{&dl.waitDry, demoDryRun, func() { a.dryRun(c, "") }}, {&dl.waitSend, demoDelete, func() { a.sendDelete(c) }}} {
		if !*w.on {
			continue
		}
		if left := w.after - now.Sub(dl.since); left > 0 {
			c.After(left)
			continue
		}
		*w.on = false
		w.do()
	}
	if a.deleting != dl {
		return
	}
	// Its own background takes the pointer: nothing under it answers, and
	// it moves the window, as the first time's steps do.
	root := ui.Box(c.Key("delete-account")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(p.bg).DragWindow()
	fadeIn := ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}}
	root.Children(func() {
		ui.Scroll(c.Key("delete-scroll")).Fill().Children(func() {
			ui.Column(c).AlignSelf(ui.Stretch).AlignItems(ui.Center).Padding(84, 0, 56, 0).Children(func() {
				col := ui.Column(c.Key("delete-column")).Width(deleteWidth)
				col.Children(func() {
					switch {
					case dl.refused != "":
						ui.Column(c.Key("delete-refused")).Transition(fadeIn).Children(func() { a.deleteRefused(c, p) })
					case dl.acc == nil:
						ui.Row(c).Gap(10).Height(240).AlignSelf(ui.Center).AlignItems(ui.Center).Children(func() {
							if dl.trouble != "" {
								ui.Text(c, dl.trouble).FontSize(13).TextColor(danger)
								return
							}
							spinner(c, p, now)
							ui.Text(c, "正在看会删掉什么…").FontSize(13).TextColor(p.ink2)
						})
					default:
						ui.Column(c.Key("delete-page")).Transition(fadeIn).Children(func() { a.deletePage(c, p, now) })
					}
				})
			})
		})
		// Back, at the top left, clear of the Mac's window buttons.
		back := ui.ButtonBase(c.Key("delete-back")).Absolute().Left(20).Top(46).Height(30).Padding(0, 10, 0, 6).Gap(2).Radius(999).
			AlignItems(ui.Center).FocusRing(false).Label("返回")
		if back.Hovered() {
			back.Background(p.well)
		}
		back.Children(func() {
			ui.Icon(c, uiGlyphs["chevronLeft"]).Size(16, 16).TextColor(p.ink2)
			ui.Text(c, "返回").FontSize(13).TextColor(p.ink2)
		})
		if (back.Clicked() || c.Shortcut(0, ui.KeyEscape)) && !dl.sending {
			a.deleting = nil
		}
	})
	root.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
}

// deleteWidth is the page's column.
const deleteWidth = 420

// deleteRefused is the page when the server will not delete this account
// (the deployment's seed names it): its words, and the way back.
func (a *app) deleteRefused(c *ui.Context, p *palette) {
	ui.Text(c, "注销账号").FontSize(22).FontWeight(600)
	ui.Text(c, a.deleting.refused).FontSize(13).TextColor(p.ink2).LineHeight(1.6).Padding(14, 16).Radius(14).Background(p.well).Margin(18, 0, 0, 0)
	ui.Row(c).Margin(20, 0, 0, 0).Children(func() {
		if ghostButton(c, p, "refused-back", "返回").Clicked() {
			a.deleting = nil
		}
	})
}

// deletePage is the page once the dry run said what goes.
func (a *app) deletePage(c *ui.Context, p *palette, now time.Time) {
	dl := a.deleting
	acc := dl.acc
	ui.Text(c, "注销账号").FontSize(22).FontWeight(600)
	ui.Text(c, a.snap.Email+" 和它名下的东西会一起删掉，删了不能恢复。").FontSize(13).TextColor(p.ink2).LineHeight(1.5).Margin(8, 0, 0, 0)

	// What goes, a line each.
	ui.Column(c.Key("what-goes")).Margin(20, 0, 0, 0).Padding(14, 16).Radius(16).Background(p.well).Gap(12).Children(func() {
		if n := len(acc.Devices); n > 0 {
			goneRow(c, p, "devices", "screen", fmt.Sprintf("%d 台设备会登出并删掉", n), a.deviceNames(acc.Devices))
		}
		var lans []model.LANID
		for _, id := range acc.LANs {
			if !slices.Contains(dl.blocked, id) {
				lans = append(lans, id)
			}
		}
		if len(lans) > 0 {
			goneRow(c, p, "lans", "people", a.lanNames(lans)+"会删掉", "只有你在里面")
		}
		if len(acc.Left) > 0 {
			goneRow(c, p, "left", "globe", "会退出"+a.lanNames(acc.Left), "别人的局域网，里面的人照常用")
		}
		var parts []string
		if acc.Shares > 0 {
			parts = append(parts, fmt.Sprintf("%d 次分享", acc.Shares))
		}
		if acc.Invites > 0 {
			parts = append(parts, fmt.Sprintf("%d 个邀请", acc.Invites))
		}
		if len(parts) > 0 {
			goneRow(c, p, "shares", "inbox", strings.Join(parts, "、")+"会删掉", "给出的和收到的")
		}
		if len(acc.Relays) > 0 {
			var names []string
			for _, id := range acc.Relays {
				names = append(names, "「"+a.ownRelayName(id)+"」")
			}
			goneRow(c, p, "relays", "arrows", strings.Join(names, "、")+"中转站会删掉", "")
		}
	})

	if len(dl.blocked) > 0 {
		a.ownedChoice(c, p)
	}
	if len(acc.RelaysInUse) > 0 {
		var names []string
		for _, id := range acc.RelaysInUse {
			names = append(names, "「"+a.ownRelayName(id)+"」")
		}
		ui.Row(c.Key("relays-in-use")).Margin(16, 0, 0, 0).Padding(12, 14).Radius(14).Gap(10).AlignItems(ui.Start).
			Background(ui.Hex("#F59E0B").Alpha(0.1)).Children(func() {
			ui.Box(c).Size(7, 7).Radius(999).Background(ui.Hex("#F59E0B")).Margin(6, 0, 0, 0)
			ui.Text(c, "你的"+strings.Join(names, "、")+"正被别人用着，注销后他们会改走公共中转站，可能慢一些。").
				FontSize(12).TextColor(p.ink2).LineHeight(1.5).Grow(1).Shrink(1)
		})
	}

	// The email again, and the red button.
	ui.Text(c, "再输一遍你的邮箱，确认是你").FontSize(13).FontWeight(500).Margin(26, 0, 8, 0)
	field := ui.TextInputBase(c.Key("confirm-email"), &dl.email).Width(deleteWidth).Height(46).Padding(0, 16).Radius(12).
		Background(p.well).FontSize(14).Placeholder("你的邮箱").PlaceholderColor(p.ink3).Label("你的邮箱")
	ok := a.emailMatches(dl.email) && !dl.asking
	if dl.trouble != "" {
		ui.Text(c.Key("delete-trouble-"+dl.trouble), dl.trouble).FontSize(12).TextColor(danger).Margin(10, 0, 0, 0).
			Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
	}
	b := ui.ButtonBase(c.Key("delete-now")).Width(deleteWidth).Height(44).Margin(14, 0, 0, 0).Radius(999).Justify(ui.Center).
		AlignItems(ui.Center).Gap(8).FocusRing(false).Label("注销账号")
	switch {
	case dl.sending:
		b.Background(danger.Alpha(0.85))
	case ok:
		b.Background(danger).Shadow(0, 1, 2, 0, danger.Alpha(0.3))
		if b.Hovered() {
			b.Background(ui.Hex("#dc2626"))
		}
		b.AnimatePose(pressPose(b, 0.98), ui.Snappy)
	default:
		b.Background(p.well).Cursor(ui.CursorDefault)
	}
	b.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Colors: true})
	b.Children(func() {
		if dl.sending {
			whiteSpinner(c, now)
			ui.Text(c, "正在注销…").FontSize(14).FontWeight(500).TextColor(white)
			return
		}
		col := white
		if !ok {
			col = p.ink3
		}
		ui.Text(c, "注销账号").FontSize(14).FontWeight(500).TextColor(col)
	})
	if (b.Clicked() || field.Submitted()) && ok && !dl.sending {
		a.deleteAccount(c)
	}
	ui.Text(c, "注销后不能恢复").FontSize(11).TextColor(p.ink3).Margin(10, 0, 0, 0).AlignSelf(ui.Center)
	later := ui.ButtonBase(c.Key("delete-later")).Height(32).Padding(0, 12).Radius(999).Margin(8, 0, 0, 0).FocusRing(false).AlignSelf(ui.Center)
	if later.Hovered() {
		later.Background(p.well)
	}
	later.Children(func() { ui.Text(c, "算了").FontSize(13).TextColor(p.ink2) })
	if later.Clicked() && !dl.sending {
		a.deleting = nil
	}
}

// goneRow is a line of what goes: its icon, what, and a grey line under it.
func goneRow(c *ui.Context, p *palette, key, icon, what, line string) {
	ui.Row(c.Key("gone-" + key)).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(28, 28).Radius(8).Background(p.bg).Center().Children(func() {
			ui.Icon(c, uiGlyphs[icon]).Size(16, 16).TextColor(p.ink2)
		})
		ui.Column(c).Gap(2).Grow(1).Shrink(1).Padding(4, 0, 0, 0).Children(func() {
			ui.Text(c, what).FontSize(13).FontWeight(500).LineHeight(1.4)
			if line != "" {
				ui.Text(c, line).FontSize(12).TextColor(p.ink2).LineHeight(1.45)
			}
		})
	})
}

// ownedChoice is the LANs I own that have others in them, and the two
// ways with them: handed to who joined first (the default), or deleted.
func (a *app) ownedChoice(c *ui.Context, p *palette) {
	dl := a.deleting
	ui.Text(c, "你建的局域网里还有别人").FontSize(13).FontWeight(500).Margin(22, 0, 10, 0)
	ui.Column(c).Gap(8).Children(func() {
		for _, id := range dl.blocked {
			l := a.snap.LAN(id)
			if l == nil {
				continue
			}
			ui.Row(c.Key("blocked-" + string(id))).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(26).Children(func() {
					for _, m := range l.Members[:min(3, len(l.Members))] {
						ball(c, a.people[string(m)], 12, false).Margin(0, -4, 0, 0).Shadow(0, 0, 0, 1.5, p.bg)
					}
				})
				ui.Text(c, l.Name).FontSize(13)
				ui.Text(c, fmt.Sprintf("还有 %d 个人", len(l.Members)-1)).FontSize(12).TextColor(p.ink3)
			})
		}
	})
	ui.Row(c).Gap(12).Margin(12, 0, 0, 0).Children(func() {
		a.ownedCard(c, p, model.AccountTransfer, "转给最早加入的人", "局域网留着，大家照常用", p.primary)
		a.ownedCard(c, p, model.AccountDeleteLANs, "整个删掉", "里面的人都会失去它", danger)
	})
	// What the choice does, as the dry run said.
	line := ""
	switch acc := dl.acc; {
	case dl.asking:
	case dl.owned == model.AccountTransfer && len(acc.Transferred) > 0:
		var parts []string
		for _, t := range acc.Transferred {
			if l := a.snap.LAN(t.LAN); l != nil {
				parts = append(parts, short(l.Name)+"转给"+spaced(a.people[string(t.To)].name))
			}
		}
		line = strings.Join(parts, "，")
	case dl.owned == model.AccountDeleteLANs:
		people := map[model.PersonID]bool{}
		var names []string
		for _, id := range dl.blocked {
			if l := a.snap.LAN(id); l != nil {
				names = append(names, short(l.Name))
				for _, m := range l.Members {
					if m != a.snap.Me {
						people[m] = true
					}
				}
			}
		}
		line = fmt.Sprintf("%s会删掉，里面的 %d 个人都会失去它", strings.Join(names, "、"), len(people))
	}
	ui.Box(c.Key("owned-line")).Height(20).Margin(10, 0, 0, 0).Children(func() {
		if line != "" {
			ui.Text(c.Key("owned-line-"+line), strings.TrimSpace(line)).FontSize(12).TextColor(p.ink2).
				Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
		}
	})
}

// ownedCard is one of the two ways with my LANs that have others in them, a
// card to choose, ringed in col while it is the one.
func (a *app) ownedCard(c *ui.Context, p *palette, owned, title, sub string, col ui.Color) {
	dl := a.deleting
	on := dl.owned == owned
	b := ui.ButtonBase(c.Key("owned-"+owned)).Width((deleteWidth-12)/2).Padding(14, 14).Radius(14).Background(p.bg).
		Border(1.5, p.line).FocusRing(false).AlignItems(ui.Start).Label(title)
	if on {
		b.Border(1.5, col).Background(col.Alpha(0.06))
	} else if b.Hovered() {
		b.Border(1.5, col.Alpha(0.4))
	}
	b.Transition(ui.ElementTransition{Duration: 140 * time.Millisecond, Colors: true})
	b.AnimatePose(pressPose(b, 0.98), ui.Snappy)
	b.Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				radio(c, p, on, col)
				ui.Text(c, title).FontSize(14).FontWeight(600)
			})
			ui.Text(c, sub).FontSize(12).TextColor(p.ink2).LineHeight(1.45).Padding(0, 0, 0, 24)
		})
	})
	if b.Clicked() && !on && !dl.sending {
		dl.owned = owned
		a.dryRun(c, owned)
	}
}

// radio is a choice's round mark: a ring, filled in col while it is the one.
func radio(c *ui.Context, p *palette, on bool, col ui.Color) {
	ring := ui.Box(c).Size(16, 16).Radius(999).Border(1.5, p.ink3.Alpha(0.6)).Center()
	if on {
		ring.Border(1.5, col)
	}
	ring.Children(func() {
		if on {
			ui.Box(c.Key("radio-dot")).Size(8, 8).Radius(999).Background(col).
				Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.3}, Exit: &ui.Motion{Scale: 0.3}})
		}
	})
}

// whiteSpinner is the spinner on a red button.
func whiteSpinner(c *ui.Context, now time.Time) {
	c.AnimationFrame()
	turn := float32(now.UnixNano()%int64(time.Second)) / float32(time.Second) * 360
	s := ui.Box(c).Size(16, 16)
	s.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1.5)
		pt.StrokePath(&path, 2, white.Alpha(0.3))
		path.Dash(0, 12, 40)
		pt.StrokePath(&path, 2, white)
	})
	s.Rotate(turn)
}

// deviceNames lists devices by name, this computer first, then as the
// tray has them; past four, "……等 6 台".
func (a *app) deviceNames(ids []model.DeviceID) string {
	ordered := slices.Clone(ids)
	slices.SortStableFunc(ordered, func(x, y model.DeviceID) int {
		rank := func(id model.DeviceID) int {
			if id == a.snap.Self {
				return -1
			}
			if i := slices.Index(a.snap.Mine, id); i >= 0 {
				return i
			}
			return len(a.snap.Mine)
		}
		return rank(x) - rank(y)
	})
	var names []string
	for _, id := range ordered {
		name := string(id)
		if d := a.snap.Device(id); d != nil {
			name = d.Name
		}
		if id == a.snap.Self {
			name = "这台电脑"
		}
		names = append(names, short(name))
	}
	if len(names) > 4 {
		return strings.Join(names[:4], "、") + fmt.Sprintf("……等 %d 台", len(names))
	}
	return strings.Join(names, "、")
}

// lanNames lists LANs as the page names them: "「家里」、「周末联机」".
func (a *app) lanNames(ids []model.LANID) string {
	var names []string
	for _, id := range ids {
		if l := a.snap.LAN(id); l != nil {
			names = append(names, "「"+short(l.Name)+"」")
		}
	}
	return strings.Join(names, "、")
}

// goneNote is what the sign-in says once the account was deleted: that it
// was, and, for one who ran relays of their own, how to stop them on their
// machines, with a button to copy it.
func (a *app) goneNote(c *ui.Context, p *palette) {
	ui.Row(c.Key("gone-line")).Gap(6).Height(20).AlignItems(ui.Center).Margin(0, 0, 18, 0).Children(func() {
		checkMark(c, p.ok, 14)
		ui.Text(c, "账号已经注销").FontSize(13).FontWeight(500).TextColor(p.ok)
	}).Transition(ui.ElementTransition{Duration: 240 * time.Millisecond, Enter: &ui.Motion{Y: -4}})
}

// goneRelays is the card under the sign-in, for one who ran relays of their
// own: the container still runs on its machine; the command stops it.
func (a *app) goneRelays(c *ui.Context, p *palette) {
	g := a.gone
	if g == nil || len(g.relays) == 0 {
		return
	}
	var names []string
	for _, n := range g.relays {
		names = append(names, "「"+n+"」")
	}
	box := ui.Column(c.Key("gone-relays")).Width(360).Margin(28, 0, 0, 0).Padding(14, 16).Radius(16).Background(p.well).Gap(10)
	box.Transition(ui.ElementTransition{Duration: 240 * time.Millisecond, Enter: &ui.Motion{Y: 6}})
	box.Children(func() {
		ui.Text(c, strings.Join(names, "、")+"中转站已经删掉，可那台机器上的容器还在跑，可以在上面停掉它：").FontSize(12).TextColor(p.ink2).LineHeight(1.5)
		// The two commands a line each, as they are run; copied as one.
		ui.Text(c, strings.ReplaceAll(relayStopCommand, " && ", "\n")).FontSize(11).FontWeight(500).LineHeight(1.6).Padding(8, 10).Radius(10).Background(p.bg)
		ui.Row(c).Children(func() {
			ui.Spacer(c)
			// No toast on the sign-in: the button says it was copied.
			label := "复制"
			if now := c.Now(); now.Sub(g.copied) < 2*time.Second {
				label = "已复制"
				c.After(2*time.Second - now.Sub(g.copied))
			}
			if ghostButton(c, p, "copy-stop", label).Clicked() {
				if a.copyText != nil {
					a.copyText(relayStopCommand)
				}
				g.copied = c.Now()
			}
		})
	})
}
