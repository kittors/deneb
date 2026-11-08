package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

func main() {
	store, at, sims, lc := pickStore()
	a := newApp(store, at)
	a.sims = sims
	if lc != nil {
		go keepEngine(a, lc)
	}
	a.copyText = mygo.Clipboard.WriteText
	a.openURL = mygo.Shell.OpenExternal
	// Invite links (deneb://join/<token>), from the landing page or pasted
	// into a browser. Registered before Run, to get the one the app opens
	// with.
	mygo.App.OnOpenURL(func(u string) { a.post(func(c *ui.Context) { a.openLink(c, u) }) })
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Deneb",
			Width:     1180,
			Height:    740,
			MinWidth:  980,
			MinHeight: 640,
			// The sidebar runs to the top of the window, under the system's
			// window buttons, as the design's frame does (DESIGN.md §7).
			TitleBarStyle: mygo.TitleBarHidden,
			// Reopens where the user left it last time. A new key: the old
			// one kept the starter template's 480×360 window.
			StateKey: "lan-window",
			// MyGo draws this window itself; no web page sits behind it.
			Content: ui.View(a.view),
		})
		// A new snapshot, or work done off the main thread, asks the window
		// for a frame built anew, which takes it in.
		a.setWake(func() { w.Update(func() {}) })
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// pickStore chooses where the world comes from, and the outside events
// ⌥⌘1 to ⌥⌘5 play where there is a world to play them in:
//
//   - deneb --demo, or DENEB_DEMO=1 (mygo dev passes no arguments): the
//     design's sample, in memory, for showing Deneb and recording it.
//   - DENEB_FAKE=ready or DENEB_FAKE=fresh: this computer's store, watching
//     a fake engine in this process with the M1 topology behind it
//     (client/fakeworld), as the dev window runs before the engine does.
//   - otherwise: this computer's store, watching its engine.
func pickStore() (client.Store, *places, map[ui.Key]func(c *ui.Context, a *app), *localclient.Client) {
	if demoMode() {
		d := client.NewDemo()
		return d, demoPlaces(), map[ui.Key]func(*ui.Context, *app){
			ui.Key1: func(*ui.Context, *app) { d.Simulate("leave") },
			ui.Key2: func(*ui.Context, *app) { d.Simulate("network") },
			ui.Key3: func(c *ui.Context, a *app) { a.openLink(c, client.DemoInvite) },
			ui.Key4: func(*ui.Context, *app) { d.Simulate("connect") },
			ui.Key5: func(*ui.Context, *app) { d.Simulate("offline") },
		}, nil
	}
	if start := os.Getenv("DENEB_FAKE"); start != "" {
		w := fakeworld.New(map[bool]fakeworld.Start{true: fakeworld.Fresh, false: fakeworld.Ready}[start == "fresh"])
		dir, err := os.MkdirTemp("", "dnb")
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(dir, "d.sock")
		if _, err := w.E.Listen(path); err != nil {
			log.Fatal(err)
		}
		l := client.NewLive(localclient.New(path))
		l.Start()
		nasIn, pcRelay, nasOn := true, false, true
		return l, &places{at: map[model.LANID]map[string]ui.Point{}}, map[ui.Key]func(*ui.Context, *app){
			ui.Key1: func(*ui.Context, *app) { nasIn = !nasIn; w.JieTakesOut(fakeworld.JieNAS, nasIn) },
			ui.Key2: func(*ui.Context, *app) {
				pcRelay = !pcRelay
				w.SetPath(fakeworld.JiePC, map[bool]apitype.PathKind{true: apitype.PathRelay, false: apitype.PathDirect}[pcRelay])
			},
			ui.Key3: func(c *ui.Context, a *app) { a.openLink(c, fakeworld.InviteLink) },
			ui.Key4: func(*ui.Context, *app) { w.SetPath(fakeworld.Build, apitype.PathConnecting) },
			ui.Key5: func(*ui.Context, *app) {
				nasOn = !nasOn
				w.SetPath(fakeworld.JieNAS, map[bool]apitype.PathKind{true: apitype.PathRelay, false: apitype.PathOffline}[nasOn])
			},
		}, nil
	}
	lc := localclient.New("")
	l := client.NewLive(lc)
	l.Start()
	return l, loadPlaces(placesFile()), nil, lc
}

// The control server of M1, on hk-155 (design/plan/m1/engine.md), for an
// engine that has never logged in and is given none: DENEB_CONTROL and
// DENEB_CONTROL_PIN choose another.
const (
	m1Control    = "https://103.42.31.155:47443"
	m1ControlPin = "sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4"
)

// keepEngine keeps this computer's engine running while the window is open
// (§2.4: in user-space mode the client starts it, detached, so that closing
// the window never takes a connection down): it starts the engine when
// nothing answers on its socket, as the window opens and whenever it stops
// answering later, a crash or an update that stopped it, looking every
// 3 seconds. The store connects to it on its own; a failure is said where
// the window waits for the engine.
func keepEngine(a *app, lc *localclient.Client) {
	var env []string
	if os.Getenv("DENEB_CONTROL") == "" {
		env = append(env, "DENEB_CONTROL="+m1Control, "DENEB_CONTROL_PIN="+m1ControlPin)
	}
	said := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := lc.EnsureRunning(ctx, localclient.SpawnOptions{Env: env})
		cancel()
		msg := ""
		if err != nil {
			log.Printf("deneb: starting the engine: %v", err)
			msg = "启动不了 Deneb 引擎，请重新安装 Deneb"
		}
		if msg != said {
			said = msg
			a.post(func(*ui.Context) { a.engineErr = msg })
		}
		time.Sleep(3 * time.Second)
	}
}

// demoMode reports whether to show the demo.
func demoMode() bool {
	return slices.Contains(os.Args[1:], "--demo") || slices.Contains(os.Args[1:], "-demo") || os.Getenv("DENEB_DEMO") == "1"
}
