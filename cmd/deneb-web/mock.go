package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// mock is an in-memory control server for the Web API (/api/v1, as
// proto/web.go lays it out), so that the browser app can be built and shown
// without one. Its world is the desktop demo's: kittors owns 周末联机 (LAN
// mode, with Mia and 阿杰) and 工作室 (device-sharing mode: 小林, 阿杰, Mia and
// Leo get the studio's public devices shared with them, scene 4). Leo is not
// in 周末联机 yet: the invite link /i/k3xQ9 is for it. Every login code is
// 000000. It keeps no state on disk.
type mock struct {
	mu       sync.Mutex
	version  uint64
	changed  chan struct{} // closed and replaced on every change
	people   []*proto.Person
	emails   map[string]proto.PersonID
	devices  map[proto.DeviceID]*mdev
	order    []proto.DeviceID
	lans     []*mlan
	shares   []*mshare
	relays   []*mrelay
	joins    []*mjoin
	invites  map[string]*minvite
	sessions map[string]proto.PersonID
	done     map[string]*proto.OpResult // op id → its answer, for retries
	seed     map[proto.PersonID]bool    // M7: accounts the deployment's seed names, which cannot be deleted
	seq      int
	now      func() time.Time
}

type mdev struct {
	v         proto.DeviceView // the parts that do not depend on who looks
	services  []proto.Service
	refused   map[proto.PersonID]bool
	loggedOut bool // M7: logged out: in no LAN, in its owner's UserView.LoggedOutDevices
}

type mlan struct {
	id      proto.LANID
	name    string
	owner   proto.PersonID
	mode    proto.LANMode
	created time.Time
	members []proto.Member
	devs    []*mlandev
	added   []proto.RelayID
}

type mlandev struct {
	id     proto.DeviceID
	joined time.Time
	on     map[proto.ServiceKey]bool
}

type mshare struct {
	id      proto.ShareID
	device  proto.DeviceID
	person  proto.PersonID
	sharer  proto.PersonID
	on      map[proto.ServiceKey]bool
	active  bool
	created time.Time
}

type mrelay struct {
	r         proto.Relay
	v         proto.RelayView
	load      *proto.RelayLoad
	connected []proto.RelayClient
	kicks     []proto.RelayKick
	lanBytes  map[proto.LANID]int64
}

type mjoin struct {
	v     proto.RelayJoinView
	owner proto.PersonID
	code  string
}

type minvite struct {
	id      proto.InviteID
	lan     proto.LANID
	inviter proto.PersonID
	state   proto.InviteState
	expires time.Time
}

// The services the demo's devices offer.
var (
	svcScreen = proto.Service{Kind: proto.ServiceScreen, Name: "远程桌面", Ports: []proto.PortRange{proto.Port(proto.TCP, 3389)}}
	svcFiles  = proto.Service{Kind: proto.ServiceFiles, Name: "共享文件夹", Ports: []proto.PortRange{proto.Port(proto.TCP, 445)}}
	svcWeb    = proto.Service{Kind: proto.ServiceWeb, Name: "网站", Ports: []proto.PortRange{proto.Port(proto.TCP, 7000)}}
	svcShell  = proto.Service{Kind: proto.ServiceShell, Name: "命令行", Ports: []proto.PortRange{proto.Port(proto.TCP, 22)}}
	svcGame   = proto.Service{Kind: proto.ServiceGame, Name: "游戏服务器", Ports: []proto.PortRange{proto.Port(proto.UDP, 27015)}}
	svcDB     = proto.Service{Kind: proto.ServiceOther, Name: "数据库", Ports: []proto.PortRange{proto.Port(proto.TCP, 5432)}}
	svcPrint  = proto.Service{Kind: proto.ServiceOther, Name: "打印", Ports: []proto.PortRange{proto.Port(proto.TCP, 631)}}
)

func newMock() *mock {
	now := time.Now().UTC().Truncate(time.Second)
	m := &mock{
		version:  100,
		changed:  make(chan struct{}),
		emails:   map[string]proto.PersonID{},
		devices:  map[proto.DeviceID]*mdev{},
		invites:  map[string]*minvite{},
		sessions: map[string]proto.PersonID{},
		done:     map[string]*proto.OpResult{},
		seed:     map[proto.PersonID]bool{},
		now:      func() time.Time { return time.Now().UTC().Truncate(time.Second) },
	}
	person := func(id, name, display, initial string, emails ...string) {
		m.people = append(m.people, &proto.Person{ID: proto.PersonID(id), Name: name, DisplayName: display, Initial: initial, Email: emails[0]})
		for _, e := range emails {
			m.emails[e] = proto.PersonID(id)
		}
	}
	person("p_kittors", "kittors", "kittors", "K", "kittors@deneb.test")
	person("p_mia", "mia", "Mia", "M", "mia@deneb.test")
	person("p_jie", "jie", "阿杰", "杰", "jie@deneb.test")
	person("p_leo", "leo", "Leo", "L", "leo@deneb.test", "leo.chen@gmail.com")
	person("p_lin", "lin", "小林", "林", "lin@deneb.test")

	n := 10
	dev := func(id, owner, name, kind, os string, online bool, svcs ...proto.Service) *mdev {
		n++
		d := &mdev{
			v: proto.DeviceView{
				ID: proto.DeviceID(id), Owner: proto.PersonID(owner), Name: name, Kind: proto.DeviceKind(kind), OS: proto.OS(os),
				Addrs:  []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, byte(n)}), 32)},
				Online: online,
			},
			services: svcs,
			refused:  map[proto.PersonID]bool{},
		}
		if !online {
			d.v.LastSeen = now.Add(-2 * time.Hour)
		}
		m.devices[d.v.ID] = d
		m.order = append(m.order, d.v.ID)
		return d
	}
	dev("d_kmac", "p_kittors", "MacBook", "laptop", "macos", true, svcScreen, svcFiles)
	dev("d_kgame", "p_kittors", "游戏主机", "console", "other", true, svcGame)
	dev("d_kstudio", "p_kittors", "工作室主机", "mini", "macos", true, svcScreen, svcShell)
	dev("d_knas", "p_kittors", "NAS", "nas", "linux", true, svcFiles, svcWeb)
	// Logged out a while ago (M7): its owner may delete it.
	dev("d_kold", "p_kittors", "旧笔记本", "laptop", "macos", false).loggedOut = true
	dev("d_mia", "p_mia", "MacBook", "laptop", "macos", true, svcScreen, svcFiles)
	dev("d_jdesk", "p_jie", "台式机", "desktop", "windows", true, svcScreen, svcWeb)
	dev("d_jnas", "p_jie", "NAS", "nas", "linux", false, svcFiles, svcScreen, svcWeb)
	// 小林's, in no LAN: what she may share with someone (共享给我). Leo
	// has none: he joins from a link and is shown how to install.
	dev("d_lin", "p_lin", "笔记本", "laptop", "macos", true, svcScreen, svcFiles)
	// The studio's own devices (public, scene 4), logged in by kittors.
	for _, s := range []struct {
		id, name, kind string
		svcs           []proto.Service
	}{
		{"d_gpu", "GPU 主机", "server", []proto.Service{svcShell, svcScreen}},
		{"d_build", "构建服务器", "server", []proto.Service{svcShell, svcWeb}},
		{"d_files", "文件服务器", "nas", []proto.Service{svcFiles}},
		{"d_db", "开发数据库", "server", []proto.Service{svcDB}},
		{"d_print", "打印机", "other", []proto.Service{svcPrint}},
		{"d_test", "测试机", "mini", []proto.Service{svcScreen, svcShell}},
	} {
		d := dev(s.id, "p_kittors", s.name, s.kind, "linux", true, s.svcs...)
		d.v.PublicLAN = "l_studio"
	}

	joined := func(p string, role proto.Role, at time.Time) proto.Member {
		return proto.Member{Person: proto.PersonID(p), Role: role, State: proto.MemberJoined, JoinedAt: at}
	}
	in := func(id string, at time.Time, on ...proto.Service) *mlandev {
		ld := &mlandev{id: proto.DeviceID(id), joined: at, on: map[proto.ServiceKey]bool{}}
		for _, s := range on {
			ld.on[s.Key()] = true
		}
		return ld
	}
	week := now.Add(-30 * 24 * time.Hour)
	m.lans = append(m.lans, &mlan{
		id: "l_weekend", name: "周末联机", owner: "p_kittors", mode: proto.LANModeLAN, created: week,
		members: []proto.Member{joined("p_kittors", proto.RoleOwner, week), joined("p_mia", proto.RoleMember, week), joined("p_jie", proto.RoleMember, week)},
		devs: []*mlandev{
			in("d_kmac", week, svcScreen), in("d_kgame", week, svcGame), in("d_mia", week, svcScreen),
			in("d_jdesk", week, svcScreen), in("d_jnas", week, svcFiles, svcWeb),
		},
		added: []proto.RelayID{"r_tokyo"},
	}, &mlan{
		id: "l_studio", name: "工作室", owner: "p_kittors", mode: proto.LANModeShare, created: week,
		members: []proto.Member{joined("p_kittors", proto.RoleOwner, week), joined("p_lin", proto.RoleMember, week), joined("p_jie", proto.RoleMember, week), joined("p_mia", proto.RoleMember, week), joined("p_leo", proto.RoleMember, week)},
		devs: []*mlandev{
			in("d_kstudio", week, svcScreen), in("d_gpu", week, svcShell, svcScreen), in("d_build", week, svcShell, svcWeb),
			in("d_files", week, svcFiles), in("d_db", week, svcDB), in("d_print", week, svcPrint), in("d_test", week, svcScreen, svcShell),
		},
	})
	// What the studio's members have already (scene 4): 阿杰 not the build
	// server, so that it can be shared with him.
	for _, s := range [][2]string{{"d_build", "p_lin"}, {"d_print", "p_lin"}, {"d_gpu", "p_jie"}, {"d_test", "p_jie"}, {"d_files", "p_mia"}} {
		m.newShare(proto.DeviceID(s[0]), proto.PersonID(s[1]), "p_kittors", nil, week)
	}

	m.relays = []*mrelay{{
		r: proto.Relay{ID: "r_hk", Name: "香港", Source: proto.RelayPublic, HostName: "relay-hk.deneb.app", DERPPort: 47445, STUNPort: 47478, RateLimit: 2 << 20},
		v: proto.RelayView{ID: "r_hk", Registered: true, Online: true, Weight: 1},
	}, {
		r:         proto.Relay{ID: "r_tokyo", Name: "东京", Source: proto.RelayServer, Owner: "p_kittors", HostName: "203.0.113.7", DERPPort: 47445, STUNPort: 47478},
		v:         proto.RelayView{ID: "r_tokyo", LANs: []proto.LANID{"l_weekend"}, Registered: true, Online: true, Since: now.Add(-3 * 24 * time.Hour), Weight: 1},
		load:      &proto.RelayLoad{At: now, Devices: 3, People: 3, Connections: 5, BytesPerSec: 1_400_000, CPU: 0.12, Memory: 64 << 20, MemoryLimit: 512 << 20, Uptime: 3 * 24 * 3600},
		connected: []proto.RelayClient{{Device: "d_kgame", Person: "p_kittors"}, {Device: "d_jdesk", Person: "p_jie"}, {Device: "d_mia", Person: "p_mia"}},
		lanBytes:  map[proto.LANID]int64{"l_weekend": 3_200_000_000},
	}}
	m.invites["k3xQ9"] = &minvite{id: "i_k3xQ9", lan: "l_weekend", inviter: "p_kittors", state: proto.InvitePending, expires: now.Add(7 * 24 * time.Hour)}
	m.invites["old42"] = &minvite{id: "i_old42", lan: "l_weekend", inviter: "p_kittors", state: proto.InviteExpired, expires: now.Add(-24 * time.Hour)}
	return m
}

// ---- The world --------------------------------------------------------------------

func (m *mock) person(id proto.PersonID) *proto.Person {
	for _, p := range m.people {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (m *mock) lan(id proto.LANID) *mlan {
	for _, l := range m.lans {
		if l.id == id {
			return l
		}
	}
	return nil
}

func (l *mlan) member(p proto.PersonID) *proto.Member {
	for i := range l.members {
		if l.members[i].Person == p {
			return &l.members[i]
		}
	}
	return nil
}

func (l *mlan) joined(p proto.PersonID) bool {
	mb := l.member(p)
	return mb != nil && mb.State == proto.MemberJoined
}

func (l *mlan) dev(id proto.DeviceID) *mlandev {
	for _, d := range l.devs {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (m *mock) relay(id proto.RelayID) *mrelay {
	for _, r := range m.relays {
		if r.r.ID == id {
			return r
		}
	}
	return nil
}

// manages: the device's owner, or for a public device its LAN's owner.
func (m *mock) manages(me proto.PersonID, d *mdev) bool {
	if d.v.PublicLAN != "" {
		l := m.lan(d.v.PublicLAN)
		return l != nil && l.owner == me
	}
	return d.v.Owner == me
}

// displayName is a device's name as the viewer reads it: "MacBook" for
// my own (as the control server gives it: what I called it), "阿杰的 NAS"
// for someone else's; a public device goes by its own name.
func (m *mock) displayName(me proto.PersonID, d *mdev) string {
	if d.v.PublicLAN != "" || d.v.Owner == me {
		return d.v.Name
	}
	return spaced(m.person(d.v.Owner).DisplayName + "的" + d.v.Name)
}

// spaced puts a space between Chinese and Latin letters, as the app writes
// names ("Mia 的 MacBook").
func spaced(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && (isHan(prev) && isLatin(r) || isLatin(prev) && isHan(r)) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isHan(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }
func isLatin(r rune) bool {
	return r < 0x80 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

func (m *mock) newShare(dev proto.DeviceID, person, sharer proto.PersonID, keys []proto.ServiceKey, at time.Time) *mshare {
	m.seq++
	s := &mshare{id: proto.ShareID(fmt.Sprintf("s_%d", m.seq)), device: dev, person: person, sharer: sharer, on: map[proto.ServiceKey]bool{}, active: true, created: at}
	for _, svc := range m.devices[dev].services {
		if len(keys) == 0 || slices.Contains(keys, svc.Key()) {
			s.on[svc.Key()] = true
		}
	}
	m.shares = append(m.shares, s)
	return s
}

func (m *mock) bump() {
	m.version++
	close(m.changed)
	m.changed = make(chan struct{})
}

// view is the person's UserView: what the device API would send them, less
// "this device".
func (m *mock) view(me proto.PersonID) *proto.UserView {
	v := &proto.UserView{Version: m.version, Me: me, Features: proto.M4Features}
	seen := map[proto.PersonID]bool{me: true}
	shown := map[proto.DeviceID]bool{}
	for _, l := range m.lans {
		if !l.joined(me) {
			continue
		}
		lv := proto.LANView{ID: l.id, Name: l.name, Owner: l.owner, CreatedAt: l.created, Mode: l.mode, Relays: m.relayList(l), AddedRelays: slices.Clone(l.added),
			// Empty lists, never null, as the control server sends them (a LAN
			// made in a browser has no device yet).
			Members: []proto.Member{}, Devices: []proto.LANDevice{}}
		if l.mode == proto.LANModeShare && l.owner != me {
			// A member of a device-sharing LAN sees its name, its owner
			// and themself; the devices come as shares.
			lv.Members = []proto.Member{*l.member(l.owner), *l.member(me)}
			seen[l.owner] = true
		} else {
			lv.Members = slices.Clone(l.members)
			for _, mb := range l.members {
				seen[mb.Person] = true
			}
			for _, ld := range l.devs {
				d := m.devices[ld.id]
				mine := m.manages(me, d)
				x := proto.LANDevice{Device: ld.id, JoinedAt: ld.joined, Services: []proto.ServiceState{}}
				for _, s := range d.services {
					if on := ld.on[s.Key()]; mine || on {
						x.Services = append(x.Services, proto.ServiceState{Service: s, On: on})
					}
				}
				lv.Devices = append(lv.Devices, x)
				shown[ld.id] = true
			}
		}
		if l.owner == me {
			for tok, inv := range m.invites {
				if inv.lan == l.id && inv.state == proto.InvitePending {
					lv.Invites = append(lv.Invites, proto.InviteView{ID: inv.id, LAN: l.id, Inviter: inv.inviter, URL: "https://deneb.app/i/" + tok, CreatedAt: l.created, ExpiresAt: inv.expires, State: inv.state})
				}
			}
		}
		v.LANs = append(v.LANs, lv)
	}
	for _, id := range m.order {
		d := m.devices[id]
		if d.loggedOut {
			if m.manages(me, d) {
				v.LoggedOutDevices = append(v.LoggedOutDevices, m.deviceView(me, d))
			}
			continue
		}
		if d.v.Owner == me && d.v.PublicLAN == "" || m.manages(me, d) {
			shown[id] = true
		}
	}
	for _, id := range m.order {
		if shown[id] {
			v.Devices = append(v.Devices, m.deviceView(me, m.devices[id]))
		}
	}
	slices.SortFunc(v.Devices, func(a, b proto.DeviceView) int { return strings.Compare(string(a.ID), string(b.ID)) })
	for _, s := range m.shares {
		if !s.active {
			continue
		}
		d := m.devices[s.device]
		if s.person == me {
			sd := proto.SharedDevice{Share: s.id, From: s.sharer, SharedAt: s.created, Services: []proto.Service{}}
			sd.Device = proto.DeviceView{ID: d.v.ID, Owner: d.v.Owner, Name: d.v.Name, DisplayName: m.displayName(me, d), Kind: d.v.Kind, OS: d.v.OS, Addrs: d.v.Addrs, Online: d.v.Online, LastSeen: d.v.LastSeen, LANs: []proto.LANID{}}
			if d.v.PublicLAN != "" {
				sd.FromLAN = m.lan(d.v.PublicLAN).name
			}
			for _, svc := range d.services {
				if s.on[svc.Key()] {
					sd.Services = append(sd.Services, svc)
				}
			}
			v.SharedWithMe = append(v.SharedWithMe, sd)
			seen[s.sharer] = true
		}
		if m.manages(me, d) {
			seen[s.person] = true
		}
	}
	for _, p := range m.people {
		if seen[p.ID] {
			pp := *p
			if p.ID != me {
				pp.Email = ""
			}
			v.People = append(v.People, pp)
		}
	}
	slices.SortFunc(v.People, func(a, b proto.Person) int { return strings.Compare(string(a.ID), string(b.ID)) })
	// Relays: those my LANs use, and my own.
	used := map[proto.RelayID]bool{}
	for _, lv := range v.LANs {
		for _, r := range lv.Relays {
			used[r] = true
		}
	}
	for _, r := range m.relays {
		if used[r.r.ID] || r.r.Owner == me {
			v.Relays = append(v.Relays, r.r)
		}
		if r.r.Owner == me {
			v.MyRelays = append(v.MyRelays, r.v)
		}
	}
	for _, j := range m.joins {
		if j.owner == me {
			v.RelayJoins = append(v.RelayJoins, j.v)
		}
	}
	v.Seats = m.seats(me)
	if v.LANs == nil {
		v.LANs = []proto.LANView{}
	}
	if v.Devices == nil {
		v.Devices = []proto.DeviceView{}
	}
	if v.Relays == nil {
		v.Relays = []proto.Relay{}
	}
	return v
}

// relayList is a LAN's relays in order: the ones dragged in, then the
// public fallback.
func (m *mock) relayList(l *mlan) []proto.RelayID {
	out := slices.Clone(l.added)
	for _, r := range m.relays {
		if r.r.Source == proto.RelayPublic {
			out = append(out, r.r.ID)
		}
	}
	return out
}

func (m *mock) deviceView(me proto.PersonID, d *mdev) proto.DeviceView {
	dv := d.v
	dv.DisplayName = m.displayName(me, d)
	dv.FQDN = d.v.Name + "." + m.person(d.v.Owner).Name + ".deneb.internal"
	dv.LANs = []proto.LANID{}
	for _, l := range m.lans {
		if l.dev(d.v.ID) != nil && l.joined(me) && (l.mode == proto.LANModeLAN || l.owner == me) {
			dv.LANs = append(dv.LANs, l.id)
		}
	}
	slices.Sort(dv.LANs)
	if m.manages(me, d) {
		dv.Services = slices.Clone(d.services)
		for p, on := range d.refused {
			if on {
				dv.Refused = append(dv.Refused, p)
			}
		}
		slices.Sort(dv.Refused)
		for _, s := range m.shares {
			if s.active && s.device == d.v.ID {
				sv := proto.ShareView{ID: s.id, Person: s.person, Sharer: s.sharer, CreatedAt: s.created, Services: []proto.Service{}}
				for _, svc := range d.services {
					if s.on[svc.Key()] {
						sv.Services = append(sv.Services, svc)
					}
				}
				dv.Shares = append(dv.Shares, sv)
			}
		}
	} else {
		dv.RefusesMe = d.refused[me]
	}
	return dv
}

// seats counts as pricing §1.4 does: me, the people in my LANs, the people
// I share with, each once.
func (m *mock) seats(me proto.PersonID) proto.Seats {
	set := map[proto.PersonID]bool{me: true}
	for _, l := range m.lans {
		if l.owner != me {
			continue
		}
		for _, mb := range l.members {
			if mb.State == proto.MemberJoined {
				set[mb.Person] = true
			}
		}
	}
	for _, s := range m.shares {
		if s.active && s.sharer == me {
			set[s.person] = true
		}
	}
	st := proto.Seats{Used: len(set), Limit: 6, Enforced: true}
	for p := range set {
		st.People = append(st.People, p)
	}
	slices.Sort(st.People)
	st.Left = max(0, st.Limit-st.Used)
	return st
}

// ---- Ops ----------------------------------------------------------------------------

type opError struct {
	status int
	err    proto.Error
}

func fail(status int, code proto.ErrorCode, msg string) *opError {
	return &opError{status: status, err: proto.Error{Code: code, Message: msg}}
}

var (
	errNoDevice = func() *opError { return fail(404, proto.ErrNotFound, "没有这台设备") }
	errNoLAN    = func() *opError { return fail(404, proto.ErrNotFound, "没有这个局域网") }
	errNoRelay  = func() *opError { return fail(404, proto.ErrNotFound, "没有这个中转站") }
)

// apply does one op as me, under the lock. A change bumps the version.
func (m *mock) apply(me proto.PersonID, op proto.Op) (*proto.OpOutput, *opError) {
	a := op.Args
	out := &proto.OpOutput{}
	dev := func() (*mdev, *opError) {
		d := m.devices[a.Device]
		if d == nil {
			return nil, errNoDevice()
		}
		return d, nil
	}
	switch op.Op {
	case proto.OpShareCreate:
		d, e := dev()
		if e != nil {
			return nil, e
		}
		if !m.manages(me, d) {
			return nil, fail(403, proto.ErrForbidden, "只有设备的主人能分享它")
		}
		if a.Person == me {
			return nil, fail(400, proto.ErrInvalid, "不能分享给自己")
		}
		if m.person(a.Person) == nil {
			return nil, fail(404, proto.ErrNotFound, "没有这个人")
		}
		for _, s := range m.shares {
			if s.device == d.v.ID && s.person == a.Person {
				if s.active {
					return nil, fail(409, proto.ErrConflict, "已经有了")
				}
				s.active = true // taken back a moment ago: undone, as it was
				out.Share = s.id
				m.bump()
				return out, nil
			}
		}
		if st := m.seats(me); !slices.Contains(st.People, a.Person) && st.Left == 0 {
			return nil, fail(403, proto.ErrSeatsFull, fmt.Sprintf("%d 个位置都用上了", st.Limit))
		}
		out.Share = m.newShare(d.v.ID, a.Person, me, a.Services, m.now()).id
	case proto.OpShareRevoke, proto.OpShareRelease, proto.OpShareLeave:
		var s *mshare
		for _, x := range m.shares {
			if x.id == a.Share {
				s = x
			}
		}
		if s == nil {
			return nil, fail(404, proto.ErrNotFound, "没有这次分享")
		}
		switch op.Op {
		case proto.OpShareRevoke:
			if !m.manages(me, m.devices[s.device]) {
				return nil, fail(403, proto.ErrForbidden, "只有分享的人能收回")
			}
			s.active = false
		case proto.OpShareLeave:
			if s.person != me {
				return nil, fail(403, proto.ErrForbidden, "这不是分给你的")
			}
			s.active = false
		case proto.OpShareRelease:
			return out, nil // seats are not held in the mock
		}
	case proto.OpInviteAccept:
		tok := inviteToken(a.Invite)
		inv := m.invites[tok]
		if inv == nil {
			return nil, fail(404, proto.ErrNotFound, "这个邀请链接不存在")
		}
		if inv.state == proto.InviteExpired || m.now().After(inv.expires) {
			return nil, fail(409, proto.ErrConflict, "这个邀请过期了")
		}
		l := m.lan(inv.lan)
		if l.joined(me) {
			return nil, fail(409, proto.ErrConflict, "你已经在「"+l.name+"」里了")
		}
		if mb := l.member(me); mb != nil {
			mb.State = proto.MemberJoined
		} else {
			l.members = append(l.members, proto.Member{Person: me, Role: proto.RoleMember, State: proto.MemberJoined, JoinedAt: m.now()})
		}
		out.LAN = l.id
	case proto.OpLANAddDevice, proto.OpLANRemoveDevice:
		d, e := dev()
		if e != nil {
			return nil, e
		}
		l := m.lan(a.LAN)
		if l == nil || !l.joined(me) {
			return nil, errNoLAN()
		}
		if op.Op == proto.OpLANAddDevice {
			if d.v.Owner != me {
				return nil, fail(403, proto.ErrForbidden, "只有设备的主人能把它加进来")
			}
			if l.mode == proto.LANModeShare && l.owner != me {
				return nil, fail(403, proto.ErrForbidden, "这个局域网只放拥有者的设备")
			}
			if l.dev(d.v.ID) != nil {
				return nil, fail(409, proto.ErrConflict, "已经在里面了")
			}
			// New devices open nothing until their owner says (DESIGN §12.5).
			l.devs = append(l.devs, &mlandev{id: d.v.ID, joined: m.now(), on: map[proto.ServiceKey]bool{}})
		} else {
			if d.v.Owner != me && l.owner != me {
				return nil, fail(403, proto.ErrForbidden, "这是"+m.person(d.v.Owner).DisplayName+"的设备，只有"+m.person(d.v.Owner).DisplayName+"能移出")
			}
			l.devs = slices.DeleteFunc(l.devs, func(x *mlandev) bool { return x.id == d.v.ID })
		}
	case proto.OpServiceSet:
		d, e := dev()
		if e != nil {
			return nil, e
		}
		if !m.manages(me, d) {
			return nil, fail(403, proto.ErrForbidden, "只有设备的主人能开关它的服务")
		}
		if a.Service == nil {
			return nil, fail(400, proto.ErrInvalid, "哪一项服务？")
		}
		key := a.Service.Key()
		if a.Share != "" {
			for _, s := range m.shares {
				if s.id == a.Share {
					s.on[key] = a.On
				}
			}
		} else {
			l := m.lan(a.LAN)
			if l == nil || l.dev(d.v.ID) == nil {
				return nil, errNoLAN()
			}
			l.dev(d.v.ID).on[key] = a.On
		}
	case proto.OpDeviceRefuse:
		d, e := dev()
		if e != nil {
			return nil, e
		}
		if !m.manages(me, d) {
			return nil, fail(403, proto.ErrForbidden, "只有设备的主人能决定谁能用")
		}
		d.refused[a.Person] = a.Refuse
	case proto.OpRelayJoin:
		m.seq++
		code := fmt.Sprintf("%sdemo%04d", proto.RelayJoinPrefix, m.seq)
		j := &mjoin{owner: me, code: code, v: proto.RelayJoinView{ID: fmt.Sprintf("j_%d", m.seq), Relay: a.Relay, CreatedAt: m.now(), ExpiresAt: m.now().Add(time.Hour)}}
		m.joins = append(m.joins, j)
		out.RelayJoin = &proto.RelayJoin{ID: j.v.ID, Code: code, Relay: a.Relay, ExpiresAt: j.v.ExpiresAt,
			Command: "docker run -d --name deneb-relay --restart unless-stopped --network host -v deneb-relay:/data deneb-relay:v0 --join " + code + " --region auto"}
	case proto.OpRelayRevokeJoin:
		n := len(m.joins)
		m.joins = slices.DeleteFunc(m.joins, func(j *mjoin) bool { return j.v.ID == a.Join && j.owner == me })
		if len(m.joins) == n {
			return nil, fail(404, proto.ErrNotFound, "没有这条安装命令")
		}
	case proto.OpRelayAdd, proto.OpRelayRemove:
		r := m.relay(a.Relay)
		if r == nil {
			return nil, errNoRelay()
		}
		l := m.lan(a.LAN)
		if l == nil || !l.joined(me) {
			return nil, errNoLAN()
		}
		if op.Op == proto.OpRelayAdd {
			if r.r.Owner != me {
				return nil, fail(403, proto.ErrForbidden, "只有中转站的主人能把它拖进来")
			}
			if slices.Contains(l.added, r.r.ID) {
				return nil, fail(409, proto.ErrConflict, "已经在里面了")
			}
			l.added = append(l.added, r.r.ID)
			r.v.LANs = append(r.v.LANs, l.id)
			slices.Sort(r.v.LANs)
		} else {
			if r.r.Owner != me && l.owner != me {
				return nil, fail(403, proto.ErrForbidden, "只有中转站的主人或局域网的拥有者能拿掉它")
			}
			l.added = slices.DeleteFunc(l.added, func(x proto.RelayID) bool { return x == r.r.ID })
			r.v.LANs = slices.DeleteFunc(r.v.LANs, func(x proto.LANID) bool { return x == l.id })
		}
	case proto.OpRelaySetWeight, proto.OpRelaySetCapacity:
		r := m.relay(a.Relay)
		if r == nil {
			return nil, errNoRelay()
		}
		if r.r.Owner != me {
			return nil, fail(403, proto.ErrForbidden, "只有中转站的主人能调")
		}
		if op.Op == proto.OpRelaySetWeight {
			if a.Weight == nil || *a.Weight < 0 || *a.Weight > 100 {
				return nil, fail(400, proto.ErrInvalid, "权重是 0 到 100")
			}
			r.v.Weight = *a.Weight
		} else {
			if a.Capacity == nil || *a.Capacity < 0 {
				return nil, fail(400, proto.ErrInvalid, "容量不能是负的")
			}
			r.v.Capacity = *a.Capacity
		}
	case proto.OpRelayKick:
		r := m.relay(a.Relay)
		if r == nil {
			return nil, errNoRelay()
		}
		if r.r.Owner != me {
			return nil, fail(403, proto.ErrForbidden, "只有中转站的主人能踢人")
		}
		if a.Person == "" && a.Device == "" {
			return nil, fail(400, proto.ErrInvalid, "踢谁？")
		}
		m.seq++
		k := proto.RelayKick{ID: fmt.Sprintf("k_%d", m.seq), Person: a.Person, Device: a.Device, By: me, At: m.now()}
		if a.Device != "" {
			k.Person = ""
		}
		r.kicks = append(r.kicks, k)
		r.connected = slices.DeleteFunc(r.connected, func(c proto.RelayClient) bool {
			return a.Device != "" && c.Device == a.Device || a.Device == "" && c.Person == a.Person
		})
		m.recount(r)
		out.Kick = k.ID
	case proto.OpRelayUnkick:
		var found bool
		for _, r := range m.relays {
			for i := range r.kicks {
				if r.kicks[i].ID == a.Kick && r.r.Owner == me && r.kicks[i].LiftedAt.IsZero() {
					r.kicks[i].LiftedAt, r.kicks[i].LiftedBy = m.now(), me
					found = true
				}
			}
		}
		if !found {
			return nil, fail(404, proto.ErrNotFound, "没有这次踢人")
		}
	case proto.OpLANCreate:
		id, e := m.createLAN(me, a.Name)
		if e != nil {
			return nil, e
		}
		out.LAN = id
	case proto.OpDeviceDelete:
		if e := m.deleteDevice(me, a.Device); e != nil {
			return nil, e
		}
	case proto.OpAccountDelete:
		acc, e := m.deleteAccount(me, a)
		if e != nil {
			return nil, e
		}
		out.Account = acc
		if a.DryRun {
			return out, nil // nothing changed
		}
	default:
		return nil, fail(400, proto.ErrInvalid, "网页版的演示还没有这个操作："+string(op.Op))
	}
	m.bump()
	return out, nil
}

// recount keeps a relay's load in step with who is on it.
func (m *mock) recount(r *mrelay) {
	if r.load == nil {
		return
	}
	people := map[proto.PersonID]bool{}
	for _, c := range r.connected {
		people[c.Person] = true
	}
	r.load.Devices, r.load.People = len(r.connected), len(people)
}

// inviteToken takes the token from a link (https://…/i/<token>) or the
// token itself.
func inviteToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// ---- HTTP -------------------------------------------------------------------------

func (m *mock) handler() http.Handler {
	mux := http.NewServeMux()
	cop := http.NewCrossOriginProtection()
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(jsonOnly(h)) }
	mux.Handle("POST "+proto.PathWebLogin, post(m.login))
	mux.Handle("POST "+proto.PathWebLoginEmail, post(m.login))
	mux.Handle("POST "+proto.PathWebLoginCode, post(m.login))
	mux.HandleFunc("GET "+proto.PathWebInvites+"{token}", func(w http.ResponseWriter, r *http.Request) { m.preview(w, r, r.PathValue("token")) })
	mux.Handle("POST "+proto.PathWebLogout, post(m.logout))
	mux.Handle("POST "+proto.PathWebInvite, post(m.invite))
	mux.HandleFunc("GET "+proto.PathWebUserView, m.authed(m.userView))
	mux.HandleFunc("GET "+proto.PathWebStream, m.authed(m.stream))
	mux.Handle("POST "+proto.PathWebOps, post(m.authed(m.ops)))
	mux.HandleFunc("GET "+proto.PathWebRelayUsage, m.authed(m.relayUsage))
	// The demo's stand-ins for what happens away from the browser.
	mux.Handle("POST /api/v1/_mock/device-up", post(m.authed(m.deviceUp)))
	mux.Handle("POST /api/v1/_mock/relay-up", post(m.authed(m.relayUp)))
	mux.Handle("POST /api/v1/_mock/share", post(m.authed(m.shareMe)))
	mux.Handle("POST /api/v1/_mock/seed", post(m.authed(m.seedMe)))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, fail(404, proto.ErrNotFound, "没有这个接口"))
	})
	return mux
}

// jsonOnly takes JSON bodies only, as the control server does (m4 §14): a
// form another site posts cannot be one.
func jsonOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			writeErr(w, fail(415, proto.ErrInvalid, "要 JSON"))
			return
		}
		h(w, r)
	}
}

func (m *mock) authed(h func(http.ResponseWriter, *http.Request, proto.PersonID)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(proto.WebSessionCookie)
		var me proto.PersonID
		if err == nil {
			m.mu.Lock()
			me = m.sessions[c.Value]
			m.mu.Unlock()
		}
		if me == "" {
			writeErr(w, fail(401, proto.ErrUnauthorized, "请先登录"))
			return
		}
		h(w, r, me)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, e *opError) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(proto.ErrorBody{Error: &e.err})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeErr(w, fail(400, proto.ErrInvalid, "请求的格式不对"))
		return false
	}
	return true
}

func (m *mock) login(w http.ResponseWriter, r *http.Request) {
	var req proto.WebLoginRequest
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	at := strings.IndexByte(email, '@')
	if at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t") {
		writeErr(w, fail(400, proto.ErrInvalid, "邮箱的样子不对"))
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.emails[email]
	if !ok {
		// A newcomer, as one who opened an invite link: an account is made.
		name := email[:at]
		id = proto.PersonID("p_" + name)
		m.people = append(m.people, &proto.Person{ID: id, Name: name, DisplayName: name, Initial: strings.ToUpper(name[:1]), Email: email})
		m.emails[email] = id
	}
	p := m.person(id)
	if req.Code == "" {
		writeJSON(w, proto.WebLoginResult{Step: "code", Account: "登录到" + spaced(p.DisplayName) + "（" + email + "）"})
		return
	}
	if strings.TrimSpace(req.Code) != "000000" {
		writeErr(w, fail(400, proto.ErrInvalid, "验证码不对"))
		return
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	m.sessions[tok] = id
	http.SetCookie(w, &http.Cookie{Name: proto.WebSessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600})
	writeJSON(w, proto.WebLoginResult{Step: "done", Person: id})
}

func (m *mock) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(proto.WebSessionCookie); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.bump() // a stream of this session sees it end
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: proto.WebSessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, struct{}{})
}

func (m *mock) invite(w http.ResponseWriter, r *http.Request) {
	var req proto.InviteLookup
	if !decode(w, r, &req) {
		return
	}
	m.preview(w, r, req.Invite)
}

// preview answers what an invite link is for, logged in or not.
func (m *mock) preview(w http.ResponseWriter, r *http.Request, link string) {
	var me proto.PersonID
	if c, err := r.Cookie(proto.WebSessionCookie); err == nil {
		m.mu.Lock()
		me = m.sessions[c.Value]
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inv := m.invites[inviteToken(link)]
	if inv == nil {
		writeErr(w, fail(404, proto.ErrNotFound, "这个邀请链接不存在"))
		return
	}
	l := m.lan(inv.lan)
	p := *m.person(inv.inviter)
	p.Email = ""
	state := inv.state
	if state == proto.InvitePending && m.now().After(inv.expires) {
		state = proto.InviteExpired
	}
	writeJSON(w, proto.InvitePreview{Invite: inv.id, LAN: l.id, LANName: l.name, Inviter: p, State: state, ExpiresAt: inv.expires, AlreadyIn: me != "" && l.joined(me)})
}

func (m *mock) userView(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	m.mu.Lock()
	v := m.view(me)
	m.mu.Unlock()
	writeJSON(w, v)
}

// stream sends the view whenever it changes (and at once if the client's
// is older), and a keep-alive after 15 quiet seconds; the session's logout
// ends it with loggedOut.
func (m *mock) stream(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fail(500, proto.ErrInternal, "不能推送"))
		return
	}
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	c, _ := r.Cookie(proto.WebSessionCookie)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	for {
		m.mu.Lock()
		live := m.sessions[c.Value] == me
		ch := m.changed
		var f *proto.StreamFrame
		if live && m.version > since {
			v := m.view(me)
			f = &proto.StreamFrame{Version: v.Version, UserView: v}
			since = v.Version
		}
		m.mu.Unlock()
		if !live {
			_ = enc.Encode(proto.StreamFrame{LoggedOut: true})
			fl.Flush()
			return
		}
		if f != nil {
			if enc.Encode(f) != nil {
				return
			}
			fl.Flush()
		}
		select {
		case <-ch:
		case <-time.After(15 * time.Second):
			if enc.Encode(proto.StreamFrame{KeepAlive: true}) != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (m *mock) ops(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	var op proto.Op
	if !decode(w, r, &op) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if op.ID != "" {
		if res, ok := m.done[string(me)+"/"+op.ID]; ok {
			writeJSON(w, res)
			return
		}
	}
	out, e := m.apply(me, op)
	if e != nil {
		writeErr(w, e)
		return
	}
	res := &proto.OpResult{Version: m.version}
	if *out != (proto.OpOutput{}) {
		res.Result = out
	}
	if op.ID != "" {
		m.done[string(me)+"/"+op.ID] = res
	}
	writeJSON(w, res)
}

func (m *mock) relayUsage(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := proto.RelayUsage{Month: m.now().Format("2006-01"), Relays: []proto.RelayMonth{}}
	for _, rl := range m.relays {
		if rl.r.Owner != me {
			continue // a public relay's usage is the deployment's to see
		}
		rm := proto.RelayMonth{Relay: rl.r.ID, LANs: []proto.UsageLine{}, Shares: []proto.UsageLine{}, Devices: []proto.UsageLine{}, Load: rl.load, Kicks: rl.kicks}
		for lan, b := range rl.lanBytes {
			rm.LANs = append(rm.LANs, proto.UsageLine{LAN: lan, Bytes: b})
			rm.Bytes += b
		}
		v := m.view(me)
		visible := map[proto.DeviceID]bool{}
		for _, d := range v.Devices {
			visible[d.ID] = true
		}
		for _, c := range rl.connected {
			if !visible[c.Device] {
				c.Device = "" // someone I cannot see: their person only
			}
			rm.Connected = append(rm.Connected, c)
		}
		u.Relays = append(u.Relays, rm)
	}
	writeJSON(w, u)
}

// deviceUp stands in for Deneb installed and logged in on this computer
// (M3-11's last step): a new laptop of mine, in the LANs I joined.
func (m *mock) deviceUp(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := proto.DeviceID(fmt.Sprintf("d_new%d", m.seq))
	d := &mdev{v: proto.DeviceView{ID: id, Owner: me, Name: "MacBook", Kind: proto.KindLaptop, OS: proto.MacOS, Online: true,
		Addrs: []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 1, byte(m.seq)}), 32)}}, services: []proto.Service{svcScreen, svcFiles}, refused: map[proto.PersonID]bool{}}
	m.devices[id] = d
	m.order = append(m.order, id)
	for _, l := range m.lans {
		if l.joined(me) && l.mode == proto.LANModeLAN {
			l.devs = append(l.devs, &mlandev{id: id, joined: m.now(), on: map[proto.ServiceKey]bool{}})
		}
	}
	m.bump()
	writeJSON(w, map[string]string{"device": string(id)})
}

// shareMe stands in for someone else sharing a device with me, or taking
// it back (共享给我: a device pops up, or goes with a word).
func (m *mock) shareMe(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	var req struct {
		Device proto.DeviceID `json:"device"`
		Sharer proto.PersonID `json:"sharer"`
		On     bool           `json:"on"`
	}
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.devices[req.Device] == nil || m.person(req.Sharer) == nil {
		http.Error(w, "no such device or person", http.StatusNotFound)
		return
	}
	var s *mshare
	for _, x := range m.shares {
		if x.device == req.Device && x.person == me {
			s = x
		}
	}
	switch {
	case req.On && s == nil:
		s = m.newShare(req.Device, me, req.Sharer, nil, m.now())
	case s != nil:
		s.active = req.On
	}
	m.bump()
	writeJSON(w, map[string]any{"share": s})
}

// relayUp stands in for the install command run on a server: the code is
// used, and the relay shows up in my list (§13 of m4/control.md).
func (m *mock) relayUp(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	var req struct {
		Join string `json:"join"`
	}
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var j *mjoin
	for _, x := range m.joins {
		if x.owner == me && (req.Join == "" || x.v.ID == req.Join) {
			j = x
		}
	}
	if j == nil {
		writeErr(w, fail(404, proto.ErrNotFound, "没有等着安装的命令"))
		return
	}
	m.joins = slices.DeleteFunc(m.joins, func(x *mjoin) bool { return x == j })
	m.seq++
	id := proto.RelayID(fmt.Sprintf("r_new%d", m.seq))
	m.relays = append(m.relays, &mrelay{
		r:        proto.Relay{ID: id, Name: "新加坡", Source: proto.RelayServer, Owner: me, HostName: "198.51.100.9", DERPPort: 47445, STUNPort: 47478},
		v:        proto.RelayView{ID: id, LANs: []proto.LANID{}, Registered: true, Online: true, Since: m.now(), Weight: 1},
		load:     &proto.RelayLoad{At: m.now(), CPU: 0.01, Memory: 20 << 20, MemoryLimit: 512 << 20},
		lanBytes: map[proto.LANID]int64{},
	})
	m.bump()
	writeJSON(w, map[string]string{"relay": string(id)})
}
