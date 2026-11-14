package main

import (
	"context"
	"log"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// A computer signed in whose engine has no network adapter, where the window
// puts it in (Windows): an amber pill tells of it, and its card puts it in
// the way the first time's step does (DESIGN §7.3, "已登录的电脑装网卡",
// M6's second batch). Signed in with LANs already, a computer gets no first
// time's steps: Deneb reinstalled over its identity, or the step put off,
// it stayed in user space, where the engine on Windows only goes through
// the relays, with nowhere in the window to put the adapter in.

// adapterNotice is the pill's and the card's words. "以后再说" puts it away
// as "知道了" does the engine's: until Deneb starts again.
var adapterNotice = model.Notice{Code: "adapter", Short: "还没装网卡", Message: "这台电脑还没装网卡",
	Detail: "装上以后，浏览器、远程桌面、游戏能直接访问局域网里的 IP 和设备名称。没装时，在 Deneb 里点「打开」访问服务。"}

// adapterJob is the adapter put in from its card: since when the system
// asks the person, since when it is in; declined, the person said no, or it
// failed; done, it went in and the pill went, for as long as the window
// runs.
type adapterJob struct {
	asking, inAt   time.Time
	declined, done bool
}

const (
	// adapterAsk is how long the demo's system takes to let the adapter in,
	// as in the first time's step.
	adapterAsk = 1800 * time.Millisecond
	// adapterShown is how long "网卡装好了" shows before the pill and the
	// card go together.
	adapterShown = 1200 * time.Millisecond
	// adapterWait is how long putting the adapter in may take: Windows's
	// question waits for the person, however long they take to answer.
	adapterWait = 10 * time.Minute
)

// adapterMissing reports whether the pill tells of the adapter: where the
// window puts it in (in the demo, where it shows Windows), signed in, the
// engine without one; and while it goes in, and a moment after, whatever
// the engine says meanwhile, which stops and starts again as the service.
func (a *app) adapterMissing() bool {
	j := &a.adapterJob
	switch {
	case a.adapter == nil && (a.signer != nil || a.platform != "windows"), j.done:
		return false
	case !j.asking.IsZero() || !j.inAt.IsZero():
		return true
	}
	return !a.snap.Kernel && a.snap.LoggedIn && a.snap.Loaded
}

// tickAdapter moves the adapter's install on, each frame: the demo's system
// lets it in after a while; once in, "网卡装好了" shows a moment, then the
// pill and the card go together.
func (a *app) tickAdapter(c *ui.Context) {
	j := &a.adapterJob
	now := c.Now()
	if a.adapter == nil && !j.asking.IsZero() {
		if d := now.Sub(j.asking); d < adapterAsk {
			c.After(adapterAsk - d)
		} else {
			j.asking, j.inAt = time.Time{}, now
		}
	}
	if j.inAt.IsZero() || j.done {
		return
	}
	if d := now.Sub(j.inAt); d < adapterShown {
		c.After(adapterShown - d)
		return
	}
	if n := a.notice(); a.selected == noticeCard && n != nil && n.Code == adapterNotice.Code {
		a.selected = ""
	}
	j.done = true
}

// putAdapterIn puts the network adapter in as an administrator, given as
// long as Windows's question waits for the person: the 30 s other work
// gets would call it failed while it went in.
func (a *app) putAdapterIn(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adapterWait)
	defer cancel()
	return a.adapter(ctx)
}

// installFromNotice puts the adapter in from its card: the demo's system
// lets it in after a while (tickAdapter); answered no, or failed, the card
// says it works without.
func (a *app) installFromNotice(c *ui.Context) {
	j := &a.adapterJob
	j.asking, j.declined = c.Now(), false
	if a.adapter == nil {
		return
	}
	run(a, c, func(ctx context.Context) (struct{}, error) { return struct{}{}, a.putAdapterIn(ctx) },
		func(c *ui.Context, _ struct{}, err error) {
			j.asking = time.Time{}
			if err != nil {
				log.Printf("deneb: the network adapter: %v", err)
				j.declined = true
				return
			}
			j.inAt = c.Now()
		})
}

// adapterCard is the adapter's card under its sentence: what Windows will
// ask, then "以后再说" and "装网卡"; while it goes in, the spinner; once
// in, the tick. One at a time, fading across in a place that keeps its
// height.
func (a *app) adapterCard(c *ui.Context, p *palette) {
	j := &a.adapterJob
	// The first time's step says it at more length, in a wider box.
	ask := "系统会请你输入开机密码或使用触控 ID，允许 Deneb 安装系统网卡。"
	if a.platform == "windows" {
		ask = "Windows 会弹窗问你，点「是」就行。"
	}
	askBox(c, p, ask)
	if j.declined {
		ui.Text(c.Key("adapter-declined"), "没装上可以先在 Deneb 里点「打开」访问，想装时再试一次").FontSize(12).TextColor(p.ink2).LineHeight(1.5).
			Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
	}
	fade := ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	ui.Box(c.Key("adapter-ways")).Height(34).Margin(4, 0, 0, 0).Children(func() {
		way := func(key string) ui.Element {
			return ui.Row(c.Key(key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Gap(8).AlignItems(ui.Center).Transition(fade)
		}
		switch {
		case !j.inAt.IsZero():
			way("adapter-in").Children(func() {
				ui.Spacer(c)
				checkMark(c, p.ok, 16)
				ui.Text(c, "网卡装好了").FontSize(13).FontWeight(500).TextColor(p.ok)
			})
		case !j.asking.IsZero():
			way("adapter-asking").Children(func() {
				ui.Spacer(c)
				ui.Row(c).Gap(8).Height(34).Padding(0, 14).Radius(999).Background(p.well).AlignItems(ui.Center).Children(func() {
					spinner(c, p, c.Now())
					ui.Text(c, "等你在系统里点允许…").FontSize(13).FontWeight(500).TextColor(p.ink2)
				})
			})
		default:
			way("adapter-idle").Children(func() {
				ui.Spacer(c)
				if ghostButton(c, p, "adapter-later", "以后再说").Clicked() {
					if a.noticesDone == nil {
						a.noticesDone = map[string]bool{}
					}
					a.noticesDone[adapterNotice.Code], a.selected = true, ""
				}
				if primaryButton(c, p, "adapter-install", "装网卡", 34, 14, 13).Clicked() {
					a.installFromNotice(c)
				}
			})
		}
	})
}
