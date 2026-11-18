package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) settingsEntry(c *ui.Context, p *palette) {
	b := ui.ButtonBase(c.Key("settings-entry")).Size(iconButtonSize, iconButtonSize).Center().Radius(999).FocusRing(false).Label("设置").Tooltip("设置")
	if b.Hovered() || a.settingsOpen {
		b.Background(p.well2)
	}
	b.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
	b.Children(func() { ui.Icon(c, uiGlyphs["gear"]).Size(16, 16).TextColor(p.ink2) })
	if b.Clicked() {
		a.settingsOpen, a.accountOpen = true, false
	}
}

// settingsLayer is the settings' panel: what Deneb this is; its update and
// the privacy notes; deleting the account. Three parts, 16 apart, the rows
// of a part one under the other, no line between them (the user, 11-17:
// 不喜欢一堆间隔线); every row on the panel's left edge (DESIGN §3.9).
func (a *app) settingsLayer(c *ui.Context, p *palette) {
	now := c.Now()
	modalPanel(c, p, "settings", "设置", 380, a.settingsOpen, func() { a.settingsOpen = false }, func() {
		ui.Column(c).Gap(16).Children(func() {
			// As much room over and under it as a menu row's words have.
			ui.Row(c).Padding(12, 0).Gap(12).Children(func() {
				starMark(c, p, 28)
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "Deneb "+mygo.App.Version()).FontSize(15).FontWeight(600)
					ui.Text(c, settingsPlatform()).FontSize(12).TextColor(p.ink3)
				})
			})
			ui.Column(c).Children(func() {
				// Whether it is the latest, where 软件更新 says what it is now.
				if menuItemWith(c, p, "settings-update", "软件更新", p.ink, "", func() { a.updateValue(c, p, now) }).Clicked() {
					if a.updates != nil {
						a.updates.click()
					}
				}
				if menuItem(c, p, "settings-privacy", "隐私说明", "", p.ink, "external").Clicked() && a.openURL != nil {
					a.openURL(privacyURL)
				}
			})
			if menuItem(c, p, "settings-delete-account", "注销账号", "", danger, "").Clicked() {
				a.settingsOpen = false
				a.openAccountDelete(c)
			}
		})
	})
}

// updateValue is what 软件更新 says now, at its row's end: nothing before
// a check; while one is under way past busyDelay, a ring and 正在检查;
// then 已是最新版本. Each in its turn, the old fading out before the new
// fades in; a check that finds the same says it again, so that a click on
// the row always shows its check done (the user, 11-17).
func (a *app) updateValue(c *ui.Context, p *palette, now time.Time) {
	u := a.updates
	if u != nil {
		u.checkBusy.follow(now, u.stage == updateChecking)
	}
	want := ""
	switch {
	case u == nil:
	case u.checkBusy.on(c, now):
		want = "\x00正在检查"
	case u.stage == updateChecking:
		want = a.updateShown // as it was, till the check shows
	case u.stage == updateCurrent:
		want = fmt.Sprintf("%d\x01%s", u.checks, u.title())
	}
	a.updateShown = want
	shown, alpha, moving := a.updateWords.step(want, now)
	if moving {
		c.AnimationFrame()
	}
	words := shown
	switch {
	case strings.HasPrefix(shown, "\x00"):
		words = shown[1:]
	case shown != "":
		words = shown[strings.Index(shown, "\x01")+1:]
	}
	// At the row's end by what shows of it, as the ↗ under it is
	// (inkRoom).
	ui.Box(c.Key("update-value")).Opacity(alpha).Margin(0, -inkRoom(words, 12), 0, 0).Children(func() {
		switch {
		case strings.HasPrefix(shown, "\x00"):
			busyValue(c, words, p.ink3, now)
		case shown != "":
			ui.Text(c, words).FontSize(12).TextColor(p.ink3).SingleLine()
		}
	})
}

func settingsPlatform() string {
	system := runtime.GOOS
	switch system {
	case "darwin":
		system = "macOS"
	case "windows":
		system = "Windows"
	case "linux":
		system = "Linux"
	}
	arch := runtime.GOARCH
	switch arch {
	case "arm64":
		arch = "ARM64"
	case "amd64":
		arch = "x64"
	}
	return system + " · " + arch
}
