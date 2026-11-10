package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
)

// TestRealAcceptance6 plays the window's part in M6's acceptance (i5 and
// i7 in design/plan/m6-acceptance.md) on a real engine and the real
// server, when DENEB_REAL6 lists the items, as "signin,diag". Frames and
// log go as M2's (DENEB_FRAMES, DENEB_DARK); DENEB_SOCKET names the engine.
//
//   - signin: an engine never signed in: the first step (DESIGN §7.3) signs
//     it in, as DENEB_REAL6_EMAIL, a test account whose code is in the seed
//     file DENEB_REAL6_SEED (read, never logged); a wrong code first. There
//     is no adapter's step on macOS yet, so the last step comes: a LAN of
//     one's own, which the clean-up deletes again, and signs out.
//   - diag: this computer's card, "更多", "导出诊断包" (§6.14): the engine
//     puts the package together, into a folder of the test's; the toast
//     says where, and shows it.
func TestRealAcceptance6(t *testing.T) {
	items := os.Getenv("DENEB_REAL6")
	if items == "" {
		t.Skip("set DENEB_REAL6 to the M6 items to play on a real engine, as signin,diag")
	}
	r := newRealRig(t)
	r.until("the engine", func() bool { return r.a.snap.Engine || r.a.snap.Version > 0 })
	for _, s := range strings.Split(items, ",") {
		fn := map[string]func(*realRig){"signin": m6signin, "diag": m6diag}[strings.TrimSpace(s)]
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

// seedCode is the code of the test account email in the seed file at
// path.
func seedCode(t *testing.T, path, email string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the seed file: %v", err)
	}
	var seed struct {
		Accounts []struct{ Email, Code string } `json:"accounts"`
	}
	if err := json.Unmarshal(b, &seed); err != nil {
		t.Fatal(err)
	}
	for _, a := range seed.Accounts {
		if a.Email == email && a.Code != "" {
			return a.Code
		}
	}
	t.Fatalf("no test account %s in the seed file", email)
	return ""
}

// typeIn types s a key at a time, as a hand does.
func (r *realRig) typeIn(s string, each time.Duration) {
	for _, ch := range s {
		r.tt.Type(string(ch))
		r.hold(each)
	}
}

func m6signin(r *realRig) {
	email := os.Getenv("DENEB_REAL6_EMAIL")
	code := seedCode(r.t, os.Getenv("DENEB_REAL6_SEED"), email)
	if r.a.snap.LoggedIn {
		r.t.Fatal("signin: the engine is signed in already")
	}
	r.a.signer = client.NewSignIn(r.lc, map[string]string{m1Control: m1ControlPin})
	saved := false
	r.a.saveOnboarded = func() { saved = true }
	r.until("the first step", func() bool { return r.tt.HasText("登录 Deneb") && r.tt.HasText("发验证码") })
	r.hold(1200 * time.Millisecond)
	r.still("登录")
	r.record()
	r.typeIn(email, 50*time.Millisecond)
	r.hold(400 * time.Millisecond)
	sent := time.Now()
	r.click("发验证码")
	r.until("the code's boxes", func() bool { return r.tt.HasText("验证码发到了") })
	r.logf("the code asked for, %s after the click", time.Since(sent).Round(time.Millisecond))
	r.hold(900 * time.Millisecond)
	r.still("验证码")
	r.record()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	r.typeIn(wrong, 120*time.Millisecond)
	r.until("the wrong code said", func() bool { return r.tt.HasText("验证码不对，再看一眼") })
	r.hold(900 * time.Millisecond)
	r.still("验证码不对")
	r.record()
	r.typeIn(code, 120*time.Millisecond)
	checked := time.Now()
	r.until("signed in", func() bool { return r.tt.HasText("登录了") })
	r.logf("signed in, %s after the last digit", time.Since(checked).Round(time.Millisecond))
	r.hold(500 * time.Millisecond)
	r.still("登录了")
	r.record()
	r.until("the engine signed in", func() bool { return r.a.snap.LoggedIn })
	r.until("the last step", func() bool { return r.tt.HasText("建一个局域网，或者加入朋友的") })
	if r.tt.HasText("给这台电脑装一块网卡") {
		r.t.Error("signin: the adapter's step came on macOS")
	}
	r.hold(1200 * time.Millisecond)
	r.still("第一个局域网")
	r.record()
	r.click("新建局域网")
	r.until("the window, the new LAN on its canvas", func() bool { return r.a.onboard == nil && len(r.a.snap.LANs) == 1 })
	lan := r.a.snap.LANs[0]
	r.logf("the first LAN: %s", lan.Name)
	r.t.Cleanup(func() {
		r.shellOutAny("denebd lan delete " + shellQuote(lan.Name))
		r.shellOutAny("denebd logout")
	})
	r.hold(1500 * time.Millisecond)
	r.still("建好了")
	if !saved {
		r.t.Error("signin: the steps done, this computer did not keep it")
	}
}

func m6diag(r *realRig) {
	if !r.a.snap.LoggedIn {
		r.t.Fatal("diag: the engine is not signed in")
	}
	dir := r.t.TempDir()
	r.a.diagnostics = func() (string, error) { return saveDiagnosticsIn(dir, r.lc, time.Now()) }
	var revealed string
	r.a.reveal = func(p string) { revealed = p }
	self := r.a.device(r.a.self)
	if self == nil {
		r.t.Fatal("diag: this computer is not on the canvas")
	}
	r.hold(800 * time.Millisecond)
	r.record()
	at := r.at(self)
	r.tt.ClickAt(at.X, at.Y)
	r.tick()
	r.until("this computer's card", func() bool { return r.tt.HasText("这就是你正在用的电脑") })
	r.hold(800 * time.Millisecond)
	r.click("更多")
	r.until("its more", func() bool { return r.tt.HasText("导出诊断包") })
	r.hold(900 * time.Millisecond)
	r.still("更多")
	r.record()
	asked := time.Now()
	r.click("导出诊断包")
	r.until("the toast", func() bool { return r.tt.HasText("诊断包存到了「下载」") })
	r.logf("the package, %s after the click", time.Since(asked).Round(time.Millisecond))
	r.hold(600 * time.Millisecond)
	r.still("存好了")
	r.record()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || !strings.HasPrefix(ents[0].Name(), "Deneb 诊断包 ") {
		r.t.Fatalf("diag: in the folder: %v", ents)
	}
	b, _ := os.ReadFile(dir + "/" + ents[0].Name())
	if len(b) < 100 || !bytes.HasPrefix(b, []byte("PK")) {
		r.t.Errorf("diag: the package is %d bytes, not a zip", len(b))
	}
	r.logf("the package: %s, %d bytes", ents[0].Name(), len(b))
	r.click("在访达中显示")
	r.hold(600 * time.Millisecond)
	if revealed == "" || !strings.HasSuffix(revealed, ents[0].Name()) {
		r.t.Errorf("diag: shown %q", revealed)
	}
	r.hold(1200 * time.Millisecond)
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
