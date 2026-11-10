package control

import (
	"slices"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestDeviceDelete: a device's owner deletes it, online or logged out. One
// online is logged out at once, as when someone else logs in on its
// machine; its LANs, shares, catalog, refusals and kicks go with it; its
// address cools down; logging in there again makes a new device. No one
// else may (TestNoOneActsOnOthers), and the server's admin may.
func TestDeviceDelete(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	sock := serveAdmin(t, s)

	// What the build server has: a place in 工作室, a service open there, a
	// share with 阿杰, a refusal, a kick on kittors's relay.
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	relay, _ := registerRelay(t, url, rc.RelayToken)
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: rc.Relay})
	w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: rc.Relay, Device: w.build.id})
	svc := &proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{{Proto: "tcp", First: 22, Last: 22}}}
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: svc, On: true})
	share := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}).Result.Share
	w.mac.must(proto.OpDeviceRefuse, proto.OpArgs{Device: w.build.id, Person: w.jie, Refuse: true})
	addr := netMapOf(s, w.build.id).Self.Addrs[0].Addr()
	old := w.build.id
	st := w.build.stream(t, false)
	st.next(t)

	v := s.Version()
	if res := w.mac.must(proto.OpDeviceDelete, proto.OpArgs{Device: w.build.id}); res.Version == v {
		t.Fatal("deleting made no new version")
	}
	// Online, it is logged out at once.
	for f := st.next(t); !f.LoggedOut; f = st.next(t) {
	}
	if _, err := w.build.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("the deleted device's call: %v", err)
	}
	// Gone from everyone's maps and views, and what it had with it.
	if p := peerOf(netMapOf(s, w.mac.id), old); p != nil {
		t.Fatal("the Mac still has the deleted device as a peer")
	}
	if lv := lanByName(w.mac.view(), "工作室"); slices.ContainsFunc(lv.Devices, func(d proto.LANDevice) bool { return d.Device == old }) {
		t.Fatal("工作室 still lists it")
	}
	if dv := deviceView(w.mac.view(), old); dv != nil {
		t.Fatal("my view still has it")
	}
	if v := w.jdesk.view(); len(v.SharedWithMe) != 0 || deviceView(v, old) != nil {
		t.Fatalf("阿杰 still has the share: %+v", v.SharedWithMe)
	}
	sw := s.state.Load().w
	if sw.shares[string(share)] != nil || len(sw.kickedDevice[rc.Relay]) != 0 || len(sw.kicksOf(rc.Relay, w.clk.now().AddDate(0, 0, -30))) != 0 {
		t.Fatal("its share or its kick stayed")
	}
	var services, refusals, released int
	s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE device = ?`, old).Scan(&services)
	s.db.QueryRow(`SELECT COUNT(*) FROM refusals WHERE device = ?`, old).Scan(&refusals)
	s.db.QueryRow(`SELECT COUNT(*) FROM released_addrs WHERE ipv4 = ?`, addr.String()).Scan(&released)
	if services != 0 || refusals != 0 || released != 1 {
		t.Fatalf("left behind: %d services, %d refusals; address released %d", services, refusals, released)
	}
	if a := relay.admit(w.build.nk); a.Allow {
		t.Fatal("the relay still lets it in")
	}
	// The machine logs in again: a new device, a new address, no LANs.
	w.build.login("kittors@deneb.test")
	if nm := netMapOf(s, w.build.id); w.build.id == old || nm.Self.Addrs[0].Addr() == addr || len(nm.Scopes) != 0 {
		t.Fatalf("logged in again: %s at %s, %d scopes", w.build.id, nm.Self.Addrs[0], len(nm.Scopes))
	}

	// Logged out, it is listed for its owner only, and its owner deletes it.
	w.jnas.call("POST", proto.PathLogout, nil, nil)
	jv := w.jdesk.view()
	if len(jv.LoggedOutDevices) != 1 || jv.LoggedOutDevices[0].ID != w.jnas.id || !slices.Equal(jv.LoggedOutDevices[0].LANs, []proto.LANID{w.weekend}) {
		t.Fatalf("阿杰's logged-out devices: %+v", jv.LoggedOutDevices)
	}
	if len(w.mac.view().LoggedOutDevices) != 0 {
		t.Fatal("kittors sees 阿杰's logged-out NAS")
	}
	w.mac.refused(proto.ErrNotFound, proto.OpDeviceDelete, proto.OpArgs{Device: w.jnas.id})
	w.jdesk.must(proto.OpDeviceDelete, proto.OpArgs{Device: w.jnas.id})
	if jv := w.jdesk.view(); len(jv.LoggedOutDevices) != 0 {
		t.Fatalf("still listed: %+v", jv.LoggedOutDevices)
	}
	w.jdesk.refused(proto.ErrNotFound, proto.OpDeviceDelete, proto.OpArgs{Device: w.jnas.id})

	// The server's admin lists them and deletes one, whoever's it is.
	leo := newEngine(t, url, "leo-vm", proto.Linux)
	leo.login("leo@deneb.test")
	ds, err := AdminDevices(sock, "Leo@deneb.test")
	if err != nil || len(ds) != 1 || ds[0].ID != leo.id || ds[0].Owner != "leo@deneb.test" || ds[0].State != "offline" {
		t.Fatalf("leo's devices: %+v, %v", ds, err)
	}
	if gone, err := AdminDeleteDevice(sock, leo.id); err != nil || gone.ID != leo.id {
		t.Fatalf("admin delete: %+v, %v", gone, err)
	}
	if _, err := leo.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("after the admin deleted it: %v", err)
	}
	if _, err := AdminDeleteDevice(sock, leo.id); err == nil {
		t.Fatal("deleted twice")
	}
}
