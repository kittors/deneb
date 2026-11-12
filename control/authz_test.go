package control

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

// TestNoOneActsOnOthers is the M7 review's check of every op (proto/ops.go)
// against people it is not theirs to run on: Mia, who shares nothing with
// kittors and 阿杰, names their LANs, devices, invites, shares, relays,
// codes and kicks; kittors, a member of 阿杰's 周末联机 but not its owner,
// tries the owner's ops there. Each is refused, nothing changes, and Mia
// sees none of it. An op added later fails the test until it has a case.
func TestNoOneActsOnOthers(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	// 阿杰 wants both approved, so there are waiting ones to act on.
	w.jdesk.must(proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend, On: true})
	w.jdesk.must(proto.OpLANSetApproveMembers, proto.OpArgs{LAN: w.weekend, On: true})
	mia := newEngine(t, url, "mia-air", proto.MacOS)
	mia.login("mia@deneb.test")
	leo := newEngine(t, url, "leo-air", proto.MacOS)
	leo.login("leo@deneb.test")
	leoID := leo.view().Me

	// What there is: the build server waiting in 周末联机, and Leo, whom
	// kittors invited there; an invite link and one made out to 阿杰; a
	// share of the build server with 阿杰; kittors's relay with a kick and
	// a join code; Mia's own LAN.
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invite: w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite.URL})
	link := w.jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	toJie := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio, Person: w.jie}).Result.Invite
	share := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}).Result.Share
	rc := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	registerRelay(t, url, rc.RelayToken)
	relay := rc.Relay
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: relay})
	kick := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: relay, Person: w.jie}).Result.Kick
	join := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin.ID
	miaLAN := mia.must(proto.OpLANCreate, proto.OpArgs{Name: "Mia 的局域网"}).Result.LAN
	if lv := lanByName(w.jdesk.view(), "周末联机"); !memberPending(lv, leoID) {
		t.Fatal("Leo is not waiting in 周末联机")
	}

	svc := &proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{{Proto: "tcp", First: 22, Last: 22}}}
	two := 2
	type try struct {
		who  *engine
		op   proto.OpType
		args proto.OpArgs
	}
	// The answer when it is not the usual not found or forbidden.
	want := map[string]proto.ErrorCode{"lan.leave desktop": proto.ErrInvalid} // the owner deletes it instead
	tries := []try{
		{mia, proto.OpLANRename, proto.OpArgs{LAN: w.studio, Name: "我的了"}},
		{w.mac, proto.OpLANRename, proto.OpArgs{LAN: w.weekend, Name: "我的了"}},
		{mia, proto.OpLANDelete, proto.OpArgs{LAN: w.weekend}},
		{w.mac, proto.OpLANDelete, proto.OpArgs{LAN: w.weekend}},
		{mia, proto.OpLANInvite, proto.OpArgs{LAN: w.studio}},
		{mia, proto.OpLANInvite, proto.OpArgs{LAN: w.studio, Person: w.mac.view().Me}},
		{mia, proto.OpInviteAccept, proto.OpArgs{Invitation: toJie.ID}},
		{mia, proto.OpInviteAccept, proto.OpArgs{Invite: string(toJie.ID)}},
		{mia, proto.OpInviteDecline, proto.OpArgs{Invitation: toJie.ID}},
		{mia, proto.OpInviteRevoke, proto.OpArgs{Invite: string(link.ID)}},
		{mia, proto.OpInviteRevoke, proto.OpArgs{Invite: string(toJie.ID)}},
		{w.jnas, proto.OpInviteRevoke, proto.OpArgs{Invite: string(toJie.ID)}},
		{mia, proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: mia.id}},
		{mia, proto.OpLANAddDevice, proto.OpArgs{LAN: miaLAN, Device: w.build.id}},
		{w.mac, proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: w.jnas.id}},
		{mia, proto.OpLANRemoveDevice, proto.OpArgs{LAN: w.studio, Device: w.build.id}},
		{w.mac, proto.OpLANRemoveDevice, proto.OpArgs{LAN: w.weekend, Device: w.jnas.id}},
		{mia, proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: svc, On: true}},
		{w.jdesk, proto.OpServiceSet, proto.OpArgs{Device: w.mac.id, LAN: w.weekend, Service: svc, On: true}},
		{mia, proto.OpServiceSet, proto.OpArgs{Share: share, Service: svc, On: true}},
		{w.jdesk, proto.OpServiceSet, proto.OpArgs{Share: share, Service: svc, On: true}},
		{mia, proto.OpDeviceRefuse, proto.OpArgs{Device: w.build.id, Person: w.jie, Refuse: true}},
		{w.jdesk, proto.OpDeviceRefuse, proto.OpArgs{Device: w.mac.id, Person: leoID, Refuse: true}},
		{mia, proto.OpDeviceRename, proto.OpArgs{Device: w.build.id, Name: "我的了"}},
		{w.jdesk, proto.OpDeviceRename, proto.OpArgs{Device: w.mac.id, Name: "我的了"}},
		{mia, proto.OpDeviceSetKind, proto.OpArgs{Device: w.build.id, Kind: proto.KindPhone}},
		{w.jdesk, proto.OpDeviceSetKind, proto.OpArgs{Device: w.mac.id, Kind: proto.KindPhone}},
		{mia, proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.build.id, Name: "mine"}},
		{w.jdesk, proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.mac.id, Name: "mine"}},
		{mia, proto.OpDeviceDelete, proto.OpArgs{Device: w.build.id}},
		{w.jdesk, proto.OpDeviceDelete, proto.OpArgs{Device: w.mac.id}},
		{mia, proto.OpDeviceRenew, proto.OpArgs{Device: w.build.id}},
		{w.jdesk, proto.OpDeviceRenew, proto.OpArgs{Device: w.mac.id}},
		{mia, proto.OpMemberRemove, proto.OpArgs{LAN: w.weekend, Person: w.mac.view().Me}},
		{w.mac, proto.OpMemberRemove, proto.OpArgs{LAN: w.weekend, Person: w.jie}},
		{mia, proto.OpLANLeave, proto.OpArgs{LAN: w.studio}},
		{w.jdesk, proto.OpLANLeave, proto.OpArgs{LAN: w.weekend}},
		{mia, proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend}},
		{w.mac, proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend}},
		{mia, proto.OpLANSetApproveMembers, proto.OpArgs{LAN: w.weekend}},
		{w.mac, proto.OpLANSetApproveMembers, proto.OpArgs{LAN: w.weekend}},
		{mia, proto.OpDeviceApprove, proto.OpArgs{LAN: w.weekend, Device: w.build.id}},
		{w.mac, proto.OpDeviceApprove, proto.OpArgs{LAN: w.weekend, Device: w.build.id}},
		{mia, proto.OpDeviceReject, proto.OpArgs{LAN: w.weekend, Device: w.build.id}},
		{w.mac, proto.OpDeviceReject, proto.OpArgs{LAN: w.weekend, Device: w.build.id}},
		{mia, proto.OpMemberApprove, proto.OpArgs{LAN: w.weekend, Person: leoID}},
		{w.mac, proto.OpMemberApprove, proto.OpArgs{LAN: w.weekend, Person: leoID}},
		{mia, proto.OpMemberReject, proto.OpArgs{LAN: w.weekend, Person: leoID}},
		{w.mac, proto.OpMemberReject, proto.OpArgs{LAN: w.weekend, Person: leoID}},
		{mia, proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: leoID}},
		{w.jdesk, proto.OpShareCreate, proto.OpArgs{Device: w.mac.id, Person: leoID}},
		{mia, proto.OpShareRevoke, proto.OpArgs{Share: share}},
		{w.jdesk, proto.OpShareRevoke, proto.OpArgs{Share: share}}, // the receiver leaves it instead
		{mia, proto.OpShareRelease, proto.OpArgs{Share: share}},
		{mia, proto.OpShareLeave, proto.OpArgs{Share: share}},
		{w.mac, proto.OpShareLeave, proto.OpArgs{Share: share}}, // the sharer revokes it instead
		{mia, proto.OpDeviceSetPublic, proto.OpArgs{Device: w.build.id, LAN: w.studio}},
		{w.jdesk, proto.OpDeviceSetPublic, proto.OpArgs{Device: w.mac.id, LAN: w.weekend}},
		{mia, proto.OpLANSetMode, proto.OpArgs{LAN: w.weekend, Mode: proto.LANModeShare}},
		{w.mac, proto.OpLANSetMode, proto.OpArgs{LAN: w.weekend, Mode: proto.LANModeShare}},
		{mia, proto.OpRelayToken, proto.OpArgs{Relay: relay}},
		{mia, proto.OpRelayRename, proto.OpArgs{Relay: relay, Name: "我的了"}},
		{w.jdesk, proto.OpRelayRename, proto.OpArgs{Relay: relay, Name: "我的了"}},
		{mia, proto.OpRelayDelete, proto.OpArgs{Relay: relay}},
		{mia, proto.OpRelayAdd, proto.OpArgs{LAN: miaLAN, Relay: relay}},
		{w.jdesk, proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: relay}},
		{mia, proto.OpRelayRemove, proto.OpArgs{LAN: w.studio, Relay: relay}},
		{mia, proto.OpRelayJoin, proto.OpArgs{Relay: relay}},
		{mia, proto.OpRelayJoin, proto.OpArgs{Public: true}},
		{mia, proto.OpRelayRevokeJoin, proto.OpArgs{Join: join}},
		{mia, proto.OpRelaySetWeight, proto.OpArgs{Relay: relay, Weight: &two}},
		{mia, proto.OpRelaySetCapacity, proto.OpArgs{Relay: relay, Capacity: &two}},
		{mia, proto.OpRelayKick, proto.OpArgs{Relay: relay, Person: w.mac.view().Me}},
		{w.jdesk, proto.OpRelayKick, proto.OpArgs{Relay: relay, Device: w.mac.id}},
		{mia, proto.OpRelayUnkick, proto.OpArgs{Kick: kick}},
		{w.jdesk, proto.OpRelayUnkick, proto.OpArgs{Kick: kick}},
	}
	// Ops with nothing of anyone else's to name: they make the asker's
	// own (a LAN, a relay) or delete the asker's account (TestAccountDelete);
	// the link case of invite.accept is the link's holder joining, which is
	// what a link is for.
	ownOnly := []proto.OpType{proto.OpLANCreate, proto.OpRelayCreate, proto.OpAccountDelete}

	covered := map[proto.OpType]bool{}
	for _, op := range ownOnly {
		covered[op] = true
	}
	for _, tr := range tries {
		covered[tr.op] = true
		v := s.Version()
		_, err := tr.who.op(tr.op, tr.args)
		wc, special := want[string(tr.op)+" "+tr.who.host]
		if err == nil || !special && err.Code != proto.ErrNotFound && err.Code != proto.ErrForbidden || special && err.Code != wc {
			t.Errorf("%s as %s %+v: %v, want refused", tr.op, tr.who.host, tr.args, err)
		}
		if s.Version() != v {
			t.Errorf("%s as %s changed something", tr.op, tr.who.host)
		}
	}
	for op := range opHandlers {
		if !covered[op] {
			t.Errorf("%s has no case here: add one", op)
		}
	}

	// Mia sees none of it.
	b, _ := json.Marshal(mia.view())
	for _, id := range []string{string(w.studio), string(w.weekend), string(w.mac.id), string(w.build.id), string(w.jdesk.id),
		string(w.jnas.id), string(share), string(relay), string(link.ID), string(toJie.ID), kick, join, string(w.jie), string(w.me), string(leoID)} {
		if strings.Contains(string(b), id) {
			t.Errorf("Mia's view has %s in it", id)
		}
	}
	if nm := netMapOf(s, mia.id); len(nm.Peers) != 0 || slices.Contains(peerIDs(nm), w.build.id) {
		t.Errorf("Mia's map has peers: %v", peerIDs(nm))
	}
}
