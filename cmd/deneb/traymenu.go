package main

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// The system's menu of the mark on Linux (DESIGN §7.2). An AppIndicator
// tells the program neither where it was clicked nor where it is, and
// Wayland lets no program place its windows: a panel would open in the
// middle of the screen, far from the mark. So a click opens the system's
// menu, whose look the system gives, and Deneb only what is in it: how
// things are, what waits for me, a check item for each LAN, and the way
// into the window: the system hands Deneb no click on the mark there
// (DESIGN §7.2).
//
// The menu follows the world as the mark does (menuBar.follow), whether or
// not the window shows: the items' words and checks change in place, and
// the menu is made anew only when its items do.

// trayMenu is the mark's menu, as the system has it now.
type trayMenu struct {
	m     *menuBar
	keys  []string // its items, in order
	lines map[string]menuLine
	items map[string]*mygo.MenuItem
	// set gives the mark a menu made anew, and change changes an item of
	// it in place: the system's, or a test's.
	set    func(*mygo.Menu)
	change func(it *mygo.MenuItem, l, was menuLine)
}

// menuLine is an item of the menu: what it says, and what a click on it
// does.
type menuLine struct {
	key     string
	label   string
	sep     bool        // a separator
	enabled bool        // clicks reach it; the first line, how things are, is grey
	check   bool        // a check item: a LAN this computer is in
	on      bool        // checked: the LAN connects here, not paused
	lan     model.LANID // the LAN a click pauses or resumes
	chore   *chore      // what a click opens the window on
	open    bool        // "打开 Deneb"
}

func newTrayMenu(m *menuBar) *trayMenu {
	return &trayMenu{m: m, set: m.tray.SetMenu, change: func(it *mygo.MenuItem, l, was menuLine) {
		if l.label != was.label {
			it.SetLabel(l.label)
		}
		if l.enabled != was.enabled {
			it.SetEnabled(l.enabled)
		}
		if l.check && it.IsChecked() != l.on {
			it.SetChecked(l.on)
		}
	}}
}

// menuLines are the menu's items for s (DESIGN §7.2):
//
//  1. how things are, in grey, as the panel's head says it ("都连上了",
//     "2 台连不上", "都暂停了");
//  2. what waits for me, three at most, one item each ("Leo 想加入周末联机…"):
//     a click opens the window on it. A system menu closes as it is
//     clicked, with no room for an undo, so nothing is let in from it;
//  3. each LAN this computer is in, checked while it connects ("周末联机 ·
//     4 台都连上了"): a click pauses it or resumes it, the panel's and the
//     sidebar's switch;
//  4. "打开 Deneb".
func menuLines(s *model.Snapshot, now time.Time) []menuLine {
	_, status := summaryOf(s, &lightPalette)
	out := []menuLine{{key: "status", label: status}}
	if s.Engine && s.LoggedIn && s.Loaded {
		if chores := choresOf(s, now); len(chores) > 0 {
			out = append(out, menuLine{key: "sep-chores", sep: true})
			for _, ch := range chores[:min(3, len(chores))] {
				ch := ch
				out = append(out, menuLine{key: "chore/" + ch.key, label: ch.text + "…", enabled: true, chore: &ch})
			}
		}
		var lans []menuLine
		for _, l := range s.LANs {
			if l.Waiting || !l.Has(s.Self) {
				continue
			}
			lans = append(lans, menuLine{key: "lan/" + string(l.ID), label: l.Name + " · " + lanLineOf(s, l),
				enabled: true, check: true, on: !l.Paused, lan: l.ID})
		}
		if len(lans) > 0 {
			out = append(out, menuLine{key: "sep-lans", sep: true})
			out = append(out, lans...)
		}
	}
	return append(out, menuLine{key: "sep-open", sep: true}, menuLine{key: "open", label: "打开 Deneb", enabled: true, open: true})
}

// show brings the menu to s: in place while its items stay the same ones,
// made anew when they change.
func (t *trayMenu) show(s *model.Snapshot, now time.Time) {
	lines := menuLines(s, now)
	keys := make([]string, len(lines))
	for i, l := range lines {
		keys[i] = l.key
	}
	if t.items != nil && slices.Equal(keys, t.keys) {
		for _, l := range lines {
			was, it := t.lines[l.key], t.items[l.key]
			t.lines[l.key] = l
			if it != nil {
				t.change(it, l, was)
			}
		}
		return
	}
	var items []*mygo.MenuItem
	t.lines, t.items = map[string]menuLine{}, map[string]*mygo.MenuItem{}
	for _, l := range lines {
		t.lines[l.key] = l
		if l.sep {
			items = append(items, mygo.Separator())
			continue
		}
		key := l.key
		it := &mygo.MenuItem{ID: key, Label: l.label, Disabled: !l.enabled,
			Click: func(*mygo.MenuItem, *mygo.Window) { t.click(key) }}
		if l.check {
			it.Type, it.Checked = mygo.MenuItemCheckbox, l.on
		}
		t.items[key] = it
		items = append(items, it)
	}
	t.keys = keys
	t.set(mygo.NewMenu(items))
}

// click does what the item key says, as the world was when the menu last
// followed it.
func (t *trayMenu) click(key string) {
	l, ok := t.lines[key]
	if !ok || !l.enabled {
		return
	}
	m := t.m
	switch {
	case l.open:
		m.openWindow()
	case l.chore != nil:
		ch := *l.chore
		m.openWindow()
		m.a.post(func(c *ui.Context) { m.a.lookAt(c, ch) })
	case l.lan != "":
		// Checked, it connected: the click pauses it.
		m.sendNow(model.Op{Kind: model.OpLANPause, LAN: l.lan, On: l.on})
	}
}

// sendNow sends op from the menu, which has no frame of its own to answer
// in, and the window may not show: laid over the world at once, as send
// does, the mark and the menu following, and the store's answer taken in
// on the main thread as it comes. One turned down comes off again, and the
// window's toast says why once it shows.
func (m *menuBar) sendNow(op model.Op) {
	a := m.a
	p := &pending{op: op}
	a.pend = append(a.pend, p)
	a.gen++
	a.sync(nil)
	answer := func(r model.Result, err error) {
		p.done, p.ver, p.err = true, r.Version, err
		a.gen++
		if err != nil {
			a.post(func(c *ui.Context) { a.problem(c, err) })
		}
		m.followStore()
		a.wakeUp()
	}
	if _, ok := a.store.(client.Immediate); ok {
		answer(a.store.Do(context.Background(), op))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r, err := a.store.Do(ctx, op)
		if w := m.win; w != nil {
			w.Update(func() { answer(r, err) })
			return
		}
		a.post(func(*ui.Context) { answer(r, err) })
	}()
	m.followStore()
	a.wakeUp()
}
