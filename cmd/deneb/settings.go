package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) settingsEntry(c *ui.Context, p *palette) {
	b := ui.ButtonBase(c.Key("settings-entry")).Size(iconButtonSize, iconButtonSize).Center().Radius(999).FocusRing(false).Label("设置").Tooltip("设置")
	if b.Hovered() || a.settingsOpen {
		b.Background(p.well2)
	}
	b.Children(func() { ui.Icon(c, uiGlyphs["gear"]).Size(16, 16).TextColor(p.ink2) })
	if b.Clicked() {
		a.settingsOpen, a.accountOpen = true, false
	}
}

// settingsLayer is the settings' panel: what Deneb this is; its update and
// the privacy notes; deleting the account. Each part apart from the next by
// a line with the same room above and below it; the rows of a part one
// under the other; every row on the panel's left edge (DESIGN §3.9).
func (a *app) settingsLayer(c *ui.Context, p *palette) {
	modalPanel(c, p, "settings", "设置", 380, a.settingsOpen, func() { a.settingsOpen = false }, func() {
		ui.Column(c).Children(func() {
			// As much room over and under it as a menu row's words have.
			ui.Row(c).Padding(12, 0).Gap(12).Children(func() {
				starMark(c, p, 28)
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "Deneb "+mygo.App.Version()).FontSize(15).FontWeight(600)
					ui.Text(c, settingsPlatform()).FontSize(12).TextColor(p.ink3)
				})
			})
			rule(c, p)
			// Whether it is the latest, where 软件更新 says what it is now.
			status := ""
			if u := a.updates; u != nil && (u.stage == updateCurrent || u.stage == updateChecking) {
				status = u.title()
			}
			if menuItem(c, p, "settings-update", "软件更新", status, p.ink, "").Clicked() {
				if a.updates != nil {
					a.updates.click()
				}
			}
			if menuItem(c, p, "settings-privacy", "隐私说明", "", p.ink, "external").Clicked() && a.openURL != nil {
				a.openURL(privacyURL)
			}
			rule(c, p)
			if menuItem(c, p, "settings-delete-account", "注销账号", "", danger, "").Clicked() {
				a.settingsOpen = false
				a.openAccountDelete(c)
			}
		})
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
