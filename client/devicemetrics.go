package client

import (
	"context"
	"encoding/json"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/localclient"
	stdmaps "maps"
	"time"

	"github.com/kittors/deneb/proto"
)

// MonitorDevices controls polling as the dedicated page enters/leaves. The
// device engine collects independently so all owned hosts can be inspected.
func (l *Live) MonitorDevices(active bool) {
	l.mu.Lock()
	changed := l.monitoring != active
	l.monitoring = active
	if changed {
		l.metricsEpoch++
	}
	cancel := l.metricsCancel
	l.mu.Unlock()
	if !active && cancel != nil {
		cancel()
	}
	if changed {
		select {
		case l.metricsWake <- struct{}{}:
		default:
		}
	}
}
func (l *Live) deviceMetrics(ctx context.Context) {
	for {
		l.mu.Lock()
		active := l.monitoring
		epoch := l.metricsEpoch
		l.mu.Unlock()
		if active && l.Snapshot().LoggedIn {
			l.update(func(time.Time) { l.metricsLoading = true })
			request, cancel := context.WithTimeout(ctx, 8*time.Second)
			l.mu.Lock()
			if !l.monitoring || epoch != l.metricsEpoch {
				l.mu.Unlock()
				cancel()
				continue
			}
			l.metricsCancel = cancel
			l.mu.Unlock()
			raw, err := l.lc.DeviceMetrics(request)
			var view proto.DeviceMetricsView
			if err == nil {
				err = json.Unmarshal(raw, &view)
			}
			l.update(func(time.Time) {
				l.metricsLoading = false
				if !l.monitoring || epoch != l.metricsEpoch || l.state == nil || !l.state.LoggedIn {
					return
				}
				if err != nil {
					l.metricsError = "设备数据暂不可用，请确认引擎已更新"
					return
				}
				// Accept only current owned device IDs, including no-LAN hosts.
				owned := map[proto.DeviceID]bool{}
				if l.view != nil {
					for _, d := range l.view.Devices {
						if d.Owner == l.view.Me {
							owned[d.ID] = true
						}
					}
				}
				for id, m := range view.Devices {
					if !owned[id] || m.Validate() != nil {
						delete(view.Devices, id)
					}
				}
				l.metrics = view
				l.metricsError = ""
			})
			// One short probe per visible connected host; no probes when away
			// from the page. Existing Deneb ICMP authorization still applies.
			snapshot := l.Snapshot()
			for _, id := range snapshot.Mine {
				if request.Err() != nil {
					break
				}
				d := snapshot.Device(id)
				if id == snapshot.Self || d == nil || !d.Online || !snapshot.Path(id).Kind.Connected() {
					continue
				}
				result, probeErr := l.lc.Ping(request, string(id), localclient.PingOptions{N: 1, Timeout: time.Second}, nil)
				measured := model.LatencySample{At: time.Now(), Failed: true}
				if probeErr == nil && result.Received > 0 {
					measured.Failed = false
					measured.RTT = time.Duration(result.AvgMs * float64(time.Millisecond))
				}
				l.update(func(time.Time) {
					if !l.monitoring || epoch != l.metricsEpoch {
						return
					}
					next := stdmaps.Clone(l.latencies)
					if next == nil {
						next = map[model.DeviceID]model.LatencySample{}
					}
					next[id] = measured
					l.latencies = next
				})
			}
			cancel()
			l.mu.Lock()
			l.metricsCancel = nil
			l.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-l.metricsWake:
		case <-time.After(15 * time.Second):
		}
	}
}

func (l *Live) RefreshDeviceMetrics() {
	select {
	case l.metricsWake <- struct{}{}:
	default:
	}
}
