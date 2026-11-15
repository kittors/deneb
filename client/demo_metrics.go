package client

// What My Devices shows of the demo's devices of mine (M7): load, memory,
// storage and the round trip, as an engine reports them every 15 s.

import (
	"time"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

func demoMetrics() map[model.DeviceID]proto.DeviceMetrics {
	gb := func(f float64) uint64 { return uint64(f * (1 << 30)) }
	f := func(v float64) *float64 { return &v }
	u := func(v uint64) *uint64 { return &v }
	now := time.Now()
	return map[model.DeviceID]proto.DeviceMetrics{
		"mac": {At: now, OS: "macos", Arch: "arm64", OSVersion: "macOS 26.1", CPUModel: "Apple M3 Pro", CPUs: 12, CPU: f(0.18),
			Memory: &proto.HostMemory{Total: gb(36), Available: gb(14.2)}, Disks: []proto.HostDisk{{Name: "Macintosh HD", Total: gb(926), Available: gb(412)}},
			Uptime: u(3*86400 + 6*3600), EngineVersion: "0.1.8"},
		"game": {At: now, OS: "windows", Arch: "amd64", OSVersion: "Windows 11", CPUModel: "AMD Ryzen 7 7800X3D", CPUs: 16, CPU: f(0.07),
			Memory: &proto.HostMemory{Total: gb(32), Available: gb(21.6)}, Disks: []proto.HostDisk{{Name: "C:", Total: gb(1862), Available: gb(1144)}},
			Uptime: u(9 * 3600), EngineVersion: "0.1.8"},
		"studio": {At: now, OS: "macos", Arch: "arm64", OSVersion: "macOS 15.6", CPUModel: "Apple M2", CPUs: 8, CPU: f(0.31),
			Memory: &proto.HostMemory{Total: gb(16), Available: gb(6.1)}, Disks: []proto.HostDisk{{Name: "Macintosh HD", Total: gb(460), Available: gb(201)}},
			Uptime: u(12*86400 + 2*3600), EngineVersion: "0.1.8"},
		"mynas": {At: now, OS: "linux", Arch: "amd64", OSVersion: "Debian 12", CPUModel: "Intel N100", CPUs: 4, CPU: f(0.05),
			Memory: &proto.HostMemory{Total: gb(8), Available: gb(5.2)}, Disks: []proto.HostDisk{{Name: "/srv", Total: gb(7452), Available: gb(3120)}},
			Uptime: u(41*86400 + 5*3600), EngineVersion: "0.1.8"},
	}
}

func demoLatencies() map[model.DeviceID]model.LatencySample {
	now := time.Now()
	return map[model.DeviceID]model.LatencySample{
		"game":   {At: now, RTT: 3200 * time.Microsecond},
		"studio": {At: now, RTT: 14100 * time.Microsecond},
		"mynas":  {At: now, RTT: 4300 * time.Microsecond},
	}
}

// RefreshDeviceMetrics stands in for the engines' sampling: the numbers
// are new again (My Devices opening, and its 刷新).
func (d *Demo) RefreshDeviceMetrics() {
	d.update(func(s *model.Snapshot) *model.Snapshot {
		n := *s
		n.DeviceMetrics, n.DeviceLatencies = demoMetrics(), demoLatencies()
		return &n
	})
}

// MonitorDevices is My Devices opening or closing: open, the numbers are
// fresh.
func (d *Demo) MonitorDevices(on bool) {
	if on {
		d.RefreshDeviceMetrics()
	}
}
