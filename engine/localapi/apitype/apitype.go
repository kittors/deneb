// Package apitype holds the types of the engine's local API (plan §2.6.2):
// what denebd tells the desktop client and the command line over a Unix
// socket or a named pipe. It has no dependencies beyond the standard library,
// so the client can import it without pulling in WireGuard or gVisor.
//
// Conventions, agreed with the client line: JSON field names are camelCase,
// every id is an opaque string, times are RFC 3339, round-trip times are
// whole milliseconds.
package apitype

import (
	"encoding/json"
	"fmt"
	"time"
)

// APIVersion is bumped on incompatible changes. GET /v1/version returns it,
// so a client built against a newer engine can say so instead of misreading.
const APIVersion = 1

// Opaque ids, issued by the control server.
type (
	DeviceID string
	PersonID string
	ScopeID  string // "lan/<id>", or "share/<id>" from M3
	RelayID  string
)

// Mode is how the engine carries this computer's traffic.
type Mode string

const (
	// ModeUserspace: no network interface; apps reach peers through the
	// engine (SOCKS5, or a local port mapping made by POST /v1/open).
	ModeUserspace Mode = "userspace"
	// ModeKernel: a system interface with one route for the deployment's
	// block; ordinary apps reach peers by name or address directly.
	ModeKernel Mode = "kernel"
)

// State is GET /v1/state and the first event of GET /v1/watch.
type State struct {
	// Version of the network map the engine is enforcing. The user view
	// carries the same number, so what the client draws and what the engine
	// does never disagree. 0 until the first map arrives.
	Version uint64 `json:"version"`

	LoggedIn bool `json:"loggedIn"`
	// LoginURL is set while a login started by POST /v1/login waits for
	// the browser.
	LoginURL     string   `json:"loginURL,omitempty"`
	User         PersonID `json:"user,omitempty"`
	SelfDeviceID DeviceID `json:"selfDeviceID,omitempty"`

	// ControlConnected is false while the control server is unreachable.
	// The engine keeps enforcing the last map meanwhile, so peers that were
	// connected stay connected (§2.6.1).
	ControlConnected bool   `json:"controlConnected"`
	ControlURL       string `json:"controlURL,omitempty"`

	Mode Mode `json:"mode"`

	// Self, Scopes and Peers are the engine's network map: only devices
	// that share a scope with this one, each with only the shared scopes.
	// Headless nodes have nothing else to show in `deneb status`.
	Self   *Device      `json:"self,omitempty"`
	Scopes []Scope      `json:"scopes"`
	Peers  []Peer       `json:"peers"`
	Relays []Relay      `json:"relays"`
	Paths  []PeerPath   `json:"paths"`
	Usage  []ServiceUse `json:"usage"` // services of this device in use now

	Engine EngineInfo `json:"engine"`

	// UserView is the control server's view for this person, forwarded
	// untouched: decode it with the types in proto/. Null on headless nodes,
	// which only fetch it when the command line asks (GET /v1/userview).
	UserView json.RawMessage `json:"userView,omitempty"`
}

// Device is one computer, server or NAS as the network map describes it.
type Device struct {
	ID          DeviceID `json:"id"`
	Name        string   `json:"name"`        // DNS label, e.g. "box"
	DisplayName string   `json:"displayName"` // what people call it, e.g. "构建服务器"
	Owner       PersonID `json:"owner"`
	OwnerName   string   `json:"ownerName"` // owner's DNS label, e.g. "lin"
	FQDN        string   `json:"fqdn"`      // box.lin.deneb.internal
	Addrs       []string `json:"addrs"`     // one IPv4 and one IPv6, the same in every LAN
	Kind        string   `json:"kind,omitempty"`
	OS          string   `json:"os,omitempty"`
}

// Peer is another device this one can see.
type Peer struct {
	Device
	Scopes []ScopeID `json:"scopes"` // only the scopes both devices are in
	Online bool      `json:"online"` // as the control server last saw it
	// RefusedByPeer: the peer's owner does not let my owner use it, so this
	// engine does not dial it. RefusedByMe: my owner does not let the peer's
	// owner use this device, so this engine drops what the peer starts.
	RefusedByPeer bool `json:"refusedByPeer,omitempty"`
	RefusedByMe   bool `json:"refusedByMe,omitempty"`
}

// Scope is a LAN (or, from M3, a share) this device is in.
type Scope struct {
	ID     ScopeID   `json:"id"`
	Name   string    `json:"name"`
	Paused bool      `json:"paused"` // set on this device only
	Open   []Service `json:"open"`   // what this device opens to the scope's other members
	Relays []RelayID `json:"relays"`
}

// Service is one port a device opens. Kind is proto.ServiceKind: screen,
// files, web, shell, game or other; the engine only looks at Proto and Port.
type Service struct {
	Kind  string `json:"kind"`
	Proto string `json:"proto"` // "tcp" or "udp"
	Port  uint16 `json:"port"`
}

type Relay struct {
	ID        RelayID `json:"id"`
	Name      string  `json:"name"` // "香港"
	Connected bool    `json:"connected"`
	Home      bool    `json:"home"` // the relay peers use to reach this device
}

// PathKind is how this computer reaches one peer right now.
type PathKind string

const (
	PathDirect      PathKind = "direct"      // a direct UDP path answers
	PathRelay       PathKind = "relay"       // via a relay; the peer answers there
	PathConnecting  PathKind = "connecting"  // just started, nothing has answered yet
	PathUnreachable PathKind = "unreachable" // online per the control server, but nothing answers
	PathOffline     PathKind = "offline"     // the control server says it is offline, and nothing answers
	PathPaused      PathKind = "paused"      // every scope shared with it is paused here
	PathRefused     PathKind = "refused"     // its owner does not let my owner use it
)

// PeerPath is reported raw: the client, not the engine, debounces it, so the
// debounce rules live in one place and are tested once (§2.7 step 7).
type PeerPath struct {
	DeviceID DeviceID `json:"deviceID"`
	Kind     PathKind `json:"kind"`
	Relay    RelayID  `json:"relay,omitempty"`    // when Kind is relay
	Endpoint string   `json:"endpoint,omitempty"` // when Kind is direct: the peer's ip:port
	RTTMs    int      `json:"rttMs"`              // 0 when unknown
	// Loss over the last 10 s on the path in use: a direct path can answer
	// and still lose a fifth of the packets (multi-lan-tech §6.8).
	Loss  float32   `json:"loss"`
	Since time.Time `json:"since"` // when this Kind began; scene 3 counts "已连接 2:14:08" from it
	// Idle: no traffic lately, so no probing either (M2: a heartbeat only
	// with traffic); Kind is the last one seen, and the line can stay as it
	// was. Traffic wakes it at once.
	Idle bool `json:"idle,omitempty"`
	// From is the kind before this one, however briefly it lasted: Kind
	// direct with From relay is "connected through the relay first, then
	// direct" (scene 1), even when the relay lasted a few milliseconds.
	From PathKind `json:"from,omitempty"`
	// Reason says why the relay although a direct path answers: "lossy"
	// (it drops packets) or "slow" (much slower than the relay).
	Reason string `json:"reason,omitempty"`
}

// ServiceUse is a service of this device that a peer uses right now: its
// connections to one port. When the service is closed, or the person
// refused, scene 5 runs a short red light down these lines.
type ServiceUse struct {
	DeviceID DeviceID  `json:"deviceID"` // the peer using it
	Scopes   []ScopeID `json:"scopes"`   // the LANs it is opened to that the peer shares
	Kind     string    `json:"kind,omitempty"`
	Name     string    `json:"name,omitempty"` // the service as opened, if it was
	Proto    string    `json:"proto"`          // "tcp" or "udp"
	Port     uint16    `json:"port"`
	Conns    int       `json:"conns"`
	Since    time.Time `json:"since"`    // the oldest connection
	LastSeen time.Time `json:"lastSeen"` // the latest packet
}

// EngineInfo is diagnostics for "更多" and `deneb status`.
type EngineInfo struct {
	Version       string    `json:"version"`
	ListenPort    uint16    `json:"listenPort"`
	BindInterface string    `json:"bindInterface,omitempty"` // physical interface the underlay is pinned to
	SOCKS         string    `json:"socks,omitempty"`         // userspace mode: SOCKS5 for local apps
	Endpoints     []string  `json:"endpoints,omitempty"`     // where peers may reach this device
	Problems      []Problem `json:"problems,omitempty"`
	// Watchers is how many /v1/watch streams are open: the desktop client
	// holds one while its window is open, so a script can wait for it.
	Watchers int `json:"watchers"`
}

// Problem is something the person may need to know, in one sentence.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EventType tells the kinds of GET /v1/watch events apart.
type EventType string

const (
	// EventState carries a whole new State. It is sent first, and again
	// whenever anything but paths changes.
	EventState EventType = "state"
	// EventPaths carries the PeerPath of every peer whose path changed.
	// Path kinds go out as they happen; RTT and loss at most every 2 s.
	EventPaths EventType = "paths"
	// EventUsage carries State.Usage when it changed (at most every 2 s).
	EventUsage EventType = "usage"
	// EventCut carries the uses just cut: a service of this device closed
	// to a LAN, or a person refused, while they were using it.
	EventCut EventType = "cut"
)

// Event is one line of the GET /v1/watch stream (NDJSON).
type Event struct {
	Type  EventType    `json:"type"`
	State *State       `json:"state,omitempty"`
	Paths []PeerPath   `json:"paths,omitempty"`
	Usage []ServiceUse `json:"usage,omitempty"` // EventUsage (may be empty: nobody uses anything now)
	Cut   []ServiceUse `json:"cut,omitempty"`
}

// OpRequest is POST /v1/ops. The engine forwards it to the control server.
type OpRequest struct {
	// ID is the idempotency key: a retried request with the same id is
	// applied at most once.
	ID   string          `json:"id"`
	Op   string          `json:"op"` // "lan.create", "lan.invite", ... (proto/)
	Args json.RawMessage `json:"args,omitempty"`
}

// OpResult is the answer to a successful op.
type OpResult struct {
	// Version in which the op took effect. The engine replies only after it
	// has applied that version (or after a short wait), so a State fetched
	// right after already shows the change.
	Version uint64          `json:"version"`
	Result  json.RawMessage `json:"result,omitempty"` // op-specific, e.g. the link from lan.invite
}

// ErrorCode is machine-readable; the message beside it is for people.
type ErrorCode string

const (
	ErrForbidden   ErrorCode = "forbidden"     // 没权限
	ErrOffline     ErrorCode = "offline"       // 连不上控制服务器（断网）
	ErrRefused     ErrorCode = "refused"       // 对方拒绝
	ErrNotLoggedIn ErrorCode = "not_logged_in" // 还没登录
	ErrNotFound    ErrorCode = "not_found"
	ErrAmbiguous   ErrorCode = "ambiguous" // a short name matches several devices
	ErrInvalid     ErrorCode = "invalid"
	ErrConflict    ErrorCode = "conflict"
	ErrTimeout     ErrorCode = "timeout"
	ErrInternal    ErrorCode = "internal"
)

// Error is the body of every non-2xx answer: {"error": {...}}.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"` // one sentence in Chinese, ready for the toast
	// Candidates lists the matches when Code is ambiguous.
	Candidates []Candidate `json:"candidates,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// ErrorBody wraps Error on the wire.
type ErrorBody struct {
	Error *Error `json:"error"`
}

// LoginRequest is POST /v1/login.
type LoginRequest struct {
	// ControlURL picks the control server for the first login. Empty means
	// the one the engine was started with.
	ControlURL string `json:"controlURL,omitempty"`
}

// LoginResponse carries the link to open in a browser, on this computer or
// any other.
type LoginResponse struct {
	URL string `json:"url"`
}

// PauseRequest is POST /v1/lan/pause. The engine changes its tables at once
// and tells the control server after, so a pause never waits on the network.
type PauseRequest struct {
	Scope  ScopeID `json:"scope"`
	Paused bool    `json:"paused"`
}

// Candidate is one device a name may mean.
type Candidate struct {
	Device DeviceID  `json:"device"`
	FQDN   string    `json:"fqdn"`
	Addr   string    `json:"addr"`
	Scopes []ScopeID `json:"scopes"`
}

// ResolveResult is GET /v1/resolve?name=box.
type ResolveResult struct {
	Name   string   `json:"name"`
	FQDN   string   `json:"fqdn"`
	Addr   string   `json:"addr"`
	Device DeviceID `json:"device"`
}

// PingResult is one probe of GET /v1/ping. The probe is an ICMP echo through
// the tunnel, so it passes the same visibility and filter checks as any
// other packet.
type PingResult struct {
	Seq      int      `json:"seq"`
	RTTMs    float64  `json:"rttMs,omitempty"`
	Path     PathKind `json:"path,omitempty"` // direct or relay, as sent
	Endpoint string   `json:"endpoint,omitempty"`
	Err      string   `json:"err,omitempty"`
}

// PingSummary is GET /v1/ping without stream=1.
type PingSummary struct {
	To       string       `json:"to"`
	Addr     string       `json:"addr"`
	Device   DeviceID     `json:"device,omitempty"`
	Sent     int          `json:"sent"`
	Received int          `json:"received"`
	MinMs    float64      `json:"minMs"`
	AvgMs    float64      `json:"avgMs"`
	MaxMs    float64      `json:"maxMs"`
	Results  []PingResult `json:"results"`
}

// PingLine is one line of GET /v1/ping?stream=1: a result per probe, then
// the summary as the last line.
type PingLine struct {
	Result  *PingResult  `json:"result,omitempty"`
	Summary *PingSummary `json:"summary,omitempty"`
}

// LocalService is one service GET /v1/services found listening here.
type LocalService struct {
	Kind  string `json:"kind"`
	Proto string `json:"proto"`
	Port  uint16 `json:"port"`
}

// OpenRequest is POST /v1/open: open one service of a peer.
type OpenRequest struct {
	Device DeviceID `json:"device"`
	Kind   string   `json:"kind,omitempty"`
	Proto  string   `json:"proto,omitempty"` // default "tcp"
	Port   uint16   `json:"port"`
}

// OpenResult is an address the system can open as is. In kernel mode it is
// the peer's name and port; in user-space mode a local port mapping on
// 127.0.0.1 that lives while the engine runs.
type OpenResult struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Addr string `json:"addr"` // host:port
}

// InvitePreviewRequest is POST /v1/invite/preview. Invite is the link
// (https://<server>/i/<token> or deneb://join/<token>) or the bare token; the
// answer is the control server's proto.InvitePreview, passed on as is.
type InvitePreviewRequest struct {
	Invite string `json:"invite"`
}

// VersionInfo is GET /v1/version.
type VersionInfo struct {
	API    int    `json:"api"`
	Engine string `json:"engine"`
}
