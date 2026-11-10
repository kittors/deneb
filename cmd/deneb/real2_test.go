package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
)

// TestRealAcceptance2 plays the window's part in M2's acceptance
// (design/plan/m2-acceptance.md) on this computer's real engine, as
// TestRealAcceptance does M1's, when DENEB_REAL2 lists the items to play,
// in order, as "3,12,11,2". Frames and log go as M1's (DENEB_FRAMES,
// DENEB_DARK). 阿杰 acts on his NAS (hk-spare) and his desktop (the Windows
// test machine) by the engine's command line over ssh, as he would in his
// own window; this computer's setup and clean-up go by its command line,
// what the window does by hand.
//
//   - 3, with 6: 验收临时, a LAN of mine, is made here. In 周末联机's card,
//     阿杰's ball dragged onto its row in the sidebar invites him (6). He
//     accepts on his NAS, then adds his desktop: on 验收临时's canvas his
//     ball comes in, then his devices, one after the other (3).
//   - 12: the menu bar's panel, drawn as the recordings draw it: its switch
//     pauses 验收临时 here and resumes it; with "新设备要同意" on there, 阿杰
//     adds his desktop again, and the panel's 同意 lets it in; 续上 renews
//     a login of mine if the server says one is due; 打开 Deneb brings the
//     window. 验收临时 is deleted at the end.
//   - 11: 阿杰 turns "新设备要同意" on in his 周末联机. 构建服务器, taken out
//     of it first, dragged in from the tray waits for him, dashed; he lets
//     it in and it connects. Dragged in again, he turns it away and it
//     goes. It is put back, and the switch turned off, as they were.
//   - 2, with 1: the engine handles a network change (denebd debug
//     netchange). On 周末联机's canvas each link may go over the relay and
//     back direct: every change at its moment in the log, the engine's own
//     reports beside it, and no frame shows 断开, 正在连接 or 连不上.
func TestRealAcceptance2(t *testing.T) {
	items := os.Getenv("DENEB_REAL2")
	if items == "" {
		t.Skip("set DENEB_REAL2 to the M2 items to play on this computer's engine, as 3,12,11,2")
	}
	r := newRealRig(t)
	r.until("the engine, logged in", func() bool { return r.a.snap.Engine && r.a.snap.LoggedIn && r.a.snap.Loaded })
	for _, s := range strings.Split(items, ",") {
		fn := map[string]func(*realRig){"2": m2netchange, "3": m2join, "4": m2cut, "5t": m2using, "11": m2approve, "12": m2panel, "12r": m2renew, "watch": m2watch}[strings.TrimSpace(s)]
		if fn == nil {
			t.Fatalf("no item %q", s)
		}
		r.step = strings.TrimSpace(s)
		r.logf("item %s begins", r.step)
		fn(r)
		// What comes after, the next item's setup or the clean-up, is
		// none of this item's frames.
		r.rec = false
		r.logf("item %s ends", r.step)
	}
}

// The temporary LAN items 3 and 12 play in.
const tryLAN = "验收临时"

// onNAS is the command that runs the engine's command line on 阿杰的 NAS:
// args may hold JSON, quoted for the shell there.
func onNAS(args string) string {
	return `ssh -o BatchMode=yes -o ConnectTimeout=15 hk-spare "docker exec deneb-node /denebd ` + strings.ReplaceAll(args, `"`, `\"`) + `"`
}

// onPC is the command that runs it on 阿杰的台式机, whose shell is cmd.exe:
// args are plain words.
func onPC(args string) string {
	return `ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85 "%USERPROFILE%\deneb\denebd.exe ` + args + `"`
}

// onPCRaw is the command that runs a command on 阿杰的台式机 as it is.
func onPCRaw(command string) string {
	return `ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85 "` + command + `"`
}

// shellOut runs command as shell does, and returns what it said.
func (r *realRig) shellOut(command string) string {
	r.t.Helper()
	r.logf("running %s", command)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out []byte
	done := make(chan error, 1)
	go func() {
		var err error
		out, err = exec.CommandContext(ctx, "/bin/sh", "-c", command).Output()
		done <- err
	}()
	for {
		select {
		case err := <-done:
			if err != nil {
				r.t.Fatalf("item %s: %s: %v\n%s", r.step, command, err, out)
			}
			r.logf("it said: %s", strings.TrimSpace(string(out)))
			return strings.TrimSpace(strings.ReplaceAll(string(out), "\r", ""))
		default:
			r.tick()
		}
	}
}

// opArgs is `op <kind> '<args as JSON>'`, for the command line.
func opArgs(kind string, args map[string]any) string {
	b, _ := json.Marshal(args)
	return "op " + kind + " '" + string(b) + "'"
}

// dropping deletes 验收临时 once the test is over, once, unless
// DENEB_REAL2_KEEP keeps it for a later run of item 12 alone.
var dropping sync.Once

func (r *realRig) dropAtEnd(tmp model.LANID) {
	dropping.Do(func() {
		r.t.Cleanup(func() {
			if os.Getenv("DENEB_REAL2_KEEP") == "" && r.lanID(tryLAN) != "" {
				r.shell("denebd lan delete " + string(tmp))
			}
		})
	})
}

// person is the id of the person named name, as the snapshot has them.
func (r *realRig) person(name string) model.PersonID {
	r.t.Helper()
	for id, p := range r.a.snap.People {
		if p.Name == name {
			return id
		}
	}
	r.t.Fatalf("item %s: no one named %s", r.step, name)
	return ""
}

// lanID is the id of my LAN named name, or "".
func (r *realRig) lanID(name string) model.LANID {
	for _, l := range r.a.store.Snapshot().LANs {
		if l.Name == name {
			return l.ID
		}
	}
	return ""
}

// storeLAN is the LAN of id as the store has it now, never nil.
func (r *realRig) storeLAN(id model.LANID) *model.LAN {
	if l := r.a.store.Snapshot().LAN(id); l != nil {
		return l
	}
	return &model.LAN{}
}

// makeLAN makes a LAN named name from the sidebar, as a person would, and
// shows it with this computer in it.
func (r *realRig) makeLAN(name string) model.LANID {
	r.t.Helper()
	r.click("新建局域网")
	r.hold(300 * time.Millisecond)
	r.tt.Type(name)
	r.tick()
	r.logf("making %s", name)
	r.tt.Key(0, ui.KeyEnter)
	r.until(name+" on the canvas", func() bool { l := r.a.snap.LAN(r.a.lan); return l != nil && l.Name == name })
	r.until("this computer in "+name, func() bool { return r.a.device(r.a.self) != nil })
	return r.a.lan
}

// openLANCard opens the card of the LAN on the canvas, by its name in the
// header.
func (r *realRig) openLANCard() {
	r.t.Helper()
	if r.a.selected != lanCard {
		r.tt.ClickAt(sidebarWidth+28+24, 22+12)
	}
	r.until("the LAN's card", func() bool { return r.a.selected == lanCard })
	r.hold(600 * time.Millisecond)
}

// dropNear presses at from and lets go at to, a hand's way, pausing over to
// as a hand would before it lets go.
func (r *realRig) dropNear(from, to ui.Point) {
	r.tt.Press(from.X, from.Y)
	for i := 1; i <= 24; i++ {
		k := ui.EaseInOut(float32(i) / 24)
		r.tt.Move(from.X+(to.X-from.X)*k, from.Y+(to.Y-from.Y)*k)
		r.tick()
	}
	r.hold(300 * time.Millisecond)
	r.tt.Release(to.X, to.Y)
	r.tick()
}

// Item 6, then 3: 阿杰 invited into 验收临时 by his ball, and coming in.
func m2join(r *realRig) {
	jie := r.person("阿杰")
	nas, pc := r.named("阿杰的 NAS"), r.named("阿杰的台式机")
	if nas == "" || pc == "" {
		r.t.Fatal("item 3: no 阿杰的 NAS or no 阿杰的台式机")
	}
	if id := r.lanID(tryLAN); id != "" {
		r.shell("denebd lan delete " + string(id))
		r.until(tryLAN+" gone", func() bool { return r.lanID(tryLAN) == "" })
	}
	r.record()
	tmp := r.makeLAN(tryLAN)
	r.logf("made %s (%s)", tryLAN, tmp)
	r.dropAtEnd(tmp)
	// 6: 阿杰's ball, from 周末联机's card onto 验收临时's row.
	r.lan("周末联机")
	r.openLANCard()
	r.until("阿杰's ball in the card", func() bool { _, ok := r.a.ballHome[string(jie)]; return ok })
	row, ok := r.a.rows[tmp]
	if !ok {
		r.t.Fatalf("item 6: no row for %s in the sidebar", tryLAN)
	}
	from := r.a.ballHome[string(jie)]
	r.logf("dragging 阿杰's ball onto %s", tryLAN)
	r.dropNear(from, ui.Point{X: row.X + row.W*0.4, Y: row.Y + row.H/2})
	invite := ""
	r.until("阿杰 invited", func() bool {
		for _, in := range r.storeLAN(tmp).Invited {
			if in.Person == jie {
				invite = in.Invite
			}
		}
		return invite != ""
	})
	r.logf("阿杰 invited into %s (invite %s); the window says %q", tryLAN, invite, r.tt.Texts())
	if !r.tt.HasText("已邀请阿杰加入「" + tryLAN + "」") {
		r.t.Errorf("item 6: no toast for the invite: %q", r.tt.Texts())
	}
	r.hold(1500 * time.Millisecond)
	r.still("invited")

	// 3: his ball, then his NAS, then his desktop.
	r.lan(tryLAN)
	r.record()
	r.hold(time.Second)
	var ballAt, nasAt, pcAt time.Time
	r.watch = func() {
		l := r.a.snap.LAN(tmp)
		if ballAt.IsZero() && l != nil && slices.Contains(l.Members, jie) {
			ballAt = time.Now()
			r.logf("阿杰 is a member")
		}
		if d := r.a.device(nas); nasAt.IsZero() && d != nil {
			nasAt = time.Now()
			r.logf("阿杰的 NAS on the canvas, %s", d.path)
		}
		if d := r.a.device(pc); pcAt.IsZero() && d != nil {
			pcAt = time.Now()
			r.logf("阿杰的台式机 on the canvas, %s", d.path)
		}
	}
	defer func() { r.watch = nil }()
	accepted := r.shell(onNAS(opArgs("invite.accept", map[string]any{"invitation": invite})))
	r.until("阿杰 and his NAS in", func() bool { return !ballAt.IsZero() && !nasAt.IsZero() })
	r.until("阿杰的 NAS connected", func() bool { return linkedPath(r.a.device(nas)) })
	r.logf("阿杰's ball %s and his NAS %s after he accepted; the NAS %s", since(ballAt, accepted), since(nasAt, accepted), r.a.device(nas).path)
	r.hold(2 * time.Second)
	// A device a member brings into a LAN of mine waits for me (an M2
	// LAN's "新设备要同意" is on from the start): it comes in dashed, and
	// one click on it lets it in.
	added := r.shell(onPC("lan add " + string(tmp)))
	r.until("阿杰的台式机 waiting for me", func() bool { d := r.a.device(pc); return d != nil && d.pending && !d.mine })
	r.logf("阿杰的台式机 on the canvas %s after he added it, saying %q", since(pcAt, added), r.a.status(r.a.device(pc)))
	r.hold(2 * time.Second)
	r.still("waits-for-me")
	r.record()
	r.logf("letting 阿杰的台式机 in")
	at := r.at(r.a.device(pc))
	r.tt.ClickAt(at.X, at.Y)
	r.tick()
	clicked := time.Now()
	r.until("阿杰的台式机 let in", func() bool {
		l := r.storeLAN(tmp)
		return l.Has(model.DeviceID(pc)) && !l.HasPending(model.DeviceID(pc))
	})
	r.until("阿杰的台式机 connected", func() bool { d := r.a.device(pc); return d != nil && !d.pending && linkedPath(d) })
	r.logf("阿杰的台式机 in %s after the click (the toast's undo time first): %s", since(time.Now(), clicked), r.a.device(pc).path)
	r.hold(2500 * time.Millisecond)
	r.still("joined")
	if _, line := r.a.summary(&lightPalette); !strings.Contains(line, "都连上了") {
		r.t.Errorf("item 3: %s says %q", tryLAN, line)
	}
}

// Item 12: the menu bar's panel, on the real world.
func m2panel(r *realRig) {
	tmp := r.lanID(tryLAN)
	if tmp == "" {
		r.t.Fatal("item 12: play item 3 first: no " + tryLAN)
	}
	pc := r.named("阿杰的台式机")
	if !r.storeLAN(tmp).Has(model.DeviceID(pc)) {
		r.t.Fatal("item 12: 阿杰的台式机 is not in " + tryLAN)
	}
	r.dropAtEnd(tmp)
	// The panel and the menu bar above it, as the recordings draw them: a
	// tester of its own, whose frames are the item's.
	var shown atomic.Int32
	r.a.showMain = func() { shown.Add(1) }
	r.a.panelShown = true
	window := r.tt
	m := &menubarScreen{a: r.a, open: true}
	r.tt = ui.NewTester(m.view, screenW, screenH)
	r.tt.SetDark(os.Getenv("DENEB_DARK") != "")
	r.tt.Frame()
	defer func() { r.tt, r.a.panelShown, r.a.showMain, r.rec = window, false, nil, false }()
	mark := ""
	r.watch = func() {
		if st := trayState(r.a.snap, time.Now()); st != mark {
			mark = st
			r.logf("the mark is %s", st)
		}
	}
	defer func() { r.watch = nil }()
	r.record()
	r.hold(1500 * time.Millisecond)
	r.still("open")

	// The switch: 验收临时 paused on this computer, and back.
	r.record()
	r.logf("pausing %s", tryLAN)
	r.click("暂停" + tryLAN)
	r.until(tryLAN+" paused", func() bool { return r.storeLAN(tmp).Paused && r.tt.HasText("已暂停") })
	// 阿杰's devices stay connected here: they share 周末联机 with it.
	r.logf("%s paused; the panel says %q; the mark is %s", tryLAN, r.a.lanLine(r.a.snap.LAN(tmp)), trayState(r.a.snap, time.Now()))
	r.hold(3 * time.Second)
	r.still("paused")
	r.record()
	r.logf("resuming %s", tryLAN)
	r.click("暂停" + tryLAN)
	r.until(tryLAN+" back", func() bool { return !r.storeLAN(tmp).Paused && !r.tt.HasText("已暂停") })
	r.logf("%s back; the panel says %q", tryLAN, r.a.lanLine(r.a.snap.LAN(tmp)))
	r.hold(2 * time.Second)

	// 同意: "新设备要同意" on in 验收临时; 阿杰 takes his desktop out and adds
	// it again, and it waits for me.
	r.shell("denebd " + opArgs("lan.setApproveDevices", map[string]any{"lan": tmp, "on": true}))
	r.until("新设备要同意 on", func() bool { return r.storeLAN(tmp).ApproveDevices })
	r.shell(onPC("lan remove " + string(tmp)))
	r.until("阿杰的台式机 out", func() bool { return !r.storeLAN(tmp).Has(model.DeviceID(pc)) })
	r.hold(time.Second)
	asked := r.shell(onPC("lan add " + string(tmp)))
	chore := say("阿杰的台式机", "想加入", tryLAN)
	r.until("the panel's "+chore, func() bool { return r.tt.HasText(chore) })
	r.logf("the panel says %s %s after he asked", chore, since(time.Now(), asked))
	r.hold(1500 * time.Millisecond)
	r.still("asks")
	r.record()
	r.logf("同意")
	r.click("同意")
	r.until("已同意", func() bool { return r.tt.HasText(say("已同意", "阿杰的台式机")) })
	r.until("阿杰的台式机 let in", func() bool {
		l := r.storeLAN(tmp)
		return l.Has(model.DeviceID(pc)) && !l.HasPending(model.DeviceID(pc))
	})
	r.until("阿杰的台式机 connected", func() bool { return r.a.snap.Path(model.DeviceID(pc)).Kind.Connected() })
	r.logf("阿杰的台式机 in, %s", r.a.snap.Path(model.DeviceID(pc)).Kind)
	r.hold(2 * time.Second)
	r.still("let-in")

	// 续上, if the server says a login of mine is due.
	if r.tt.HasText("续上") {
		r.record()
		r.logf("续上")
		r.click("续上")
		r.until("renewed", func() bool { return !r.tt.HasText("续上") && !r.tt.HasText("已续上") })
		r.logf("renewed")
		r.hold(time.Second)
		r.still("renewed")
	} else {
		r.logf("no login of mine is due: 续上 not played")
	}

	r.record()
	r.click("打开 Deneb")
	r.hold(500 * time.Millisecond)
	if shown.Load() != 1 {
		r.t.Errorf("item 12: 打开 Deneb brought the window %d times", shown.Load())
	}
	r.logf("打开 Deneb brought the window")
}

// Item 4: 阿杰's desktop holds a connection to this computer's web page
// (deneb open on it, and a PowerShell client through the port it gives,
// asking every second); this computer closes that service to 周末联机 from
// its card. A short red light runs on the desktop's link, and its second
// line says the service was closed (scene 5); then the service is opened
// again, as it was.
func m2cut(r *realRig) {
	r.lan("周末联机")
	pc := r.named("阿杰的台式机")
	if r.a.device(pc) == nil {
		r.t.Fatal("item 4: 阿杰的台式机 is not in 周末联机")
	}
	web := ""
	for _, v := range r.a.store.Snapshot().LAN(r.a.lan).Services[model.DeviceID(r.a.self)] {
		if v.Proto == "tcp" && v.Port == 80 && v.On {
			web = v.Name
		}
	}
	if web == "" {
		r.t.Fatal("item 4: this computer's port 80 is not open to 周末联机")
	}
	opened := r.shellOut(onPC("open " + r.a.snap.Devices[model.DeviceID(r.a.self)].FQDN + " 80"))
	_, port, ok := strings.Cut(strings.TrimSpace(opened), ":")
	if !ok {
		r.t.Fatalf("item 4: deneb open said %q", opened)
	}
	ps := "$c=New-Object Net.Sockets.TcpClient('127.0.0.1'," + port + ");$s=$c.GetStream();" +
		"for($i=0;$i -lt 60;$i++){$b=[Text.Encoding]::ASCII.GetBytes(\"HEAD / HTTP/1.1`r`nHost: x`r`n`r`n\");$s.Write($b,0,$b.Length);" +
		"while($s.DataAvailable){[void]$s.ReadByte()};Start-Sleep 1};$c.Close()"
	u := utf16.Encode([]rune(ps))
	b := make([]byte, 2*len(u))
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	hold := exec.Command("/bin/sh", "-c", onPCRaw("powershell -NoProfile -EncodedCommand "+base64.StdEncoding.EncodeToString(b)))
	if err := hold.Start(); err != nil {
		r.t.Fatal(err)
	}
	defer hold.Process.Kill()
	using := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		st, err := r.lc.State(ctx)
		if err != nil {
			return false
		}
		for _, u := range st.Usage {
			if string(u.DeviceID) == pc && u.Port == 80 {
				return true
			}
		}
		return false
	}
	r.until("阿杰的台式机 using "+web, using)
	r.logf("阿杰的台式机 uses %s", web)
	r.card(r.a.self)
	r.record()
	r.hold(time.Second)
	r.logf("closing %s", web)
	r.toggleService(web)
	closed := time.Now()
	r.t.Cleanup(func() {
		// Opened again, as it was, whatever happened.
		on := false
		for _, v := range r.a.store.Snapshot().LAN(r.lanID("周末联机")).Services[model.DeviceID(r.a.self)] {
			on = on || v.Port == 80 && v.On
		}
		if !on {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.a.store.Do(ctx, model.Op{Kind: model.OpServiceSet, LAN: r.lanID("周末联机"), Device: model.DeviceID(r.a.self), Service: "tcp/80", On: true})
		}
	})
	r.until("the red light", func() bool { d := r.a.device(pc); return d != nil && d.closed != "" })
	r.logf("cut: 阿杰的台式机 says %q, %s after the switch", r.a.status(r.a.device(pc)), since(time.Now(), closed))
	r.hold(450 * time.Millisecond)
	r.still("red-light")
	r.record()
	r.hold(1500 * time.Millisecond)
	r.still("closed")
	r.record()
	r.hold(3 * time.Second)
	r.logf("opening %s again", web)
	r.toggleService(web)
	r.hold(3 * time.Second)
	r.still("opened-again")
}

// Item 5's "已连接": this computer uses 构建服务器's web page over one long
// connection (longconn, from DENEB_LONGCONN, through this computer's
// SOCKS5): on 工作室's canvas its second line counts from when it began,
// and says how it connects again once it ends.
func m2using(r *realRig) {
	bin := os.Getenv("DENEB_LONGCONN")
	if bin == "" {
		r.t.Fatal("item 5: set DENEB_LONGCONN to a longconn built for this computer")
	}
	r.lan("工作室")
	build := r.named("构建服务器")
	if !linkedPath(r.a.device(build)) {
		r.t.Fatal("item 5: 构建服务器 is not connected in 工作室")
	}
	addr := r.a.snap.Devices[model.DeviceID(build)].Addr
	hold := exec.Command(bin, "-socks", "127.0.0.1:41080", "-active", "20s", "-pause", "1s", "-total", "20s", addr)
	r.record()
	r.hold(time.Second)
	if err := hold.Start(); err != nil {
		r.t.Fatal(err)
	}
	began := time.Now()
	done := make(chan error, 1)
	go func() { done <- hold.Wait() }()
	counting := func() bool { return strings.HasPrefix(r.a.status(r.a.device(build)), "已连接") }
	r.until("已连接", counting)
	r.logf("构建服务器 says %q, %s after the connection began", r.a.status(r.a.device(build)), since(time.Now(), began))
	r.hold(6 * time.Second)
	r.logf("构建服务器 says %q", r.a.status(r.a.device(build)))
	r.still("counting")
	r.record()
	r.until("the connection over", func() bool {
		select {
		case err := <-done:
			r.logf("longconn ended: %v", err)
			return true
		default:
			return false
		}
	})
	ended := time.Now()
	r.until("构建服务器 not counting", func() bool { return !counting() })
	r.logf("构建服务器 says %q, %s after the connection ended", r.a.status(r.a.device(build)), since(time.Now(), ended))
	r.hold(2 * time.Second)
	r.still("done")
}

// Item 12's 续上: a login of mine due, renewed from the menu bar's panel.
// The server makes 构建服务器's login due (deneb-control expire) while its
// engine is stopped, so that it does not renew itself; this computer
// renews it with device.renew. Light and dark at once, the dark panel a
// second tester on the same world, recorded to DENEB_FRAMES_TWIN: the
// click is the light one's.
func m2renew(r *realRig) {
	build := r.named("构建服务器")
	dev := model.DeviceID(build)
	due := func() bool {
		d := r.a.store.Snapshot().Devices[dev]
		return d != nil && (d.Login.Expired || d.Login.Due(time.Now()))
	}
	r.until("构建服务器's login due", due)
	r.a.showMain = func() {}
	r.a.panelShown = true
	window := r.tt
	m := &menubarScreen{a: r.a, open: true}
	r.tt = ui.NewTester(m.view, screenW, screenH)
	dark := ui.NewTester(m.view, screenW, screenH)
	dark.SetDark(true)
	twin, n := os.Getenv("DENEB_FRAMES_TWIN"), 0
	if twin != "" {
		os.MkdirAll(filepath.Join(twin, "real-"+r.step), 0o755)
	}
	darkStill := func(name string) {
		if twin == "" {
			return
		}
		dark.SetScale(2)
		dark.Frame()
		if f, err := os.Create(filepath.Join(twin, fmt.Sprintf("real-%s_%s.png", r.step, name))); err == nil {
			png.Encode(f, dark.Image())
			f.Close()
		}
		dark.SetScale(1)
		dark.Frame()
	}
	mark := ""
	r.watch = func() {
		dark.Advance(time.Second / 60)
		dark.Frame()
		if twin != "" && r.rec {
			if f, err := os.Create(filepath.Join(twin, "real-"+r.step, fmt.Sprintf("%05d.jpg", n))); err == nil {
				jpeg.Encode(f, dark.Image(), &jpeg.Options{Quality: 88})
				f.Close()
			}
			n++
		}
		if st := trayState(r.a.snap, time.Now()); st != mark {
			mark = st
			r.logf("the mark is %s", st)
		}
	}
	defer func() { r.tt, r.a.panelShown, r.a.showMain, r.watch, r.rec = window, false, nil, nil, false }()
	chore := say("构建服务器", "需要重新登录")
	r.record()
	r.until("the panel's "+chore, func() bool { return r.tt.HasText(chore) })
	r.hold(1500 * time.Millisecond)
	r.still("renew-due")
	darkStill("renew-due")
	r.record()
	r.logf("续上")
	r.click("续上")
	clicked := time.Now()
	r.until("已续上", func() bool { return r.tt.HasText("已续上") })
	r.until("构建服务器's login renewed", func() bool { return !due() })
	renewed := time.Now()
	r.until("the row folded away", func() bool { return !r.tt.HasText("已续上") && !r.tt.HasText(chore) })
	r.logf("renewed by the server %s after the click; the row gone %s after", since(renewed, clicked), since(time.Now(), clicked))
	r.hold(1500 * time.Millisecond)
	r.still("renewed")
	darkStill("renewed")
}

// Item 11: 构建服务器 waits for 阿杰 in his 周末联机, let in once and
// turned away once.
func m2approve(r *realRig) {
	lan := r.lanID("周末联机")
	build := r.named("构建服务器")
	if lan == "" || build == "" {
		r.t.Fatal("item 11: no 周末联机 or no 构建服务器")
	}
	dev := model.DeviceID(build)
	in := func() bool { return r.storeLAN(lan).Has(dev) }
	waits := func() bool { return r.storeLAN(lan).HasPending(dev) }
	approve := func(on bool) {
		r.shell(onNAS(opArgs("lan.setApproveDevices", map[string]any{"lan": lan, "on": on})))
		r.until(fmt.Sprintf("新设备要同意 %v", on), func() bool { return r.storeLAN(lan).ApproveDevices == on })
	}
	// As it was, whatever happens: 构建服务器 in, the switch off.
	r.t.Cleanup(func() {
		if waits() {
			r.shell(onNAS(opArgs("device.approve", map[string]any{"lan": lan, "device": build})))
		} else if !in() {
			r.shell("denebd lan add " + string(lan) + " " + build)
			r.until("构建服务器 back in 周末联机", func() bool { return in() || waits() })
			if waits() {
				r.shell(onNAS(opArgs("device.approve", map[string]any{"lan": lan, "device": build})))
			}
		}
		r.until("构建服务器 in 周末联机", in)
		approve(false)
	})
	if in() {
		r.shell("denebd lan remove " + string(lan) + " " + build)
		r.until("构建服务器 out of 周末联机", func() bool { return !in() })
	}
	approve(true)
	r.lan("周末联机")
	inTray := func() bool { return slices.ContainsFunc(r.a.tray, func(d *device) bool { return d.id == build }) }
	r.until("构建服务器 in the tray", inTray)
	r.record()
	r.hold(time.Second)

	// Let in.
	r.logf("dragging 构建服务器 in")
	r.fromTray(build, ui.Point{X: r.a.origin.X + 640, Y: r.a.origin.Y + 470})
	r.until("构建服务器 waiting for 阿杰", func() bool { d := r.a.device(build); return d != nil && d.pending && waits() })
	r.logf("构建服务器 says %q", r.a.status(r.a.device(build)))
	r.hold(2 * time.Second)
	r.still("waiting")
	r.record()
	r.shell(onNAS("status"))
	approved := r.shell(onNAS(opArgs("device.approve", map[string]any{"lan": lan, "device": build})))
	r.until("构建服务器 let in", func() bool { d := r.a.device(build); return d != nil && !d.pending && in() })
	letIn := time.Now()
	r.until("构建服务器 connected", func() bool { return linkedPath(r.a.device(build)) })
	r.logf("构建服务器 let in %s after 阿杰 did it, connected %s after: %s", since(letIn, approved), since(time.Now(), approved), r.a.device(build).path)
	r.shell(onNAS("status"))
	r.hold(2500 * time.Millisecond)
	r.still("let-in")

	// Turned away.
	r.record()
	r.logf("dragging 构建服务器 into the tray")
	r.toTray(build)
	r.until("构建服务器 out of 周末联机", func() bool { return !in() && !waits() })
	r.hold(5 * time.Second)
	r.until("构建服务器 in the tray", inTray)
	r.logf("dragging 构建服务器 in again")
	r.fromTray(build, ui.Point{X: r.a.origin.X + 640, Y: r.a.origin.Y + 470})
	r.until("构建服务器 waiting again", func() bool { d := r.a.device(build); return d != nil && d.pending && waits() })
	r.hold(2 * time.Second)
	refused := r.shell(onNAS(opArgs("device.reject", map[string]any{"lan": lan, "device": build})))
	r.until("构建服务器 turned away", func() bool { return r.a.device(build) == nil && !waits() && !in() })
	r.logf("构建服务器 off the canvas %s after 阿杰 turned it away; the window says %q", since(time.Now(), refused), r.tt.Texts())
	r.shell(onNAS("status"))
	r.hold(3 * time.Second)
	r.still("turned-away")
}

// Item 2, with 1. First a network change as the engine handles it (denebd
// debug netchange): every link is made again, and the window shows none of
// it. Then 阿杰的 NAS's direct path breaks (denebd debug block nas): the
// link goes over the relay (2); a network change while it stays broken
// connects it over the relay first (1), and once it mends (unblock) the
// link goes direct again. Every change shown is logged at its moment, the
// engine's own reports beside it, and no frame shows 断开, 正在连接 or
// 连不上, nor the header anything but 都连上了.
func m2netchange(r *realRig) {
	r.lan("周末联机")
	var ids []string
	for _, d := range r.a.devices {
		if d.id != r.a.self {
			ids = append(ids, d.id)
		}
	}
	nas := r.named("阿杰的 NAS")
	if !slices.Contains(ids, nas) {
		r.t.Fatal("item 2: 阿杰的 NAS is not in 周末联机")
	}
	r.until("every link up", func() bool {
		for _, id := range ids {
			if !linkedPath(r.a.device(id)) {
				return false
			}
		}
		return true
	})
	for _, id := range ids {
		defer r.engineSays(id)()
	}
	name := func(id string) string { return r.a.snap.Devices[model.DeviceID(id)].Name }
	went := map[string][]string{}
	bad := ""
	r.watch = func() {
		for _, id := range ids {
			d := r.a.device(id)
			p := "off the canvas"
			if d != nil {
				p = d.path
			}
			if w := went[id]; len(w) == 0 || w[len(w)-1] != p {
				went[id] = append(w, p)
				status := ""
				if d != nil {
					status = r.a.status(d)
				}
				r.logf("the window shows %s %s: %s", name(id), p, status)
			}
			if bad == "" && !linkedPath(d) {
				bad = fmt.Sprintf("%s %s", name(id), p)
			}
		}
		if bad == "" {
			if _, line := r.a.summary(&lightPalette); !strings.Contains(line, "都连上了") {
				bad = fmt.Sprintf("the header %q", line)
			} else if r.tt.HasText("断开") || r.tt.HasText("正在连接") || r.tt.HasText("连不上") {
				bad = fmt.Sprintf("%q", r.tt.Texts())
			}
			if bad != "" {
				bad += " at " + time.Now().Format("15:04:05.000")
			}
		}
	}
	defer func() { r.watch = nil }()
	nasPath := func() string {
		if d := r.a.device(nas); d != nil {
			return d.path
		}
		return ""
	}

	// A network change, the paths whole.
	r.record()
	r.hold(2 * time.Second)
	changed := r.shell("denebd debug netchange")
	r.logf("network change at %s", changed.Format("15:04:05.000"))
	r.hold(10 * time.Second)
	r.still("netchange")

	// 阿杰的 NAS's direct path broken: over the relay.
	r.record()
	r.t.Cleanup(func() { exec.Command("denebd", "debug", "unblock", "nas").Run() })
	blocked := r.shell("denebd debug block nas")
	r.until("阿杰的 NAS over the relay", func() bool { return nasPath() == "relay" })
	r.logf("阿杰的 NAS over the relay %s after its direct path broke", since(time.Now(), blocked))
	r.hold(4 * time.Second)
	r.still("relay")

	// A network change while it stays broken: connected over the relay
	// first; mended, direct.
	r.record()
	again := r.shell("denebd debug netchange")
	r.hold(6 * time.Second)
	r.logf("阿杰的 NAS %s, 6 s after a network change at %s", nasPath(), again.Format("15:04:05.000"))
	mended := r.shell("denebd debug unblock nas")
	r.until("阿杰的 NAS direct again", func() bool { return nasPath() == "direct" })
	r.logf("阿杰的 NAS direct %s after its path mended", since(time.Now(), mended))
	r.hold(4 * time.Second)
	r.still("direct-again")
	for _, id := range ids {
		r.logf("%s showed %s", name(id), strings.Join(went[id], ", "))
	}
	if bad != "" {
		r.t.Errorf("item 2: the window showed %s", bad)
	}
}
