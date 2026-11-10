package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// internalOp runs fn as an op sent from device from, the way handleOps
// runs one, for the M3 operations that have no wire form until proto has
// them.
func internalOp(t *testing.T, s *Server, from proto.DeviceID, fn func(c *opCtx) error) error {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.changeLocked(from, func(tx *sql.Tx, w *World) error {
		me := w.devices[from]
		return fn(&opCtx{s: s, tx: tx, w: w, dev: me, me: me.Owner, now: s.now()})
	})
	return err
}

func shareDevice(t *testing.T, s *Server, from, device proto.DeviceID, to proto.PersonID, services ...proto.ServiceKey) (string, error) {
	t.Helper()
	var id string
	err := internalOp(t, s, from, func(c *opCtx) error {
		var err error
		id, err = c.shareCreate(device, to, services)
		return err
	})
	return id, err
}

func errCode(err error) proto.ErrorCode {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.body.Code
	}
	return ""
}

// newSeatServer is a clock server with a seat limit.
func newSeatServer(t *testing.T, limit int) (*Server, *httptest.Server, *clock) {
	t.Helper()
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true,
		Seed: DevSeed(), Now: clk.now, SeatLimit: limit, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close(); s.Close() })
	return s, ts, clk
}

// TestShareBuildServer is M3's acceptance check, server side: I share the
// build server with 阿杰; his devices see it and only it, through the
// share; it cannot reach his devices; taking it back cuts it off, and undo
// brings it back as it was.
func TestShareBuildServer(t *testing.T) {
	w := newM2World(t)
	s := w.s
	web := proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{proto.Port(proto.TCP, 80)}}
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: &web, On: true})
	// Refused first: sharing says yes, so the refusal goes.
	w.mac.must(proto.OpDeviceRefuse, proto.OpArgs{Device: w.build.id, Person: w.jie, Refuse: true})

	id, err := shareDevice(t, s, w.mac.id, w.build.id, w.jie, web.Key())
	if err != nil {
		t.Fatal(err)
	}
	sid := shareScope(id)
	if _, err := shareDevice(t, s, w.mac.id, w.build.id, w.jie); errCode(err) != proto.ErrConflict || !strings.Contains(err.Error(), "已经有了") {
		t.Fatalf("sharing it again: %v", err)
	}
	if _, err := shareDevice(t, s, w.jdesk.id, w.build.id, w.jie); errCode(err) != proto.ErrNotFound {
		t.Fatalf("阿杰 sharing my build server: %v", err)
	}

	bm := netMapOf(s, w.build.id)
	sc := scopeByID(bm, sid)
	if sc == nil || len(sc.Open) != 1 || sc.Open[0].Key() != web.Key() || !slices.Equal(sc.Relays, []proto.RelayID{"hk"}) {
		t.Fatalf("the build server's share scope: %+v", sc)
	}
	for _, e := range []*engine{w.jdesk, w.jnas} {
		p := peerOf(bm, e.id)
		if p == nil || !slices.Equal(p.Scopes, []proto.ScopeID{sid}) || p.RefusedByMe {
			t.Errorf("the build server sees %s as %+v", e.host, p)
		}
		nm := netMapOf(s, e.id)
		bp := peerOf(nm, w.build.id)
		if bp == nil || !slices.Equal(bp.Scopes, []proto.ScopeID{sid}) {
			t.Errorf("%s sees the build server as %+v", e.host, bp)
		}
		if rs := scopeByID(nm, sid); rs == nil || len(rs.Open) != 0 {
			t.Errorf("%s's side of the share opens %+v", e.host, rs)
		}
		// Nothing of 工作室 reaches 阿杰's devices through the share.
		b, _ := json.Marshal(nm)
		if strings.Contains(string(b), string(w.studio)) || strings.Contains(string(b), "工作室") {
			t.Errorf("%s's map mentions 工作室: %s", e.host, b)
		}
	}
	// 阿杰's two devices do not meet through the share: they share 周末联机
	// only.
	if p := peerOf(netMapOf(s, w.jdesk.id), w.jnas.id); slices.Contains(p.Scopes, sid) {
		t.Error("阿杰's own devices meet through the share")
	}

	// Taken back: gone from both sides; shared again (undo): as it was.
	if err := internalOp(t, s, w.mac.id, func(c *opCtx) error { return c.shareRevoke(id) }); err != nil {
		t.Fatal(err)
	}
	if peerOf(netMapOf(s, w.jdesk.id), w.build.id) != nil || peerOf(netMapOf(s, w.build.id), w.jdesk.id) != nil {
		t.Fatal("after taking it back they still see each other")
	}
	again, err := shareDevice(t, s, w.mac.id, w.build.id, w.jie)
	if err != nil || again != id {
		t.Fatalf("undo: %q, %v (the share was %q)", again, err, id)
	}
	if sc := scopeByID(netMapOf(s, w.build.id), sid); sc == nil || len(sc.Open) != 1 {
		t.Fatalf("after undo the share opens %+v", sc)
	}

	// Refused after sharing: the refusal wins, the share stays.
	w.mac.must(proto.OpDeviceRefuse, proto.OpArgs{Device: w.build.id, Person: w.jie, Refuse: true})
	if p := peerOf(netMapOf(s, w.build.id), w.jdesk.id); p == nil || !p.RefusedByMe {
		t.Fatalf("after refusing 阿杰 the build server sees him as %+v", p)
	}
	if p := peerOf(netMapOf(s, w.jdesk.id), w.build.id); p == nil || !p.RefusedByPeer {
		t.Fatalf("after refusing, 阿杰 sees the build server as %+v", p)
	}
}

func scopeByID(nm *proto.NetMap, id proto.ScopeID) *proto.Scope {
	for i := range nm.Scopes {
		if nm.Scopes[i].ID == id {
			return &nm.Scopes[i]
		}
	}
	return nil
}

// TestSeatLimit is M3's seat check with the limit at 3 (pricing-and-relays
// §1.4, §1.5): sharing with 阿杰 and Mia joining fill my seats; a fourth
// person cannot come in; taking a share back keeps the seat while undo is
// possible, and frees it after.
func TestSeatLimit(t *testing.T) {
	s, ts, clk := newSeatServer(t, 3)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	build := newEngine(t, ts.URL, "build", proto.Linux)
	desk := newEngine(t, ts.URL, "desktop", proto.Windows)
	mia := newEngine(t, ts.URL, "mia", proto.MacOS)
	leo := newEngine(t, ts.URL, "leo", proto.Windows)
	mac.login("kittors@deneb.test")
	build.login("kittors@deneb.test")
	desk.login("jie@deneb.test")
	mia.login("mia@deneb.test")
	leo.login("leo@deneb.test")
	jie, miaID, leoID := desk.view().Me, mia.view().Me, leo.view().Me
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN

	jieShare, err := shareDevice(t, s, mac.id, build.id, jie)
	if err != nil {
		t.Fatal(err)
	}
	inv := mac.must(proto.OpLANInvite, proto.OpArgs{LAN: studio, Person: miaID}).Result.Invite
	mia.must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	if v := mac.view(); v.Seats.Used != 3 || v.Seats.Limit != 3 || !v.Seats.Enforced {
		t.Fatalf("my seats: %+v", v.Seats)
	}
	// Full: a fourth person cannot have a device.
	if _, err := shareDevice(t, s, mac.id, build.id, leoID); errCode(err) != proto.ErrSeatsFull || !strings.Contains(err.Error(), "3 个位置都用上了") {
		t.Fatalf("sharing with a fourth person: %v", err)
	}
	// Someone already in my seats still gets another device.
	if _, err := shareDevice(t, s, mac.id, mac.id, miaID); err != nil {
		t.Fatalf("sharing with Mia, already in my seats: %v", err)
	}
	// A fourth person accepting my invite waits, a dashed ball; letting
	// them in needs a seat.
	linv := mac.must(proto.OpLANInvite, proto.OpArgs{LAN: studio, Person: leoID}).Result.Invite
	if lv := leo.view(); len(lv.Invitations) != 1 || !lv.Invitations[0].SeatsFull {
		t.Fatalf("Leo's invitation does not say my seats are full: %+v", lv.Invitations)
	}
	leo.must(proto.OpInviteAccept, proto.OpArgs{Invitation: linv.ID})
	if lv := lanByName(leo.view(), "工作室"); lv == nil || !lv.Pending {
		t.Fatalf("Leo, accepting while my seats are full: %+v", lv)
	}
	if err := mac.refused(proto.ErrSeatsFull, proto.OpMemberApprove, proto.OpArgs{LAN: studio, Person: leoID}); !strings.Contains(err.Message, "3 个位置都用上了") {
		t.Fatalf("approving Leo: %v", err)
	}

	// Taking 阿杰's share back: for the seconds undo is offered, the seat
	// stays his.
	if err := internalOp(t, s, mac.id, func(c *opCtx) error { return c.shareRevoke(jieShare) }); err != nil {
		t.Fatal(err)
	}
	mac.refused(proto.ErrSeatsFull, proto.OpMemberApprove, proto.OpArgs{LAN: studio, Person: leoID})
	if again, err := shareDevice(t, s, mac.id, build.id, jie); err != nil || again != jieShare {
		t.Fatalf("undo within the hold: %q, %v", again, err)
	}
	if err := internalOp(t, s, mac.id, func(c *opCtx) error { return c.shareRevoke(jieShare) }); err != nil {
		t.Fatal(err)
	}
	// After the hold the seat is free, and the sweep says so.
	clk.add(seatHold + time.Second)
	if !s.sweep() {
		t.Fatal("the sweep did not notice the seat coming free")
	}
	mac.auth()
	mac.must(proto.OpMemberApprove, proto.OpArgs{LAN: studio, Person: leoID})
	if v := mac.view(); v.Seats.Used != 3 || slices.Contains(v.Seats.People, jie) {
		t.Fatalf("my seats after: %+v", v.Seats)
	}
	// 阿杰's share cannot come back now: his seat went to Leo.
	if _, err := shareDevice(t, s, mac.id, build.id, jie); errCode(err) != proto.ErrSeatsFull {
		t.Fatalf("bringing 阿杰's share back with no seat: %v", err)
	}
}

// TestPublicDevice: a device given to 工作室 is its owner's to manage, the
// person who logged it in or not; it lives only in its LAN; deleting the
// LAN gives it back.
func TestPublicDevice(t *testing.T) {
	w := newM2World(t)
	s := w.s
	if err := internalOp(t, s, w.mac.id, func(c *opCtx) error { return c.deviceSetPublic(w.build.id, w.studio) }); err != nil {
		t.Fatal(err)
	}
	if d := s.state.Load().w.devices[w.build.id]; d.PublicLAN != w.studio {
		t.Fatalf("public LAN %q", d.PublicLAN)
	}
	// Only in its LAN.
	w.mac.refused(proto.ErrInvalid, proto.OpLANAddDevice, proto.OpArgs{LAN: w.weekend, Device: w.build.id})
	// 阿杰 owns 周末联机, not 工作室: he cannot make his NAS public there.
	if err := internalOp(t, s, w.jnas.id, func(c *opCtx) error { return c.deviceSetPublic(w.jnas.id, w.studio) }); errCode(err) != proto.ErrNotFound {
		t.Fatalf("阿杰 making his NAS public in 工作室: %v", err)
	}
	// A member of 工作室 sees it but cannot manage it.
	mia := newEngine(t, w.mac.url, "mia", proto.MacOS)
	mia.login("mia@deneb.test")
	inv := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio, Person: mia.view().Me}).Result.Invite
	mia.must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	web := proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{proto.Port(proto.TCP, 80)}}
	if err := mia.refused(proto.ErrForbidden, proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: &web, On: true}); !strings.Contains(err.Message, "「工作室」的公共设备") {
		t.Fatalf("Mia switching a public device's service: %v", err)
	}
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: &web, On: true})
	// The owner shares it, with anyone.
	if _, err := shareDevice(t, s, w.mac.id, w.build.id, w.jie); err != nil {
		t.Fatal(err)
	}
	// Deleting 工作室 gives it back to its person.
	w.mac.must(proto.OpLANDelete, proto.OpArgs{LAN: w.studio})
	if d := s.state.Load().w.devices[w.build.id]; d.PublicLAN != "" {
		t.Fatalf("after deleting 工作室 the build server belongs to %q", d.PublicLAN)
	}
}

func sharedWithMe(v *proto.UserView, id proto.DeviceID) *proto.SharedDevice {
	for i := range v.SharedWithMe {
		if v.SharedWithMe[i].Device.ID == id {
			return &v.SharedWithMe[i]
		}
	}
	return nil
}

// TestShareViews goes through the device interface, as the client and the
// Web will: share.create, service.set on a share, share.revoke, undo,
// device.setPublic, and what each side's view and map say.
func TestShareViews(t *testing.T) {
	w := newM2World(t)
	s := w.s
	web := proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{proto.Port(proto.TCP, 80)}}
	ssh := proto.Service{Kind: proto.ServiceShell, Ports: []proto.PortRange{proto.Port(proto.TCP, 22)}}
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Device: w.build.id, LAN: w.studio, Service: &web, On: true})

	res := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie, Services: []proto.ServiceKey{web.Key()}})
	id := res.Result.Share
	if id == "" {
		t.Fatalf("share.create answered %+v", res)
	}
	w.mac.refused(proto.ErrConflict, proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie})

	// 阿杰's "共享给我": the build server, from me, with the web site only.
	sd := sharedWithMe(w.jdesk.view(), w.build.id)
	if sd == nil || sd.Share != id || sd.From != w.me || len(sd.Services) != 1 || sd.Services[0].Key() != web.Key() || len(sd.Device.LANs) != 0 || sd.FromLAN != "" {
		t.Fatalf("阿杰's shared device: %+v", sd)
	}
	// Mine: whom the build server is given to; one seat more.
	kv := w.mac.view()
	if dv := deviceView(kv, w.build.id); len(dv.Shares) != 1 || dv.Shares[0].Person != w.jie || dv.Shares[0].ID != id {
		t.Fatalf("my build server's shares: %+v", dv.Shares)
	}
	if kv.Seats.Used != 2 || kv.Seats.Left != 4 {
		t.Fatalf("my seats: %+v", kv.Seats)
	}
	// The receiver's side only connects out; the shared side opens the web.
	if sc := scopeByID(netMapOf(s, w.jdesk.id), proto.ShareScope(id)); sc == nil || !sc.NoInbound || sc.Kind != proto.ScopeShare {
		t.Fatalf("阿杰's side of the share: %+v", sc)
	}
	if sc := scopeByID(netMapOf(s, w.build.id), proto.ShareScope(id)); sc == nil || sc.NoInbound || sc.Kind != proto.ScopeShare || len(sc.Open) != 1 {
		t.Fatalf("the build server's side of the share: %+v", sc)
	}
	if sc := scopeByID(netMapOf(s, w.build.id), proto.LANScope(w.studio)); sc == nil || sc.Kind != proto.ScopeLAN {
		t.Fatalf("a LAN scope: %+v", sc)
	}

	// Another service in the share; 阿杰 cannot touch it.
	w.mac.must(proto.OpServiceSet, proto.OpArgs{Share: id, Service: &ssh, On: true})
	if sd := sharedWithMe(w.jdesk.view(), w.build.id); len(sd.Services) != 2 {
		t.Fatalf("after opening the shell 阿杰 sees %+v", sd.Services)
	}
	w.jdesk.refused(proto.ErrNotFound, proto.OpServiceSet, proto.OpArgs{Share: id, Service: &ssh})

	// Taken back, and undone.
	w.mac.must(proto.OpShareRevoke, proto.OpArgs{Share: id})
	if sharedWithMe(w.jdesk.view(), w.build.id) != nil || len(deviceView(w.mac.view(), w.build.id).Shares) != 0 {
		t.Fatal("the revoked share still shows")
	}
	if res := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: w.jie}); res.Result.Share != id {
		t.Fatalf("undo made share %q, not %q", res.Result.Share, id)
	}
	if sd := sharedWithMe(w.jdesk.view(), w.build.id); sd == nil || len(sd.Services) != 2 {
		t.Fatalf("after undo 阿杰 sees %+v", sd)
	}

	// Made public in 工作室: 阿杰 sees where it comes from.
	w.mac.must(proto.OpDeviceSetPublic, proto.OpArgs{Device: w.build.id, LAN: w.studio})
	if dv := deviceView(w.mac.view(), w.build.id); dv.PublicLAN != w.studio {
		t.Fatalf("the public build server: %+v", dv)
	}
	if sd := sharedWithMe(w.jdesk.view(), w.build.id); sd == nil || sd.FromLAN != "工作室" || sd.Device.PublicLAN != w.studio {
		t.Fatalf("阿杰's shared public device: %+v", sd)
	}
	w.jdesk.refused(proto.ErrNotFound, proto.OpDeviceSetPublic, proto.OpArgs{Device: w.build.id})
}

// TestShareReleaseAndLeave: a revoked share keeps its seat only until the
// client says the undo is gone (share.release); a receiver can give a share
// up (share.leave), and the giver just sees it go.
func TestShareReleaseAndLeave(t *testing.T) {
	s, ts, _ := newSeatServer(t, 2)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	build := newEngine(t, ts.URL, "build", proto.Linux)
	desk := newEngine(t, ts.URL, "desktop", proto.Windows)
	leo := newEngine(t, ts.URL, "leo", proto.Windows)
	mac.login("kittors@deneb.test")
	build.login("kittors@deneb.test")
	desk.login("jie@deneb.test")
	leo.login("leo@deneb.test")
	jie, leoID := desk.view().Me, leo.view().Me

	id := mac.must(proto.OpShareCreate, proto.OpArgs{Device: build.id, Person: jie}).Result.Share
	mac.refused(proto.ErrSeatsFull, proto.OpShareCreate, proto.OpArgs{Device: build.id, Person: leoID})
	mac.must(proto.OpShareRevoke, proto.OpArgs{Share: id})
	// The toast is up: the seat is still 阿杰's.
	mac.refused(proto.ErrSeatsFull, proto.OpShareCreate, proto.OpArgs{Device: build.id, Person: leoID})
	// The toast went untouched: the client releases, the seat is free at
	// once, no ten seconds to wait.
	desk.refused(proto.ErrNotFound, proto.OpShareRelease, proto.OpArgs{Share: id})
	mac.must(proto.OpShareRelease, proto.OpArgs{Share: id})
	leoShare := mac.must(proto.OpShareCreate, proto.OpArgs{Device: build.id, Person: leoID}).Result.Share
	if v := mac.view(); v.Seats.Used != 2 || slices.Contains(v.Seats.People, jie) {
		t.Fatalf("after releasing 阿杰's seat: %+v", v.Seats)
	}

	// Leo gives it up: gone from both sides, no word to me, and the seat
	// is free with no hold.
	mac.refused(proto.ErrNotFound, proto.OpShareLeave, proto.OpArgs{Share: leoShare})
	leo.must(proto.OpShareLeave, proto.OpArgs{Share: leoShare})
	if sharedWithMe(leo.view(), build.id) != nil {
		t.Fatal("Leo still has the device he gave up")
	}
	if dv := deviceView(mac.view(), build.id); len(dv.Shares) != 0 {
		t.Fatalf("my build server still lists %+v", dv.Shares)
	}
	if peerOf(netMapOf(s, build.id), leo.id) != nil {
		t.Fatal("the build server still sees Leo's device")
	}
	if _, err := shareDevice(t, s, mac.id, mac.id, jie); err != nil {
		t.Fatalf("Leo's seat did not come free: %v", err)
	}
}

// TestShareMode is scene 4 as the server sees it: 工作室 for sharing
// devices; members come in by invite as ever but bring no device and see
// nothing of the LAN; the owner gives them devices one by one.
func TestShareMode(t *testing.T) {
	w := newM2World(t)
	s := w.s
	mia := newEngine(t, w.mac.url, "mia", proto.MacOS)
	mia.login("mia@deneb.test")
	miaID := mia.view().Me
	// 阿杰 is in 工作室 with his desktop, as a LAN, then it turns to sharing.
	inv := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio, Person: w.jie}).Result.Invite
	w.jdesk.must(proto.OpInviteAccept, proto.OpArgs{Invitation: inv.ID})
	if peerOf(netMapOf(s, w.build.id), w.jdesk.id) == nil {
		t.Fatal("as a LAN, the build server does not see 阿杰's desktop")
	}
	w.jdesk.refused(proto.ErrForbidden, proto.OpLANSetMode, proto.OpArgs{LAN: w.studio, Mode: proto.LANModeShare})
	w.mac.must(proto.OpLANSetMode, proto.OpArgs{LAN: w.studio, Mode: proto.LANModeShare})

	// 阿杰's desktop left 工作室; he is still a member, seeing its name and
	// owner only.
	if p := peerOf(netMapOf(s, w.build.id), w.jdesk.id); p != nil {
		t.Fatalf("in a sharing LAN the build server sees 阿杰's desktop: %+v", p)
	}
	jv := lanByName(w.jdesk.view(), "工作室")
	if jv == nil || jv.Mode != proto.LANModeShare || len(jv.Devices) != 0 || len(jv.Members) != 2 {
		t.Fatalf("阿杰's view of the sharing 工作室: %+v", jv)
	}
	w.jdesk.refused(proto.ErrInvalid, proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: w.jdesk.id})
	// A new member comes in by invite, with no device.
	minv := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.studio, Person: miaID}).Result.Invite
	mia.must(proto.OpInviteAccept, proto.OpArgs{Invitation: minv.ID})
	if nm := netMapOf(s, mia.id); len(nm.Scopes) != 0 || len(nm.Peers) != 0 {
		t.Fatalf("Mia's device, a member of the sharing 工作室: %+v", nm.Scopes)
	}
	// The owner sees the members (the admin's member rows) and the devices.
	kv := lanByName(w.mac.view(), "工作室")
	if len(kv.Members) != 3 || len(kv.Devices) != 2 {
		t.Fatalf("my view of the sharing 工作室: %d members, %d devices", len(kv.Members), len(kv.Devices))
	}
	if v := w.mac.view(); v.Seats.Used != 3 {
		t.Fatalf("my seats: %+v", v.Seats)
	}

	// The build server, a public device of 工作室, goes to Mia: she has it
	// and only it, from "工作室".
	w.mac.must(proto.OpDeviceSetPublic, proto.OpArgs{Device: w.build.id, LAN: w.studio})
	w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: miaID})
	sd := sharedWithMe(mia.view(), w.build.id)
	if sd == nil || sd.FromLAN != "工作室" {
		t.Fatalf("Mia's shared device: %+v", sd)
	}
	if nm := netMapOf(s, mia.id); len(nm.Peers) != 1 || nm.Peers[0].ID != w.build.id {
		t.Fatalf("Mia's device sees %v", peerIDs(nm))
	}
	// Back to a LAN: members bring their devices back themselves.
	w.mac.must(proto.OpLANSetMode, proto.OpArgs{LAN: w.studio, Mode: proto.LANModeLAN})
	w.jdesk.must(proto.OpLANAddDevice, proto.OpArgs{LAN: w.studio, Device: w.jdesk.id})
}
