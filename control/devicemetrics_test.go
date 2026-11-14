package control

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

func TestDeviceMetricsOwnershipAndValidation(t *testing.T) {
	w := newM2World(t)
	sample := proto.DeviceMetrics{At: time.Now().Add(365 * 24 * time.Hour), OS: "linux", Arch: "amd64", CPUs: 8, Memory: &proto.HostMemory{Total: 100, Available: 0}, Disks: []proto.HostDisk{{Name: "/", Total: 200, Available: 0}}}
	version := w.s.Version()
	if status, err := w.mac.call("POST", proto.PathDeviceMetrics, sample, nil); status != http.StatusOK || err != nil {
		t.Fatalf("post %d %v", status, err)
	}
	var view proto.DeviceMetricsView
	w.mac.call("GET", proto.PathDeviceMetrics, nil, &view)
	own, ok := view.Devices[w.mac.id]
	if !ok || own.Memory.Available != 0 || own.Disks[0].Available != 0 {
		t.Fatal("zero measurements were lost")
	}
	if own.At.After(time.Now().Add(time.Minute)) {
		t.Fatal("trusted client timestamp")
	}
	view = proto.DeviceMetricsView{}
	w.jdesk.call("GET", proto.PathDeviceMetrics, nil, &view)
	if len(view.Devices) != 0 {
		t.Fatal("LAN member received another owner's host metrics")
	}
	if w.s.Version() != version {
		t.Fatal("sampling changes topology version")
	}
	raw, _ := json.Marshal(w.jdesk.view())
	var v map[string]json.RawMessage
	json.Unmarshal(raw, &v)
	if _, ok := v["metrics"]; ok {
		t.Fatal("metrics entered user view")
	}
	bad := sample
	bad.Memory = &proto.HostMemory{Total: 100, Available: 101}
	if status, _ := w.mac.call("POST", proto.PathDeviceMetrics, bad, nil); status != http.StatusBadRequest {
		t.Fatal("invalid memory accepted")
	}
	// There is no caller-selected device identity: extra fields cannot replace
	// the authenticated machine. It can only replace its own sample.
	forged := map[string]interface{}{"device": w.mac.id, "os": "windows", "arch": "amd64", "cpus": 2}
	if status, _ := w.jdesk.call("POST", proto.PathDeviceMetrics, forged, nil); status != http.StatusOK {
		t.Fatal("self sample rejected")
	}
	w.mac.call("GET", proto.PathDeviceMetrics, nil, &view)
	if view.Devices[w.mac.id].OS != "linux" {
		t.Fatal("peer overwrote another device")
	}
	w.s.deviceSamples.mu.Lock()
	old := w.s.deviceSamples.samples[w.mac.id]
	old.At = time.Now().Add(-25 * time.Hour)
	w.s.deviceSamples.samples[w.mac.id] = old
	w.s.deviceSamples.mu.Unlock()
	view = proto.DeviceMetricsView{}
	w.mac.call("GET", proto.PathDeviceMetrics, nil, &view)
	if _, ok := view.Devices[w.mac.id]; ok {
		t.Fatal("expired sample retained")
	}
}
