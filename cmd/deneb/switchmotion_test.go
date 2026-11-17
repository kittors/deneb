package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The sidebar going from page to page (design/plan/m7/switch-motion.md §3):
// what both pages have stays and moves to its place on the new one; the old
// one's others fade out where they are; the new one's fade in once the old
// are out of the way; another click on the way takes over from wherever
// everything is, without an element twice or one stuck.

// switchTestRig is the demo with my game console in 工作室 too, showing
// 周末联机, the console placed in both.
func switchTestRig(t *testing.T) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.run(800 * time.Millisecond)
	switchWorld(d)
	r.run(600 * time.Millisecond)
	r.show("work")
	r.show("weekend")
	return r
}

// show switches to id from the sidebar's side, and lets it settle.
func (r *m3rig) show(id model.LANID) {
	r.a.post(func(c *ui.Context) { r.a.show(c, id, false) })
	r.run(1200 * time.Millisecond)
}

func TestSwitchMovesWhatBothHave(t *testing.T) {
	r := switchTestRig(t)
	was := r.a.device("game")
	from := was.live
	r.a.post(func(c *ui.Context) { r.a.show(c, "work", false) })
	r.run(50 * time.Millisecond)
	game := r.a.device("game")
	if game == nil || game.key() != was.key() {
		t.Fatalf("the console has another element on 工作室's canvas: %v, was %q", game, was.key())
	}
	if !game.shifting || dist(game.live, from) > 40 {
		t.Errorf("the console jumped: at %v 50 ms after the switch, from %v (shifting %v)", game.live, from, game.shifting)
	}
	ghosts := map[string]bool{}
	for _, g := range r.a.sw.ghosts {
		ghosts[g.id] = true
	}
	for _, id := range []string{"mia", "jdesk", "jnas"} {
		if !ghosts[id] {
			t.Errorf("%s, only on 周末联机, does not fade out; ghosts %v", id, ghosts)
		}
	}
	if ghosts["mac"] || ghosts["game"] {
		t.Errorf("a device on both pages fades out: %v", ghosts)
	}
	for _, id := range []string{"studio", "lindb", "linci"} {
		if d := r.a.device(id); d == nil || d.vis > 0.01 {
			t.Errorf("%s, only on 工作室, shows before the old ones are out of the way: %+v", id, d)
		}
	}
	r.run(1200 * time.Millisecond)
	if len(r.a.sw.ghosts) != 0 {
		t.Errorf("ghosts left a second later: %d", len(r.a.sw.ghosts))
	}
	if game.shifting || dist(game.live, game.at()) > 0.75 {
		t.Errorf("the console did not land at its place: %v, place %v", game.live, game.at())
	}
	for _, d := range r.a.devices {
		if d.vis < 0.999 {
			t.Errorf("%s is not quite there a second later: %v", d.id, d.vis)
		}
	}
}

// Clicked faster than any switch takes, through every kind of page, the
// view never has two elements of one key (the tester would panic), and it
// ends as one click there would leave it.
func TestSwitchRapidClicks(t *testing.T) {
	r := switchTestRig(t)
	r.d.Simulate("waiting")
	r.run(300 * time.Millisecond)
	for round := 0; round < 3; round++ {
		for _, id := range []model.LANID{"work", allLANs, "weekend", "books", model.SharedLANID, "work", allLANs} {
			r.a.post(func(c *ui.Context) { r.a.show(c, id, false) })
			r.run(time.Duration(40+round*30) * time.Millisecond)
		}
	}
	r.a.post(func(c *ui.Context) { r.a.show(c, "weekend", false) })
	r.run(1500 * time.Millisecond)
	if len(r.a.sw.ghosts) != 0 || len(r.a.sw.ghostRelays) != 0 {
		t.Errorf("ghosts left: %d devices, %d relays", len(r.a.sw.ghosts), len(r.a.sw.ghostRelays))
	}
	for _, d := range r.a.devices {
		if d.vis < 0.999 || d.shifting {
			t.Errorf("%s did not settle: vis %v, shifting %v", d.id, d.vis, d.shifting)
		}
	}
	if !r.has("周末联机") || r.a.sw.title.shown != "weekend\x00周末联机" {
		t.Errorf("the title is %q", r.a.sw.title.shown)
	}
}

// The header's title is one at a time: the old one fades out before the
// new one fades in, and a name clicked past on the way never shows.
func TestSwitchTitleOneAtATime(t *testing.T) {
	r := switchTestRig(t)
	seen := map[string]bool{}
	r.a.post(func(c *ui.Context) { r.a.show(c, "work", false) })
	r.run(30 * time.Millisecond)
	r.a.post(func(c *ui.Context) { r.a.show(c, allLANs, false) })
	for i := 0; i < 40; i++ {
		r.run(16 * time.Millisecond)
		seen[r.a.sw.title.shown] = true
		if strings.Contains(strings.Join(r.tt.Texts(), "|"), "工作室|全部局域网") {
			t.Fatal("two titles show at once")
		}
	}
	if seen["work\x00工作室"] {
		t.Error("工作室, clicked past, showed as the title")
	}
	if r.a.sw.title.shown != "\x00全部局域网" {
		t.Errorf("the title is %q", r.a.sw.title.shown)
	}
}

// The sidebar's white ground travels from the row chosen before to the one
// chosen now, under the rows, and hands itself back to the row.
func TestSwitchPillTravels(t *testing.T) {
	r := switchTestRig(t)
	r.pick("工作室", 50*time.Millisecond)
	if !r.a.sw.pillOn {
		t.Fatal("no pill on its way to 工作室's row")
	}
	r.run(1200 * time.Millisecond)
	if r.a.sw.pillOn {
		t.Error("the pill still travels a second later")
	}
}

// Into a LAN I wait to be let into, and out of it: its words fade in once
// the devices are out of the way, and out before the next page's come in.
func TestSwitchToWaitingBoard(t *testing.T) {
	r := switchTestRig(t)
	r.d.Simulate("waiting")
	r.run(300 * time.Millisecond)
	words := "同意你加入「读书会」"
	r.a.post(func(c *ui.Context) { r.a.show(c, "books", false) })
	r.run(50 * time.Millisecond)
	if len(r.a.sw.ghosts) == 0 {
		t.Fatal("周末联机's devices went at once")
	}
	r.run(1000 * time.Millisecond)
	if !r.hasPart(words) || len(r.a.sw.ghosts) != 0 {
		t.Fatalf("the waiting LAN's words do not show, or ghosts stay: %q", r.tt.Texts())
	}
	r.a.post(func(c *ui.Context) { r.a.show(c, "weekend", false) })
	r.run(50 * time.Millisecond)
	if !r.hasPart(words) {
		t.Error("the waiting words went at once")
	}
	r.run(1000 * time.Millisecond)
	if r.hasPart(words) {
		t.Error("the waiting words stay on 周末联机's canvas")
	}
}

// The LAN on the canvas gone, deleted elsewhere: the canvas goes to the
// first LAN there is as a click there would, not at once.
func TestSwitchLANGone(t *testing.T) {
	r := switchTestRig(t)
	r.show("work")
	r.d.Simulate("lan-gone")
	r.run(50 * time.Millisecond)
	if r.a.lan == "work" {
		t.Fatal("the canvas stays on the gone LAN")
	}
	if len(r.a.sw.ghosts) == 0 {
		t.Error("工作室's devices went at once")
	}
	r.run(1200 * time.Millisecond)
	if len(r.a.sw.ghosts) != 0 {
		t.Error("ghosts left")
	}
}

// Every LAN gone: the header's words fade out, the canvas says there is
// none, and nothing of the last LAN stays.
func TestSwitchToEmpty(t *testing.T) {
	r := switchTestRig(t)
	r.d.Simulate("lan-gone")
	r.run(1200 * time.Millisecond)
	r.d.Simulate("lan-gone")
	r.run(50 * time.Millisecond)
	if len(r.a.sw.ghosts) == 0 {
		t.Error("周末联机's devices went at once")
	}
	if !r.has("周末联机") {
		t.Error("the title went at once")
	}
	r.run(1200 * time.Millisecond)
	if !r.has("还没有局域网") || r.has("周末联机") || len(r.a.sw.ghosts) != 0 {
		t.Errorf("not the empty canvas a second later: %q", r.tt.Texts())
	}
}

// hasPart reports whether any text shown holds s.
func (r *m3rig) hasPart(s string) bool {
	for _, x := range r.tt.Texts() {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}
