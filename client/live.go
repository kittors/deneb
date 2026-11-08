package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
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
	deb     debouncer
	snap    *model.Snapshot
	subs    map[int]func(*model.Snapshot)
	nextSub int
	timer   *time.Timer
	cancel  context.CancelFunc
}

// NewLive returns the store of the engine lc talks to. It watches nothing
// until Start.
func NewLive(lc *localclient.Client) *Live {
	l := &Live{lc: lc, now: time.Now, subs: map[int]func(*model.Snapshot){}}
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

func (l *Live) watch(ctx context.Context) {
	for {
		err := l.lc.Watch(ctx, func(ev apitype.Event) error {
			switch ev.Type {
			case apitype.EventState:
				if ev.State != nil {
					l.takeState(ev.State)
				}
			case apitype.EventPaths:
				l.takePaths(ev.Paths)
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
	if v := l.view; v != nil && st.LoggedIn {
		s.Loaded = true
		if v.Me != "" {
			s.Me = model.PersonID(v.Me)
		}
		for _, p := range v.People {
			s.People[model.PersonID(p.ID)] = person(p, s.Me)
		}
		for _, d := range v.Devices {
			md := &model.Device{
				ID: model.DeviceID(d.ID), Owner: model.PersonID(d.Owner), Name: d.DisplayName,
				Kind: string(d.Kind), OS: string(d.OS), FQDN: d.FQDN, Online: d.Online,
				RefusesMe: d.RefusesMe, Refused: map[model.PersonID]bool{},
			}
			if md.Name == "" {
				md.Name = d.Name
			}
			for _, a := range d.Addrs {
				if a.Addr().Is4() && md.Addr == "" {
					md.Addr = a.Addr().String()
				} else if a.Addr().Is6() && md.Addr6 == "" {
					md.Addr6 = a.Addr().String()
				}
			}
			for _, p := range d.Refused {
				md.Refused[model.PersonID(p)] = true
			}
			for _, svc := range d.Services {
				md.Catalog = append(md.Catalog, service(svc, false))
			}
			s.Devices[md.ID] = md
			if md.Owner == s.Me {
				s.Mine = append(s.Mine, md.ID)
			}
		}
		for _, r := range v.Relays {
			s.Relays[model.RelayID(r.ID)] = &model.Relay{ID: model.RelayID(r.ID), Name: r.Name}
		}
		for _, lv := range v.LANs {
			ml := &model.LAN{ID: model.LANID(lv.ID), Name: lv.Name, Owner: model.PersonID(lv.Owner),
				Services: map[model.DeviceID][]model.Service{}}
			for _, m := range lv.Members {
				// Only those in: someone invited, or waiting to be let in
				// (M2), is not in the LAN yet.
				if m.State == proto.MemberJoined || m.State == "" {
					ml.Members = append(ml.Members, model.PersonID(m.Person))
				}
			}
			for _, d := range lv.Devices {
				id := model.DeviceID(d.Device)
				ml.Devices = append(ml.Devices, id)
				svcs := make([]model.Service, 0, len(d.Services))
				for _, ss := range d.Services {
					svcs = append(svcs, service(ss.Service, ss.On))
				}
				ml.Services[id] = svcs
			}
			for _, r := range lv.Relays {
				ml.Relays = append(ml.Relays, model.RelayID(r))
			}
			s.LANs = append(s.LANs, ml)
		}
	}
	for _, ls := range l.local {
		s.Local = append(s.Local, service(localService(ls), false))
	}
	paths, next := l.deb.at(now)
	s.Paths = paths
	l.wakeAtLocked(next)
	return s
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
	args := proto.OpArgs{
		LAN: proto.LANID(op.LAN), Device: proto.DeviceID(op.Device), Person: proto.PersonID(op.Person),
		Name: op.Name, Invite: op.Invite, On: op.On, Refuse: op.Refuse,
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
		}
	}
	return r, nil
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
		AlreadyIn: p.AlreadyIn, Gone: p.State != "" && p.State != proto.InvitePending,
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
		msg := strings.TrimRight(e.Message, "。.")
		if msg == "" {
			msg = "没办成，等一下再试试"
		}
		return &model.Error{Code: model.ErrorCode(e.Code), Message: msg}
	}
	return &model.Error{Code: model.ErrInternal, Message: "没办成，等一下再试试"}
}
