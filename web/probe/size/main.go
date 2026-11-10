//go:build js

// Size probe: what a Deneb web app costs at least. MyGo's UI, the user view
// decoded from JSON into proto's types, fetched with the browser's fetch.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"syscall/js"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/proto"
)

type app struct {
	v    proto.UserView
	err  string
	name string
	w    *mygo.Window
}

func (a *app) load(path string) {
	var onText, onResp js.Func
	onText = js.FuncOf(func(_ js.Value, b []js.Value) any {
		var v proto.UserView
		if err := json.Unmarshal([]byte(b[0].String()), &v); err != nil {
			a.err = err.Error()
		} else {
			a.v = v
		}
		if a.w != nil {
			a.w.Update(func() {})
		}
		return nil
	})
	onResp = js.FuncOf(func(_ js.Value, r []js.Value) any { return r[0].Call("text").Call("then", onText) })
	js.Global().Call("fetch", path).Call("then", onResp)
}

func (a *app) post(op proto.Op) {
	b, _ := json.Marshal(op)
	opts := js.Global().Get("Object").New()
	opts.Set("method", "POST")
	opts.Set("body", string(b))
	js.Global().Call("fetch", "/api/v1/ops", opts)
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		ui.Text(c, "设备共享").FontSize(22).Bold()
		ui.Text(c, "把设备拖到成员身上，就分享给他了").TextColor(t.TextMuted)
		if a.err != "" {
			ui.Text(c, a.err)
		}
		ui.Row(c).Gap(16).Children(func() {
			ui.Column(c).Gap(8).Padding(12).Radius(12).Background(t.SurfaceHover).Children(func() {
				for _, d := range a.v.Devices {
					el := ui.Column(c.Key(string(d.ID))).Padding(10).Radius(10).Background(t.Surface).Children(func() {
						ui.Text(c, d.Name).Bold()
					})
					if _, _, ok := el.Dragged(); ok {
						a.post(proto.Op{Op: "share.create"})
					}
				}
			})
			ui.Column(c).Gap(8).Children(func() {
				for _, p := range a.v.People {
					ui.Row(c.Key(string(p.ID))).Gap(8).Children(func() {
						ui.Text(c, p.Name)
						ui.Textf(c, "%d 台", len(a.v.SharedWithMe))
					})
				}
			})
		})
		ui.TextInput(c.Key("name"), &a.name)
		ui.Text(c, fmt.Sprintf("seats %d/%d", a.v.Seats.Used, a.v.Seats.Limit))
	})
}

func main() {
	a := &app{}
	a.load("/api/v1/view")
	mygo.App.WhenReady(func() {
		a.w = mygo.NewWindow(mygo.WindowOptions{Title: "Deneb", Content: ui.View(a.view)})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
