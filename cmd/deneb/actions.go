package main

import (
	"context"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// openService opens service s of d with this computer's app for it
// (§2.7): the engine gets the service ready (in userspace mode, a port of
// its own on this computer that leads there), and the system opens the
// address it answers with: a remote desktop in the remote desktop app, a
// shared folder in the file manager, a website in the browser.
func (a *app) openService(c *ui.Context, d *device, s service) {
	run(a, c, func(ctx context.Context) (string, error) { return a.store.Open(ctx, d.dev, s.svc) },
		func(c *ui.Context, addr string, err error) {
			if err != nil {
				a.problem(c, err)
				return
			}
			if a.openURL == nil || a.openURL(serviceURL(s.svc, addr)) != nil {
				a.notify(c, d, "这台电脑上没有能打开"+s.name+"的程序", nil)
			}
		})
}

// serviceURL is the link the system opens a service at addr with: one its
// apps know by the kind of service and its port.
func serviceURL(s model.Service, addr string) string {
	switch s.Kind {
	case "screen":
		if s.Port == 5900 {
			// Screen sharing, as macOS has it.
			return "vnc://" + addr
		}
		// Remote desktop, in the form Microsoft's apps take on every system.
		return "rdp://full%20address=s:" + url.PathEscape(addr)
	case "files":
		return "smb://" + addr
	case "shell":
		return "ssh://" + addr
	}
	if s.Port == 443 || strings.HasSuffix(addr, ":443") {
		return "https://" + addr
	}
	return "http://" + addr
}

// inviteFriends puts a link into the LAN on the clipboard, for the person to
// send to a friend: opening it, the friend is asked into the LAN.
func (a *app) inviteFriends(c *ui.Context) {
	lan := a.lan
	run(a, c, func(ctx context.Context) (model.Result, error) {
		return a.store.Do(ctx, model.Op{Kind: model.OpLANInvite, LAN: lan})
	}, func(c *ui.Context, r model.Result, err error) {
		if err != nil {
			a.problem(c, err)
			return
		}
		if a.copyText != nil {
			a.copyText(r.Link)
		}
		a.notify(c, nil, "邀请链接已复制，发给朋友就行", nil)
	})
}

// short cuts a name for a sentence: one too long to read at a glance in a
// toast ends in "…". Chinese characters count as two Latin letters, about
// as wide as they are.
func short(name string) string {
	const most = 24
	w, cut := 0, -1
	for i, r := range name {
		cw := 1
		if r >= 0x1100 {
			cw = 2
		}
		if w+cw > most-1 && cut < 0 {
			cut = i
		}
		w += cw
		if w > most {
			return name[:cut] + "…"
		}
	}
	return name
}
