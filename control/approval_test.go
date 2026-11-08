package control

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// m2World is M1's topology on a clock: 阿杰 owns 周末联机 with his desktop
// and NAS, and kittors is in it with the Mac; the build server is
// kittors's, only in 工作室.
type m2World struct {
	s                       *Server
	clk                     *clock
	mac, build, jdesk, jnas *engine
	studio, weekend         proto.LANID
	me, jie                 proto.PersonID
}

func newM2World(t *testing.T) *m2World {
	s, ts, clk := newClockServer(t, 0)
	w := &m2World{s: s, clk: clk,
		mac: newEngine(t, ts.URL, "mac", proto.MacOS), build: newEngine(t, ts.URL, "build", proto.Linux),
		jdesk: newEngine(t, ts.URL, "desktop", proto.Windows), jnas: newEngine(t, ts.URL, "nas", proto.Linux)}
	w.mac.login("kittors@deneb.test")
	w.build.login("kittors@deneb.test")
	w.jdesk.login("jie@deneb.test")
	w.jnas.login("jie@deneb.test")
	w.studio = w.mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: w.build.id})
	w.weekend = w.jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	w.jdesk.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.jnas.id})
	inv := w.jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	w.mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	w.me, w.jie = w.mac.view().Me, w.jdesk.view().Me
	return w
}

func pendingIn(lv *proto.LANView, id proto.DeviceID) (found, pending bool) {
	if lv == nil {
		return false, false
	}
	for _, d := range lv.Devices {
		if d.Device == id {
			return true, d.Pending
		}
	}
	return false, false
}

// TestApproveDevices is M2's check of "新设备要同意": 阿杰 turns it on, I
// put the build server into 周末联机, 阿杰 sees it waiting; it gets in
// only when he lets it, and not when he turns it away.
func TestApproveDevices(t *testing.T) {
	w := newM2World(t)
	s := w.s
	// New LANs want both approved (the plan's §0.2, which the user took);
	// only the owner changes that.
	if lv := lanByName(w.mac.view(), "周末联机"); !lv.ApproveDevices || !lv.ApproveMembers {
		t.Fatalf("the switches as a member sees them: %v %v", lv.ApproveDevices, lv.ApproveMembers)
	}
	w.mac.refused(proto.ErrForbidden, proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend})
	// My Mac came in with me when I accepted 阿杰's invite: letting a person
	// in lets in the computer they came with.
	if found, pending := pendingIn(lanByName(w.jdesk.view(), "周末联机"), w.mac.id); !found || pending {
		t.Fatal("the Mac that accepted the owner's invite waits")
	}

	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	if found, pending := pendingIn(lanByName(w.jdesk.view(), "周末联机"), w.build.id); !found || !pending {
		t.Fatal("阿杰 does not see the build server waiting")
	}
	if found, pending := pendingIn(lanByName(w.mac.view(), "周末联机"), w.build.id); !found || !pending {
		t.Fatal("I do not see my build server waiting")
	}
	if peerOf(netMapOf(s, w.jnas.id), w.build.id) != nil {
		t.Fatal("the NAS sees the build server before 阿杰 let it in")
	}
	if p := peerOf(netMapOf(s, w.build.id), w.jnas.id); p != nil {
		t.Fatal("the waiting build server sees the NAS")
	}
	// Taking it out again takes back the request; adding is a no-op twice.
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	w.mac.refused(proto.ErrForbidden, proto.OpDeviceApprove, proto.OpArgs{LAN: w.weekend, Device: w.build.id})

	w.jdesk.must(proto.OpDeviceApprove, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	if p := peerOf(netMapOf(s, w.jnas.id), w.build.id); p == nil || !slices.Equal(p.Scopes, []proto.ScopeID{proto.LANScope(w.weekend)}) {
		t.Fatalf("after 阿杰 let it in, the NAS sees the build server as %+v", p)
	}

	// Out and in again: it waits again, and this time 阿杰 says no.
	w.mac.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	w.jdesk.must(proto.OpDeviceReject, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	if found, _ := pendingIn(lanByName(w.mac.view(), "周末联机"), w.build.id); found {
		t.Fatal("the turned-away build server is still in 周末联机")
	}
	if peerOf(netMapOf(s, w.jnas.id), w.build.id) != nil {
		t.Fatal("the NAS sees the turned-away build server")
	}
	w.jdesk.refused(proto.ErrNotFound, proto.OpDeviceReject, proto.OpArgs{LAN: w.weekend, Device: w.build.id})

	// The owner's own devices never wait.
	w.jnas.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: w.weekend, Device: w.jnas.id})
	w.jnas.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.jnas.id})
	if peerOf(netMapOf(s, w.mac.id), w.jnas.id) == nil {
		t.Fatal("the owner's own NAS had to wait")
	}
	// Turned off, devices come straight in again.
	w.jdesk.must(proto.OpLANSetApproveDevices, proto.OpArgs{LAN: w.weekend})
	w.mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	if peerOf(netMapOf(s, w.jnas.id), w.build.id) == nil {
		t.Fatal("with approval off the build server still waits")
	}
}

// TestApproveMembers: people a member (not the owner) invites wait for
// the owner, and see nothing inside until let in; people the owner invites
// come straight in. Invites can be made out to a person.
func TestApproveMembers(t *testing.T) {
	w := newM2World(t)
	s := w.s
	ts := w.mac.url
	mia := newEngine(t, ts, "mia-air", proto.MacOS)
	leo := newEngine(t, ts, "leo-pc", proto.Windows)
	mia.login("mia@deneb.test")
	leo.login("leo@deneb.test")
	miaID, leoID := mia.view().Me, leo.view().Me

	// I (a member) invite Mia, by her ball: the invite is hers alone.
	inv := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend, Person: miaID}).Result.Invite
	if inv.Person != miaID {
		t.Fatalf("invite made out to %q", inv.Person)
	}
	again := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend, Person: miaID}).Result.Invite
	if again.ID != inv.ID {
		t.Fatal("inviting Mia twice made two invites")
	}
	mv := mia.view()
	if len(mv.Invitations) != 1 || mv.Invitations[0].LANName != "周末联机" || !mv.Invitations[0].NeedsApproval || mv.Invitations[0].Inviter.ID != w.me {
		t.Fatalf("Mia's invitations: %+v", mv.Invitations)
	}
	if len(leo.view().Invitations) != 0 {
		t.Fatal("Leo sees Mia's invitation")
	}
	leo.refused(proto.ErrNotFound, proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	leo.refused(proto.ErrForbidden, proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	// The owner sees whom it is for, as a ball waiting for an answer; so
	// do I, who made it.
	for _, e := range []*engine{w.jdesk, w.mac} {
		v := e.view()
		lv := lanByName(v, "周末联机")
		if len(lv.Invites) != 1 || lv.Invites[0].Person != miaID || !slices.ContainsFunc(v.People, func(p proto.Person) bool { return p.ID == miaID }) ||
			len(lv.Invited) != 1 || lv.Invited[0].Person != miaID || lv.Invited[0].InvitedBy != w.me || lv.Invited[0].Invite != inv.ID {
			t.Fatalf("%s sees the invites %+v, invited %+v", e.host, lv.Invites, lv.Invited)
		}
	}

	// Mia accepts by its id, and waits: she sees the name and the owner,
	// nothing else; her MacBook is in no map.
	if res := mia.must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID}); res.Result.LAN != w.weekend {
		t.Fatalf("accept answered %+v", res)
	}
	mv = mia.view()
	lv := lanByName(mv, "周末联机")
	if lv == nil || !lv.Pending || len(lv.Members) != 0 || len(lv.Devices) != 0 || len(mv.Invitations) != 0 {
		t.Fatalf("Mia, waiting, sees %+v", lv)
	}
	if nm := netMapOf(s, mia.id); len(nm.Peers) != 0 || len(nm.Scopes) != 0 {
		t.Fatalf("Mia's MacBook, waiting, has %v", peerIDs(nm))
	}
	if peerOf(netMapOf(s, w.jnas.id), mia.id) != nil {
		t.Fatal("the NAS sees Mia's MacBook before 阿杰 let her in")
	}
	jv := lanByName(w.jdesk.view(), "周末联机")
	if !slices.ContainsFunc(jv.Members, func(m proto.Member) bool { return m.Person == miaID && m.State == proto.MemberPending }) {
		t.Fatalf("阿杰 does not see Mia waiting: %+v", jv.Members)
	}
	if found, pending := pendingIn(jv, mia.id); !found || !pending {
		t.Fatal("阿杰 does not see Mia's MacBook waiting with her")
	}
	if kv := lanByName(w.mac.view(), "周末联机"); slices.ContainsFunc(kv.Members, func(m proto.Member) bool { return m.Person == miaID }) {
		t.Fatal("I, not the owner, see Mia waiting")
	}
	w.jdesk.refused(proto.ErrConflict, proto.OpDeviceApprove, proto.OpArgs{LAN: w.weekend, Device: mia.id})
	if jw := w.jdesk.view(); jw.Seats.Used != 2 {
		t.Fatalf("someone waiting took a seat: %+v", jw.Seats)
	}

	// 阿杰 lets her in: she and her MacBook are in.
	w.jdesk.must(proto.OpMemberApprove, proto.OpArgs{LAN: w.weekend, Person: miaID})
	if p := peerOf(netMapOf(s, w.jnas.id), mia.id); p == nil {
		t.Fatal("after 阿杰 let Mia in, the NAS does not see her MacBook")
	}
	if lv := lanByName(mia.view(), "周末联机"); lv.Pending || len(lv.Members) != 3 {
		t.Fatalf("Mia, let in, sees %+v", lv)
	}

	// Leo, invited by the owner himself, comes straight in.
	linv := w.jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend, Person: leoID}).Result.Invite
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invitation: linv.ID})
	if peerOf(netMapOf(s, w.jnas.id), leo.id) == nil {
		t.Fatal("Leo, invited by the owner, had to wait")
	}
	w.jdesk.refused(proto.ErrConflict, proto.OpLANInvite, proto.OpArgs{LAN: w.weekend, Person: leoID})

	// A member's invite, the owner says no: member.reject turns them down,
	// and does not touch someone already in.
	w.jdesk.must(proto.OpMemberRemove, proto.OpArgs{LAN: w.weekend, Person: leoID})
	linv2 := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invite: linv2.URL})
	w.jdesk.refused(proto.ErrConflict, proto.OpMemberReject, proto.OpArgs{LAN: w.weekend, Person: miaID})
	w.mac.refused(proto.ErrForbidden, proto.OpMemberReject, proto.OpArgs{LAN: w.weekend, Person: leoID})
	w.jdesk.must(proto.OpMemberReject, proto.OpArgs{LAN: w.weekend, Person: leoID})
	if lanByName(leo.view(), "周末联机") != nil || peerOf(netMapOf(s, w.jnas.id), leo.id) != nil {
		t.Fatal("Leo, turned down, is still there")
	}
	// Waiting, then taking it back: lan.leave works while waiting.
	linv3 := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invite: linv3.URL})
	leo.must(proto.OpLANLeave, proto.OpArgs{LAN: w.weekend})
	if lanByName(leo.view(), "周末联机") != nil {
		t.Fatal("Leo's request is still there after he took it back")
	}
	// With approval off, a member's invitee comes straight in.
	w.jdesk.must(proto.OpLANSetApproveMembers, proto.OpArgs{LAN: w.weekend})
	linv4 := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invite: linv4.URL})
	if peerOf(netMapOf(s, w.jnas.id), leo.id) == nil {
		t.Fatal("with approval off Leo still waits")
	}
}

// TestRenewAndDNSName: the owner renews a device whose login ran out from
// another device ("点一下就续上"), and renames its DNS label.
func TestRenewAndDNSName(t *testing.T) {
	w := newM2World(t)
	s := w.s
	// The NAS has been away all its lifetime; the desktop came back.
	w.clk.add(DefaultLoginLifetime - time.Hour)
	w.jdesk.auth()
	w.mac.auth()
	w.clk.add(2 * time.Hour)
	s.sweep()
	w.jdesk.auth() // their tokens ran out with the hours
	w.mac.auth()
	if netMapOf(s, w.jnas.id) != nil || peerOf(netMapOf(s, w.mac.id), w.jnas.id) != nil {
		t.Fatal("the NAS's login did not run out")
	}
	dv := deviceView(w.jdesk.view(), w.jnas.id)
	if dv == nil || !dv.Login.Expired || w.clk.now().Before(dv.Login.RenewFrom) {
		t.Fatalf("阿杰 sees his NAS as %+v", dv)
	}
	if mv := deviceView(w.mac.view(), w.jnas.id); mv == nil || !mv.Login.Expired || !mv.Login.ExpiresAt.IsZero() {
		t.Fatalf("I see 阿杰's NAS as %+v (it should read 登录过期, and no more)", mv)
	}
	w.mac.refused(proto.ErrForbidden, proto.OpDeviceRenew, proto.OpArgs{Device: w.jnas.id})
	w.jdesk.must(proto.OpDeviceRenew, proto.OpArgs{Device: w.jnas.id})
	if peerOf(netMapOf(s, w.mac.id), w.jnas.id) == nil {
		t.Fatal("after 阿杰 renewed it the Mac does not see the NAS")
	}
	if dv := deviceView(w.jdesk.view(), w.jnas.id); dv.Login.Expired || !w.clk.now().Before(dv.Login.RenewFrom) {
		t.Fatalf("after renewing: %+v", dv.Login)
	}

	// Renaming carries the DNS label along when the name has letters to
	// make one from; a Chinese name keeps the label it had.
	w.jdesk.must(proto.OpDeviceRename, proto.OpArgs{Device: w.jnas.id, Name: "Jie NAS 2"})
	if p := peerOf(netMapOf(s, w.mac.id), w.jnas.id); p.DisplayName != "Jie NAS 2" || p.FQDN != "jie-nas-2.jie.deneb.internal" {
		t.Fatalf("after renaming the Mac sees %q, %q", p.DisplayName, p.FQDN)
	}
	w.jdesk.must(proto.OpDeviceRename, proto.OpArgs{Device: w.jnas.id, Name: "阿杰的 NAS"})
	if p := peerOf(netMapOf(s, w.mac.id), w.jnas.id); p.DisplayName != "阿杰的 NAS" || p.FQDN != "nas.jie.deneb.internal" {
		t.Fatalf("after a Chinese name the Mac sees %q, %q", p.DisplayName, p.FQDN)
	}
	// Taken labels get a number.
	w.jdesk.must(proto.OpDeviceRename, proto.OpArgs{Device: w.jdesk.id, Name: "NAS"})
	if p := peerOf(netMapOf(s, w.mac.id), w.jdesk.id); p.FQDN != "nas-2.jie.deneb.internal" {
		t.Fatalf("a taken label: %q", p.FQDN)
	}

	// A DNS label set by hand: the FQDN follows, later renames leave it;
	// two of one person's devices cannot share one.
	w.jdesk.must(proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.jnas.id, Name: "Storage"})
	if p := peerOf(netMapOf(s, w.mac.id), w.jnas.id); p == nil || p.FQDN != "storage.jie.deneb.internal" {
		t.Fatalf("after the new label the Mac sees %+v", p)
	}
	w.jdesk.must(proto.OpDeviceRename, proto.OpArgs{Device: w.jnas.id, Name: "Big Box"})
	if p := peerOf(netMapOf(s, w.mac.id), w.jnas.id); p.FQDN != "storage.jie.deneb.internal" {
		t.Fatalf("a rename moved a label set by hand: %q", p.FQDN)
	}
	if err := w.jdesk.refused(proto.ErrConflict, proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.jdesk.id, Name: "storage"}); !strings.Contains(err.Message, "storage") {
		t.Fatalf("refusal: %v", err)
	}
	w.jdesk.refused(proto.ErrInvalid, proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.jdesk.id, Name: "阿杰"})
	w.mac.refused(proto.ErrForbidden, proto.OpDeviceSetDNSName, proto.OpArgs{Device: w.jnas.id, Name: "x"})
}
