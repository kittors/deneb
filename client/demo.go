package client

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/client/model"
)

// Demo is the store of the demo mode (deneb --demo, the recordings and the
// view's tests): the design's sample LAN, 周末联机, and a second one, held
// in memory. Its ops take effect at once and always go through, so the view
// behaves as the prototype that wrote these values into its state did; the
// ids are the prototype's too, as the light on each link and the bend of
// each link are worked out from them. Simulate plays a few outside events.
type Demo struct {
	// netPaths are the paths this computer had before it lost its network
	// ("nonet"), to come back to.
	netPaths map[model.DeviceID]model.Path
	mu       sync.Mutex
	snap     *model.Snapshot
	subs     map[int]func(*model.Snapshot)
	next     int
	manual   bool // events play only as Simulate is called: for recordings
	m3       demoM3
	m4       demoM4
	m7seed   bool // M7: the deployment's seed names my account ("m7-seed")
}

// Manual makes the demo's events play only as Simulate is called, never on
// a timer, so that a recording knows when each comes.
func (d *Demo) Manual() { d.manual = true }

// demoUses are who uses which of this computer's services in the demo's
// world: 阿杰's desktop, its 远程桌面.
var demoUses = []struct {
	by  model.DeviceID
	key string
}{{"jdesk", "tcp/3389"}}

// recentCuts are the cuts of the last few seconds (cutKeep, as the live
// store keeps them): the view has played the others.
func recentCuts(cuts []model.Cut) []model.Cut {
	return slices.DeleteFunc(slices.Clone(cuts), func(c model.Cut) bool { return time.Since(c.At) > cutKeep })
}

// NewDemo returns the demo's store, as the design's sample shows it.
func NewDemo() *Demo { return &Demo{snap: demoSnapshot(), subs: map[int]func(*model.Snapshot){}} }

func (*Demo) Immediate() {}

func (d *Demo) Snapshot() *model.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snap
}

func (d *Demo) Subscribe(fn func(*model.Snapshot)) (cancel func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := d.next
	d.next++
	d.subs[id] = fn
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.subs, id)
	}
}

// update makes fn's change to the snapshot the snapshot, one version on,
// and tells the subscribers. fn runs under the lock, so that the view's ops
// and Simulate's timers never undo each other's changes; it returns its
// argument when it changes nothing.
func (d *Demo) update(fn func(*model.Snapshot) *model.Snapshot) *model.Snapshot {
	d.mu.Lock()
	s := d.snap
	n := fn(s)
	if n == s {
		d.mu.Unlock()
		return s
	}
	n.Version = s.Version + 1
	d.snap = n
	subs := make([]func(*model.Snapshot), 0, len(d.subs))
	for _, f := range d.subs {
		subs = append(subs, f)
	}
	d.mu.Unlock()
	for _, f := range subs {
		f(n)
	}
	return n
}

func (d *Demo) Do(_ context.Context, op model.Op) (model.Result, error) {
	var res model.Result
	var opErr error
	n := d.update(func(s *model.Snapshot) *model.Snapshot {
		switch op.Kind {
		case model.OpShareCreate, model.OpShareRevoke, model.OpShareRelease, model.OpShareLeave, model.OpMemberRemove:
			// M3's (DESIGN §6.11), decided as its server does.
			n, err := d.m3Op(s, op)
			opErr = err
			return n
		case model.OpRelayAdd, model.OpRelayRemove, model.OpRelayRename, model.OpRelayKick, model.OpRelayUnkick,
			model.OpRelayJoin, model.OpRelayRevokeJoin:
			// M4's (DESIGN §6.12).
			return d.m4Op(s, op, &res)
		case model.OpDeviceDelete, model.OpLogout, model.OpAccountDelete:
			// M7's (DESIGN §6.15).
			n, err := d.m7Op(s, op, &res)
			opErr = err
			return n
		case model.OpLANInvite:
			if op.Person != "" {
				return s.With(op)
			}
			res.Link = "https://deneb.example/i/demo-" + string(op.LAN)
			return s
		case model.OpLANCreate:
			n, id := withNewLAN(s, op.Name)
			res.LAN = id
			return n
		case model.OpInviteAccept:
			res.LAN = demoInviteLAN
			n := s.With(op)
			if n.LAN(demoInviteLAN) != nil {
				return n
			}
			return withInvitedLAN(n)
		case model.OpInviteDecline:
			return s.With(op)
		case model.OpDeviceRenew:
			// Renewed for the demo's 180 days, asked again two weeks before.
			d := s.Devices[op.Device]
			if d == nil || !d.Login.Due(time.Now()) {
				return s
			}
			n := *s
			n.Devices = maps(s.Devices)
			nd := *d
			nd.Login = model.Login{Expires: time.Now().Add(180 * 24 * time.Hour), RenewFrom: time.Now().Add(166 * 24 * time.Hour)}
			n.Devices[op.Device] = &nd
			if nd.Online {
				n.Paths = maps(s.Paths)
				n.Paths[op.Device] = model.Path{Kind: model.PathDirect, RTT: 4 * time.Millisecond, Since: time.Now()}
			}
			return &n
		case model.OpServiceSet:
			// 阿杰's desktop uses this computer's 远程桌面 (scene 5): closing
			// it to 周末联机 cuts him off, a red light on his link.
			n := s.With(op)
			if n == s || op.On || op.Device != s.Self {
				return n
			}
			l := s.LAN(op.LAN)
			for _, use := range demoUses {
				i := slices.IndexFunc(l.Services[s.Self], func(v model.Service) bool { return v.Key == op.Service })
				if use.key == op.Service && i >= 0 && l.Has(use.by) {
					c := *n
					c.Cuts = append(recentCuts(s.Cuts), model.Cut{Device: use.by, Service: l.Services[s.Self][i].Name, At: time.Now()})
					n = &c
				}
			}
			return n
		case model.OpLANAddRelay:
			// The LAN's devices that nothing reaches go through 香港.
			l := s.LAN(op.LAN)
			if l == nil {
				return s
			}
			n := *s
			n.Paths = maps(s.Paths)
			changed := false
			for _, id := range l.Devices {
				if s.Paths[id].Kind == model.PathUnreachable {
					n.Paths[id] = model.Path{Kind: model.PathRelay, Relay: "hk", RTT: 52 * time.Millisecond, Since: time.Now()}
					changed = true
				}
			}
			if !changed {
				return s
			}
			return &n
		}
		return s.With(op)
	})
	if opErr != nil {
		return model.Result{}, opErr
	}
	res.Version = n.Version
	return res, nil
}

func (*Demo) Login(context.Context) (string, error) { return "", nil }

func (*Demo) Open(context.Context, model.DeviceID, model.Service) (string, error) {
	return "", &model.Error{Code: model.ErrInvalid, Message: "演示模式里打不开对方的服务"}
}

func (d *Demo) Preview(_ context.Context, link string) (*model.Invite, error) {
	s := d.Snapshot()
	return &model.Invite{Link: link, LAN: demoInviteLAN, LANName: "桌游夜", Inviter: *s.People["leo"],
		AlreadyIn: s.LAN(demoInviteLAN) != nil}, nil
}

// DemoInvite is the invite link the demo's "收到邀请" opens.
const DemoInvite = "deneb://join/demo"

const demoInviteLAN model.LANID = "boardgame"

// Simulate plays one of the demo's outside events, as the design demos'
// control bar does:
//
//   - "network": Mia's MacBook loses its direct path and goes through the
//     relay, or gets it back (scene 1's 网络变了).
//   - "leave": 阿杰的 NAS leaves 周末联机, as when 阿杰 takes it out on his
//     computer, or comes back.
//   - "connect": 阿杰的台式机 is connected to anew: 正在连接… for 2.4 s,
//     then direct ("connected"), unless the demo is Manual.
//   - "offline": 阿杰的台式机 goes offline, or comes back.
//
// And M2's (DESIGN §6.7–§6.8), each played on the sample as it is:
//
//   - "pending-device": 阿杰 adds his new 阿杰的树莓派 to 周末联机, which waits
//     for me to let it in.
//   - "pending-member": 小林 comes into 周末联机 on Mia's invite, and waits.
//   - "login-due": 我的游戏主机's login runs out in 12 days.
//   - "login-expired": 工作室主机's login ran out.
//   - "others-expired": 阿杰的 NAS's login ran out.
//   - "unreachable": nothing reaches Mia's MacBook, not even through a relay.
//   - "twin": Mia adds her new MacBook, named as her old one.
//   - "stale": my old 我的游戏主机, gone for 40 days, is in 周末联机 too.
//   - "notice": Clash takes Deneb's connections.
//   - "invitation": Leo invites me into 桌游夜 by name.
//   - "using": this computer has used dev-db's database for 2:14:08.
//   - "waiting": I asked into Leo's 读书会 on a member's invite, and wait
//     for Leo.
//   - "engine": this computer's Deneb stops answering, or answers again.
func (d *Demo) Simulate(event string) {
	now := time.Now()
	d.update(func(s *model.Snapshot) *model.Snapshot {
		n := *s
		n.Paths = maps(s.Paths)
		switch event {
		case "network":
			if s.Paths["mia"].Kind == model.PathRelay {
				n.Paths["mia"] = model.Path{Kind: model.PathDirect, RTT: 9 * time.Millisecond, Since: now}
			} else {
				n.Paths["mia"] = model.Path{Kind: model.PathRelay, Relay: "hk", RTT: 46 * time.Millisecond, Since: now}
			}
		case "leave":
			l := s.LAN("weekend")
			if l == nil {
				return s
			}
			op := model.Op{Kind: model.OpLANRemoveDevice, LAN: "weekend", Device: "jnas"}
			if !l.Has("jnas") {
				op.Kind = model.OpLANAddDevice
			}
			return s.With(op)
		case "connect":
			n.Paths["jdesk"] = model.Path{Kind: model.PathConnecting, Since: now}
			if !d.manual {
				time.AfterFunc(2400*time.Millisecond, func() { d.Simulate("connected") })
			}
		case "nonet":
			// This computer loses its network, or gets it back (DESIGN
			// §6.10): the engine finds no device then; back, they connect
			// again, and are there 2 s later ("nonet-back").
			if !s.NoNetwork {
				n.NoNetwork = true
				d.netPaths = maps(s.Paths)
				for id := range s.Paths {
					n.Paths[id] = model.Path{Kind: model.PathUnreachable, Since: now}
				}
				break
			}
			n.NoNetwork = false
			for id := range d.netPaths {
				n.Paths[id] = model.Path{Kind: model.PathConnecting, Since: now}
			}
			if !d.manual {
				time.AfterFunc(2*time.Second, func() { d.Simulate("nonet-back") })
			}
		case "nonet-back":
			if s.NoNetwork || d.netPaths == nil {
				return s
			}
			for id, p := range d.netPaths {
				p.Since = now
				n.Paths[id] = p
			}
			d.netPaths = nil
		case "connected":
			n.Paths["jdesk"] = model.Path{Kind: model.PathDirect, RTT: 11 * time.Millisecond, Since: now}
		case "offline":
			dev := *s.Devices["jdesk"]
			dev.Online = !dev.Online
			n.Devices = maps(s.Devices)
			n.Devices["jdesk"] = &dev
			if dev.Online {
				dev.LastSeen = time.Time{}
				n.Paths["jdesk"] = model.Path{Kind: model.PathDirect, RTT: 11 * time.Millisecond, Since: now}
			} else {
				dev.LastSeen = now.Add(-3 * time.Hour)
				n.Paths["jdesk"] = model.Path{Kind: model.PathOffline, Since: now}
			}
		default:
			if n := m2Event(s, event, now); n != s {
				return n
			}
			if n := d.m3Event(s, event, now); n != s {
				return n
			}
			if n := d.m4Event(s, event, now); n != s {
				return n
			}
			if n := d.m6Event(s, event, now); n != s {
				return n
			}
			return d.m7Event(s, event)
		}
		return &n
	})
}

// m2Event plays one of M2's outside events on s (see Simulate), or returns s.
func m2Event(s *model.Snapshot, event string, now time.Time) *model.Snapshot {
	n := *s
	n.Devices = maps(s.Devices)
	n.Paths = maps(s.Paths)
	n.LANs = slices.Clone(s.LANs)
	if s.LAN("weekend") == nil {
		return s // left in the demo's window: the events play on 周末联机
	}
	weekend := func() *model.LAN {
		i := slices.IndexFunc(n.LANs, func(l *model.LAN) bool { return l.ID == "weekend" })
		l := *n.LANs[i]
		l.Services = maps(l.Services)
		n.LANs[i] = &l
		return &l
	}
	device := func(id model.DeviceID) *model.Device {
		d := *n.Devices[id]
		n.Devices[id] = &d
		return &d
	}
	switch event {
	case "pending-device":
		if _, ok := s.Devices["jpi"]; ok {
			return s
		}
		n.Devices["jpi"] = &model.Device{ID: "jpi", Owner: "jie", Name: "阿杰的树莓派", Kind: "mini", OS: "linux",
			Addr: "10.67.0.20", FQDN: "pi.jie.deneb.internal", Online: true, Refused: map[model.PersonID]bool{}}
		n.Paths["jpi"] = model.Path{Kind: model.PathDirect, RTT: 12 * time.Millisecond}
		l := weekend()
		l.ApproveDevices = true
		l.Pending = append(slices.Clone(l.Pending), "jpi")
		l.Services["jpi"] = []model.Service{{Key: "tcp/22", Kind: "shell", Name: "命令行", Proto: "tcp", Port: 22, Ports: "22", On: true}}
	case "pending-member":
		l := weekend()
		if slices.Contains(l.Members, "lin") || slices.Contains(l.PendingMembers, "lin") {
			return s
		}
		l.ApproveMembers = true
		l.PendingMembers = append(slices.Clone(l.PendingMembers), "lin")
	case "login-due":
		device("game").Login = model.Login{Expires: now.Add(12 * 24 * time.Hour), RenewFrom: now.Add(-2 * 24 * time.Hour)}
	case "login-expired":
		d := device("studio")
		d.Login, d.Online = model.Login{Expires: now.Add(-24 * time.Hour), RenewFrom: now.Add(-15 * 24 * time.Hour), Expired: true}, false
		n.Paths["studio"] = model.Path{Kind: model.PathOffline, Since: now}
	case "others-expired":
		d := device("jnas")
		d.Login, d.Online = model.Login{Expired: true}, false
		n.Paths["jnas"] = model.Path{Kind: model.PathOffline, Since: now}
	case "unreachable":
		n.Paths["mia"] = model.Path{Kind: model.PathUnreachable, Since: now}
	case "twin":
		if _, ok := s.Devices["mia2"]; ok {
			return s
		}
		twin := *s.Devices["mia"]
		twin.ID, twin.Addr, twin.FQDN = "mia2", "10.67.0.21", "macbook-2.mia.deneb.internal"
		n.Devices["mia2"] = &twin
		n.Paths["mia2"] = model.Path{Kind: model.PathDirect, RTT: 10 * time.Millisecond}
		l := weekend()
		l.Devices = append(slices.Clone(l.Devices), "mia2")
		l.Services["mia2"] = []model.Service{{Key: "tcp/3389", Kind: "screen", Name: "远程桌面", Proto: "tcp", Port: 3389, Ports: "3389", On: true}}
	case "stale":
		if _, ok := s.Devices["game0"]; ok {
			return s
		}
		old := *s.Devices["game"]
		old.ID, old.Online, old.LastSeen = "game0", false, now.Add(-40*24*time.Hour)
		old.Addr, old.FQDN = "10.67.0.22", "console-2.kittors.deneb.internal"
		n.Devices["game0"] = &old
		n.Paths["game0"] = model.Path{Kind: model.PathOffline}
		n.Mine = append(slices.Clone(s.Mine), "game0")
		l := weekend()
		l.Devices = append(slices.Clone(l.Devices), "game0")
		l.Services["game0"] = slices.Clone(old.Catalog)
	case "local-network":
		// macOS keeps the engine off the devices behind this router, and
		// lets it back (DESIGN §6.13): 我的游戏主机, at home, goes the long
		// way round meanwhile.
		if i := slices.IndexFunc(s.Notices, func(x model.Notice) bool { return x.Code == model.LocalNetwork.Code }); i >= 0 {
			n.Notices = slices.Delete(slices.Clone(s.Notices), i, i+1)
			n.Paths["game"] = model.Path{Kind: model.PathDirect, RTT: 3 * time.Millisecond, Since: now}
			break
		}
		n.Notices = append(slices.Clone(s.Notices), model.LocalNetwork)
		n.Paths["game"] = model.Path{Kind: model.PathRelay, Relay: "hk", RTT: 61 * time.Millisecond, Since: now}
	case "notice":
		if len(s.Notices) > 0 {
			return s
		}
		n.Notices = []model.Notice{{Code: "tun-capture", Short: "Clash 可能挡住连接", Message: "Clash 正在接管网络，局域网里的设备可能连不上",
			Rule: "IP-CIDR,10.67.0.0/16,DIRECT"}}
	case "invitation":
		if len(s.Invitations) > 0 || s.LAN(demoInviteLAN) != nil {
			return s
		}
		n.Invitations = []model.Invitation{{ID: "inv-boardgame", LAN: demoInviteLAN, LANName: "桌游夜",
			Inviter: *s.People["leo"], Expires: now.Add(7 * 24 * time.Hour)}}
	case "waiting":
		if s.LAN("books") != nil {
			return s
		}
		n.LANs = append(n.LANs, &model.LAN{ID: "books", Name: "读书会", Owner: "leo", Members: []model.PersonID{"leo"},
			Waiting: true, Services: map[model.DeviceID][]model.Service{}})
	case "using":
		n.Using = maps(s.Using)
		if _, ok := n.Using["lindb"]; ok {
			return s
		}
		n.Using["lindb"] = now.Add(-(2*time.Hour + 14*time.Minute + 8*time.Second))
	case "engine":
		// This computer's Deneb stops answering, or answers again.
		n.Engine = !s.Engine
	default:
		return s
	}
	return &n
}

func maps[K comparable, V any](m map[K]V) map[K]V {
	n := make(map[K]V, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

// demoSnapshot is the world of the design's sample: the prototype's values,
// as its newApp wrote them.
func demoSnapshot() *model.Snapshot {
	svc := func(key, kind, name string, on bool) model.Service {
		proto, ports, _ := strings.Cut(key, "/")
		port, _ := strconv.Atoi(ports)
		return model.Service{Key: key, Kind: kind, Name: name, Proto: proto, Port: uint16(port), Ports: ports, On: on}
	}
	screen := func(on bool) model.Service { return svc("tcp/3389", "screen", "远程桌面", on) }
	files := func(on bool) model.Service { return svc("tcp/445", "files", "共享文件夹", on) }
	web := func(on bool) model.Service { return svc("tcp/8080", "web", "网页", on) }
	shell := func(on bool) model.Service { return svc("tcp/22", "shell", "命令行", on) }
	game := func(on bool) model.Service { return svc("udp/27015", "game", "游戏服务器", on) }

	people := map[model.PersonID]*model.Person{
		"me":  {ID: "me", Name: "kittors", Initial: "K", Handle: "kittors", Color: 0},
		"mia": {ID: "mia", Name: "Mia", Initial: "M", Handle: "mia", Color: 1},
		"jie": {ID: "jie", Name: "阿杰", Initial: "杰", Handle: "jie", Color: 2},
		"leo": {ID: "leo", Name: "Leo", Initial: "L", Handle: "leo", Color: 3},
		"lin": {ID: "lin", Name: "小林", Initial: "林", Handle: "lin", Color: 4},
	}
	dev := func(id, owner, name, kind, os string, n int, fqdn string, catalog ...model.Service) *model.Device {
		return &model.Device{ID: model.DeviceID(id), Owner: model.PersonID(owner), Name: name, Kind: kind, OS: os,
			Addr: "10.67.0." + strconv.Itoa(n), FQDN: fqdn + ".deneb.internal", Online: true,
			Catalog: catalog, Refused: map[model.PersonID]bool{}}
	}
	devices := map[model.DeviceID]*model.Device{
		"mac":    dev("mac", "me", "kittors 的 MacBook", "laptop", "macos", 10, "macbook.kittors", screen(true), files(false)),
		"mia":    dev("mia", "mia", "Mia 的 MacBook", "laptop", "macos", 11, "macbook.mia"),
		"jdesk":  dev("jdesk", "jie", "阿杰的台式机", "desktop", "windows", 12, "desktop.jie"),
		"game":   dev("game", "me", "我的游戏主机", "console", "other", 13, "console.kittors", game(true)),
		"jnas":   dev("jnas", "jie", "阿杰的 NAS", "nas", "linux", 14, "nas.jie"),
		"studio": dev("studio", "me", "工作室主机", "mini", "macos", 15, "studio.kittors", screen(true), shell(true)),
		"mynas":  dev("mynas", "me", "我的 NAS", "nas", "linux", 16, "nas.kittors", files(true)),
		"lindb":  dev("lindb", "lin", "dev-db", "server", "linux", 17, "devdb.lin"),
		"linci":  dev("linci", "lin", "CI 构建机", "server", "linux", 18, "ci.lin"),
		"leopc":  dev("leopc", "leo", "Leo 的台式机", "desktop", "windows", 19, "desktop.leo"),
	}
	weekend := &model.LAN{
		ID: "weekend", Name: "周末联机", Owner: "me",
		Members: []model.PersonID{"me", "mia", "jie", "leo"},
		Devices: []model.DeviceID{"mac", "mia", "jdesk", "game", "jnas"},
		Services: map[model.DeviceID][]model.Service{
			"mac":   {screen(true), files(false)},
			"mia":   {screen(true), files(false)},
			"jdesk": {screen(true), web(false)},
			"game":  {game(true)},
			"jnas":  {files(true), screen(false), web(true)},
		},
		Relays: []model.RelayID{"hk"},
		// Made in M1: as the server moved them, new members wait, new
		// devices do not (DESIGN §6.7).
		ApproveMembers: true,
	}
	work := &model.LAN{
		ID: "work", Name: "工作室", Owner: "me",
		Members: []model.PersonID{"me", "lin"},
		Devices: []model.DeviceID{"mac", "studio", "lindb", "linci"},
		Services: map[model.DeviceID][]model.Service{
			"mac":    {screen(true), files(false)},
			"studio": {screen(true), shell(true)},
			"lindb":  {shell(true), web(true)},
			"linci":  {web(true)},
		},
		Relays:         []model.RelayID{"hk"},
		ApproveMembers: true,
	}
	direct := func(ms int) model.Path {
		return model.Path{Kind: model.PathDirect, RTT: time.Duration(ms) * time.Millisecond}
	}
	return &model.Snapshot{
		Version: 1,
		Engine:  true,
		// This computer has its network adapter: the window's notice of one
		// not in yet (Windows, DESIGN §7.3) is the event "user-mode".
		Kernel:   true,
		Loaded:   true,
		LoggedIn: true,
		Me:       "me",
		Self:     "mac",
		Control:  true,
		People:   people,
		LANs:     []*model.LAN{weekend, work},
		Devices:  devices,
		Mine:     []model.DeviceID{"mac", "game", "studio", "mynas"},
		Relays:   map[model.RelayID]*model.Relay{"hk": {ID: "hk", Name: "香港"}},
		Paths: map[model.DeviceID]model.Path{
			"mia": direct(9), "jdesk": direct(11), "game": direct(3),
			"jnas":   {Kind: model.PathRelay, Relay: "hk", RTT: 48 * time.Millisecond},
			"studio": direct(14), "mynas": direct(4), "lindb": direct(16), "linci": direct(18),
			"leopc": direct(21),
		},
		// The demo's server does all that M2 adds, and M4's relay in a
		// click, to show what they look like.
		Features: map[string]bool{model.FeatureApprove: true, model.FeatureInvitePerson: true,
			model.FeatureRenew: true, model.FeatureAddRelay: true},
	}
}

// withNewLAN adds a LAN named name, made by me: this computer is in it, as
// in every LAN made on it.
func withNewLAN(s *model.Snapshot, name string) (*model.Snapshot, model.LANID) {
	id := model.LANID("lan" + strconv.Itoa(len(s.LANs)+1))
	for s.LAN(id) != nil {
		id += "x"
	}
	n := *s
	l := &model.LAN{ID: id, Name: name, Owner: s.Me, Members: []model.PersonID{s.Me},
		Devices: []model.DeviceID{s.Self}, Services: map[model.DeviceID][]model.Service{},
		// A new LAN asks me about new devices and members (DESIGN §6.7).
		ApproveDevices: true, ApproveMembers: true}
	if me := s.Devices[s.Self]; me != nil {
		l.Services[s.Self] = slices.Clone(me.Catalog)
	}
	n.LANs = append(slices.Clone(s.LANs), l)
	return &n, id
}

// withInvitedLAN adds the LAN of the demo's invite, 桌游夜: Leo's, with his
// computer in it, and this one joining.
func withInvitedLAN(s *model.Snapshot) *model.Snapshot {
	n := *s
	l := &model.LAN{ID: demoInviteLAN, Name: "桌游夜", Owner: "leo",
		Members: []model.PersonID{"leo", s.Me},
		Devices: []model.DeviceID{"leopc", s.Self},
		Services: map[model.DeviceID][]model.Service{
			"leopc": {{Key: "udp/27015", Kind: "game", Name: "游戏服务器", Proto: "udp", Port: 27015, Ports: "27015", On: true}},
		},
		Relays: []model.RelayID{"hk"},
	}
	if me := s.Devices[s.Self]; me != nil {
		l.Services[s.Self] = slices.Clone(me.Catalog)
	}
	n.LANs = append(slices.Clone(s.LANs), l)
	return &n
}
