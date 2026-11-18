package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// The menu bar (DESIGN §7.1): a mark in the macOS menu bar, and the panel
// a click on it opens under it, drawn in the client's own look. The panel
// and the window are one view of one world: what one changes, the other
// shows at once.

// panelWidth is the panel's width; it is as tall as what it holds, up to
// panelMaxHeight.
const (
	panelWidth     = 320
	panelMaxHeight = 560
)

// trayState is the menu bar's mark for s: "hollow" while nothing connects
// here (the engine does not answer, no one logged in on this computer yet,
// it has no network, or every LAN is paused), "dot" while something waits
// for me, "solid" otherwise.
func trayState(s *model.Snapshot, now time.Time) string {
	switch {
	case !s.Engine || !s.LoggedIn || s.NoNetwork || allPaused(s):
		return "hollow"
	case len(choresOf(s, now)) > 0:
		return "dot"
	}
	return "solid"
}

// allPaused reports whether s has LANs and every one I am in is paused here.
func allPaused(s *model.Snapshot) bool {
	n := 0
	for _, l := range s.LANs {
		if l.Waiting {
			continue
		}
		if !l.Paused {
			return false
		}
		n++
	}
	return n > 0
}

// chore is something that waits for me, as the panel lists it: a sentence,
// and the button that does it.
type chore struct {
	key, text, button string
	kind              string // "device", "member", "renew", "invitation", "unreachable"
	lan               model.LANID
	dev               model.DeviceID
	person            model.PersonID
	invitation        string
}

// choresOf are what waits for me in s, most pressing first: devices and
// people to let into my LANs, my logins to renew, invites to answer, and
// devices nothing reaches.
func choresOf(s *model.Snapshot, now time.Time) []chore {
	var out []chore
	name := func(id model.PersonID) string {
		if p := s.Person(id); p != nil {
			return p.Name
		}
		return "有人"
	}
	for _, l := range s.LANs {
		if l.Owner != s.Me || !s.Can(model.FeatureApprove) {
			continue
		}
		for _, id := range l.Pending {
			if d := s.Device(id); d != nil && d.Owner != s.Me {
				out = append(out, chore{key: "device/" + string(l.ID) + "/" + string(id), kind: "device", lan: l.ID, dev: id,
					text: say(short(d.Name), "想加入", short(l.Name)), button: "同意"})
			}
		}
		for _, id := range l.PendingMembers {
			out = append(out, chore{key: "member/" + string(l.ID) + "/" + string(id), kind: "member", lan: l.ID, person: id,
				text: say(name(id), "想加入", short(l.Name)), button: "同意"})
		}
	}
	if s.Can(model.FeatureRenew) {
		for _, id := range s.Mine {
			if d := s.Device(id); d != nil && (d.Login.Expired || d.Login.Due(now)) {
				out = append(out, chore{key: "renew/" + string(id), kind: "renew", dev: id,
					text: say(short(d.Name), "需要重新登录"), button: "续上"})
			}
		}
	}
	for _, inv := range s.Invitations {
		out = append(out, chore{key: "invitation/" + inv.ID, kind: "invitation", lan: inv.LAN, invitation: inv.ID,
			text: say(inv.Inviter.Name, "邀请你加入", short(inv.LANName)), button: "看看"})
	}
	seen := map[model.DeviceID]bool{}
	for _, l := range s.LANs {
		if l.Waiting || l.Paused || s.NoNetwork {
			// No network here: nothing is out of reach over there (§6.10).
			continue
		}
		for _, id := range l.Devices {
			if seen[id] || id == s.Self || s.Path(id).Kind != model.PathUnreachable {
				continue
			}
			seen[id] = true
			if d := s.Device(id); d != nil {
				out = append(out, chore{key: "unreachable/" + string(id), kind: "unreachable", lan: l.ID, dev: id,
					text: say(short(d.Name), "连不上"), button: "看看"})
			}
		}
	}
	return out
}

// doneRow is a chore the panel just did, said in its place for a moment
// ("已同意阿杰的树莓派 · 撤销") before it folds away. One that lets someone
// in is held: it goes as the row folds, and undo drops it unsent, as the
// window's toast does.
type doneRow struct {
	key, text string
	until     time.Time
	p         *pending
}

// panelBody is the panel: this computer and how it is, what waits for me,
// my LANs with their switches, and the way into the window. It is as tall
// as what it holds, up to panelMaxHeight: beyond it the middle scrolls,
// this computer and the way into the window staying in sight.
func (a *app) panelBody(c *ui.Context, p *palette) ui.Element {
	now := c.Now()
	a.expireDone(c, now)
	s := a.snap
	body := ui.Column(c.Key("panel")).Width(panelWidth).MaxHeight(panelMaxHeight).Radius(20).Background(p.bg).Clip().Padding(12)
	if a.platform == "windows" {
		// Windows rounds the flyout as it rounds its menus, and outlines it
		// (DESIGN §7.2): the panel fills it, square.
		body.Radius(0)
	} else if p.dark && !a.systemPanel {
		// The system outlines its own panel with a hairline.
		body.Border(1, p.line)
	}
	note := a.panelNote.text != "" && now.Before(a.panelNote.until)
	if note {
		c.After(a.panelNote.until.Sub(now))
	}
	hasChores := s.Engine && (len(choresOf(s, now)) > 0 || len(a.panelDone) > 0)
	hasLANs := s.Engine && len(s.LANs) > 0
	hasShared := s.Engine && s.Can(model.FeatureShare) && len(s.Shared) > 0
	a.panelWaits = a.panelWaiting(c, now, !hasChores && !hasLANs && !hasShared)
	// One thing at a time (§6.5): the groups come once the ring has faded
	// out of the room it kept, the room folding as they open out, the
	// panel's height going from one to the other in step.
	ringIn := ui.Animation{Duration: swapOut}
	if a.panelWaits {
		// Coming over groups that fold away, it waits for them to.
		ringIn.Delay = foldTime
	}
	ring := body.AnimateTo("panel-ring", map[bool]float32{false: 0, true: 1}[a.panelWaits], ringIn)
	room := a.panelWaits || ring > 0
	chores, lans, shared := hasChores && !room, hasLANs && !room, hasShared && !room
	body.Children(func() {
		a.panelHead(c, p)
		a.fold(c, "note-section", note, func() { a.panelNoteRow(c, p) })
		ui.Scroll(c.Key("panel-scroll")).Shrink(1).Children(func() {
			a.fold(c, "wait-section", room, func() { a.panelWait(c, p, now, ring) })
			// Come while the ring fades, they are laid out out of sight: the
			// room it kept goes to the room they take, in step (panelWait),
			// so that the flyout's height does not jump as they open out.
			a.fold(c, "chores-section", chores, func() {
				a.panelChores(c, p, now)
			}, hasChores)
			a.fold(c, "lans-section", lans, func() {
				panelLabel(c, p, "局域网")
				for _, l := range s.LANs {
					a.panelLAN(c, p, l)
				}
			}, hasLANs)
			a.fold(c, "shared-section", shared, func() {
				// The devices given to me, a row each (DESIGN §6.11).
				panelLabel(c, p, "共享给我")
				for _, sh := range s.Shared {
					a.panelShared(c, p, sh)
				}
			}, hasShared)
		})
		a.notePanelRoom(map[string]bool{"chores-section": hasChores, "lans-section": hasLANs, "shared-section": hasShared})
		a.panelOpen(c, p)
	})
	return body
}

// A row of the panel's groups (a LAN's, a share's), and a group's label
// over its rows (panelLabel): its words of 12 between 12 over and 4 under.
const (
	panelRowHeight   = 48
	panelLabelTop    = 12
	panelLabelBottom = 4
)

// panelRoomFirst is the room the panel keeps for its groups while it waits
// for them, before it has ever laid them out: two LANs' rows under their
// label, about (its words' line taken as their size). It need not be
// exact: once the groups come, the room goes to theirs in step.
var panelRoomFirst = panelLabelTop + typeScale[0] + panelLabelBottom + 2*panelRowHeight

// panelWaiting reports whether the panel waits for what it shows: none of
// its groups (empty) because this computer's engine does not answer, or
// the person's view is not in yet (a window just opened, the engine being
// changed for the one shipped). Then it keeps the room its groups took
// last (panelWait) rather than folding to its head, which left the flyout
// half as tall till they came (the user, 11-17, after updating to 0.1.9:
// "变得非常扁平，然后过一会就好了"). Past startingPatience still waiting,
// what is shows: the engine is not running.
func (a *app) panelWaiting(c *ui.Context, now time.Time, empty bool) bool {
	s := a.snap
	if !empty || s.Engine && (!s.LoggedIn || s.Loaded) {
		a.panelBlankSince = time.Time{}
		return false
	}
	if a.panelBlankSince.IsZero() {
		a.panelBlankSince = now
	}
	left := startingPatience - now.Sub(a.panelBlankSince)
	if left <= 0 {
		return false
	}
	c.After(left)
	return true
}

// panelWait is the room the panel's groups took last, a ring turning in its
// middle while they come, as opaque as ring.
func (a *app) panelWait(c *ui.Context, p *palette, now time.Time, ring float32) {
	room := a.panelRoom
	if room <= 0 {
		room = panelRoomFirst
	}
	box := ui.Box(c.Key("panel-wait")).Center()
	// To the room the groups take once it is known, in step.
	box.Height(box.AnimateTo("room", room, ui.Animation{Duration: fadeTime}))
	box.Children(func() {
		ui.Box(c).Opacity(ring).Children(func() { spinner(c, p, now) })
	})
}

// notePanelRoom keeps the room the panel's groups take while one shows, as
// they were last laid out: what panelWait keeps for them.
func (a *app) notePanelRoom(open map[string]bool) {
	var room float32
	for key, on := range open {
		if on {
			room += a.foldH[key]
		}
	}
	if room > 0 {
		a.panelRoom = room
	}
}

// panelNote is why something done from the panel did not go through, said
// in the panel until a moment is over, as the window's toast says it: the
// window may be out of sight.
type panelNote struct {
	text  string
	until time.Time
}

// panelNoteRow says the panel's note, under this computer.
func (a *app) panelNoteRow(c *ui.Context, p *palette) {
	ui.Box(c).Height(8)
	// Its words on the edge the rows' are, 8 in: the well round them as
	// the chores' is.
	ui.Row(c).Radius(14).Background(p.well).Padding(8).Children(func() {
		ui.Text(c, a.panelNote.text).FontSize(12).TextColor(p.ink2).Grow(1).Shrink(1)
	})
}

// fold is a group of the panel that opens out as it comes and folds away
// as it goes, by its height, the rest of the panel following in step and in
// place (DESIGN §6.6: nothing jumps). It is built while it shows, or folds.
// Given measure, a group folded away is laid out all the same, out of
// sight in no room, to know the room it takes (foldH) before it opens.
func (a *app) fold(c *ui.Context, key string, open bool, build func(), measure ...bool) {
	holder := ui.Column(c.Key(key)).Clip()
	k := holder.AnimateTo("open", map[bool]float32{false: 0, true: 1}[open], ui.Animation{Duration: foldTime})
	if k < 1 {
		holder.Height(a.foldH[key] * k).Opacity(k)
	}
	if k <= 0 && (len(measure) == 0 || !measure[0]) {
		return
	}
	holder.Children(func() {
		inner := ui.Column(c.Key(key + "-inner"))
		inner.Children(build)
		if b := inner.Bounds(); b.H > 0 {
			if a.foldH == nil {
				a.foldH = map[string]float32{}
			}
			a.foldH[key] = b.H
		}
	})
}

// panelHead is this computer, its name, and how things are, in a few words.
func (a *app) panelHead(c *ui.Context, p *palette) {
	s := a.snap
	// As far from the panel's top as its rows' things are from its sides.
	ui.Row(c).Gap(12).Padding(8, 8, 4, 8).AlignItems(ui.Center).Children(func() {
		kind, name := "laptop", "这台电脑"
		if d := s.Device(s.Self); d != nil {
			kind, name = glyphFor(d.Kind), d.Name
		}
		col := ownerColors[0]
		if me := s.Person(s.Me); me != nil {
			col = ownerColors[me.Color%model.NumColors]
		}
		a.profileTile(c, p, s.Self, kind, col, 36, false, 0)
		ui.Column(c).Gap(4).Grow(1).Children(func() {
			ui.Text(c, name).FontSize(14).FontWeight(600).SingleLine().MaxWidth(220)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				dot, line := a.panelSummary(p)
				ui.Box(c.Key("summary-dot")).Size(7, 7).Radius(999).Background(dot).Shadow(0, 0, 0, 3, dot.Alpha(0.18)).
					Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
				// One at a time, as the window's words (words.go).
				swapWords(c, a.words, "panel-summary", line, 12, 400, p.ink2, false)
			})
		})
	})
}

// panelSummary is how things are here, in a few words, and the color of
// the dot before them. While the panel waits for the engine (panelWaiting)
// it is connecting, not "not running": the engine is most likely just
// starting, or being changed.
func (a *app) panelSummary(p *palette) (ui.Color, string) {
	if a.panelWaits && !a.snap.Engine {
		return p.ink3, "正在连接…"
	}
	return summaryOf(a.snap, p)
}

// summaryOf is how things are in s, in a few words, and the color of the
// dot before them: the panel's head, and the first line of the system's
// menu on Linux (DESIGN §7.2).
func summaryOf(s *model.Snapshot, p *palette) (ui.Color, string) {
	amber := ui.Hex("#F59E0B")
	var lans []*model.LAN
	for _, l := range s.LANs {
		if !l.Waiting {
			lans = append(lans, l)
		}
	}
	switch {
	case !s.Engine:
		return p.ink3, "Deneb 没在运行"
	case !s.LoggedIn:
		return p.ink3, "还没登录"
	case !s.Loaded:
		return p.ink3, "正在连接…"
	case s.NoNetwork:
		return p.ink3, "这台电脑没联网"
	case len(lans) == 0:
		return p.ink3, "还没有局域网"
	case allPaused(s):
		return p.ink3, "都暂停了"
	}
	// As it is, in the card's words: 都连上了 only when everything is; of
	// the devices that are not, the most pressing kind, as the card puts
	// them first (连不上 before 不在线).
	unreachable, offline, connecting := map[model.DeviceID]bool{}, map[model.DeviceID]bool{}, false
	for _, l := range lans {
		if l.Paused {
			continue
		}
		for _, id := range l.Devices {
			if id == s.Self {
				continue
			}
			switch s.Path(id).Kind {
			case model.PathUnreachable:
				unreachable[id] = true
			case model.PathOffline:
				offline[id] = true
			case model.PathConnecting:
				connecting = true
			}
		}
	}
	switch {
	case len(unreachable) > 0:
		return amber, fmt.Sprintf("%d 台连不上", len(unreachable))
	case connecting:
		return p.ink3, "正在连接…"
	case len(offline) > 0:
		return p.ink3, fmt.Sprintf("%d 台不在线", len(offline))
	}
	return p.ok, "都连上了"
}

// panelChores lists what waits for me, three at most, each with its
// button; one just done says so in its place, and folds away.
func (a *app) panelChores(c *ui.Context, p *palette, now time.Time) {
	chores := choresOf(a.snap, now)
	var done []*doneRow
	for _, d := range a.panelDone {
		done = append(done, d)
	}
	slices.SortFunc(done, func(x, y *doneRow) int { return x.until.Compare(y.until) })
	panelLabel(c, p, "要你处理")
	// The well reaches to the panel's sides, as a chosen row's light does;
	// its words keep to the rows' edge, 8 in.
	list := ui.Column(c.Key("chores")).Radius(14).Background(p.well).Padding(0, 8)
	list.Children(func() {
		row := func(key string) ui.Element {
			r := ui.Row(c.Key(key)).Height(44).Gap(12).AlignItems(ui.Center)
			r.Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}})
			return r
		}
		for _, d := range done {
			row(d.key).Children(func() {
				ui.Text(c, d.text).FontSize(13).TextColor(p.ink2).SingleLine().Shrink(1).Grow(1)
				if d.p != nil {
					// Its words end on the rows' edge, as 同意's pill does.
					undo := ui.ButtonBase(c.Key("undo")).Height(28).Padding(0, 8).Margin(0, -8, 0, 0).Radius(999).FocusRing(false)
					undo.Children(func() { ui.Text(c, "撤销").FontSize(12).FontWeight(500).TextColor(p.primary) })
					if undo.Clicked() {
						a.undoDone(d)
					}
				}
			})
		}
		shown := 0
		for _, ch := range chores {
			if shown == 3 {
				break
			}
			if _, ok := a.panelDone[ch.key]; ok {
				continue
			}
			shown++
			row(ch.key).Children(func() {
				ui.Text(c, ch.text).FontSize(13).FontWeight(500).SingleLine().Shrink(1).Grow(1)
				if wellButton(c, p, "do", ch.button).Clicked() {
					a.doChore(c, ch, now)
				}
			})
		}
		if more := len(chores) - shown - len(done); more > 0 {
			row("more").Children(func() {
				ui.Text(c, fmt.Sprintf("还有 %d 件", more)).FontSize(13).TextColor(p.ink2).Grow(1)
				b := ui.ButtonBase(c.Key("open")).Height(28).Padding(0, 8).Margin(0, -8, 0, 0).Radius(999).FocusRing(false)
				b.Children(func() { ui.Text(c, "打开 Deneb 看").FontSize(12).FontWeight(500).TextColor(p.primary) })
				if b.Clicked() {
					a.openMain(c)
				}
			})
		}
	})
}

// doChore does what a chore's button says.
func (a *app) doChore(c *ui.Context, ch chore, now time.Time) {
	s := a.snap
	switch ch.kind {
	case "device":
		name := ""
		if d := s.Device(ch.dev); d != nil {
			name = short(d.Name)
		}
		a.panelHold(c, ch.key, model.Op{Kind: model.OpDeviceApprove, LAN: ch.lan, Device: ch.dev}, say("已同意", name), now)
	case "member":
		name := ""
		if p := s.Person(ch.person); p != nil {
			name = p.Name
		}
		a.panelHold(c, ch.key, model.Op{Kind: model.OpMemberApprove, LAN: ch.lan, Person: ch.person}, say("已同意", name), now)
	case "renew":
		a.send(c, model.Op{Kind: model.OpDeviceRenew, Device: ch.dev})
		a.panelSay(ch.key, "已续上", now)
	case "invitation", "unreachable":
		a.openMain(c)
		a.lookAt(c, ch)
	}
}

// lookAt shows in the window what ch is about: the invite's card, or the
// LAN with the device chosen, its card open (the one to let in, the one
// whose login to renew, the one out of reach), or the LAN of the person
// to let in, whose dashed ball waits in the header. The panel's "看看"
// does it, and every chore of the system's menu on Linux (DESIGN §7.2).
func (a *app) lookAt(c *ui.Context, ch chore) {
	s := a.snap
	if ch.kind == "invitation" {
		for _, inv := range s.Invitations {
			if inv.ID == ch.invitation {
				a.showInvitation(inv)
			}
		}
		return
	}
	if ch.lan == "" && ch.dev != "" {
		// A login to renew: the device in the first LAN that has it.
		for _, l := range s.LANs {
			if !l.Waiting && l.Has(ch.dev) {
				ch.lan = l.ID
				break
			}
		}
	}
	if ch.lan != "" && a.lan != ch.lan && !a.all() {
		a.show(c, ch.lan, false)
	}
	if ch.dev == "" || (ch.lan == "" && a.device(string(ch.dev)) == nil) {
		return
	}
	key := string(ch.dev)
	if a.all() {
		key = allKey(ch.lan, ch.dev)
	}
	a.selected, a.more = key, false
}

// panelHold shows op at once and says so in the chore's place, sending it
// only as that folds away; undo drops it unsent.
func (a *app) panelHold(c *ui.Context, key string, op model.Op, text string, now time.Time) {
	p := &pending{op: op, held: true}
	a.pend = append(a.pend, p)
	a.gen++
	a.sync(nil)
	if a.panelDone == nil {
		a.panelDone = map[string]*doneRow{}
	}
	a.panelDone[key] = &doneRow{key: key, text: text, until: now.Add(4 * time.Second), p: p}
	c.After(4 * time.Second)
}

// panelSay says what a chore did, in its place, for a moment.
func (a *app) panelSay(key, text string, now time.Time) {
	if a.panelDone == nil {
		a.panelDone = map[string]*doneRow{}
	}
	a.panelDone[key] = &doneRow{key: key, text: text, until: now.Add(2500 * time.Millisecond)}
}

// undoDone takes back what a done row did: what it held stays unsent.
func (a *app) undoDone(d *doneRow) {
	if d.p != nil {
		a.pend = slices.DeleteFunc(a.pend, func(x *pending) bool { return x == d.p })
		a.gen++
	}
	delete(a.panelDone, d.key)
}

// expireDone folds the done rows whose moment is over, sending what they
// held.
func (a *app) expireDone(c *ui.Context, now time.Time) {
	for key, d := range a.panelDone {
		if left := d.until.Sub(now); left > 0 {
			c.After(left)
			continue
		}
		if d.p != nil && d.p.held {
			a.commit(c, d.p)
		}
		delete(a.panelDone, key)
	}
}

// flushPanel sends what the panel holds, at once: the panel closes, and
// nothing would fold its rows.
func (a *app) flushPanel(c *ui.Context) {
	for key, d := range a.panelDone {
		if d.p != nil && d.p.held {
			a.commit(c, d.p)
		}
		delete(a.panelDone, key)
	}
}

// panelLAN is one of my LANs in the panel: its members' dots, its name and
// how it is, and its switch; a click elsewhere on it opens the window on
// it.
func (a *app) panelLAN(c *ui.Context, p *palette, l *model.LAN) {
	row := ui.ButtonBase(c.Key("panel-lan-"+string(l.ID))).Height(panelRowHeight).Padding(0, 8).Gap(12).Radius(12).FocusRing(false)
	if row.Hovered() {
		row.Background(p.well)
	}
	flipped := false
	row.Children(func() {
		ui.Row(c).Width(24).Children(func() {
			for i, id := range l.Members[:min(3, len(l.Members))] {
				b := ball(c, a.people[string(id)], 12, false).Shadow(0, 0, 0, 1.5, p.bg)
				if i > 0 {
					b.Margin(0, 0, 0, -4)
				}
			}
		})
		ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
			ui.Text(c, l.Name).FontSize(13).FontWeight(500).SingleLine()
			line := a.lanLine(l)
			ui.Text(c.Key("line-"+line), line).FontSize(12).TextColor(p.ink3).SingleLine().
				Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
		})
		if !l.Waiting && l.Has(a.snap.Self) {
			// The sidebar's switch: only where this computer is in.
			lan, paused := l.ID, l.Paused
			if miniToggle(c, p, "panel-pause-"+string(lan), "暂停"+l.Name, !paused) {
				flipped = true
				a.pause(c, lan, !paused)
			}
		}
	})
	if row.Clicked() && !flipped {
		a.openMain(c)
		a.show(c, l.ID, false)
	}
}

// lanLine is how a LAN is, under its name in the panel.
func (a *app) lanLine(l *model.LAN) string { return lanLineOf(a.snap, l) }

// lanLineOf is how l is in s, in a few words: under its name in the panel,
// after it in the system's menu on Linux.
func lanLineOf(s *model.Snapshot, l *model.LAN) string {
	switch {
	case l.Waiting:
		owner := "拥有者"
		if p := s.Person(l.Owner); p != nil {
			owner = p.Name
		}
		return say("等", owner, "同意你加入")
	case l.Paused:
		return "已暂停"
	case s.NoNetwork:
		return "等这台电脑联网"
	case s.Self != "" && !l.Has(s.Self):
		return "这台电脑还没加进来"
	}
	n, up := 0, 0
	for _, id := range l.Devices {
		n++
		if k := s.Path(id).Kind; id == s.Self || k == model.PathDirect || k == model.PathRelay {
			up++
		}
	}
	if up == n {
		return fmt.Sprintf("%d 台都连上了", n)
	}
	return fmt.Sprintf("%d 台，%d 台连上了", n, up)
}

// panelOpen is the panel's way into the window.
func (a *app) panelOpen(c *ui.Context, p *palette) {
	ui.Box(c).Height(12)
	b := ghostButton(c, p, "open-deneb", "打开 Deneb").AlignSelf(ui.Stretch).Justify(ui.Center)
	if b.Clicked() {
		a.openMain(c)
	}
}

// openMain brings the window to the front, the panel put away: the
// window's, or nothing in a test.
func (a *app) openMain(c *ui.Context) {
	a.flushPanel(c)
	if a.showMain != nil {
		a.showMain()
	}
}

// panel is the flyout's view: the panel, sized to what it holds. It takes
// the store's news as the window does, but leaves the canvas to the
// window's own frames.
func (a *app) panel(c *ui.Context) {
	a.drain(c)
	a.sync(nil)
	p := colors(c)
	// The panel draws all of the flyout: nothing around it, where the
	// window runs a frame behind the panel's height.
	c.Root().Background(ui.Transparent)
	// Out of the window's flow, the panel is as tall as what it holds,
	// whatever the window's height is now, and the window follows it.
	ui.Column(c.Key("panel-at")).Absolute().Left(0).Top(0).Children(func() {
		b := a.panelBody(c, p).Bounds()
		if b.H <= 0 {
			// Not laid out yet: the next frame measures it.
			c.After(0)
			return
		}
		if h := int(min(b.H, panelMaxHeight) + 0.5); h != a.panelHeight && a.resizePanel != nil {
			a.panelHeight = h
			a.resizePanel(h)
		}
	})
}

// say puts a sentence together from its parts: a name that starts or ends
// in a Latin letter or a digit is set apart from the Chinese beside it by
// a space, as everywhere else ("Mia 的 MacBook 连不上", "等 Leo 同意你加入").
func say(parts ...string) string {
	latin := func(r rune) bool { return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }
	var b strings.Builder
	for i, part := range parts {
		if i > 0 && part != "" && b.Len() > 0 {
			last, _ := utf8.DecodeLastRuneInString(b.String())
			first, _ := utf8.DecodeRuneInString(part)
			if latin(last) != latin(first) && (latin(last) || latin(first)) && last != ' ' && first != ' ' {
				b.WriteByte(' ')
			}
		}
		b.WriteString(part)
	}
	return b.String()
}

// panelShared is a device given to me, in the panel: its tile and name,
// and where it comes from and how it is ("设计部 · 直接连上"). A click opens
// the window on it.
func (a *app) panelShared(c *ui.Context, p *palette, sh model.SharedDevice) {
	m := a.snap.Device(sh.Device)
	if m == nil {
		return
	}
	row := ui.ButtonBase(c.Key("panel-shared-"+string(sh.Device))).Height(panelRowHeight).Padding(0, 8).Gap(12).Radius(12).FocusRing(false)
	if row.Hovered() {
		row.Background(p.well)
	}
	from := short(sh.FromLAN)
	if from == "" {
		if who := a.snap.Person(sh.From); who != nil {
			from = who.Name
		}
	}
	col := ui.Hex("#a1a1aa")
	if who := a.snap.Person(m.Owner); who != nil {
		col = ownerColors[who.Color%model.NumColors]
	}
	row.Children(func() {
		a.profileTile(c, p, m.ID, glyphFor(m.Kind), col, 24, false, 0)
		ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
			ui.Text(c, m.Name).FontSize(13).FontWeight(500).SingleLine()
			line := from + " · " + a.pathWords(sh.Device)
			ui.Text(c.Key("line-"+line), line).FontSize(12).TextColor(p.ink3).SingleLine().
				Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Enter: &ui.Motion{}})
		})
	})
	if row.Clicked() {
		a.openMain(c)
		a.show(c, model.SharedLANID, false)
		a.selected = string(sh.Device)
	}
}

// pathWords is how this computer reaches dev, in the words of its second
// line on the canvas.
func (a *app) pathWords(dev model.DeviceID) string {
	s := a.snap
	if s.NoNetwork {
		return "等这台电脑联网"
	}
	pt := s.Path(dev)
	switch pt.Kind {
	case model.PathRelay:
		name := "中转站"
		if r := s.Relays[pt.Relay]; r != nil {
			name = r.Name
		}
		return "经" + name + "中转"
	case model.PathConnecting:
		return "正在连接…"
	case model.PathUnreachable:
		return "连不上"
	case model.PathOffline:
		return "不在线"
	case model.PathRefused:
		return "主人不让你用"
	}
	return "直接连上"
}

// panelLabel names a part of the menu bar's panel: on the rows' edge, 12
// under the part before it, 4 over its own rows (DESIGN §3.9).
func panelLabel(c *ui.Context, p *palette, s string) {
	ui.Text(c, s).FontSize(12).FontWeight(500).TextColor(p.ink3).Padding(panelLabelTop, 8, panelLabelBottom, 8)
}
