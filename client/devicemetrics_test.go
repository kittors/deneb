package client

import (
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

func TestDeviceMetricsAreOwnedAndClearedOnAccountChange(t *testing.T) {
	l := NewLive(nil)
	l.state = &apitype.State{LoggedIn: true, User: "me", SelfDeviceID: "mac"}
	l.view = &proto.UserView{Me: "me", Devices: []proto.DeviceView{{ID: "mac", Owner: "me"}, {ID: "other", Owner: "jie"}}}
	sample := proto.DeviceMetrics{At: time.Now(), CPUs: 1}
	l.metrics = proto.DeviceMetricsView{Devices: map[proto.DeviceID]proto.DeviceMetrics{"mac": sample, "other": sample}}
	s := l.buildLocked(time.Now())
	if len(s.DeviceMetrics) != 1 {
		t.Fatal("snapshot includes non-owned sample")
	}
	l.takeState(&apitype.State{LoggedIn: true, User: "jie", SelfDeviceID: "new"})
	if len(l.Snapshot().DeviceMetrics) != 0 || len(l.metrics.Devices) != 0 {
		t.Fatal("host metrics survived account switch")
	}
	l.MonitorDevices(true)
	epoch := l.metricsEpoch
	l.MonitorDevices(false)
	if l.monitoring || l.metricsEpoch <= epoch {
		t.Fatal("leaving page did not invalidate pending fetch")
	}
}
