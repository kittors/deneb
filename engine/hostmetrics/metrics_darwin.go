package hostmetrics

import (
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/kittors/deneb/proto"
	"golang.org/x/sys/unix"
)

var mach struct {
	once       sync.Once
	err        error
	hostSelf   func() uint32
	statistics func(uint32, int32, unsafe.Pointer, *uint32) int32
	release    func(uint32, uint32) int32
	taskSelf   uint32
}

func initMach() {
	h, e := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		mach.err = e
		return
	}
	names := []string{"mach_host_self", "host_statistics", "mach_port_deallocate", "mach_task_self_"}
	addresses := make([]uintptr, len(names))
	for i, n := range names {
		v, e := purego.Dlsym(h, n)
		if e != nil {
			mach.err = e
			return
		}
		addresses[i] = v
	}
	purego.RegisterFunc(&mach.hostSelf, addresses[0])
	purego.RegisterFunc(&mach.statistics, addresses[1])
	purego.RegisterFunc(&mach.release, addresses[2])
	// Dlsym returns a C data address. Copy through libc rather than turning a
	// stored uintptr into a Go pointer (invalid under the checkptr contract).
	copyAddress, err := purego.Dlsym(h, "memcpy")
	if err != nil {
		mach.err = err
		return
	}
	var copyWord func(*uint32, uintptr, uintptr) uintptr
	purego.RegisterFunc(&copyWord, copyAddress)
	copyWord(&mach.taskSelf, addresses[3], 4)
}
func readHost(m *proto.DeviceMetrics) (c counters, ok bool) {
	m.CPUModel, _ = unix.Sysctl("machdep.cpu.brand_string")
	version, _ := unix.Sysctl("kern.osproductversion")
	m.OSVersion = "macOS " + version
	total, _ := unix.SysctlUint64("hw.memsize")
	mach.once.Do(initMach)
	if mach.err == nil {
		host := mach.hostSelf()
		defer mach.release(mach.taskSelf, host)
		var cpu [4]uint32
		count := uint32(len(cpu))
		if mach.statistics(host, 3, unsafe.Pointer(&cpu[0]), &count) == 0 && count == 4 {
			c.busy = uint64(cpu[0]) + uint64(cpu[1]) + uint64(cpu[3])
			c.total = c.busy + uint64(cpu[2])
			ok = true
		}
		var vm [15]uint32
		count = uint32(len(vm))
		if total > 0 && mach.statistics(host, 2, unsafe.Pointer(&vm[0]), &count) == 0 && count >= 4 {
			// Free + inactive pages are reclaimable. This is OS available memory,
			// not Activity Monitor's application-memory or pressure percentage.
			available := (uint64(vm[0]) + uint64(vm[2])) * uint64(unix.Getpagesize())
			if available <= total {
				m.Memory = &proto.HostMemory{Total: total, Available: available}
			}
		}
	}
	if !ok {
		m.Errors = append(m.Errors, "CPU计数不可用")
	}
	if m.Memory == nil {
		m.Errors = append(m.Errors, "内存统计不可用")
	}
	if b, e := unix.SysctlRaw("kern.boottime"); e == nil && len(b) >= int(unsafe.Sizeof(unix.Timeval{})) {
		tv := *(*unix.Timeval)(unsafe.Pointer(&b[0]))
		seconds := time.Now().Unix() - tv.Sec
		if seconds >= 0 {
			u := uint64(seconds)
			m.Uptime = &u
		}
	}
	seen := map[[2]int32]bool{}
	n, e := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if e == nil {
		mounts := make([]unix.Statfs_t, n)
		n, e = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
		if e == nil {
			for _, st := range mounts[:n] {
				if len(m.Disks) >= 32 {
					break
				}
				name := unix.ByteSliceToString(st.Mntonname[:])
				fs := unix.ByteSliceToString(st.Fstypename[:])
				if fs != "apfs" && fs != "hfs" {
					continue
				}
				if name != "/" && name != "/System/Volumes/Data" && len(name) < 9 {
					continue
				}
				// APFS system/data volumes share a container. Show the writable data
				// volume once; omit system support/snapshot volumes and user paths.
				if fs == "apfs" && name != "/System/Volumes/Data" && (name == "/" || len(name) < 9 || name[:9] != "/Volumes/") {
					continue
				}
				label := name
				if name == "/System/Volumes/Data" {
					label = "Macintosh HD"
				}
				if st.Flags&unix.MNT_RDONLY != 0 || seen[st.Fsid.Val] {
					continue
				}
				if st.Blocks > 0 {
					seen[st.Fsid.Val] = true
					m.Disks = append(m.Disks, proto.HostDisk{Name: label, Total: st.Blocks * uint64(st.Bsize), Available: st.Bavail * uint64(st.Bsize)})
				}
			}
		}
	}
	if len(m.Disks) == 0 {
		m.Errors = append(m.Errors, "磁盘统计不可用")
	}
	return
}
