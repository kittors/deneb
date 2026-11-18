package main

// The STUN servers on the public relays' page (0.1.11, FeatureSTUN;
// design/plan/stun-servers-20261118.md §4), as the control server keeps them
// (control/stun.go): a list in order, each with what its last lookup found.
// The demo's lookups take a moment, and a name with "nowhere" in it is not
// found. POST /api/v1/_mock/stun {"scene": …} puts one of stunScenes in
// place, for the recordings: none, one, many, long names; with "hold", a
// lookup waits until POST /api/v1/_mock/stun-go (or 5 seconds), so a
// recording says when it ends, on its own clock.

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// mstun is one STUN server.
type mstun struct {
	host     string
	port     uint16
	v4, v6   netip.Addr
	added    time.Time
	lookedUp time.Time
	err      string // what the last lookup said; "" when it found an address
}

func (s *mstun) key() string { return net.JoinHostPort(s.host, strconv.Itoa(int(s.port))) }

// stunLookupDelay is how long the demo's lookup of a name being added takes.
const stunLookupDelay = 900 * time.Millisecond

// stunGate, when a recording set "hold", is where a lookup waits for
// POST /api/v1/_mock/stun-go; nil, it takes stunLookupDelay.
var (
	stunGateMu sync.Mutex
	stunGate   chan struct{}
)

func gate() chan struct{} {
	stunGateMu.Lock()
	defer stunGateMu.Unlock()
	return stunGate
}

// stunWait is the lookup's wait, before the lock.
func stunWait() {
	if g := gate(); g != nil {
		select {
		case <-g:
		case <-time.After(5 * time.Second):
		}
		return
	}
	time.Sleep(stunLookupDelay)
}

// maxSTUN is the control server's limit (control/stun.go).
const maxSTUN = 8

// stunScene is one of the lists the recordings show, as at now.
func stunScene(name string, now time.Time) []*mstun {
	ok := func(host string, port uint16, v4, v6 string, ago time.Duration) *mstun {
		s := &mstun{host: host, port: port, added: now.Add(-30 * 24 * time.Hour), lookedUp: now.Add(-ago)}
		if v4 != "" {
			s.v4 = netip.MustParseAddr(v4)
		}
		if v6 != "" {
			s.v6 = netip.MustParseAddr(v6)
		}
		return s
	}
	cf := func() *mstun { return ok("stun.cloudflare.com", 3478, "198.51.100.10", "2001:db8::10", 6*time.Minute) }
	switch name {
	case "empty":
		return nil
	case "many":
		failed := ok("stun.example.org", 3478, "198.51.100.30", "", 3*time.Minute)
		failed.err = "lookup stun.example.org: i/o timeout"
		never := &mstun{host: "stun.example.edu", port: 3478, added: now.Add(-2 * time.Hour), lookedUp: now.Add(-4 * time.Minute), err: "lookup stun.example.edu: no such host"}
		return []*mstun{
			cf(),
			ok("stun.example.com", 3478, "198.51.100.20", "2001:db8::20", 6*time.Minute),
			ok("stun.example.net", 19302, "198.51.100.21", "", 6*time.Minute),
			failed,
			never,
			ok("203.0.113.9", 3478, "203.0.113.9", "", 6*time.Minute),
			ok("stun-hongkong-backup-01.relay-service.example.com", 3478, "198.51.100.40", "2001:db8::40", 6*time.Minute),
			ok("stun.example.info", 3478, "198.51.100.50", "", 6*time.Minute),
		}
	case "long":
		return []*mstun{
			cf(),
			ok("stun-hongkong-backup-01.relay-service.example.com", 3478, "198.51.100.40", "2001:db8::40", 6*time.Minute),
			ok("stun-singapore-02.very-long-subdomain-for-testing.example.net", 19302, "198.51.100.41", "", 6*time.Minute),
		}
	default: // "one": as a deployment starts
		return []*mstun{cf()}
	}
}

// stunView is the list as the page gets it.
func (m *mock) stunView() []proto.OperatorSTUN {
	out := []proto.OperatorSTUN{}
	for _, s := range m.stun {
		out = append(out, proto.OperatorSTUN{Server: s.key(), HostName: s.host, Port: s.port, IPv4: s.v4, IPv6: s.v6,
			AddedAt: s.added, LookedUp: s.lookedUp, LookupError: s.err})
	}
	return out
}

// parseSTUN reads "host" or "host:port" as the control server does.
func parseSTUN(spec string) (string, uint16, *opError) {
	bad := fail(400, proto.ErrInvalid, "写成主机名，或者主机名:端口，比如 stun.cloudflare.com:3478")
	spec = strings.TrimSpace(spec)
	host, port := spec, ""
	if h, p, err := net.SplitHostPort(spec); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(spec, "[") && strings.HasSuffix(spec, "]") {
		host = spec[1 : len(spec)-1]
	}
	n := uint64(3478)
	if port != "" {
		var err error
		if n, err = strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return "", 0, bad
		}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() {
			return "", 0, bad
		}
		return a.Unmap().String(), uint16(n), nil
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	labels := strings.Split(host, ".")
	if len(host) > 253 || len(labels) < 2 {
		return "", 0, bad
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", 0, bad
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", 0, bad
			}
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", 0, bad
	}
	return host, uint16(n), nil
}

// stunOp does stun.add and stun.remove, under the lock (the lookup's wait
// is before it, in ops).
func (m *mock) stunOp(me proto.PersonID, op proto.OpType, a proto.OpArgs) *opError {
	if me != opsOperator {
		return fail(403, proto.ErrForbidden, "只有部署管理员能改")
	}
	host, port, e := parseSTUN(a.STUN)
	if e != nil {
		return e
	}
	key := net.JoinHostPort(host, strconv.Itoa(int(port)))
	i := slices.IndexFunc(m.stun, func(s *mstun) bool { return s.key() == key })
	switch op {
	case proto.OpSTUNAdd:
		if i >= 0 {
			return fail(409, proto.ErrConflict, "已经有 "+key+" 了")
		}
		if len(m.stun) >= maxSTUN {
			return fail(409, proto.ErrConflict, "最多 8 个，先去掉一个")
		}
		if strings.Contains(host, "nowhere") {
			return fail(400, proto.ErrInvalid, "查不到 "+host+" 的地址，没有加")
		}
		now := m.now()
		s := &mstun{host: host, port: port, added: now, lookedUp: now}
		if a, err := netip.ParseAddr(host); err == nil {
			s.v4 = a
		} else {
			// A made-up address of the documentation's, by the name.
			h := 0
			for _, c := range host {
				h = (h*31 + int(c)) & 0xff
			}
			s.v4 = netip.AddrFrom4([4]byte{198, 51, 100, byte(60 + h%120)})
		}
		m.stun = append(m.stun, s)
	case proto.OpSTUNRemove:
		if i < 0 {
			return fail(404, proto.ErrNotFound, "没有 "+key)
		}
		m.stun = slices.Delete(m.stun, i, i+1)
	}
	m.bump()
	return nil
}

// stunSceneHandler puts one of the scenes in place (a demo's stand-in).
func (m *mock) stunSceneHandler(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	var req struct {
		Scene string `json:"scene"`
		Hold  bool   `json:"hold"`
	}
	if !decode(w, r, &req) {
		return
	}
	stunGateMu.Lock()
	stunGate = nil
	if req.Hold {
		stunGate = make(chan struct{}, 1)
	}
	stunGateMu.Unlock()
	m.mu.Lock()
	m.stun = stunScene(req.Scene, m.now())
	m.bump()
	m.mu.Unlock()
	writeJSON(w, struct{}{})
}

// stunGo lets the lookup waiting for it go on.
func (m *mock) stunGo(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
	if g := gate(); g != nil {
		select {
		case g <- struct{}{}:
		default:
		}
	}
	writeJSON(w, struct{}{})
}
