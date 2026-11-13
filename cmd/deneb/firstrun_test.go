package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
)

// fakeSigner is the server's login page as the first step meets it: a code
// goes to any email; right signs in, which the engine then reports (in).
type fakeSigner struct {
	mu    sync.Mutex
	right string
	sent  []string
	in    func()
}

func (f *fakeSigner) SendCode(ctx context.Context, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, email)
	return nil
}

func (f *fakeSigner) CheckCode(ctx context.Context, email, code string) error {
	if code != f.right {
		return client.ErrWrongCode
	}
	f.in()
	return nil
}

// TestFirstRunLive: the first time's steps in the real window (DESIGN
// §7.3, M6 i5), on a fresh engine: no one signed in, the first step shows
// in place of the login button; the code goes to the email; a wrong one
// shakes, the right one signs in; with no adapter to put in here, the
// last step comes next; a LAN of one's own ends the steps, which this
// computer keeps.
func TestFirstRunLive(t *testing.T) {
	r := newLiveRig(t, fakeworld.Fresh, "first-run")
	saved := false
	s := &fakeSigner{right: "135790", in: func() {
		r.w.E.Update(func(st *apitype.State) { st.LoggedIn, st.User, st.SelfDeviceID = true, fake.Me, fake.ThisMac })
	}}
	r.a.signer, r.a.saveOnboarded = s, func() { saved = true }
	r.until("the first step", func() bool { return r.tt.HasText("登录 Deneb") && r.tt.HasText("发验证码") })
	r.tt.Frame()
	r.tt.Type("mia@deneb.test")
	r.settle(200 * time.Millisecond)
	r.click("发验证码")
	r.until("the code's boxes", func() bool { return r.tt.HasText("验证码发到了 mia@deneb.test") })
	if len(s.sent) != 1 || s.sent[0] != "mia@deneb.test" {
		t.Errorf("codes sent to %v", s.sent)
	}
	r.settle(300 * time.Millisecond)
	r.tt.Type("000000")
	r.until("the wrong code said", func() bool { return r.tt.HasText("验证码不对，再看一眼") })
	r.settle(500 * time.Millisecond)
	r.tt.Type("135790")
	r.until("signed in", func() bool { return r.tt.HasText("登录了") })
	r.until("the last step, with no adapter here", func() bool { return r.tt.HasText("建一个局域网，或者加入朋友的") })
	if r.tt.HasText("给这台电脑装一块网卡") {
		t.Error("the adapter's step came, with no adapter to put in")
	}
	r.click("新建局域网")
	r.until("the window, the LAN on its canvas", func() bool { return r.a.onboard == nil && len(r.a.snap.LANs) == 1 })
	if !saved {
		t.Error("the steps done, this computer did not keep it")
	}
}

// TestFirstRunOnce: signed in already, with a LAN, the steps never show,
// and this computer keeps that they need not; signed out later, the first
// step alone shows, and signing in is all.
func TestFirstRunOnce(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "first-run-once")
	saved := false
	s := &fakeSigner{right: "135790", in: func() {
		r.w.E.Update(func(st *apitype.State) { st.LoggedIn, st.User, st.SelfDeviceID = true, fake.Me, fake.ThisMac })
	}}
	r.a.signer, r.a.saveOnboarded = s, func() { saved = true }
	r.until("the LANs", func() bool { return r.a.snap.Loaded && len(r.a.snap.LANs) > 0 })
	r.settle(300 * time.Millisecond)
	if r.a.onboard != nil || !saved {
		t.Fatalf("signed in with LANs: steps %+v, kept %v", r.a.onboard, saved)
	}
	r.w.E.Update(func(st *apitype.State) { st.LoggedIn = false })
	r.until("the first step again", func() bool { return r.tt.HasText("登录 Deneb") })
	if got := r.a.nextStep(0); got != 3 {
		t.Errorf("after signing in again, step %d, want done", got)
	}
}

// TestNextStep: which step comes after which, in the real window: the
// adapter only where the window puts it in and the engine has none yet;
// the first LAN only while there is none; once done here, nothing after
// signing in. The demo shows them all.
func TestNextStep(t *testing.T) {
	r := newM6Rig(t, true, -1)
	if got := r.a.nextStep(0); got != 1 {
		t.Errorf("the demo: after signing in, step %d", got)
	}
	r.a.signer = &fakeSigner{}
	if got := r.a.nextStep(0); got != 2 {
		t.Errorf("no adapter to put in: step %d, want 2", got)
	}
	r.a.adapter = func(context.Context) error { return nil }
	if got := r.a.nextStep(0); got != 1 {
		t.Errorf("an adapter to put in: step %d, want 1", got)
	}
	r.a.onboarded = true
	if got := r.a.nextStep(0); got != 3 {
		t.Errorf("done before: step %d, want 3", got)
	}
}

// TestSaveDiagnostics: the package goes into the downloads under its
// name; a second the same minute gets " (2)"; one the engine could not
// put together leaves nothing behind.
func TestSaveDiagnostics(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 9, 23, 41, 7, 0, time.Local)
	p, err := saveDiagnosticsIn(dir, zipOf("PK…"), at)
	if err != nil || filepath.Base(p) != "Deneb 诊断包 2026-11-09 23-41.zip" {
		t.Fatalf("first: %q, %v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "PK…" {
		t.Errorf("its contents: %q", b)
	}
	p2, err := saveDiagnosticsIn(dir, zipOf("PK2"), at)
	if err != nil || filepath.Base(p2) != "Deneb 诊断包 2026-11-09 23-41 (2).zip" {
		t.Errorf("second: %q, %v", p2, err)
	}
	if _, err := saveDiagnosticsIn(dir, failing{}, at.Add(time.Minute)); err == nil {
		t.Error("an engine that failed: no error")
	}
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Errorf("left in the downloads: %s", strings.Join(names, ", "))
	}
}

type zipOf string

func (z zipOf) Diagnostics(ctx context.Context, w io.Writer) error {
	_, err := w.Write([]byte(z))
	return err
}

type failing struct{}

func (failing) Diagnostics(context.Context, io.Writer) error {
	return errors.New("no engine")
}
