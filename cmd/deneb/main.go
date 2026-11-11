package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
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
	useUpdater()
	if !oneWindow() {
		return
	}
	// The appearance of recordings and screenshots, DENEB_THEME=light or
	// dark, whatever the system's: the same on every machine, and none of
	// its settings changed for it.
	if t := os.Getenv("DENEB_THEME"); t == "light" || t == "dark" {
		mygo.Theme.SetSource(mygo.ThemeSource(t))
	}
	store, at, sims, lc := pickStore()
	a := newApp(store, at)
	a.sims = sims
	if lc != nil {
		go keepEngine(a, lc)
		// This computer's diagnostics package, from the engine (M6, DESIGN
		// §6.14); the demo only pretends.
		a.diagnostics = func() (string, error) { return saveDiagnostics(lc) }
		// The first time's steps (M6, DESIGN §7.3): signing in here, to
		// the server whose pin the window knows (others in the browser);
		// the network adapter, where the window puts it in.
		a.signer = client.NewSignIn(lc, map[string]string{m1Control: m1ControlPin})
		a.onboarded, a.saveOnboarded = onboardedState()
		a.adapter = adapterInstaller(lc)
	}
	a.copyText = mygo.Clipboard.WriteText
	a.openURL = mygo.Shell.OpenExternal
	a.reveal = mygo.Shell.ShowItemInFolder
	// Invite links (deneb://join/<token>), from the landing page or pasted
	// into a browser. Registered before Run, to get the one the app opens
	// with.
	mygo.App.OnOpenURL(func(u string) { a.post(func(c *ui.Context) { a.openLink(c, u) }) })
	mygo.App.WhenReady(func() {
		// The menu bar's mark first: the window's frames keep it up to date.
		bar := newMenuBar(a)
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Deneb",
			Width:     1180,
			Height:    740,
			MinWidth:  980,
			MinHeight: 640,
			// The view's size, as the design's frame is (DESIGN §7): on
			// Windows the window's own size takes in the edges it is resized
			// by, which nothing draws in.
			UseContentSize: true,
			// The sidebar runs to the top of the window, under the system's
			// window buttons, as the design's frame does (DESIGN.md §7).
			// Where they are at the top right, the strip they take is as
			// tall as DESIGN §7.2 has it.
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: titleBarHeight(runtime.GOOS),
			// Reopens where the user left it last time. A new key: the old
			// one kept the starter template's 480×360 window.
			StateKey: "lan-window",
			// MyGo draws this window itself; no web page sits behind it.
			Content: ui.View(bar.view(a.view)),
		})
		bar.attach(w)
		// Deneb opened again (Windows, Linux: oneWindow): the window there
		// is comes to the front.
		mygo.App.OnSecondInstance(func([]string, string) {
			if a.showMain != nil {
				a.showMain()
				return
			}
			w.Show()
			w.Focus()
		})
		// A new snapshot, or work done off the main thread, asks the window
		// and the menu bar's panel for a frame built anew, which takes it
		// in, and the mark for the world they show.
		a.setWake(func() {
			w.Update(bar.followStore)
			bar.update()
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// titleBarHeight is the height of the strip the window buttons take at the
// top right (DESIGN §7.2): 32 on Windows, as its own title bars, and 40 on
// Linux, GTK's round buttons centered in it. On macOS the traffic lights
// take the window's own title bar's.
func titleBarHeight(goos string) int {
	switch goos {
	case "windows":
		return 32
	case "linux":
		return 40
	}
	return 0
}

// oneWindow makes this the only window of Deneb where opening the app again
// starts it again (Windows, Linux): the window closes to the mark, and the
// app opened again, from the Start menu or the app grid, shows the one
// there is, as the Dock icon does on the Mac; a deneb:// link opened by it
// goes to that one too. It reports false in a second one, which has
// handed its links over and quits.
func oneWindow() bool {
	if runtime.GOOS == "darwin" || demoMode() {
		return true
	}
	return mygo.App.RequestSingleInstanceLock()
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
		// As an M4 server would have it (DESIGN §6.11, §6.12).
		d.Simulate("m3")
		d.Simulate("m4")
		return d, demoPlaces(), map[ui.Key]func(*ui.Context, *app){
			ui.Key1: func(*ui.Context, *app) { d.Simulate("leave") },
			ui.Key2: func(*ui.Context, *app) { d.Simulate("network") },
			ui.Key3: func(c *ui.Context, a *app) { a.openLink(c, client.DemoInvite) },
			ui.Key4: func(*ui.Context, *app) { d.Simulate("connect") },
			ui.Key5: func(*ui.Context, *app) { d.Simulate("offline") },
			// M2's states (DESIGN §6.7–§6.8), each played on the sample.
			ui.Key6: func(*ui.Context, *app) { d.Simulate("pending-device") },
			ui.Key7: func(*ui.Context, *app) { d.Simulate("pending-member") },
			ui.Key8: func(*ui.Context, *app) { d.Simulate("login-due") },
			ui.Key9: func(*ui.Context, *app) { d.Simulate("unreachable") },
			ui.Key0: func(*ui.Context, *app) { d.Simulate("invitation") },
			ui.KeyE: func(*ui.Context, *app) { d.Simulate("others-expired") },
			ui.KeyX: func(*ui.Context, *app) { d.Simulate("login-expired") },
			ui.KeyT: func(*ui.Context, *app) { d.Simulate("twin") },
			ui.KeyO: func(*ui.Context, *app) { d.Simulate("stale") },
			ui.KeyN: func(*ui.Context, *app) { d.Simulate("notice") },
			ui.KeyU: func(*ui.Context, *app) { d.Simulate("using") },
			ui.KeyW: func(*ui.Context, *app) { d.Simulate("waiting") },
			// This computer loses its network, or gets it back (§6.10).
			ui.KeyL: func(*ui.Context, *app) { d.Simulate("nonet") },
			// M3's (§6.11): the next seat limit; 小美 gives me her printer,
			// or takes it back; Leo invites me, his seats full.
			ui.KeyS: func(*ui.Context, *app) { d.Simulate("seats") },
			ui.KeyG: func(*ui.Context, *app) { d.Simulate("share-in") },
			ui.KeyY: func(*ui.Context, *app) { d.Simulate("invitation-full") },
			// M4's (§6.12): my server stops, or comes back; an install
			// command waiting is run on a server.
			ui.KeyR: func(*ui.Context, *app) { d.Simulate("relay-down") },
			ui.KeyJ: func(*ui.Context, *app) { d.Simulate("relay-joined") },
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

// notAllowedText is what the window says when this computer's engine is
// another account's: the one that installed Deneb on Linux, or its
// service on Windows. It runs, and reinstalling would not help; this
// account cannot use it (localclient.ErrNotAllowed). The user chose the
// wording on 11-10: it says what to do.
const notAllowedText = "Deneb 在这台电脑上是给另一个账户用的，请换回那个账户"

// keepEngine keeps this computer's engine running while the window is open
// (§2.4: in user-space mode the client starts it, detached, so that closing
// the window never takes a connection down): it starts the engine when
// nothing answers on its socket, as the window opens and whenever it stops
// answering later, a crash or an update that stopped it, looking every
// 3 seconds. The store connects to it on its own; a failure is said where
// the window waits for the engine. The engine is the one the package ships
// where it ships one (bundledEngine), and once it answers, it is brought
// to the window's version (followEngine).
func keepEngine(a *app, lc *localclient.Client) {
	var env []string
	if os.Getenv("DENEB_CONTROL") == "" {
		env = append(env, "DENEB_CONTROL="+m1Control, "DENEB_CONTROL_PIN="+m1ControlPin)
	}
	bin := bundledEngine()
	said, followed := "", false
	for {
		if installing.Load() {
			time.Sleep(3 * time.Second)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := lc.EnsureRunning(ctx, localclient.SpawnOptions{Binary: bin, Args: ownEngineArgs, Env: env})
		cancel()
		msg := ""
		switch {
		case errors.Is(err, localclient.ErrNotAllowed):
			// Nothing to start: the engine is there, for another account.
			msg = notAllowedText
		case err != nil:
			log.Printf("deneb: starting the engine: %v", err)
			msg = "启动不了 Deneb 引擎，请重新安装 Deneb"
		case !followed:
			followed = true
			go followEngineOnce(lc)
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
