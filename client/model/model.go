// Package model is the client's world as the view reads it: who the person
// is, their LANs, the people and devices in them, and how this computer
// reaches each device (implementation.md §2.7).
//
// A Snapshot is built whole and never changed afterwards; a new one takes
// its place. So the view, which reads it without locks on the main thread,
// can hold one for a whole frame while a store builds the next one on
// another goroutine. It holds no MyGo types: owner colors are indices the
// view turns into colors, so the same snapshot can feed the desktop, the
// Web and the tests.
package model

import (
	"hash/fnv"
	"slices"
	"time"
)

// Ids are opaque: only the control server makes them, and they never change,
// so the client keys colors and saved layouts on them.
type (
	PersonID string
	LANID    string
	DeviceID string
	RelayID  string
)

// Snapshot is everything the view shows, at one version.
type Snapshot struct {
	// Version goes up with every change the control server makes. The ops
	// the view sent stay laid over the snapshot until it reaches the version
	// they took effect in.
	Version uint64

	// Engine is false while this computer's engine does not answer: the
	// view says so, and the store keeps trying.
	Engine bool
	// Loaded is set once the person's world came: until then the view does
	// not know whether they have LANs, and says it is still connecting.
	Loaded bool
	// LoggedIn is false on a computer no one logged in on yet: the view then
	// shows only the button to log in.
	LoggedIn bool
	// LoginURL is set while a login waits for the browser.
	LoginURL string
	Me       PersonID
	Self     DeviceID // this computer
	// Control is false while the control server is out of reach. The engine
	// keeps the connections it has meanwhile, so the view keeps drawing them.
	Control bool

	People  map[PersonID]*Person
	LANs    []*LAN // in the order I joined them
	Devices map[DeviceID]*Device
	// Mine are my own devices in a fixed order, the ones in no LAN too: the
	// tray of a LAN is those of them not in it, in this order.
	Mine   []DeviceID
	Relays map[RelayID]*Relay
	// Paths is how this computer reaches each device now, debounced (§2.7
	// step 7). A device missing here is reached as Path tells.
	Paths map[DeviceID]Path

	SharedWithMe int // devices others share with me alone (M3)
	// Local are the common services this computer listens on, off: what
	// its card offers to open besides those it has opened before.
	Local []Service
}

// Person is someone in my LANs, me too.
type Person struct {
	ID      PersonID
	Name    string // what people call them: "阿杰"
	Initial string // what their ball shows: "杰"
	Handle  string // their short Latin name, as in nas.jie.deneb.internal: "jie"
	Color   int    // the index of their owner color; mine is 0
}

// LAN is one of my LANs.
type LAN struct {
	ID    LANID
	Name  string
	Owner PersonID
	// Members are in the order they joined, the owner first.
	Members []PersonID
	// Devices are in the order they joined.
	Devices []DeviceID
	// Services are what each device in the LAN opens to the LAN: all of
	// them, on or off, for my own devices; only the ones on for the others'.
	Services map[DeviceID][]Service
	// Relays are the relays the LAN uses, the one it prefers first.
	Relays []RelayID
}

// Has reports whether dev is in the LAN.
func (l *LAN) Has(dev DeviceID) bool { return slices.Contains(l.Devices, dev) }

// Device is a computer, NAS, console or server.
type Device struct {
	ID    DeviceID
	Owner PersonID
	Name  string // what people call it: "阿杰的 NAS"
	Kind  string // laptop, desktop, mini, server, nas, console, phone, tablet or other
	OS    string
	Addr  string // its IPv4 address, the same in every LAN
	Addr6 string
	FQDN  string // its full name: nas.jie.deneb.internal
	// Online is what the control server last heard; Paths says whether this
	// computer reaches it.
	Online bool
	// Catalog is every service set up on one of my devices, on in some LAN
	// or not. A device joining a LAN brings these.
	Catalog []Service
	// Refused are the people I do not let use one of my devices. Refusals
	// are kept per device (DESIGN §6.5).
	Refused map[PersonID]bool
	// RefusesMe is set on another's device whose owner does not let me use it.
	RefusesMe bool
}

// Service is something a device opens to others, in everyday words.
type Service struct {
	// Key names the service on its device, to the engine and the server:
	// its ports, such as "tcp/3389".
	Key  string
	Kind string // screen, files, web, shell, game or other
	Name string // what people call it: "远程桌面"
	// Proto and Port are what "打开" opens: its first port.
	Proto string
	Port  uint16
	// Ports are all its ports, as "更多" lists them: "3389".
	Ports string
	On    bool
}

// Relay is a server that carries traffic between devices that cannot reach
// each other directly.
type Relay struct {
	ID   RelayID
	Name string // where it is: "香港"
}

// PathKind is how this computer reaches a device.
type PathKind string

const (
	PathSelf        PathKind = "self"        // it is this computer
	PathDirect      PathKind = "direct"      // 直接连上
	PathRelay       PathKind = "relay"       // 经中转
	PathConnecting  PathKind = "connecting"  // 正在连接…
	PathUnreachable PathKind = "unreachable" // online, but nothing answers: 连不上
	PathOffline     PathKind = "offline"     // 不在线
	PathPaused      PathKind = "paused"      // 已暂停 (M2)
	PathRefused     PathKind = "refused"     // its owner does not let me use it
	// PathApart is a device online in no LAN with this computer, as in a
	// LAN made on another of my devices that this one is not in yet: the
	// engine never connects to it, so it is not being connected to.
	PathApart PathKind = "apart"
)

// Connected reports whether the device can be used now.
func (k PathKind) Connected() bool { return k == PathSelf || k == PathDirect || k == PathRelay }

// Path is how this computer reaches one device, as the view shows it.
type Path struct {
	Kind  PathKind
	Relay RelayID // when Kind is PathRelay
	RTT   time.Duration
	Loss  float32
	Since time.Time // when this kind began
}

// LAN returns the LAN of id, or nil.
func (s *Snapshot) LAN(id LANID) *LAN {
	for _, l := range s.LANs {
		if l.ID == id {
			return l
		}
	}
	return nil
}

// Device returns the device of id, or nil.
func (s *Snapshot) Device(id DeviceID) *Device { return s.Devices[id] }

// Person returns the person of id, or nil.
func (s *Snapshot) Person(id PersonID) *Person { return s.People[id] }

// Path returns how this computer reaches dev now. A device the engine has
// not reported on yet is being connected to, offline if the server says
// so, or apart if it is in no LAN with this computer.
func (s *Snapshot) Path(dev DeviceID) Path {
	if dev == s.Self && dev != "" {
		return Path{Kind: PathSelf}
	}
	if p, ok := s.Paths[dev]; ok {
		return p
	}
	if d := s.Devices[dev]; d != nil && !d.Online {
		return Path{Kind: PathOffline}
	}
	if !s.together(dev) {
		return Path{Kind: PathApart}
	}
	return Path{Kind: PathConnecting}
}

// together reports whether dev is in a LAN with this computer, as it is
// whenever this computer is not known yet.
func (s *Snapshot) together(dev DeviceID) bool {
	if s.Self == "" {
		return true
	}
	for _, l := range s.LANs {
		if l.Has(s.Self) && l.Has(dev) {
			return true
		}
	}
	return false
}

// Tray returns my devices that are not in the LAN of id, in Mine's order.
func (s *Snapshot) Tray(id LANID) []DeviceID {
	l := s.LAN(id)
	var out []DeviceID
	for _, d := range s.Mine {
		if l == nil || !l.Has(d) {
			out = append(out, d)
		}
	}
	return out
}

// NumColors is how many owner colors there are (DESIGN §3.3).
const NumColors = 6

// ColorFor returns the owner color of a person on my screen: blue for me,
// and for anyone else one of the other five by a fixed rule on their id, so
// that they have the same color in every one of my LANs and on every start.
func ColorFor(me, id PersonID) int {
	if id == me {
		return 0
	}
	h := fnv.New32a()
	h.Write([]byte(id))
	return 1 + int(h.Sum32()%(NumColors-1))
}

// Invite is what an invite link says before I accept it: who asks me into
// which LAN (scene 3's card).
type Invite struct {
	Link      string // as it was opened
	LAN       LANID
	LANName   string
	Inviter   Person
	AlreadyIn bool // I am in the LAN already
	Gone      bool // the link was used, taken back or expired
}

// Error is a store's no: its kind, for the view to tell what to do, and one
// sentence in Chinese the toast shows as it is.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// ErrorCode is the kind of an Error. The strings are the local interface's.
type ErrorCode string

const (
	ErrForbidden   ErrorCode = "forbidden"     // 没权限
	ErrOffline     ErrorCode = "offline"       // the control server is out of reach
	ErrRefused     ErrorCode = "refused"       // the other end does not let me
	ErrNotLoggedIn ErrorCode = "not_logged_in" // no one logged in on this computer
	ErrNotRunning  ErrorCode = "not_running"   // the engine is not running
	ErrConflict    ErrorCode = "conflict"
	ErrInvalid     ErrorCode = "invalid"
	ErrTimeout     ErrorCode = "timeout"
	ErrInternal    ErrorCode = "internal"
)
