package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// Live is the store of a computer that runs Deneb: its engine's, over the
// local interface (implementation.md §2.6.2). It watches the engine and makes
// a snapshot of every event: the user view the engine forwards from the
// control server, the engine's own state beside it, and the paths to each
// device debounced. While the engine does not answer, the snapshot says so
// and Live tries again every second.
type Live struct {
	lc  *localclient.Client
	now func() time.Time

	mu      sync.Mutex
	state   *apitype.State
	view    *proto.UserView
	local   []apitype.LocalService // what this computer listens on, as the engine last found
	down    bool                   // the engine stopped answering
	cuts    []cutUse               // the uses this computer cut, the last few seconds'
	deb     debouncer
	snap    *model.Snapshot
	subs    map[int]func(*model.Snapshot)
	nextSub int
	timer   *time.Timer
	cancel  context.CancelFunc
	// taken are the shares this store took back, by device and person:
	// share.release names one the view no longer lists (M3).
	taken map[string]proto.ShareID
	// usage is my relays' usage as the server last said it; joins the
	// install commands this store asked for, by join ID, which the server
	// shows only once; usageNow asks for the usage again (M4).
	usage    *proto.RelayUsage
	joins    map[string]string
	usageNow chan struct{}
	// proxy is which proxy program runs here, for the notice of one taking
	// the connections (M6).
	proxy runningProxy
}

// NewLive returns the store of the engine lc talks to. It watches nothing
// until Start.
func NewLive(lc *localclient.Client) *Live {
	l := &Live{lc: lc, now: time.Now, subs: map[int]func(*model.Snapshot){}, usageNow: make(chan struct{}, 1)}
	l.snap = l.buildLocked(l.now())
	return l
}

// Start watches the engine until Close.
func (l *Live) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.cancel = cancel
	l.mu.Unlock()
	go l.watch(ctx)
	go l.listen(ctx)
	go l.relayUsage(ctx)
}

// listen asks the engine which common services this computer listens on,
// now and every 30 seconds, for its card to offer them.
func (l *Live) listen(ctx context.Context) {
	for {
		if l.Snapshot().LoggedIn {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			found, err := l.lc.Services(c)
			cancel()
			if err == nil {
				l.update(func(time.Time) { l.local = found })
			}
		}
		wait := 30 * time.Second
		if len(l.local) == 0 {
			wait = 3 * time.Second // not logged in yet, or the engine was not there
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Close stops watching.
func (l *Live) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
	if l.timer != nil {
		l.timer.Stop()
	}
}

// liveFeatures are the server's features this store takes the data and
// the ops of: a control shows only where both the server and this store
// take it.
var liveFeatures = map[string]bool{
	proto.FeatureApprove: true, proto.FeatureInvitePerson: true, proto.FeatureRename: true,
	proto.FeatureRenew: true, proto.FeatureDNSName: true,
	// M3's (DESIGN §6.11).
	proto.FeatureShare: true, proto.FeatureSeats: true, proto.FeatureShareMode: true, proto.FeaturePublicDevice: true,
	// M4's (DESIGN §6.12).
	proto.FeatureRelays: true,
}

func (l *Live) watch(ctx context.Context) {
	for {
		// The engine's version only goes up while it runs, but two of its
		// goroutines can each take a State and hand them over in the other
		// order: an older State after a newer one would undo, here, an op
		// that went through, until the engine's next change. A new watch,
		// as after the engine restarted, starts afresh.
		var newest uint64
		err := l.lc.Watch(ctx, func(ev apitype.Event) error {
			switch ev.Type {
			case apitype.EventState:
				if ev.State != nil && ev.State.Version >= newest {
					newest = ev.State.Version
					l.takeState(ev.State)
				}
			case apitype.EventPaths:
				l.takePaths(ev.Paths)
			case apitype.EventUsage:
				l.takeUsage(ev.Usage, ev.Using)
			case apitype.EventCut:
				l.takeCut(ev.Cut)
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		_ = err // not running, or gone: the same to the view
		l.update(func(time.Time) { l.down = true })
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// takeState takes in a whole new state from the engine.
func (l *Live) takeState(st *apitype.State) {
	var view *proto.UserView
	if len(st.UserView) > 0 && string(st.UserView) != "null" {
		v := new(proto.UserView)
		if json.Unmarshal(st.UserView, v) == nil {
			view = v
		}
	}
	l.update(func(now time.Time) {
		if view == nil && len(st.UserView) > 0 {
			// A view that does not read: the last one stays, rather than an
			// empty world for a moment.
			view = l.view
		}
		l.state, l.view, l.down = st, view, false
		paths := make(map[model.DeviceID]model.Path, len(st.Paths))
		for _, p := range st.Paths {
			paths[model.DeviceID(p.DeviceID)] = toPath(p)
		}
		l.deb.replace(paths, now)
	})
}

// takePaths takes in the paths that changed.
func (l *Live) takePaths(paths []apitype.PeerPath) {
	l.update(func(now time.Time) {
		for _, p := range paths {
			l.deb.report(model.DeviceID(p.DeviceID), toPath(p), now)
		}
	})
}

// takeUsage takes in who uses what now, both ways: the engine says it apart
// from the state, as it changes.
func (l *Live) takeUsage(usage, using []apitype.ServiceUse) {
	l.update(func(time.Time) {
		if l.state == nil {
			return
		}
		st := *l.state
		st.Usage, st.Using = usage, using
		l.state = &st
	})
}

// cutUse is a use this computer cut, as the engine told of it, and when.
type cutUse struct {
	use apitype.ServiceUse
	at  time.Time
}

// cutKeep is how long a cut stays in the snapshots: the red light and the
// sentence that go with it are over by then (scene 5).
const cutKeep = 6 * time.Second

// takeCut takes in the uses this computer just cut, closing a service or
// refusing someone.
func (l *Live) takeCut(cut []apitype.ServiceUse) {
	l.update(func(now time.Time) {
		l.cuts = slices.DeleteFunc(l.cuts, func(c cutUse) bool { return now.Sub(c.at) > cutKeep })
		for _, u := range cut {
			l.cuts = append(l.cuts, cutUse{use: u, at: now})
		}
	})
}

// update changes what Live knows with fn, makes the snapshot anew and tells
// the subscribers.
func (l *Live) update(fn func(now time.Time)) {
	now := l.now()
	l.mu.Lock()
	fn(now)
	s := l.buildLocked(now)
	l.snap = s
	subs := make([]func(*model.Snapshot), 0, len(l.subs))
	for _, f := range l.subs {
		subs = append(subs, f)
	}
	l.mu.Unlock()
	for _, f := range subs {
		f(s)
	}
}

func (l *Live) Snapshot() *model.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snap
}

func (l *Live) Subscribe(fn func(*model.Snapshot)) (cancel func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextSub
	l.nextSub++
	l.subs[id] = fn
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.subs, id)
	}
}

// buildLocked makes the snapshot of what Live knows as of now, and sets
// the timer for when a debounced path may change without a new report.
func (l *Live) buildLocked(now time.Time) *model.Snapshot {
	s := &model.Snapshot{
		People:  map[model.PersonID]*model.Person{},
		Devices: map[model.DeviceID]*model.Device{},
		Relays:  map[model.RelayID]*model.Relay{},
		Paths:   map[model.DeviceID]model.Path{},
	}
	st := l.state
	if st == nil {
		return s
	}
	s.Engine = !l.down
	s.Version = st.Version
	s.LoggedIn = st.LoggedIn
	s.LoginURL = st.LoginURL
	s.Me = model.PersonID(st.User)
	s.Self = model.DeviceID(st.SelfDeviceID)
	s.Control = st.ControlConnected
	for _, r := range st.Relays {
		s.Relays[model.RelayID(r.ID)] = &model.Relay{ID: model.RelayID(r.ID), Name: r.Name}
	}
	// The LANs this computer paused on itself: the engine's, not the
	// server's (DESIGN §9 scene 3).
	paused := map[apitype.ScopeID]bool{}
	for _, sc := range st.Scopes {
		paused[sc.ID] = sc.Paused
	}
	if v := l.view; v != nil && st.LoggedIn {
		s.Loaded = true
		if len(v.Features) > 0 {
			// What the server does beyond M1 (M2 on), as far as this store
			// takes it: a control shows only where its data and ops are
			// wired here too.
			s.Features = make(map[string]bool, len(v.Features))
			for _, f := range v.Features {
				if liveFeatures[f] {
					s.Features[f] = true
				}
			}
			// The engine's, not the server's: this computer's diagnostics
			// package (M6, DESIGN §6.14); an engine before it says so when
			// asked. Not with an M1 server, whose window shows M1's.
			s.Features[model.FeatureDiagnostics] = true
		}
		if v.Me != "" {
			s.Me = model.PersonID(v.Me)
		}
		for _, p := range v.People {
			s.People[model.PersonID(p.ID)] = person(p, s.Me)
		}
		for _, d := range v.Devices {
			md := deviceFrom(d)
			s.Devices[md.ID] = md
			if md.Owner == s.Me {
				s.Mine = append(s.Mine, md.ID)
			}
		}
		// M3 (DESIGN §6.11): the devices given to me, and my seats.
		takeShared(s, v)
		s.Seats = seatsOf(v.Seats)
		for _, lv := range v.LANs {
			ml := &model.LAN{ID: model.LANID(lv.ID), Name: lv.Name, Owner: model.PersonID(lv.Owner),
				Services: map[model.DeviceID][]model.Service{}, Mode: lanMode(lv.Mode),
				// M2: its two switches, and whether I wait to be let in.
				ApproveDevices: lv.ApproveDevices, ApproveMembers: lv.ApproveMembers, Waiting: lv.Pending,
				Paused: paused[apitype.ScopeID(proto.LANScope(lv.ID))]}
			for _, m := range lv.Members {
				// Only those in are members: someone invited is not in yet,
				// and one who waits for me to let them in shows apart (M2).
				switch m.State {
				case proto.MemberJoined, "":
					ml.Members = append(ml.Members, model.PersonID(m.Person))
				case proto.MemberPending:
					ml.PendingMembers = append(ml.PendingMembers, model.PersonID(m.Person))
				}
			}
			for _, iv := range lv.Invited {
				ml.Invited = append(ml.Invited, model.Invited{Person: model.PersonID(iv.Person), By: model.PersonID(iv.InvitedBy),
					Invite: string(iv.Invite), Expires: iv.ExpiresAt})
			}
			for _, d := range lv.Devices {
				id := model.DeviceID(d.Device)
				if d.Pending {
					// Waits for the owner: a dashed tile, not in the LAN yet.
					ml.Pending = append(ml.Pending, id)
				} else {
					ml.Devices = append(ml.Devices, id)
				}
				svcs := make([]model.Service, 0, len(d.Services))
				for _, ss := range d.Services {
					svcs = append(svcs, service(ss.Service, ss.On))
				}
				ml.Services[id] = svcs
			}
			for _, r := range lv.Relays {
				ml.Relays = append(ml.Relays, model.RelayID(r))
			}
			// The ones people dragged in, as an M4 server says.
			for _, r := range lv.AddedRelays {
				if s.Can(model.FeatureRelays) {
					ml.AddedRelays = append(ml.AddedRelays, model.RelayID(r))
				}
			}
			if ml.Mode == model.ModeShare && ml.Owner != s.Me {
				// A member of a LAN that only hands out devices has of it
				// what it gives them, under "共享给我" (DESIGN §6.11).
				s.ShareLANs = append(s.ShareLANs, ml)
				continue
			}
			s.LANs = append(s.LANs, ml)
		}
		// M4 (DESIGN §6.12): the relays, after the LANs, which say which
		// of theirs someone dragged in.
		takeRelays(s, v, l.usage, l.joins)
		for _, iv := range v.Invitations {
			// Invites made out to me that wait for my answer (M2).
			if iv.State != "" && iv.State != proto.InvitePending {
				continue
			}
			s.Invitations = append(s.Invitations, model.Invitation{ID: string(iv.Invite), LAN: model.LANID(iv.LAN),
				LANName: iv.LANName, Inviter: *person(iv.Inviter, s.Me), Expires: iv.ExpiresAt, SeatsFull: iv.SeatsFull})
		}
	}
	for _, ls := range l.local {
		s.Local = append(s.Local, service(localService(ls), false))
	}
	// What this computer uses now, and since when: the "已连接" count, from
	// the oldest of its connections to each device (DESIGN §6.7).
	for _, u := range st.Using {
		if s.Using == nil {
			s.Using = map[model.DeviceID]time.Time{}
		}
		id := model.DeviceID(u.DeviceID)
		if t, ok := s.Using[id]; !ok || u.Since.Before(t) {
			s.Using[id] = u.Since
		}
	}
	// Another program's tunnel takes Deneb's connections: the amber pill
	// (DESIGN §6.8). The engine cannot tell which program; Clash is the one
	// people have.
	for _, p := range st.Engine.Problems {
		if p.Code == apitype.ProblemProxy {
			// Which program it is, the store looks for among the running
			// ones (M6, DESIGN §6.14); the engine does not say.
			if app, ok := l.proxy.known(now, func() { l.update(func(time.Time) {}) }); ok {
				s.Notices = append(s.Notices, proxyNotice(p.Code, p.Rule, app))
			}
		}
		if p.Code == model.LocalNetwork.Code {
			// macOS keeps the engine off the devices behind this router
			// (DESIGN §6.13).
			s.Notices = append(s.Notices, model.LocalNetwork)
		}
	}
	s.Kernel = st.Mode == apitype.ModeKernel
	paths, next := l.deb.at(now)
	s.Paths = paths
	// M4: how long a round trip through each relay takes, as the devices
	// that go through it now say, for its card: "离你很快" or not, and
	// its "延迟". One no device goes through says nothing of it.
	for _, p := range s.Paths {
		if r := s.Relays[p.Relay]; p.Kind == model.PathRelay && r != nil && p.RTT > 0 && (r.RTT == 0 || p.RTT < r.RTT) {
			r.RTT = p.RTT
		}
	}
	s.NoNetwork = noNetwork(st, s)
	// The uses just cut, each service named as the card names it.
	for _, c := range l.cuts {
		if until := c.at.Add(cutKeep); now.Before(until) {
			s.Cuts = append(s.Cuts, model.Cut{Device: model.DeviceID(c.use.DeviceID), Service: cutService(s, c.use), At: c.at})
			if next.IsZero() || until.Before(next) {
				next = until
			}
		}
	}
	l.wakeAtLocked(next)
	return s
}

// problemNoNetwork is the engine's problem for a computer without a
// network of its own (DESIGN §6.10).
const problemNoNetwork = "no-network"

// noNetwork reports whether this computer has no network: as its engine
// says it outright; or, from an engine that does not, as the control server
// and every device it shares a LAN with go out of reach at once, which no
// trouble over there does. A device that connects says there is a network;
// one being connected to, as all are as the engine starts, says nothing,
// and neither does one that is just off.
func noNetwork(st *apitype.State, s *model.Snapshot) bool {
	for _, p := range st.Engine.Problems {
		if p.Code == problemNoNetwork {
			return true
		}
	}
	if st.ControlConnected || !st.LoggedIn {
		return false
	}
	out := 0
	for id, p := range s.Paths {
		switch {
		case id == s.Self:
		case p.Kind.Connected():
			return false
		case p.Kind == model.PathUnreachable:
			out++
		}
	}
	return out > 0
}

// cutService is the name of this computer's service a cut use was of: as
// the card of a LAN it was opened to lists it, else as the engine names it.
func cutService(s *model.Snapshot, u apitype.ServiceUse) string {
	for _, sc := range u.Scopes {
		lan, ok := proto.ScopeID(sc).LAN()
		if !ok {
			continue
		}
		l := s.LAN(model.LANID(lan))
		if l == nil {
			continue
		}
		for _, v := range l.Services[s.Self] {
			if v.Proto == u.Proto && slices.Contains(portsOf(v), u.Port) {
				return v.Name
			}
		}
	}
	if u.Name != "" {
		return u.Name
	}
	return "这项服务"
}

// portsOf are the ports of a service, as its key lists them ("tcp/80",
// "udp/27015-27030").
func portsOf(v model.Service) []uint16 {
	ports := []uint16{v.Port}
	_, spec, _ := strings.Cut(v.Key, "/")
	for _, part := range strings.Split(spec, ",") {
		lo, hi, ok := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if ok {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		for p := a; p <= b && p-a < 1024; p++ {
			ports = append(ports, uint16(p))
		}
	}
	return ports
}

// wakeAtLocked makes the snapshot anew at next, when a debounced path is due
// to change though nothing new was reported.
func (l *Live) wakeAtLocked(next time.Time) {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if next.IsZero() {
		return
	}
	l.timer = time.AfterFunc(max(next.Sub(l.now()), 0)+time.Millisecond, func() { l.update(func(time.Time) {}) })
}

// person is someone as the view knows them: their color by the client's
// rule, their initial taken from their name when the server gives none.
func person(p proto.Person, me model.PersonID) *model.Person {
	mp := &model.Person{ID: model.PersonID(p.ID), Name: p.DisplayName, Initial: p.Initial, Handle: p.Name,
		Color: model.ColorFor(me, model.PersonID(p.ID))}
	if mp.Name == "" {
		mp.Name = p.Name
	}
	if mp.Initial == "" {
		if r, _ := utf8.DecodeRuneInString(mp.Name); r != utf8.RuneError {
			mp.Initial = string(unicode.ToUpper(r))
		}
	}
	return mp
}

// service is a service as the view shows it: its kind's name when it has
// none of its own, and its ports as "更多" lists them.
func service(s proto.Service, on bool) model.Service {
	ms := model.Service{Key: string(s.Key()), Kind: string(s.Kind), Name: s.DisplayName(), On: on}
	var ports []string
	for i, p := range s.Ports {
		if i == 0 {
			ms.Proto, ms.Port = string(p.Proto), p.First
		}
		if p.First == p.Last {
			ports = append(ports, strconv.Itoa(int(p.First)))
		} else {
			ports = append(ports, strconv.Itoa(int(p.First))+"–"+strconv.Itoa(int(p.Last)))
		}
	}
	ms.Ports = strings.Join(ports, ", ")
	return ms
}

func toPath(p apitype.PeerPath) model.Path {
	return model.Path{Kind: model.PathKind(p.Kind), Relay: model.RelayID(p.Relay),
		RTT: time.Duration(p.RTTMs) * time.Millisecond, Loss: p.Loss, Since: p.Since}
}

// Do sends op to the engine, which passes it to the control server. An op
// whose answer was lost to an engine starting over is sent again with the
// same key, so that it takes effect once.
func (l *Live) Do(ctx context.Context, op model.Op) (model.Result, error) {
	if op.Kind == model.OpLANPause {
		return l.pause(ctx, op.LAN, op.On)
	}
	args := proto.OpArgs{
		LAN: proto.LANID(op.LAN), Device: proto.DeviceID(op.Device), Person: proto.PersonID(op.Person),
		Name: op.Name, Invite: op.Invite, On: op.On, Refuse: op.Refuse,
		Kind: proto.DeviceKind(op.DevKind), Invitation: proto.InviteID(op.Invitation),
		Share: proto.ShareID(op.Share), Mode: proto.LANMode(op.Mode),
	}
	if err := l.shareArgs(op, &args); err != nil {
		return model.Result{}, err
	}
	if err := l.relayArgs(op, &args); err != nil {
		return model.Result{}, err
	}
	if op.Kind == model.OpServiceSet {
		svc := l.service(op.Device, op.LAN, op.Service)
		if svc == nil {
			return model.Result{}, &model.Error{Code: model.ErrInvalid, Message: "找不到这项服务"}
		}
		args.Service = svc
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return model.Result{}, err
	}
	req := apitype.OpRequest{ID: op.ID, Op: string(op.Kind), Args: raw}
	if req.ID == "" {
		req.ID = localclient.NewOpID()
	}
	var res *apitype.OpResult
	for try := 0; ; try++ {
		res, err = l.lc.Do(ctx, req)
		if err == nil || !errors.Is(err, localclient.ErrNotRunning) || try == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return model.Result{}, modelError(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err != nil {
		return model.Result{}, modelError(err)
	}
	r := model.Result{Version: res.Version}
	if len(res.Result) > 0 {
		var out struct {
			proto.OpOutput
			Link string `json:"link"`
		}
		if json.Unmarshal(res.Result, &out) == nil {
			r.LAN = model.LANID(out.LAN)
			if out.Invite != nil {
				r.Link = out.Invite.URL
			}
			if r.Link == "" {
				r.Link = out.Link
			}
			r.Share = string(out.Share)
			l.relaysDone(op, out.OpOutput, &r)
		}
	}
	l.sharesDone(op, args, r)
	return r, nil
}

// pause pauses a LAN on this computer, or resumes it: the engine does it on
// its own, at once, as `deneb lan pause` does, and tells the server after.
// The snapshot says so from now on, rather than wait for the engine's next
// state, so that the switch does not flip back in between.
func (l *Live) pause(ctx context.Context, lan model.LANID, on bool) (model.Result, error) {
	scope := apitype.ScopeID(proto.LANScope(proto.LANID(lan)))
	if err := l.lc.Pause(ctx, scope, on); err != nil {
		if _, ok := localclient.AsError(err); ok || errors.Is(err, localclient.ErrNotRunning) || errors.Is(err, context.DeadlineExceeded) {
			return model.Result{}, modelError(err)
		}
		// An engine older than the switch answers without an error of its
		// own: in plain words, never the HTTP status.
		return model.Result{}, &model.Error{Code: model.ErrInvalid, Message: "这台电脑上的 Deneb 还不能暂停，更新以后再试"}
	}
	l.update(func(time.Time) {
		st := l.state
		if st == nil {
			return
		}
		n := *st
		n.Scopes = slices.Clone(st.Scopes)
		for i := range n.Scopes {
			if n.Scopes[i].ID == scope {
				n.Scopes[i].Paused = on
			}
		}
		l.state = &n
	})
	return model.Result{}, nil
}

// service finds the service of dev with key, as service.set names it to the
// server: its kind, name and ports.
func (l *Live) service(dev model.DeviceID, lan model.LANID, key string) *proto.Service {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.view == nil {
		return nil
	}
	for _, lv := range l.view.LANs {
		if lv.ID != proto.LANID(lan) {
			continue
		}
		for _, d := range lv.Devices {
			if d.Device != proto.DeviceID(dev) {
				continue
			}
			for _, s := range d.Services {
				if string(s.Key()) == key {
					svc := s.Service
					return &svc
				}
			}
		}
	}
	for _, d := range l.view.Devices {
		if d.ID != proto.DeviceID(dev) {
			continue
		}
		for _, s := range d.Services {
			if string(s.Key()) == key {
				svc := s
				return &svc
			}
		}
	}
	if l.state != nil && dev == model.DeviceID(l.state.SelfDeviceID) {
		for _, ls := range l.local {
			if svc := localService(ls); string(svc.Key()) == key {
				return &svc
			}
		}
	}
	return nil
}

// localService is a service the engine found listening here, as the
// control server takes it the first time it is opened.
func localService(ls apitype.LocalService) proto.Service {
	p := proto.TCP
	if ls.Proto == "udp" {
		p = proto.UDP
	}
	return proto.Service{Kind: proto.ServiceKind(ls.Kind), Ports: []proto.PortRange{proto.Port(p, ls.Port)}}
}

func (l *Live) Login(ctx context.Context) (string, error) {
	u, err := l.lc.Login(ctx, "")
	if err != nil {
		return "", modelError(err)
	}
	return u, nil
}

func (l *Live) Open(ctx context.Context, dev model.DeviceID, svc model.Service) (string, error) {
	res, err := l.lc.Open(ctx, apitype.OpenRequest{Device: apitype.DeviceID(dev), Kind: svc.Kind, Proto: svc.Proto, Port: svc.Port})
	if err != nil {
		return "", modelError(err)
	}
	if res.Addr != "" {
		return res.Addr, nil
	}
	return net.JoinHostPort(res.Host, strconv.Itoa(int(res.Port))), nil
}

func (l *Live) Preview(ctx context.Context, link string) (*model.Invite, error) {
	raw, err := l.lc.PreviewInvite(ctx, link)
	if err != nil {
		return nil, modelError(err)
	}
	var p proto.InvitePreview
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &model.Error{Code: model.ErrInternal, Message: "看不懂这条邀请"}
	}
	me := l.Snapshot().Me
	return &model.Invite{
		Link: link, LAN: model.LANID(p.LAN), LANName: p.LANName, Inviter: *person(p.Inviter, me),
		AlreadyIn: p.AlreadyIn, Gone: p.State != "" && p.State != proto.InvitePending, SeatsFull: p.SeatsFull,
	}, nil
}

// modelError is an error of the engine's as the view takes it: a kind, and
// one sentence for the toast, without the full stop, as the toast's words
// have none.
func modelError(err error) error {
	switch {
	case errors.Is(err, localclient.ErrNotRunning):
		return &model.Error{Code: model.ErrNotRunning, Message: "Deneb 没在运行，等它启动好再试"}
	case errors.Is(err, context.DeadlineExceeded):
		return &model.Error{Code: model.ErrTimeout, Message: "等了太久，没办成"}
	}
	if e, ok := localclient.AsError(err); ok {
		if strings.HasPrefix(e.Message, "不认识的操作") {
			// A server older than this client: in plain words, never the
			// op's name (the view hides what it knows the server lacks).
			return &model.Error{Code: model.ErrorCode(e.Code), Message: "服务器还没更新，暂时用不了"}
		}
		msg := strings.TrimRight(e.Message, "。.")
		if msg == "" {
			msg = "没办成，等一下再试试"
		}
		return &model.Error{Code: model.ErrorCode(e.Code), Message: msg}
	}
	return &model.Error{Code: model.ErrInternal, Message: "没办成，等一下再试试"}
}
