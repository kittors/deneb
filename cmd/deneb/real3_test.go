package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestRealAcceptance3 plays the window's part in M3's acceptance
// (design/plan/m3-acceptance.md) on this computer's real engine, when
// DENEB_REAL3 lists the items, as "share,shared". Frames and log go as
// M2's (DENEB_FRAMES, DENEB_DARK). 阿杰 acts on his desktop (the Windows
// test machine) and his NAS (hk-spare) by the engine's command line over
// ssh; 构建服务器 is hk-155's container.
//
//   - share: 构建服务器 and liang的MacBook Air leave 周末联机 (阿杰's), so
//     that 阿杰 sees neither through it. 构建服务器, in 周末联机's tray,
//     held up to 阿杰's ball is given to him: his desktop lists it and
//     uses its services; it lists no device only in 工作室; 构建服务器
//     reaches none of his devices. Taken back from his card, his desktop
//     loses it, and how soon is measured against the click; undo gives it
//     back; taken back again, the toast goes and the seat with it. Both
//     devices go back into 周末联机 at the end.
//   - shared: 阿杰 gives me his NAS from it: "共享给我" counts it and shows
//     it, "阿杰 · 直接连上"; its card says whose and which way; "不要了"
//     gives it back once the toast goes.
func TestRealAcceptance3(t *testing.T) {
	items := os.Getenv("DENEB_REAL3")
	if items == "" {
		t.Skip("set DENEB_REAL3 to the M3 items to play on this computer's engine, as share,shared")
	}
	r := newRealRig(t)
	r.until("the engine, logged in", func() bool { return r.a.snap.Engine && r.a.snap.LoggedIn && r.a.snap.Loaded })
	if !r.a.snap.Can(model.FeatureShare) {
		t.Fatal("the server does not share, or this store does not take it")
	}
	for _, s := range strings.Split(items, ",") {
		fn := map[string]func(*realRig){"share": m3share, "shared": m3shared, "seats": m3seats}[strings.TrimSpace(s)]
		if fn == nil {
			t.Fatalf("no item %q", s)
		}
		r.step = strings.TrimSpace(s)
		r.logf("item %s begins", r.step)
		fn(r)
		r.rec = false
		r.logf("item %s ends", r.step)
	}
}

const (
	buildAddr = "10.48.1.3" // 构建服务器
	nasAddr   = "10.48.1.4" // 阿杰的 NAS
	pcAddr    = "10.48.1.5" // 阿杰的台式机
)

// onBuild is the command that runs the engine's command line on 构建服务器.
func onBuild(args string) string {
	return `ssh -o BatchMode=yes -o ConnectTimeout=15 hk-155 "docker exec deneb-node /denebd ` + strings.ReplaceAll(args, `"`, `\"`) + `"`
}

// liftTo holds from, as a hand does, and carries it to where to says in
// each frame, up to the header's balls; it lets go once ready says so, or
// after a few seconds.
func (r *realRig) liftTo(from ui.Point, to func() ui.Point, ready func() bool) {
	r.liftHold(from, to, ready)()
}

// liftHold is liftTo that holds on there: it returns what lets go.
func (r *realRig) liftHold(from ui.Point, to func() ui.Point, ready func() bool) (letGo func()) {
	r.tt.Move(from.X, from.Y)
	r.tick()
	r.tt.Press(from.X, from.Y)
	r.hold(120 * time.Millisecond)
	at := from
	for i := 1; i <= 40; i++ {
		p := to()
		k := ui.EaseInOut(float32(i) / 40)
		at = ui.Point{X: from.X + (p.X-from.X)*k, Y: from.Y + (p.Y-from.Y)*k}
		r.tt.Move(at.X, at.Y)
		r.tick()
	}
	deadline := time.Now().Add(4 * time.Second)
	for !ready() && time.Now().Before(deadline) {
		p := to()
		r.tt.Move(p.X, p.Y)
		at = p
		r.tick()
	}
	r.hold(400 * time.Millisecond)
	return func() {
		r.tt.Release(at.X, at.Y)
		r.tick()
	}
}

// ballOf is where person who's ball shows in the header, in the window.
func (r *realRig) ballOf(who model.PersonID) func() ui.Point {
	return func() ui.Point {
		b, ok := r.a.ballRects[string(who)]
		if !ok {
			r.t.Fatalf("step %s: no ball of %s in the header", r.step, who)
		}
		return ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
}

// background runs command off the test's thread, and returns what it
// said, line by line, once it is over (wait).
func background(command string) (wait func() []string) {
	out := make(chan []string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		b, _ := cmd.CombinedOutput()
		var lines []string
		sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(string(b), "\r", "")))
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		out <- lines
	}()
	return func() []string { return <-out }
}

// pcOffset is how far 阿杰's desktop's clock is ahead of this computer's,
// as near as one round trip tells.
func (r *realRig) pcOffset() time.Duration {
	// Of five tries, the one with the shortest round trip says it best.
	best, rtt := time.Duration(0), time.Duration(1<<62)
	for range 5 {
		before := time.Now()
		s := r.shellOut(onPCRaw(`echo %TIME%`)) // " 0:16:02.15": to the hundredth
		after := time.Now()
		mid := before.Add(after.Sub(before) / 2)
		s = strings.TrimSpace(s)
		if len(s) == len("0:00:00.00") {
			s = "0" + s
		}
		pc, err := time.ParseInLocation("15:04:05.00", s, time.Local)
		if err != nil {
			r.t.Fatalf("the desktop's clock: %q: %v", s, err)
		}
		pc = time.Date(mid.Year(), mid.Month(), mid.Day(), pc.Hour(), pc.Minute(), pc.Second(), pc.Nanosecond(), time.Local)
		if d := after.Sub(before); d < rtt {
			best, rtt = pc.Sub(mid), d
		}
	}
	r.logf("the desktop's clock is %s ahead (round trip %s, of five the shortest)", best.Round(time.Millisecond), rtt.Round(time.Millisecond))
	return best
}

// probeTimes reads the probe's lines ("15:04:05.000 ok 12.3ms", "… FAIL …")
// as times on this computer's clock, and whether each answered.
func probeTimes(lines []string, offset time.Duration) (at []time.Time, ok []bool) {
	now := time.Now()
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		tm, err := time.ParseInLocation("15:04:05.000", f[0], time.Local)
		if err != nil {
			continue
		}
		tm = time.Date(now.Year(), now.Month(), now.Day(), tm.Hour(), tm.Minute(), tm.Second(), tm.Nanosecond(), time.Local).Add(-offset)
		at = append(at, tm)
		ok = append(ok, f[1] == "ok")
	}
	return at, ok
}

func m3share(r *realRig) {
	build := r.named("构建服务器")
	jie := r.person("阿杰")
	if build == "" || jie == "" {
		r.t.Fatal("share: no 构建服务器, or no 阿杰")
	}
	// Out of 周末联机, both, so that 阿杰 sees them through nothing but
	// what I give him; back in at the end, as they were.
	r.shell("denebd lan remove 周末联机 build")
	r.shell("denebd lan remove 周末联机 liangdemacbook-air")
	r.t.Cleanup(func() {
		exec.Command("/bin/sh", "-c", "denebd lan add 周末联机 build; denebd lan add 周末联机 liangdemacbook-air").Run()
	})
	r.lan("周末联机")
	r.until("构建服务器 in 周末联机's tray", func() bool {
		_, ok := r.a.slots[build]
		return ok && r.a.trayShows()
	})
	r.hold(1500 * time.Millisecond)
	r.record()
	home := r.a.slots[build]
	r.liftTo(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, r.ballOf(jie), func() bool { return r.a.aim.ok && r.a.aim.who == string(jie) })
	clicked := time.Now()
	r.logf("let go over 阿杰's ball")
	r.until("the share, from the server", func() bool {
		m := r.a.base.Devices[model.DeviceID(build)]
		return m != nil && m.ShareTo(jie) != nil && m.ShareTo(jie).ID != ""
	})
	r.logf("the server has it, %s after letting go", time.Since(clicked).Round(time.Millisecond))
	r.hold(1200 * time.Millisecond)
	r.still("分享了")
	// The toast going, recorded; the checks over ssh after it leave the
	// window as it is, unrecorded.
	r.record()
	r.hold(4500 * time.Millisecond)
	r.rec = false
	sh := r.a.base.Devices[model.DeviceID(build)].ShareTo(jie)
	var open []string
	for _, s := range sh.Services {
		if s.On {
			open = append(open, s.Key)
		}
	}
	r.logf("given to 阿杰, share %s, opening %v", sh.ID, open)

	// 阿杰's side.
	st := r.shellOut(onPC("status"))
	if !strings.Contains(st, "构建服务器") {
		r.t.Errorf("share: 阿杰's desktop does not list 构建服务器:\n%s", st)
	}
	if strings.Contains(st, "liang的MacBook Air") {
		r.t.Errorf("share: 阿杰's desktop lists liang的MacBook Air, only in 工作室:\n%s", st)
	}
	page := r.shellOutAny(onPC("debug http " + buildAddr + " 80"))
	if !strings.Contains(page, `"ok":true`) || !strings.Contains(page, "share/") || strings.Contains(page, "lan/") {
		r.t.Errorf("share: 阿杰's desktop on 构建服务器's web page: %s", page)
	}
	if got := r.shellOutAny(onPC("debug probe " + buildAddr + " tcp 7000 250ms 1")); !mostlyOK(got) {
		r.t.Errorf("share: 阿杰's desktop on 构建服务器's 计时流: %s", got)
	}
	opened := r.shellOutAny(onPC("open " + buildAddr + " 80"))
	r.logf("阿杰's desktop opens 构建服务器's web page at %s", opened)
	// 构建服务器 reaches none of 阿杰's devices.
	for _, addr := range []string{pcAddr, nasAddr} {
		if got := r.shellOutAny(onBuild("debug probe " + addr + " tcp 7000 250ms 1")); !strings.HasPrefix(lastLine(got), "0 ok") {
			r.t.Errorf("share: 构建服务器 reaches %s:7000: %s", addr, got)
		}
		r.logf("构建服务器 pings %s: %s", addr, lastLine(r.shellOutAny(onBuild("ping "+addr+" -c 2"))))
	}

	// What it opens to him, switched in his card: the web page off, then
	// on again; the 计时流 all along.
	r.record()
	r.hold(300 * time.Millisecond)
	r.tt.ClickAt(r.ballOf(jie)().X, r.ballOf(jie)().Y)
	r.tick()
	r.until("阿杰's card", func() bool { return r.tt.HasText("你分享给阿杰的") })
	r.hold(600 * time.Millisecond)
	r.click("分享了构建服务器")
	r.until("what it opens to him", func() bool { return r.tt.HasText("开放给阿杰的") })
	r.hold(600 * time.Millisecond)
	r.still("开放给阿杰的")
	r.record()
	web := func() bool {
		sh := r.a.base.Devices[model.DeviceID(build)].ShareTo(jie)
		for _, s := range sh.Services {
			if s.Key == "tcp/80" {
				return s.On
			}
		}
		return false
	}
	switchWeb := func() {
		b, ok := r.tt.Find("网站")
		if !ok {
			r.t.Fatalf("share: no 网站 in 阿杰's card: %q", r.tt.Texts())
		}
		x := r.a.origin.X + r.a.cardAt.X + r.a.cardAt.W - 40
		r.tt.ClickAt(x, b.Y+b.H/2)
		r.tick()
	}
	switchWeb()
	r.until("the web page off for him", func() bool { return !web() })
	r.hold(time.Second)
	r.still("关掉网站")
	if page := r.shellOutAny(onPC("debug http " + buildAddr + " 80")); strings.Contains(page, `"ok":true`) {
		r.t.Errorf("share: with the web page off for him, 阿杰's desktop still gets it: %s", page)
	}
	if got := r.shellOutAny(onPC("debug probe " + buildAddr + " tcp 7000 250ms 1")); !mostlyOK(got) {
		r.t.Errorf("share: with the web page off, 阿杰's desktop on the 计时流: %s", got)
	}
	r.record()
	switchWeb()
	r.until("the web page on again", web)
	r.hold(800 * time.Millisecond)
	r.click("关闭")
	r.hold(800 * time.Millisecond)

	// Taken back from his card: how soon his desktop loses it.
	offset := r.pcOffset()
	wait := background(onPC("debug probe " + buildAddr + " tcp 7000 100ms 9"))
	r.hold(3 * time.Second)
	if !r.tt.HasText("你分享给阿杰的") {
		r.tt.ClickAt(r.ballOf(jie)().X, r.ballOf(jie)().Y)
		r.tick()
	}
	r.until("阿杰's card", func() bool { return r.tt.HasText("你分享给阿杰的") })
	r.hold(700 * time.Millisecond)
	r.still("阿杰的卡片")
	r.record()
	chip, ok := r.tt.Find("分享了构建服务器")
	if !ok {
		r.t.Fatalf("share: no chip of 构建服务器 in 阿杰's card: %q", r.tt.Texts())
	}
	r.tt.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	r.hold(400 * time.Millisecond)
	revoked := time.Now()
	r.click("收回")
	r.logf("taken back")
	r.hold(900 * time.Millisecond)
	r.still("收回")
	r.record()
	lines := wait()
	for _, l := range lines {
		r.logf("probe: %s", l)
	}
	at, okk := probeTimes(lines, offset)
	lastOK, firstFail := time.Time{}, time.Time{}
	for i := range at {
		if okk[i] {
			lastOK = at[i]
		} else if firstFail.IsZero() && at[i].After(revoked) {
			firstFail = at[i]
		}
	}
	r.logf("阿杰's desktop: last answer %s after the click, first one not answered %s after", lastOK.Sub(revoked).Round(time.Millisecond), firstFail.Sub(revoked).Round(time.Millisecond))
	if lastOK.Sub(revoked) > 2*time.Second {
		r.t.Errorf("share: 阿杰's desktop still reached 构建服务器 %s after it was taken back", lastOK.Sub(revoked))
	}
	// Undo, within the toast: as it was.
	r.click("撤销")
	r.until("the share back", func() bool {
		m := r.a.base.Devices[model.DeviceID(build)]
		return m != nil && m.ShareTo(jie) != nil
	})
	r.hold(1500 * time.Millisecond)
	r.still("撤销")
	r.rec = false
	// His engine gets the share again with the next network map and
	// connects anew: a few tries, each two seconds long.
	back := false
	for try := 0; try < 3 && !back; try++ {
		back = mostlyOK(r.shellOutAny(onPC("debug probe " + buildAddr + " tcp 7000 250ms 2")))
	}
	if !back {
		r.t.Errorf("share: after undo, 阿杰's desktop does not get 构建服务器 back")
	}
	// Taken back again; the toast goes, and with it 阿杰's seat of mine.
	// (His card is still open: another click on his ball would close it.)
	r.record()
	if !r.tt.HasText("你分享给阿杰的") {
		r.tt.ClickAt(r.ballOf(jie)().X, r.ballOf(jie)().Y)
		r.tick()
	}
	r.until("阿杰's card", func() bool { return r.tt.HasText("你分享给阿杰的") })
	r.until("构建服务器's chip back", func() bool { _, ok := r.tt.Find("分享了构建服务器"); return ok })
	r.hold(400 * time.Millisecond)
	chip, _ = r.tt.Find("分享了构建服务器")
	r.tt.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	r.hold(400 * time.Millisecond)
	r.click("收回")
	held := r.a.base.Seats.Holds(jie)
	r.until("the toast gone and the seat free", func() bool { return r.a.toast == nil && !r.a.base.Seats.Holds(jie) })
	r.logf("阿杰's seat of mine: kept while the toast showed (%v), free once it went", held)
	r.still("提示条走了")
}

// shellOutAny is shellOut for a command that may fail: what it said either
// way.
func (r *realRig) shellOutAny(command string) string {
	r.logf("running %s", command)
	out, _ := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	s := strings.TrimSpace(strings.ReplaceAll(string(out), "\r", ""))
	r.logf("it said: %s", s)
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func m3shared(r *realRig) {
	me := r.a.snap.Me
	nas := r.named("阿杰的 NAS")
	if nas == "" {
		r.t.Fatal("shared: no 阿杰的 NAS")
	}
	r.lan("周末联机")
	r.hold(time.Second)
	r.record()
	out := r.shellOut(onNAS(opArgs("share.create", map[string]any{"device": nas, "person": me, "services": []string{"tcp/7000"}})))
	r.logf("阿杰 gives me his NAS: %s", out)
	r.until("it given to me", func() bool { return r.a.snap.SharedOf(model.DeviceID(nas)) != nil })
	r.hold(1500 * time.Millisecond)
	r.still("侧边栏数字")
	r.record()
	r.click("共享给我")
	r.until("the canvas of those given to me", func() bool { return r.a.shared() && r.a.device(nas) != nil })
	r.until("it connected", func() bool { return linked(r.a.device(nas).path) })
	r.hold(1500 * time.Millisecond)
	if got := r.a.status(r.a.device(nas)); got != "阿杰 · 直接连上" && !strings.HasPrefix(got, "阿杰 · 经") {
		r.t.Errorf("shared: 阿杰的 NAS says %q", got)
	}
	r.still("共享给我")
	r.record()
	d := r.a.device(nas)
	r.tt.ClickAt(r.at(d).X, r.at(d).Y)
	r.tick()
	r.until("its card", func() bool { return r.tt.HasText("阿杰分享给你的") })
	r.hold(800 * time.Millisecond)
	for _, want := range []string{"只有你能连它，反过来不行", "不要了", "你可以用"} {
		if !r.tt.HasText(want) {
			r.t.Errorf("shared: its card lacks %q: %q", want, r.tt.Texts())
		}
	}
	r.still("卡片")
	r.record()
	r.click("不要了")
	r.hold(800 * time.Millisecond)
	r.still("不要了")
	r.record()
	r.until("given back once the toast went", func() bool { return r.a.base.SharedOf(model.DeviceID(nas)) == nil })
	r.logf("given back")
	r.hold(time.Second)
	r.still("还回去了")
}

// mostlyOK reports whether a probe's summary ("4 ok, 1 failed") says the
// service answered more often than not: the home line drops a SYN now and
// then (about one attempt in eight on 11-08), which says nothing of the
// share.
func mostlyOK(out string) bool {
	var ok, failed int
	if _, err := fmt.Sscanf(lastLine(out), "%d ok, %d failed", &ok, &failed); err != nil {
		return false
	}
	return ok > 0 && ok > failed
}

// onTestMac is the command that runs the engine's command line of who's
// temporary engine on the test Mac (liang的MacBook Air): mia or leo, for
// the seats.
func onTestMac(who, args string) string {
	return `ssh -o BatchMode=yes -o ConnectTimeout=15 liang@192.168.1.18 "cd ~/deneb && DENEB_SOCKET=~/deneb-` + who + `/denebd.sock ./denebd ` + strings.ReplaceAll(args, `"`, `\"`) + `"`
}

// m3seats is item 3, with my seats held to three for the test (the
// control line sets it on the server; mia's and leo's devices are
// temporary engines on the test Mac). 构建服务器 given to 阿杰 takes my
// second seat; mia, joining 工作室 by my link, the third: "邀请朋友" goes
// dashed, and its click shows the seats. leo, brought into 周末联机 by
// 阿杰, has a ball here but no seat of mine: a device held up to him does
// not go, nor does his ball into 工作室. 构建服务器 taken back from 阿杰
// keeps his seat while the toast shows, and frees it once it goes; then
// one can go to leo. mia stays connected all along.
func m3seats(r *realRig) {
	build := r.named("构建服务器")
	jie := r.person("阿杰")
	if s := r.a.snap.Seats; !s.Enforced || s.Limit != 3 {
		r.t.Fatalf("seats: my seats are %+v; the control line sets the limit to 3 first", s)
	}
	r.shell("denebd lan remove 周末联机 build")
	r.t.Cleanup(func() { exec.Command("/bin/sh", "-c", "denebd lan add 周末联机 build").Run() })
	r.lan("工作室")
	r.hold(1500 * time.Millisecond)
	r.record()
	r.until("还能加 2 人", func() bool { return r.tt.HasText("还能加 2 人") })
	r.hold(800 * time.Millisecond)
	r.still("还能加2人")
	r.record()

	// 构建服务器 to 阿杰, from 周末联机's tray: my second seat.
	r.lan("周末联机")
	r.until("构建服务器 in the tray", func() bool { _, ok := r.a.slots[build]; return ok && r.a.trayShows() })
	r.hold(1200 * time.Millisecond)
	home := r.a.slots[build]
	r.liftTo(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, r.ballOf(jie), func() bool { return r.a.aim.ok && r.a.aim.who == string(jie) })
	r.until("还能加 1 人", func() bool { return r.a.base.Seats.Left == 1 && r.tt.HasText("还能加 1 人") })
	r.hold(4500 * time.Millisecond)
	r.still("还能加1人")
	r.rec = false

	// mia joins 工作室 by my link: the third seat.
	r.lan("工作室")
	var link string
	r.a.copyText = func(s string) { link = s }
	r.record()
	r.click("邀请朋友")
	r.until("the invite link", func() bool { return link != "" })
	r.logf("工作室's link: %s", link)
	r.shellOut(onTestMac("mia", "lan join "+link))
	r.until("my seats full", func() bool { return r.a.base.Seats.Left == 0 && r.a.seatsFull() })
	mia := r.someone("Mia", "mia")
	r.hold(1500 * time.Millisecond)
	r.still("满了")
	r.record()
	r.click("邀请朋友")
	r.until("the seats' card", func() bool { return r.tt.HasText("3 个位置都用上了") })
	r.hold(900 * time.Millisecond)
	r.still("位置卡片")
	r.record()
	r.click("关闭")
	r.hold(800 * time.Millisecond)
	r.rec = false

	// mia stays connected to 构建服务器 in 工作室 all along.
	miaProbe := background(onTestMac("mia", "debug probe "+buildAddr+" tcp 7000 500ms 75"))

	// leo, brought into 周末联机 by 阿杰: a ball here, no seat of mine.
	nasLink := r.shellOut(onNAS("lan invite 周末联机"))
	nasLink = nasLink[strings.LastIndex(nasLink, "https://"):]
	r.shellOut(onTestMac("leo", "lan join "+strings.Fields(nasLink)[0]))
	r.lan("周末联机")
	leo := model.PersonID("")
	r.until("leo in 周末联机", func() bool {
		leo = r.someoneOr("Leo", "leo")
		l := r.a.snap.LAN(r.a.lan)
		return leo != "" && l != nil && slices.Contains(l.Members, leo)
	})
	r.hold(1500 * time.Millisecond)
	r.record()
	v := r.a.base.Version
	home = r.a.slots[build]
	letGo := r.liftHold(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, r.ballOf(leo), func() bool { return r.a.aim.who == string(leo) && r.tt.HasText("位置满了") })
	if !r.tt.HasText("位置满了") || r.a.aim.ok {
		r.t.Errorf("seats: held up to leo, the line says %q (ok %v)", r.a.aimLine(), r.a.aim.ok)
	}
	r.still("拖给leo放不进去")
	r.record()
	letGo()
	r.hold(1200 * time.Millisecond)
	if r.a.base.Version != v {
		r.t.Errorf("seats: let go over leo, something was sent")
	}
	// His ball onto 工作室's row in the sidebar: no.
	ball := r.ballOf(leo)()
	row := r.a.rows[r.lan2("工作室")]
	r.tt.Move(ball.X, ball.Y)
	r.tick()
	r.tt.Press(ball.X, ball.Y)
	r.hold(100 * time.Millisecond)
	for i := 1; i <= 40; i++ {
		k := ui.EaseInOut(float32(i) / 40)
		r.tt.Move(ball.X+(row.X+row.W*0.6-ball.X)*k, ball.Y+(row.Y+row.H/2-ball.Y)*k)
		r.tick()
	}
	r.hold(800 * time.Millisecond)
	if r.a.personFull == "" || !r.tt.HasText("3 个位置都用上了") {
		r.t.Errorf("seats: leo's ball over 工作室's row: full %q; %q", r.a.personFull, r.tt.Texts())
	}
	r.still("拖人也进不去")
	r.record()
	r.tt.Release(row.X+row.W*0.6, row.Y+row.H/2)
	r.hold(1200 * time.Millisecond)
	if r.a.base.Version != v {
		r.t.Errorf("seats: leo's ball let go over 工作室, something was sent")
	}

	// 构建服务器 taken back from 阿杰: his seat stays while the toast shows.
	r.tt.ClickAt(r.ballOf(jie)().X, r.ballOf(jie)().Y)
	r.tick()
	r.until("阿杰's card", func() bool { return r.tt.HasText("你分享给阿杰的") })
	r.hold(600 * time.Millisecond)
	chip, _ := r.tt.Find("分享了构建服务器")
	r.tt.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	r.hold(400 * time.Millisecond)
	r.click("收回")
	r.hold(600 * time.Millisecond)
	still := r.a.base.Seats.Holds(jie) && r.a.base.Seats.Left == 0
	tried := r.shellOutAny("denebd " + opArgs("share.create", map[string]any{"device": build, "person": string(leo)}))
	r.logf("while the toast shows: 阿杰's seat kept %v; giving leo a device: %s", still, tried)
	if !still || !strings.Contains(tried, "seats_full") && !strings.Contains(tried, "位置都用上了") {
		r.t.Errorf("seats: while the toast shows, 阿杰's seat kept %v, leo given a device: %s", still, tried)
	}
	r.still("收回时位置先留着")
	r.record()
	r.until("the toast gone, a seat free", func() bool { return r.a.toast == nil && r.a.base.Seats.Left == 1 })
	r.hold(1200 * time.Millisecond)
	r.still("提示条走了位置放出")
	r.record()
	// Now one can go to leo.
	if r.tt.HasText("你分享给阿杰的") {
		r.click("关闭")
		r.hold(600 * time.Millisecond)
	}
	home = r.a.slots[build]
	r.liftTo(ui.Point{X: r.a.origin.X + home.X, Y: r.a.origin.Y + home.Y}, r.ballOf(leo), func() bool { return r.a.aim.ok && r.a.aim.who == string(leo) })
	r.until("given to leo", func() bool {
		m := r.a.base.Devices[model.DeviceID(build)]
		return m != nil && m.ShareTo(leo) != nil
	})
	r.hold(1500 * time.Millisecond)
	r.still("之后能分给leo")

	// Single tries go unanswered on the home line now and then (a lost
	// SYN, a half-second wait); what would say she lost it is a run of
	// them: more than four in a row is two seconds without 构建服务器.
	lines := miaProbe()
	okN, failN, run, longest := 0, 0, 0, 0
	for _, l := range lines {
		f := strings.Fields(l)
		switch {
		case len(f) > 1 && f[1] == "ok":
			okN++
			run = 0
		case len(f) > 1 && f[1] == "FAIL":
			failN++
			run++
			longest = max(longest, run)
			r.logf("mia's probe: %s", l)
		}
	}
	r.logf("mia on 构建服务器 all along: %d answered, %d not, at most %d in a row", okN, failN, longest)
	if okN == 0 || longest > 4 {
		r.t.Errorf("seats: mia lost 构建服务器: %d answered, %d not, %d in a row", okN, failN, longest)
	}

	// As it was: leo's share taken back and let go, mia out of 工作室, leo
	// out of 周末联机, 构建服务器 back in it (the clean-up above).
	if sh := r.a.base.Devices[model.DeviceID(build)].ShareTo(leo); sh != nil {
		r.shellOutAny("denebd " + opArgs("share.revoke", map[string]any{"share": sh.ID}))
		r.shellOutAny("denebd " + opArgs("share.release", map[string]any{"share": sh.ID}))
	}
	r.shellOutAny("denebd " + opArgs("member.remove", map[string]any{"lan": string(r.lan2("工作室")), "person": string(mia)}))
	r.shellOutAny(onNAS(opArgs("member.remove", map[string]any{"lan": string(r.lan2("周末联机")), "person": string(leo)})))
}

// lan2 is the id of the LAN named name, without going to its canvas.
func (r *realRig) lan2(name string) model.LANID {
	for _, l := range r.a.snap.LANs {
		if l.Name == name {
			return l.ID
		}
	}
	r.t.Fatalf("step %s: no LAN %q", r.step, name)
	return ""
}

// someone is the id of the person named one of names, as the snapshot has
// them; the test stops without them.
func (r *realRig) someone(names ...string) model.PersonID {
	r.t.Helper()
	if id := r.someoneOr(names...); id != "" {
		return id
	}
	r.t.Fatalf("step %s: no one named %v", r.step, names)
	return ""
}

// someoneOr is someone, or "" if no one is named so.
func (r *realRig) someoneOr(names ...string) model.PersonID {
	for id, p := range r.a.snap.People {
		if slices.Contains(names, p.Name) {
			return id
		}
	}
	return ""
}
