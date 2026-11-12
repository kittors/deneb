package main

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// M7 on the live store (DESIGN §6.15): what only a real engine does, which
// the demo cannot: answer late, say no, log this computer out on its own.

// m7LiveRig is the window signed in on the fake world, as the real one is.
func m7LiveRig(t *testing.T, name string) *liveRig {
	r := newLiveRig(t, fakeworld.Ready, name)
	r.a.signer, r.a.onboarded = &fakeSigner{}, true
	r.until("the window", func() bool {
		return r.a.snap.LoggedIn && r.a.snap.Loaded && r.a.self != "" && r.a.device(r.a.self) != nil
	})
	r.settle(300 * time.Millisecond)
	return r
}

// deleteThisComputer opens this computer's card, "更多", and clicks
// 删除这台电脑, scrolling the card to it.
func (r *liveRig) deleteThisComputer() {
	r.t.Helper()
	p := r.at(r.a.device(r.a.self))
	r.tt.ClickAt(p.X, p.Y)
	r.until("its card", func() bool { return r.a.selected == r.a.self && r.a.cardAt.W > 0 })
	r.settle(400 * time.Millisecond)
	r.click("更多")
	r.until("its more", func() bool { return r.tt.HasText("删除这台电脑") })
	at := r.a.cardAt
	for i := 0; i < 40; i++ {
		if b, ok := r.tt.Find("删除这台电脑"); ok && b.Y+b.H < 740-12 {
			break
		}
		r.tt.Wheel(at.X+at.W/2, at.Y+at.H/2, 0, 20)
		r.settle(16 * time.Millisecond)
	}
	r.settle(300 * time.Millisecond)
	r.click("删除这台电脑")
	r.until("the toast", func() bool { return r.tt.HasText("这台电脑会被删掉") })
}

// TestM7SelfDeleteRefusedLive: the server says no to deleting this
// computer: the window, faded to its background, comes back as it was,
// and the toast says why; nothing signs out.
func TestM7SelfDeleteRefusedLive(t *testing.T) {
	r := m7LiveRig(t, "m7-self-refused")
	r.w.E.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		return nil, &apitype.Error{Code: apitype.ErrForbidden, Message: "服务器不让删这台电脑。"}
	})
	r.deleteThisComputer()
	down := false
	r.until("back, with the server's words", func() bool {
		down = down || r.a.curtain != nil && r.a.curtain.done
		return down && r.a.curtain == nil && r.tt.HasText("服务器不让删这台电脑")
	})
	if r.a.onboard != nil || !r.a.snap.LoggedIn || r.a.device(r.a.self) == nil {
		t.Errorf("refused: the sign-in came, or this computer went: onboard %v, logged in %v", r.a.onboard != nil, r.a.snap.LoggedIn)
	}
}

// TestM7SelfDeleteWindowClosedLive: the window closes to the menu bar
// while the toast still offers undo: what it held goes now, with no
// window to fade (systray.go hands it no frame).
func TestM7SelfDeleteWindowClosedLive(t *testing.T) {
	r := m7LiveRig(t, "m7-self-closed")
	var mu sync.Mutex
	var sent []string
	r.w.E.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		mu.Lock()
		sent = append(sent, op.Op)
		mu.Unlock()
		return nil, nil
	})
	r.deleteThisComputer()
	// As the window's close does (menuBar.attach).
	if tn := r.a.toast; tn != nil && tn.commit != nil {
		tn.commit(nil)
	}
	r.a.toast = nil
	r.until("device.delete sent", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(sent, "device.delete")
	})
	if r.a.curtain != nil {
		t.Error("closed: the window faded with no frame to fade in")
	}
}

// TestM7DeleteAccountLive: the server logs this computer out as it
// deletes the account, and the engine answers only later (it waits for a
// version that never comes): the page stays as it was, and the window goes
// to the sign-in, which says so, without waiting for the engine.
func TestM7DeleteAccountLive(t *testing.T) {
	r := m7LiveRig(t, "m7-account")
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	r.w.E.HandleOps(func(op apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
		if op.Op != "account.delete" {
			return nil, nil
		}
		var a proto.OpArgs
		json.Unmarshal(op.Args, &a)
		res, _ := json.Marshal(proto.OpOutput{Account: &proto.AccountDeletion{DryRun: a.DryRun,
			Devices: []proto.DeviceID{fakeworld.ThisMac}, LANs: []proto.LANID{}, Left: []proto.LANID{}}})
		if a.DryRun {
			return &apitype.OpResult{Result: res}, nil
		}
		if a.Confirm != "kittors@example.com" {
			return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "要注销，请再输一遍你的邮箱"}
		}
		r.w.E.Update(func(s *apitype.State) {
			s.LoggedIn, s.User, s.SelfDeviceID, s.Peers, s.Paths, s.Scopes, s.UserView = false, "", "", nil, nil, nil, nil
		})
		<-release
		return &apitype.OpResult{Result: res}, nil
	})
	r.click("账号")
	r.until("the account card", func() bool { return r.tt.HasText("注销账号") && r.tt.HasText("kittors@example.com") })
	r.settle(300 * time.Millisecond)
	r.click("注销账号")
	r.until("what goes", func() bool { return r.a.deleting != nil && r.a.deleting.acc != nil })
	r.settle(400 * time.Millisecond)
	field, ok := r.tt.Find("你的邮箱")
	if !ok {
		t.Fatalf("no email field: %q", r.tt.Texts())
	}
	r.tt.ClickAt(field.X+field.W/2, field.Y+field.H/2)
	r.settle(100 * time.Millisecond)
	r.tt.Type("kittors@example.com")
	r.settle(300 * time.Millisecond)
	r.tt.ClickAt(590, field.Y+field.H/2+23+14+22)
	r.until("deleting", func() bool { return r.a.deleting != nil && r.a.deleting.sending })
	r.until("the sign-in, saying so, the engine not answered yet", func() bool {
		return r.a.deleting == nil && r.a.curtain == nil && r.tt.HasText("账号已经注销") && r.tt.HasText("发验证码")
	})
	close(release)
	r.settle(800 * time.Millisecond)
	if !r.tt.HasText("账号已经注销") || r.a.deleting != nil || r.a.onboard == nil {
		t.Errorf("once the engine answered: %q", r.tt.Texts())
	}
}
