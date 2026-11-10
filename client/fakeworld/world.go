// Package fakeworld is a pretend control server and network behind a fake
// engine (engine/localapi/fake), for the client's live store: in its tests,
// and behind the dev window before the real engine runs (DENEB_FAKE=1). It
// is the M1 acceptance topology (implementation.md §3 M1): kittors on this
// Mac with 构建服务器, and 阿杰 with his desktop and his NAS.
//
// It keeps the people, LANs and devices as the control server does, decides
// ops by the server's rules (only a device's owner moves it, only a LAN's
// owner invites), and hands the engine kittors's user view and network map
// after each change, under one version. It moves paths as the real engine
// does: a device this computer starts to reach goes through the relay
// first, then direct where it can (the PoC's startup test: 0.12 s, 0.63 s);
// one it stops reaching goes.
package fakeworld

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/proto"
)

// The topology's ids: the fake engine's, so that its own answers (open,
// ping, resolve) know the same devices.
const (
	Me      = proto.PersonID(fake.Me)
	Jie     = proto.PersonID(fake.Jie)
	ThisMac = proto.DeviceID(fake.ThisMac)
	Build   = proto.DeviceID(fake.Build)
	JiePC   = proto.DeviceID(fake.JiePC)
	JieNAS  = proto.DeviceID(fake.JieNAS)
	Studio  = proto.LANID("studio")
	Weekend = proto.LANID("weekend")
	HK      = proto.RelayID(fake.HongKong)
)

// InviteLink is 阿杰's invite into 周末联机, as his landing page hands it to
// the app.
const InviteLink = proto.JoinLinkBase + inviteToken

const inviteToken = "weekendjie"

// Start is where the world starts.
type Start int

const (
	// Fresh: no one has logged in on this Mac yet; kittors has no LAN, and
	// 阿杰 has 周末联机 with his two devices, to invite kittors into. The
	// acceptance steps 1–3 start here.
	Fresh Start = iota
	// Ready: as the acceptance topology ends up: kittors's 工作室 with this
	// Mac and 构建服务器, and 阿杰's 周末联机 with his two devices and this
	// Mac, 构建服务器 in its tray. Steps 7 and 9 start here.
	Ready
)

// World is the pretend control server and network.
type World struct {
	E *fake.Engine

	mu      sync.Mutex
	version uint64
	people  []proto.Person
	devices map[proto.DeviceID]*device
	order   []proto.DeviceID
	lans    []*lan
	nextLAN int
	// gen counts each device's connections, so that a path set on its way
	// up never lands after the device was let go and reached again.
	gen    map[proto.DeviceID]int
	relays map[proto.DeviceID]bool // devices this Mac reaches through the relay only
}

type device struct {
	id            proto.DeviceID
	owner         proto.PersonID
	name, display string
	kind          proto.DeviceKind
	os            proto.OS
	addr          netip.Addr
	catalog       []proto.Service
	refused       map[proto.PersonID]bool
}

type lan struct {
	id      proto.LANID
	name    string
	owner   proto.PersonID
	members []proto.PersonID
	devices []proto.DeviceID
	// open is what each device opens in the LAN, by service key. It stays
	// when the device leaves, so that bringing it back brings it all back.
	open map[proto.DeviceID]map[proto.ServiceKey]bool
}

func svc(kind proto.ServiceKind, name string, p proto.IPProto, port uint16) proto.Service {
	return proto.Service{Kind: kind, Name: name, Ports: []proto.PortRange{proto.Port(p, port)}}
}

// New returns the world as start has it, its engine set up to match.
func New(start Start) *World {
	w := &World{
		E:       fake.New(),
		devices: map[proto.DeviceID]*device{},
		gen:     map[proto.DeviceID]int{},
		relays:  map[proto.DeviceID]bool{JieNAS: true},
		people: []proto.Person{
			{ID: Me, Name: "kittors", DisplayName: "kittors", Initial: "K", Email: "kittors@example.com"},
			{ID: Jie, Name: "jie", DisplayName: "阿杰", Initial: "杰"},
		},
	}
	add := func(id proto.DeviceID, owner proto.PersonID, name, display string, kind proto.DeviceKind, os proto.OS, addr string, catalog ...proto.Service) {
		w.devices[id] = &device{id: id, owner: owner, name: name, display: display, kind: kind, os: os,
			addr: netip.MustParseAddr(addr), catalog: catalog, refused: map[proto.PersonID]bool{}}
		w.order = append(w.order, id)
	}
	add(ThisMac, Me, "mac", "kittors 的 MacBook", proto.KindLaptop, proto.MacOS, "10.77.0.1",
		svc(proto.ServiceScreen, "屏幕共享", proto.TCP, 5900), svc(proto.ServiceFiles, "", proto.TCP, 445))
	add(Build, Me, "build", "构建服务器", proto.KindServer, proto.Linux, "10.77.0.2",
		svc(proto.ServiceWeb, "", proto.TCP, 8080), svc(proto.ServiceShell, "", proto.TCP, 22))
	add(JiePC, Jie, "desktop", "阿杰的台式机", proto.KindDesktop, proto.Windows, "10.77.0.3",
		svc(proto.ServiceScreen, "", proto.TCP, 3389), svc(proto.ServiceGame, "游戏服务器", proto.UDP, 27015))
	add(JieNAS, Jie, "nas", "阿杰的 NAS", proto.KindNAS, proto.Linux, "10.77.0.4",
		svc(proto.ServiceFiles, "", proto.TCP, 445), svc(proto.ServiceWeb, "", proto.TCP, 5000))
	weekend := &lan{id: Weekend, name: "周末联机", owner: Jie, members: []proto.PersonID{Jie},
		devices: []proto.DeviceID{JiePC, JieNAS}, open: map[proto.DeviceID]map[proto.ServiceKey]bool{
			JiePC:  {key(proto.TCP, 3389): true, key(proto.UDP, 27015): true},
			JieNAS: {key(proto.TCP, 445): true, key(proto.TCP, 5000): false},
		}}
	w.lans = []*lan{weekend}
	if start == Ready {
		studio := &lan{id: Studio, name: "工作室", owner: Me, members: []proto.PersonID{Me},
			devices: []proto.DeviceID{ThisMac, Build}, open: map[proto.DeviceID]map[proto.ServiceKey]bool{
				ThisMac: {key(proto.TCP, 5900): true},
				Build:   {key(proto.TCP, 8080): true, key(proto.TCP, 22): true},
			}}
		w.lans = []*lan{studio, weekend}
		weekend.members = append(weekend.members, Me)
		weekend.devices = append(weekend.devices, ThisMac)
		weekend.open[ThisMac] = map[proto.ServiceKey]bool{key(proto.TCP, 445): true}
	}
	w.E.HandleOps(w.op)
	w.E.HandlePreview(w.preview)
	w.mu.Lock()
	w.publishLocked()
	w.mu.Unlock()
	if start == Fresh {
		w.E.Update(func(st *apitype.State) {
			st.LoggedIn, st.User, st.SelfDeviceID = false, "", ""
		})
	} else {
		// This Mac has reached its peers for hours: the paths it settled on.
		var paths []apitype.PeerPath
		for _, p := range w.E.Snapshot().Peers {
			pp := w.settled(proto.DeviceID(p.ID))
			pp.Since = time.Now().Add(-2 * time.Hour)
			paths = append(paths, pp)
		}
		w.E.PushPaths(paths...)
	}
	return w
}

func key(p proto.IPProto, port uint16) proto.ServiceKey {
	return proto.Service{Ports: []proto.PortRange{proto.Port(p, port)}}.Key()
}

// settled is the path this Mac ends up on to dev: through the relay for 阿杰's
// NAS, behind a NAT that lets nothing through, and direct for the others.
func (w *World) settled(dev proto.DeviceID) apitype.PeerPath {
	if w.relays[dev] {
		return apitype.PeerPath{DeviceID: apitype.DeviceID(dev), Kind: apitype.PathRelay, Relay: apitype.RelayID(HK), RTTMs: 52, Since: time.Now()}
	}
	return apitype.PeerPath{DeviceID: apitype.DeviceID(dev), Kind: apitype.PathDirect, Endpoint: "198.51.100.7:41641", RTTMs: 9, Since: time.Now()}
}

func (w *World) lan(id proto.LANID) *lan {
	for _, l := range w.lans {
		if l.id == id {
			return l
		}
	}
	return nil
}

// ---- The control server -------------------------------------------------------------

// op decides an op as the control server would, for kittors on this Mac.
func (w *World) op(req apitype.OpRequest) (*apitype.OpResult, *apitype.Error) {
	var args proto.OpArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "看不懂这个操作。"}
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out, aerr := w.decideLocked(Me, ThisMac, proto.OpType(req.Op), args)
	if aerr != nil {
		return nil, aerr
	}
	res := &apitype.OpResult{Version: w.publishLocked()}
	if out != nil {
		res.Result, _ = json.Marshal(out)
	}
	return res, nil
}

// decideLocked applies op, asked for by who on from, or says why not.
func (w *World) decideLocked(who proto.PersonID, from proto.DeviceID, op proto.OpType, a proto.OpArgs) (*proto.OpOutput, *apitype.Error) {
	no := func(code apitype.ErrorCode, msg string) (*proto.OpOutput, *apitype.Error) {
		return nil, &apitype.Error{Code: code, Message: msg}
	}
	l := w.lan(a.LAN)
	member := l != nil && slices.Contains(l.members, who)
	switch op {
	case proto.OpLANCreate:
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return no(apitype.ErrInvalid, "给局域网起个名字。")
		}
		w.nextLAN++
		nl := &lan{id: proto.LANID(fmt.Sprintf("lan%d", w.nextLAN)), name: name, owner: who,
			members: []proto.PersonID{who}, devices: []proto.DeviceID{from},
			open: map[proto.DeviceID]map[proto.ServiceKey]bool{}}
		w.lans = append(w.lans, nl)
		return &proto.OpOutput{LAN: nl.id}, nil
	case proto.OpLANInvite:
		if l == nil || !member {
			return no(apitype.ErrNotFound, "找不到这个局域网。")
		}
		if l.owner != who {
			return no(apitype.ErrForbidden, "只有拥有者能邀请朋友。")
		}
		url := proto.InviteURL("https://deneb.example", "inv"+string(l.id))
		return &proto.OpOutput{LAN: l.id, Invite: &proto.InviteView{LAN: l.id, Inviter: who, URL: url, State: proto.InvitePending}}, nil
	case proto.OpInviteAccept:
		tok, err := proto.ParseInvite(a.Invite)
		if err != nil || tok != inviteToken {
			return no(apitype.ErrNotFound, "这个邀请链接无效，或者已经过期。")
		}
		wl := w.lan(Weekend)
		if !slices.Contains(wl.members, who) {
			wl.members = append(wl.members, who)
		}
		if !slices.Contains(wl.devices, from) {
			wl.devices = append(wl.devices, from)
		}
		return &proto.OpOutput{LAN: Weekend}, nil
	case proto.OpInviteDecline:
		return nil, nil
	case proto.OpLANAddDevice, proto.OpLANRemoveDevice, proto.OpServiceSet:
		d := w.devices[a.Device]
		if l == nil || !member || d == nil {
			return no(apitype.ErrNotFound, "找不到这台设备或这个局域网。")
		}
		if d.owner != who {
			owner := w.display(d.owner)
			if op == proto.OpServiceSet {
				return no(apitype.ErrForbidden, "这是"+spaced(owner)+"的设备，只有"+spaced(owner)+"能改。")
			}
			return no(apitype.ErrForbidden, "这是"+spaced(owner)+"的设备，只有"+spaced(owner)+"能移出。")
		}
		switch op {
		case proto.OpLANAddDevice:
			if !slices.Contains(l.devices, d.id) {
				l.devices = append(l.devices, d.id)
			}
		case proto.OpLANRemoveDevice:
			l.devices = slices.DeleteFunc(l.devices, func(x proto.DeviceID) bool { return x == d.id })
		case proto.OpServiceSet:
			if a.Service == nil || !slices.Contains(l.devices, d.id) {
				return no(apitype.ErrInvalid, "这台设备不在这个局域网里。")
			}
			k := a.Service.Key()
			if !slices.ContainsFunc(d.catalog, func(s proto.Service) bool { return s.Key() == k }) {
				d.catalog = append(d.catalog, *a.Service)
			}
			if l.open[d.id] == nil {
				l.open[d.id] = map[proto.ServiceKey]bool{}
			}
			l.open[d.id][k] = a.On
		}
		return nil, nil
	case proto.OpDeviceRefuse:
		d := w.devices[a.Device]
		if d == nil {
			return no(apitype.ErrNotFound, "找不到这台设备。")
		}
		if d.owner != who {
			return no(apitype.ErrForbidden, "只有设备的主人能决定谁能用它。")
		}
		if a.Refuse {
			d.refused[a.Person] = true
		} else {
			delete(d.refused, a.Person)
		}
		return nil, nil
	case proto.OpDeviceRename, proto.OpDeviceSetKind:
		// As M1's server has them: a new name keeps the DNS label.
		d := w.devices[a.Device]
		if d == nil {
			return no(apitype.ErrNotFound, "找不到这台设备。")
		}
		if d.owner != who {
			return no(apitype.ErrForbidden, "只有设备的主人能改它。")
		}
		if op == proto.OpDeviceRename {
			if strings.TrimSpace(a.Name) == "" {
				return no(apitype.ErrInvalid, "给设备起个名字。")
			}
			d.display = strings.TrimSpace(a.Name)
		} else {
			d.kind = a.Kind
		}
		return nil, nil
	}
	return no(apitype.ErrInvalid, "这个操作还不支持。")
}

func (w *World) display(id proto.PersonID) string {
	for _, p := range w.people {
		if p.ID == id {
			return p.DisplayName
		}
	}
	return string(id)
}

func spaced(name string) string {
	if name != "" && name[0] < 0x80 {
		return " " + name + " "
	}
	return name
}

func (w *World) preview(invite string) (json.RawMessage, *apitype.Error) {
	tok, err := proto.ParseInvite(invite)
	if err != nil || tok != inviteToken {
		return nil, &apitype.Error{Code: apitype.ErrNotFound, Message: "这个邀请链接无效，或者已经过期。"}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	wl := w.lan(Weekend)
	p := proto.InvitePreview{Invite: "inv-weekend", LAN: Weekend, LANName: wl.name, Inviter: w.people[1],
		State: proto.InvitePending, ExpiresAt: time.Now().Add(7 * 24 * time.Hour), AlreadyIn: slices.Contains(wl.members, Me)}
	b, _ := json.Marshal(p)
	return b, nil
}

// ---- What 阿杰 and the network do --------------------------------------------------------

// JieTakesOut is 阿杰 taking one of his devices out of 周末联机 on his
// computer (acceptance step 5), or bringing it back with in.
func (w *World) JieTakesOut(dev proto.DeviceID, in bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	op := proto.OpLANRemoveDevice
	if in {
		op = proto.OpLANAddDevice
	}
	if _, aerr := w.decideLocked(Jie, JiePC, op, proto.OpArgs{LAN: Weekend, Device: dev}); aerr != nil {
		return aerr
	}
	w.publishLocked()
	return nil
}

// SetPath is the network changing under this Mac's path to dev, as the
// engine would report it.
func (w *World) SetPath(dev proto.DeviceID, kind apitype.PathKind) {
	p := apitype.PeerPath{DeviceID: apitype.DeviceID(dev), Kind: kind, Since: time.Now()}
	switch kind {
	case apitype.PathRelay:
		p.Relay, p.RTTMs = apitype.RelayID(HK), 52
	case apitype.PathDirect:
		p.RTTMs = 9
	}
	w.E.PushPaths(p)
}

// ---- What the engine is told ---------------------------------------------------------

// publishLocked hands the engine the new user view and network map under a
// new version, and starts connecting to the devices this Mac sees anew.
func (w *World) publishLocked() uint64 {
	w.version++
	view := w.userViewLocked(Me)
	view.Version = w.version
	raw, _ := json.Marshal(view)
	self, scopes, peers := w.netmapLocked(ThisMac)
	var fresh []proto.DeviceID
	version := w.version
	w.E.Update(func(st *apitype.State) {
		st.Version = version
		st.UserView = raw
		st.Self, st.Scopes, st.Peers = self, scopes, peers
		st.Relays = []apitype.Relay{{ID: apitype.RelayID(HK), Name: "香港", Connected: true, Home: true}}
		keep := st.Paths[:0:0]
		had := map[apitype.DeviceID]bool{}
		for _, p := range st.Paths {
			if slices.ContainsFunc(peers, func(q apitype.Peer) bool { return q.ID == p.DeviceID }) {
				keep = append(keep, p)
				had[p.DeviceID] = true
			}
		}
		for _, q := range peers {
			if !had[q.ID] {
				keep = append(keep, apitype.PeerPath{DeviceID: q.ID, Kind: apitype.PathConnecting, Since: time.Now()})
				fresh = append(fresh, proto.DeviceID(q.ID))
			}
		}
		st.Paths = keep
	})
	for _, id := range fresh {
		w.gen[id]++
		w.connect(id, w.gen[id])
	}
	return w.version
}

// connect plays this Mac reaching dev anew: through the relay after 0.12 s,
// then direct after 0.63 s where it can be.
func (w *World) connect(dev proto.DeviceID, gen int) {
	still := func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.gen[dev] == gen && slices.ContainsFunc(w.E.Snapshot().Peers, func(p apitype.Peer) bool { return p.ID == apitype.DeviceID(dev) })
	}
	time.AfterFunc(120*time.Millisecond, func() {
		if still() {
			w.E.PushPaths(apitype.PeerPath{DeviceID: apitype.DeviceID(dev), Kind: apitype.PathRelay, Relay: apitype.RelayID(HK), RTTMs: 52, Since: time.Now()})
		}
	})
	if w.relays[dev] {
		return
	}
	time.AfterFunc(630*time.Millisecond, func() {
		if still() {
			w.E.PushPaths(w.settled(dev))
		}
	})
}

// userViewLocked is who's world as the control server sends it: their LANs,
// the people and devices in them, and all of their own devices.
func (w *World) userViewLocked(who proto.PersonID) proto.UserView {
	v := proto.UserView{Me: who, Relays: []proto.Relay{{ID: HK, Name: "香港", Source: proto.RelayServer, HostName: "hk.example", DERPPort: 443}}}
	seen := map[proto.PersonID]bool{who: true}
	visible := map[proto.DeviceID][]proto.LANID{}
	for _, l := range w.lans {
		if !slices.Contains(l.members, who) {
			continue
		}
		lv := proto.LANView{ID: l.id, Name: l.name, Owner: l.owner, Relays: []proto.RelayID{HK}}
		for _, m := range l.members {
			role := proto.RoleMember
			if m == l.owner {
				role = proto.RoleOwner
			}
			lv.Members = append(lv.Members, proto.Member{Person: m, Role: role, State: proto.MemberJoined})
			seen[m] = true
		}
		for _, id := range l.devices {
			d := w.devices[id]
			ld := proto.LANDevice{Device: id, Services: []proto.ServiceState{}}
			for _, s := range d.catalog {
				on := l.open[id][s.Key()]
				// Another's device shows only what it opens.
				if d.owner == who || on {
					ld.Services = append(ld.Services, proto.ServiceState{Service: s, On: on})
				}
			}
			lv.Devices = append(lv.Devices, ld)
			visible[id] = append(visible[id], l.id)
		}
		v.LANs = append(v.LANs, lv)
	}
	for _, p := range w.people {
		if seen[p.ID] {
			if p.ID != who {
				p.Email = ""
			}
			v.People = append(v.People, p)
		}
	}
	for _, id := range w.order {
		d := w.devices[id]
		if d.owner != who && visible[id] == nil {
			continue
		}
		dv := proto.DeviceView{ID: id, Owner: d.owner, Name: d.name, DisplayName: d.display,
			FQDN: proto.FQDN(d.name, w.handle(d.owner)), Kind: d.kind, OS: d.os,
			Addrs: []netip.Prefix{netip.PrefixFrom(d.addr, 32)}, Online: true, LANs: visible[id]}
		if d.owner == who {
			dv.Services = slices.Clone(d.catalog)
			for p := range d.refused {
				dv.Refused = append(dv.Refused, p)
			}
			slices.Sort(dv.Refused)
		} else {
			dv.RefusesMe = d.refused[who]
		}
		v.Devices = append(v.Devices, dv)
	}
	return v
}

func (w *World) handle(id proto.PersonID) string {
	for _, p := range w.people {
		if p.ID == id {
			return p.Name
		}
	}
	return string(id)
}

// netmapLocked is what the engine on self enforces: the LANs it is in, and
// the devices it shares one of them with.
func (w *World) netmapLocked(self proto.DeviceID) (*apitype.Device, []apitype.Scope, []apitype.Peer) {
	me := w.devices[self]
	selfDev := w.apiDevice(me)
	var scopes []apitype.Scope
	shared := map[proto.DeviceID][]apitype.ScopeID{}
	for _, l := range w.lans {
		if !slices.Contains(l.devices, self) {
			continue
		}
		sc := apitype.Scope{ID: apitype.ScopeID(proto.LANScope(l.id)), Name: l.name, Open: []apitype.Service{}, Relays: []apitype.RelayID{apitype.RelayID(HK)}}
		for _, s := range me.catalog {
			if l.open[self][s.Key()] {
				for _, p := range s.Ports {
					sc.Open = append(sc.Open, apitype.Service{Kind: string(s.Kind), Proto: string(p.Proto), Port: p.First})
				}
			}
		}
		scopes = append(scopes, sc)
		for _, id := range l.devices {
			if id != self {
				shared[id] = append(shared[id], sc.ID)
			}
		}
	}
	var peers []apitype.Peer
	for _, id := range w.order {
		if s := shared[id]; s != nil {
			d := w.devices[id]
			peers = append(peers, apitype.Peer{Device: *w.apiDevice(d), Scopes: s, Online: true,
				RefusedByPeer: d.refused[me.owner], RefusedByMe: me.refused[d.owner]})
		}
	}
	return selfDev, scopes, peers
}

func (w *World) apiDevice(d *device) *apitype.Device {
	return &apitype.Device{ID: apitype.DeviceID(d.id), Name: d.name, DisplayName: d.display, Owner: apitype.PersonID(d.owner),
		OwnerName: w.handle(d.owner), FQDN: proto.FQDN(d.name, w.handle(d.owner)), Addrs: []string{d.addr.String()},
		Kind: string(d.kind), OS: string(d.os)}
}
