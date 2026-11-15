package proto

import (
	"net/netip"
	"time"
)

// A relay's state and its reasons (M7, design/plan/m7/relay-install-api.md
// §4): whether it can be used from outside, as the control server works it
// out from its own checks, a second place's (the watchdog on another
// machine) and what its owner's devices say. Its card shows it, and
// deneb-relay install and status print it: each problem carries a sentence
// to say and what to do, and its code and numbers for a card that says it
// its own way.

// RelayState is where a relay stands.
type RelayState string

const (
	// RelayWaiting (等待安装): no program has registered yet, or it said
	// it was uninstalled (RelayProblemUninstalled).
	RelayWaiting RelayState = "waiting"
	// RelayChecking (检查中): registered; the first check is not done.
	RelayChecking RelayState = "checking"
	// RelayOK (可以用): TCP and UDP answer from outside.
	RelayOK RelayState = "ok"
	// RelayPartial (部分可用): it relays, but not all is well: UDP does not
	// answer, only IPv6 does, or some of its owner's devices cannot reach
	// it. Problems say which.
	RelayPartial RelayState = "partial"
	// RelayUnreachable (连不上): TCP does not answer from outside.
	RelayUnreachable RelayState = "unreachable"
	// RelayOffline (不在线): no sync for 3 minutes; RelayView.LastSeen is
	// when it last synced.
	RelayOffline RelayState = "offline"
)

// RelayStatus is a relay's state, why, and what the checks saw.
type RelayStatus struct {
	State RelayState `json:"state"`
	// Checking: a check someone asked for runs now (the card's "正在检查",
	// its "重新检查" turned off). Checks every 10 minutes run unseen.
	Checking bool `json:"checking,omitempty"`
	// Alert: it was usable since it was set up, and now it cannot be
	// reached; its owner sees an alert until it is usable again.
	Alert bool `json:"alert,omitempty"`
	// UsableAt is when it last became usable; zero if it never was.
	UsableAt time.Time `json:"usableAt,omitzero"`
	// Problems are why it is not simply usable, the worst first.
	Problems []RelayProblem `json:"problems"`
	// Probes are what each place's last check saw, an address at a time.
	Probes []RelayProbe `json:"probes,omitempty"`
	// Devices are what its owner's devices online say: those connected to
	// it, and those that tried and could not. Missing while none of them
	// has tried it (it is in none of their LANs yet).
	Devices *RelayDevices `json:"devices,omitempty"`
	// CheckedAt, in the answer to /relay/v1/check only, is when the check
	// it reports finished; views leave it out, so a check every 10 minutes
	// that finds the same does not change them.
	CheckedAt time.Time `json:"checkedAt,omitzero"`
}

// Problem levels.
const (
	RelayLevelError = "error" // it cannot be used (RelayUnreachable)
	RelayLevelWarn  = "warn"  // it can, but not fully (RelayPartial)
	RelayLevelInfo  = "info"  // worth knowing; nothing wrong
)

// Problem codes (RelayProblem.Code).
const (
	// RelayProblemUninstalled (waiting): its program said it was
	// uninstalled; installing it again brings it back.
	RelayProblemUninstalled = "uninstalled"
	// RelayProblemNoPublicIP (error): the address devices would dial is
	// not one on the Internet, or the router's own is not (a carrier's
	// NAT): it cannot be a relay, but over IPv6 if it has a public one.
	RelayProblemNoPublicIP = "no_public_ip"
	// RelayProblemBehindNAT (error): behind a router (its own addresses
	// are private), with no port forwarded to it: Addr is its address on
	// the LAN, to forward the ports to.
	RelayProblemBehindNAT = "behind_nat"
	// RelayProblemTCPBlocked (error): TCP Port does not answer from
	// outside: a cloud's security group, the host firewall (Firewall, if
	// the relay found one), or the data center.
	RelayProblemTCPBlocked = "tcp_blocked"
	// RelayProblemPinMismatch (error): something answers TCP Port with a
	// certificate not the relay's: the port goes to another machine or
	// program.
	RelayProblemPinMismatch = "pin_mismatch"
	// RelayProblemUDPBlocked (warn): STUN on UDP Port does not answer: it
	// relays, but does not help devices connect directly.
	RelayProblemUDPBlocked = "udp_blocked"
	// RelayProblemIPv6Only (warn): only its IPv6 address answers: only
	// devices with IPv6 can use it.
	RelayProblemIPv6Only = "ipv6_only"
	// RelayProblemIPv6Blocked (info): its IPv6 address does not answer;
	// devices use IPv4.
	RelayProblemIPv6Blocked = "ipv6_blocked"
	// RelayProblemUntested (warn): no place could check it (none of them
	// has IPv6, and it has only IPv6): its devices will tell.
	RelayProblemUntested = "untested"
	// RelayProblemDevicesFailing (warn): Count of its owner's devices
	// online, of Of that tried, cannot reach it (Devices), though the
	// checks can: a route the checks do not take, across a border most
	// often.
	RelayProblemDevicesFailing = "devices_failing"
	// RelayProblemCrashing (warn): its program started Count times within
	// the hour.
	RelayProblemCrashing = "crashing"
	// RelayProblemPorts (info): it uses ports other than the usual ones
	// (DERPPort, STUNPort): those are the ones to open.
	RelayProblemPorts = "ports"
)

// RelayProblem is one reason a relay is not simply usable.
type RelayProblem struct {
	Code  string `json:"code"`
	Level string `json:"level"`
	// Message says it in a sentence, Advice what to do: as the server
	// would put them, for deneb-relay to print and a card that has no
	// words of its own for Code.
	Message string `json:"message"`
	Advice  string `json:"advice,omitempty"`

	// The details, as Code needs them.
	Port     uint16     `json:"port,omitempty"`
	Family   string     `json:"family,omitempty"` // "ipv4", "ipv6"
	Addr     netip.Addr `json:"addr,omitzero"`
	Firewall string     `json:"firewall,omitempty"` // RelayFirewall*
	DERPPort uint16     `json:"derpPort,omitempty"`
	STUNPort uint16     `json:"stunPort,omitempty"`
	Devices  []DeviceID `json:"devices,omitempty"`
	Count    int        `json:"count,omitempty"`
	Of       int        `json:"of,omitempty"`
}

// RelayProbe is what one place saw of one of a relay's addresses.
type RelayProbe struct {
	// From is the place: "control" for the control server, or the second
	// place's name ("hk-spare").
	From   string     `json:"from"`
	Family string     `json:"family"` // "ipv4", "ipv6"
	Addr   netip.Addr `json:"addr"`
	// TCP is how DERP's port answered a TLS handshake (RelayProbe*), UDP
	// how its STUN port answered a request.
	TCP string `json:"tcp"`
	UDP string `json:"udp,omitempty"`
}

// What a probe found (RelayProbe.TCP, .UDP).
const (
	RelayProbeOK      = "ok"      // it answered, with the relay's certificate for TCP
	RelayProbeRefused = "refused" // TCP: nothing listens (a reset)
	RelayProbeTimeout = "timeout" // no answer: dropped on the way
	// RelayProbeUnreachable: the network said there is no way to it.
	RelayProbeUnreachable = "unreachable"
	RelayProbeTLS         = "tls"  // TCP: something answers, not with TLS
	RelayProbePin         = "pin"  // TCP: TLS with a certificate not the relay's
	RelayProbeNone        = "none" // UDP: the relay has no STUN port
	// RelayProbeNoRoute: the place itself has no route to the address's
	// family (a place with no IPv6): it says nothing about the relay.
	RelayProbeNoRoute = "noroute"
	RelayProbeError   = "error" // anything else
)

// RelayDevices are what a relay's owner's devices online say of it.
type RelayDevices struct {
	// Reached are the devices connected to it, Failed those that tried
	// and could not, with why.
	Reached []DeviceID           `json:"reached"`
	Failed  []RelayDeviceFailure `json:"failed"`
}

// RelayDeviceFailure is a device that cannot reach a relay, and why
// (RelayFailure.Reason).
type RelayDeviceFailure struct {
	Device DeviceID `json:"device"`
	Reason string   `json:"reason"`
}

// RelayFailure is a relay on a device's lists that it tried and could not
// use (Report.RelayFailures).
type RelayFailure struct {
	Relay RelayID `json:"relay"`
	// Reason is why (RelayFail*).
	Reason string `json:"reason"`
	// Refused is why the relay turned it away (RelayDeny*), for
	// RelayFailRefused.
	Refused string `json:"refused,omitempty"`
	// Since is when it first failed, in this run of failures.
	Since time.Time `json:"since,omitzero"`
}

// Why a device could not use a relay (RelayFailure.Reason).
const (
	RelayFailConnect = "connect" // no TCP connection
	RelayFailTimeout = "timeout" // no answer in time
	RelayFailTLS     = "tls"     // the handshake failed, or the certificate is not the relay's
	RelayFailRefused = "refused" // the relay turned it away (Refused)
)
