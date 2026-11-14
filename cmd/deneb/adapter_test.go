package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestAdapterNoticeLive: on Windows, a computer signed in with LANs whose
// engine has no network adapter (DESIGN §7.3, M6's second batch): no first
// time's steps, but the amber pill; its card says why and what Windows
// will ask; "装网卡" asks, the header says the adapter goes in, not that
// the engine is gone; once in, the tick, then the pill and the card go,
// and stay gone though the engine that has it answers later.
func TestAdapterNoticeLive(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "adapter-notice")
	release := make(chan struct{})
	r.a.signer, r.a.platform = &fakeSigner{}, "windows"
	r.a.onboarded = true
	r.a.adapter = func(ctx context.Context) error {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	r.until("the pill", func() bool { return r.tt.HasText("还没装网卡") })
	if r.a.onboard != nil {
		t.Fatal("signed in with LANs, the first time's steps showed")
	}
	r.click("还没装网卡")
	r.until("the card", func() bool {
		return r.tt.HasText("这台电脑还没装网卡") && r.tt.HasText("Windows 会弹窗问你，点「是」就行。")
	})
	r.settle(300 * time.Millisecond)
	r.click("装网卡")
	r.until("Windows asks", func() bool {
		return r.tt.HasText("等你在系统里点允许…") && r.tt.HasText("正在装网卡…")
	})
	if r.tt.HasText("连不上这台电脑上的 Deneb，正在重试…") {
		t.Error("while the adapter goes in, the header tells of an engine gone")
	}
	// The service starts with the adapter, then Windows's question is over.
	r.w.E.Update(func(st *apitype.State) { st.Mode = apitype.ModeKernel })
	r.settle(200 * time.Millisecond)
	if !r.tt.HasText("等你在系统里点允许…") {
		t.Error("the engine answered with its adapter before the install was over: the card went early")
	}
	close(release)
	r.until("the tick", func() bool { return r.tt.HasText("网卡装好了") })
	r.settle(adapterShown + 600*time.Millisecond)
	if r.tt.HasText("还没装网卡") || r.tt.HasText("这台电脑还没装网卡") || r.a.selected != "" {
		t.Errorf("in: the pill or the card stayed: %q", r.tt.Texts())
	}
	r.w.E.Update(func(st *apitype.State) { st.Mode = apitype.ModeUserspace })
	r.settle(300 * time.Millisecond)
	if r.tt.HasText("还没装网卡") {
		t.Error("put in from its card, the pill came back while the window ran")
	}
}

// TestAdapterNoticeDeclined: Windows's question answered no: the card says
// it works without, "装网卡" again; "以后再说" puts the pill away until
// Deneb starts again.
func TestAdapterNoticeDeclined(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "adapter-declined")
	r.a.signer, r.a.platform, r.a.onboarded = &fakeSigner{}, "windows", true
	r.a.adapter = func(context.Context) error { return errors.New("not allowed as an administrator") }
	r.until("the pill", func() bool { return r.tt.HasText("还没装网卡") })
	r.click("还没装网卡")
	r.until("the card", func() bool { return r.tt.HasText("装网卡") })
	r.settle(300 * time.Millisecond)
	r.click("装网卡")
	r.until("said no", func() bool {
		return r.tt.HasText("没装上可以先在 Deneb 里点「打开」访问，想装时再试一次")
	})
	r.settle(300 * time.Millisecond)
	if !r.tt.HasText("装网卡") || !r.tt.HasText("以后再说") || r.tt.HasText("正在装网卡…") {
		t.Errorf("declined: %q", r.tt.Texts())
	}
	r.click("以后再说")
	r.settle(600 * time.Millisecond)
	if r.tt.HasText("还没装网卡") || r.a.selected != "" {
		t.Errorf("put off: the pill or the card stayed: %q", r.tt.Texts())
	}
}

// TestAdapterNoticeWhere: the pill only where the window puts the adapter
// in (the demo shows it where it shows Windows), with none in yet; the
// engine's notices first; never after the first time's step put it in.
func TestAdapterNoticeWhere(t *testing.T) {
	r := newM6Rig(t, false, -1)
	code := func() string {
		if n := r.a.notice(); n != nil {
			return n.Code
		}
		return ""
	}
	if got := code(); got != "" {
		t.Errorf("the demo on the Mac: %q", got)
	}
	r.a.platform = "windows"
	if got := code(); got != "" {
		t.Errorf("Windows with the adapter in: %q", got)
	}
	r.d.Simulate("user-mode")
	r.run(100 * time.Millisecond)
	if got := code(); got != adapterNotice.Code {
		t.Errorf("Windows without the adapter: %q", got)
	}
	r.d.Simulate("notice-surge")
	r.run(100 * time.Millisecond)
	if got := code(); got != "tun-capture" {
		t.Errorf("Surge's and the adapter's: %q first", got)
	}
	r.a.noticesDone = map[string]bool{"tun-capture": true}
	r.a.signer = &fakeSigner{}
	if got := code(); got != "" {
		t.Errorf("a real window that cannot put it in (the Mac, Linux): %q", got)
	}
	r.a.adapter = func(context.Context) error { return nil }
	if got := code(); got != adapterNotice.Code {
		t.Errorf("a real window that can: %q", got)
	}
	r.a.adapterJob.done = true
	if got := code(); got != "" {
		t.Errorf("put in already: %q", got)
	}
}

// TestAdapterNoticeDemo: the demo's system lets the adapter in after a
// while; the tick shows a moment, then the pill and the card go.
func TestAdapterNoticeDemo(t *testing.T) {
	r := newM6Rig(t, false, -1)
	r.a.platform = "windows"
	r.d.Simulate("user-mode")
	r.run(900 * time.Millisecond)
	r.tap(r.text("还没装网卡"))
	r.run(900 * time.Millisecond)
	r.tap(r.text("装网卡"))
	r.run(600 * time.Millisecond)
	if !r.tt.HasText("等你在系统里点允许…") || !r.tt.HasText("正在装网卡…") {
		t.Fatalf("asking: %q", r.tt.Texts())
	}
	r.run(adapterAsk)
	if !r.tt.HasText("网卡装好了") || r.tt.HasText("正在装网卡…") {
		t.Fatalf("in: %q", r.tt.Texts())
	}
	r.run(adapterShown + 600*time.Millisecond)
	if r.tt.HasText("还没装网卡") || r.tt.HasText("网卡装好了") {
		t.Errorf("gone: %q", r.tt.Texts())
	}
}

// TestPutAdapterIn: Windows's question gets as long as the person takes,
// not the 30 s other work gets: the install's own time, whatever the
// caller's.
func TestPutAdapterIn(t *testing.T) {
	a := &app{adapter: func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d, ok := ctx.Deadline(); !ok || time.Until(d) < adapterWait-time.Minute {
			return errors.New("a short deadline")
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	cancel()
	if err := a.putAdapterIn(ctx); err != nil {
		t.Error(err)
	}
}

// macOS must offer the existing install flow to a signed-in userspace
// engine, with its own system authentication wording instead of Windows UAC.
func TestMacAdapterNoticeLive(t *testing.T) {
	r := newLiveRig(t, fakeworld.Ready, "mac-adapter")
	r.a.signer, r.a.platform, r.a.onboarded = &fakeSigner{}, "darwin", true
	r.a.adapter = func(context.Context) error { return errors.New("cancelled") }
	r.until("macOS adapter notice", func() bool { return r.tt.HasText("还没装网卡") })
	r.click("还没装网卡")
	r.until("macOS administrator explanation", func() bool {
		return r.tt.HasText("系统会请你输入开机密码或使用触控 ID，允许 Deneb 安装系统网卡。")
	})
	if r.tt.HasText("Windows 会弹窗问你，点「是」就行。") {
		t.Fatal("macOS asks with Windows instructions")
	}
	r.settle(300 * time.Millisecond)
	r.click("装网卡")
	r.until("cancellation permits retry", func() bool {
		return r.tt.HasText("没装上可以先在 Deneb 里点「打开」访问，想装时再试一次")
	})
}
