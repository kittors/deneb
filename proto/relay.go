package proto

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// The relay API (implementation.md §2.6.3) is what a Deneb relay asks the
// control server: HTTPS and JSON, with the control server's pin, like the
// device API. Register comes first, with the registration token its owner
// got from relay.create; it answers with a secret the relay keeps, and
// every later call carries "Authorization: Bearer <secret>".
const (
	PathRelayRegister = "/relay/v1/register" // POST RelayRegisterRequest → RelayRegisterResult
	PathRelayAdmit    = "/relay/v1/admit"    // POST RelayAdmitRequest → RelayAdmitResult
	PathRelaySync     = "/relay/v1/sync"     // POST RelaySyncRequest → RelaySyncResult, every minute
	PathRelayWatch    = "/relay/v1/watch"    // GET ?since=<version> → RelayWatchResult, long poll
)

// RelayWatchResult answers a watch: the version now. It comes back as
// soon as the version passes since, or after half a minute; either way
// the relay syncs when it changed, so a kick or a relay taken out of a
// LAN closes connections within a second, not a minute.
type RelayWatchResult struct {
	Version uint64 `json:"version"`
}

// RelayRegisterRequest is a relay's first call, with its owner's
// registration token.
type RelayRegisterRequest struct {
	Token string `json:"token"`
	RelayInfo
}

// RelayInfo is where a relay is and the pin of its self-signed
// certificate. It comes with the registration and again with the first
// sync after each start, so a relay that moved or got a new certificate
// needs no new token.
type RelayInfo struct {
	// HostName is its TLS name; empty means its address.
	HostName string `json:"hostName,omitempty"`
	// IPv4 and IPv6 are where devices reach it. With neither, the server
	// takes the address the request came from.
	IPv4     netip.Addr `json:"ipv4,omitzero"`
	IPv6     netip.Addr `json:"ipv6,omitzero"`
	DERPPort uint16     `json:"derpPort"`
	STUNPort uint16     `json:"stunPort,omitempty"`
	Pin      string     `json:"pin"`
	Version  string     `json:"version,omitempty"` // the relay program's
	// Region is what the relay could find out about where it is, for its
	// name when it registers with a join code and no name was given: a
	// clue the server turns into a place, "name:香港" (the relay's
	// -region), "aliyun:cn-hongkong", "aws:ap-east-1" (a cloud's metadata),
	// "tz:Asia/Hong_Kong" ($TZ). Empty when it found nothing.
	Region string `json:"region,omitempty"`
}

// RelayJoin is a join code and the one-line install command that carries
// it (relay.join, M4): shown once, to be pasted on the owner's server.
type RelayJoin struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Command string `json:"command"`
	// Relay is the relay it sets up again; empty for a new one.
	Relay     RelayID   `json:"relay,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RelayJoinView is a join code waiting to be used, as its owner's view
// lists it: without the code, which is shown only once.
type RelayJoinView struct {
	ID        string    `json:"id"`
	Relay     RelayID   `json:"relay,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RelayJoinPrefix starts a join code. The code carries the control
// server's address and pin and the registration token, so a relay needs
// nothing else: deneb-relay --join <code>.
const RelayJoinPrefix = "deneb-join-"

type relayJoinCode struct {
	Control string `json:"c"`
	Pin     string `json:"p,omitempty"` // empty for a plain-HTTP development server
	Token   string `json:"t"`
}

// MakeRelayJoinCode bundles a control server's URL and pin with a
// registration token.
func MakeRelayJoinCode(control, pin, token string) string {
	b, _ := json.Marshal(relayJoinCode{Control: control, Pin: pin, Token: token})
	return RelayJoinPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ParseRelayJoinCode takes a join code apart.
func ParseRelayJoinCode(code string) (control, pin, token string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(code), RelayJoinPrefix)
	if !ok {
		return "", "", "", fmt.Errorf("加入码要以 %s 开头", RelayJoinPrefix)
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return "", "", "", fmt.Errorf("加入码不完整：%w", err)
	}
	var c relayJoinCode
	if err := json.Unmarshal(b, &c); err != nil || c.Control == "" || c.Token == "" {
		return "", "", "", errors.New("加入码不完整")
	}
	return c.Control, c.Pin, c.Token, nil
}

// RelayRegisterResult is who the relay is now.
type RelayRegisterResult struct {
	Relay  RelayID     `json:"relay"`
	Name   string      `json:"name"`
	Source RelaySource `json:"source"`
	// Secret is the relay's from now on; the token is used up. The server
	// keeps only its hash.
	Secret    string `json:"secret"`
	RateLimit int64  `json:"rateLimit,omitempty"` // bytes a second for each connection; zero is none
}

// RelayAdmitRequest asks whether a client may use the relay, once it has
// said which node key it is. It is Tailscale's derpserver admission
// request (tailcfg.DERPAdmitClientRequest), field names and all, so the
// derpserver's own hook asks it unchanged.
type RelayAdmitRequest struct {
	NodePublic NodePublic `json:"NodePublic"`
	Source     netip.Addr `json:"Source"` // the client's address
}

// RelayAdmitResult is derpserver's admission answer. A no comes with
// status 200 too: any other status the derpserver takes for the server
// being down. Reason says why not; derpserver ignores it, the Deneb relay
// passes it on.
type RelayAdmitResult struct {
	Allow  bool   `json:"Allow"`
	Reason string `json:"reason,omitempty"`
}

// Why a relay turns a device away (RelayAdmitResult.Reason).
const (
	// RelayDenyLoggedOut: no device someone is logged in on has the node
	// key, or its login ran out.
	RelayDenyLoggedOut = "loggedOut"
	// RelayDenyNotListed: someone's own relay, and the device is in none
	// of the LANs or shares it serves.
	RelayDenyNotListed = "notListed"
	// RelayDenyKicked: the relay's owner kicked the device, or its owner,
	// off it (relay.kick).
	RelayDenyKicked = "kicked"
)

// RelayRefusedPrefix starts the DERP health message (FrameHealth) a Deneb
// relay sends a client it turns away, just before closing: the reason
// follows it, "deneb-refused:notListed". An engine that sees it knows
// it was refused, not cut off. A client that is closed later, when a
// sync finds it may no longer use the relay, hears nothing first: the
// next connect tells it why.
const RelayRefusedPrefix = "deneb-refused:"

// RelaySyncRequest is a relay's call every minute: who is connected, and
// what each client sent and received since the last sync that got an
// answer.
type RelaySyncRequest struct {
	Connected []NodePublic       `json:"connected"`
	Usage     []RelayClientUsage `json:"usage,omitempty"`
	// Info comes with the first sync after a start: the server updates
	// the relay's address, ports and pin if they changed.
	Info *RelayInfo `json:"info,omitempty"`
	// Health is how the relay is doing, for its card's "负载".
	Health *RelayHealth `json:"health,omitempty"`
}

// RelayHealth is a relay's load, as it measures it.
type RelayHealth struct {
	Connections int `json:"connections"`
	// BytesPerSec is what it carried, in and out, over the time since the
	// last sync.
	BytesPerSec int64 `json:"bytesPerSec"`
	// CPU is its share of the CPU it may use, 0–1: of one CPU in a
	// container held to one.
	CPU         float64 `json:"cpu"`
	Memory      int64   `json:"memory"`                // bytes it uses
	MemoryLimit int64   `json:"memoryLimit,omitempty"` // its container's limit; zero for none known
	Uptime      int64   `json:"uptime"`                // seconds since it started
}

// RelayClientUsage is one client's bytes through the relay. Only per
// client: who sent to whom is not counted (pricing-and-relays §2.3).
type RelayClientUsage struct {
	Node     NodePublic `json:"node"`
	Sent     int64      `json:"sent"`     // what the client sent through the relay
	Received int64      `json:"received"` // what the relay delivered to it
}

// RelaySyncResult answers a sync.
type RelaySyncResult struct {
	// Deny are the connected clients that may no longer use the relay
	// (logged out, out of the LAN, the relay taken out of it): the relay
	// closes them.
	Deny      []NodePublic `json:"deny"`
	Name      string       `json:"name"`
	RateLimit int64        `json:"rateLimit,omitempty"`
}
