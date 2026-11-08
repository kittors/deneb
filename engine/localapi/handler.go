// Package localapi serves the engine's local API (plan §2.6.2) over HTTP.
// The real engine and the fake one in ./fake both implement Backend, so the
// client tests against exactly the wire format the engine speaks.
//
// This package stays free of WireGuard, gVisor and Tailscale imports: the
// desktop client links it (through ./fake) into its tests.
package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// Backend is what the HTTP layer needs from an engine.
type Backend interface {
	State() *apitype.State
	// Subscribe calls fn for every event after it returns. fn must not
	// block; the HTTP layer coalesces for slow readers itself.
	Subscribe(fn func(apitype.Event)) (cancel func())
	Do(ctx context.Context, op apitype.OpRequest) (*apitype.OpResult, error)
	Login(ctx context.Context, req apitype.LoginRequest) (*apitype.LoginResponse, error)
	Logout(ctx context.Context) error
	Pause(ctx context.Context, req apitype.PauseRequest) error
	Resolve(ctx context.Context, name string) (*apitype.ResolveResult, error)
	// Ping sends n probes and calls each after every one.
	Ping(ctx context.Context, to string, n int, interval, timeout time.Duration, each func(apitype.PingResult)) (*apitype.PingSummary, error)
	Services(ctx context.Context) ([]apitype.LocalService, error)
	Open(ctx context.Context, req apitype.OpenRequest) (*apitype.OpenResult, error)
	UserView(ctx context.Context) (json.RawMessage, error)
	// PreviewInvite asks the control server what an invite link is for,
	// before the person accepts it: proto.InvitePreview, passed on as is.
	PreviewInvite(ctx context.Context, invite string) (json.RawMessage, error)
	EngineVersion() string
}

// Handler returns the local API for b. extra, if not nil, serves paths the
// mux does not know (the test build adds /v1/test/... there).
func Handler(b Backend, extra http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, apitype.VersionInfo{API: apitype.APIVersion, Engine: b.EngineVersion()})
	})
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, b.State())
	})
	mux.HandleFunc("GET /v1/watch", func(w http.ResponseWriter, r *http.Request) {
		serveWatch(w, r, b)
	})
	mux.HandleFunc("POST /v1/ops", func(w http.ResponseWriter, r *http.Request) {
		var op apitype.OpRequest
		if !readJSON(w, r, &op) {
			return
		}
		if op.Op == "" {
			WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "缺少操作名。"})
			return
		}
		res, err := b.Do(r.Context(), op)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("POST /v1/login", func(w http.ResponseWriter, r *http.Request) {
		var req apitype.LoginRequest
		if r.ContentLength != 0 && !readJSON(w, r, &req) {
			return
		}
		res, err := b.Login(r.Context(), req)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("POST /v1/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := b.Logout(r.Context()); err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, struct{}{})
	})
	mux.HandleFunc("POST /v1/lan/pause", func(w http.ResponseWriter, r *http.Request) {
		var req apitype.PauseRequest
		if !readJSON(w, r, &req) {
			return
		}
		if err := b.Pause(r.Context(), req); err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, struct{}{})
	})
	mux.HandleFunc("GET /v1/resolve", func(w http.ResponseWriter, r *http.Request) {
		res, err := b.Resolve(r.Context(), r.URL.Query().Get("name"))
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		servePing(w, r, b)
	})
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, r *http.Request) {
		res, err := b.Services(r.Context())
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("POST /v1/open", func(w http.ResponseWriter, r *http.Request) {
		var req apitype.OpenRequest
		if !readJSON(w, r, &req) {
			return
		}
		res, err := b.Open(r.Context(), req)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("GET /v1/userview", func(w http.ResponseWriter, r *http.Request) {
		raw, err := b.UserView(r.Context())
		if err != nil {
			WriteError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		w.Write(raw)
	})
	mux.HandleFunc("POST /v1/invite/preview", func(w http.ResponseWriter, r *http.Request) {
		var req apitype.InvitePreviewRequest
		if !readJSON(w, r, &req) {
			return
		}
		if req.Invite == "" {
			WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "缺少邀请链接。"})
			return
		}
		raw, err := b.PreviewInvite(r.Context(), req.Invite)
		if err != nil {
			WriteError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	})
	if extra != nil {
		mux.Handle("/", extra)
	}
	return mux
}

// serveWatch streams events as NDJSON: the current State first, then every
// change. A slow reader never makes the engine wait: pending events are
// coalesced to the latest State and the latest path of each peer.
func serveWatch(w http.ResponseWriter, r *http.Request, b Backend) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	q := newEventQueue()
	cancel := b.Subscribe(q.push)
	defer cancel()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	if err := enc.Encode(apitype.Event{Type: apitype.EventState, State: b.State()}); err != nil {
		return
	}
	flusher.Flush()
	q.dropStatesUpTo() // anything queued so far is already in the first State
	for {
		select {
		case <-r.Context().Done():
			return
		case <-q.wake:
		}
		for _, ev := range q.drain() {
			if err := enc.Encode(ev); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}

// eventQueue keeps at most one pending State and one pending path per peer.
type eventQueue struct {
	mu    sync.Mutex
	state *apitype.State
	paths map[apitype.DeviceID]apitype.PeerPath
	order []apitype.DeviceID
	wake  chan struct{}
}

func newEventQueue() *eventQueue {
	return &eventQueue{paths: map[apitype.DeviceID]apitype.PeerPath{}, wake: make(chan struct{}, 1)}
}

func (q *eventQueue) push(ev apitype.Event) {
	q.mu.Lock()
	switch ev.Type {
	case apitype.EventState:
		if ev.State != nil {
			q.state = ev.State
			// A State carries every path; older pending paths are stale.
			q.paths = map[apitype.DeviceID]apitype.PeerPath{}
			q.order = q.order[:0]
		}
	case apitype.EventPaths:
		for _, p := range ev.Paths {
			if _, ok := q.paths[p.DeviceID]; !ok {
				q.order = append(q.order, p.DeviceID)
			}
			q.paths[p.DeviceID] = p
		}
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *eventQueue) dropStatesUpTo() {
	q.mu.Lock()
	q.state = nil
	q.mu.Unlock()
}

func (q *eventQueue) drain() []apitype.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []apitype.Event
	if q.state != nil {
		out = append(out, apitype.Event{Type: apitype.EventState, State: q.state})
		q.state = nil
	}
	if len(q.order) > 0 {
		ps := make([]apitype.PeerPath, 0, len(q.order))
		for _, id := range q.order {
			ps = append(ps, q.paths[id])
		}
		out = append(out, apitype.Event{Type: apitype.EventPaths, Paths: ps})
		q.paths = map[apitype.DeviceID]apitype.PeerPath{}
		q.order = q.order[:0]
	}
	return out
}

func servePing(w http.ResponseWriter, r *http.Request, b Backend) {
	qv := r.URL.Query()
	to := qv.Get("to")
	if to == "" {
		WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "要 ping 哪台设备？"})
		return
	}
	n := atoiDefault(qv.Get("n"), 4)
	if n < 1 || n > 1000 {
		WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "次数要在 1 到 1000 之间。"})
		return
	}
	interval := durDefault(qv.Get("interval"), time.Second)
	timeout := durDefault(qv.Get("timeout"), 2*time.Second)
	if qv.Get("stream") != "1" {
		res, err := b.Ping(r.Context(), to, n, interval, timeout, nil)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeJSON(w, res)
		return
	}
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	started := false
	start := func() {
		if !started {
			started = true
			w.Header().Set("Content-Type", "application/x-ndjson")
		}
	}
	res, err := b.Ping(r.Context(), to, n, interval, timeout, func(pr apitype.PingResult) {
		start()
		enc.Encode(apitype.PingLine{Result: &pr})
		if flusher != nil {
			flusher.Flush()
		}
	})
	if err != nil {
		if !started {
			WriteError(w, err)
			return
		}
		// Headers are gone; report the failure in-band.
		enc.Encode(apitype.PingLine{Summary: &apitype.PingSummary{To: to}})
		return
	}
	start()
	enc.Encode(apitype.PingLine{Summary: res})
}

// WriteError sends err as {"error": ...} with a fitting status code.
func WriteError(w http.ResponseWriter, err error) {
	var ae *apitype.Error
	if !errors.As(err, &ae) {
		ae = &apitype.Error{Code: apitype.ErrInternal, Message: "引擎出错了：" + err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(StatusFor(ae.Code))
	json.NewEncoder(w).Encode(apitype.ErrorBody{Error: ae})
}

// StatusFor maps an error code to an HTTP status. Clients should switch on
// the code, not the status; the status only helps curl users.
func StatusFor(c apitype.ErrorCode) int {
	switch c {
	case apitype.ErrInvalid:
		return http.StatusBadRequest
	case apitype.ErrNotLoggedIn:
		return http.StatusUnauthorized
	case apitype.ErrForbidden, apitype.ErrRefused:
		return http.StatusForbidden
	case apitype.ErrNotFound:
		return http.StatusNotFound
	case apitype.ErrAmbiguous, apitype.ErrConflict:
		return http.StatusConflict
	case apitype.ErrOffline:
		return http.StatusServiceUnavailable
	case apitype.ErrTimeout:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "请求格式不对：" + err.Error()})
		return false
	}
	return true
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func durDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 && d <= time.Minute {
		return d
	}
	return def
}
