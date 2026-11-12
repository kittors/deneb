package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// relayCall is a relay program's request to the relay API.
func relayCall(t *testing.T, url, secret, path string, in, out any) (int, *proto.Error) {
	t.Helper()
	b, _ := json.Marshal(in)
	req, err := http.NewRequest("POST", url+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var eb proto.ErrorBody
		json.NewDecoder(resp.Body).Decode(&eb)
		if eb.Error == nil {
			eb.Error = &proto.Error{Message: resp.Status}
		}
		return resp.StatusCode, eb.Error
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, nil
}

// testRelay is a relay program as the server sees it.
type testRelay struct {
	t      *testing.T
	url    string
	secret string
}

const testPin = "sha256:abababababababababababababababababababababababababababababababab"

func registerRelay(t *testing.T, url, token string) (*testRelay, proto.RelayRegisterResult) {
	t.Helper()
	var reg proto.RelayRegisterResult
	if _, err := relayCall(t, url, "", proto.PathRelayRegister, proto.RelayRegisterRequest{
		Token: token, DERPPort: 8443, STUNPort: 3479, Pin: strings.ToUpper(testPin), Version: "test",
	}, &reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	return &testRelay{t: t, url: url, secret: reg.Secret}, reg
}

func (r *testRelay) admits(k proto.NodePublic) bool {
	r.t.Helper()
	return r.admit(k).Allow
}

func (r *testRelay) admit(k proto.NodePublic) proto.RelayAdmitResult {
	r.t.Helper()
	var res proto.RelayAdmitResult
	if _, err := relayCall(r.t, r.url, r.secret, proto.PathRelayAdmit, proto.RelayAdmitRequest{NodePublic: k}, &res); err != nil {
		r.t.Fatalf("admit: %v", err)
	}
	return res
}

func (r *testRelay) sync(req proto.RelaySyncRequest) proto.RelaySyncResult {
	r.t.Helper()
	var res proto.RelaySyncResult
	if _, err := relayCall(r.t, r.url, r.secret, proto.PathRelaySync, req, &res); err != nil {
		r.t.Fatalf("sync: %v", err)
	}
	return res
}

func myRelay(v *proto.UserView, id proto.RelayID) *proto.RelayView {
	for i := range v.MyRelays {
		if v.MyRelays[i].ID == id {
			return &v.MyRelays[i]
		}
	}
	return nil
}

func relayIn(v *proto.UserView, id proto.RelayID) *proto.Relay {
	for i := range v.Relays {
		if v.Relays[i].ID == id {
			return &v.Relays[i]
		}
	}
	return nil
}

func relayOf(nm *proto.NetMap, id proto.RelayID) *proto.Relay {
	for i := range nm.Relays {
		if nm.Relays[i].ID == id {
			return &nm.Relays[i]
		}
	}
	return nil
}

func (e *engine) relayUsage() proto.RelayUsage {
	e.t.Helper()
	var u proto.RelayUsage
	if _, err := e.call("GET", proto.PathRelayUsage, nil, &u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

func monthOf(u proto.RelayUsage, id proto.RelayID) *proto.RelayMonth {
	for i := range u.Relays {
		if u.Relays[i].Relay == id {
			return &u.Relays[i]
		}
	}
	return nil
}

// TestRelays is M4's server side for "我的服务器": I make a relay, its
// program registers, I drag it into my LANs; it lets in their devices
// and no one else's, its owner sees what it carried, and it goes when I
// take it out, leave, or delete it.
func TestRelays(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url

	res := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: " 香港 "}).Result
	if res == nil || res.Relay == "" || !strings.HasPrefix(res.RelayToken, "deneb-relay-") {
		t.Fatalf("relay.create: %+v", res)
	}
	r := res.Relay
	v := w.mac.view()
	if mv := myRelay(v, r); mv == nil || mv.Registered || mv.Online || mv.Seeded || len(mv.LANs) != 0 ||
		mv.TokenExpiresAt.Sub(w.clk.now()) != relayJoinLife {
		t.Fatalf("my new relay: %+v", mv)
	}
	if rl := relayIn(v, r); rl == nil || rl.Name != "香港" || rl.Owner != w.me || rl.Source != proto.RelayServer {
		t.Fatalf("my new relay in the view: %+v", rl)
	}
	if mv := myRelay(v, "hk"); mv == nil || !mv.Seeded || !mv.Registered {
		t.Fatalf("hk-spare's derper among my relays: %+v", mv)
	}
	// Dragged in before it is set up: on no list until it registers.
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	if lv := lanByName(w.mac.view(), "工作室"); !slices.Equal(lv.AddedRelays, []proto.RelayID{r}) || !slices.Equal(lv.Relays, []proto.RelayID{"hk"}) {
		t.Fatalf("工作室 before the relay registers: added %v, list %v", lv.AddedRelays, lv.Relays)
	}

	// The relay program registers; the token works once, and an
	// address-less request takes the address it came from.
	if code, err := relayCall(t, url, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: res.RelayToken, DERPPort: 8443, Pin: "sha256:nope"}, nil); code != 400 {
		t.Fatalf("a bad pin: %d %v", code, err)
	}
	relay, reg := registerRelay(t, url, res.RelayToken)
	if reg.Relay != r || reg.Name != "香港" || reg.Source != proto.RelayServer || reg.RateLimit != 0 {
		t.Fatalf("registered: %+v", reg)
	}
	if code, _ := relayCall(t, url, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: res.RelayToken, DERPPort: 8443, Pin: testPin}, nil); code != 401 {
		t.Fatalf("the token worked twice: %d", code)
	}
	nm := netMapOf(s, w.build.id)
	if sc := scopeByID(nm, proto.LANScope(w.studio)); sc == nil || !slices.Equal(sc.Relays, []proto.RelayID{r, "hk"}) {
		t.Fatalf("工作室's list once it registered: %+v", sc)
	}
	if rl := relayOf(nm, r); rl == nil || rl.Pin != testPin || rl.IPv4.String() != "127.0.0.1" || rl.HostName != "127.0.0.1" || rl.DERPPort != 8443 || rl.STUNPort != 3479 {
		t.Fatalf("the relay in the build server's map: %+v", rl)
	}
	if mv := myRelay(w.mac.view(), r); !mv.Registered || !mv.Online || !mv.TokenExpiresAt.IsZero() || !slices.Equal(mv.LANs, []proto.LANID{w.studio}) {
		t.Fatalf("my relay once registered: %+v", mv)
	}

	// Admission: 工作室's devices, no one else's.
	if !relay.admits(w.build.nk) || !relay.admits(w.mac.nk) {
		t.Fatal("my relay turns away my own devices in 工作室")
	}
	if relay.admits(w.jnas.nk) || relay.admits(w.jdesk.nk) || relay.admits(proto.NodePublic{1, 2, 3}) {
		t.Fatal("my relay lets in devices of no LAN it is in")
	}
	if a, b := relay.admit(w.jnas.nk), relay.admit(proto.NodePublic{1, 2, 3}); a.Reason != proto.RelayDenyNotListed || b.Reason != proto.RelayDenyLoggedOut {
		t.Fatalf("why not: %+v, %+v", a, b)
	}
	if code, _ := relayCall(t, url, "nope", proto.PathRelayAdmit, proto.RelayAdmitRequest{NodePublic: w.build.nk}, nil); code != 401 {
		t.Fatalf("admit with a wrong secret: %d", code)
	}

	// Dragged into 周末联机: 阿杰's devices get in. It stays mine, but
	// it is his LAN, so he may take it out again.
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: r})
	if !relay.admits(w.jnas.nk) {
		t.Fatal("dragged into 周末联机, my relay turns away 阿杰's NAS")
	}
	if err := w.jdesk.refused(proto.ErrForbidden, proto.OpRelayDelete, proto.OpArgs{Relay: r}); err.Message != "这是 kittors 的中转站，只有 kittors 能删除" {
		t.Fatalf("阿杰 deletes my relay: %q", err.Message)
	}
	w.jdesk.refused(proto.ErrForbidden, proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: r})
	w.jdesk.must(proto.OpRelayRemove, proto.OpArgs{LAN: w.weekend, Relay: r})
	if relay.admits(w.jnas.nk) {
		t.Fatal("taken out of 周末联机, my relay still lets in 阿杰's NAS")
	}
	// Already connected, the NAS is closed at the next sync.
	if sr := relay.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.build.nk, w.jnas.nk}}); !slices.Equal(sr.Deny, []proto.NodePublic{w.jnas.nk}) || sr.Name != "香港" {
		t.Fatalf("sync: %+v", sr)
	}

	// Where to reach a peer: the relays it is on that both may use.
	w.mac.report(proto.Report{HomeRelay: r, Relays: []proto.RelayID{r, "hk", "nope"}})
	if p := peerOf(netMapOf(s, w.build.id), w.mac.id); p == nil || !slices.Equal(p.Relays, []proto.RelayID{r, "hk"}) || p.HomeRelay != r {
		t.Fatalf("the build server reaches my Mac through %+v", p)
	}
	if p := peerOf(netMapOf(s, w.jnas.id), w.mac.id); p == nil || !slices.Equal(p.Relays, []proto.RelayID{"hk"}) || p.HomeRelay != "hk" {
		t.Fatalf("阿杰's NAS reaches my Mac through %+v", p)
	}
	if nm := netMapOf(s, w.jnas.id); relayOf(nm, r) != nil {
		t.Fatal("阿杰's NAS is told of my relay, which does not let it in")
	}

	// A share goes through the sharer's relay.
	mia := newEngine(t, url, "mia", proto.MacOS)
	mia.login("mia@deneb.test")
	sh := w.mac.must(proto.OpShareCreate, proto.OpArgs{Device: w.build.id, Person: mia.view().Me}).Result.Share
	if sc := scopeByID(netMapOf(s, mia.id), proto.ShareScope(sh)); sc == nil || !slices.Equal(sc.Relays, []proto.RelayID{r, "hk"}) {
		t.Fatalf("the share in Mia's map: %+v", sc)
	}
	if !relay.admits(mia.nk) {
		t.Fatal("my relay turns away Mia, whom I shared the build server with")
	}
	if rl := relayIn(mia.view(), r); rl == nil || rl.Owner != w.me {
		t.Fatalf("my relay in Mia's view: %+v", rl)
	}

	// What it carried: per device, summed by LAN and share.
	relay.sync(proto.RelaySyncRequest{Usage: []proto.RelayClientUsage{
		{Node: w.build.nk, Sent: 1000, Received: 500},
		{Node: w.mac.nk, Sent: 200, Received: 100},
		{Node: w.jnas.nk, Sent: 50},
		{Node: mia.nk, Sent: 10, Received: 20},
		{Node: proto.NodePublic{9}, Sent: 7},
	}})
	u := w.mac.relayUsage()
	m := monthOf(u, r)
	if u.Month != w.clk.now().UTC().Format("2006-01") || m == nil || m.Bytes != 1260 || monthOf(u, "hk") == nil {
		t.Fatalf("my relays' month: %+v", u)
	}
	if len(m.LANs) != 1 || m.LANs[0] != (proto.UsageLine{LAN: w.studio, Bytes: 1200}) {
		t.Fatalf("by LAN: %+v", m.LANs)
	}
	if len(m.Shares) != 1 || m.Shares[0] != (proto.UsageLine{Share: sh, Bytes: 30}) {
		t.Fatalf("by share: %+v", m.Shares)
	}
	wantDevices := []proto.UsageLine{{Device: w.build.id, Bytes: 1500}, {Device: w.mac.id, Bytes: 300}}
	slices.SortFunc(wantDevices, func(a, b proto.UsageLine) int { return strings.Compare(string(a.Device), string(b.Device)) })
	if !slices.Equal(m.Devices, wantDevices) {
		t.Fatalf("by device: %+v", m.Devices)
	}
	if u := w.jdesk.relayUsage(); len(u.Relays) != 0 {
		t.Fatalf("阿杰 has no relay, yet: %+v", u)
	}
	if next, err := s.relayUsage(s.state.Load().w, w.me, w.clk.now().AddDate(0, 1, 0)); err != nil || monthOf(*next, r).Bytes != 0 {
		t.Fatalf("next month: %+v, %v", next, err)
	}

	// Started again somewhere else, with a new certificate: the first
	// sync says so, and the maps follow without a new token.
	moved := "sha256:" + strings.Repeat("cd", 32)
	relay.sync(proto.RelaySyncRequest{Info: &proto.RelayInfo{IPv4: netip.MustParseAddr("203.0.113.7"), DERPPort: 9443, Pin: moved}})
	if rl := relayOf(netMapOf(s, w.build.id), r); rl.IPv4.String() != "203.0.113.7" || rl.HostName != "203.0.113.7" || rl.DERPPort != 9443 || rl.STUNPort != 0 || rl.Pin != moved {
		t.Fatalf("the relay after it moved: %+v", rl)
	}
	if code, err := relayCall(t, url, relay.secret, proto.PathRelaySync, proto.RelaySyncRequest{Info: &proto.RelayInfo{DERPPort: 9443, Pin: "nope"}}, nil); code != 400 {
		t.Fatalf("a sync with a bad pin: %d %v", code, err)
	}

	// Online until it stops syncing for a while.
	w.clk.add(relayOfflineAfter)
	s.sweep()
	if mv := myRelay(w.mac.view(), r); mv.Online || mv.LastSeen.IsZero() {
		t.Fatalf("after %s without a sync: %+v", relayOfflineAfter, mv)
	}
	relay.sync(proto.RelaySyncRequest{})
	if mv := myRelay(w.mac.view(), r); !mv.Online {
		t.Fatalf("synced again: %+v", mv)
	}

	// Renamed; set up again elsewhere, so the old program's secret stops.
	w.mac.must(proto.OpRelayRename, proto.OpArgs{Relay: r, Name: "香港二号"})
	if sr := relay.sync(proto.RelaySyncRequest{}); sr.Name != "香港二号" {
		t.Fatalf("renamed, the relay hears %q", sr.Name)
	}
	tok := w.mac.must(proto.OpRelayToken, proto.OpArgs{Relay: r}).Result.RelayToken
	if !relay.admits(w.build.nk) {
		t.Fatal("a new token stopped the old secret before it was used")
	}
	relay2, _ := registerRelay(t, url, tok)
	if code, _ := relayCall(t, url, relay.secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 401 {
		t.Fatalf("the old program's secret, after setting up again: %d", code)
	}

	// Leaving a LAN takes my relay out of it.
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: r})
	w.mac.must(proto.OpLANLeave, proto.OpArgs{LAN: w.weekend})
	if lv := lanByName(w.jdesk.view(), "周末联机"); len(lv.AddedRelays) != 0 || slices.Contains(lv.Relays, r) {
		t.Fatalf("周末联机 after I left: %+v %+v", lv.AddedRelays, lv.Relays)
	}

	// Deleted: off every list and view, its secret dead, its usage gone.
	w.mac.must(proto.OpRelayDelete, proto.OpArgs{Relay: r})
	if relayOf(netMapOf(s, w.build.id), r) != nil || relayIn(w.mac.view(), r) != nil || myRelay(w.mac.view(), r) != nil || monthOf(w.mac.relayUsage(), r) != nil {
		t.Fatal("a deleted relay is still about")
	}
	if code, _ := relayCall(t, url, relay2.secret, proto.PathRelayAdmit, proto.RelayAdmitRequest{NodePublic: w.build.nk}, nil); code != 401 {
		t.Fatalf("a deleted relay's secret: %d", code)
	}

	// The deployment's relay is not the client's to change, and its owner
	// is no one else's business.
	w.mac.refused(proto.ErrForbidden, proto.OpRelayRename, proto.OpArgs{Relay: "hk", Name: "x"})
	if err := w.jdesk.refused(proto.ErrForbidden, proto.OpRelayDelete, proto.OpArgs{Relay: "hk"}); strings.Contains(err.Message, "kittors") {
		t.Fatalf("阿杰 deletes the default relay: %q", err.Message)
	}
	w.jdesk.refused(proto.ErrNotFound, proto.OpRelayRename, proto.OpArgs{Relay: "r_nosuchrelay", Name: "x"})
	// Ten at most: hk-spare's derper and nine more.
	for range maxRelays - 1 {
		w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "又一个"})
	}
	w.mac.refused(proto.ErrInvalid, proto.OpRelayCreate, proto.OpArgs{Name: "太多了"})
}

// TestAdminRelays: the public relays are the operator's, through the admin
// socket; every device logged in may use them, at their rate limit,
// which the map says so the client can show it.
func TestAdminRelays(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	s, ts, sock := adminServer(t, t.TempDir(), DevSeed(), &lines, &mu)
	// A join code first: the relay comes into being when it is used.
	tok, err := AdminRelayAdd(sock, "东京", 2_000_000)
	if err != nil || tok.Relay != "" || tok.Name != "东京" || time.Until(tok.Expires) < relayJoinLife-time.Minute ||
		!strings.Contains(tok.Command, "--join "+tok.Code) || !strings.Contains(tok.Command, DefaultRelayImage) {
		t.Fatalf("relay-add: %+v, %v", tok, err)
	}
	if rs, err := AdminRelays(sock); err != nil || len(rs) != 1 || rs[0].ID != "hk" || !rs[0].Seeded || !rs[0].Default || rs[0].Owner != "kittors@deneb.test" {
		t.Fatalf("relays: %+v, %v", rs, err)
	}
	relay, reg := registerRelay(t, ts.URL, tok.Token)
	if reg.Source != proto.RelayPublic || reg.RateLimit != 2_000_000 || reg.Name != "东京" {
		t.Fatalf("registered: %+v", reg)
	}
	id := reg.Relay
	rs, err := AdminRelays(sock)
	if err != nil || len(rs) != 2 || rs[1].ID != id || rs[1].Source != proto.RelayPublic || !rs[1].Registered || rs[1].RateLimit != 2_000_000 || rs[1].Owner != "" {
		t.Fatalf("the new public relay: %+v, %v", rs, err)
	}

	login := func(host, email string) *engine {
		e := newEngine(t, ts.URL, host, proto.Linux)
		if _, err := AdminApprove(sock, startLogin(t, e), email); err != nil {
			t.Fatal(err)
		}
		var st proto.LoginState
		e.call("POST", proto.PathLogin, e.loginRequest(), &st)
		e.id = st.Device
		return e
	}
	nas := login("nas", "jie@deneb.test")
	// In no LAN yet, it may still use the public relay, and is told of it.
	if !relay.admits(nas.nk) {
		t.Fatal("the public relay turns away a device logged in")
	}
	nm := netMapOf(s, nas.id)
	if rl := relayOf(nm, id); rl == nil || rl.RateLimit != 2_000_000 || rl.Owner != "" || rl.Pin != testPin {
		t.Fatalf("the public relay in a map: %+v", rl)
	}
	if rl := relayOf(nm, "hk"); rl == nil || rl.Owner != "" {
		t.Fatalf("the default relay's owner shows to a stranger: %+v", rl)
	}
	l := nas.must(proto.OpLANCreate, proto.OpArgs{Name: "周末联机"}).Result.LAN
	if lv := lanByName(nas.view(), "周末联机"); lv.ID != l || !slices.Equal(lv.Relays, []proto.RelayID{"hk", id}) {
		t.Fatalf("a new LAN's list: the default, then the public one: %+v", lv.Relays)
	}
	nas.refused(proto.ErrForbidden, proto.OpRelayAdd, proto.OpArgs{LAN: l, Relay: id})
	nas.call("POST", proto.PathLogout, nil, nil)
	if relay.admits(nas.nk) {
		t.Fatal("the public relay lets in a device logged out")
	}

	if _, err := AdminRelayRate(sock, "hk", 1); err == nil {
		t.Fatal("a rate limit on someone's own relay")
	}
	if _, err := AdminRelayNewToken(sock, "hk"); err == nil {
		t.Fatal("a registration token for the seed's relay")
	}
	a, err := AdminRelayRate(sock, id, 1_000_000)
	if err != nil || a.RateLimit != 1_000_000 || !a.Online {
		t.Fatalf("relay-rate: %+v, %v", a, err)
	}
	if sr := relay.sync(proto.RelaySyncRequest{}); sr.RateLimit != 1_000_000 {
		t.Fatalf("the relay hears %d bytes/s", sr.RateLimit)
	}
	pc := login("pc", "mia@deneb.test")
	if rl := relayOf(netMapOf(s, pc.id), id); rl.RateLimit != 1_000_000 {
		t.Fatalf("the map says %d bytes/s", rl.RateLimit)
	}

	// The operator's weight, capacity and kicks on a public relay.
	two, five := 2, 5
	if a, err := AdminRelaySet(sock, id, &two, &five); err != nil || a.Weight != 2 || a.Capacity != 5 {
		t.Fatalf("relay-set: %+v, %v", a, err)
	}
	k, err := AdminRelayKick(sock, id, "mia@deneb.test", "")
	if err != nil || k == "" || relay.admits(pc.nk) {
		t.Fatalf("relay-kick: %q, %v", k, err)
	}
	if a := relay.admit(pc.nk); a.Reason != proto.RelayDenyKicked {
		t.Fatalf("why: %+v", a)
	}
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{Connections: 1, CPU: 0.5}})
	if rs, err := AdminRelays(sock); err != nil || rs[1].Kicks != 1 || rs[1].Load == nil || rs[1].Load.CPU != 0.5 {
		t.Fatalf("relays after the kick: %+v, %v", rs, err)
	}
	if _, err := AdminRelayKick(sock, id, "nobody@deneb.test", ""); err == nil {
		t.Fatal("kicked someone who is not there")
	}
	if err := AdminRelayUnkick(sock, k); err != nil || !relay.admits(pc.nk) {
		t.Fatalf("relay-unkick: %v", err)
	}
	if err := AdminRelayUnkick(sock, k); err == nil {
		t.Fatal("lifted twice")
	}
	mu.Lock()
	all := strings.Join(lines, "\n")
	mu.Unlock()
	for _, want := range []string{
		"admin: join code j_",
		"relay " + string(id) + " (东京) registered from 127.0.0.1",
		"relay " + string(id) + " (东京) turns away device " + string(nas.id) + " (logged out or run out)",
		"admin: relay " + string(id) + " now 1000000 bytes/s per connection",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the log lacks %q:\n%s", want, all)
		}
	}
}

// joinRegister registers a relay the way deneb-relay --join does: the
// token out of the code, the place it found out about itself.
func joinRegister(t *testing.T, s *Server, code string, info proto.RelayInfo) (int, proto.RelayRegisterResult) {
	t.Helper()
	control, pin, token, err := proto.ParseRelayJoinCode(code)
	if err != nil || control != s.cfg.PublicURL || pin != s.cfg.Pin {
		t.Fatalf("the code carries %q %q: %v", control, pin, err)
	}
	if info.DERPPort == 0 {
		info.DERPPort = 47445
	}
	if info.Pin == "" {
		info.Pin = testPin
	}
	var res proto.RelayRegisterResult
	code2, _ := relayCall(t, control, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: token, RelayInfo: info}, &res)
	return code2, res
}

// TestRelayJoin is "用我自己的服务器" with nothing to fill in: a join code
// and the command that carries it; the relay comes into being when the
// command runs, named after the region it reports; the code works once,
// within the hour, and can be taken back; "重新安装" keeps the relay.
func TestRelayJoin(t *testing.T) {
	w := newM2World(t)
	s := w.s
	s.cfg.Pin = testPin
	now := w.clk.now()

	j := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	if j == nil || !strings.HasPrefix(j.Code, proto.RelayJoinPrefix) || j.Relay != "" || !j.ExpiresAt.Equal(now.Add(relayJoinLife)) ||
		j.Command != "docker run -d --name deneb-relay --restart unless-stopped --network host --log-opt max-size=10m --log-opt max-file=3 -v deneb-relay:/data "+DefaultRelayImage+" --join "+j.Code+" --region auto" {
		t.Fatalf("relay.join: %+v", j)
	}
	v := w.mac.view()
	if len(v.RelayJoins) != 1 || v.RelayJoins[0].ID != j.ID || v.RelayJoins[0].Relay != "" || !v.RelayJoins[0].ExpiresAt.Equal(j.ExpiresAt) || len(v.MyRelays) != 1 {
		t.Fatalf("waiting for the install: joins %+v, relays %+v", v.RelayJoins, v.MyRelays)
	}
	if strings.Contains(fmt.Sprint(v), j.Code) {
		t.Fatal("the view shows the code again")
	}
	if len(w.jdesk.view().RelayJoins) != 0 {
		t.Fatal("阿杰 sees my join code")
	}

	// The command runs: a relay of mine named after its region, online.
	code, reg := joinRegister(t, s, j.Code, proto.RelayInfo{Region: "aws:ap-east-1"})
	if code != 200 || reg.Name != "香港" || reg.Source != proto.RelayServer || reg.Secret == "" {
		t.Fatalf("registered: %d %+v", code, reg)
	}
	r := reg.Relay
	v = w.mac.view()
	mv := myRelay(v, r)
	if mv == nil || !mv.Registered || !mv.Online || !mv.Since.Equal(now) || len(v.RelayJoins) != 0 {
		t.Fatalf("after the install: %+v, joins %+v", mv, v.RelayJoins)
	}
	if rl := relayIn(v, r); rl == nil || rl.Name != "香港" || rl.Owner != w.me {
		t.Fatalf("my new relay: %+v", rl)
	}
	if code, _ := joinRegister(t, s, j.Code, proto.RelayInfo{}); code != 401 {
		t.Fatalf("the code worked twice: %d", code)
	}

	// No region: named after its address; a name given wins over the
	// region.
	j2 := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	if _, reg := joinRegister(t, s, j2.Code, proto.RelayInfo{IPv4: netip.MustParseAddr("198.51.100.4")}); reg.Name != "198.51.100.4" {
		t.Fatalf("no region: %+v", reg)
	}
	j3 := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Name: "家里的 NAS"}).Result.RelayJoin
	if _, reg := joinRegister(t, s, j3.Code, proto.RelayInfo{Region: "tz:Asia/Tokyo"}); reg.Name != "家里的 NAS" {
		t.Fatalf("a name given: %+v", reg)
	}

	// "重新安装": the same relay, still in its LAN, the old secret dead;
	// a second code replaces the first.
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	first := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Relay: r}).Result.RelayJoin
	again := w.mac.must(proto.OpRelayJoin, proto.OpArgs{Relay: r}).Result.RelayJoin
	if first.Relay != r || len(w.mac.view().RelayJoins) != 1 {
		t.Fatalf("set up again: %+v; joins %+v", first, w.mac.view().RelayJoins)
	}
	if code, _ := joinRegister(t, s, first.Code, proto.RelayInfo{}); code != 401 {
		t.Fatalf("the replaced code: %d", code)
	}
	_, re := joinRegister(t, s, again.Code, proto.RelayInfo{Region: "tz:Asia/Tokyo", IPv4: netip.MustParseAddr("198.51.100.9")})
	if re.Relay != r || re.Name != "香港" {
		t.Fatalf("set up again: %+v", re)
	}
	if code, _ := relayCall(t, s.cfg.PublicURL, reg.Secret, proto.PathRelaySync, proto.RelaySyncRequest{}, nil); code != 401 {
		t.Fatalf("the old program's secret: %d", code)
	}
	if lv := lanByName(w.mac.view(), "工作室"); !slices.Contains(lv.AddedRelays, r) {
		t.Fatalf("set up again, it left 工作室: %+v", lv.AddedRelays)
	}
	if mv := myRelay(w.mac.view(), r); !mv.Since.Equal(now) {
		t.Fatalf("set up again, it is new: %+v", mv)
	}
	w.mac.refused(proto.ErrForbidden, proto.OpRelayJoin, proto.OpArgs{Relay: "hk"})
	w.jdesk.refused(proto.ErrNotFound, proto.OpRelayJoin, proto.OpArgs{Relay: r}) // not in a LAN of his: he does not see it

	// Taken back, it no longer works; only its owner takes it back.
	j4 := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	w.jdesk.refused(proto.ErrNotFound, proto.OpRelayRevokeJoin, proto.OpArgs{Join: j4.ID})
	w.mac.must(proto.OpRelayRevokeJoin, proto.OpArgs{Join: j4.ID})
	if code, _ := joinRegister(t, s, j4.Code, proto.RelayInfo{}); code != 401 || len(w.mac.view().RelayJoins) != 0 {
		t.Fatalf("a revoked code: %d", code)
	}

	// An hour later it no longer works, and the sweep takes it off the list.
	j5 := w.mac.must(proto.OpRelayJoin, proto.OpArgs{}).Result.RelayJoin
	w.clk.add(relayJoinLife)
	w.mac.auth() // its token ran out with the hour too
	if code, _ := joinRegister(t, s, j5.Code, proto.RelayInfo{}); code != 401 {
		t.Fatalf("an expired code: %d", code)
	}
	if len(w.mac.view().RelayJoins) != 0 {
		t.Fatal("an expired code is still waiting")
	}
	if !s.sweep() || len(s.state.Load().w.joins) != 0 {
		t.Fatalf("the sweep left %d codes", len(s.state.Load().w.joins))
	}

	// Five waiting codes at most.
	for range maxPendingJoins {
		w.mac.must(proto.OpRelayJoin, proto.OpArgs{})
	}
	w.mac.refused(proto.ErrInvalid, proto.OpRelayJoin, proto.OpArgs{})
}

func TestRegionName(t *testing.T) {
	for clue, want := range map[string]string{
		"aliyun:cn-hongkong": "香港", "tencent:ap-hongkong": "香港", "aws:ap-east-1": "香港", "gcp:asia-east2-a": "香港",
		"gcp:asia-east2": "香港", "azure:eastasia": "香港", "tz:Asia/Hong_Kong": "香港", "tz:Asia/Tokyo": "东京",
		"aws:AP-NORTHEAST-1": "东京", "name: 家里 ": "家里", "aws:mars-1": "", "tz:asia/tokyo": "", "nonsense": "",
		"name:" + strings.Repeat("长", 41): "", "": "",
	} {
		if got := regionName(clue); got != want {
			t.Errorf("regionName(%q) = %q, want %q", clue, got, want)
		}
	}
}

// TestRelayWeights: 3:1 puts about three devices in four first on the
// heavier relay; a standby comes after the weighted ones, and a relay
// down after the fallback, except that a full one keeps those on it.
func TestRelayWeights(t *testing.T) {
	w := &World{relayMeta: map[proto.RelayID]*relayMeta{"a": {Weight: 3}, "b": {Weight: 1}, "s": {Weight: 0}}}
	first := map[proto.RelayID]int{}
	const n = 4000
	for i := range n {
		up, standby, down := w.spread(&device{ID: proto.DeviceID(fmt.Sprintf("d_%d", i))}, []proto.RelayID{"s", "b", "a"})
		if len(up) != 2 || !slices.Equal(standby, []proto.RelayID{"s"}) || len(down) != 0 {
			t.Fatalf("spread: %v %v %v", up, standby, down)
		}
		first[up[0]]++
	}
	if share := float64(first["a"]) / n; share < 0.72 || share > 0.78 {
		t.Fatalf("3:1 put %.3f of the devices first on the heavier relay", share)
	}
	// The same device, the same answer.
	d := &device{ID: "d_7"}
	a1, _, _ := w.spread(d, []proto.RelayID{"a", "b"})
	a2, _, _ := w.spread(d, []proto.RelayID{"b", "a"})
	if !slices.Equal(a1, a2) {
		t.Fatalf("the order depends on the input: %v, %v", a1, a2)
	}
	// Down: offline always; full, unless the device is on it.
	w.relayOffline = func(id proto.RelayID) bool { return id == "b" }
	w.relayFull = func(id proto.RelayID) bool { return id == "a" }
	if up, _, down := w.spread(d, []proto.RelayID{"a", "b"}); len(up) != 0 || !slices.Equal(down, []proto.RelayID{"a", "b"}) {
		t.Fatalf("down: %v, %v", up, down)
	}
	d.Relays = []proto.RelayID{"a"}
	if up, _, down := w.spread(d, []proto.RelayID{"a", "b"}); !slices.Equal(up, []proto.RelayID{"a"}) || !slices.Equal(down, []proto.RelayID{"b"}) {
		t.Fatalf("full, with the device on it: %v, %v", up, down)
	}
	// Fullness does not flip with every client.
	for _, c := range []struct {
		was           bool
		devices, capa int
		cpu           float64
		want          bool
	}{
		{false, 9, 10, 0.5, false}, {false, 10, 10, 0.5, true}, {true, 9, 10, 0.5, true}, {true, 8, 10, 0.5, false},
		{false, 1, 0, 0.9, true}, {true, 1, 0, 0.8, true}, {true, 1, 0, 0.7, false}, {false, 100, 0, 0.1, false},
	} {
		if got := isFull(c.was, c.devices, c.capa, c.cpu); got != c.want {
			t.Errorf("isFull(%+v) = %v", c, got)
		}
	}
}

// TestRelayKick: I kick a device, then a person, off my relay: off at
// once (the maps drop it, the relay is told at its next sync, and turns
// it away saying why), recorded with who and when; lifting it brings
// them back.
func TestRelayKick(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	res := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	relay, _ := registerRelay(t, url, res.RelayToken)
	r := res.Relay
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: r})
	if !relay.admits(w.build.nk) || !relay.admits(w.jnas.nk) {
		t.Fatal("before the kicks")
	}

	k1 := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Device: w.build.id}).Result.Kick
	if a := relay.admit(w.build.nk); a.Allow || a.Reason != proto.RelayDenyKicked {
		t.Fatalf("a kicked device: %+v", a)
	}
	if relayOf(netMapOf(s, w.build.id), r) != nil || scopeByID(netMapOf(s, w.build.id), proto.LANScope(w.studio)).Relays[0] == r {
		t.Fatal("the kicked device is still told of the relay")
	}
	if !relay.admits(w.mac.nk) || relayOf(netMapOf(s, w.mac.id), r) == nil {
		t.Fatal("kicking the build server kicked my Mac too")
	}
	// The Mac reaches the build server no more through the relay.
	w.build.report(proto.Report{HomeRelay: r, Relays: []proto.RelayID{r, "hk"}})
	if p := peerOf(netMapOf(s, w.mac.id), w.build.id); slices.Contains(p.Relays, r) {
		t.Fatalf("the Mac reaches the kicked build server through %v", p.Relays)
	}
	if sr := relay.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.build.nk, w.mac.nk}}); !slices.Equal(sr.Deny, []proto.NodePublic{w.build.nk}) {
		t.Fatalf("sync: %+v", sr)
	}
	// Again changes nothing; only the owner kicks.
	v := s.Version()
	if res := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Device: w.build.id}); s.Version() != v || res.Version > v {
		t.Fatalf("kicking twice made a new version: %d → %d, answered %d", v, s.Version(), res.Version)
	}
	w.jdesk.refused(proto.ErrForbidden, proto.OpRelayKick, proto.OpArgs{Relay: r, Device: w.mac.id})
	w.mac.refused(proto.ErrInvalid, proto.OpRelayKick, proto.OpArgs{Relay: r})

	// A person: all their devices.
	w.clk.add(time.Second)
	k2 := w.mac.must(proto.OpRelayKick, proto.OpArgs{Relay: r, Person: w.jie}).Result.Kick
	if relay.admits(w.jnas.nk) || relay.admits(w.jdesk.nk) {
		t.Fatal("阿杰's devices still get in")
	}
	u := monthOf(w.mac.relayUsage(), r)
	if len(u.Kicks) != 2 || u.Kicks[0].ID != k2 || u.Kicks[0].Person != w.jie || u.Kicks[0].By != w.me || u.Kicks[1].Device != w.build.id || u.Kicks[0].At.IsZero() {
		t.Fatalf("the kicks: %+v", u.Kicks)
	}

	// Lifted: back in, and recorded.
	w.jdesk.refused(proto.ErrNotFound, proto.OpRelayUnkick, proto.OpArgs{Kick: k1})
	w.mac.must(proto.OpRelayUnkick, proto.OpArgs{Kick: k1})
	if !relay.admits(w.build.nk) || relayOf(netMapOf(s, w.build.id), r) == nil {
		t.Fatal("lifted, the build server is still off")
	}
	u = monthOf(w.mac.relayUsage(), r)
	if len(u.Kicks) != 2 || u.Kicks[1].ID != k1 || u.Kicks[1].LiftedAt.IsZero() || u.Kicks[1].LiftedBy != w.me {
		t.Fatalf("the lifted kick: %+v", u.Kicks)
	}
	w.mac.refused(proto.ErrNotFound, proto.OpRelayUnkick, proto.OpArgs{Kick: k1})
	// After 30 days, the lifted one leaves the card.
	if ks := s.state.Load().w.kicksOf(r, w.clk.now().AddDate(0, 0, 31).AddDate(0, 0, -30)); len(ks) != 1 || ks[0].ID != k2 {
		t.Fatalf("a month on: %+v", ks)
	}
}

// TestRelayLoad: a sync says how the relay is doing; the owner's card
// shows it with who is on it, and at capacity the relay is full: no one
// new is put first on it, those on it stay.
func TestRelayLoad(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	res := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	relay, _ := registerRelay(t, url, res.RelayToken)
	r := res.Relay
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: r})
	w.mac.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: r})
	one := 1
	w.mac.must(proto.OpRelaySetCapacity, proto.OpArgs{Relay: r, Capacity: &one})
	three := 3
	w.mac.must(proto.OpRelaySetWeight, proto.OpArgs{Relay: r, Weight: &three})
	w.mac.refused(proto.ErrInvalid, proto.OpRelaySetWeight, proto.OpArgs{Relay: r})
	if mv := myRelay(w.mac.view(), r); mv.Weight != 3 || mv.Capacity != 1 || mv.Full {
		t.Fatalf("settings: %+v", mv)
	}

	health := &proto.RelayHealth{Connections: 3, BytesPerSec: 12345, CPU: 0.25, Memory: 40 << 20, MemoryLimit: 256 << 20, Uptime: 600}
	w.build.report(proto.Report{HomeRelay: r, Relays: []proto.RelayID{r}})
	relay.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.build.nk, w.mac.nk, w.jnas.nk}, Health: health})
	m := monthOf(w.mac.relayUsage(), r)
	l := m.Load
	if l == nil || l.Devices != 3 || l.People != 2 || l.Connections != 3 || l.BytesPerSec != 12345 || l.CPU != 0.25 || l.Memory != 40<<20 || l.MemoryLimit != 256<<20 || l.Uptime != 600 || !l.Full {
		t.Fatalf("load: %+v", l)
	}
	// 阿杰's NAS is in 周末联机 with me: I see it; and my two.
	if len(m.Connected) != 3 {
		t.Fatalf("connected: %+v", m.Connected)
	}
	if mv := myRelay(w.mac.view(), r); !mv.Full {
		t.Fatalf("at capacity: %+v", mv)
	}
	// Full: the build server, on it, keeps it first; 阿杰's desktop, not
	// on it, gets it last.
	if sc := scopeByID(netMapOf(s, w.build.id), proto.LANScope(w.studio)); sc.Relays[0] != r {
		t.Fatalf("the build server, on the full relay: %v", sc.Relays)
	}
	if sc := scopeByID(netMapOf(s, w.jdesk.id), proto.LANScope(w.weekend)); sc.Relays[len(sc.Relays)-1] != r || sc.Relays[0] == r {
		t.Fatalf("阿杰's desktop, new to the full relay: %v", sc.Relays)
	}
	// Empty again: no longer full.
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{}})
	if mv := myRelay(w.mac.view(), r); mv.Full {
		t.Fatalf("empty: %+v", mv)
	}
	if sc := scopeByID(netMapOf(s, w.jdesk.id), proto.LANScope(w.weekend)); sc.Relays[0] != r {
		t.Fatalf("not full, first again: %v", sc.Relays)
	}
}

// TestRelayWatch: a relay's watch answers at the next change, or after a
// while with nothing.
func TestRelayWatch(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	res := w.mac.must(proto.OpRelayCreate, proto.OpArgs{Name: "香港"}).Result
	relay, _ := registerRelay(t, url, res.RelayToken)
	watch := func(since uint64) (uint64, time.Duration) {
		start := time.Now()
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s%s?since=%d", url, proto.PathRelayWatch, since), nil)
		req.Header.Set("Authorization", "Bearer "+relay.secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var wr proto.RelayWatchResult
		json.NewDecoder(resp.Body).Decode(&wr)
		return wr.Version, time.Since(start)
	}
	v := s.Version()
	if got, _ := watch(v - 1); got != v {
		t.Fatalf("behind: %d, want %d", got, v)
	}
	done := make(chan uint64, 1)
	go func() { got, _ := watch(v); done <- got }()
	time.Sleep(50 * time.Millisecond)
	w.mac.must(proto.OpRelayRename, proto.OpArgs{Relay: res.Relay, Name: "香港二号"})
	select {
	case got := <-done:
		if got <= v {
			t.Fatalf("after a change: %d", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not wake")
	}
	old := relayWatchWait
	relayWatchWait = 100 * time.Millisecond
	defer func() { relayWatchWait = old }()
	if got, took := watch(s.Version()); got != s.Version() || took < 90*time.Millisecond {
		t.Fatalf("nothing new: %d after %s", got, took)
	}
}

// TestSeededRelayOnline: the derper on hk-spare never syncs; its card says
// it is up while a device online is connected to it, as the engines say.
func TestSeededRelayOnline(t *testing.T) {
	w := newM2World(t)
	w.mac.report(proto.Report{HomeRelay: "hk", Relays: []proto.RelayID{"hk"}})
	wd := w.s.state.Load().w
	now := w.clk.now()
	find := func(rs []proto.RelayView) *proto.RelayView {
		for i := range rs {
			if rs[i].ID == "hk" {
				return &rs[i]
			}
		}
		return nil
	}
	if rv := find(wd.myRelays(w.me, now, func(id proto.DeviceID) bool { return id == w.mac.id })); rv == nil || !rv.Online || !rv.LastSeen.IsZero() {
		t.Fatalf("hk with the Mac online on it: %+v", rv)
	}
	if rv := find(wd.myRelays(w.me, now, func(proto.DeviceID) bool { return false })); rv.Online {
		t.Fatalf("hk with no one online: %+v", rv)
	}
	// An engine from before M4 says only its home.
	w.build.report(proto.Report{HomeRelay: "hk"})
	wd = w.s.state.Load().w
	if rv := find(wd.myRelays(w.me, now, func(id proto.DeviceID) bool { return id == w.build.id })); !rv.Online {
		t.Fatalf("hk as an old engine's home: %+v", rv)
	}
}

func operatorMonth(u proto.OperatorUsage, id proto.RelayID) *proto.RelayMonth {
	for i := range u.Relays {
		if u.Relays[i].Relay == id {
			return &u.Relays[i]
		}
	}
	return nil
}

func operatorRelay(ov *proto.OperatorView, id proto.RelayID) *proto.RelayView {
	for i := range ov.Relays {
		if ov.Relays[i].ID == id {
			return &ov.Relays[i]
		}
	}
	return nil
}

// TestOperator is the ops page's server side (DESIGN §14.12 M4-13). The
// deployment's operators, named with deneb-control operators, manage the
// public relays from the browser as people manage their own (the plan's
// §16): list them, add one with the one-line command (a standby at first),
// weigh them, kick and lift, a kicked device going through another. No
// one else may, and the numbers name the heaviest users by traffic alone.
func TestOperator(t *testing.T) {
	w := newM2World(t)
	s, url := w.s, w.mac.url
	s.cfg.Pin = testPin
	sock := serveAdmin(t, s)
	kit, jie := newWebBrowser(t, url), newWebBrowser(t, url)
	kit.login("kittors@deneb.test", DevCode)
	jie.login("jie@deneb.test", DevCode)
	view := func(b *browser2) *proto.UserView {
		t.Helper()
		var v proto.UserView
		if st := b.do("GET", proto.PathWebUserView, nil, &v, nil); st != 200 {
			t.Fatalf("view: %d", st)
		}
		return &v
	}
	usage := func(b *browser2) (int, proto.OperatorUsage) {
		t.Helper()
		var u proto.OperatorUsage
		st := b.do("GET", proto.PathWebOperatorUsage, nil, &u, nil)
		return st, u
	}
	must := func(b *browser2, op proto.OpType, a proto.OpArgs) proto.OpResult {
		t.Helper()
		st, res := b.op(op, a)
		if st != 200 {
			t.Fatalf("%s %+v: %d", op, a, st)
		}
		return res
	}
	refused := func(b *browser2, want int, op proto.OpType, a proto.OpArgs) {
		t.Helper()
		if st, _ := b.op(op, a); st != want {
			t.Fatalf("%s %+v: %d, want %d", op, a, st, want)
		}
	}
	weekend := proto.LANScope(w.weekend)
	nasList := func() []proto.RelayID { return scopeByID(netMapOf(s, w.jnas.id), weekend).Relays }
	zero, one, three := 0, 1, 3

	// No one is an operator until the server's admin says so: no ops page,
	// no numbers, no public relays to add.
	for _, b := range []*browser2{kit, jie} {
		if view(b).Operator != nil {
			t.Fatal("an ops page before anyone was made an operator")
		}
		if st, _ := usage(b); st != 403 {
			t.Fatalf("the ops page's numbers for someone else: %d", st)
		}
		refused(b, 403, proto.OpRelayJoin, proto.OpArgs{Public: true})
	}
	if ops, err := AdminOperators(sock); err != nil || len(ops) != 0 {
		t.Fatalf("operators at first: %+v, %v", ops, err)
	}
	v := s.Version()
	if ops, err := AdminSetOperator(sock, " Kittors@deneb.test", false); err != nil || len(ops) != 1 || ops[0].Email != "kittors@deneb.test" {
		t.Fatalf("operators -add: %+v, %v", ops, err)
	}
	if s.Version() == v {
		t.Fatal("making an operator made no new version, so their view would not change")
	}
	if _, err := AdminSetOperator(sock, "nobody@deneb.test", false); err == nil {
		t.Fatal("an unknown account was made an operator")
	}
	if ov := view(kit).Operator; ov == nil || len(ov.Relays) != 0 || len(ov.Joins) != 0 {
		t.Fatalf("the operator's part of the view: %+v", ov)
	}
	if view(jie).Operator != nil {
		t.Fatal("阿杰 got the ops page too")
	}

	// "+ 加一台公共中转站": a code and the command; "等待安装" until it runs;
	// then the relay, named after its place, a standby, 2 MB/s a connection.
	add := func(place string) (proto.RelayID, *testRelay) {
		t.Helper()
		j := must(kit, proto.OpRelayJoin, proto.OpArgs{Public: true}).Result.RelayJoin
		if j == nil || !strings.Contains(j.Command, "--join "+j.Code) {
			t.Fatalf("the join code: %+v", j)
		}
		if ov := view(kit).Operator; len(ov.Joins) != 1 || ov.Joins[0].ID != j.ID || ov.Joins[0].Relay != "" {
			t.Fatalf("waiting to be installed: %+v", ov.Joins)
		}
		if len(view(jie).RelayJoins) != 0 {
			t.Fatal("someone else sees the code waiting")
		}
		st, reg := joinRegister(t, s, j.Code, proto.RelayInfo{Region: "name:" + place})
		if st != 200 || reg.Source != proto.RelayPublic || reg.Name != place || reg.RateLimit != defaultPublicRateLimit {
			t.Fatalf("register: %d %+v", st, reg)
		}
		return reg.Relay, &testRelay{t: t, url: url, secret: reg.Secret}
	}
	p1, r1 := add("香港")
	w.clk.add(time.Second)
	p2, r2 := add("东京")
	kv := view(kit)
	if len(kv.Operator.Joins) != 0 || len(kv.Operator.Relays) != 2 {
		t.Fatalf("both installed: %+v", kv.Operator)
	}
	for _, rv := range kv.Operator.Relays {
		if rv.Weight != newPublicRelayWeight || !rv.Registered || !rv.Online || rv.Since.IsZero() || rv.Default || len(rv.LANs) != 0 {
			t.Fatalf("a public relay just installed: %+v", rv)
		}
		if rl := relayIn(kv, rv.ID); rl == nil || rl.Source != proto.RelayPublic || rl.RateLimit != defaultPublicRateLimit {
			t.Fatalf("its card's details: %+v", rl)
		}
	}
	// Both standbys: after the deployment's default, in the order of
	// their IDs. "多用" on 香港 puts it before 东京 for everyone.
	refused(jie, 403, proto.OpRelaySetWeight, proto.OpArgs{Relay: p1, Weight: &three})
	must(kit, proto.OpRelaySetWeight, proto.OpArgs{Relay: p1, Weight: &three})
	if got := nasList(); !slices.Equal(got, []proto.RelayID{"hk", p1, p2}) {
		t.Fatalf("阿杰's NAS goes through %v", got)
	}
	if rv := operatorRelay(view(kit).Operator, p1); rv.Weight != 3 {
		t.Fatalf("香港's weight: %+v", rv)
	}
	must(kit, proto.OpRelaySetCapacity, proto.OpArgs{Relay: p2, Capacity: &one})
	must(kit, proto.OpRelaySetCapacity, proto.OpArgs{Relay: p2, Capacity: &zero})
	must(kit, proto.OpRelayRename, proto.OpArgs{Relay: p2, Name: "东京 2"})

	// A kick from the card: 阿杰 off 香港. His devices go through 东京
	// instead; 香港 lets them in no more and drops the one on it.
	refused(jie, 403, proto.OpRelayKick, proto.OpArgs{Relay: p1, Person: w.me})
	w.clk.add(time.Second)
	k := must(kit, proto.OpRelayKick, proto.OpArgs{Relay: p1, Person: w.jie}).Result.Kick
	if got := nasList(); !slices.Equal(got, []proto.RelayID{"hk", p2}) {
		t.Fatalf("kicked off 香港, 阿杰's NAS goes through %v", got)
	}
	if a := r1.admit(w.jnas.nk); a.Allow || a.Reason != proto.RelayDenyKicked {
		t.Fatalf("香港 and the kicked NAS: %+v", a)
	}
	if !r2.admits(w.jnas.nk) || !r1.admits(w.mac.nk) || !slices.Contains(scopeByID(netMapOf(s, w.mac.id), weekend).Relays, p1) {
		t.Fatal("the kick reached further than 阿杰 on 香港")
	}
	if sr := r1.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.jnas.nk, w.mac.nk}}); !slices.Equal(sr.Deny, []proto.NodePublic{w.jnas.nk}) {
		t.Fatalf("香港's sync: %+v", sr)
	}
	refused(jie, 404, proto.OpRelayUnkick, proto.OpArgs{Kick: k})
	must(kit, proto.OpRelayUnkick, proto.OpArgs{Kick: k})
	if got := nasList(); !slices.Equal(got, []proto.RelayID{"hk", p1, p2}) || !r1.admits(w.jnas.nk) {
		t.Fatalf("let back on 香港, the NAS goes through %v", got)
	}

	// The numbers: what each carried this month, its heaviest users by
	// traffic, its load, its kicks; and everyone on them now, once each.
	health := &proto.RelayHealth{Connections: 2, BytesPerSec: 1000, CPU: 0.1}
	r1.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.mac.nk, w.jnas.nk}, Health: health, Usage: []proto.RelayClientUsage{
		{Node: w.mac.nk, Sent: 700, Received: 300}, {Node: w.jnas.nk, Sent: 100, Received: 900}, {Node: w.jdesk.nk, Sent: 50}}})
	r2.sync(proto.RelaySyncRequest{Connected: []proto.NodePublic{w.build.nk, w.jnas.nk}, Health: health, Usage: []proto.RelayClientUsage{
		{Node: w.build.nk, Sent: 5}}})
	st, u := usage(kit)
	if st != 200 || u.People != 2 || u.Devices != 3 || len(u.Relays) != 2 {
		t.Fatalf("the ops page's numbers: %d %+v", st, u)
	}
	m := operatorMonth(u, p1)
	if m.Bytes != 850 || m.Users != 2 || len(m.Top) != 2 || m.Top[0].Person.ID != w.jie || m.Top[0].Bytes != 1050 ||
		m.Top[1].Person.ID != w.me || m.Top[1].Bytes != 1000 || m.Top[0].Person.DisplayName != "阿杰" {
		t.Fatalf("香港's month: %+v", m)
	}
	if m.Load == nil || m.Load.Devices != 2 || m.Load.People != 2 || m.Load.BytesPerSec != 1000 || len(m.Kicks) != 1 || m.Kicks[0].By != w.me || m.Kicks[0].LiftedAt.IsZero() {
		t.Fatalf("香港 now: %+v, kicks %+v", m.Load, m.Kicks)
	}
	if m2 := operatorMonth(u, p2); m2.Bytes != 5 || m2.Users != 1 || len(m2.Top) != 1 || m2.Top[0].Person.ID != w.me {
		t.Fatalf("东京's month: %+v", m2)
	}
	// Who with whom, whose devices, emails: none of it.
	b, _ := json.Marshal(u)
	for _, d := range []proto.DeviceID{w.mac.id, w.build.id, w.jdesk.id, w.jnas.id} {
		if strings.Contains(string(b), string(d)) {
			t.Fatalf("the numbers name device %s: %s", d, b)
		}
	}
	if strings.Contains(string(b), "@") || len(m.Connected) != 0 || len(m.Devices) != 0 || len(m.LANs) != 0 {
		t.Fatalf("the numbers say too much: %s", b)
	}
	// At most the first few, the heaviest first.
	sw := s.state.Load().w
	byPerson := map[proto.PersonID]int64{}
	for i, e := range []string{"kittors@deneb.test", "jie@deneb.test", "mia@deneb.test", "leo@deneb.test"} {
		byPerson[sw.byEmail[e].ID] = int64(10 * (i + 1))
	}
	if top := sw.topPeople(byPerson, 3); len(top) != 3 || top[0].Bytes != 40 || top[2].Bytes != 20 || top[0].Person.Email != "" {
		t.Fatalf("the top three: %+v", top)
	}

	// Setting one up again, and taking the code back; 阿杰 cannot.
	j := must(kit, proto.OpRelayJoin, proto.OpArgs{Relay: p2}).Result.RelayJoin
	if ov := view(kit).Operator; len(ov.Joins) != 1 || ov.Joins[0].Relay != p2 || operatorRelay(ov, p2).TokenExpiresAt.IsZero() {
		t.Fatalf("a code to set 东京 up again: %+v", ov)
	}
	refused(jie, 404, proto.OpRelayRevokeJoin, proto.OpArgs{Join: j.ID})
	must(kit, proto.OpRelayRevokeJoin, proto.OpArgs{Join: j.ID})
	if len(view(kit).Operator.Joins) != 0 {
		t.Fatal("the code is still waiting")
	}

	// No one drags a public relay into a LAN or deletes it, operators
	// neither; and an operator has no say over someone's own relay.
	refused(kit, 403, proto.OpRelayAdd, proto.OpArgs{LAN: w.studio, Relay: p1})
	refused(kit, 403, proto.OpRelayDelete, proto.OpArgs{Relay: p1})
	jr := w.jdesk.must(proto.OpRelayCreate, proto.OpArgs{Name: "阿杰的服务器"}).Result
	registerRelay(t, url, jr.RelayToken)
	w.jdesk.must(proto.OpRelayAdd, proto.OpArgs{LAN: w.weekend, Relay: jr.Relay})
	refused(kit, 403, proto.OpRelaySetWeight, proto.OpArgs{Relay: jr.Relay, Weight: &one})
	refused(kit, 403, proto.OpRelayKick, proto.OpArgs{Relay: jr.Relay, Device: w.jnas.id})
	if ov := view(kit).Operator; len(ov.Relays) != 2 {
		t.Fatalf("someone's own relay on the ops page: %+v", ov.Relays)
	}

	// No longer an operator: the page goes, and so does the say.
	if ops, err := AdminSetOperator(sock, "kittors@deneb.test", true); err != nil || len(ops) != 0 {
		t.Fatalf("operators -remove: %+v, %v", ops, err)
	}
	if view(kit).Operator != nil {
		t.Fatal("the ops page stayed")
	}
	if st, _ := usage(kit); st != 403 {
		t.Fatalf("the numbers after: %d", st)
	}
	refused(kit, 403, proto.OpRelaySetWeight, proto.OpArgs{Relay: p1, Weight: &one})
}

// TestOperatorSeedRelay: a public relay the seed file lists is the seed
// file's to change, so the ops page only kicks on it.
func TestOperatorSeedRelay(t *testing.T) {
	seed := DevSeed()
	seed.Relays = append([]SeedRelay{{Relay: proto.Relay{ID: "pub", Name: "香港", Source: proto.RelayPublic,
		HostName: "relay.example.com", IPv4: netip.MustParseAddr("192.0.2.1"), DERPPort: 47445, STUNPort: 47478}}}, seed.Relays...)
	var mu sync.Mutex
	var lines []string
	s, ts, sock := adminServer(t, t.TempDir(), seed, &lines, &mu)
	if _, err := AdminSetOperator(sock, "kittors@deneb.test", false); err != nil {
		t.Fatal(err)
	}
	kit := newWebBrowser(t, ts.URL)
	kit.login("kittors@deneb.test", DevCode)
	var v proto.UserView
	kit.do("GET", proto.PathWebUserView, nil, &v, nil)
	if rv := operatorRelay(v.Operator, "pub"); rv == nil || !rv.Default || !rv.Seeded {
		t.Fatalf("the seed's public relay on the ops page: %+v", v.Operator)
	}
	two := 2
	for op, a := range map[proto.OpType]proto.OpArgs{
		proto.OpRelaySetWeight: {Relay: "pub", Weight: &two},
		proto.OpRelayRename:    {Relay: "pub", Name: "别的"},
		proto.OpRelayJoin:      {Relay: "pub"},
	} {
		if st, _ := kit.op(op, a); st != 403 {
			t.Fatalf("%s on the seed's relay: %d", op, st)
		}
	}
	if st, _ := kit.op(proto.OpRelayKick, proto.OpArgs{Relay: "pub", Person: s.state.Load().w.byEmail["leo@deneb.test"].ID}); st != 200 {
		t.Fatalf("a kick on the seed's relay: %d", st)
	}
}
