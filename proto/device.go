package proto

import (
	"net/netip"
	"time"
)

// The device interface: engine ⇄ control server (implementation.md §2.6.1).
//
// Everything is HTTPS with JSON bodies, to the server's public URL, with
// the TLS pinned (PinnedTLSConfig). Every call but the two auth ones carries
// "Authorization: Bearer <token>". Errors come back as an ErrorBody with a
// 4xx or 5xx status; on 401 the engine gets a new token and retries.
//
// A device's life:
//
//  1. Auth: POST PathAuthChallenge, sign the challenge with the machine key,
//     POST PathAuthToken for a token good for about an hour. Any machine key
//     gets a token; what it may do depends on whether someone is logged in on
//     it. Proving the key first is what keeps anyone from tying someone
//     else's machine key to their own account.
//  2. Login: POST PathLogin with the node and disco keys. If no one is logged
//     in, the answer carries a LoginURL to open in a browser (the client
//     opens it; the command line prints it). Then POST PathLoginWait, which
//     answers once the person has logged in in the browser, or after about
//     25 seconds with LoggedIn still false: call it again.
//  3. Stream: GET PathStream. The server sends the whole current frame
//     first, then a new one each time something this device or its owner
//     sees changes, and a keep-alive frame every 15 seconds. A stream that
//     stays silent for 45 seconds is dead: reconnect.
//  4. Report: POST PathReport whenever the endpoints, the home relay or the
//     paused scopes change.
//  5. Ops: POST PathOps with an Op; the answer is an OpResult.
//
// If the control server is out of reach, the engine keeps working from the
// last NetMap: devices already connected stay connected.
const (
	PathAuthChallenge = "/device/v1/auth/challenge" // POST AuthChallengeRequest → AuthChallenge
	PathAuthToken     = "/device/v1/auth/token"     // POST AuthTokenRequest → AuthToken
	PathLogin         = "/device/v1/login"          // POST LoginRequest → LoginState
	PathLoginWait     = "/device/v1/login/wait"     // POST (no body) → LoginState, long poll
	PathLogout        = "/device/v1/logout"         // POST (no body) → LoginState
	PathStream        = "/device/v1/stream"         // GET ?since=<version>&userView=1 → NDJSON of StreamFrame
	PathReport        = "/device/v1/report"         // POST Report → ReportResult
	PathOps           = "/device/v1/ops"            // POST Op → OpResult
	PathUserView      = "/device/v1/userview"       // GET → UserView, for engines that do not stream it
	PathInvite        = "/device/v1/invite"         // POST InviteLookup → InvitePreview
)

// StreamKeepAlive is how often the stream sends a keep-alive frame, and
// StreamDeadAfter how long a silent stream is given before the engine
// reconnects.
const (
	StreamKeepAlive = 15 * time.Second
	StreamDeadAfter = 45 * time.Second
)

// AuthChallengeRequest asks for a challenge to sign.
type AuthChallengeRequest struct {
	MachineKey MachinePublic `json:"machineKey"`
}

// AuthChallenge is what to sign, with MachinePrivate.SignChallenge.
type AuthChallenge struct {
	Challenge []byte    `json:"challenge"`
	ExpiresAt time.Time `json:"expiresAt"` // about a minute
}

// AuthTokenRequest trades a signed challenge for a token.
type AuthTokenRequest struct {
	MachineKey MachinePublic `json:"machineKey"`
	Challenge  []byte        `json:"challenge"`
	Signature  []byte        `json:"signature"`
}

// AuthToken goes in "Authorization: Bearer <Token>".
type AuthToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// LoginRequest tells the server about this device. It is sent at every
// start, not only the first: the server updates the keys and the system
// facts it carries, and answers whether someone is logged in on it.
type LoginRequest struct {
	NodeKey  NodePublic  `json:"nodeKey"`
	DiscoKey DiscoPublic `json:"discoKey"`
	// Hostname is the system's host name. The device's Name (its DNS
	// label) is made from it when the device is first logged in.
	Hostname string `json:"hostname"`
	OS       OS     `json:"os"`
	// DisplayName and Kind are what the person called the device and what
	// it is, if the engine knows (the command line takes --name and --kind).
	// They are used when the device is first logged in; later changes go
	// through device.rename and device.setKind.
	DisplayName   string     `json:"displayName,omitempty"`
	Kind          DeviceKind `json:"kind,omitempty"`
	EngineVersion string     `json:"engineVersion,omitempty"`
}

// LoginState is whether someone is logged in on this machine.
type LoginState struct {
	LoggedIn bool `json:"loggedIn"`
	// LoginURL is set while no one is: open it in a browser, on any device.
	// It stays the same until ExpiresAt, so asking again gives the same one.
	LoginURL  string    `json:"loginURL,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// Device and Person are set once someone is.
	Device DeviceID `json:"device,omitempty"`
	Person PersonID `json:"person,omitempty"`
	// LoginExpiresAt is when the login runs out unless the device comes
	// back first; each token it gets renews it. (M2)
	LoginExpiresAt time.Time `json:"loginExpiresAt,omitzero"`
}

// StreamFrame is one line on the stream. A frame carries a NetMap, the
// owner's UserView if the stream asked for it, or neither when it is only a
// keep-alive. NetMap and UserView always share the frame's version.
type StreamFrame struct {
	Version   uint64    `json:"version,omitempty"`
	NetMap    *NetMap   `json:"netMap,omitempty"`
	UserView  *UserView `json:"userView,omitempty"`
	KeepAlive bool      `json:"keepAlive,omitempty"`
	// LoggedOut ends the stream: the device was logged out, here or by
	// someone else logging in on the machine. Log in again.
	LoggedOut bool `json:"loggedOut,omitempty"`
}

// Report is what the engine tells the server about itself. Each report
// replaces the last one whole.
type Report struct {
	Endpoints []netip.AddrPort `json:"endpoints"`
	HomeRelay RelayID          `json:"homeRelay,omitempty"`
	// Paused are the scopes this device paused on itself.
	Paused []ScopeID `json:"paused"`
}

// ReportResult answers a report.
type ReportResult struct {
	Version uint64 `json:"version"`
}

// InviteLookup asks what an invite link is for, before accepting it.
type InviteLookup struct {
	Invite string `json:"invite"` // the link, or its token
}
