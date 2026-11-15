package proto

import "net/netip"

// The prober API (M7, design/plan/m7/relay-install-api.md §5): a second
// place that checks relays for the control server, so a relay is not called
// unreachable for a trouble on the control server's own route. The watchdog
// on another machine is one (cmd/deneb-watchdog, "prober" in its file). It
// asks for work, with the deployment's probe secret as
// "Authorization: Bearer <secret>", and long polling, and posts back what
// it saw.
const (
	PathProbeNext = "/probe/v1/next" // POST ProbeNextRequest → ProbeJobs, within 25 s
	PathProbeDone = "/probe/v1/done" // POST ProbeDone → {}
)

// ProbeNextRequest asks for work.
type ProbeNextRequest struct {
	// Name is the place, as cards and the log say it: "hk-spare".
	Name string `json:"name"`
}

// ProbeJobs are the work waiting, none when the wait ran out.
type ProbeJobs struct {
	Jobs []ProbeJob `json:"jobs"`
}

// ProbeJob is one relay's addresses to check.
type ProbeJob struct {
	ID      string        `json:"id"`
	Targets []ProbeTarget `json:"targets"`
}

// ProbeTarget is one address of a relay to check: a TLS handshake with
// DERPPort, which must show Pin, and a STUN request to STUNPort.
type ProbeTarget struct {
	Family   string     `json:"family"` // "ipv4", "ipv6"
	Addr     netip.Addr `json:"addr"`
	DERPPort uint16     `json:"derpPort"`
	STUNPort uint16     `json:"stunPort,omitempty"`
	Pin      string     `json:"pin"`
	// HostName is the relay's TLS name, sent as SNI when it is a name.
	HostName string `json:"hostName,omitempty"`
}

// ProbeDone is a job's results, From left to the server, which knows who
// asked.
type ProbeDone struct {
	ID      string       `json:"id"`
	Results []RelayProbe `json:"results"`
}
