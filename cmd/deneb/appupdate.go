package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var downloadGlyph = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M7 16H6a4 4 0 0 1-.5-8A6.5 6.5 0 0 1 18 7.5a4.3 4.3 0 0 1 .5 8.5H17M12 10v11m-4-4 4 4 4-4"/></svg>`))

type updateStage string

const (
	updateIdle        updateStage = "idle"
	updateChecking    updateStage = "checking"
	updateAvailable   updateStage = "available"
	updateDownloading updateStage = "downloading"
	updateVerifying   updateStage = "verifying"
	updateReady       updateStage = "ready"
	updateFailed      updateStage = "failed"
	updateCurrent     updateStage = "current"
)

type appRelease struct {
	version, notes string
	install        func(context.Context, func(int64, int64)) error
}
type appUpdater struct {
	app               *app
	stage             updateStage
	open              bool
	release           *appRelease
	downloaded, total int64
	err               string
	cancel            context.CancelFunc
	check             func(context.Context) (*appRelease, error)
	relaunch          func()
}

func newAppUpdater(a *app) *appUpdater {
	return &appUpdater{app: a, stage: updateIdle, check: func(ctx context.Context) (*appRelease, error) {
		up, err := mygo.Updater.Check(ctx)
		if err != nil || up == nil {
			return nil, err
		}
		return &appRelease{version: up.Version, notes: up.Notes, install: up.Install}, nil
	}, relaunch: mygo.App.Relaunch}
}
func (u *appUpdater) start() {
	if !mygo.Updater.Enabled() {
		return
	}
	go func() {
		u.app.post(func(*ui.Context) { u.beginCheck(false) })
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			u.app.post(func(*ui.Context) { u.beginCheck(false) })
		}
	}()
}
func (u *appUpdater) beginCheck(show bool) {
	if show {
		if u.stage == updateAvailable || u.stage == updateDownloading || u.stage == updateVerifying || u.stage == updateReady {
			u.open = true
			u.app.settingsOpen = false
			return
		}
		u.open = false
		u.app.settingsOpen = true
	}
	if u.stage == updateChecking || u.stage == updateDownloading || u.stage == updateVerifying || u.stage == updateReady {
		return
	}
	u.stage, u.err, u.release = updateChecking, "", nil
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	u.cancel = cancel
	go func() {
		release, err := u.check(ctx)
		cancel()
		u.app.post(func(*ui.Context) {
			u.cancel = nil
			switch {
			case err != nil:
				log.Printf("deneb: check update: %v", err)
				u.stage, u.err = updateFailed, "暂时检查不了更新，请稍后重试"
				if show && u.app.settingsOpen {
					u.open = true
					u.app.settingsOpen = false
				}
				if errors.Is(err, mygo.ErrUpdatesDisabled) {
					u.err = "这份安装包未启用在线更新，请安装支持更新的新版 Deneb"
				}
			case release == nil:
				u.stage, u.release = updateCurrent, nil
				u.open = false
			default:
				u.stage, u.release = updateAvailable, release
				if show && u.app.settingsOpen {
					u.open = true
					u.app.settingsOpen = false
				}
			}
		})
	}()
}
func (u *appUpdater) download() {
	if u.release == nil || u.stage == updateDownloading || u.stage == updateVerifying || u.stage == updateReady {
		return
	}
	u.stage, u.err, u.downloaded, u.total = updateDownloading, "", 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	release := u.release
	go func() {
		err := release.install(ctx, func(done, total int64) {
			u.app.post(func(*ui.Context) {
				if u.stage != updateDownloading && u.stage != updateVerifying {
					return
				}
				u.downloaded, u.total = done, total
				if total > 0 && done >= total {
					u.stage = updateVerifying
				} else {
					u.stage = updateDownloading
				}
			})
		})
		cancel()
		u.app.post(func(*ui.Context) {
			u.cancel = nil
			switch {
			case errors.Is(err, context.Canceled):
				u.stage, u.err = updateAvailable, "下载已取消，可以重新开始"
			case err != nil:
				log.Printf("deneb: install update: %v", err)
				u.stage, u.err = updateFailed, "更新未完成，当前版本仍可正常使用。请重试。"
			default:
				u.stage = updateReady
			}
		})
	}()
}
func (u *appUpdater) click() {
	switch u.stage {
	case updateAvailable, updateDownloading, updateVerifying, updateReady:
		u.open = true
		u.app.settingsOpen = false
	case updateFailed:
		u.open = true
		u.app.settingsOpen = false
	default:
		u.beginCheck(true)
	}
}
func (u *appUpdater) title() string {
	switch u.stage {
	case updateChecking:
		return "正在检查更新"
	case updateAvailable:
		return "有新版本可用"
	case updateDownloading:
		return "正在下载更新"
	case updateVerifying:
		return "正在验证并准备更新"
	case updateReady:
		return "更新已就绪"
	case updateFailed:
		return "更新未完成"
	case updateCurrent:
		return "已是最新版本"
	}
	return "检查更新"
}
func (a *app) updateEntry(c *ui.Context, p *palette) {
	u := a.updates
	if u == nil || u.stage != updateAvailable && u.stage != updateDownloading && u.stage != updateVerifying && u.stage != updateReady {
		return
	}
	ui.Row(c).Shrink(0).Children(func() {
		b := ui.ButtonBase(c.Key("app-update-entry")).Height(34).Radius(999).Background(p.well2).Gap(7).Center().FocusRing(false).Label(u.title())
		prominent := u.stage == updateAvailable || u.stage == updateReady || u.stage == updateDownloading || u.stage == updateVerifying
		if prominent {
			b.Background(p.primary)
		}
		expanded := b.Hovered() || u.open || u.stage == updateDownloading
		target := float32(34)
		if expanded {
			target = 112
		}
		width := b.AnimateTo("width", target, ui.Animation{Duration: 180 * time.Millisecond})
		b.Width(width).Clip()
		ink := p.ink2
		if prominent {
			ink = white
		}
		b.AnimatePose(pressPose(b, .96), ui.Snappy)
		b.Tooltip(u.title())
		b.Children(func() {
			ui.Icon(c, downloadGlyph).Size(17, 17).TextColor(ink).Shrink(0)
			if width > 50 {
				label := "更新"
				if !prominent {
					label = "检查更新"
				}
				if u.stage == updateReady {
					label = "重启更新"
				}
				if u.stage == updateDownloading && u.total > 0 {
					label = fmt.Sprintf("下载中 %d%%", u.downloaded*100/u.total)
				}
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(ink).SingleLine()
			}
		})
		if b.Clicked() {
			u.click()
		}
	})
}
func (a *app) updateLayer(c *ui.Context, p *palette) {
	u := a.updates
	if u == nil {
		return
	}
	modalPanel(c, p, "app-update", u.title(), 420, u.open, func() { u.open = false }, func() {
		message := "你正在使用 Deneb " + mygo.App.Version()
		if u.release != nil {
			message = "Deneb " + u.release.version + " 已可用"
		}
		if u.stage == updateReady {
			message = "重启以安装 Deneb " + u.release.version + "，保留设备与设置。"
		}
		ui.Text(c, message).FontSize(13).TextColor(p.ink2).LineHeight(1.5)
		if u.stage == updateAvailable && u.release.notes != "" {
			ui.Scroll(c).MaxHeight(140).Children(func() { ui.Text(c, u.release.notes).FontSize(12).TextColor(p.ink2).LineHeight(1.5) })
		}
		if u.err != "" {
			ui.Text(c, u.err).FontSize(12).TextColor(p.ink2).LineHeight(1.5)
		}
		if u.stage == updateDownloading || u.stage == updateVerifying {
			fraction := float32(0)
			if u.total > 0 {
				fraction = min(1, float32(u.downloaded)/float32(u.total))
			}
			ui.Box(c).Height(5).Radius(999).Background(p.well2).Clip().Children(func() { ui.Box(c).Height(5).Width(376 * fraction).Radius(999).Background(p.primary) })
			status := "正在连接下载服务…"
			if u.total > 0 {
				status = fmt.Sprintf("%.1f / %.1f MB · %d%%", float64(u.downloaded)/(1<<20), float64(u.total)/(1<<20), u.downloaded*100/u.total)
			}
			if u.stage == updateVerifying {
				status = "正在校验签名并准备安装…"
			}
			ui.Text(c, status).FontSize(12).TextColor(p.ink3)
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			button := func(key, label string, primary bool, fn func()) {
				b := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 20).Radius(999).FocusRing(false).Background(p.well)
				ink := p.ink
				if primary {
					b.Background(p.primary)
					ink = white
				}
				b.AnimatePose(pressPose(b, .97), ui.Snappy)
				b.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(ink).SingleLine() })
				if b.Clicked() {
					fn()
				}
			}
			switch u.stage {
			case updateAvailable:
				button("later", "稍后", false, func() { u.open = false })
				button("download", "下载更新", true, u.download)
			case updateDownloading, updateVerifying:
				button("background", "后台下载", false, func() { u.open = false })
				if u.stage == updateDownloading {
					button("cancel", "取消下载", false, func() {
						if u.cancel != nil {
							u.cancel()
						}
					})
				}
			case updateReady:
				button("later", "暂不", false, func() { u.open = false })
				button("restart", "重启并更新", true, func() { u.relaunch() })
			case updateFailed:
				button("close", "关闭", false, func() { u.open = false })
				button("retry", "重试", true, func() {
					if u.release != nil {
						u.download()
					} else {
						u.beginCheck(true)
					}
				})
			default:
				button("close", "关闭", false, func() { u.open = false })
			}
		})
	})
}

// Updates remain available before login and during first-run setup.
func (a *app) updateOutsideSidebar(c *ui.Context, p *palette) {
	if a.updates == nil {
		return
	}
	ui.Box(c.Key("login-update")).Absolute().Left(12).Bottom(12).Children(func() { a.updateEntry(c, p) })
	a.updateLayer(c, p)
}
