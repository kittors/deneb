package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// The first time Deneb opens on a computer (DESIGN §7.3): sign in, the
// network adapter, the first LAN, one step at a time, in a column in the
// middle of the window; then the window as it always is, the steps fading
// away over it.

// onboarding is where the first time's steps are.
type onboarding struct {
	step    int // 0 sign in, 1 the adapter, 2 the first LAN
	email   string
	sentAt  time.Time // the code went to the email
	code    string
	wrongAt time.Time // the code was wrong: its boxes shake
	okAt    time.Time // signed in
	focus   bool      // the code's boxes were clicked: their field takes the keys
	asking  time.Time // 装上: the system asks the person
	inAt    time.Time // the adapter is in
	skipAt  time.Time // 先不装
	joining bool      // 加入朋友的: the link's field
	link    string
	invite  *model.Invite
	leaving time.Time // done: it fades away over the window
	next    time.Time // when it goes on by itself to the next step
	// The real window's (a.signer, a.adapter): asking the server for a code,
	// or checking one; what went wrong, in words; the browser signs in for
	// a server whose pin the window does not know; the system's question
	// was answered no.
	sending, checking bool
	trouble           string
	viaBrowser        bool
	declined          bool
}

// signer signs this computer in from the first step: it asks the server to
// send a code to an email, then hands it the code (client.SignIn).
type signer interface {
	SendCode(ctx context.Context, email string) error
	CheckCode(ctx context.Context, email, code string) error
}

// checkOnboard starts the first time's steps in the real window (DESIGN
// §7.3): whoever is not signed in gets the first; a person signed in with
// nothing yet gets the others, once on this computer. Signed in with LANs
// already, there is nothing to show: done.
func (a *app) checkOnboard() {
	if a.signer == nil || a.onboard != nil || !a.snap.Engine && a.snap.Version == 0 {
		return
	}
	switch {
	case !a.snap.LoggedIn:
		a.onboard = &onboarding{}
	case !a.snap.Loaded || a.onboarded:
	case len(a.snap.LANs) > 0 || len(a.snap.Shared) > 0 || len(a.snap.ShareLANs) > 0:
		a.setOnboarded()
	default:
		if step := a.nextStep(0); step < 3 {
			a.onboard = &onboarding{step: step}
		}
	}
}

// nextStep is the step after from: the adapter where the window puts it in
// (the demo shows it too), the first LAN while there is none; 3 is done.
// Once the steps were done on this computer, signing in again is all.
func (a *app) nextStep(from int) int {
	if a.signer != nil && a.onboarded {
		return 3
	}
	n := from + 1
	if n == 1 && a.signer != nil && (a.adapter == nil || a.snap.Kernel) {
		n = 2
	}
	if n == 2 && a.signer != nil && a.snap.Loaded && len(a.snap.LANs) > 0 {
		n = 3
	}
	return n
}

// setOnboarded keeps that the steps were done on this computer.
func (a *app) setOnboarded() {
	if a.onboarded {
		return
	}
	a.onboarded = true
	if a.saveOnboarded != nil {
		a.saveOnboarded()
	}
}

// onboardFade is how long the steps take to fade away over the window.
const onboardFade = 360 * time.Millisecond

// demoCode is the code the demo's sign in takes.
const demoCode = "246810"

// onboardView is the steps, the whole window.
func (a *app) onboardView(c *ui.Context, p *palette) {
	o := a.onboard
	now := c.Now()
	if !o.next.IsZero() {
		if now.Before(o.next) {
			c.After(o.next.Sub(now))
		} else {
			o.next = time.Time{}
			o.step = a.nextStep(o.step)
			o.joining = false
		}
	}
	if a.signer != nil && o.leaving.IsZero() && (o.step >= 3 || o.step == 2 && a.snap.Loaded && len(a.snap.LANs) > 0 && o.invite == nil) {
		// Done, or a LAN came while the step showed (joined by a link
		// opened from the browser): the window, the LAN on its canvas.
		o.leaving = now
		a.setOnboarded()
	}
	ui.Column(c.Key("onboard")).Fill().Background(p.bg).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Box(c).Height(150)
		ui.Icon(c, starGlyph).Size(40, 40).TextColor(p.primary)
		ui.Row(c).Gap(8).Margin(16, 0, 0, 0).Children(func() {
			for i := 0; i < 3; i++ {
				col := p.ink3.Alpha(0.35)
				w := float32(8)
				switch {
				case i == o.step:
					col, w = p.primary, 22
				case i < o.step:
					col = p.primary.Alpha(0.45)
				}
				d := ui.Box(c.Key(fmt.Sprint("dot-", i))).Height(8).Radius(999).Background(col)
				d.Width(d.AnimateTo("w", w, ui.Snappy))
				d.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Colors: true})
			}
		})
		ui.Box(c).Height(36)
		holder := ui.Box(c.Key("onboard-holder")).Width(380)
		holder.Children(func() {
			step := ui.Column(c.Key(fmt.Sprint("step-", o.step))).Width(380).AlignItems(ui.Center)
			step.Transition(ui.ElementTransition{Duration: 240 * time.Millisecond, Position: true,
				Enter: &ui.Motion{X: 24}, Exit: &ui.Motion{X: -24}})
			step.Children(func() {
				switch o.step {
				case 0:
					a.onboardSignIn(c, p, now)
				case 1:
					a.onboardAdapter(c, p, now)
				default:
					a.onboardLAN(c, p, now)
				}
			})
		})
		// The window coming here through its background (logged out,
		// deleted): the curtain lifts over the steps (DESIGN §6.15).
		a.curtainView(c, p, true)
	})
}

// onboardTitle is a step's title and the sentence under it.
func onboardTitle(c *ui.Context, p *palette, title, sub string) {
	ui.Text(c, title).FontSize(22).FontWeight(600)
	ui.Text(c, sub).FontSize(13).TextColor(p.ink2).Margin(8, 0, 0, 0).LineHeight(1.5).TextAlign(ui.Center).MaxWidth(360)
}

// onboardSignIn is the first step: the email, then the code sent to it.
func (a *app) onboardSignIn(c *ui.Context, p *palette, now time.Time) {
	o := a.onboard
	if a.gone != nil {
		// The account was just deleted (DESIGN §6.15).
		a.goneNote(c, p)
	}
	onboardTitle(c, p, "登录 Deneb", "用邮箱就行，我们发一个 6 位验证码")
	// The email, then the code: one at a time, fading across in a place
	// that keeps its height.
	fade := ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	ui.Box(c.Key("sign-ways")).Width(380).Height(150).Margin(26, 0, 0, 0).Children(func() {
		way := func(key string) ui.Element {
			return ui.Column(c.Key(key)).Absolute().Left(0).Top(0).Right(0).AlignItems(ui.Center).Transition(fade)
		}
		if o.sentAt.IsZero() {
			way("way-email").Children(func() {
				field := ui.TextInputBase(c.Key("email"), &o.email).Width(360).Height(46).Padding(0, 16).Radius(12).Background(p.well).
					FontSize(14).Placeholder("你的邮箱").PlaceholderColor(p.ink3).AutoFocus()
				send := onboardButton(c, p, "send-code", "发验证码").Margin(12, 0, 0, 0)
				if (send.Clicked() || field.Submitted()) && strings.Contains(o.email, "@") && !o.sending {
					a.sendCode(c, now)
				}
				if line := a.signLine(); line != "" {
					ui.Text(c.Key("sign-line-"+line), line).FontSize(12).TextColor(a.signLineColor(p)).Margin(10, 0, 0, 0).
						TextAlign(ui.Center).MaxWidth(360).Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
				}
			})
			return
		}
		way("way-code").Children(func() { a.onboardCode(c, p, now) })
	})
	a.goneRelays(c, p)
}

// onboardCode is the six boxes of the code sent to the email, and the line
// under them.
func (a *app) onboardCode(c *ui.Context, p *palette, now time.Time) {
	o := a.onboard
	ui.Text(c, "验证码发到了 "+maskEmail(o.email)).FontSize(13).FontWeight(500)
	// The six boxes show the code; a field out of sight takes the keys.
	digits := []rune(o.code)
	if len(digits) > 6 {
		digits = digits[:6]
	}
	shake := float32(0)
	if !o.wrongAt.IsZero() && now.Sub(o.wrongAt) < 420*time.Millisecond {
		t := float64(now.Sub(o.wrongAt)) / float64(420*time.Millisecond)
		shake = float32(6 * math.Sin(t*math.Pi*5) * (1 - t))
		c.AnimationFrame()
	}
	boxes := ui.ButtonBase(c.Key("code-boxes")).Margin(14, 0, 0, 0).FocusRing(false).Cursor(ui.CursorText)
	boxes.Translate(shake, 0)
	boxes.Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			for i := 0; i < 6; i++ {
				ring := p.line
				if i == len(digits) && o.okAt.IsZero() {
					ring = p.primary
				}
				if !o.wrongAt.IsZero() && len(digits) == 0 {
					ring = ui.Hex("#ef4444").Alpha(0.7)
				}
				b := ui.Box(c.Key(fmt.Sprint("box-", i))).Size(48, 56).Radius(12).Background(p.bg).Border(1.5, ring).Center()
				b.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
				b.Children(func() {
					if i < len(digits) {
						ui.Text(c.Key(fmt.Sprint("d-", i, string(digits[i]))), string(digits[i])).FontSize(22).FontWeight(600).FontFeatures("tnum").
							Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Enter: &ui.Motion{Scale: 0.8}, Exit: &ui.Motion{}})
					}
				})
			}
		})
	})
	if boxes.Clicked() {
		o.focus = true
	}
	field := ui.TextInputBase(c.Key("code"), &o.code).Absolute().Left(-2000).Top(0).Size(10, 10).AutoFocus()
	if o.focus {
		field.Focus()
		o.focus = false
	}
	if field.Changed() {
		o.code = onlyDigits(o.code, 6)
		if len(o.code) > 0 {
			o.wrongAt = time.Time{}
		}
	}
	if len(o.code) == 6 && o.okAt.IsZero() && !o.checking {
		switch {
		case a.signer != nil:
			a.checkCode(c, o.code)
		case o.code == demoCode:
			o.okAt = now
			o.next = now.Add(700 * time.Millisecond)
		default:
			o.code, o.wrongAt = "", now
		}
	}
	line, col := "", p.ink3
	switch {
	case !o.okAt.IsZero():
		line, col = "登录了", p.ok
	case !o.wrongAt.IsZero():
		line, col = "验证码不对，再看一眼", ui.Hex("#ef4444")
	case o.trouble != "":
		line, col = o.trouble, ui.Hex("#ef4444")
	case o.checking:
		line = "正在登录…"
	default:
		if left := 60 - int(now.Sub(o.sentAt).Seconds()); left > 0 {
			line = fmt.Sprintf("没收到？%d 秒后可以重发", left)
			c.After(time.Second)
		} else {
			line = "没收到？重发"
		}
	}
	ui.Row(c.Key("code-line-"+line)).Gap(6).Height(20).Margin(14, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		if !o.okAt.IsZero() {
			checkMark(c, p.ok, 14)
		}
		ui.Text(c, line).FontSize(12).TextColor(col)
	}).Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
}

// onboardAdapter is the second step: why the adapter, what the system will
// ask, and the way on without it.
func (a *app) onboardAdapter(c *ui.Context, p *palette, now time.Time) {
	o := a.onboard
	kind, col := "laptop", ownerColors[0]
	if d := a.snap.Device(a.snap.Self); d != nil {
		kind = glyphFor(d.Kind)
	}
	deviceTile(c, p, kind, col, 56, false).Margin(0, 0, 18, 0)
	onboardTitle(c, p, "给这台电脑装一块网卡", "装上以后，这台电脑上的任何程序（浏览器、远程桌面、游戏）都能直接连到局域网里的设备，就像在同一个 Wi-Fi 里。")
	ask := "系统会请你在「登录项与扩展」里打开 Deneb，输一次开机密码。"
	if a.platform == "windows" {
		ask = "Windows 会弹一个窗口问你，点「是」就行。"
	}
	askBox(c, p, ask).Margin(18, 0, 0, 0).Width(360)
	if a.adapter == nil && !o.asking.IsZero() && o.inAt.IsZero() && now.Sub(o.asking) >= adapterAsk {
		// The demo's system lets it in.
		o.inAt = now
		o.next = now.Add(900 * time.Millisecond)
		a.adapterJob.done = true
	} else if a.adapter == nil && !o.asking.IsZero() && o.inAt.IsZero() {
		c.After(adapterAsk - now.Sub(o.asking))
	}
	// The ways on, one at a time, fading across in a place that keeps its
	// height.
	fade := ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	ui.Box(c.Key("adapter-ways")).Width(360).Height(96).Margin(20, 0, 0, 0).Children(func() {
		way := func(key string) ui.Element {
			return ui.Column(c.Key(key)).Absolute().Left(0).Top(0).Right(0).AlignItems(ui.Center).Transition(fade)
		}
		switch {
		case !o.inAt.IsZero():
			way("adapter-in").Children(func() {
				ui.Row(c).Gap(8).Height(44).AlignItems(ui.Center).Children(func() {
					checkMark(c, p.ok, 16)
					ui.Text(c, "网卡装好了").FontSize(14).FontWeight(500).TextColor(p.ok)
				})
			})
		case !o.asking.IsZero():
			way("adapter-asking").Children(func() {
				ui.Row(c).Gap(10).Height(44).Padding(0, 22).Radius(999).Background(p.well).AlignItems(ui.Center).Children(func() {
					spinner(c, p, now)
					ui.Text(c, "等你在系统里点允许…").FontSize(14).FontWeight(500).TextColor(p.ink2)
				})
			})
		case !o.skipAt.IsZero():
			way("adapter-skip").Children(func() {
				// Where it is put in later: the pill in the window (DESIGN §7.3).
				ui.Text(c, "也能用：在 Deneb 里点「打开」就能连；\n以后点窗口右上角的「还没装网卡」就能装").FontSize(12).TextColor(p.ink2).
					Margin(12, 0, 0, 0).TextAlign(ui.Center).MaxWidth(360)
			})
		default:
			way("adapter-idle").Children(func() {
				if o.declined {
					ui.Text(c, "没装上也能用，在 Deneb 里点「打开」照样能连").FontSize(12).TextColor(p.ink2).
						Margin(0, 0, 10, 0).TextAlign(ui.Center).MaxWidth(360)
				}
				if onboardButton(c, p, "install", "装上").Clicked() {
					o.asking, o.declined = now, false
					a.installAdapter(c)
				}
				later := ui.ButtonBase(c.Key("skip")).Height(32).Padding(0, 12).Radius(999).Margin(8, 0, 0, 0).FocusRing(false)
				if later.Hovered() {
					later.Background(p.well)
				}
				later.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
				later.Children(func() { ui.Text(c, "先不装").FontSize(13).TextColor(p.ink2) })
				if later.Clicked() {
					o.skipAt = now
					o.next = now.Add(1600 * time.Millisecond)
				}
			})
		}
	})
}

// askBox says what the system will ask the person, beside a lock, in a grey
// box: the adapter's step, and its card in the window (DESIGN §7.3).
func askBox(c *ui.Context, p *palette, text string) ui.Element {
	box := ui.Row(c).Gap(8).Padding(10, 14).Radius(12).Background(p.well).AlignItems(ui.Start)
	box.Children(func() {
		ui.Icon(c, uiGlyphs["lock"]).Size(16, 16).TextColor(p.ink2).Margin(1, 0, 0, 0)
		ui.Text(c, text).FontSize(12).TextColor(p.ink2).LineHeight(1.5).Grow(1).Shrink(1)
	})
	return box
}

// onboardButton is the steps' main button, as wide as their column, its
// hover fading in.
func onboardButton(c *ui.Context, p *palette, key, label string) ui.Element {
	b := primaryButton(c, p, key, label, 44, 24, 14).Width(360).Justify(ui.Center)
	b.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
	return b
}

// onboardLAN is the last step: a LAN of one's own, or a friend's.
func (a *app) onboardLAN(c *ui.Context, p *palette, now time.Time) {
	o := a.onboard
	onboardTitle(c, p, "建一个局域网，或者加入朋友的", "以后随时能再建、再加入")
	// The two cards, the link's field, the friend's invite: one at a time,
	// fading across in a place that keeps its height.
	fade := ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	ui.Box(c.Key("lan-ways")).Width(360).Height(160).Margin(26, 0, 0, 0).Children(func() {
		way := func(key string) ui.Element {
			return ui.Column(c.Key(key)).Absolute().Left(0).Top(0).Right(0).AlignItems(ui.Center).Transition(fade)
		}
		switch {
		case o.invite != nil:
			way("way-invite").Children(func() { a.onboardInvite(c, p) })
		case o.joining:
			way("way-link").Gap(10).Children(func() {
				field := ui.TextInputBase(c.Key("link"), &o.link).Width(360).Height(46).Padding(0, 16).Radius(12).Background(p.well).
					FontSize(13).Placeholder("把邀请链接粘贴到这里").PlaceholderColor(p.ink3).AutoFocus()
				join := onboardButton(c, p, "join-link", "加入")
				if (join.Clicked() || field.Submitted()) && strings.TrimSpace(o.link) != "" {
					link := strings.TrimSpace(o.link)
					run(a, c, func(ctx context.Context) (*model.Invite, error) { return a.store.Preview(ctx, link) },
						func(c *ui.Context, inv *model.Invite, err error) {
							if err != nil || inv == nil || inv.Gone {
								o.link = ""
								return
							}
							o.invite = inv
						})
				}
			})
		default:
			way("way-choices").Children(func() {
				ui.Row(c).Gap(12).Children(func() {
					a.choiceCard(c, p, "new", "新建局域网", "先只有这台电脑，之后把别的设备拖进来、邀请朋友", func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							deviceTile(c, p, "laptop", ownerColors[0], 34, false)
							ui.Box(c).Size(22, 22).Radius(999).Background(p.primary.Alpha(0.12)).Center().Children(func() {
								ui.Icon(c, uiGlyphs["plus"]).Size(12, 12).TextColor(p.primary)
							})
						})
					}, func() { a.onboardNewLAN(c) })
					a.choiceCard(c, p, "join", "加入朋友的", "打开朋友发来的邀请链接，或者粘贴在这里", func() {
						ui.Box(c).Size(34, 34).Radius(10).Background(p.well).Center().Children(func() {
							ui.Icon(c, uiGlyphs["globe"]).Size(18, 18).TextColor(p.ink2)
						})
					}, func() { o.joining = true })
				})
			})
		}
	})
	later := ui.ButtonBase(c.Key("later")).Height(32).Padding(0, 12).Radius(999).Margin(14, 0, 0, 0).FocusRing(false)
	if later.Hovered() {
		later.Background(p.well)
	}
	later.Children(func() { ui.Text(c, "以后再说").FontSize(13).TextColor(p.ink2) })
	if later.Clicked() {
		o.leaving = now
	}
}

// choiceCard is one of the last step's two cards: its picture, its title
// and a line, the whole card a button.
func (a *app) choiceCard(c *ui.Context, p *palette, key, title, sub string, pic, do func()) {
	b := ui.ButtonBase(c.Key("choice-"+key)).Width(174).Height(160).Padding(18, 16).Radius(16).Background(p.bg).Border(1, p.line).
		FocusRing(false).AlignItems(ui.Start).Label(title)
	if b.Hovered() {
		b.Border(1, p.primary.Alpha(0.5)).Shadow(0, 8, 20, 0, p.shadow)
	}
	b.Transition(ui.ElementTransition{Duration: 140 * time.Millisecond, Colors: true})
	b.AnimatePose(pressPose(b, 0.97), ui.Snappy)
	b.Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			pic()
			ui.Text(c, title).FontSize(15).FontWeight(600)
			ui.Text(c, sub).FontSize(12).TextColor(p.ink2).LineHeight(1.45)
		})
	})
	if b.Clicked() {
		do()
	}
}

// onboardInvite is the friend's invite, as its card says it (§6.7), in
// the step.
func (a *app) onboardInvite(c *ui.Context, p *palette) {
	o := a.onboard
	inv := o.invite
	box := ui.Column(c.Key("join-invite")).Width(360).Padding(16).Radius(16).Background(p.bg).Border(1, p.line)
	box.Transition(ui.ElementTransition{Animation: &inviteSpring, Enter: &ui.Motion{Y: -8, Scale: 0.97}, Exit: &ui.Motion{}})
	box.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			who := &person{id: string(inv.Inviter.ID), name: inv.Inviter.Name, ch: inv.Inviter.Initial,
				color: ownerColors[inv.Inviter.Color%model.NumColors]}
			ball(c, who, 40, false)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, strings.TrimLeft(spaced(short(inv.Inviter.Name)), " ")+"邀请你加入「"+short(inv.LANName)+"」").FontSize(15).FontWeight(600)
				ui.Text(c, "这台电脑会跟着加进去").FontSize(12).TextColor(p.ink2)
			})
		})
		ui.Row(c).Margin(14, 0, 0, 0).Children(func() {
			ui.Spacer(c)
			if primaryButton(c, p, "onboard-join", "加入", 34, 16, 13).Clicked() {
				a.join(c, &inviteCard{inv: inv})
				o.leaving = c.Now()
			}
		})
	})
}

// onboardNewLAN makes the first LAN, named after its owner, and leaves the
// steps for the window, the LAN on its canvas.
func (a *app) onboardNewLAN(c *ui.Context) {
	name := "我的局域网"
	if me := a.snap.Person(a.snap.Me); me != nil && me.Name != "" {
		name = say(me.Name, "的局域网")
	}
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: name})
	}, func(c *ui.Context, r model.Result, err error) {
		if err != nil {
			a.problem(c, err)
			return
		}
		a.onboard.leaving = c.Now()
		a.goTo(c, r.LAN)
	})
}

// onboardOver is the steps fading away over the window, once done; nil
// once they have.
func (a *app) onboardOver(c *ui.Context, p *palette) {
	o := a.onboard
	k := float32(c.Now().Sub(o.leaving)) / float32(onboardFade)
	if k >= 1 {
		a.onboard = nil
		return
	}
	c.AnimationFrame()
	ui.Box(c.Key("onboard-over")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Opacity(1 - ui.EaseInOut(k)).PassThrough().
		Children(func() { a.onboardView(c, p) })
}

// spinner is a small ring turning, while something is under way.
func spinner(c *ui.Context, p *palette, now time.Time) {
	c.AnimationFrame()
	turn := float32(now.UnixNano()%int64(time.Second)) / float32(time.Second) * 360
	s := ui.Box(c).Size(16, 16)
	s.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1.5)
		pt.StrokePath(&path, 2, p.ink3.Alpha(0.25))
		path.Dash(0, 12, 40)
		pt.StrokePath(&path, 2, p.primary)
	})
	s.Rotate(turn)
}

// checkMark is a tick in col, size across.
func checkMark(c *ui.Context, col ui.Color, size float32) ui.Element {
	b := ui.Box(c).Size(size, size)
	b.Draw(func(pt *ui.Painter, r ui.Rect) {
		var path ui.Path
		path.MoveTo(r.X+r.W*0.18, r.Y+r.H*0.54).LineTo(r.X+r.W*0.42, r.Y+r.H*0.76).LineTo(r.X+r.W*0.84, r.Y+r.H*0.26)
		path.Cap(ui.RoundCap).Join(ui.RoundJoin)
		pt.StrokePath(&path, max(1.6, r.W*0.13), col)
	})
	return b
}

// maskEmail shows an email as the code went to it, its name but the first
// letter hidden: k•••@gmail.com.
func maskEmail(email string) string {
	at := strings.Index(email, "@")
	if at < 1 {
		return email
	}
	r := []rune(email[:at])
	return string(r[0]) + "•••" + email[at:]
}

// onlyDigits keeps the digits of s, at most n.
func onlyDigits(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) && b.Len() < n {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// diagnosticsRow is the last line of this computer's "更多" (DESIGN §6.14):
// "导出诊断包", and what is in it and what is not; while it is put
// together, "正在收集…"; then the toast says where it went, with the way to
// see it there. The engine puts it together (a.diagnostics); the demo,
// which has none, only pretends, for as long as it usually takes.
func (a *app) diagnosticsRow(c *ui.Context, p *palette) {
	now := c.Now()
	busy := a.diagBusy || a.diagnostics == nil && !a.diagAt.IsZero() && now.Sub(a.diagAt) < 1300*time.Millisecond
	if a.diagnostics == nil && !a.diagAt.IsZero() && !busy {
		a.diagAt = time.Time{}
		a.diagSaved(c, filepath.Join(downloadsDir(), diagName(now)), nil)
	}
	ui.Column(c.Key("diag")).Margin(10, 0, 0, 0).Gap(2).Children(func() {
		b := ui.ButtonBase(c.Key("diag-button")).Height(28).Padding(0, 4).Gap(6).Radius(8).FocusRing(false).AlignSelf(ui.Start).AlignItems(ui.Center)
		if b.Hovered() && !busy {
			b.Background(p.well)
		}
		b.Children(func() {
			if busy {
				if a.diagnostics == nil {
					c.After(1300*time.Millisecond - now.Sub(a.diagAt))
				}
				spinner(c, p, now)
				ui.Text(c, "正在收集…").FontSize(12).TextColor(p.ink2)
				return
			}
			ui.Icon(c, uiGlyphs["inbox"]).Size(14, 14).TextColor(p.primary)
			ui.Text(c, "导出诊断包").FontSize(12).FontWeight(500).TextColor(p.primary)
		})
		if b.Clicked() && !busy {
			a.diagAt = now
			if put := a.diagnostics; put != nil {
				a.diagBusy = true
				go func() {
					path, err := put()
					a.post(func(c *ui.Context) {
						a.diagBusy, a.diagAt = false, time.Time{}
						a.diagSaved(c, path, err)
					})
				}()
			}
		}
		ui.Text(c, "里面有版本、网络状况和最近的连接记录，没有密钥，也没有你传的东西").FontSize(11).TextColor(p.ink3).Padding(0, 4, 0, 4).LineHeight(1.45)
	})
}

// diagSaved says where the diagnostics package went, with the way to see
// it in this system's file manager; or that it could not be put together.
func (a *app) diagSaved(c *ui.Context, path string, err error) {
	if err != nil {
		log.Printf("deneb: the diagnostics package: %v", err)
		a.notify(c, nil, "诊断包没导出来，过一会儿再试", nil)
		return
	}
	show := map[string]string{"windows": "在资源管理器中显示", "linux": "打开所在文件夹"}[a.platform]
	if show == "" {
		show = "在访达中显示"
	}
	a.notify(c, nil, "诊断包存到了「下载」", func() {
		switch {
		case a.reveal != nil:
			a.reveal(path)
		case a.openURL != nil:
			a.openURL("file://" + filepath.Dir(path))
		}
	})
	a.toast.undoLabel = show
}

// diagName is the diagnostics package's file name, put together at t:
// "Deneb 诊断包 2026-11-09 23-41.zip".
func diagName(t time.Time) string {
	return "Deneb 诊断包 " + t.Format("2006-01-02 15-04") + ".zip"
}

// downloadsDir is where the diagnostics package goes: the person's
// downloads.
func downloadsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// sendCode asks the server to send the code to the email the person typed:
// the demo's goes at once; a server whose pin the window does not know is
// signed in to in the browser.
func (a *app) sendCode(c *ui.Context, now time.Time) {
	o := a.onboard
	if a.signer == nil {
		o.sentAt = now
		return
	}
	o.sending, o.trouble = true, ""
	email := strings.TrimSpace(o.email)
	run(a, c, func(ctx context.Context) (struct{}, error) { return struct{}{}, a.signer.SendCode(ctx, email) },
		func(c *ui.Context, _ struct{}, err error) {
			o.sending = false
			switch {
			case err == nil:
				o.sentAt = c.Now()
			case errors.Is(err, client.ErrUseBrowser):
				o.viaBrowser = true
				a.startLogin(c)
			default:
				o.trouble = troubleWords(err)
			}
		})
}

// checkCode hands the server the code: signed in, the next step comes in;
// a wrong one shakes the boxes, which empty for another try.
func (a *app) checkCode(c *ui.Context, code string) {
	o := a.onboard
	o.checking, o.trouble = true, ""
	email := strings.TrimSpace(o.email)
	run(a, c, func(ctx context.Context) (struct{}, error) { return struct{}{}, a.signer.CheckCode(ctx, email, code) },
		func(c *ui.Context, _ struct{}, err error) {
			o.checking = false
			now := c.Now()
			switch {
			case err == nil:
				o.okAt = now
				o.next = now.Add(700 * time.Millisecond)
			case errors.Is(err, client.ErrWrongCode):
				o.code, o.wrongAt = "", now
			default:
				o.code, o.trouble = "", troubleWords(err)
			}
		})
}

// signLine is the line under "发验证码": what went wrong, or where to sign
// in when the browser does it.
func (a *app) signLine() string {
	o := a.onboard
	switch {
	case o.sending:
		return "正在发…"
	case o.viaBrowser:
		return "在浏览器里登录完，这里会自己往下走"
	}
	return o.trouble
}

func (a *app) signLineColor(p *palette) ui.Color {
	if o := a.onboard; o.trouble != "" && !o.sending && !o.viaBrowser {
		return ui.Hex("#ef4444")
	}
	return p.ink3
}

// troubleWords says what went wrong in a sentence: the server's own words
// when it said some, else that it could not be reached.
func troubleWords(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "连不上") || strings.Contains(msg, "dial") || strings.Contains(msg, "timeout") {
		return "连不上 Deneb 服务器，过一会儿再试"
	}
	if r := []rune(msg); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return msg
}

// installAdapter puts the network adapter in as an administrator (the
// Windows service, whose question the system asks once): the demo's lets it
// in after a while; answered no, or failed, the step says it works without.
func (a *app) installAdapter(c *ui.Context) {
	o := a.onboard
	if a.adapter == nil {
		return
	}
	run(a, c, func(ctx context.Context) (struct{}, error) { return struct{}{}, a.putAdapterIn(ctx) },
		func(c *ui.Context, _ struct{}, err error) {
			now := c.Now()
			if err != nil {
				log.Printf("deneb: the network adapter: %v", err)
				o.asking, o.declined = time.Time{}, true
				return
			}
			o.inAt = now
			o.next = now.Add(900 * time.Millisecond)
			// In: the window's pill never tells of it, though the engine
			// that has it may not have answered yet.
			a.adapterJob.done = true
		})
}
