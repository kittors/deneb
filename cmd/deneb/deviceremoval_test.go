package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

func TestLANCardNeverOffersAccountDeviceDeletion(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d := client.NewDemo()
		d.Manual()
		a := newApp(d, demoPlaces())
		a.selected = "game"
		a.more = true
		tt := ui.NewTester(a.view, 1180, 740)
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(700 * time.Millisecond)
		if !tt.HasText("移出局域网") {
			t.Fatal("positive control: LAN removal is missing")
		}
		if tt.HasText("删除这台设备") || tt.HasText("删除这台电脑") {
			t.Fatal("LAN card exposes account-wide device deletion")
		}
	}
}
func TestAccountDeviceDeletionLivesOnMyDevices(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	a.myDevicesOpen = true
	a.deviceDetails = "game"
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText("CPU") {
		t.Fatal("positive control: device page not rendered")
	}
	if !tt.HasText("删除这台设备") {
		t.Fatal("my devices has no account-level deletion")
	}
}

func TestLANRemovalKeepsIdentityAndOtherLAN(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	_, err := d.Do(t.Context(), model.Op{Kind: model.OpLANAddDevice, LAN: "work", Device: "game"})
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(d, demoPlaces())
	a.selected = "game"
	tt := ui.NewTester(a.view, 1180, 740)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	original := *d.Snapshot().Device("game")
	services := d.Snapshot().LAN("work").Services["game"]
	if err := tt.Click("移出局域网"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if !d.Snapshot().LAN("weekend").Has("game") || a.confirming == nil {
		t.Fatal("removal happened before confirmation")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if !d.Snapshot().LAN("weekend").Has("game") {
		t.Fatal("cancel removed device")
	}
	if err := tt.Click("移出局域网"); err != nil {
		t.Fatal(err)
	}
	acceptConfirmation(t, tt, "移出局域网")
	tt.Advance(700 * time.Millisecond)
	s := d.Snapshot()
	got := s.Device("game")
	if s.LAN("weekend").Has("game") || !s.LAN("work").Has("game") || got == nil || got.ID != original.ID || got.Addr != original.Addr || got.Owner != original.Owner {
		t.Fatal("LAN removal affected account identity or another LAN")
	}
	if !reflect.DeepEqual(services, s.LAN("work").Services["game"]) || !reflect.DeepEqual(original.Catalog, got.Catalog) {
		t.Fatal("LAN removal changed services")
	}
	if err := tt.Click("撤销"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	if !d.Snapshot().LAN("weekend").Has("game") || !d.Snapshot().LAN("work").Has("game") {
		t.Fatal("undo did not restore only the removed LAN")
	}
}

// This store records the actual boundary calls; optimistic UI changes cannot
// masquerade as a committed deletion. Demo data is never a real device.
type deviceRemovalStore struct {
	*client.Demo
	calls  []model.Op
	refuse bool
}

func (s *deviceRemovalStore) Do(ctx context.Context, op model.Op) (model.Result, error) {
	s.calls = append(s.calls, op)
	if s.refuse {
		return model.Result{}, &model.Error{Message: "服务器拒绝删除测试设备"}
	}
	return s.Demo.Do(ctx, op)
}
func deviceDeletionRig(t *testing.T, id model.DeviceID, dark bool) (*app, *deviceRemovalStore, *ui.Tester) {
	t.Helper()
	d := client.NewDemo()
	d.Manual()
	if id == "mynas" {
		d.Snapshot().Device(id).Online = false
		d.Snapshot().Device(id).Name = "离线且未加入任何网络的设备（长名称布局验证）"
	}
	s := &deviceRemovalStore{Demo: d}
	a := newApp(s, demoPlaces())
	a.myDevicesOpen = true
	a.deviceDetails = id
	tt := ui.NewTester(a.view, 980, 640)
	tt.SetDark(dark)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	return a, s, tt
}
func clickVisibleDeviceAction(t *testing.T, tt *ui.Tester, label string, viewportHeight float32) {
	t.Helper()
	for i := 0; i < 60; i++ {
		b, ok := tt.Find(label)
		if ok && b.Y >= 100 && b.Y+b.H < viewportHeight-12 {
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			return
		}
		tt.Wheel(850, 460, 0, 40)
		tt.Frame()
		tt.Advance(32 * time.Millisecond)
	}
	t.Fatalf("action %q is not accessible in viewport: %v", label, tt.Texts())
}
func TestMyDevicesDeletionCancelUndoAndCommit(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, id := range []model.DeviceID{"game", "mynas"} {
			t.Run(string(id)+map[bool]string{true: "-dark", false: "-light"}[dark], func(t *testing.T) {
				a, s, tt := deviceDeletionRig(t, id, dark)
				original := *s.Snapshot().Device(id)
				clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
				tt.Advance(300 * time.Millisecond)
				if a.confirming == nil || a.confirming.target != original.Name || len(s.calls) != 0 {
					t.Fatal("delete was not confirmed before boundary call")
				}
				if !tt.HasText("将从所有局域网移除，收回共享并让设备登出。重新添加后地址会改变。") {
					t.Fatal("confirmation hides impact")
				}
				tt.Key(0, ui.KeyEnter)
				tt.Frame()
				tt.Advance(300 * time.Millisecond)
				if a.confirming != nil || len(s.calls) != 0 || a.snap.Device(id) == nil {
					t.Fatal("default Enter did not cancel")
				}
				clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
				tt.Advance(300 * time.Millisecond)
				tt.Key(0, ui.KeyEscape)
				tt.Frame()
				tt.Advance(300 * time.Millisecond)
				if a.confirming != nil || len(s.calls) != 0 || a.snap.Device(id) == nil {
					t.Fatal("Escape did not cancel")
				}
				clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
				acceptConfirmation(t, tt, "删除设备")
				tt.Advance(500 * time.Millisecond)
				if a.snap.Device(id) != nil || s.Snapshot().Device(id) == nil || len(s.calls) != 0 {
					t.Fatal("delete did not remain held")
				}
				b, ok := tt.Find("撤销")
				if !ok || b.Y < 0 || b.Y+b.H > 640 || b.X < sidebarWidth {
					t.Fatalf("page undo toast not visible: %v %v", b, ok)
				}
				if err := tt.Click("撤销"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				tt.Advance(400 * time.Millisecond)
				if a.snap.Device(id) == nil || !reflect.DeepEqual(*s.Snapshot().Device(id), original) || len(s.calls) != 0 || len(a.pend) != 0 {
					t.Fatal("undo did not restore device without request")
				}
				a.deviceDetails = id
				tt.Frame()
				tt.Advance(400 * time.Millisecond)
				clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
				acceptConfirmation(t, tt, "删除设备")
				tt.Move(300, 100)
				tt.Advance(6 * time.Second)
				tt.Frame()
				if len(s.calls) != 1 || s.calls[0].Kind != model.OpDeviceDelete || s.calls[0].Device != id || s.Snapshot().Device(id) != nil {
					t.Fatalf("page toast did not commit once: %+v", s.calls)
				}
				for _, l := range s.Snapshot().LANs {
					if l.Has(id) || l.HasPending(id) {
						t.Fatal("global deletion kept LAN membership")
					}
				}
				tt.Advance(6 * time.Second)
				tt.Frame()
				if len(s.calls) != 1 {
					t.Fatal("deletion repeated")
				}
			})
		}
	}
}
func TestMyDevicesDeletionRejectionRestoresDevice(t *testing.T) {
	a, s, tt := deviceDeletionRig(t, "game", true)
	s.refuse = true
	clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
	acceptConfirmation(t, tt, "删除设备")
	tt.Move(300, 100)
	tt.Advance(6 * time.Second)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	if len(s.calls) != 1 || a.snap.Device("game") == nil || !a.snap.LAN("weekend").Has("game") || !a.myDevicesOpen || !tt.HasText("服务器拒绝删除测试设备") {
		t.Fatalf("rejection not restored on page: %v %+v", tt.Texts(), s.calls)
	}
}
func TestMyDevicesDeletionStaleConfirmationCannotAct(t *testing.T) {
	for _, change := range []string{"page", "owner", "gone"} {
		t.Run(change, func(t *testing.T) {
			a, s, tt := deviceDeletionRig(t, "game", false)
			clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
			tt.Advance(300 * time.Millisecond)
			switch change {
			case "page":
				a.myDevicesOpen = false
			case "owner":
				a.snap.Device("game").Owner = "jie"
			case "gone":
				delete(a.snap.Devices, "game")
			}
			acceptConfirmation(t, tt, "删除设备")
			tt.Advance(6 * time.Second)
			tt.Frame()
			if len(s.calls) != 0 || len(a.pend) != 0 {
				t.Fatal("stale confirmation deleted device")
			}
		})
	}
}
func TestMyDevicesSelfDeletionUndo(t *testing.T) {
	a, s, tt := deviceDeletionRig(t, "mac", true)
	clickVisibleDeviceAction(t, tt, "删除这台电脑", 640)
	acceptConfirmation(t, tt, "删除设备")
	tt.Advance(400 * time.Millisecond)
	if !tt.HasText("这台电脑会被删掉") || !tt.HasText("撤销") || !a.snap.LoggedIn {
		t.Fatal("self deletion bypassed undo")
	}
	if err := tt.Click("撤销"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(6 * time.Second)
	tt.Frame()
	if len(s.calls) != 0 || a.snap.Device("mac") == nil || a.curtain != nil || !a.snap.LoggedIn {
		t.Fatal("self undo deleted device")
	}
}
func TestAllLANCardsNeverOfferDeviceDeletion(t *testing.T) {
	r := newAllRig(t)
	for _, id := range []string{"weekend/game", "work/studio", "weekend/mia", "mac"} {
		r.a.selected = id
		r.a.more = true
		r.tt.Frame()
		r.step(500)
		if !r.tt.HasText("更多") {
			t.Fatal("positive control: selected card is missing")
		}
		if r.tt.HasText("删除这台设备") || r.tt.HasText("删除这台电脑") {
			t.Fatalf("all LAN card %s offers global delete", id)
		}
	}
}

func TestMyDevicesDeletionCommitsOnceWhenReturningToLAN(t *testing.T) {
	a, s, tt := deviceDeletionRig(t, "game", false)
	clickVisibleDeviceAction(t, tt, "删除这台设备", 640)
	acceptConfirmation(t, tt, "删除设备")
	tt.Advance(300 * time.Millisecond)
	if err := tt.Click("周末联机"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	tt.Advance(6 * time.Second)
	tt.Frame()
	if a.myDevicesOpen || len(s.calls) != 1 || s.calls[0].Kind != model.OpDeviceDelete || s.Snapshot().Device("game") != nil {
		t.Fatalf("navigation lost or duplicated pending deletion: %+v", s.calls)
	}
}

func TestDeviceDeletionRecordingUsesMyDevices(t *testing.T) {
	r := newM7Rig(t)
	shots := 0
	r.onShot = func(state string) {
		shots++
		if !r.a.myDevicesOpen {
			t.Fatalf("recording %s left My Devices", state)
		}
	}
	playDeleteDevice(r)
	if shots < 5 || r.a.snap.Device("game") != nil || !r.tt.HasText("删除这台电脑") {
		t.Fatal("migrated deletion scene missed delete/undo/self entry")
	}
}
