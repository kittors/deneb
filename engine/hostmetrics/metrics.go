// Package hostmetrics reads local operating-system counters, never processes,
// file contents, network mounts or disk serials. Sampling does no active work.
package hostmetrics

import (
	"runtime"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

type counters struct{ busy, total uint64 }
type Sampler struct {
	mu           sync.Mutex
	previous     counters
	havePrevious bool
}

func (s *Sampler) Sample() proto.DeviceMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := proto.DeviceMetrics{At: time.Now().UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	c, ok := readHost(&m)
	if ok {
		if s.havePrevious && c.total > s.previous.total && c.busy >= s.previous.busy {
			n := float64(c.busy-s.previous.busy) / float64(c.total-s.previous.total)
			if n >= 0 && n <= 1 {
				m.CPU = &n
			}
		}
		s.previous = c
		s.havePrevious = true
	}
	return m
}
