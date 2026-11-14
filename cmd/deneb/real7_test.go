package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// TestRealAcceptance7 plays deleting a device, logging out and deleting the
// account (DESIGN §6.15) in the real window, on real engines and a real
// control server, when DENEB_REAL7 lists the items, as
// "logout,device,self,account,seed". All of it is on this computer and the
// test's own: a development server (deneb-control -dev, plain HTTP on
// 127.0.0.1) with test data, and engines in user space with state of their
// own, never a deployment's, since what is deleted is gone. DENEB_REAL7_BIN
// is a folder with deneb-control and denebd built from this tree. Frames
// and log go as M2's (DENEB_FRAMES, DENEB_DARK).
//
//   - logout: the account card, 登出: the sign-in; signed in again (off the
//     recording), this computer comes back as it was.
//   - device: My Devices, 我的 NAS details, 删除这台设备: the toast; as it goes,
//     the server deletes it, and logs its engine out.
//   - self: My Devices, this computer details, 删除这台电脑: the toast, the window
//     fades to the sign-in; signed in again, it is a new device.
//   - account: 注销账号: what goes, 工作室 handed to Mia, who joined first;
//     my email again; the sign-in says the account is gone.
//   - seed: a server whose seed names my account: the page says why not.
func TestRealAcceptance7(t *testing.T) {
	items := os.Getenv("DENEB_REAL7")
	if items == "" {
		t.Skip("set DENEB_REAL7 to the M7 items to play on a server and engines of the test's own, as logout,device,self,account,seed")
	}
	var w *m7world
	var r *realRig
	for _, s := range strings.Split(items, ",") {
		s = strings.TrimSpace(s)
		fn := map[string]func(*realRig, *m7world){"logout": m7logout, "device": m7device, "self": m7self,
			"account": m7account, "seed": m7seed}[s]
		if fn == nil {
			t.Fatalf("no item %q", s)
		}
		// The seed's item has a world of its own: one whose seed names me.
		if seedMe := s == "seed"; w == nil || w.seedMe != seedMe {
			if w != nil {
				w.stop()
			}
			w = newM7World(t, seedMe)
			r = w.rig()
		}
		r.step = s
		r.logf("item %s begins", s)
		fn(r, w)
		r.rec = false
		r.logf("item %s ends", s)
	}
}

const (
	m7me   = "kittors@example.com"
	m7code = "000000" // the development server's code for its seed's accounts
)

// m7world is the items' world: a development control server, and engines
// in user space: kittors's MacBook (the window's) and NAS, Mia's MacBook,
// 阿杰's NAS. kittors signs up (sign-up is open), so the account can be
// deleted; Mia and 阿杰 are the seed's. kittors owns 工作室 (Mia joined it
// first, then 阿杰) and 家 (the NAS), and is in 阿杰's 周末联机.
type m7world struct {
	t      *testing.T
	bin    string
	dir    string
	base   string
	seedMe bool
	cmds   []*exec.Cmd
}

func newM7World(t *testing.T, seedMe bool) *m7world {
	t.Helper()
	bin := os.Getenv("DENEB_REAL7_BIN")
	if bin == "" {
		t.Fatal("set DENEB_REAL7_BIN to a folder with deneb-control and denebd built from this tree")
	}
	// Short: the engines' sockets go in it.
	dir, err := os.MkdirTemp("/tmp", "dnb7-")
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	w := &m7world{t: t, bin: bin, dir: dir, base: "http://" + addr, seedMe: seedMe}
	t.Cleanup(func() { w.stop(); os.RemoveAll(dir) })
	type account struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Initial     string `json:"initial"`
		Name        string `json:"name"`
		Code        string `json:"code"`
	}
	accounts := []account{{"mia@example.com", "Mia", "M", "mia", m7code}, {"jie@example.com", "阿杰", "杰", "jie", m7code}}
	if seedMe {
		accounts = append(accounts, account{m7me, "kittors", "K", "kittors", m7code})
	}
	seed, _ := json.Marshal(map[string]any{"accounts": accounts, "relays": []any{}})
	seedFile := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(seedFile, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	w.start("control", "deneb-control", "-dev", "-open-signup", "-seed", seedFile, "-listen", addr,
		"-public-url", w.base, "-data", filepath.Join(dir, "control"), "-log-dir", "off", "-mail", "log")
	w.until("the control server", func() bool {
		resp, err := http.Get(w.base + "/")
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})
	if !seedMe {
		w.signup(m7me)
	}
	for _, e := range []struct{ name, label, kind, host, email string }{
		{"mac", "kittors 的 MacBook", "laptop", "kittors-macbook", m7me},
		{"nas", "我的 NAS", "nas", "kittors-nas", m7me},
		{"mia", "Mia 的 MacBook", "laptop", "mia-macbook", "mia@example.com"},
		{"jnas", "阿杰的 NAS", "nas", "jie-nas", "jie@example.com"},
	} {
		if seedMe && e.name != "mac" {
			continue
		}
		w.engine(e.name, e.label, e.kind, e.host)
		w.approve(e.name, e.email)
	}
	w.cli("mac", "lan", "create", "工作室")
	if seedMe {
		return w
	}
	w.cli("mac", "lan", "create", "家")
	w.cli("nas", "lan", "add", "家")
	w.cli("mia", "lan", "join", w.invite("mac", "工作室"))
	w.cli("jnas", "lan", "join", w.invite("mac", "工作室"))
	w.cli("jnas", "lan", "create", "周末联机")
	w.cli("mac", "lan", "join", w.invite("jnas", "周末联机"))
	return w
}

// rig is the window on the world's MacBook, signing in by itself as to a
// server it knows (DESIGN §7.3).
func (w *m7world) rig() *realRig {
	w.t.Setenv("DENEB_SOCKET", w.sock("mac"))
	r := newRealRig(w.t)
	r.a.signer = client.NewSignIn(r.lc, map[string]string{w.base: ""})
	r.a.saveOnboarded = func() {}
	r.until("the engine, signed in", func() bool { return r.a.snap.Engine && r.a.snap.LoggedIn && r.a.snap.Loaded })
	return r
}

func (w *m7world) sock(name string) string { return filepath.Join(w.dir, name+".sock") }

// start runs bin's binary, its output into name.log.
func (w *m7world) start(name, binary string, args ...string) {
	w.t.Helper()
	f, err := os.Create(filepath.Join(w.dir, name+".log"))
	if err != nil {
		w.t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(w.bin, binary), args...)
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	w.cmds = append(w.cmds, cmd)
}

// stop stops the engines, then the server.
func (w *m7world) stop() {
	for i := len(w.cmds) - 1; i >= 0; i-- {
		cmd := w.cmds[i]
		cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}
	w.cmds = nil
}

func (w *m7world) until(what string, ok func() bool) {
	w.t.Helper()
	for deadline := time.Now().Add(time.Minute); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// engine starts an engine in user space on the world's server: no SOCKS
// port, no interface of the system's, a name for the window.
func (w *m7world) engine(name, label, kind, host string) {
	w.t.Helper()
	w.start(name, "denebd", "run", "-state", filepath.Join(w.dir, name), "-socket", w.sock(name),
		"-control", w.base, "-name", label, "-kind", kind, "-hostname", host, "-bind-if", "none", "-socks", "")
	w.until(name+"'s engine", func() bool { _, err := w.state(name); return err == nil })
}

func (w *m7world) state(name string) (*apitype.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return localclient.New(w.sock(name)).State(ctx)
}

// approve signs the engine name in as email, without a browser: the
// development server's /dev/approve.
func (w *m7world) approve(name, email string) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link, err := localclient.New(w.sock(name)).Login(ctx, "")
	if err != nil {
		w.t.Fatalf("%s: login: %v", name, err)
	}
	w.post("/dev/approve", map[string]string{"login": link, "email": email})
	w.until(name+" signed in", func() bool { st, err := w.state(name); return err == nil && st.LoggedIn })
}

// signup makes email's account, as the Web's login does with sign-up open.
func (w *m7world) signup(email string) {
	w.t.Helper()
	n := w.codes(email)
	w.post("/api/v1/login/email", map[string]string{"email": email})
	w.post("/api/v1/login/code", map[string]string{"email": email, "code": w.code(email, n)})
}

func (w *m7world) post(path string, body any) {
	w.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(w.base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		w.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		w.t.Fatalf("POST %s: %s %s", path, resp.Status, msg)
	}
}

func codeLine(email string) *regexp.Regexp {
	return regexp.MustCompile(`the login code for ` + regexp.QuoteMeta(email) + ` is (\d{6})`)
}

// codes is how many codes the server has logged for email.
func (w *m7world) codes(email string) int {
	b, _ := os.ReadFile(filepath.Join(w.dir, "control.log"))
	return len(codeLine(email).FindAllSubmatch(b, -1))
}

// code waits for a code past the first n the server logged for email, and
// returns it, never logged here.
func (w *m7world) code(email string, n int) string {
	w.t.Helper()
	var code string
	w.until("the code in the server's log", func() bool {
		b, _ := os.ReadFile(filepath.Join(w.dir, "control.log"))
		if m := codeLine(email).FindAllSubmatch(b, -1); len(m) > n {
			code = string(m[len(m)-1][1])
		}
		return code != ""
	})
	return code
}

// cli runs denebd's command line on the engine name.
func (w *m7world) cli(name string, args ...string) string {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(w.bin, "denebd"), args...)
	cmd.Env = append(os.Environ(), "DENEB_SOCKET="+w.sock(name))
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("denebd %s on %s: %v\n%s", strings.Join(args, " "), name, err, out)
	}
	return strings.TrimSpace(string(out))
}

var linkRE = regexp.MustCompile(`[a-z]+://\S+`)

func (w *m7world) invite(name, lan string) string {
	w.t.Helper()
	out := w.cli(name, "lan", "invite", lan)
	link := linkRE.FindString(out)
	if link == "" {
		w.t.Fatalf("lan invite %s: no link in %q", lan, out)
	}
	return link
}

// run shows d of the view's own time, frame by frame, however long each
// takes to draw: the pauses a hand makes, as the demo's recordings pace
// them (m7frames_test.go). hold, by the clock on the wall, gives fewer
// frames when they are slow to draw.
func (r *realRig) run(d time.Duration) {
	for i := 0; i < int((d*60+time.Second/2)/time.Second); i++ {
		r.tick()
	}
}

// await shows frames until ok holds, one per 1/60 s of real time at most:
// what the server and the engines take shows as long as it took.
func (r *realRig) await(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(time.Minute)
	next := time.Now()
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("item %s: timed out waiting for %s; the window shows %q", r.step, what, r.tt.Texts())
		}
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		}
		next = time.Now().Add(time.Second / 60)
		r.tick()
	}
}

// keys types s a key at a time, each of the view's time apart.
func (r *realRig) keys(s string, each time.Duration) {
	for _, ch := range s {
		r.tt.Type(string(ch))
		r.run(each)
	}
}

// tapDevice clicks the device of id, and waits for its card.
func (r *realRig) tapDevice(id string) {
	r.t.Helper()
	d := r.a.device(id)
	if d == nil {
		r.t.Fatalf("item %s: %s is not on the canvas", r.step, id)
	}
	p := r.at(d)
	r.tt.ClickAt(p.X, p.Y)
	r.tick()
	r.await("its card", func() bool { return r.a.selected == id && r.a.cardAt.W > 0 })
}

// signIn signs this computer in again through the window's first step,
// off the recording: my email, then the code the server sent.
func (w *m7world) signIn(r *realRig) {
	r.t.Helper()
	r.rec = false
	r.until("the sign-in", func() bool { return r.tt.HasText("发验证码") && r.a.curtain == nil })
	n := w.codes(m7me)
	r.keys(m7me, time.Second/60)
	r.click("发验证码")
	r.until("the code's boxes", func() bool { return r.tt.HasText("验证码发到了") })
	code := m7code
	if !w.seedMe {
		code = w.code(m7me, n)
	}
	r.keys(code, time.Second/60)
	r.until("the window", func() bool { return r.a.onboard == nil && r.a.snap.LoggedIn && r.a.snap.Loaded })
	r.logf("signed in again, as device %s", r.a.self)
}

// openAccount opens the account card from the sidebar's last row.
func (r *realRig) openAccount() {
	r.t.Helper()
	r.click("账号")
	r.await("the account card", func() bool { return r.tt.HasText("注销账号") && r.tt.HasText(m7me) })
}

func m7logout(r *realRig, w *m7world) {
	self := r.a.self
	r.lan("工作室")
	r.run(600 * time.Millisecond)
	r.record()
	r.run(700 * time.Millisecond)
	r.openAccount()
	r.run(1200 * time.Millisecond)
	r.still("账号卡片")
	r.record()
	r.click("登出")
	r.await("the sign-in", func() bool { return r.tt.HasText("发验证码") && r.a.curtain == nil })
	r.logf("logged out: the sign-in")
	if st, err := w.state("mac"); err != nil || st.LoggedIn {
		r.t.Errorf("logout: the engine says %+v, %v", st, err)
	}
	r.run(1300 * time.Millisecond)
	r.still("登出了")
	w.signIn(r)
	if r.a.self != self {
		r.t.Errorf("logout: this computer came back as %s, not %s", r.a.self, self)
	}
}

func m7device(r *realRig, w *m7world) {
	r.lan("家")
	nas := r.named("我的 NAS")
	r.until("我的 NAS on the canvas", func() bool { return nas != "" && r.a.device(nas) != nil })
	r.run(600 * time.Millisecond)
	r.record()
	r.run(600 * time.Millisecond)
	r.click("我的设备")
	r.run(600 * time.Millisecond)
	r.click("详情我的 NAS")
	r.run(600 * time.Millisecond)
	r.still("我的设备里的删除")
	r.record()
	clickVisibleDeviceAction(r.t, r.tt, "删除这台设备", 740)
	acceptConfirmation(r.t, r.tt, "删除设备")
	r.run(1000 * time.Millisecond)
	r.still("删掉了")
	r.record()
	// As the toast goes, the server hears of it, and logs the NAS out.
	r.await("the NAS logged out", func() bool { st, err := w.state("nas"); return err == nil && !st.LoggedIn })
	r.logf("我的 NAS deleted on the server: its engine is logged out")
	r.await("the toast gone", func() bool { return !r.tt.HasText("撤销") })
	r.run(1200 * time.Millisecond)
	r.still("提示条走了")
	if r.named("我的 NAS") != "" {
		r.t.Errorf("device: the NAS is still in the snapshot")
	}
}

func m7self(r *realRig, w *m7world) {
	self := r.a.self
	r.lan("工作室")
	r.run(600 * time.Millisecond)
	r.record()
	r.run(600 * time.Millisecond)
	r.click("我的设备")
	r.run(600 * time.Millisecond)
	r.click("详情" + r.a.snap.Device(r.a.snap.Self).Name)
	r.run(600 * time.Millisecond)
	r.still("删除这台电脑")
	r.record()
	clickVisibleDeviceAction(r.t, r.tt, "删除这台电脑", 740)
	acceptConfirmation(r.t, r.tt, "删除设备")
	r.run(1000 * time.Millisecond)
	r.still("这台电脑会被删掉")
	r.record()
	r.await("the sign-in", func() bool { return r.tt.HasText("发验证码") && r.a.curtain == nil })
	r.logf("this computer deleted: the sign-in")
	r.run(1300 * time.Millisecond)
	r.still("删掉了这台电脑")
	w.signIn(r)
	if r.a.self == self {
		r.t.Errorf("self: signed in again as the deleted device %s", self)
	}
	// The new device goes into my LANs again, as before.
	for _, l := range []string{"工作室", "家", "周末联机"} {
		w.cli("mac", "lan", "add", l)
	}
	r.until("this computer in its LANs", func() bool {
		for _, l := range r.a.snap.LANs {
			if l.Name == "工作室" && slices.Contains(l.Devices, model.DeviceID(r.a.self)) {
				return true
			}
		}
		return false
	})
}

func m7account(r *realRig, w *m7world) {
	r.lan("工作室")
	r.run(600 * time.Millisecond)
	r.record()
	r.run(600 * time.Millisecond)
	r.openAccount()
	r.run(900 * time.Millisecond)
	r.click("注销账号")
	r.await("what goes", func() bool {
		dl := r.a.deleting
		return dl != nil && dl.acc != nil && !dl.asking && len(dl.acc.Blocked) == 0
	})
	acc := r.a.deleting.acc
	r.logf("the dry run: %d devices, LANs deleted %v, handed over %v, left %v", len(acc.Devices), acc.LANs, acc.Transferred, acc.Left)
	r.run(1300 * time.Millisecond)
	r.still("会删掉的")
	r.record()
	for i := 0; i < 30; i++ {
		r.tt.Wheel(590, 420, 0, 14)
		r.tick()
	}
	r.run(700 * time.Millisecond)
	r.still("转给最早加入的人")
	r.record()
	field, ok := r.tt.Find("你的邮箱")
	if !ok {
		r.t.Fatalf("account: no email field; the window shows %q", r.tt.Texts())
	}
	r.tt.ClickAt(field.X+field.W/2, field.Y+field.H/2)
	r.tick()
	r.run(300 * time.Millisecond)
	r.keys(m7me, 55*time.Millisecond)
	r.run(600 * time.Millisecond)
	r.still("邮箱对上了")
	r.record()
	// The red button under the field: its words are the title's too.
	r.tt.ClickAt(590, field.Y+field.H/2+23+14+22)
	r.tick()
	r.await("deleting", func() bool { return r.a.deleting == nil || r.a.deleting.sending })
	sent := time.Now()
	r.await("the sign-in, saying so", func() bool { return r.tt.HasText("发验证码") && r.a.gone != nil && r.a.curtain == nil })
	r.logf("the account deleted: the sign-in says so, %s after the click", time.Since(sent).Round(time.Millisecond))
	r.run(1600 * time.Millisecond)
	r.still("注销了")
	if st, err := w.state("mac"); err != nil || st.LoggedIn {
		r.t.Errorf("account: the engine says %+v, %v", st, err)
	}
	r.logf("Mia's LANs now: %s", w.cli("mia", "lan", "list"))
}

func m7seed(r *realRig, w *m7world) {
	r.run(600 * time.Millisecond)
	r.record()
	r.run(600 * time.Millisecond)
	r.openAccount()
	r.run(900 * time.Millisecond)
	r.click("注销账号")
	r.await("the server's no", func() bool { return r.a.deleting != nil && r.a.deleting.refused != "" })
	r.logf("refused: %s", r.a.deleting.refused)
	r.run(1500 * time.Millisecond)
	r.still("种子账号")
	r.record()
	r.click("返回")
	r.await("the window", func() bool { return r.a.deleting == nil })
	r.run(900 * time.Millisecond)
}
