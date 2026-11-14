package hostmetrics

import (
	"fmt"
	"unsafe"

	"github.com/kittors/deneb/proto"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var kernel = windows.NewLazySystemDLL("kernel32.dll")
var getTimes = kernel.NewProc("GetSystemTimes")
var getMemory = kernel.NewProc("GlobalMemoryStatusEx")
var getUptime = kernel.NewProc("GetTickCount64")
var getDrives = kernel.NewProc("GetLogicalDrives")
var getDriveType = kernel.NewProc("GetDriveTypeW")

type memoryStatus struct {
	Length, Load                                                                         uint32
	Total, Available, PageTotal, PageAvailable, VirtualTotal, VirtualAvailable, Extended uint64
}

func readHost(m *proto.DeviceMetrics) (c counters, ok bool) {
	var idle, kernelTime, user windows.Filetime
	if r, _, _ := getTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernelTime)), uintptr(unsafe.Pointer(&user))); r != 0 {
		ticks := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
		c.total = ticks(kernelTime) + ticks(user)
		c.busy = c.total - ticks(idle)
		ok = true
	} else {
		m.Errors = append(m.Errors, "CPU计数不可用")
	}
	var memory memoryStatus
	memory.Length = uint32(unsafe.Sizeof(memory))
	if r, _, _ := getMemory.Call(uintptr(unsafe.Pointer(&memory))); r != 0 && memory.Total > 0 && memory.Available <= memory.Total {
		m.Memory = &proto.HostMemory{Total: memory.Total, Available: memory.Available}
	} else {
		m.Errors = append(m.Errors, "内存统计不可用")
	}
	if key, e := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); e == nil {
		m.CPUModel, _, _ = key.GetStringValue("ProcessorNameString")
		key.Close()
	}
	v := windows.RtlGetVersion()
	m.OSVersion = fmt.Sprintf("Windows %d.%d (build %d)", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	ticks, _, _ := getUptime.Call()
	up := uint64(ticks) / 1000
	m.Uptime = &up
	drives, _, _ := getDrives.Call()
	for i := 0; i < 26; i++ {
		if drives&(1<<i) == 0 {
			continue
		}
		name := fmt.Sprintf("%c:\\", 'A'+i)
		ptr, e := windows.UTF16PtrFromString(name)
		if e != nil {
			continue
		}
		kind, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(ptr)))
		if kind != 3 {
			continue
		}
		var free, total, all uint64
		if windows.GetDiskFreeSpaceEx(ptr, &free, &total, &all) == nil && total > 0 {
			m.Disks = append(m.Disks, proto.HostDisk{Name: name, Total: total, Available: free})
		}
	}
	if len(m.Disks) == 0 {
		m.Errors = append(m.Errors, "磁盘统计不可用")
	}
	return
}
