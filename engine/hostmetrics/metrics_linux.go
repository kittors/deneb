package hostmetrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/kittors/deneb/proto"
	"golang.org/x/sys/unix"
)

func readHost(m *proto.DeviceMetrics) (c counters, ok bool) {
	if b, e := os.ReadFile("/proc/stat"); e == nil {
		f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
		if len(f) >= 5 && f[0] == "cpu" {
			values := make([]uint64, 0, 8)
			for _, v := range f[1:min(len(f), 9)] {
				n, e := strconv.ParseUint(v, 10, 64)
				if e != nil {
					values = nil
					break
				}
				values = append(values, n)
			}
			if len(values) >= 4 {
				for _, v := range values {
					c.total += v
				}
				idle := values[3]
				if len(values) > 4 {
					idle += values[4]
				}
				c.busy = c.total - idle
				ok = true
			}
		}
	}
	if !ok {
		m.Errors = append(m.Errors, "CPU计数不可用")
	}
	if b, e := os.ReadFile("/proc/cpuinfo"); e == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(b)))
		for scanner.Scan() {
			k, v, yes := strings.Cut(scanner.Text(), ":")
			if yes && (strings.TrimSpace(k) == "model name" || strings.TrimSpace(k) == "Hardware") {
				m.CPUModel = strings.TrimSpace(v)
				break
			}
		}
	}
	if b, e := os.ReadFile("/etc/os-release"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, yes := strings.CutPrefix(line, "PRETTY_NAME="); yes {
				m.OSVersion = strings.Trim(v, "\"")
				break
			}
		}
	}
	if b, e := os.ReadFile("/proc/meminfo"); e == nil {
		values := map[string]uint64{}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				v, e := strconv.ParseUint(f[1], 10, 64)
				if e == nil {
					values[strings.TrimSuffix(f[0], ":")] = v * 1024
				}
			}
		}
		if total := values["MemTotal"]; total > 0 {
			available, yes := values["MemAvailable"]
			if yes && available <= total {
				m.Memory = &proto.HostMemory{Total: total, Available: available}
			}
		}
	}
	if m.Memory == nil {
		m.Errors = append(m.Errors, "内存统计不可用")
	}
	if b, e := os.ReadFile("/proc/uptime"); e == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			v, e := strconv.ParseFloat(f[0], 64)
			if e == nil && v >= 0 {
				u := uint64(v)
				m.Uptime = &u
			}
		}
	}
	// Only local fixed filesystems. Reading network/FUSE mounts can block and
	// pseudo/container overlay mounts would double-count the same host storage.
	seen := map[uint64]bool{}
	if b, e := os.ReadFile("/proc/mounts"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || len(m.Disks) >= 32 {
				continue
			}
			switch f[2] {
			case "ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "overlay":
			default:
				continue
			}
			mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`).Replace(f[1])
			if f[2] == "overlay" && mount != "/" {
				continue
			}
			info, e := os.Stat(mount)
			if e != nil || !info.IsDir() {
				continue
			} // Docker file bind mounts are not disks.
			var st unix.Statfs_t
			if unix.Statfs(mount, &st) != nil || st.Blocks == 0 {
				continue
			}
			var device unix.Stat_t
			if unix.Stat(mount, &device) != nil || seen[uint64(device.Dev)] {
				continue
			}
			seen[uint64(device.Dev)] = true
			m.Disks = append(m.Disks, proto.HostDisk{Name: mount, Total: st.Blocks * uint64(st.Bsize), Available: st.Bavail * uint64(st.Bsize)})
		}
	}
	if len(m.Disks) == 0 {
		m.Errors = append(m.Errors, "磁盘统计不可用")
	}
	return
}
