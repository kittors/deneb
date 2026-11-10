package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kittors/deneb/proto"
)

// Health: what each sync tells the control server about the relay's load
// (the plan's §16), measured over the time since the last one.

// healthMeter keeps what the last measure saw.
type healthMeter struct {
	start    time.Time
	at       time.Time
	bytes    int64         // everything carried, at the last measure
	cpu      time.Duration // CPU time used, at the last measure
	cpus     float64       // CPUs it may use
	memLimit int64
}

func newHealthMeter(now time.Time) *healthMeter {
	return &healthMeter{start: now, at: now, cpu: cpuTime(), cpus: cpuQuota(), memLimit: memoryLimit()}
}

// measure is the relay's health now; bytes is all it carried so far.
func (h *healthMeter) measure(now time.Time, bytes int64, connections int) proto.RelayHealth {
	elapsed := now.Sub(h.at).Seconds()
	cpu := cpuTime()
	out := proto.RelayHealth{
		Connections: connections,
		Memory:      memoryUsed(),
		MemoryLimit: h.memLimit,
		Uptime:      int64(now.Sub(h.start).Seconds()),
	}
	if elapsed > 0 {
		out.BytesPerSec = int64(float64(bytes-h.bytes) / elapsed)
		out.CPU = min(max((cpu-h.cpu).Seconds()/elapsed/h.cpus, 0), 1)
	}
	h.at, h.bytes, h.cpu = now, bytes, cpu
	return out
}

// cpuTime is the CPU time the process used, user and system.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	tv := func(t syscall.Timeval) time.Duration {
		return time.Duration(t.Sec)*time.Second + time.Duration(t.Usec)*time.Microsecond
	}
	return tv(ru.Utime) + tv(ru.Stime)
}

// cpuQuota is the CPUs the relay may use: its container's quota (cgroup
// v2's cpu.max), or all of them.
func cpuQuota() float64 {
	n := float64(runtime.NumCPU())
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return n
	}
	f := strings.Fields(string(b))
	if len(f) != 2 || f[0] == "max" {
		return n
	}
	quota, err1 := strconv.ParseFloat(f[0], 64)
	period, err2 := strconv.ParseFloat(f[1], 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return n
	}
	return min(quota/period, n)
}

// memoryUsed is the memory the process holds: its resident set on Linux,
// what the Go runtime holds from the system elsewhere.
func memoryUsed() int64 {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 1 {
			if pages, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				return pages * int64(os.Getpagesize())
			}
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Sys - m.HeapReleased)
}

// memoryLimit is the container's memory limit (cgroup v2, then v1), or
// zero for none.
func memoryLimit() int64 {
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || v <= 0 || v >= 1<<60 { // "max", or v1's "no limit"
			return 0
		}
		return v
	}
	return 0
}

// watch waits on the control server for changes and has the relay sync
// at each, so a kick or a relay taken out of a LAN closes connections at
// once rather than at the next minute's sync.
func (r *relay) watch(ctx context.Context) {
	var since uint64
	// Its own client: the server holds the request up to half a minute.
	c := &http.Client{Transport: r.ctl.hc.Transport}
	for ctx.Err() == nil {
		v, err := r.watchOnce(ctx, c, since)
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if v != since {
			if since != 0 {
				r.nudge()
			}
			since = v
		}
	}
}

func (r *relay) watchOnce(ctx context.Context, c *http.Client, since uint64) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s%s?since=%d", r.ctl.base, proto.PathRelayWatch, since), nil)
	if err != nil {
		return 0, err
	}
	r.ctl.mu.Lock()
	req.Header.Set("Authorization", "Bearer "+r.ctl.secret)
	r.ctl.mu.Unlock()
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("watch: %s", resp.Status)
	}
	var w proto.RelayWatchResult
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return 0, err
	}
	return w.Version, nil
}

// nudge asks the sync loop for a sync now.
func (r *relay) nudge() {
	select {
	case r.syncNow <- struct{}{}:
	default:
	}
}
