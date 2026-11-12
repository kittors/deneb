package main

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// person is someone in my LANs. Their color marks their devices, so the eye
// tells whose a device is without reading (design/DESIGN.md §3.2).
type person struct {
	id, name, ch string
	color        ui.Color
}

// ownerColors are the owner colors of DESIGN §3.3, in the order of the
// index a snapshot gives each person: blue is me.
var ownerColors = [model.NumColors]ui.Color{
	ui.Hex("#3B82F6"), ui.Hex("#EC4899"), ui.Hex("#F59E0B"), ui.Hex("#8B5CF6"), ui.Hex("#22C55E"), ui.Hex("#06B6D4"),
}

// service is something a device opens to others, in everyday words.
type service struct {
	icon, name string
	on         bool
	svc        model.Service // as the snapshot has it: the key ops name it by, its ports
}

// facts is what the snapshot says about a device. The view copies them in
// as each snapshot comes (refresh) and changes them only by sending ops, so
// that a device on its way out of the LAN still shows as it was.
type facts struct {
	// id keys it on the canvas: the device's own id, or in the "全部" view,
	// for its second region and on, the LAN's and the device's (spot).
	id, kind, owner, name string
	dev                   model.DeviceID // the device, as the snapshot names it
	lan                   model.LANID    // the LAN it shows in: the canvas's, or its region's
	// path is how this computer reaches it: "self", "direct", "relay",
	// "connecting", "unreachable", "offline", "paused" or "refused".
	path      string
	relayID   model.RelayID // the relay it goes through, when path is "relay"
	rtt       time.Duration
	mine      bool // mine, so mine to take out of the LAN
	online    bool
	refusesMe bool            // another's, whose owner does not let me use it
	refused   map[string]bool // the people I refused on it (mine only)
	services  []service       // what it opens in this LAN
	addr      string
	fqdn      string

	// M2 (DESIGN §6.7–§6.8).
	pending  bool        // it waits for the owner to let it into this LAN
	login    model.Login // when its login runs out: mine only, another's only whether it has
	lastSeen time.Time   // when the server last heard from it, while offline
	using    time.Time   // since when this computer uses one of its services
	twin     int         // 2, 3…: the second, third device of its name on the canvas
	stale    bool        // mine, of a name another of mine has, and gone for 30 days
}

// device is a tile on the canvas: its facts, and how the view shows it.
// base is where its tile's center sits in the canvas; off is how far it is
// from there, which its drag gesture owns and a flight (into the tray, or
// out of it) sets; live is where it shows in the frame being built, for the
// links to follow it while it moves.
type device struct {
	facts
	base, off, live ui.Point
	rest            ui.Point  // where it rested before the drag going on
	joined          time.Time // when it joined, to draw its track out
	leaving         time.Time // when it set off into the tray, or off the canvas, to draw its track back in
	dragging        bool      // the pointer drags it (last frame)
	flying          bool      // on its way somewhere the hand did not carry it: it floats over the rest
	arriving        bool      // just out of the tray: its first frame shows it as it was there
	arrive          ui.Pose   // how it looked in the tray, or in the hand
	aimed           bool      // on its way into the tray, and the tray's slot is known
	dim             float32   // how far its link has faded, as it is held over the tray (0 to 1)
	up              float32   // how far its link has faded away, as it is held up in the header (0 to 1)
	cut             float32   // how far its link is cut, its owner refused on this computer (0 to 1)
	faded           float32   // how far it dims with its region while another LAN has the focus (0 to 1)
	way             ui.Point  // the way it steps aside for a dragged one
	push            ui.Point  // how far it is stepping aside now
	// popped is when it came onto the canvas without the hand, joined
	// elsewhere; its first frame shows it small and clear.
	popped   time.Time
	popFirst bool // the frame it pops in in is still to come
	// gone is when it set off the canvas without the hand, gone from the LAN
	// elsewhere: it shrinks away as its track is drawn back in, and is taken
	// off the canvas once it has.
	gone time.Time
	// unplaced: it has no place on this LAN's canvas yet, and finds one as
	// soon as the canvas is laid out.
	unplaced bool
	// path changes are drawn: lastPath is the path its link was drawn for,
	// and pathAt when it changed to the one it has.
	lastPath string
	fromPath string
	// lastRelay and fromRelay are the relays of those paths: a link that
	// goes from one relay to another is drawn anew too (M4).
	lastRelay, fromRelay model.RelayID
	pathAt               time.Time
	// oldGeo is the shape of the link it had before it went from direct to
	// the relay, or back: it fades as the new one grows out.
	oldGeo *linkGeo
	// waited: it was waiting to be let in as the canvas was last brought to
	// the snapshot; approved is when it was let in, to spring up full.
	waited   bool
	approved time.Time
	// turnedDown: mine, it waited and the owner turned it away. It keeps
	// its dashed look on its way back to the tray.
	turnedDown bool
	// closed is the service of this computer it was using that was just
	// closed to it (scene 5), told of at closedAt on the view's clock: a
	// short red light runs on its link, and its second line says so for a
	// moment. closedSeen is the store's moment of that cut, to tell a new
	// one from it.
	closed               string
	closedAt, closedSeen time.Time
	// elem keys its element while it moves from region to region in the
	// "全部" view (rekey): the element, and its springs, stay with it.
	elem string
}

// key is what its element is keyed by: its id as it came onto the canvas.
func (d *device) key() string {
	if d.elem != "" {
		return d.elem
	}
	return d.id
}

// at is where the device rests: its base moved by the user.
func (d *device) at() ui.Point { return ui.Point{X: d.base.X + d.off.X, Y: d.base.Y + d.off.Y} }

// tileColor is the color of a device's tile: its owner's, or grey while it
// is offline (DESIGN §3.4).
func (a *app) tileColor(d *device) ui.Color {
	if d.path == "offline" || d.path == "paused" {
		return offlineGrey
	}
	return a.people[d.owner].color
}

var offlineGrey = ui.Hex("#a1a1aa")

// removable reports whether I can take the device out of the LAN: it is
// mine, and not the computer I am using.
func (d *device) removable() bool { return d.mine && d.path != "self" }

// relayStop is a relay on the canvas: one a link goes through when it
// cannot go direct, or, from M4, one dragged into the LAN (DESIGN §6.12).
type relayStop struct {
	id model.RelayID
	// legacy: a relay of M1–M3, which keeps its place under the key it
	// always had. down: it stopped (M4): grey, "连不上".
	legacy, down    bool
	name, sub       string
	nameW           float32 // about how wide the wider of the two lines is
	base, off, live ui.Point
	dragging        bool
	way, push       ui.Point
	side            string // where its name goes: the side no track leaves from
	unplaced        bool   // it has no place on this LAN's canvas yet
	// byHand: the person put it where it is, on this LAN's canvas, now or
	// before; a place it took by itself is looked at again as it comes to
	// show (checked), and changed if a track through it would cross a
	// name or a tile there (relayClear).
	byHand, checked bool
	// shown: it was on the canvas in the frame before. It pops in when a
	// link comes to go through it (popFirst, poppedAt), and shrinks away
	// from leftAt, once the last link through it goes.
	shown    bool
	popFirst bool
	poppedAt time.Time
	leftAt   time.Time
	// flight: on its way between the tray and the canvas (M4).
	flight *relayFlight
}

// notice is the toast: what just happened, to which device, and its undo.
type notice struct {
	id    int
	text  string
	dev   *device
	undo  func()
	until time.Time
	top   bool // shown at the top, so as not to cover the device it is about
	// commit sends what the toast holds back, once it goes without undo
	// (hold).
	commit func(c *ui.Context)
	// orb: it is about a relay, whose ball leads it (M4).
	orb bool
	// undoLabel names its button when that is no undo: "在访达中显示" (M6).
	undoLabel string
}

// pending is an op the view sent that the store has not confirmed yet. It
// stays laid over the store's snapshot until the snapshot has it, so that
// what the view shows never goes back to what it was in between.
type pending struct {
	op   model.Op
	done bool   // the store answered
	ver  uint64 // the version it took effect in
	err  error
	held bool // shown, but not sent until its toast goes (hold)
}

// app is the client's view: the LAN on the canvas, as the store's snapshot
// tells it, and how everything on the canvas shows and moves.
type app struct {
	store  client.Store
	base   *model.Snapshot // the store's snapshot the view last took
	snap   *model.Snapshot // base, with the ops not yet confirmed laid over it: what the view shows
	pend   []*pending
	gen    int         // bumped as pend changes, so that sync knows to lay it over again
	synced int         // the gen the snapshot was last laid over at
	lan    model.LANID // the LAN on the canvas
	me     string      // my person id
	self   string      // this computer's device id
	places *places     // where things rest on each LAN's canvas

	// Work done off the main thread hands its results back through post;
	// wake asks the window for a frame to take them in.
	inboxMu sync.Mutex
	inbox   []func(c *ui.Context)
	wake    func()
	// copyText puts text on the clipboard, and openURL opens a link with the
	// system's app for it: the window's, or a test's.
	copyText func(string)
	openURL  func(string) error
	// signer signs this computer in from the first step; adapter puts the
	// network adapter in as an administrator, where the window does that
	// (Windows); onboarded: the first time's steps were done on this
	// computer, or put off, which saveOnboarded keeps. The real window's:
	// the demo pretends (DESIGN §7.3).
	signer        signer
	adapter       func(ctx context.Context) error
	onboarded     bool
	saveOnboarded func()

	people  map[string]*person
	members []string  // the people of the LAN, in the order they joined
	devices []*device // what the canvas shows
	tray    []*device // my devices not in the LAN
	trayOff map[string]*ui.Point
	slots   map[string]ui.Point // where each tray item's slot is in the canvas, from the last layout
	relays  []*relayStop
	// strip is the height of the strip the window buttons take above the
	// header, where they are at the top right (DESIGN §7.2); platform is
	// where the app runs, as runtime.GOOS says ("windows": the system
	// rounds and outlines the tray's panel); chrome, set by the
	// recordings, draws the window buttons as the system does.
	strip    float32
	platform string
	chrome   func(c *ui.Context, p *palette, bar ui.TitleBar)
	// onboard is the first time's steps while they show (DESIGN §7.3).
	onboard *onboarding
	// adapterJob is the network adapter put in from its notice, on a
	// computer signed in already (DESIGN §7.3, M6's second batch).
	adapterJob adapterJob
	// M7 (DESIGN §6.15): the account card, open over the window above the
	// sidebar's last row (where that was in the last frame, and how tall
	// the window is); the page that deletes the account, while it shows;
	// what the sign-in says once the account was deleted.
	accountOpen  bool
	accountRowAt ui.Rect
	winH         float32
	deleting     *accountDeleting
	gone         *accountGone
	curtain      *curtain // the window fading to its background, and back, around the sign-in
	// diagAt is when the diagnostics package began to be put together, while
	// it is (DESIGN §6.14); diagBusy, while the engine puts it together.
	// diagnostics has the engine put it together into the downloads, and
	// says where (nil in the demo, which pretends); reveal shows a file in
	// this system's file manager.
	diagAt      time.Time
	diagBusy    bool
	diagnostics func() (string, error)
	reveal      func(path string)
	// M4's relays in the tray and their cards (DESIGN §6.12): the "谁在用"
	// chips as a card opened; the install commands asked for here, by
	// their join, the one whose card the "+" opened, and when each was
	// copied; what the tray says of the relay slot the pointer is on (from
	// the frame before, and this one's); when each of my relays came up
	// while the window was open; the relay of the tray in the hand, out
	// over the canvas.
	relayChips   *relayChips
	commands     map[string]string
	joinAsked    string
	copiedAt     map[string]time.Time
	trayHint     string
	trayHintNext string
	newRelays    map[model.RelayID]time.Time
	knownRelays  map[model.RelayID]bool
	heldRelay    model.RelayID
	heldRelayAt  ui.Point
	cameHint     string
	cameUntil    time.Time
	selected     string
	more         bool // the card's "更多" is open
	toast        *notice
	toasts       int       // toasts so far, to key each one
	start        time.Time // when the window opened
	origin       ui.Point  // the canvas's top left in the window, from the last layout
	cw, ch       float32   // the canvas's size in the frame being built

	drag    dragNow // what the pointer drags, from the frame before
	landing *device // one of mine on its way into the tray
	// landingByHand: it was let go over the tray, rather than sent by a
	// button, so the tray still says what letting go does.
	landingByHand bool
	slot          ui.Point  // the slot the tray opened for it, in the canvas as it shows
	slotOK        bool      // the slot is laid out, so slot is where it is
	held          *device   // a device of the tray the hand holds out over the canvas, from the frame before
	heldAt        ui.Point  // where it is, in the canvas
	trayUp        bool      // the tray shows, or is going away
	trayOut       time.Time // when the tray, going away, is gone

	cardAt    ui.Rect               // where the card shows, in the canvas, from the last layout: drops keep clear of it
	cardLeft  bool                  // the card shows at the canvas's left (cardOnLeft)
	trayAt    ui.Rect               // where the tray shows, in the canvas, from the last layout: drops go into it
	geos      map[string]*linkGeo   // each link's shape, while its ends stay put
	beamPath  ui.Path               // the path a beam is drawn with, reused
	beamStops [3][3]ui.GradientStop // the stops of a beam's three strokes, reused
	beams     map[string]*beamRun   // the light on each link, from frame to frame
	beamsAt   time.Time             // the frame the light was last moved on in

	// unreconciled: the snapshot changed since the canvas was last brought
	// to it.
	unreconciled bool
	// clock is the view's clock as the frame being built has it (now).
	clock time.Time

	renaming *renaming       // a name edited in place (DESIGN §6.9)
	picking  bool            // the card's row of kinds is open
	watching map[string]bool // devices to tell of as they come online (DESIGN §6.8)
	// seenInvitations are the invites by name whose card has shown once:
	// one put off stays in the sidebar, rather than show again by itself.
	seenInvitations map[string]bool
	noticesDone     map[string]bool // notices put away with "知道了", by code

	// A person's ball dragged from the header onto a LAN's row in the
	// sidebar invites them there (DESIGN §6.7).
	person     *personDrag
	personOver model.LANID // the row it would be let go on
	// personFull is the row of a LAN of mine the ball is over, mine full
	// and they new to it: it does not light, and says so (DESIGN §6.11).
	personFull model.LANID

	// M3 (DESIGN §6.11): one of my devices held up to a ball in the header
	// gives it to that person.
	ballRects     map[string]ui.Rect   // where each ball one can be given to was, in the window
	aim           shareAim             // what the hand aims at this frame
	trayDrag      *device              // the tray's device in the hand, last frame
	trayDragAt    ui.Point             // where its tile was, in the canvas
	unclipUntil   time.Time            // until when the canvas lets a tile show over the header, on its way back
	popAt         map[string]time.Time // when a ball took a device: it springs
	shareOpen     string               // the share chip open in a card: device/person
	sharePick     string               // the device whose "+" shows whom it can go to
	pickNote      string               // the person "+" says "位置满了" of
	pickNoteUntil time.Time
	seatFreeing   bool                     // the seats' card shows × on each ball
	seatFor       string                   // the person waiting whom the seats' card opened for
	freeing       map[string]bool          // people whose seat a toast frees
	leaving       map[model.DeviceID]bool  // devices given to me I gave back here
	knownShared   map[model.DeviceID]bool  // the devices given to me, last snapshot
	ballOffs      map[string]*ui.Point     // each ball's drag offset
	ballHome      map[string]ui.Point      // where each ball rests, in the window
	stackWas      map[string]int           // how each person was in the stack, last frame
	stackIn       map[string]time.Time     // when each was let in, to spring up full
	remedyWas     map[string]string        // the card's last remedy for each device, kept while it folds shut
	rows          map[model.LANID]*ui.Rect // where the sidebar's LAN rows are

	// The "全部" view (DESIGN §9 scene 3): each LAN's region as it shows
	// (regionRects) and as its devices rest (regionRest), how far the hand
	// moved one (regionOff), the generation of its springs, when it came in
	// and went, how it was painted last, and which one the hand holds.
	regionRects  map[model.LANID]ui.Rect
	regionRest   map[model.LANID]ui.Rect
	regionOff    map[model.LANID]ui.Point
	regionGen    map[model.LANID]int
	regionBorn   map[model.LANID]time.Time
	regionLeft   map[model.LANID]time.Time
	regionLast   map[model.LANID]regionPaint
	regionPaints []regionPaint
	regionsLaid  bool
	regionHeld   model.LANID
	// focus is the LAN whose sidebar row the pointer is on, the others
	// dimmed, until unfocusAt once it is off the rows.
	focus     model.LANID
	unfocusAt time.Time
	// Built from the devices, a frame late: the region a device held would
	// move into, and why another's stops at its region's edge.
	dropOver, dropNext model.LANID
	edgeNote, edgeNext *edgeNote
	edgeHit            string // the device of another's that reached its region's edge in this drag

	// The menu bar's panel (DESIGN §7.1): the chores it just did, said in
	// their place for a moment; why something it did went wrong; how tall
	// it last asked to be; and what the app gives it: the window brought to
	// the front, the panel resized. panelShown is set while it shows, and
	// systemPanel where the system's flyout draws its shadow and outline.
	panelDone   map[string]*doneRow
	panelNote   panelNote
	panelHeight int
	foldH       map[string]float32 // each folding group's height, as last laid out
	showMain    func()
	resizePanel func(height int)
	panelShown  bool
	systemPanel bool

	naming *naming // a new LAN being named in the sidebar
	// wantLAN is a LAN made or joined here that the snapshot does not have
	// yet: the canvas goes to it once it has.
	wantLAN model.LANID
	// knownLANs are the LANs the sidebar has shown; one new to it lights up
	// as it comes in, from the frame it first shows in (flashFrom).
	knownLANs map[model.LANID]bool
	flashFrom map[model.LANID]time.Time

	invite    *inviteCard // someone asks me into a LAN (scene 3)
	laterLink string      // a link opened before the login, opened after it

	loggingIn bool   // the login page is open in the browser
	loginErr  string // why the login did not start
	engineErr string // why this computer's engine could not be started

	// sims are the outside events ⌥⌘1 to ⌥⌘5 play, in the demo and in the
	// fake world: someone takes a device out elsewhere, the network changes,
	// an invite comes.
	sims map[ui.Key]func(c *ui.Context, a *app)
}

// newApp returns the view of the store's world, with things resting where
// pl keeps them, and this computer's card open, as the design's sample
// shows it.
func newApp(s client.Store, pl *places) *app {
	a := &app{
		store:   s,
		places:  pl,
		people:  map[string]*person{},
		trayOff: map[string]*ui.Point{},
		slots:   map[string]ui.Point{},
		// M4 (DESIGN §6.12).
		commands:  map[string]string{},
		copiedAt:  map[string]time.Time{},
		newRelays: map[model.RelayID]time.Time{},
		beams:     map[string]*beamRun{},
		geos:      map[string]*linkGeo{},
	}
	// Where the app runs: the first time's words and the toasts say what
	// this system calls things (DESIGN §7.3, §6.14).
	a.platform = runtime.GOOS
	a.sync(nil)
	if len(a.snap.LANs) > 0 {
		a.lan = a.snap.LANs[0].ID
	}
	a.load()
	a.selected = a.self
	// A new snapshot asks for a frame built anew, which takes it in: the
	// frames the light runs in only repaint, and never read the store.
	s.Subscribe(func(*model.Snapshot) { a.wakeUp() })
	return a
}

func (a *app) device(id string) *device {
	for _, d := range a.devices {
		if d.id == id {
			return d
		}
	}
	return nil
}

// status is a device's second line: how it is connected, in a few words.
func (a *app) status(d *device) string {
	if from := a.sharedFrom(d); from != "" {
		// On the canvas of the devices given to me: where it comes from,
		// then how it is (DESIGN §6.11: "设计部 · 直接连上").
		return from + " · " + a.statusOf(d)
	}
	return a.statusOf(d)
}

// statusOf is what d's second line says of it, wherever it comes from.
func (a *app) statusOf(d *device) string {
	switch {
	case a.all() && d.id == a.self:
		// In no region of the "全部" view: how many it is in (scene 3).
		if n := a.inLANs(); n > 0 {
			return "在 " + strconv.Itoa(n) + " 个局域网里"
		}
		return "还没加进局域网"
	case d.pending && d.mine:
		return "等" + spaced(a.ownerName()) + "同意"
	case d.pending:
		return "等你同意"
	case a.cutOff(d):
		return "用不了这台电脑"
	case d.login.Expired:
		return "登录过期了"
	case d.mine && d.login.Due(a.now()):
		return "需要重新登录"
	case d.stale:
		return "旧的那台 · 很久没上线"
	case d.closed != "" && a.now().Sub(d.closedAt) < closedSay:
		// Using a service of this computer that was just closed to it
		// (scene 5): "命令行被关了", for a moment.
		return d.closed + "被关了"
	case !d.using.IsZero() && linked(d.path):
		return "已连接 " + clock(a.now().Sub(d.using))
	}
	switch d.path {
	case "self":
		return "你正在用"
	case "relay":
		return a.viaWords(d)
	case "connecting":
		return "正在连接…"
	case "unreachable":
		return "连不上"
	case "offline":
		return "不在线"
	case "nonet":
		return "等这台电脑联网"
	case "paused":
		return "已暂停"
	case "refused":
		return "主人不让你用"
	case "apart":
		// In a LAN this computer is not in: there is no link to tell of.
		return "在线"
	}
	return "直接连上"
}

// cutOff reports whether I refused the owner of d on this computer: its
// link to it is cut (scene 5).
func (a *app) cutOff(d *device) bool {
	me := a.device(a.self)
	return me != nil && d != me && d.owner != a.me && me.refused[d.owner]
}

// spaced puts spaces around a name in Latin letters, as Chinese text sets
// them apart: "这是 Mia 的设备", but "这是阿杰的设备".
func spaced(name string) string {
	if name != "" && name[0] < 0x80 {
		return " " + name + " "
	}
	return name
}

// personDrag is a person's ball in the hand: whose, and where the pointer
// is, in the window.
type personDrag struct {
	id string
	at ui.Point
}
