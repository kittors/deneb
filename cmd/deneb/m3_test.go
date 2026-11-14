package main

import (
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// M3 in the demo (DESIGN §6.11): one of my devices held up to someone's
// ball is given to them, and comes back to its place; its card says to
// whom and which way, and takes it back with undo; the devices given to me
// have a canvas of their own; the seats say a word only when few are left,
// and when none are, nothing new goes in, without a shake or a dialog.

type m3rig struct {
	t      *testing.T
	a      *app
	d      *client.Demo
	tt     *ui.Tester
	px, py float32 // where the pointer is
	// When recording (TestM3Frames): a frame a 60th of a second into dir,
	// and stills beside it, named after it.
	dir, still string
	n          int
	// onShot, when set, looks at the window as each still would be taken,
	// recording or not.
	onShot func(state string)
}

func newM3Rig(t *testing.T) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	d.Simulate("m3")
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.run(800 * time.Millisecond)
	return r
}

func (r *m3rig) run(dt time.Duration) {
	step := 16 * time.Millisecond
	if r.dir != "" {
		step = time.Second / 60
	}
	for end := r.tt.Now().Add(dt); r.tt.Now().Before(end); {
		r.tt.Advance(step)
		r.tt.Frame()
		if r.dir != "" {
			f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%04d.jpg", r.n)))
			if err != nil {
				r.t.Fatal(err)
			}
			jpeg.Encode(f, r.tt.Image(), &jpeg.Options{Quality: 88})
			f.Close()
			r.n++
		}
	}
}

// shot writes a still of the window, twice as sharp, when recording.
func (r *m3rig) shot(state string) {
	if r.onShot != nil {
		r.onShot(state)
	}
	if r.dir == "" {
		return
	}
	r.tt.SetScale(2)
	r.tt.Frame()
	f, err := os.Create(r.still + "_" + state + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	png.Encode(f, r.tt.Image())
	f.Close()
	r.tt.SetScale(1)
	r.tt.Frame()
}

// tile is where a device of the canvas shows, in the window.
func (r *m3rig) tile(id string) ui.Point {
	r.t.Helper()
	d := r.a.device(id)
	if d == nil {
		r.t.Fatalf("no %s on the canvas; the window shows %q", id, r.tt.Texts())
	}
	p := d.at()
	return ui.Point{X: r.a.origin.X + p.X, Y: r.a.origin.Y + p.Y}
}

// ball is where someone's ball shows in the header, in the window.
func (r *m3rig) ball(id string) ui.Point {
	b, ok := r.a.ballRects[id]
	if !ok {
		r.t.Fatalf("no ball of %s to drop on; balls %v", id, r.a.ballRects)
	}
	return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
}

// hold presses at from and moves to where to says, step by step as a hand
// does, the frames running; it holds on there.
func (r *m3rig) hold(from ui.Point, to func() ui.Point) {
	r.move(from.X, from.Y)
	r.run(100 * time.Millisecond)
	r.tt.Press(from.X, from.Y)
	r.run(50 * time.Millisecond)
	for i := 1; i <= 14; i++ {
		p := to()
		k := float32(i) / 14
		r.move(from.X+(p.X-from.X)*k, from.Y+(p.Y-from.Y)*k)
		r.run(32 * time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		// Where it is now that the balls spread out.
		p := to()
		r.move(p.X, p.Y)
		r.run(120 * time.Millisecond)
	}
}

func (r *m3rig) move(x, y float32) {
	r.px, r.py = x, y
	r.tt.Move(x, y)
}

func (r *m3rig) letGo() {
	r.tt.Release(r.px, r.py)
	r.run(900 * time.Millisecond)
}

func (r *m3rig) click(text string) {
	r.t.Helper()
	if err := r.tt.Click(text); err != nil {
		r.t.Fatalf("click %q: %v; the window shows %q", text, err, r.tt.Texts())
	}
	r.run(500 * time.Millisecond)
}

func (r *m3rig) has(text string) bool {
	for _, s := range r.tt.Texts() {
		if strings.Contains(s, text) {
			return true
		}
	}
	return false
}

func (r *m3rig) shareOf(dev, who string) *model.Share {
	m := r.d.Snapshot().Devices[model.DeviceID(dev)]
	if m == nil {
		return nil
	}
	return m.ShareTo(model.PersonID(who))
}

// Held up to 阿明's ball on the canvas of the devices given to me, this
// computer is given to him, opening what it opens to its LANs, and goes back
// to its place; the line under the balls said what letting go would do.
func TestShareByDropOnBall(t *testing.T) {
	r := newM3Rig(t)
	r.click("共享给我")
	r.run(1200 * time.Millisecond)
	rest := r.a.device("mac").at()
	r.hold(r.tile("mac"), func() ui.Point { return r.ball("ming") })
	if line := r.a.aimLine(); line != "松手，只给阿明用这一台" {
		t.Errorf("over 阿明's ball the line says %q", line)
	}
	if !r.a.upHigh(r.a.device("mac")) {
		t.Error("held up in the header, the tile is not small")
	}
	r.letGo()
	sh := r.shareOf("mac", "ming")
	if sh == nil {
		t.Fatal("not given to 阿明")
	}
	if len(sh.Services) != 2 || !sh.Services[0].On || sh.Services[0].Key != "tcp/3389" || sh.Services[1].On {
		t.Errorf("it opens %+v to him, want its 远程桌面 on and 共享文件夹 off", sh.Services)
	}
	if !r.has("已把这台电脑分享给阿明") {
		t.Errorf("no toast; the window shows %q", r.tt.Texts())
	}
	r.run(600 * time.Millisecond)
	if at := r.a.device("mac").at(); dist(at, rest) > 1 {
		t.Errorf("it rests at %v, not back at %v", at, rest)
	}
	if r.a.aim.d != nil {
		t.Error("the aim outlives the drag")
	}
}

// From the tray: 我的 NAS, not in 周末联机, given to 阿杰, back in its slot;
// a click on his ball shows what I gave him.
func TestShareFromTray(t *testing.T) {
	r := newM3Rig(t)
	r.shareNAS("jie")
	if l := r.d.Snapshot().LAN("weekend"); l.Has("mynas") {
		t.Error("it went into 周末联机 too")
	}
	if r.a.trayOff["mynas"] == nil || *r.a.trayOff["mynas"] != (ui.Point{}) {
		t.Errorf("not back in its slot: %v", r.a.trayOff["mynas"])
	}
	r.run(4500 * time.Millisecond)
	b := r.ball("jie")
	r.tt.ClickAt(b.X, b.Y)
	r.run(600 * time.Millisecond)
	for _, want := range []string{"周末联机的成员", "你分享给阿杰的", "我的 NAS", "仅允许阿杰访问"} {
		if !r.has(want) {
			t.Errorf("his card lacks %q: %q", want, r.tt.Texts())
		}
	}
}

// shareNAS drags 我的 NAS out of the tray onto who's ball.
func (r *m3rig) shareNAS(who string) {
	r.t.Helper()
	slot, ok := r.a.slots["mynas"]
	if !ok {
		r.t.Fatalf("我的 NAS is not in the tray: %q", r.tt.Texts())
	}
	from := ui.Point{X: r.a.origin.X + slot.X, Y: r.a.origin.Y + slot.Y}
	r.hold(from, func() ui.Point { return r.ball(who) })
	r.letGo()
	if r.shareOf("mynas", who) == nil {
		r.t.Fatalf("not given to %s: %q", who, r.tt.Texts())
	}
}

// What cannot go says so while it is held, and nothing is sent: 阿杰 has
// it already; Mia, in 周末联机 with my game console, uses it already.
func TestShareCannotGo(t *testing.T) {
	r := newM3Rig(t)
	r.shareNAS("jie")
	r.run(4500 * time.Millisecond)
	v := r.d.Snapshot().Version
	slot := r.a.slots["mynas"]
	r.hold(ui.Point{X: r.a.origin.X + slot.X, Y: r.a.origin.Y + slot.Y}, func() ui.Point { return r.ball("jie") })
	if line := r.a.aimLine(); line != "阿杰已经有了" {
		t.Errorf("over 阿杰's ball the line says %q", line)
	}
	r.letGo()
	r.hold(r.tile("game"), func() ui.Point { return r.ball("mia") })
	if line := r.a.aimLine(); line != "Mia 已经能用了" {
		t.Errorf("my game console over Mia's ball: the line says %q", line)
	}
	r.letGo()
	if got := r.d.Snapshot().Version; got != v {
		t.Errorf("something was sent: version %d, was %d", got, v)
	}
}

// The card of my device says to whom and which way; a click on the chip
// shows what it opens to them; × takes it back at once, undo gives it back
// as it was, and once the toast goes untouched the seat it kept comes free
// (share.release).
func TestShareCardRevokeUndoRelease(t *testing.T) {
	r := newM3Rig(t)
	r.click("共享给我")
	r.run(1200 * time.Millisecond)
	r.hold(r.tile("mac"), func() ui.Point { return r.ball("ming") })
	r.letGo()
	r.run(4500 * time.Millisecond) // the toast goes
	r.tt.ClickAt(r.tile("mac").X, r.tile("mac").Y)
	r.run(600 * time.Millisecond)
	if !r.has("分享给") || !r.has("仅允许阿明访问") {
		t.Fatalf("the card does not say it: %q", r.tt.Texts())
	}
	r.click("分享给阿明")
	if !r.has("开放给阿明的") {
		t.Fatalf("the chip shows nothing: %q", r.tt.Texts())
	}
	r.revoke(t, "mac", "ming")
	if r.shareOf("mac", "ming") != nil {
		t.Fatal("still given to him")
	}
	if !r.has("已从阿明收回这台电脑") {
		t.Fatalf("no toast: %q", r.tt.Texts())
	}
	r.click("撤销")
	sh := r.shareOf("mac", "ming")
	if sh == nil || len(sh.Services) != 2 || !sh.Services[0].On {
		t.Fatalf("undo did not give it back as it was: %+v", sh)
	}
	r.revoke(t, "mac", "ming")
	if !r.d.Snapshot().Seats.Holds("ming") {
		t.Fatal("阿明's seat went with the toast still up")
	}
	r.run(4500 * time.Millisecond)
	if r.d.Snapshot().Seats.Holds("ming") {
		t.Error("阿明's seat stays once the toast went")
	}
}

// revoke clicks the × of who's chip in the open card.
func (r *m3rig) revoke(t *testing.T, dev, who string) {
	t.Helper()
	b, ok := r.tt.Find("分享给" + r.a.people[who].name)
	if !ok {
		t.Fatalf("no chip of %s: %q", who, r.tt.Texts())
	}
	r.tt.Move(b.X+b.W/2, b.Y+b.H/2)
	r.run(200 * time.Millisecond)
	if err := r.tt.Click("收回"); err != nil {
		t.Fatalf("no ×: %v", err)
	}
	acceptConfirmation(t, r.tt, "收回共享")
	r.run(500 * time.Millisecond)
}

// The devices given to me, on their own canvas: where each comes from and
// how it is connected, its card one way; "取消共享" gives it back, with undo.
func TestSharedWithMe(t *testing.T) {
	r := newM3Rig(t)
	r.click("共享给我")
	r.run(1200 * time.Millisecond)
	if !r.a.shared() {
		t.Fatal("the canvas did not change")
	}
	for id, want := range map[string]string{"gpu": "设计部 · 直接连上", "mingnas": "阿明 · 经香港中转"} {
		if d := r.a.device(id); d == nil || r.a.status(d) != want {
			t.Errorf("%s: %v says %q, want %q", id, d != nil, r.a.status(r.a.device(id)), want)
		}
	}
	if _, line := r.a.summary(&lightPalette); line != "2 台设备都连上了" {
		t.Errorf("the header says %q", line)
	}
	if r.has("邀请朋友") {
		t.Error("an invite button over the devices given to me")
	}
	r.tt.ClickAt(r.tile("gpu").X, r.tile("gpu").Y)
	r.run(600 * time.Millisecond)
	for _, want := range []string{"设计部的设备", "可用服务", "远程桌面", "仅允许你访问此设备", "取消共享"} {
		if !r.has(want) {
			t.Errorf("the card lacks %q: %q", want, r.tt.Texts())
		}
	}
	r.click("取消共享")
	acceptConfirmation(t, r.tt, "取消共享")
	r.run(600 * time.Millisecond)
	if r.a.device("gpu") != nil && r.a.device("gpu").gone.IsZero() {
		t.Error("it stays on the canvas")
	}
	if r.d.Snapshot().SharedOf("gpu") == nil {
		t.Error("given back before the toast went")
	}
	r.click("撤销")
	r.run(600 * time.Millisecond)
	if d := r.a.device("gpu"); d == nil || !d.gone.IsZero() {
		t.Fatal("undo did not bring it back")
	}
	r.run(600 * time.Millisecond)
	r.tt.ClickAt(r.tile("gpu").X, r.tile("gpu").Y)
	r.run(600 * time.Millisecond)
	r.click("取消共享")
	acceptConfirmation(t, r.tt, "取消共享")
	r.run(4500 * time.Millisecond)
	if r.d.Snapshot().SharedOf("gpu") != nil {
		t.Error("not given back once the toast went")
	}
	if r.has("收回了") {
		t.Errorf("my own giving back says someone took it back: %q", r.tt.Texts())
	}
}

// One given to me comes: the sidebar's row lights up and counts it; taken
// back by who gave it, it says so.
func TestSharedComesAndGoes(t *testing.T) {
	r := newM3Rig(t)
	r.click("共享给我")
	r.d.Simulate("share-in")
	r.run(300 * time.Millisecond)
	if _, ok := r.tt.Find("3"); !ok {
		t.Errorf("the row does not count it: %q", r.tt.Texts())
	}
	r.d.Simulate("share-in-up")
	r.run(1500 * time.Millisecond)
	if d := r.a.device("meiprint"); d == nil || r.a.status(d) != "小美 · 直接连上" {
		t.Fatalf("小美's printer: %q", r.tt.Texts())
	}
	r.d.Simulate("share-in")
	r.run(300 * time.Millisecond)
	if !r.has("小美收回了小美的打印机") {
		t.Errorf("it went without a word: %q", r.tt.Texts())
	}
}

// The seats: one left says so under "邀请朋友"; this computer given to 阿明
// takes the last, and "邀请朋友" turns into the dashed outline, its click
// the seats' card; freeing Leo's seat waits for the toast, and undo is
// nothing done.
func TestSeats(t *testing.T) {
	r := newM3Rig(t)
	if line := r.a.seatLine(); line != "还能加 1 人" || !r.has("还能加 1 人") {
		t.Errorf("the line under 邀请朋友 says %q", line)
	}
	r.d.Simulate("seats") // seven
	r.run(300 * time.Millisecond)
	if !r.has("还能加 2 人") {
		t.Errorf("at seven: %q", r.tt.Texts())
	}
	r.d.Simulate("seats") // ten: nothing said
	r.run(300 * time.Millisecond)
	if r.has("还能加") {
		t.Errorf("at ten something is said: %q", r.tt.Texts())
	}
	r.d.Simulate("seats") // six again
	r.run(300 * time.Millisecond)

	r.click("共享给我")
	r.run(1200 * time.Millisecond)
	r.hold(r.tile(string(r.a.snap.Self)), func() ui.Point { return r.ball("ming") })
	r.letGo()
	if r.shareOf("mac", "ming") == nil {
		t.Fatal("this computer not given to 阿明")
	}
	if !r.a.seatsFull() {
		t.Fatalf("seats %+v, not full", r.d.Snapshot().Seats)
	}
	r.click("周末联机")
	r.click("邀请朋友")
	if r.a.selected != seatCard || !r.has("6 个位置都用上了") {
		t.Fatalf("the dashed 邀请朋友 shows %q", r.tt.Texts())
	}
	r.click("腾一个位置")
	if err := r.tt.Click("移出Leo"); err != nil {
		t.Fatalf("no × on Leo: %v", err)
	}
	r.run(500 * time.Millisecond)
	acceptConfirmation(t, r.tt, "移出成员")
	r.run(500 * time.Millisecond)
	if !r.has("已移出 Leo") || !r.has("还能加 1 人") {
		t.Fatalf("freeing Leo: %q", r.tt.Texts())
	}
	if !r.d.Snapshot().Seats.Holds("leo") || !contains(r.d.Snapshot().LAN("weekend").Members, "leo") {
		t.Error("sent before the toast went")
	}
	r.click("撤销")
	if !contains(r.a.snap.LAN("weekend").Members, "leo") || !r.a.seatsFull() {
		t.Error("undo did not bring Leo back")
	}
}

func contains(ps []model.PersonID, who model.PersonID) bool {
	for _, p := range ps {
		if p == who {
			return true
		}
	}
	return false
}

// Seats full: this computer held up to 小美, new to my seats, does not go;
// her ball does not light, the line says why, and nothing is sent.
func TestSeatsFullStopsAShare(t *testing.T) {
	r := newM3Rig(t)
	r.d.Simulate("share-in")
	r.click("共享给我")
	r.run(1200 * time.Millisecond)
	r.hold(r.tile("mac"), func() ui.Point { return r.ball("ming") })
	r.letGo()
	r.run(4500 * time.Millisecond)
	v := r.d.Snapshot().Version
	r.hold(r.tile("mac"), func() ui.Point { return r.ball("mei") })
	if line := r.a.aimLine(); line != "位置满了" || r.a.aim.ok {
		t.Errorf("over 小美's ball the line says %q (ok %v)", line, r.a.aim.ok)
	}
	r.letGo()
	if got := r.d.Snapshot().Version; got != v {
		t.Errorf("something was sent: version %d, was %d", got, v)
	}
	// Nor does her ball, dropped on 周末联机's row, invite her.
	r.hold(r.ball("mei"), func() ui.Point {
		row := r.a.rows["weekend"]
		return ui.Point{X: row.X + row.W/2, Y: row.Y + row.H/2}
	})
	if r.a.personFull != "weekend" || r.a.personOver != "" || !r.has("6 个位置都用上了") {
		t.Errorf("over 周末联机's row: full %q, over %q; %q", r.a.personFull, r.a.personOver, r.tt.Texts())
	}
	r.letGo()
	if got := r.d.Snapshot().Version; got != v {
		t.Errorf("an invite was sent: version %d, was %d", got, v)
	}
}

// Invited by Leo, his seats full: the card says so, and joining waits.
func TestInviteSeatsFull(t *testing.T) {
	r := newM3Rig(t)
	r.d.Simulate("invitation-full")
	r.run(800 * time.Millisecond)
	if !r.has("Leo 的位置满了，腾出来就能加入") {
		t.Errorf("the invite card says %q", r.tt.Texts())
	}
}

// Without M3 on the server, nothing of it shows: the card has no 分享给,
// the header no line, and the balls take nothing.
func TestM3NeedsTheServer(t *testing.T) {
	a, d := newDemoApp()
	d.Manual()
	tt := ui.NewTester(a.view, 1180, 740)
	for i := 0; i < 50; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if a.canShare(a.device("game")) || a.seatLine() != "" || a.seatsFull() || len(a.ballRects) > 0 {
		t.Error("M3 shows on an M2 server")
	}
	p := a.device("game").at()
	tt.ClickAt(a.origin.X+p.X, a.origin.Y+p.Y)
	for i := 0; i < 40; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if tt.HasText("分享给") {
		t.Error("分享给 on an M2 server")
	}
}
