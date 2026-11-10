package control

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
)

// The server watches itself and the public relays, and tells the operator
// when something goes wrong (design/plan/m7/control-ops.md §3): a public
// relay offline or full, the backups failing or stale, the disk filling,
// memory running high, devices dropping off at once. Whatever it cannot
// see itself, being down above all, the watchdog on the other machine
// says (cmd/deneb-watchdog).

const (
	healthEvery         = time.Minute
	relayFullAlertAfter = 10 * time.Minute
	backupStaleAfter    = 26 * time.Hour
	// The disk under the data directory is low below 15% free, and fine
	// again from 20%; memory high above 200 MB (the container may have
	// 256), fine again below 180.
	diskLowFree  = 0.15
	diskOKFree   = 0.20
	memoryHigh   = 200 << 20
	memoryOK     = 180 << 20
	dropWindow   = 5 * time.Minute
	dropAtLeast  = 4 // fewer devices online than this were never "many"
	healthKeyFmt = "%s/%s"
)

// health is what the checks remember between rounds.
type health struct {
	// round is held through a round of checks: one at a time.
	round     sync.Mutex
	fullSince map[proto.RelayID]time.Time
	online    []onlineSample
	started   bool

	// mu guards backupErr, which RunBackups sets: the last backup's error,
	// nil once one works.
	mu        sync.Mutex
	backupErr error

	// Tests replace these.
	memory func() int64
	disk   func(path string) (free, total uint64, err error)
}

type onlineSample struct {
	at time.Time
	n  int
}

func newHealth() *health {
	return &health{fullSince: map[proto.RelayID]time.Time{}, memory: memoryUsed, disk: diskFree}
}

// Notify tells the operator something once, unless the same key went out
// within the cooldown: for the command's own events, such as a certificate
// that would not renew.
func (s *Server) Notify(key, title, text string) { s.alerts.Notify(key, title, text) }

// Alerts are the problems firing now and the last alerts, for the admin
// socket.
func (s *Server) Alerts() ([]alert.Alert, []alert.Record) {
	return s.alerts.Active(), s.alerts.Recent()
}

// checkHealth is one round of the checks.
func (s *Server) checkHealth() {
	h := s.health
	h.round.Lock()
	defer h.round.Unlock()
	h.mu.Lock()
	backupErr := h.backupErr
	h.mu.Unlock()
	now := s.now()
	st := s.state.Load()
	w := st.w
	isOnline := s.hub.onlineSnapshot()
	devices := 0
	for _, d := range w.devices {
		if isOnline(d.ID) {
			devices++
		}
	}
	if !h.started {
		h.started = true
		s.alerts.Notify("started", "控制服务器启动了",
			fmt.Sprintf("%d 个人、%d 台设备、%d 个中转站；数据版本 %d", len(w.people), len(w.devices), len(w.relays), st.v))
	}

	// The public relays: everyone's fallback, so the operator's to watch.
	var ids []proto.RelayID
	for id, rl := range w.relays {
		if rl.Source == proto.RelayPublic && ready(rl) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		name := "公共中转站「" + w.relays[id].Name + "」"
		key := fmt.Sprintf(healthKeyFmt, "relay-offline", id)
		if s.relayLive.offline(id) {
			_, seen := s.relayLive.status(id)
			text := "从没同步过"
			if !seen.IsZero() {
				text = alert.Clock(seen) + " 以后就没有再同步"
			}
			s.alerts.Fire(key, name+"连不上了", text+"；经它的设备会自己改走别的中转站")
		} else {
			s.alerts.Resolve(key, name+"恢复了", "又在同步了")
		}
		key = fmt.Sprintf(healthKeyFmt, "relay-full", id)
		if s.relayLive.isFull(id) {
			since, ok := h.fullSince[id]
			if !ok {
				since = now
				h.fullSince[id] = now
			}
			if now.Sub(since) >= relayFullAlertAfter {
				text := fmt.Sprintf("已经满了 %d 分钟，新连上来的设备先走别的中转站", int(now.Sub(since)/time.Minute))
				if ld := s.relayLive.lastLoad(id); ld != nil {
					text += fmt.Sprintf("；现在 %d 台设备，CPU %.0f%%", ld.devices, ld.health.CPU*100)
				}
				s.alerts.Fire(key, name+"满了", text)
			}
		} else {
			delete(h.fullSince, id)
			s.alerts.Resolve(key, name+"不满了", "")
		}
	}

	// The backups.
	if backupErr != nil {
		s.alerts.Fire("backup-failed", "每天的备份写不出来", backupErr.Error())
	} else {
		s.alerts.Resolve("backup-failed", "备份又写得出来了", "")
	}
	if bs, err := s.backups(); err == nil {
		switch {
		case len(bs) == 0:
			s.alerts.Fire("backup-stale", "还没有一份备份", "数据目录下的 "+BackupDir+" 里是空的")
		case now.Sub(bs[0].Written) >= backupStaleAfter:
			s.alerts.Fire("backup-stale", "超过 26 小时没有新的备份", "最新的一份是 "+bs[0].Name+"，"+alert.Clock(bs[0].Written)+" 写的")
		default:
			s.alerts.Resolve("backup-stale", "又有新的备份了", bs[0].Name)
		}
	}

	// The disk and the memory.
	if free, total, err := h.disk(s.cfg.DataDir); err == nil && total > 0 {
		share := float64(free) / float64(total)
		text := fmt.Sprintf("只剩 %.0f%%（%s）", share*100, sizeOf(int64(free)))
		switch {
		case share < diskLowFree:
			s.alerts.Fire("disk-low", "数据目录所在的磁盘快满了", text)
		case share >= diskOKFree:
			s.alerts.Resolve("disk-low", "磁盘空间够了", fmt.Sprintf("还剩 %.0f%%", share*100))
		}
	}
	switch m := h.memory(); {
	case m > memoryHigh:
		s.alerts.Fire("memory-high", "控制服务器的内存用得太多", fmt.Sprintf("现在 %s；容器给它 256 MB，到了会被杀掉重启", sizeOf(m)))
	case m < memoryOK:
		s.alerts.Resolve("memory-high", "控制服务器的内存降下来了", "现在 "+sizeOf(m))
	}

	// Devices dropping off at once: the server's or the network's trouble
	// more likely than theirs.
	h.online = append(h.online, onlineSample{now, devices})
	for len(h.online) > 0 && now.Sub(h.online[0].at) > dropWindow {
		h.online = h.online[1:]
	}
	most := 0
	for _, o := range h.online {
		most = max(most, o.n)
	}
	if most >= dropAtLeast && devices*2 <= most {
		s.alerts.Notify("devices-dropped", "在线设备一下子少了很多",
			fmt.Sprintf("%d 分钟内从 %d 台掉到 %d 台：可能是服务器或网络出了事", int(dropWindow/time.Minute), most, devices))
	}

	s.alerts.Tick()
}

// sizeOf is a byte count as people read it.
func sizeOf(b int64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GB"
	case b >= 1<<20:
		return strconv.FormatInt(b>>20, 10) + " MB"
	}
	return strconv.FormatInt(b>>10, 10) + " KB"
}

// memoryUsed is the process's resident memory: what a container's limit
// counts (Linux), or what Go holds from the system elsewhere.
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
