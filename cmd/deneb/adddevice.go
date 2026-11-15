package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// "添加设备" (design/plan/m7/mydevices.md §7): three steps, in plain
// words, to bring another computer of mine in; it waits for that computer,
// and says so when it comes, open or not.

// siteAddress is where the other computer gets Deneb: the deployment's web
// app (the docs and demos write deneb.app).
const siteAddress = "deneb.app"

// linuxInstall is how Linux takes the two packages at once: the window's,
// which needs the engine's.
const linuxInstall = "sudo apt install ./deneb.deb ./denebd.deb"

// addOne is the card: open or not, which devices were there when it opened
// (one more is the one that came), and its little states.
type addOne struct {
	open      bool
	known     map[model.DeviceID]bool
	came      model.DeviceID
	cameAt    time.Time
	blocked   bool      // "首次打开时被系统阻止" open
	helpH     float32   // the help's height open, as last laid out
	copiedURL time.Time // "已复制" for 2 s
	copiedCmd time.Time
}

func (a *app) openAddOne(c *ui.Context) {
	known := map[model.DeviceID]bool{}
	for _, id := range a.snap.Mine {
		known[id] = true
	}
	a.addOne = &addOne{open: true, known: known}
	a.svcEdit = nil
}

// watchArrivals runs every frame: a device of mine that was not there when
// "添加设备" last opened has come. Open, the card ticks its third step and
// goes; closed, a toast says so. Its card on My Devices lights up.
func (a *app) watchArrivals(c *ui.Context) {
	o := a.addOne
	if o == nil {
		return
	}
	now := c.Now()
	for _, id := range a.snap.Mine {
		if o.known[id] {
			continue
		}
		o.known[id] = true
		d := a.snap.Device(id)
		if d == nil || o.came != "" {
			continue
		}
		o.came, o.cameAt = id, now
		if a.cardFlash == nil {
			a.cardFlash = map[model.DeviceID]time.Time{}
		}
		a.cardFlash[id] = now.Add(1200 * time.Millisecond)
		if !o.open {
			// Closed, the user is at something else: a toast, and the list
			// stays where it is.
			a.notify(c, nil, short(d.Name)+"已加入", nil)
		}
	}
	if o.open && o.came != "" {
		if left := o.cameAt.Add(1200 * time.Millisecond).Sub(now); left > 0 {
			c.After(left)
		} else {
			// The card goes, and the list glides to the new one as its
			// light fades.
			o.open = false
			a.revealCard, a.revealBy, a.revealTo = o.came, now.Add(900*time.Millisecond), 0
		}
	}
	if !o.open && o.came != "" && now.Sub(o.cameAt) > 2*time.Second {
		// Done: the next one is told of when "添加设备" opens again.
		a.addOne = nil
	}
}

// maskEmail shows the first letter and the domain: k•••@example.com.
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return email
	}
	return email[:1] + "•••" + email[at:]
}

func (a *app) addOneLayer(c *ui.Context, p *palette) {
	a.watchArrivals(c)
	o := a.addOne
	open := o != nil && o.open
	close := func() {
		if a.addOne != nil {
			a.addOne.open = false
		}
	}
	modalPanel(c, p, "add-one", "添加设备", 480, open, close, func() {
		now := c.Now()
		// A title and the three steps, no more (design/components.md 对话框).
		ui.Column(c).Gap(16).Padding(4, 0, 0, 0).Children(func() {
			addStep(c, p, 1, false, func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "在另一台电脑上打开 "+siteAddress).FontSize(13).FontWeight(500).Grow(1)
					label := "复制网址"
					if now.Sub(o.copiedURL) < 2*time.Second {
						label = "已复制"
						c.After(2*time.Second - now.Sub(o.copiedURL))
					}
					b := textButton(c, p, "copy-url", label, true)
					if b.Clicked() {
						if a.copyText != nil {
							a.copyText("https://" + siteAddress)
						}
						o.copiedURL = now
					}
				})
			})
			addStep(c, p, 2, false, func() {
				ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "下载并安装 Deneb").FontSize(13).FontWeight(500)
				})
				// The toggle and its help, with no gap between: the help
				// brings its own as it opens.
				ui.Column(c).Children(func() {
					more := ui.ButtonBase(c.Key("blocked")).Height(24).Gap(4).FocusRing(false).AlignSelf(ui.Start).Label("首次打开时被系统阻止")
					ink := p.ink3
					if more.Hovered() || o.blocked {
						ink = p.primary
					}
					shut := float32(0)
					if o.blocked {
						shut = 1
					}
					k := more.AnimateTo("open", shut, ui.Smooth)
					more.Children(func() {
						ui.Text(c, "首次打开时被系统阻止").FontSize(12).TextColor(ink)
						ui.Icon(c, uiGlyphs["chevron"]).Size(12, 12).TextColor(ink).Rotate(90 * k)
					})
					if more.Clicked() {
						o.blocked = !o.blocked
					}
					// It opens in height (DESIGN §5: Smooth), its words
					// clipped to it, and the card grows with it, centered.
					// Shut, it is still laid out, for the height it opens to.
					holder := ui.Column(c.Key("blocked-help-holder")).Height(k * o.helpH).Clip()
					if !o.blocked && k < 0.01 {
						holder.Invisible()
					}
					holder.Children(func() {
						inner := ui.Column(c.Key("blocked-help")).Padding(6, 0, 0, 0)
						if b := inner.Bounds(); b.H > 0 {
							o.helpH = b.H
						}
						inner.Children(func() {
							ui.Column(c).Gap(12).Padding(12).Radius(12).Background(p.well).Children(func() {
								helpLine(c, p, "Mac", "打开「系统设置」>「隐私与安全」，在「安全性」中点按「仍要打开」，再输入登录密码。")
								helpLine(c, p, "Windows", "出现「Windows 已保护你的电脑」时，点「更多信息」，再点「仍要运行」。")
								helpLine(c, p, "Linux", "需同时安装 deneb.deb 和 denebd.deb（后台服务）。在下载目录中运行：")
								// The command on one line, the panel's width:
								// wrapped, it would read as two.
								ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Row(c).Grow(1).Shrink(1).Height(32).Padding(0, 12).Radius(8).Background(p.bg).AlignItems(ui.Center).Children(func() {
										ui.Text(c, linuxInstall).Font("SF Mono, Menlo, monospace").FontSize(11.5).TextColor(p.ink).SingleLine()
									})
									label := "复制"
									if now.Sub(o.copiedCmd) < 2*time.Second {
										label = "已复制"
										c.After(2*time.Second - now.Sub(o.copiedCmd))
									}
									if textButton(c, p, "copy-cmd", label, true).Clicked() {
										if a.copyText != nil {
											a.copyText(linuxInstall)
										}
										o.copiedCmd = now
									}
								})
							})
						})
					})
				})
			})
			done := o.came != ""
			addStep(c, p, 3, done, func() {
				ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
					if done {
						name := "设备"
						if d := a.snap.Device(o.came); d != nil {
							name = short(d.Name)
						}
						ui.Text(c.Key("came"), name+"已加入").FontSize(13).FontWeight(600).TextColor(p.ok).Transition(ui.ElementTransition{Animation: &ui.Smooth, Enter: &ui.Motion{Y: 4}})
						return
					}
					login := "使用同一邮箱登录"
					if e := maskEmail(a.snap.Email); e != "" {
						login = "使用 " + e + " 登录"
					}
					ui.Text(c.Key("login"), login).FontSize(13).FontWeight(500).Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Exit: &ui.Motion{}})
				})
			})
		})
		// Once it came, "等待设备上线…" fades where it is: the card keeps its
		// size for the moment it stays.
		waiting := float32(1)
		if o.came != "" {
			waiting = 0
		}
		wait := ui.Row(c.Key("waiting")).Gap(8).AlignItems(ui.Center).Padding(4, 0, 0, 0)
		wait.Opacity(wait.AnimateTo("shown", waiting, ui.Animation{Duration: 160 * time.Millisecond}))
		wait.Children(func() {
			spinner(c, p, now)
			ui.Text(c, "等待设备上线…").FontSize(12).TextColor(p.ink3)
		})
	})
}

// addStep is one numbered step: a blue number, or a green tick once done.
func addStep(c *ui.Context, p *palette, n int, done bool, body func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		// The number on the middle of the step's first line (28 high).
		holder := ui.Box(c).Size(24, 28).Center().Shrink(0)
		holder.Children(func() { stepDot(c, p, n, done) })
		ui.Column(c).Grow(1).Shrink(1).Gap(8).Children(body)
	})
}

// stepDot is a step's number, a green tick once done.
func stepDot(c *ui.Context, p *palette, n int, done bool) {
	dot := ui.Box(c.Key("n")).Size(24, 24).Radius(999).Center().Shrink(0)
	dot.Transition(ui.ElementTransition{Animation: &ui.Snappy, Colors: true})
	if done {
		dot.Background(p.ok)
		dot.Children(func() { checkMark(c, white, 12) })
		return
	}
	dot.Background(p.primary)
	dot.Children(func() { ui.Text(c, string(rune('0'+n))).FontSize(12).FontWeight(600).TextColor(white) })
}

// helpLine is a system's name, on the first line of its words.
func helpLine(c *ui.Context, p *palette, os, text string) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Text(c, os).Width(60).Shrink(0).FontSize(12).FontWeight(600).TextColor(p.ink2).SingleLine().LineHeight(1.5)
		ui.Text(c, text).Grow(1).Shrink(1).FontSize(12).TextColor(p.ink2).LineHeight(1.5)
	})
}
