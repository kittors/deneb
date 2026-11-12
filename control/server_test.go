package control

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// newTestServer starts a development server on a fresh directory.
func newTestServer(t *testing.T, dir string) (*Server, *httptest.Server) {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{
		DataDir:   dir,
		PublicURL: "http://" + ts.Listener.Addr().String(),
		Dev:       true,
		Seed:      DevSeed(),
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
		s.Close()
	})
	return s, ts
}

// engine is a fake engine: it speaks the device interface the way a real
// one would.
type engine struct {
	t     *testing.T
	url   string
	mk    proto.MachinePrivate
	nk    proto.NodePublic
	dk    proto.DiscoPublic
	token string
	id    proto.DeviceID
	host  string
	os    proto.OS
	caps  []string // claimed at login
	n     int      // op ids
}

func newEngine(t *testing.T, url, host string, os proto.OS) *engine {
	e := &engine{t: t, url: url, mk: proto.NewMachineKey(), host: host, os: os}
	rand.Read(e.nk[:])
	rand.Read(e.dk[:])
	return e
}

// call does one request and decodes the answer into out, or the error
// body on a non-2xx status.
func (e *engine) call(method, path string, in, out any) (int, *proto.Error) {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, body)
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var eb proto.ErrorBody
		if err := json.Unmarshal(b, &eb); err != nil || eb.Error == nil {
			e.t.Fatalf("%s %s: %s: %s", method, path, resp.Status, b)
		}
		return resp.StatusCode, eb.Error
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return resp.StatusCode, nil
}

func (e *engine) auth() {
	e.t.Helper()
	var ch proto.AuthChallenge
	if _, err := e.call("POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: e.mk.Public()}, &ch); err != nil {
		e.t.Fatal(err)
	}
	var tok proto.AuthToken
	if _, err := e.call("POST", proto.PathAuthToken, proto.AuthTokenRequest{
		MachineKey: e.mk.Public(), Challenge: ch.Challenge, Signature: e.mk.SignChallenge(ch.Challenge),
	}, &tok); err != nil {
		e.t.Fatal(err)
	}
	e.token = tok.Token
}

func (e *engine) loginRequest() proto.LoginRequest {
	return proto.LoginRequest{NodeKey: e.nk, DiscoKey: e.dk, Hostname: e.host, OS: e.os, EngineVersion: "test", Caps: e.caps}
}

// login logs the engine in as email the way the command line would: ask,
// get a link, have the person approve it, wait.
func (e *engine) login(email string) {
	e.t.Helper()
	e.auth()
	var st proto.LoginState
	if _, err := e.call("POST", proto.PathLogin, e.loginRequest(), &st); err != nil {
		e.t.Fatal(err)
	}
	if st.LoggedIn || !strings.Contains(st.LoginURL, "/login?c=") {
		e.t.Fatalf("login: %+v", st)
	}
	done := make(chan proto.LoginState)
	go func() {
		var w proto.LoginState
		e.call("POST", proto.PathLoginWait, nil, &w)
		done <- w
	}()
	time.Sleep(20 * time.Millisecond) // let the wait start: it must wake, not poll
	if _, err := e.call("POST", "/dev/approve", map[string]string{"login": st.LoginURL, "email": email}, nil); err != nil {
		e.t.Fatal(err)
	}
	select {
	case w := <-done:
		if !w.LoggedIn || w.Device == "" {
			e.t.Fatalf("login wait: %+v", w)
		}
		e.id = w.Device
	case <-time.After(5 * time.Second):
		e.t.Fatal("login wait did not wake up")
	}
}

func (e *engine) op(op proto.OpType, a proto.OpArgs) (proto.OpResult, *proto.Error) {
	e.t.Helper()
	e.n++
	var res proto.OpResult
	_, err := e.call("POST", proto.PathOps, proto.Op{ID: fmt.Sprintf("%s-%d", e.host, e.n), Op: op, Args: a}, &res)
	return res, err
}

// must runs an op that has to go through.
func (e *engine) must(op proto.OpType, a proto.OpArgs) proto.OpResult {
	e.t.Helper()
	res, err := e.op(op, a)
	if err != nil {
		e.t.Fatalf("%s %+v: %v", op, a, err)
	}
	return res
}

// refused runs an op that has to be refused with code.
func (e *engine) refused(code proto.ErrorCode, op proto.OpType, a proto.OpArgs) *proto.Error {
	e.t.Helper()
	_, err := e.op(op, a)
	if err == nil || err.Code != code {
		e.t.Fatalf("%s %+v: got %v, want %s", op, a, err, code)
	}
	return err
}

func (e *engine) view() *proto.UserView {
	e.t.Helper()
	var v proto.UserView
	if _, err := e.call("GET", proto.PathUserView, nil, &v); err != nil {
		e.t.Fatal(err)
	}
	return &v
}

func (e *engine) report(r proto.Report) uint64 {
	e.t.Helper()
	var res proto.ReportResult
	if _, err := e.call("POST", proto.PathReport, r, &res); err != nil {
		e.t.Fatal(err)
	}
	return res.Version
}

// netMap is the device's map as the server has it now.
func netMapOf(s *Server, id proto.DeviceID) *proto.NetMap {
	st := s.state.Load()
	return st.w.netMap(id, st.v, s.hub.onlineSnapshot())
}

func peerIDs(nm *proto.NetMap) []proto.DeviceID {
	var out []proto.DeviceID
	for _, p := range nm.Peers {
		out = append(out, p.ID)
	}
	return out
}

func peerOf(nm *proto.NetMap, id proto.DeviceID) *proto.Peer {
	for i := range nm.Peers {
		if nm.Peers[i].ID == id {
			return &nm.Peers[i]
		}
	}
	return nil
}

func lanByName(v *proto.UserView, name string) *proto.LANView {
	for i := range v.LANs {
		if v.LANs[i].Name == name {
			return &v.LANs[i]
		}
	}
	return nil
}

func deviceView(v *proto.UserView, id proto.DeviceID) *proto.DeviceView {
	for i := range v.Devices {
		if v.Devices[i].ID == id {
			return &v.Devices[i]
		}
	}
	return nil
}

func sorted(ids ...proto.DeviceID) []proto.DeviceID {
	slices.Sort(ids)
	return ids
}

// TestM1Topology walks the acceptance topology of implementation.md §3 M1
// through the device interface: two people, two LANs, a computer in both.
func TestM1Topology(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "kittors-MacBook-Pro.local", proto.MacOS)
	build := newEngine(t, ts.URL, "hk-155", proto.Linux)
	jdesk := newEngine(t, ts.URL, "DESKTOP-JIE", proto.Windows)
	jnas := newEngine(t, ts.URL, "nas", proto.Linux)
	mac.login("kittors@deneb.test")
	build.login("kittors@deneb.test")
	jdesk.login("jie@deneb.test")
	jnas.login("JIE@deneb.test ")

	// Addresses come from the deployment's /16, never 100.64/10, one each.
	n := s.Network()
	seen := map[netip.Addr]bool{}
	for _, e := range []*engine{mac, build, jdesk, jnas} {
		nm := netMapOf(s, e.id)
		a := nm.Self.Addrs[0].Addr()
		if !n.IPv4.Contains(a) || seen[a] || netip.MustParsePrefix("100.64.0.0/10").Contains(a) {
			t.Fatalf("%s got address %s in %s", e.host, a, n.IPv4)
		}
		seen[a] = true
		if !n.IPv6.Contains(nm.Self.Addrs[1].Addr()) {
			t.Fatalf("%s got IPv6 %s outside %s", e.host, nm.Self.Addrs[1], n.IPv6)
		}
	}
	if nm := netMapOf(s, mac.id); nm.Self.FQDN != "kittors-macbook-pro.kittors.deneb.internal" || nm.Self.Kind != proto.KindLaptop {
		t.Fatalf("mac's own node: %+v", nm.Self)
	}

	// 工作室, made on the Mac: the Mac is in it at once.
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: " 工作室 "}).Result.LAN
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: studio, Device: build.id})
	// 周末联机, made by 阿杰 on his desktop; his NAS goes in from there.
	weekend := jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	jdesk.must(proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: jnas.id})
	// M1 had no approvals: 阿杰 turns "新设备要同意" off, so the members'
	// devices below come straight in. The M2 default (on) is checked in
	// approval_test.go.
	jdesk.must(proto.OpLANSetApproveDevices, proto.OpArgs{LAN: weekend})

	// Only the owner invites; the link opens a card that names the LAN and
	// who asks, and nothing else.
	jnas.must(proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: jnas.id}) // already in: no change
	inv := jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}).Result.Invite
	if !strings.HasPrefix(inv.URL, ts.URL+"/i/") {
		t.Fatalf("invite link %q", inv.URL)
	}
	var pre proto.InvitePreview
	if _, err := mac.call("POST", proto.PathInvite, proto.InviteLookup{Invite: inv.URL}, &pre); err != nil {
		t.Fatal(err)
	}
	if pre.LANName != "周末联机" || pre.Inviter.DisplayName != "阿杰" || pre.Inviter.Email != "" || pre.State != proto.InvitePending || pre.AlreadyIn {
		t.Fatalf("invite preview: %+v", pre)
	}
	// Who is in, and how many devices: for the card's balls.
	if len(pre.Members) != 1 || pre.Members[0].DisplayName != "阿杰" || pre.Members[0].Email != "" || pre.Devices != 2 {
		t.Fatalf("invite preview's LAN: %d members %+v, %d devices", len(pre.Members), pre.Members, pre.Devices)
	}
	token, _ := proto.ParseInvite(inv.URL)
	if got := mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: proto.JoinLink(token)}); got.Result.LAN != weekend {
		t.Fatalf("accept answered %+v", got)
	}
	// A link lets in one person.
	mia := newEngine(t, ts.URL, "mia-air", proto.MacOS)
	mia.login("mia@deneb.test")
	if err := mia.refused(proto.ErrConflict, proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL}); !strings.Contains(err.Message, "用过") {
		t.Fatalf("second accept: %v", err)
	}

	// Who sees whom.
	want := map[*engine][]proto.DeviceID{
		mac:   sorted(build.id, jdesk.id, jnas.id),
		build: {mac.id},
		jdesk: sorted(mac.id, jnas.id),
		jnas:  sorted(mac.id, jdesk.id),
		mia:   nil,
	}
	for e, ids := range want {
		if got := peerIDs(netMapOf(s, e.id)); !slices.Equal(got, ids) {
			t.Errorf("%s sees %v, want %v", e.host, got, ids)
		}
	}
	nm := netMapOf(s, mac.id)
	if p := peerOf(nm, build.id); !slices.Equal(p.Scopes, []proto.ScopeID{proto.LANScope(studio)}) {
		t.Errorf("mac sees build through %v", p.Scopes)
	}
	if p := peerOf(nm, jnas.id); !slices.Equal(p.Scopes, []proto.ScopeID{proto.LANScope(weekend)}) {
		t.Errorf("mac sees the NAS through %v", p.Scopes)
	}
	// 阿杰's side knows nothing of 工作室: not its name, not the build
	// server, not that the Mac is in it.
	for _, e := range []*engine{jdesk, jnas} {
		b, _ := json.Marshal(netMapOf(s, e.id))
		if bytes.Contains(b, []byte("工作室")) || bytes.Contains(b, []byte(studio)) || bytes.Contains(b, []byte(build.id)) {
			t.Errorf("%s's map leaks 工作室: %s", e.host, b)
		}
	}
	jv := jdesk.view()
	jb, _ := json.Marshal(jv)
	if bytes.Contains(jb, []byte("工作室")) || bytes.Contains(jb, []byte(build.id)) || bytes.Contains(jb, []byte("kittors@deneb.test")) {
		t.Errorf("阿杰's view leaks: %s", jb)
	}
	if dv := deviceView(jv, mac.id); dv == nil || !slices.Equal(dv.LANs, []proto.LANID{weekend}) {
		t.Errorf("阿杰 sees the Mac as %+v", dv)
	}

	// My view: both LANs in the order I joined them, the build server in
	// 工作室 and in 周末联机's tray (mine, not in it).
	kv := mac.view()
	if len(kv.LANs) != 2 || kv.LANs[0].ID != studio || kv.LANs[1].ID != weekend {
		t.Fatalf("my LANs: %+v", kv.LANs)
	}
	if w := lanByName(kv, "周末联机"); len(w.Members) != 2 || w.Members[0].Role != proto.RoleOwner || w.Owner == kv.Me || w.Invites != nil {
		t.Errorf("周末联机 as I see it: %+v", w)
	}
	if dv := deviceView(kv, build.id); dv == nil || !slices.Equal(dv.LANs, []proto.LANID{studio}) {
		t.Errorf("my build server: %+v", dv)
	}
	if kv.Seats.Used != 1 || jv.Seats.Used != 2 || jv.Seats.Limit != 6 || !jv.Seats.Enforced {
		t.Errorf("seats: mine %+v, 阿杰's %+v", kv.Seats, jv.Seats)
	}

	// Only the owner of a device moves it, and the refusal says whose it is.
	if err := jdesk.refused(proto.ErrForbidden, proto.OpLANRemoveDevice, proto.OpArgs{LAN: weekend, Device: mac.id}); err.Message != "这是 kittors 的设备，只有 kittors 能移出" {
		t.Errorf("refusal reads %q", err.Message)
	}
	// A device 阿杰 cannot see does not exist for him.
	jdesk.refused(proto.ErrNotFound, proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: build.id})
	jdesk.refused(proto.ErrNotFound, proto.OpLANInvite, proto.OpArgs{LAN: studio})
	// From M2 any member may invite (the owner then approves who comes in);
	// whoever made an invite may take it back.
	mine := mac.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}).Result.Invite
	if mine.Inviter == jv.Me {
		t.Fatal("the member's invite says the owner made it")
	}
	mac.must(proto.OpInviteRevoke, proto.OpArgs{Invite: string(mine.ID)})

	// The build server in and out of 周末联机, and back (undo).
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: build.id})
	if p := peerOf(netMapOf(s, jnas.id), build.id); p == nil || !slices.Equal(p.Scopes, []proto.ScopeID{proto.LANScope(weekend)}) {
		t.Fatalf("the NAS sees the build server as %+v", p)
	}
	if p := peerOf(netMapOf(s, mac.id), build.id); !slices.Equal(p.Scopes, sortedScopes(studio, weekend)) {
		t.Fatalf("the Mac sees the build server through %v", p.Scopes)
	}
	mac.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: weekend, Device: build.id})
	if peerOf(netMapOf(s, jnas.id), build.id) != nil {
		t.Fatal("the NAS still sees the build server")
	}
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: build.id})

	// Services open per LAN: the web site to 工作室 only, then to both.
	web := &proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{proto.Port(proto.TCP, 80)}}
	mac.must(proto.OpServiceSet, proto.OpArgs{Device: build.id, LAN: studio, Service: web, On: true})
	scopes := map[proto.ScopeID][]proto.Service{}
	for _, sc := range netMapOf(s, build.id).Scopes {
		scopes[sc.ID] = sc.Open
	}
	if len(scopes[proto.LANScope(studio)]) != 1 || len(scopes[proto.LANScope(weekend)]) != 0 {
		t.Fatalf("build opens %v", scopes)
	}
	if lv := lanByName(jdesk.view(), "周末联机"); len(serviceOf(lv, build.id)) != 0 {
		t.Fatalf("阿杰 sees a service closed to him: %+v", serviceOf(lv, build.id))
	}
	mac.must(proto.OpServiceSet, proto.OpArgs{Device: build.id, LAN: weekend, Service: web, On: true})
	if svcs := serviceOf(lanByName(jdesk.view(), "周末联机"), build.id); len(svcs) != 1 || !svcs[0].On || svcs[0].DisplayName() != "网站" {
		t.Fatalf("阿杰 sees %+v", svcs)
	}
	// Out and back in keeps the switches.
	mac.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: weekend, Device: build.id})
	mac.must(proto.OpLANAddDevice, proto.OpArgs{LAN: weekend, Device: build.id})
	if svcs := serviceOf(lanByName(jdesk.view(), "周末联机"), build.id); len(svcs) != 1 {
		t.Fatalf("after undo 阿杰 sees %+v", svcs)
	}
	// The same switch again changes nothing and moves no version; it
	// answers with one the Mac has (TestNoChangeVersion).
	v0 := s.Version()
	if res := mac.must(proto.OpServiceSet, proto.OpArgs{Device: build.id, LAN: weekend, Service: web, On: true}); s.Version() != v0 || res.Version > v0 {
		t.Fatalf("a no-op moved the version from %d to %d, answered %d", v0, s.Version(), res.Version)
	}
	// Ports belong to one service.
	mac.refused(proto.ErrConflict, proto.OpServiceSet, proto.OpArgs{Device: build.id, LAN: weekend,
		Service: &proto.Service{Kind: proto.ServiceOther, Ports: []proto.PortRange{{Proto: proto.TCP, First: 79, Last: 81}}}, On: true})

	// 谁能用: on the Mac, not 阿杰; both ends are told.
	mac.must(proto.OpDeviceRefuse, proto.OpArgs{Device: mac.id, Person: jv.Me, Refuse: true})
	if p := peerOf(netMapOf(s, jdesk.id), mac.id); !p.RefusedByPeer || p.RefusedByMe {
		t.Errorf("阿杰's desktop sees the Mac as %+v", p)
	}
	if p := peerOf(netMapOf(s, mac.id), jdesk.id); !p.RefusedByMe || p.RefusedByPeer {
		t.Errorf("the Mac sees 阿杰's desktop as %+v", p)
	}
	if p := peerOf(netMapOf(s, mac.id), build.id); p.RefusedByMe || p.RefusedByPeer {
		t.Errorf("refusing 阿杰 touched my own build server: %+v", p)
	}
	if dv := deviceView(jdesk.view(), mac.id); !dv.RefusesMe {
		t.Error("阿杰's view does not say the Mac refuses him")
	}
	if dv := deviceView(mac.view(), mac.id); !slices.Equal(dv.Refused, []proto.PersonID{jv.Me}) {
		t.Errorf("my view of the Mac: refused %v", dv.Refused)
	}
	mac.refused(proto.ErrInvalid, proto.OpDeviceRefuse, proto.OpArgs{Device: mac.id, Person: kv.Me, Refuse: true})
	mac.must(proto.OpDeviceRefuse, proto.OpArgs{Device: mac.id, Person: jv.Me})
	if p := peerOf(netMapOf(s, jdesk.id), mac.id); p.RefusedByPeer {
		t.Error("the refusal did not go")
	}

	// A pause on the NAS: the others lose it at once, it keeps them, and
	// its own map says it paused.
	weekendScope := proto.LANScope(weekend)
	jnas.report(proto.Report{Endpoints: []netip.AddrPort{netip.MustParseAddrPort("198.51.100.4:41703")}, Paused: []proto.ScopeID{weekendScope, "lan/nope"}})
	// The paused NAS stays in the Mac's map, its scope moved to
	// PausedScopes, so the pause lifts without waiting on the server.
	if p := peerOf(netMapOf(s, mac.id), jnas.id); p == nil || len(p.Scopes) != 0 || !slices.Equal(p.PausedScopes, []proto.ScopeID{weekendScope}) {
		t.Errorf("the Mac sees the paused NAS as %+v", p)
	}
	nasMap := netMapOf(s, jnas.id)
	if peerOf(nasMap, mac.id) == nil || !nasMap.Scopes[0].Paused {
		t.Errorf("the paused NAS's own map: %+v", nasMap.Scopes)
	}
	jnas.report(proto.Report{Endpoints: []netip.AddrPort{netip.MustParseAddrPort("198.51.100.4:41703")}})
	if p := peerOf(netMapOf(s, mac.id), jnas.id); p == nil || len(p.Endpoints) != 1 {
		t.Errorf("after the pause the Mac sees the NAS as %+v", p)
	}
	// A report that changes nothing moves no version.
	if v := jnas.report(proto.Report{Endpoints: []netip.AddrPort{netip.MustParseAddrPort("198.51.100.4:41703")}}); v != s.Version() {
		t.Error("a repeated report moved the version")
	}

	// 阿杰 takes the NAS out; the Mac's 工作室 is untouched.
	before := peerOf(netMapOf(s, mac.id), build.id)
	jdesk.must(proto.OpLANRemoveDevice, proto.OpArgs{LAN: weekend, Device: jnas.id})
	after := netMapOf(s, mac.id)
	if peerOf(after, jnas.id) != nil || !slices.Equal(peerOf(after, build.id).Scopes, before.Scopes) {
		t.Errorf("after the NAS left the Mac sees %v", peerIDs(after))
	}

	// Leaving takes my devices out with me; the owner cannot leave.
	jdesk.refused(proto.ErrInvalid, proto.OpLANLeave, proto.OpArgs{LAN: weekend})
	mac.must(proto.OpLANLeave, proto.OpArgs{LAN: weekend})
	if got := peerIDs(netMapOf(s, jdesk.id)); len(got) != 0 {
		t.Errorf("after I left, 阿杰 still sees %v", got)
	}
	if lanByName(mac.view(), "周末联机") != nil {
		t.Error("周末联机 is still in my view")
	}
}

func sortedScopes(ls ...proto.LANID) []proto.ScopeID {
	var out []proto.ScopeID
	for _, l := range ls {
		out = append(out, proto.LANScope(l))
	}
	slices.Sort(out)
	return out
}

func serviceOf(lv *proto.LANView, id proto.DeviceID) []proto.ServiceState {
	for _, d := range lv.Devices {
		if d.Device == id {
			return d.Services
		}
	}
	return nil
}

func TestOpIdempotent(t *testing.T) {
	s, ts := newTestServer(t, "")
	e := newEngine(t, ts.URL, "mac", proto.MacOS)
	e.login("kittors@deneb.test")
	op := proto.Op{ID: "same", Op: proto.OpLANCreate, Args: proto.OpArgs{Name: "家"}}
	var a, b proto.OpResult
	e.call("POST", proto.PathOps, op, &a)
	e.call("POST", proto.PathOps, op, &b)
	if a.Version != b.Version || a.Result.LAN != b.Result.LAN {
		t.Fatalf("retry answered differently: %+v, %+v", a, b)
	}
	if n := len(e.view().LANs); n != 1 || s.Version() != a.Version {
		t.Fatalf("retry applied again: %d LANs, version %d", n, s.Version())
	}
	// A refusal is answered the same way twice too.
	bad := proto.Op{ID: "bad", Op: proto.OpLANRename, Args: proto.OpArgs{LAN: "l_nope", Name: "x"}}
	_, e1 := e.call("POST", proto.PathOps, bad, nil)
	_, e2 := e.call("POST", proto.PathOps, bad, nil)
	if e1 == nil || e2 == nil || *e1 != *e2 {
		t.Fatalf("refusals: %v, %v", e1, e2)
	}
	if _, err := e.call("POST", proto.PathOps, proto.Op{Op: proto.OpLANCreate}, nil); err == nil || err.Code != proto.ErrInvalid {
		t.Fatalf("an op without an id: %v", err)
	}
}

// stream reads frames off a device's stream.
type stream struct {
	frames chan proto.StreamFrame
	cancel func()
}

func (e *engine) stream(t *testing.T, view bool) *stream {
	ctx, cancel := context.WithCancel(context.Background())
	q := ""
	if view {
		q = "?userView=1"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+proto.PathStream+q, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("stream: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	st := &stream{frames: make(chan proto.StreamFrame, 16), cancel: cancel}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var f proto.StreamFrame
			if err := json.Unmarshal(sc.Bytes(), &f); err == nil {
				st.frames <- f
			}
		}
		close(st.frames)
	}()
	t.Cleanup(cancel)
	return st
}

func (st *stream) next(t *testing.T) proto.StreamFrame {
	t.Helper()
	select {
	case f, ok := <-st.frames:
		if !ok {
			t.Fatal("stream ended")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5 s")
	}
	panic("unreachable")
}

// TestNoChangeVersion: an op that changes nothing answers with the newest
// version the asker's streams were sent, not the current one, which may be
// a change it never hears of: an engine waits for the version an op
// answers with, up to 3 s. A change it did hear of is in the answer.
func TestNoChangeVersion(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	jdesk := newEngine(t, ts.URL, "jdesk", proto.Windows)
	mac.login("kittors@deneb.test")
	jdesk.login("jie@deneb.test")
	weekend := jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	inv := jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}).Result.Invite
	mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	again := proto.OpArgs{LAN: weekend, Device: mac.id} // in already: no change

	// No stream open: nothing to wait for.
	if v := mac.must(proto.OpLANAddDevice, again).Version; v != 0 {
		t.Fatalf("no change, no stream: version %d", v)
	}

	// The stream opens; the Mac coming online is a change of its own,
	// published a moment later. has is the newest version the Mac got.
	before := s.Version()
	st := mac.stream(t, false)
	has := st.next(t).Version
	for deadline := time.Now().Add(5 * time.Second); s.Version() == before; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("coming online was never published")
		}
	}
	drain := func() {
		for {
			select {
			case f := <-st.frames:
				has = f.Version
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}
	drain()

	// 阿杰's new invite changes only his view: the current version moves
	// past the Mac's, and the Mac's no-change op does not ask it to wait
	// for a version it will never get.
	jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend})
	if s.Version() <= has {
		t.Fatalf("the current version %d did not move past the Mac's %d", s.Version(), has)
	}
	if v := mac.must(proto.OpLANAddDevice, again).Version; v != has {
		t.Fatalf("no change: version %d, want the Mac's %d (the current one is %d)", v, has, s.Version())
	}

	// 阿杰 renames 周末联机, which the Mac hears; then another invite it
	// does not. The Mac's no-change op answers with the rename.
	renamed := jdesk.must(proto.OpLANRename, proto.OpArgs{LAN: weekend, Name: "周末"}).Version
	if f := st.next(t); f.Version != renamed {
		t.Fatalf("the rename reached the Mac as version %d, not %d", f.Version, renamed)
	}
	jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend})
	if v := mac.must(proto.OpLANAddDevice, again).Version; v != renamed || s.Version() <= renamed {
		t.Fatalf("no change after the rename: version %d, want %d (the current one is %d)", v, renamed, s.Version())
	}

	// From a browser: the newest version its person's browser streams were
	// sent, 0 with none open.
	b := newWebBrowser(t, ts.URL)
	b.login("kittors@deneb.test", DevCode)
	if st, r := b.op(proto.OpLANAddDevice, again); st != 200 || r.Version != 0 {
		t.Fatalf("no change from a browser with no stream: %d, version %d", st, r.Version)
	}
	me := s.state.Load().w.byEmail["kittors@deneb.test"].ID
	tab := &sub{person: me, view: true, notify: make(chan struct{}, 1)}
	if _, ok := s.hub.add(tab); !ok {
		t.Fatal("no room for the tab")
	}
	defer s.hub.remove(tab, nil)
	now := s.state.Load()
	s.hub.prime(tab, now.w, now.v)
	jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend})
	if st, r := b.op(proto.OpLANAddDevice, again); st != 200 || r.Version != now.v || s.Version() <= now.v {
		t.Fatalf("no change from a browser with a tab open: %d, version %d, want %d (the current one is %d)", st, r.Version, now.v, s.Version())
	}
}

func TestStream(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	jdesk := newEngine(t, ts.URL, "jdesk", proto.Windows)
	mac.login("kittors@deneb.test")
	jdesk.login("jie@deneb.test")
	weekend := jdesk.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	inv := jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}).Result.Invite

	st := mac.stream(t, true)
	first := st.next(t)
	if first.NetMap == nil || first.UserView == nil || first.Version != first.NetMap.Version || first.Version != first.UserView.Version {
		t.Fatalf("first frame: %+v", first)
	}
	if len(first.NetMap.Peers) != 0 || !first.NetMap.Self.Online {
		t.Fatalf("first frame's map: %+v", first.NetMap)
	}

	// The op's version is the frame's: the client drops its overlay on it.
	res := mac.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	f := st.next(t)
	if f.Version != res.Version || len(f.NetMap.Peers) != 1 || lanByName(f.UserView, "周末联机") == nil {
		t.Fatalf("after accepting: version %d (op said %d), peers %v", f.Version, res.Version, peerIDs(f.NetMap))
	}
	if f.NetMap.Peers[0].Online {
		t.Fatal("阿杰's desktop shows online without a stream")
	}

	// 阿杰's desktop opens its stream: the Mac hears it came online.
	jst := jdesk.stream(t, false)
	if jf := jst.next(t); jf.UserView != nil || jf.NetMap == nil {
		t.Fatalf("a stream without userView=1 got %+v", jf)
	}
	if f := st.next(t); !f.NetMap.Peers[0].Online {
		t.Fatalf("the Mac did not hear 阿杰 come online: %+v", f.NetMap.Peers[0])
	}

	// A change only 阿杰 sees reaches only 阿杰.
	v := jdesk.must(proto.OpLANRename, proto.OpArgs{LAN: weekend, Name: "周末"}).Version
	if f := st.next(t); f.Version != v || f.NetMap.Scopes[0].Name != "周末" {
		t.Fatalf("rename: %+v", f.NetMap.Scopes)
	}
	jdesk.must(proto.OpLANInvite, proto.OpArgs{LAN: weekend}) // the owner's view only
	select {
	case f := <-st.frames:
		t.Fatalf("the Mac got a frame for 阿杰's new invite: version %d", f.Version)
	case <-time.After(200 * time.Millisecond):
	}

	// Logging out ends the stream with a frame that says so.
	var ls proto.LoginState
	mac.call("POST", proto.PathLogout, nil, &ls)
	if f := st.next(t); !f.LoggedOut {
		t.Fatalf("after logout: %+v", f)
	}
	if _, ok := <-st.frames; ok {
		t.Fatal("the stream went on after logging out")
	}
	if peerOf(netMapOf(s, jdesk.id), mac.id) != nil {
		t.Fatal("阿杰 still sees the logged-out Mac")
	}
}

func TestReloginKeepsDevice(t *testing.T) {
	s, ts := newTestServer(t, "")
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	addr := netMapOf(s, mac.id).Self.Addrs[0]
	id := mac.id

	var ls proto.LoginState
	mac.call("POST", proto.PathLogout, nil, &ls)
	if _, err := mac.call("GET", proto.PathUserView, nil, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("a logged-out device read its view: %v", err)
	}
	// The same person again: same device, same address, same LANs.
	mac.login("kittors@deneb.test")
	nm := netMapOf(s, mac.id)
	if mac.id != id || nm.Self.Addrs[0] != addr || len(nm.Scopes) != 1 || nm.Scopes[0].ID != proto.LANScope(studio) {
		t.Fatalf("after re-login: id %s (was %s), %v, scopes %v", mac.id, id, nm.Self.Addrs, nm.Scopes)
	}

	// Someone else on the same machine: a new device; the old address
	// waits out its cooldown.
	mac.call("POST", proto.PathLogout, nil, &ls)
	mac.login("mia@deneb.test")
	nm = netMapOf(s, mac.id)
	if mac.id == id || nm.Self.Addrs[0] == addr || len(nm.Scopes) != 0 {
		t.Fatalf("another person got the old device: %s, %v", mac.id, nm.Self.Addrs)
	}
	other := newEngine(t, ts.URL, "other", proto.Linux)
	other.login("leo@deneb.test")
	if a := netMapOf(s, other.id).Self.Addrs[0]; a == addr {
		t.Fatalf("the released address %s was given out again at once", a)
	}

	// Already logged in: login answers so, and takes a new node key.
	mac.nk[0] ^= 0xff
	var st proto.LoginState
	mac.call("POST", proto.PathLogin, mac.loginRequest(), &st)
	if !st.LoggedIn || st.Device != mac.id || netMapOf(s, mac.id).Self.NodeKey != mac.nk {
		t.Fatalf("login while logged in: %+v", st)
	}
	// A node key another device holds is refused.
	dup := newEngine(t, ts.URL, "dup", proto.Linux)
	dup.nk = other.nk
	dup.auth()
	var dst proto.LoginState
	dup.call("POST", proto.PathLogin, dup.loginRequest(), &dst)
	code := dst.LoginURL[strings.Index(dst.LoginURL, "c=")+2:]
	if _, err := dup.call("POST", "/dev/approve", map[string]string{"login": code, "email": "leo@deneb.test"}, nil); err == nil || err.Code != proto.ErrConflict {
		t.Fatalf("a second device with the same node key: %v", err)
	}
}

func TestAuth(t *testing.T) {
	_, ts := newTestServer(t, "")
	e := newEngine(t, ts.URL, "mac", proto.MacOS)
	var ch proto.AuthChallenge
	e.call("POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: e.mk.Public()}, &ch)

	// Someone else's key cannot answer it, and an answer counts once.
	other := proto.NewMachineKey()
	if _, err := e.call("POST", proto.PathAuthToken, proto.AuthTokenRequest{
		MachineKey: e.mk.Public(), Challenge: ch.Challenge, Signature: other.SignChallenge(ch.Challenge)}, nil); err == nil {
		t.Fatal("a wrong signature got a token")
	}
	var ch2 proto.AuthChallenge
	e.call("POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: e.mk.Public()}, &ch2)
	req := proto.AuthTokenRequest{MachineKey: e.mk.Public(), Challenge: ch2.Challenge, Signature: e.mk.SignChallenge(ch2.Challenge)}
	if _, err := e.call("POST", proto.PathAuthToken, req, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.call("POST", proto.PathAuthToken, req, nil); err == nil || err.Code != proto.ErrUnauthorized {
		t.Fatalf("a replayed answer: %v", err)
	}
	// A challenge made for one key does not serve another.
	if _, err := e.call("POST", proto.PathAuthToken, proto.AuthTokenRequest{
		MachineKey: other.Public(), Challenge: ch2.Challenge, Signature: other.SignChallenge(ch2.Challenge)}, nil); err == nil {
		t.Fatal("a challenge served another key")
	}

	e.token = "d1.garbage.garbage"
	if _, err := e.call("POST", proto.PathLogin, e.loginRequest(), nil); err == nil || err.Code != proto.ErrUnauthorized {
		t.Fatalf("a bad token: %v", err)
	}
	e.auth()
	if _, err := e.call("POST", proto.PathOps, proto.Op{ID: "x", Op: proto.OpLANCreate}, nil); err == nil || err.Code != proto.ErrNotLoggedIn {
		t.Fatalf("an op before login: %v", err)
	}

	k := newKeyring([]byte("secret"))
	now := time.Now()
	tok, _ := k.newToken(e.mk.Public(), now)
	if _, err := k.checkToken(tok, now.Add(tokenTTL)); err == nil {
		t.Fatal("an expired token passed")
	}
	if _, err := newKeyring([]byte("other")).checkToken(tok, now); err == nil {
		t.Fatal("a token passed under another secret")
	}
	if _, err := k.checkToken(strings.Replace(tok, "d1.", "s1.", 1), now); err == nil {
		t.Fatal("a token passed under another prefix")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s, ts := newTestServer(t, dir)
	mac := newEngine(t, ts.URL, "mac", proto.MacOS)
	mac.login("kittors@deneb.test")
	studio := mac.must(proto.OpLANCreate, proto.OpArgs{Name: "工作室"}).Result.LAN
	v, n := s.Version(), s.Network()
	ts.Close()
	s.Close()

	s2, err := New(Config{DataDir: dir, PublicURL: "http://x", Dev: true, Seed: DevSeed(), Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Version() < v || s2.Network().IPv4 != n.IPv4 || s2.Network().IPv6 != n.IPv6 {
		t.Fatalf("after restart: version %d (was %d), network %v (was %v)", s2.Version(), v, s2.Network(), n)
	}
	if nm := netMapOf(s2, mac.id); nm == nil || nm.Scopes[0].ID != proto.LANScope(studio) {
		t.Fatalf("after restart the Mac's map is %+v", nm)
	}
	// A restart with the same seed changes nothing.
	if s2.Version() != v {
		t.Fatalf("restarting with the same seed moved the version from %d to %d", v, s2.Version())
	}
}

// TestConcurrentOps runs ops from several devices at once; the race
// detector (go test -race) watches the hub and the world swaps.
func TestConcurrentOps(t *testing.T) {
	s, ts := newTestServer(t, "")
	var es []*engine
	for i, email := range []string{"kittors@deneb.test", "jie@deneb.test", "mia@deneb.test", "leo@deneb.test"} {
		e := newEngine(t, ts.URL, fmt.Sprintf("host%d", i), proto.Linux)
		e.login(email)
		es = append(es, e)
	}
	lan := es[0].must(proto.OpLANCreate, proto.OpArgs{Name: "大家"}).Result.LAN
	for _, e := range es[1:] {
		inv := es[0].must(proto.OpLANInvite, proto.OpArgs{LAN: lan}).Result.Invite
		e.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	}
	var streams []*stream
	for _, e := range es {
		streams = append(streams, e.stream(t, true))
	}
	var wg sync.WaitGroup
	for i, e := range es {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				e.n++
				svc := &proto.Service{Kind: proto.ServiceOther, Ports: []proto.PortRange{proto.Port(proto.TCP, uint16(1000+j))}}
				var res proto.OpResult
				e.call("POST", proto.PathOps, proto.Op{ID: fmt.Sprintf("c%d-%d", i, j), Op: proto.OpServiceSet,
					Args: proto.OpArgs{Device: e.id, LAN: lan, Service: svc, On: j%2 == 0}}, &res)
			}
		}()
	}
	wg.Wait()
	// Every stream ends on what the server has now: frames only go out when
	// something a device sees changed, so the last one it got must match.
	for i, st := range streams {
		var last proto.StreamFrame
		for {
			select {
			case f := <-st.frames:
				last = f
				continue
			case <-time.After(300 * time.Millisecond):
			}
			break
		}
		want := netMapOf(s, es[i].id)
		got := last.NetMap
		if got == nil {
			t.Fatalf("stream %d got no frame", i)
		}
		got.Version, want.Version = 0, 0
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if !bytes.Equal(gb, wb) {
			t.Fatalf("stream %d ended on\n%s\nbut the server has\n%s", i, gb, wb)
		}
		if last.Version > s.Version() || last.UserView.Version != last.Version {
			t.Fatalf("stream %d: frame version %d, view %d, server %d", i, last.Version, last.UserView.Version, s.Version())
		}
	}
}
