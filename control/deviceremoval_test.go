package control

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kittors/deneb/proto"
)

// LAN removal keeps the account identity and every unrelated capability.
// This is a disposable control server/database, never a deployed device.
func TestLANRemovalPreservesDeviceOtherLANAndShare(t *testing.T) {
	w := newM2World(t)
	w.jdesk.must(proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend, On: false})
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	svc := &proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{proto.Port(proto.TCP, 22)}}
	w.mac.must(proto.OpServiceSet, proto.OpArgs{LAN: w.studio, Device: w.build.id, Service: svc, On: true})
	share := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}).Result.Share
	old := netMapOf(w.s, w.build.id).Self
	services := serviceOf(lanByName(w.mac.view(), "工作室"), w.build.id)
	w.mac.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	now := netMapOf(w.s, w.build.id).Self
	if !reflect.DeepEqual(old, now) {
		t.Fatal("LAN removal changed device identity/address")
	}
	if deviceView(w.mac.view(), w.build.id) == nil {
		t.Fatal("LAN removal deleted owned device")
	}
	if _, err := w.build.call("GET", proto.PathUserView, nil, nil); err != nil {
		t.Fatalf("LAN removal signed out device: %v", err)
	}
	if lv := lanByName(w.mac.view(), "周末联机"); slices.ContainsFunc(lv.Devices, func(d proto.LANDevice) bool { return d.Device == w.build.id }) {
		t.Fatal("device still in removed LAN")
	}
	if !reflect.DeepEqual(services, serviceOf(lanByName(w.mac.view(), "工作室"), w.build.id)) {
		t.Fatal("other LAN services changed")
	}
	if w.s.state.Load().w.shares[string(share)] == nil || len(w.jdesk.view().SharedWithMe) != 1 {
		t.Fatal("LAN removal revoked share")
	}
	// Undo rejoins only this LAN, retaining original service settings.
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	if lv := lanByName(w.mac.view(), "周末联机"); !slices.ContainsFunc(lv.Devices, func(d proto.LANDevice) bool { return d.Device == w.build.id }) {
		t.Fatal("undo did not restore LAN")
	}
	if !reflect.DeepEqual(old, netMapOf(w.s, w.build.id).Self) {
		t.Fatal("undo changed identity")
	}
}
