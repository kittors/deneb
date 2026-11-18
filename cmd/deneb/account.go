package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The account (M7, DESIGN §6.15): the sidebar's last row opens its card,
// with logging this computer out, the privacy notes, and deleting the
// account; account-wide device deletion lives on My Devices.

// privacyURL is the privacy notes, PRIVACY.md on GitHub.
const privacyURL = "https://github.com/kittors/deneb/blob/main/PRIVACY.md"

// danger is the red of what deletes for good: the words of the button, as
// the code that was wrong is said (onboard.go).
var danger = ui.Hex("#ef4444")

// accountRowView is the compact account control in the footer toolbar.
func (a *app) accountRowView(c *ui.Context, p *palette) {
	// The ball on the edge the sidebar's marks start at, its light 4 out of
	// it all round.
	row := ui.ButtonBase(c.Key("account-row")).Size(40, 40).Margin(0, 0, 0, 4).Radius(999).Center().
		FocusRing(false).Label("账号").Tooltip("账号")
	if row.Hovered() || a.accountOpen {
		row.Background(p.well2)
	}
	row.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	row.Children(func() {
		if who := a.people[a.me]; who != nil {
			ball(c, who, 32, false)
		} else {
			ui.Box(c).Size(32, 32).Radius(999).Background(p.well2)
		}
	})
	if b := row.Bounds(); b.W > 0 {
		a.accountRowAt = b
	}
	if row.Clicked() {
		a.accountOpen = !a.accountOpen
		if a.accountOpen {
			a.selected, a.more = "", false
		}
	}
}

// accountLayer is the account card over the window, above the sidebar's
// last row, while it is open: a click anywhere else, or Esc, puts it away.
func (a *app) accountLayer(c *ui.Context, p *palette) {
	layer := ui.Box(c.Key("account-layer")).Absolute().Left(0).Top(0).Right(0).Bottom(0)
	if b := layer.Bounds(); b.H > 0 {
		a.winH = b.H
	}
	if !a.accountOpen {
		layer.PassThrough()
	}
	layer.Children(func() {
		if !a.accountOpen {
			return
		}
		away := ui.ButtonBase(c.Key("account-away")).Absolute().Left(0).Top(0).Right(0).Bottom(0).FocusRing(false).Cursor(ui.CursorDefault)
		if away.Clicked() {
			a.accountOpen = false
		}
		r := a.accountRowAt
		card := ui.Column(c.Key("account-card")).Absolute().Left(r.X).Bottom(a.winH-r.Y+8).Width(cardWidth).Radius(20).Background(p.bg).
			Shadow(0, 10, 28, 0, p.shadow).Shadow(0, 2, 6, 0, ui.RGBA(9, 9, 11, 0.05)).TransformOrigin(0, 1)
		if p.dark {
			card.Border(1, p.line)
		}
		card.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond,
			Enter: &ui.Motion{Scale: 0.96, Y: 8}, Exit: &ui.Motion{Scale: 0.97, Y: 6}})
		if c.Shortcut(0, ui.KeyEscape) {
			a.accountOpen = false
		}
		card.Children(func() { a.accountCard(c, p) })
	})
}

// accountBall is my ball on the account card, across: the column this
// computer's tile keeps to under it.
const accountBall = 40

// accountCard is what the account card says, from the top: who I am; this
// computer, with "登出"; the privacy notes; deleting the account.
func (a *app) accountCard(c *ui.Context, p *palette) {
	me := a.snap.Person(a.snap.Me)
	name := ""
	if me != nil {
		name = me.Name
	}
	// Me, then this computer, 16 apart: no line between (the user, 11-17).
	// This computer's tile under my ball, on its middle line, their words
	// from one edge (the user, 11-18: things of a kind in one column).
	ui.Column(c).Padding(16).Gap(16).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ball(c, a.people[a.me], accountBall, false)
			ui.Column(c).Gap(4).Shrink(1).Children(func() {
				ui.Text(c, name).FontSize(15).FontWeight(600).SingleLine()
				ui.Text(c, a.snap.Email).FontSize(12).TextColor(p.ink2).SingleLine()
			})
		})
		if d := a.snap.Device(a.snap.Self); d != nil {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(accountBall).Center().Shrink(0).Children(func() {
					a.profileTile(c, p, d.ID, glyphFor(d.Kind), ownerColors[0], 32, false, 0)
				})
				ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
					ui.Text(c, "这台电脑").FontSize(11).TextColor(p.ink3)
					ui.Text(c, d.Name).FontSize(13).FontWeight(500).SingleLine()
				})
				edit := ui.ButtonBase(c.Key("edit-self")).Height(28).Padding(0, 12).Radius(999).Background(p.well).FocusRing(false).Label("编辑这台电脑")
				edit.Children(func() { ui.Text(c, "编辑").FontSize(12).FontWeight(500).TextColor(p.ink2) })
				if edit.Clicked() {
					a.accountOpen = false
					a.selected = string(a.snap.Self)
					a.more = false
				}
				out := ui.ButtonBase(c.Key("logout")).Height(28).Padding(0, 12).Radius(999).Background(p.well).FocusRing(false)
				if out.Hovered() {
					out.Background(p.well2)
				}
				out.AnimatePose(pressPose(out, pressGive), ui.Snappy)
				out.Children(func() { ui.Text(c, "登出").FontSize(12).FontWeight(500).TextColor(p.ink).SingleLine() })
				if out.Clicked() {
					a.logout(c)
				}
			})
		}
	})
}

// menuItem is a row of a card or a panel that is one button: its words, in
// col; at its right end what it is now (value, grey, as a 名称：值 has it)
// and an icon, if any. It lights up under the pointer, out to the card's
// edges, its words staying on the edge all else keeps to; pressed, it
// darkens a step (a whole row does not shrink).
func menuItem(c *ui.Context, p *palette, key, label, value string, col ui.Color, icon string) ui.Element {
	return menuItemWith(c, p, key, label, col, icon, func() {
		if value != "" {
			// At the row's end by what shows of it (inkRoom).
			ui.Text(c, value).FontSize(12).TextColor(p.ink3).SingleLine().Margin(0, -inkRoom(value, 12), 0, 0)
		}
	})
}

// menuItemWith is menuItem with its value drawn by value: one that changes
// while the row's work is under way (settings' 软件更新).
func menuItemWith(c *ui.Context, p *palette, key, label string, col ui.Color, icon string, value func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(40).Padding(0, 8).Margin(0, -8).Radius(10).Gap(12).AlignItems(ui.Center).FocusRing(false).Label(label)
	switch {
	case b.Pressed():
		// A step darker than the light under the pointer (a whole row does
		// not give, as a button does).
		b.Background(p.press)
	case b.Hovered():
		b.Background(p.well)
	}
	b.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	b.Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(col).Grow(1).Shrink(1).SingleLine()
		value()
		if icon != "" {
			// Its strokes on the edge, as the panel's × is.
			ui.Icon(c, uiGlyphs[icon]).Size(16, 16).Margin(0, -glyphRoom, 0, 0).TextColor(p.ink3)
		}
	})
	return b
}

// logout logs this computer out (OpLogout), once the window faded to its
// background: the sign-in comes in through it, and logging in again, this
// computer keeps its address.
func (a *app) logout(c *ui.Context) {
	a.accountOpen = false
	a.lowerCurtain(c, func(c *ui.Context) {
		// Down, the window waits for the engine: past busyDelay, the
		// curtain says so (the window a blank page till then, the user's
		// recording of 11-17 1.2 s of it).
		cu := a.curtain
		cu.waiting = "正在登出"
		cu.work.begin(c.Now())
		run(a, c, func(ctx context.Context) (model.Result, error) {
			return a.store.Do(ctx, model.Op{Kind: model.OpLogout})
		}, func(c *ui.Context, _ model.Result, err error) {
			cu.work.end()
			if err != nil {
				if a.curtain != nil {
					a.curtain.back = true
				}
				a.problem(c, err)
				return
			}
			a.signedOut()
		})
	})
}

// curtain fades the window to its background before what changes all of
// it (logging out, the account or this computer deleted), then does then;
// it lifts again over the sign-in that comes, so that the window goes
// through its background rather than cut to another (DESIGN §6.6).
type curtain struct {
	at, lift time.Time
	then     func(c *ui.Context)
	done     bool
	// Deleting this computer, sent once the curtain is down: done, the
	// sign-in comes; refused, it lifts back over the window as it was.
	op   *pending
	back bool
	// While what it waits for is under way (logging out), the curtain says
	// so in its middle, past busyDelay: waiting, the words; work, its timing;
	// said, that it has said so. Said, the line stays till the curtain
	// lifts, and fades out first: the sign-in comes in a view of its own,
	// where the line would not fade out by itself.
	waiting string
	work    busy
	said    bool
}

const (
	curtainFall = 180 * time.Millisecond
	curtainLift = 240 * time.Millisecond
)

func (a *app) lowerCurtain(c *ui.Context, then func(c *ui.Context)) {
	a.curtain = &curtain{at: c.Now(), then: then}
}

// curtainView draws the curtain over all the window shows: falling, or
// down while what it waits for comes; lifting, over the sign-in.
func (a *app) curtainView(c *ui.Context, p *palette, lifting bool) {
	cu := a.curtain
	if cu == nil {
		return
	}
	if o := cu.op; o != nil && o.done {
		cu.op = nil
		if o.err != nil {
			cu.back = true
		} else {
			a.signedOut()
		}
	}
	now := c.Now()
	k := float32(1)
	switch {
	case (lifting || cu.back) && cu.done:
		if cu.lift.IsZero() {
			// The line on it, if it said one, goes first; then the curtain
			// lifts: one thing at a time.
			cu.lift = now
			if cu.said {
				cu.lift = now.Add(swapOut)
			}
		}
		k = min(1, 1-float32(now.Sub(cu.lift))/float32(curtainLift))
		if k <= 0 {
			a.curtain = nil
			return
		}
	case !cu.done:
		k = min(1, float32(now.Sub(cu.at))/float32(curtainFall))
	}
	c.AnimationFrame()
	box := ui.Box(c.Key("curtain")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(p.bg).Opacity(ui.EaseInOut(k)).Center()
	if !cu.lift.IsZero() {
		// Lifting, it lets the pointer through to what comes under it.
		box.PassThrough()
	}
	box.Children(func() {
		if cu.waiting == "" {
			return
		}
		if cu.work.on(c, now) {
			cu.said = true
		}
		if !cu.said {
			return
		}
		line := float32(1)
		if !cu.lift.IsZero() {
			line = max(0, min(1, float32(cu.lift.Sub(now))/float32(swapOut)))
		}
		// As the starting page: the mark, greyed, and the line under it.
		ui.Column(c.Key("curtain-waiting")).Gap(16).AlignItems(ui.Center).Opacity(line).
			Transition(ui.ElementTransition{Duration: swapIn, Enter: &ui.Motion{}}).
			Children(func() {
				ui.Icon(c, markGlyph).Size(markSize, markSize).TextColor(p.ink3)
				waitingWords(c, p, cu.waiting+"…", now)
			})
	})
	if k >= 1 && !cu.done {
		cu.done = true
		cu.then(c)
	}
}

// signedOut puts the window at the sign-in: logged out here, deleted, or
// this computer deleted. The real window would come to it by itself
// (checkOnboard); the demo, which has no sign-in of its own, too.
func (a *app) signedOut() {
	a.selected, a.more, a.accountOpen, a.deleting = "", false, false, nil
	a.setMyDevices(false)
	a.deviceDetails = ""
	a.settingsOpen = false
	a.renaming, a.avatarEdit, a.confirming = nil, nil, nil
	if o := a.onboard; o == nil || o.step != 0 || !o.leaving.IsZero() {
		// (The real window may be at it already: what was typed stays.)
		a.onboard = &onboarding{}
	}
}

// canDelete keeps account-wide deletion on My Devices. A stale confirmation
// cannot act after the device/owner changed, nor queue the same delete twice.
func (a *app) canDelete(id model.DeviceID) bool {
	d := a.snap.Device(id)
	if !a.myDevicesOpen || d == nil || d.Owner != a.snap.Me {
		return false
	}
	for _, p := range a.pend {
		if p.op.Kind == model.OpDeviceDelete && p.op.Device == id {
			return false
		}
	}
	return true
}

func (a *app) deleteRow(c *ui.Context, p *palette, id model.DeviceID) {
	if !a.canDelete(id) {
		return
	}
	label := "删除这台设备"
	if id == a.snap.Self {
		label = "删除这台电脑"
	}
	ui.Row(c.Key("delete-device")).Margin(8, 0, 0, 0).Children(func() {
		// Its mark on the edge the details start at, its light standing out of
		// it, as a menu row's does.
		b := ui.ButtonBase(c.Key("delete-button")).Height(32).Padding(0, 8).Margin(0, 0, 0, -8).Gap(8).Radius(8).FocusRing(false).AlignItems(ui.Center).Label(label)
		if b.Hovered() {
			b.Background(p.well2)
		}
		b.AnimatePose(pressPose(b, pressGive), ui.Snappy)
		b.Children(func() {
			ui.Icon(c, uiGlyphs["trash"]).Size(14, 14).TextColor(danger)
			ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(danger)
		})
		if b.Clicked() {
			a.deleteDevice(c, id)
		}
	})
}

// Deletion is confirmed first, then held behind the visible undo toast. It
// removes the account's device from every LAN; LAN cards never call it.
func (a *app) deleteDevice(c *ui.Context, id model.DeviceID) {
	if !a.canDelete(id) {
		return
	}
	d := a.snap.Device(id)
	a.askConfirm("删除设备", d.Name, "将从所有局域网移除，收回共享并让设备登出。重新添加后地址会改变。", func(c *ui.Context) {
		if a.canDelete(id) {
			a.deleteDeviceNow(c, id)
		}
	})
}
func (a *app) deleteDeviceNow(c *ui.Context, id model.DeviceID) {
	d := a.snap.Device(id)
	if d == nil {
		return
	}
	a.selected, a.more = "", false
	op := model.Op{Kind: model.OpDeviceDelete, Device: id}
	if id == a.snap.Self {
		a.holdFor(c, op, "这台电脑会被删掉")
		held := a.pend[len(a.pend)-1]
		// As the toast goes, the window fades to its background, and then
		// the server hears of it: the sign-in comes in through it.
		commit := a.toast.commit
		a.toast.commit = func(c *ui.Context) {
			if c == nil {
				// The window closed to the bar: nothing to fade.
				commit(nil)
				return
			}
			a.lowerCurtain(c, func(c *ui.Context) {
				commit(c)
				if s := a.store.Snapshot(); s != nil && !s.LoggedIn {
					a.signedOut()
				} else if a.curtain != nil {
					a.curtain.op = held
				}
			})
		}
		return
	}
	a.deviceDetails = ""
	a.holdFor(c, op, say("已删除", short(d.Name)))
}
