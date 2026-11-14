package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

func (a *app) setMyDevices(open bool) {
	a.myDevicesOpen = open
	if store, ok := a.store.(interface{ MonitorDevices(bool) }); ok {
		store.MonitorDevices(open)
	}
	if open {
		a.selected = ""
		a.more = false
		a.invite = nil
	}
}

func (a *app) myDevicesPage(c *ui.Context, p *palette) {
	ww, wh := c.Size()
	a.cw, a.ch = ww-sidebarWidth, wh-a.strip
	page := ui.Column(c.Key("my-devices-page")).Grow(1).FillWidth()
	if b := page.Bounds(); b.W > 0 {
		a.origin = ui.Point{X: b.X, Y: b.Y}
	}
	page.Children(func() { a.myDevicesContent(c, p); a.toastView(c, p, a.cw, a.ch, c.Now()) })
}
func (a *app) myDevicesContent(c *ui.Context, p *palette) {
	ui.Row(c.Key("devices-header")).Height(headerHeight).Shrink(0).Padding(14, 28).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "我的设备").FontSize(22).FontWeight(650)
			online := 0
			for _, id := range a.snap.Mine {
				if d := a.snap.Device(id); d != nil && d.Online {
					online++
				}
			}
			ui.Text(c, fmt.Sprintf("%d 台设备 · %d 台在线", len(a.snap.Mine), online)).FontSize(12).TextColor(p.ink3)
		})
		label := "刷新"
		if a.snap.MetricsLoading {
			label = "刷新中"
		}
		if dialogButton(c, p, "refresh-devices", label, false, false, a.snap.MetricsLoading).Clicked() {
			if store, ok := a.store.(interface{ RefreshDeviceMetrics() }); ok {
				store.RefreshDeviceMetrics()
			}
		}
	})
	rule(c, p)
	ui.Scroll(c.Key("device-page-scroll")).Grow(1).Children(func() {
		ui.Column(c).Padding(24, 28).Gap(16).Children(func() {
			if a.snap.MetricsError != "" {
				ui.Text(c, a.snap.MetricsError).FontSize(12).TextColor(p.ink3)
			}
			if len(a.snap.Mine) == 0 {
				ui.Box(c).Height(240).Center().Children(func() { ui.Text(c, "暂无设备").FontSize(15).TextColor(p.ink3) })
			}
			for _, id := range a.snap.Mine {
				d := a.snap.Device(id)
				if d != nil && d.Owner == a.snap.Me {
					a.deviceMonitorRow(c, p, d)
				}
			}
		})
	})
}
func (a *app) deviceMonitorRow(c *ui.Context, p *palette, d *model.Device) {
	sample, has := a.snap.DeviceMetrics[d.ID]
	now := c.Now()
	stale := !d.Online || !a.snap.Control && d.ID != a.snap.Self || has && now.Sub(sample.At) > 45*time.Second
	ui.Column(c.Key("host-" + string(d.ID))).Padding(18).Gap(16).Radius(16).Background(p.well).Transition(ui.ElementTransition{Animation: &ui.Smooth, Size: true}).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			avatar := ui.ButtonBase(c.Key("avatar")).Size(44, 44).Radius(13).Label("修改" + d.Name + "头像")
			avatar.Children(func() { a.profileTile(c, p, d.ID, glyphFor(d.Kind), ownerColors[0], 44, false, 0) })
			if avatar.Clicked() {
				a.openAvatar(d.ID)
			}
			ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
				ui.Text(c, d.Name).FontSize(15).FontWeight(600).SingleLine()
				status := "离线"
				if d.Online {
					status = "在线"
				}
				if d.ID == a.snap.Self {
					status = "本机"
				}
				if d.Addr != "" {
					status += " · " + d.Addr
				}
				ui.Text(c, status).FontSize(12).TextColor(p.ink3).SingleLine()
			})
			edit := ui.ButtonBase(c.Key("rename")).Height(30).Padding(0, 10).Radius(999).Label("重命名" + d.Name)
			edit.Children(func() { ui.Text(c, "重命名").FontSize(12).TextColor(p.ink2) })
			if edit.Clicked() {
				a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.ID}, d.Name)
			}
			details := "详情"
			if a.deviceDetails == d.ID {
				details = "收起"
			}
			if dialogButton(c, p, "details", details, false, false, false).Label(details + d.Name).Clicked() {
				if a.deviceDetails == d.ID {
					a.deviceDetails = ""
				} else {
					a.deviceDetails = d.ID
				}
			}
		})
		if !has {
			message := "尚未上报 · 请确认设备引擎已更新"
			if !d.Online {
				message = "设备离线"
			} else if a.snap.MetricsError != "" {
				message = "需更新设备引擎"
			}
			ui.Text(c, message).FontSize(12).TextColor(p.ink3)
		} else if stale {
			ui.Text(c, "上次采样 · "+sample.At.Local().Format("01-02 15:04:05")).FontSize(12).TextColor(p.ink3)
		}
		ui.Row(c).Gap(14).Children(func() {
			cpu := "—"
			if has && sample.CPU != nil {
				cpu = fmt.Sprintf("%.0f%%", *sample.CPU*100)
			}
			memory := "—"
			memorySub := "未采集"
			if has && sample.Memory != nil {
				memory = hostBytes(sample.Memory.Total - sample.Memory.Available)
				memorySub = "共 " + hostBytes(sample.Memory.Total)
			}
			disk := "—"
			diskSub := "未采集"
			if has && len(sample.Disks) > 0 {
				disk = hostBytes(sample.Disks[0].Available)
				diskSub = "共 " + hostBytes(sample.Disks[0].Total) + " · 可用"
			}
			deviceMetric(c, p, "CPU", cpu, "使用率", stale)
			deviceMetric(c, p, "内存", memory, memorySub, stale)
			deviceMetric(c, p, "存储", disk, diskSub, stale)
			rtt, path := a.deviceLatency(d)
			deviceMetric(c, p, "延迟", rtt, path, false)
		})
		if a.deviceDetails == d.ID {
			rule(c, p)
			ui.Column(c.Key("host-details")).Gap(10).Transition(ui.ElementTransition{Animation: &ui.Smooth, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Y: 4}}).Children(func() {
				value := sample.OSVersion
				if !has || value == "" {
					value = d.OS
				}
				if value == "" {
					value = "未采集"
				}
				monitorFact(c, p, "系统", value)
				if has {
					processor := sample.CPUModel
					if processor == "" {
						processor = "未采集"
					}
					monitorFact(c, p, "处理器", processor)
					monitorFact(c, p, "配置", fmt.Sprintf("%d 核 · %s", sample.CPUs, sample.Arch))
					if sample.Memory != nil {
						monitorFact(c, p, "可用内存", hostBytes(sample.Memory.Available)+" / "+hostBytes(sample.Memory.Total))
					}
					for _, disk := range sample.Disks {
						monitorFact(c, p, disk.Name, hostBytes(disk.Available)+" 可用 / "+hostBytes(disk.Total))
					}
					if sample.Uptime != nil {
						monitorFact(c, p, "已运行", hostUptime(*sample.Uptime))
					}
					if sample.EngineVersion != "" {
						monitorFact(c, p, "引擎", sample.EngineVersion)
					}
					monitorFact(c, p, "采样时间", sample.At.Local().Format("2006-01-02 15:04:05"))
					if len(sample.Errors) > 0 {
						ui.Text(c, strings.Join(sample.Errors, " · ")).FontSize(11).TextColor(p.ink3)
					}
				}
				monitorFact(c, p, "地址", d.Addr)
				a.deleteRow(c, p, d.ID)
			})
		}
	})
}
func deviceMetric(c *ui.Context, p *palette, label, value, sub string, stale bool) {
	ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.ink3)
		ink := p.ink
		if stale {
			ink = p.ink3
		}
		ui.Text(c, value).FontSize(21).FontWeight(600).TextColor(ink).SingleLine()
		ui.Text(c, sub).FontSize(11).TextColor(p.ink3).SingleLine()
	})
}
func monitorFact(c *ui.Context, p *palette, label, value string) {
	if value == "" {
		value = "—"
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(88).Shrink(0).FontSize(12).TextColor(p.ink3)
		ui.Text(c, value).Grow(1).Shrink(1).FontSize(12).TextColor(p.ink2)
	})
}
func (a *app) deviceLatency(d *model.Device) (string, string) {
	if d.ID == a.snap.Self {
		return "—", "本机"
	}
	if !d.Online {
		return "—", "离线"
	}
	path, ok := a.snap.Paths[d.ID]
	if !ok {
		return "—", "无共同网络"
	}
	if path.Kind == model.PathApart {
		return "—", "无共同网络"
	}
	if !path.Kind.Connected() {
		return "—", "未连通"
	}
	if measured, ok := a.snap.DeviceLatencies[d.ID]; ok && time.Since(measured.At) <= 45*time.Second {
		if measured.Failed {
			return "—", "探测超时"
		}
		name := "直连"
		if path.Kind == model.PathRelay {
			name = "中转"
		}
		return fmt.Sprintf("%.1f ms", float64(measured.RTT)/float64(time.Millisecond)), name
	}
	if path.RTT <= 0 {
		return "—", "尚未测得"
	}
	name := "直连"
	if path.Kind == model.PathRelay {
		name = "中转"
	}
	return fmt.Sprintf("%.1f ms", float64(path.RTT)/float64(time.Millisecond)), name
}
func hostBytes(n uint64) string {
	for _, u := range []struct {
		n uint64
		s string
	}{{1 << 40, "TB"}, {1 << 30, "GB"}, {1 << 20, "MB"}, {1 << 10, "KB"}} {
		if n >= u.n {
			return fmt.Sprintf("%.1f %s", float64(n)/float64(u.n), u.s)
		}
	}
	return fmt.Sprintf("%d B", n)
}
func hostUptime(s uint64) string {
	if s >= 86400 {
		return fmt.Sprintf("%d 天 %d 小时", s/86400, s%86400/3600)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", s/3600, s%3600/60)
}
