package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

func TestCurrentUpdateStaysInSettings(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	a.updates = newAppUpdater(a)
	a.updates.stage = updateCurrent
	a.updates.check = func(context.Context) (*appRelease, error) { return nil, nil }
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	if _, ok := tt.Find("已是最新版本"); ok {
		t.Fatal("current version still has a sidebar update entry")
	}
	a.updates.click()
	tt.Frame()
	for i := 0; i < 100 && a.updates.stage == updateChecking; i++ {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	tt.Advance(500 * time.Millisecond)
	if a.updates.open {
		t.Fatal("current version still opens a blocking dialog")
	}
	if !a.settingsOpen || !tt.HasText("已是最新版本") {
		t.Fatalf("check result not visible in settings: %v", tt.Texts())
	}
}
func TestMyDevicesIsIndependentPage(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Click("我的设备")
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	if _, ok := tt.Find("关闭我的设备"); ok {
		t.Fatal("my devices still opens a dialog")
	}
	if !tt.HasText("CPU") || !tt.HasText("内存") || !tt.HasText("存储") || !tt.HasText("延迟") {
		t.Fatalf("device metrics missing: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !a.myDevicesOpen {
		t.Fatal("Escape dismissed the page")
	}
	lan := a.snap.LAN(a.lan)
	if lan == nil {
		t.Fatal("no selected LAN")
	}
	tt.Click(lan.Name)
	tt.Frame()
	if a.myDevicesOpen {
		t.Fatal("cannot return to selected LAN")
	}
}

func TestDeviceMonitorZeroOfflineDetailsAndEdit(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d := client.NewDemo()
		d.Manual()
		a := newApp(d, demoPlaces())
		id := d.Snapshot().Mine[0]
		cpu := float64(0)
		up := uint64(0)
		a.snap.DeviceMetrics = map[model.DeviceID]proto.DeviceMetrics{id: {At: time.Now(), CPUs: 8, CPU: &cpu, Arch: "arm64", OSVersion: "macOS", CPUModel: "Apple M4", Memory: &proto.HostMemory{Total: 16 << 30, Available: 0}, Disks: []proto.HostDisk{{Name: "System", Total: 512 << 30, Available: 0}}, Uptime: &up}}
		a.myDevicesOpen = true
		tt := ui.NewTester(a.view, 980, 640)
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		if !tt.HasText("0%") || !tt.HasText("0 B") {
			t.Fatalf("zero is unavailable: %v", tt.Texts())
		}
		name := a.snap.Device(id).Name
		if err := tt.Click("详情" + name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		if !tt.HasText("Apple M4") || !tt.HasText("8 核 · arm64") {
			t.Fatal("configuration not in details")
		}
		if err := tt.Click("重命名" + name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		if a.renaming == nil {
			t.Fatal("rename no longer available")
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		if !a.myDevicesOpen || a.renaming != nil {
			t.Fatal("closing edit lost the page")
		}
	}
}

func TestMyDevicesLatencyStates(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	host := &model.Device{ID: "remote", Online: true}
	if value, state := a.deviceLatency(host); value != "—" || state != "无共同网络" {
		t.Fatal(value, state)
	}
	a.snap.Paths[host.ID] = model.Path{Kind: model.PathDirect, RTT: 17 * time.Millisecond}
	if value, state := a.deviceLatency(host); value != "17.0 ms" || state != "直连" {
		t.Fatal(value, state)
	}
	host.Online = false
	if value, state := a.deviceLatency(host); value != "—" || state != "离线" {
		t.Fatal("offline displays last RTT as current")
	}
}
