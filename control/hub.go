package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// hub keeps the open streams and which devices are online. After every
// change it computes each stream's frame and hands over the ones whose
// content changed; a device whose map and view stayed the same hears
// nothing.
type hub struct {
	mu      sync.Mutex
	subs    map[*sub]bool
	streams map[proto.DeviceID]int         // open streams per device
	views   map[proto.PersonID]int         // open browser streams per person
	online  map[proto.DeviceID]bool        // as last published
	offline map[proto.DeviceID]*time.Timer // pending offline marks, for the grace period
	now     func() time.Time
}

// One device, or one person's browsers, may hold only so many streams at
// once: each costs a map or a view on every change (M7 review). An engine
// holds one, two for a moment while it reconnects; a person has a few tabs.
const (
	maxStreamsPerDevice = 8
	maxStreamsPerPerson = 32
)

// errTooManyStreams turns away a stream over the limit.
var errTooManyStreams = newErr(http.StatusTooManyRequests, proto.ErrConflict, "同时开着的连接太多了，请关掉一些再试")

// offlineGrace is how long a device that dropped its stream still shows
// online, so a reconnect does not flicker every peer's screen.
const offlineGrace = 10 * time.Second

func newHub(now func() time.Time) *hub {
	return &hub{
		subs:    map[*sub]bool{},
		streams: map[proto.DeviceID]int{},
		views:   map[proto.PersonID]int{},
		online:  map[proto.DeviceID]bool{},
		offline: map[proto.DeviceID]*time.Timer{},
		now:     now,
	}
}

// sub is one open stream. The writer waits on notify and sends what is in
// pending; only the newest frame is kept, as each frame is whole.
type sub struct {
	device proto.DeviceID
	view   bool // the stream asked for the user view too
	// person is a browser's stream (/api/v1/stream): the person's view
	// alone, no device and no network map.
	person proto.PersonID

	mu       sync.Mutex
	pending  []byte
	ended    bool // the last frame (LoggedOut) is pending or sent
	notify   chan struct{}
	sent     bool
	lastHash [32]byte
	lastV    uint64
}

// take returns the frame to send, if any, and whether it ends the stream.
func (s *sub) take() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.pending
	s.pending = nil
	return b, s.ended && b != nil
}

// lastSent is the newest version handed to device's streams, or with
// device empty to person's browsers; 0 when none is open or none has had a
// frame yet. An op that changed nothing answers with it (applyOp).
func (h *hub) lastSent(device proto.DeviceID, person proto.PersonID) uint64 {
	h.mu.Lock()
	var mine []*sub
	for s := range h.subs {
		if device != "" && s.device == device || device == "" && person != "" && s.person == person {
			mine = append(mine, s)
		}
	}
	h.mu.Unlock()
	var v uint64
	for _, s := range mine {
		s.mu.Lock()
		v = max(v, s.lastV)
		s.mu.Unlock()
	}
	return v
}

func (h *hub) isOnline(id proto.DeviceID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[id]
}

// onlineSnapshot copies the online set for computing frames without the
// hub's lock.
func (h *hub) onlineSnapshot() online {
	h.mu.Lock()
	m := make(map[proto.DeviceID]bool, len(h.online))
	for k, v := range h.online {
		m[k] = v
	}
	h.mu.Unlock()
	return func(id proto.DeviceID) bool { return m[id] }
}

// add registers a stream and reports whether its device just came online.
// add takes a stream in, unless its device or person is at the limit (ok
// false; then it must not be removed).
func (h *hub) add(s *sub) (cameOnline, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.person != "" {
		// A browser is not a device: nothing comes online.
		if h.views[s.person] >= maxStreamsPerPerson {
			return false, false
		}
		h.views[s.person]++
		h.subs[s] = true
		return false, true
	}
	if h.streams[s.device] >= maxStreamsPerDevice {
		return false, false
	}
	h.subs[s] = true
	h.streams[s.device]++
	if t := h.offline[s.device]; t != nil {
		t.Stop()
		delete(h.offline, s.device)
	}
	if !h.online[s.device] {
		h.online[s.device] = true
		return true, true
	}
	return false, true
}

// remove drops a stream. When it was the device's last, goOffline runs
// after the grace period unless the device reconnected by then.
func (h *hub) remove(s *sub, goOffline func(proto.DeviceID)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, s)
	if s.person != "" {
		if h.views[s.person]--; h.views[s.person] <= 0 {
			delete(h.views, s.person)
		}
		return
	}
	h.streams[s.device]--
	if h.streams[s.device] > 0 {
		return
	}
	delete(h.streams, s.device)
	id := s.device
	h.offline[id] = time.AfterFunc(offlineGrace, func() {
		h.mu.Lock()
		if h.streams[id] > 0 || h.offline[id] == nil {
			h.mu.Unlock()
			return
		}
		delete(h.offline, id)
		h.online[id] = false
		h.mu.Unlock()
		goOffline(id)
	})
}

// publish hands every stream its frame for world w at version v. force
// names a device whose streams get the frame even if nothing in it
// changed: the device that just sent an op waits for that version.
func (h *hub) publish(w *World, v uint64, force proto.DeviceID) {
	h.mu.Lock()
	subs := make([]*sub, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	isOnline := h.onlineSnapshot()
	now := h.now()
	views := map[proto.PersonID]*proto.UserView{}
	for _, s := range subs {
		h.deliver(s, w, v, isOnline, now, views, s.device == force)
	}
}

// prime sends a new stream its first frame, unless a publish beat it to it.
func (h *hub) prime(s *sub, w *World, v uint64) {
	h.deliver(s, w, v, h.onlineSnapshot(), h.now(), map[proto.PersonID]*proto.UserView{}, false)
}

func (h *hub) deliver(s *sub, w *World, v uint64, isOnline online, now time.Time, views map[proto.PersonID]*proto.UserView, force bool) {
	var f *proto.StreamFrame
	if s.person != "" {
		f = viewFrameFor(w, s.person, v, isOnline, now, views)
	} else {
		f = frameFor(w, s.device, s.view, v, isOnline, now, views)
	}
	if f == nil {
		s.mu.Lock()
		if !s.ended {
			s.ended = true
			s.pending, _ = json.Marshal(proto.StreamFrame{LoggedOut: true})
			s.signal()
		}
		s.mu.Unlock()
		return
	}
	// The hash leaves the version out, so a frame that only moved to a
	// newer version is not news.
	f.Version = 0
	if f.NetMap != nil {
		f.NetMap.Version = 0
	}
	if f.UserView != nil {
		f.UserView.Version = 0
	}
	// Encoded into a buffer that is used again: most frames are found
	// unchanged, and a fresh copy of each, every change, was most of the
	// garbage with thousands of streams (the load test, design/plan/m7/
	// control-ops.md §4).
	buf := frameBufs.Get().(*bytes.Buffer)
	buf.Reset()
	json.NewEncoder(buf).Encode(f)
	sum := sha256.Sum256(buf.Bytes())
	if buf.Cap() <= maxPooledFrame {
		frameBufs.Put(buf)
	}
	f.Version = v
	if f.NetMap != nil {
		f.NetMap.Version = v
	}
	if f.UserView != nil {
		f.UserView.Version = v
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || v < s.lastV || (s.sent && sum == s.lastHash && !force) {
		return
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	s.pending, s.sent, s.lastHash, s.lastV = b, true, sum, v
	s.signal()
}

// frameBufs are buffers to encode frames into for their hash; a buffer
// grown past maxPooledFrame (a map of hundreds of peers) is let go.
var frameBufs = sync.Pool{New: func() any { return new(bytes.Buffer) }}

const maxPooledFrame = 1 << 20

// signal wakes the writer; s.mu is held.
func (s *sub) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// viewFrameFor builds a browser's frame: the person's view alone, or nil
// if there is no such person any more.
func viewFrameFor(w *World, p proto.PersonID, v uint64, isOnline online, now time.Time, views map[proto.PersonID]*proto.UserView) *proto.StreamFrame {
	uv, ok := views[p]
	if !ok {
		uv = w.userView(p, v, isOnline, now)
		views[p] = uv
	}
	if uv == nil {
		return nil
	}
	c := *uv
	return &proto.StreamFrame{Version: v, UserView: &c}
}

// frameFor builds a device's frame, or nil if no one is logged in on it.
// views caches each person's view across their devices in one publish.
func frameFor(w *World, id proto.DeviceID, withView bool, v uint64, isOnline online, now time.Time, views map[proto.PersonID]*proto.UserView) *proto.StreamFrame {
	nm := w.netMap(id, v, isOnline)
	if nm == nil {
		return nil
	}
	f := &proto.StreamFrame{Version: v, NetMap: nm}
	if withView {
		owner := nm.Self.Owner
		uv, ok := views[owner]
		if !ok {
			uv = w.userView(owner, v, isOnline, now)
			views[owner] = uv
		}
		if uv != nil {
			c := *uv // the hash pass zeroes the version; keep the cached one intact
			f.UserView = &c
		}
	}
	return f
}
