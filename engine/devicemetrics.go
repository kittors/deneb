package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kittors/deneb/engine/hostmetrics"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

func (e *Engine) metricsLoop() {
	var sampler hostmetrics.Sampler
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		loggedIn := e.st.LoggedIn
		device := e.st.Device
		e.mu.Unlock()
		if loggedIn {
			sample := sampler.Sample()
			sample.EngineVersion = e.opts.Version
			e.metricsMu.Lock()
			e.metricsSample = &sample
			e.metricsDevice = device
			e.metricsMu.Unlock()
			if cc, err := e.loggedInClient(); err == nil {
				ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
				err := cc.ReportDeviceMetrics(ctx, sample)
				cancel()
				// Old control servers can reject the new endpoint; local measurements
				// remain usable. No network/topology state changes on collection failure.
				if err != nil && e.ctx.Err() == nil {
					e.logf("[metrics] publish: %v", err)
				}
			}
		}
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (e *Engine) DeviceMetrics(ctx context.Context) (json.RawMessage, error) {
	e.mu.Lock()
	cc, self, person := e.cc, e.st.Device, e.st.Person
	loggedIn := e.st.LoggedIn
	e.mu.Unlock()
	if !loggedIn || cc == nil {
		return nil, errNotLoggedIn
	}
	view := proto.DeviceMetricsView{Devices: map[proto.DeviceID]proto.DeviceMetrics{}}
	raw, remoteErr := cc.DeviceMetrics(ctx)
	if remoteErr == nil {
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, &apitype.Error{Code: apitype.ErrInternal, Message: "设备数据格式不正确"}
		}
	}
	e.metricsMu.Lock()
	sample := e.metricsSample
	device := e.metricsDevice
	e.metricsMu.Unlock()
	e.mu.Lock()
	sameIdentity := e.st.LoggedIn && e.st.Device == self && e.st.Person == person && e.cc == cc
	e.mu.Unlock()
	if !sameIdentity {
		return nil, errNotLoggedIn
	}
	if sample != nil && self != "" && self == device {
		if view.Devices == nil {
			view.Devices = map[proto.DeviceID]proto.DeviceMetrics{}
		}
		view.Devices[self] = *sample
	}
	// An old control server does not support remote metrics, but the local
	// sample is still useful. Availability of each remote host stays explicit.
	if remoteErr != nil && len(view.Devices) == 0 {
		return nil, apiErr(remoteErr)
	}
	return json.Marshal(view)
}
