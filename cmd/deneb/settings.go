package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) settingsEntry(c *ui.Context, p *palette) {
	b := ui.ButtonBase(c.Key("settings-entry")).Size(32, 34).Center().Radius(999).FocusRing(false).Label("设置").Tooltip("设置")
	if b.Hovered() || a.settingsOpen {
		b.Background(p.well2)
	}
	b.Children(func() { ui.Icon(c, uiGlyphs["gear"]).Size(18, 18).TextColor(p.ink2) })
	if b.Clicked() {
		a.settingsOpen, a.accountOpen = true, false
	}
}

func (a *app) settingsLayer(c *ui.Context, p *palette) {
	modalPanel(c, p, "settings", "设置", 380, a.settingsOpen, func() { a.settingsOpen = false }, func() {
		ui.Row(c).Margin(20, 0, 6, 0).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, starGlyph).Size(28, 28).TextColor(p.primary)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "Deneb "+mygo.App.Version()).FontSize(15).FontWeight(600)
				ui.Text(c, settingsPlatform()).FontSize(12).TextColor(p.ink3)
			})
		})
		rule(c, p)
		if menuItem(c, p, "settings-update", "软件更新", p.ink, "").Clicked() {
			if a.updates != nil {
				a.updates.click()
			}
		}
		if u := a.updates; u != nil && (u.stage == updateCurrent || u.stage == updateChecking) {
			ui.Text(c, u.title()).FontSize(12).TextColor(p.ink3).Padding(0, 12, 8, 12)
		}
		if menuItem(c, p, "settings-privacy", "隐私说明", p.ink, "external").Clicked() && a.openURL != nil {
			a.openURL(privacyURL)
		}
		rule(c, p)
		if menuItem(c, p, "settings-delete-account", "注销账号", danger, "").Clicked() {
			a.settingsOpen = false
			a.openAccountDelete(c)
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
