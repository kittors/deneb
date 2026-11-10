package proto

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// NetMap is what the control server sends one device. It holds only the
// peers this device shares a scope with, because a peer it cannot see is a
// peer it cannot reach: the first of the four layers of isolation
// (multi-lan-tech §3.5). The engine replaces its tables with each one whole;
// a NetMap is never patched in place.
type NetMap struct {
	Version uint64  `json:"version"`
	Network Network `json:"network"`
	Self    Node    `json:"self"`
	// Peers are sorted by ID. A peer is here only if it shares at least one
	// scope with this device: Scopes holds the shared scopes the peer has
	// not paused, PausedScopes the ones it paused. (Before M2 a peer that
	// paused every shared scope was left out; now it stays with empty
	// Scopes, so an engine keeps its keys and path and a resume needs no
	// round trip. An engine that knows only Scopes skips it, as before.)
	Peers []Peer `json:"peers"`
	// Scopes are the LANs (from M3, shares too) this device is in, sorted by
	// ID. Scopes this device paused are here too, marked Paused, so a resume
	// works at once even while the control server is out of reach.
	Scopes []Scope `json:"scopes"`
	// Relays are the ones the scopes use and the ones the peers call home,
	// sorted by ID: a packet to a peer goes through the peer's home relay.
	Relays []Relay `json:"relays"`
}

// Network is the deployment's address space, the same for every device.
type Network struct {
	// IPv4 is the deployment's /16 in 10.0.0.0/8: macOS routes it as one
	// route. It is never in 100.64.0.0/10 (multi-lan-tech §3.3).
	IPv4 netip.Prefix `json:"ipv4"`
	// IPv6 is the deployment's ULA /48.
	IPv6 netip.Prefix `json:"ipv6"`
	// DNS are where the engine answers Domain: one address in each block.
	DNS []netip.Addr `json:"dns"`
	// Domain is the one suffix the engine takes over: "deneb.internal".
	Domain string `json:"domain"`
}

// Node is one device as others see it.
//
// Devices and people have two names, as in the local interface
// (engine/localapi/apitype): Name is the DNS label ("nas", "jie") and
// DisplayName is what people see ("阿杰的 NAS", "阿杰"). The formal name is
// Name.OwnerName.Domain, which FQDN spells out.
type Node struct {
	ID               DeviceID   `json:"id"`
	Name             string     `json:"name"`
	DisplayName      string     `json:"displayName"`
	Owner            PersonID   `json:"owner"`
	OwnerName        string     `json:"ownerName"`
	OwnerDisplayName string     `json:"ownerDisplayName"`
	FQDN             string     `json:"fqdn"` // "nas.jie.deneb.internal", no trailing dot
	Kind             DeviceKind `json:"kind"`
	OS               OS         `json:"os"`

	NodeKey  NodePublic  `json:"nodeKey"`
	DiscoKey DiscoPublic `json:"discoKey"`
	// Addrs are one IPv4 /32 and one IPv6 /128, the same in every scope.
	Addrs []netip.Prefix `json:"addrs"`
	// Endpoints are the device's addresses on the Internet as it last
	// reported them, for hole punching.
	Endpoints []netip.AddrPort `json:"endpoints,omitempty"`
	HomeRelay RelayID          `json:"homeRelay,omitempty"`
	// Online is whether the device holds a stream to the control server
	// now. It is a hint for the screen; an engine keeps trying a peer that
	// shows offline, since the two may still reach each other.
	Online bool `json:"online"`
	// LastSeen is when it last dropped its stream, for "对方离线" and how
	// long ago. (M2)
	LastSeen time.Time `json:"lastSeen,omitzero"`
	// PublicLAN is set on a public device: it belongs to this LAN, not to
	// a person, and the LAN's owner manages it. Engines do not use it; the
	// interface reads it from the view. (M3)
	PublicLAN LANID `json:"publicLAN,omitempty"`
	// Caps are the capabilities the device claimed when its engine last
	// logged in (LoginRequest.Caps), sorted, without repeats. On Self they
	// echo what the server took. Missing means none: an old engine, or a
	// server before M4. (M4)
	Caps []string `json:"caps,omitempty"`
}

// Peer is another device this one may reach.
type Peer struct {
	Node
	// Scopes are only the scopes both devices are in, minus the ones the
	// peer paused, so a peer never learns of the others. It may be empty
	// when PausedScopes is not.
	Scopes []ScopeID `json:"scopes"`
	// PausedScopes are the scopes both devices are in that the peer paused
	// on itself. The pausing device enforces its pause, dropping those
	// scopes' packets both ways; this side keeps the peer known (keys,
	// paths), shows it as paused, and keeps letting those scopes through as
	// before, so traffic flows again the moment the peer resumes, before
	// the server's next map arrives. (M2)
	PausedScopes []ScopeID `json:"pausedScopes,omitempty"`
	// Refusals are kept per device (DESIGN §6.5) and both ends are told: the
	// refused end stops dialing, and the refusing end's filter drops it
	// anyway.
	RefusedByPeer bool `json:"refusedByPeer,omitempty"` // the peer's owner refused my owner on it
	RefusedByMe   bool `json:"refusedByMe,omitempty"`   // my owner refused the peer's owner on me
	// Relays are where to reach the peer through a relay, best first: the
	// relays it is connected to (Report.Relays) that are on the lists of
	// the scopes both devices are in and the peer has not paused, so each
	// one lets both in (admission, implementation.md §2.6.3). Node.HomeRelay
	// is the first of them, and empty when there is none: then only a
	// direct path reaches the peer. (M4)
	Relays []RelayID `json:"relays,omitempty"`
}

// Scope is one scope this device is in, as this device should enforce it.
type Scope struct {
	ID   ScopeID `json:"id"`
	Name string  `json:"name"` // the LAN's name: "工作室"
	// Open is what this device opens to the other devices of the scope:
	// only the services switched on. Ping is not a service: every peer of a
	// scope may ping, unless refused (DESIGN §12: new devices open nothing
	// but answer ping, so people can tell they are connected).
	Open []Service `json:"open"`
	// Paused is set by this device. The engine stops the scope's packets and
	// names at once, then reports it, so a pause never waits on the server;
	// this is the server's echo of the last report.
	Paused bool `json:"paused,omitempty"`
	// Relays are the relays of the scope, most preferred first.
	Relays []RelayID `json:"relays"`

	// Kind is what the scope is: a LAN, or a share (one device given to one
	// person). Engines pass it on for display only. (M3)
	Kind ScopeKind `json:"kind,omitempty"`
	// NoInbound closes the scope to anything its peers start: this side
	// only connects out. The server sets it on a share's receiver side, so
	// the shared device cannot reach the receiver's devices (DESIGN §12
	// item 12, "只有他能连你，反过来不行"). (M3)
	//
	//   - It blocks only what the peer starts: replies to connections this
	//     side started pass as ever (the receiver uses the device's
	//     services, the device answers).
	//   - It is per scope, and scopes add up: if the two devices also share
	//     a LAN, what that LAN lets in still comes in; NoInbound only keeps
	//     out what would come in through this scope.
	//   - With NoInbound set, the scope's Open is not looked at: nothing
	//     opens this side through it, whatever Open says.
	//   - Pauses, refusals and PausedScopes work on it as on any scope.
	//
	// An engine from before M3 does not know it and goes by Open, which the
	// server sends empty on this side: the only difference is that the
	// shared device can still ping in.
	NoInbound bool `json:"noInbound,omitempty"`
}

// ScopeKind is what a scope is.
type ScopeKind string

const (
	ScopeLAN   ScopeKind = "lan"
	ScopeShare ScopeKind = "share"
)

// Service is something a device opens to others: a kind people recognize,
// and the ports the filter lets in for it.
type Service struct {
	Kind ServiceKind `json:"kind"`
	// Name is what people see. Empty means the kind's usual name
	// (ServiceKind.DefaultName).
	Name  string      `json:"name,omitempty"`
	Ports []PortRange `json:"ports"`
}

// ServiceKey names a service on one device: its ports, sorted and joined,
// such as "tcp/3389,udp/3389". Two services on a device never share ports.
type ServiceKey string

// Key returns the service's key.
func (s Service) Key() ServiceKey {
	ps := slices.Clone(s.Ports)
	slices.SortFunc(ps, PortRange.compare)
	ps = slices.Compact(ps)
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.String()
	}
	return ServiceKey(strings.Join(parts, ","))
}

// DisplayName is the name to show for the service.
func (s Service) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Kind.DefaultName()
}

// Validate reports what is wrong with the service, if anything.
func (s Service) Validate() error {
	if !s.Kind.Valid() {
		return fmt.Errorf("service kind %q", s.Kind)
	}
	if len(s.Ports) == 0 {
		return fmt.Errorf("service %q opens no port", s.DisplayName())
	}
	for _, p := range s.Ports {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	if len([]rune(s.Name)) > 40 {
		return fmt.Errorf("service name is longer than 40 characters")
	}
	return nil
}

// Allows reports whether the service lets in a connection to port over
// proto.
func (s Service) Allows(proto IPProto, port uint16) bool {
	for _, p := range s.Ports {
		if p.Proto == proto && p.First <= port && port <= p.Last {
			return true
		}
	}
	return false
}

// ServiceKind is the kind of a service, in the words people use for it.
type ServiceKind string

const (
	ServiceScreen ServiceKind = "screen" // 远程桌面, or macOS screen sharing
	ServiceFiles  ServiceKind = "files"  // 共享文件夹
	ServiceWeb    ServiceKind = "web"    // 网站
	ServiceShell  ServiceKind = "shell"  // 命令行; opening it lets people reach whatever the device reaches (multi-lan-tech §3.5)
	ServiceGame   ServiceKind = "game"   // 游戏
	ServiceOther  ServiceKind = "other"  // anything else, named by its owner
)

// Valid reports whether the kind is one of the known ones.
func (k ServiceKind) Valid() bool {
	switch k {
	case ServiceScreen, ServiceFiles, ServiceWeb, ServiceShell, ServiceGame, ServiceOther:
		return true
	}
	return false
}

// DefaultName is how the interface names a service of this kind.
func (k ServiceKind) DefaultName() string {
	switch k {
	case ServiceScreen:
		return "远程桌面"
	case ServiceFiles:
		return "共享文件夹"
	case ServiceWeb:
		return "网站"
	case ServiceShell:
		return "命令行"
	case ServiceGame:
		return "游戏"
	}
	return "服务"
}

// IPProto is a transport protocol a port range is for.
type IPProto string

const (
	TCP IPProto = "tcp"
	UDP IPProto = "udp"
)

// PortRange is a range of ports of one protocol, both ends included. It
// prints as "tcp/80" or "udp/27015-27030".
type PortRange struct {
	Proto IPProto
	First uint16
	Last  uint16
}

// Port is the range of a single port.
func Port(proto IPProto, port uint16) PortRange {
	return PortRange{Proto: proto, First: port, Last: port}
}

func (p PortRange) String() string {
	if p.First == p.Last {
		return fmt.Sprintf("%s/%d", p.Proto, p.First)
	}
	return fmt.Sprintf("%s/%d-%d", p.Proto, p.First, p.Last)
}

// Validate reports what is wrong with the range, if anything.
func (p PortRange) Validate() error {
	if p.Proto != TCP && p.Proto != UDP {
		return fmt.Errorf("port range %s: protocol must be tcp or udp", p)
	}
	if p.First == 0 || p.Last < p.First {
		return fmt.Errorf("port range %s: ports must be 1-65535, low to high", p)
	}
	return nil
}

func (p PortRange) compare(q PortRange) int {
	if c := strings.Compare(string(p.Proto), string(q.Proto)); c != 0 {
		return c
	}
	if p.First != q.First {
		return int(p.First) - int(q.First)
	}
	return int(p.Last) - int(q.Last)
}

func (p PortRange) MarshalText() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return []byte(p.String()), nil
}

func (p *PortRange) UnmarshalText(b []byte) error {
	q, err := ParsePortRange(string(b))
	if err != nil {
		return err
	}
	*p = q
	return nil
}

// ParsePortRange reads "tcp/80" or "udp/27015-27030".
func ParsePortRange(s string) (PortRange, error) {
	proto, ports, ok := strings.Cut(s, "/")
	if !ok {
		return PortRange{}, fmt.Errorf("port range %q: want proto/port", s)
	}
	lo, hi, isRange := strings.Cut(ports, "-")
	first, err := strconv.ParseUint(lo, 10, 16)
	if err != nil {
		return PortRange{}, fmt.Errorf("port range %q: %w", s, err)
	}
	last := first
	if isRange {
		if last, err = strconv.ParseUint(hi, 10, 16); err != nil {
			return PortRange{}, fmt.Errorf("port range %q: %w", s, err)
		}
	}
	p := PortRange{Proto: IPProto(proto), First: uint16(first), Last: uint16(last)}
	return p, p.Validate()
}

// Relay is a DERP server devices relay through when they cannot go direct.
// It forwards sealed packets by node key and never sees inside them.
type Relay struct {
	ID     RelayID     `json:"id"`
	Name   string      `json:"name"` // where it is, as people say it: "香港"
	Source RelaySource `json:"source"`
	Owner  PersonID    `json:"owner,omitempty"` // whose server or home device it is
	// HostName is the DERP server's TLS name.
	HostName string `json:"hostName"`
	// IPv4 and IPv6 are dialed as they are, not looked up: the local DNS may
	// be a proxy's fake-ip mode (multi-lan-tech §6.5).
	IPv4     netip.Addr `json:"ipv4,omitzero"`
	IPv6     netip.Addr `json:"ipv6,omitzero"`
	DERPPort uint16     `json:"derpPort"`           // TCP, TLS
	STUNPort uint16     `json:"stunPort,omitempty"` // UDP; zero means no STUN
	// Pin is the SPKI fingerprint of a Deneb relay's self-signed
	// certificate, "sha256:<hex>" as the control server's: the engine
	// trusts the relay by it, not by a CA. Empty for a relay whose
	// certificate a CA signed, as the derper on hk-spare. (M4)
	Pin string `json:"pin,omitempty"`
	// RateLimit is the most each connection may send through the relay,
	// in bytes a second; zero is no limit. Only public relays have one,
	// and their card says so: a limit is never silent (implementation.md
	// §3 M4). (M4)
	RateLimit int64 `json:"rateLimit,omitempty"`
}

// RelaySource is where a relay comes from (implementation.md §2.2).
type RelaySource string

const (
	RelayServer RelaySource = "server" // 我的服务器: someone's own server
	RelayHome   RelaySource = "home"   // 家里的设备: a device at home, from M4
	RelayPublic RelaySource = "public" // 公共: run by us, from M4
)

// DeviceKind is what a device is, for its icon and its words.
type DeviceKind string

const (
	KindLaptop  DeviceKind = "laptop"
	KindDesktop DeviceKind = "desktop"
	KindMini    DeviceKind = "mini" // a small desktop, such as a Mac mini
	KindServer  DeviceKind = "server"
	KindNAS     DeviceKind = "nas"
	KindConsole DeviceKind = "console" // 游戏主机
	KindPhone   DeviceKind = "phone"
	KindTablet  DeviceKind = "tablet"
	KindOther   DeviceKind = "other"
)

// Valid reports whether the kind is one of the known ones.
func (k DeviceKind) Valid() bool {
	switch k {
	case KindLaptop, KindDesktop, KindMini, KindServer, KindNAS, KindConsole, KindPhone, KindTablet, KindOther:
		return true
	}
	return false
}

// OS is the system a device runs.
type OS string

const (
	MacOS   OS = "macos"
	Windows OS = "windows"
	Linux   OS = "linux"
	IOS     OS = "ios"
	Android OS = "android"
	OtherOS OS = "other"
)

// Valid reports whether the system is one of the known ones.
func (o OS) Valid() bool {
	switch o {
	case MacOS, Windows, Linux, IOS, Android, OtherOS:
		return true
	}
	return false
}
