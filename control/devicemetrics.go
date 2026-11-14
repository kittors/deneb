package control

import (
	"net/http"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// Host samples are volatile. They do not bump the topology version, write to
// SQLite or enter peer/user views. Authorization is checked on every read.
type deviceSamples struct {
	mu      sync.Mutex
	samples map[proto.DeviceID]proto.DeviceMetrics
}

func (s *Server) handleDeviceMetrics(w http.ResponseWriter, r *http.Request, c *caller) {
	st := s.state.Load()
	device := st.w.byMachine[c.machine]
	if device == nil || !device.usable() {
		s.writeErr(w, r, errForbidden("这台设备还没登录"))
		return
	}
	now := s.now().UTC()
	if r.Method == http.MethodPost {
		var sample proto.DeviceMetrics
		if err := readJSON(w, r, &sample); err != nil {
			s.writeErr(w, r, err)
			return
		}
		if err := sample.Validate(); err != nil {
			s.writeErr(w, r, errInvalid(err.Error()))
			return
		}
		// Use receipt time. A host cannot make its sample remain fresh indefinitely.
		sample.At = now
		s.deviceSamples.mu.Lock()
		if s.deviceSamples.samples == nil {
			s.deviceSamples.samples = map[proto.DeviceID]proto.DeviceMetrics{}
		}
		for id, v := range s.deviceSamples.samples {
			if now.Sub(v.At) > 24*time.Hour || st.w.devices[id] == nil {
				delete(s.deviceSamples.samples, id)
			}
		}
		s.deviceSamples.samples[device.ID] = sample
		s.deviceSamples.mu.Unlock()
		writeJSON(w, http.StatusOK, struct{}{})
		return
	}
	out := proto.DeviceMetricsView{Devices: map[proto.DeviceID]proto.DeviceMetrics{}}
	s.deviceSamples.mu.Lock()
	for id, v := range s.deviceSamples.samples {
		d := st.w.devices[id]
		if d != nil && d.usable() && d.Owner == device.Owner && now.Sub(v.At) <= 24*time.Hour {
			out.Devices[id] = v
		}
	}
	s.deviceSamples.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) pruneDeviceMetrics(now time.Time) {
	st := s.state.Load()
	s.deviceSamples.mu.Lock()
	defer s.deviceSamples.mu.Unlock()
	for id, v := range s.deviceSamples.samples {
		d := st.w.devices[id]
		if d == nil || !d.usable() || now.Sub(v.At) > 24*time.Hour {
			delete(s.deviceSamples.samples, id)
		}
	}
}
