package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// The STUN servers (0.1.11, proto.STUNServer). A relay's STUN tells a
// device the address and port it is seen from, which peers then punch to.
// Where the relay's address is blocked (hk-155's, from home lines in China,
// since 11-16) nothing answers, and peers stay on the relay; a STUN service
// elsewhere answers instead. Cloudflare's is the first (migration 15); the
// operators add and remove others (deneb-control stun). Devices ask every
// one as well as the relays, and use what answers.
//
// The names are looked up here, where the server runs, and the network
// map carries what was found: a device's own DNS may be a proxy's fake-ip
// mode. A lookup runs at start, every stunLookupEvery after, and when one
// is added. An address stays while the lookup still finds it, so a name
// that gives its addresses in another order each time does not send every
// device a new map; one that is gone gives way to the least of the new
// ones. A lookup that fails keeps what there was.

const (
	// stunLookupEvery is how often the names are looked up again.
	stunLookupEvery = 10 * time.Minute
	// stunLookupTimeout bounds one name's lookup.
	stunLookupTimeout = 10 * time.Second
	// DefaultSTUNPort is STUN's own port (RFC 5389), for a server given
	// without one.
	DefaultSTUNPort = 3478
	// maxSTUN is how many there may be: each is a request from every
	// device every 20 seconds.
	maxSTUN = 8
)

// stunServer is one of the deployment's STUN servers.
type stunServer struct {
	proto.STUNServer
	AddedAt time.Time // zero for the one the migration put in
}

// stunLooks is how each name's last lookup went, by "host:port": in memory
// only, since the first lookup after a start says it again.
type stunLooks struct {
	mu sync.Mutex
	m  map[string]stunLook
}

type stunLook struct {
	at  time.Time
	err string // "" when it found an address
}

func (l *stunLooks) set(key string, at time.Time, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]stunLook{}
	}
	e := ""
	if err != nil {
		e = err.Error()
	}
	l.m[key] = stunLook{at: at, err: e}
}

func (l *stunLooks) get(key string) stunLook {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m[key]
}

// parseSTUN reads a STUN server as an operator writes it: a host name or an
// address, with the port after a colon unless it is 3478
// ("stun.cloudflare.com", "stun.example.com:19302", "[2001:db8::1]:3478").
func parseSTUN(spec string) (string, uint16, error) {
	bad := errInvalid(pangu("写成主机名，或者主机名:端口，比如stun.cloudflare.com:3478"))
	spec = strings.TrimSpace(spec)
	host, port := spec, ""
	if h, p, err := net.SplitHostPort(spec); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(spec, "[") && strings.HasSuffix(spec, "]") {
		host = spec[1 : len(spec)-1]
	}
	n := uint64(DefaultSTUNPort)
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
	if !isHostName(host) {
		return "", 0, bad
	}
	return host, uint16(n), nil
}

// isHostName reports whether s is a DNS name of two labels or more, each of
// letters, digits and hyphens, at most 63 long and not starting or ending
// with a hyphen, the last not all digits.
func isHostName(s string) bool {
	labels := strings.Split(s, ".")
	if len(s) > 253 || len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

// lookupSTUN finds host's addresses for a server that had was: the ones of
// was the lookup still gave, or else the least IPv4 and IPv6 it gave. A
// server given by its address is at that address.
func (s *Server) lookupSTUN(ctx context.Context, host string, was proto.STUNServer) (v4, v6 netip.Addr, err error) {
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Is4() {
			return a, netip.Addr{}, nil
		}
		return netip.Addr{}, a, nil
	}
	if s.cfg.LookupIP == nil {
		return v4, v6, errors.New("this server looks no names up")
	}
	ctx, cancel := context.WithTimeout(ctx, stunLookupTimeout)
	defer cancel()
	addrs, err := s.cfg.LookupIP(ctx, "ip", host)
	if err != nil {
		return v4, v6, err
	}
	var all4, all6 []netip.Addr
	for _, a := range addrs {
		a = a.Unmap().WithZone("")
		switch {
		case !a.IsValid() || a.IsUnspecified() || a.IsMulticast():
		case a.Is4():
			all4 = append(all4, a)
		default:
			all6 = append(all6, a)
		}
	}
	if len(all4)+len(all6) == 0 {
		return v4, v6, fmt.Errorf("%s has no address", host)
	}
	return keepAddr(was.IPv4, all4), keepAddr(was.IPv6, all6), nil
}

// keepAddr is was if got has it, or else the least of got.
func keepAddr(was netip.Addr, got []netip.Addr) netip.Addr {
	if len(got) == 0 {
		return netip.Addr{}
	}
	if slices.Contains(got, was) {
		return was
	}
	return slices.MinFunc(got, netip.Addr.Compare)
}

// stunAddrs says where a server is, for the log.
func stunAddrs(sv proto.STUNServer) string {
	var as []string
	for _, a := range []netip.Addr{sv.IPv4, sv.IPv6} {
		if a.IsValid() {
			as = append(as, a.String())
		}
	}
	if len(as) == 0 {
		return "no address"
	}
	return strings.Join(as, " and ")
}

// findSTUN is where host:port is on w's list, or -1.
func findSTUN(w *World, host string, port uint16) int {
	return slices.IndexFunc(w.stun, func(s stunServer) bool { return s.HostName == host && s.Port == port })
}

// RunSTUN looks the STUN servers' names up, now and every stunLookupEvery,
// until ctx ends.
func (s *Server) RunSTUN(ctx context.Context) {
	t := time.NewTicker(stunLookupEvery)
	defer t.Stop()
	for {
		s.refreshSTUN(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshSTUN looks every server up once and writes the addresses that
// changed, all in one change: the devices get one new map.
func (s *Server) refreshSTUN(ctx context.Context) {
	var found []proto.STUNServer
	for _, sv := range s.state.Load().w.stun {
		if ctx.Err() != nil {
			return
		}
		v4, v6, err := s.lookupSTUN(ctx, sv.HostName, sv.STUNServer)
		s.stunLooks.set(sv.String(), s.now(), err)
		if err != nil {
			s.logf("stun: looking %s up: %v; it stays at %s", sv, err, stunAddrs(sv.STUNServer))
			continue
		}
		if v4 != sv.IPv4 || v6 != sv.IPv6 {
			found = append(found, proto.STUNServer{HostName: sv.HostName, Port: sv.Port, IPv4: v4, IPv6: v6})
		}
	}
	if len(found) == 0 {
		return
	}
	var moved []string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		for _, f := range found {
			i := findSTUN(w, f.HostName, f.Port)
			if i < 0 { // removed while it was looked up
				continue
			}
			was := w.stun[i].STUNServer
			if was.IPv4 == f.IPv4 && was.IPv6 == f.IPv6 {
				continue
			}
			if _, err := tx.Exec(`UPDATE stun_servers SET ipv4 = ?, ipv6 = ? WHERE host = ? AND port = ?`,
				addrText(f.IPv4), addrText(f.IPv6), f.HostName, f.Port); err != nil {
				return err
			}
			moved = append(moved, fmt.Sprintf("%s is at %s now, was %s", f, stunAddrs(f), stunAddrs(was)))
		}
		if len(moved) == 0 {
			return errNoChange
		}
		return nil
	})
	if err != nil {
		s.logf("stun: writing the addresses found: %v", err)
		return
	}
	for _, m := range moved {
		s.logf("stun: %s", m)
	}
}

// opsAddSTUN puts a STUN server at the end of the list. Its name is looked
// up first, and one that cannot be is not added: what is on the list is
// what the devices ask. by nil is the admin socket.
func (s *Server) opsAddSTUN(by *person, spec string) (proto.STUNServer, error) {
	host, port, err := parseSTUN(spec)
	if err != nil {
		return proto.STUNServer{}, err
	}
	sv := proto.STUNServer{HostName: host, Port: port}
	there := func(w *World) error {
		if findSTUN(w, host, port) >= 0 {
			return errConflict(pangu("已经有" + sv.String() + "了"))
		}
		if len(w.stun) >= maxSTUN {
			return errConflict(pangu(fmt.Sprintf("最多%d个，先去掉一个", maxSTUN)))
		}
		return nil
	}
	if err := there(s.state.Load().w); err != nil {
		return sv, err
	}
	v4, v6, err := s.lookupSTUN(context.Background(), host, sv)
	s.stunLooks.set(sv.String(), s.now(), err)
	if err != nil {
		return sv, errInvalid(pangu(fmt.Sprintf("查不到%s的地址，没有加（%v）", host, err)))
	}
	sv.IPv4, sv.IPv6 = v4, v6
	actor := actorOf(by)
	text := pangu("加了查外网地址的服务" + sv.String())
	_, err = s.change("", func(tx *sql.Tx, w *World) error {
		if err := there(w); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO stun_servers (host, port, pos, ipv4, ipv6, added_at)
			VALUES (?, ?, (SELECT COALESCE(MAX(pos), -1) + 1 FROM stun_servers), ?, ?, ?)`,
			host, port, addrText(v4), addrText(v6), ms(s.now())); err != nil {
			return err
		}
		return recordOps(tx, s.now(), actor, "stun.add", sv.String(), text, "")
	})
	if err != nil {
		return sv, err
	}
	s.logOps(actor, "stun.add", text)
	return sv, nil
}

// opsRemoveSTUN takes a STUN server off the list: the devices stop asking
// it with their next map. by nil is the admin socket.
func (s *Server) opsRemoveSTUN(by *person, spec string) error {
	host, port, err := parseSTUN(spec)
	if err != nil {
		return err
	}
	key := proto.STUNServer{HostName: host, Port: port}.String()
	actor := actorOf(by)
	text := pangu("去掉了查外网地址的服务" + key)
	_, err = s.change("", func(tx *sql.Tx, w *World) error {
		if findSTUN(w, host, port) < 0 {
			return errNotFound(pangu("没有" + key))
		}
		if _, err := tx.Exec(`DELETE FROM stun_servers WHERE host = ? AND port = ?`, host, port); err != nil {
			return err
		}
		return recordOps(tx, s.now(), actor, "stun.remove", key, text, "")
	})
	if err != nil {
		return err
	}
	s.logOps(actor, "stun.remove", text)
	return nil
}

// AdminSTUN is one STUN server, as deneb-control stun lists it.
type AdminSTUN struct {
	Server  string     `json:"server"` // host:port
	IPv4    netip.Addr `json:"ipv4,omitzero"`
	IPv6    netip.Addr `json:"ipv6,omitzero"`
	AddedAt time.Time  `json:"addedAt,omitzero"`
	// LookedUp is when its name was last looked up, zero before the first
	// since the server started; Error is what that lookup said, "" when it
	// found an address.
	LookedUp time.Time `json:"lookedUp,omitzero"`
	Error    string    `json:"error,omitempty"`
}

// AdminSTUNRequest adds a STUN server, "host" or "host:port", or removes
// one.
type AdminSTUNRequest struct {
	Add    string `json:"add,omitempty"`
	Remove string `json:"remove,omitempty"`
}

func (s *Server) adminSTUNRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /stun", s.handleAdminSTUN)
	m.HandleFunc("POST /stun", s.handleAdminSetSTUN)
}

func (s *Server) adminSTUN() []AdminSTUN {
	out := []AdminSTUN{}
	for _, sv := range s.state.Load().w.stun {
		l := s.stunLooks.get(sv.String())
		out = append(out, AdminSTUN{Server: sv.String(), IPv4: sv.IPv4, IPv6: sv.IPv6, AddedAt: sv.AddedAt, LookedUp: l.at, Error: l.err})
	}
	return out
}

func (s *Server) handleAdminSTUN(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.adminSTUN())
}

// handleAdminSetSTUN adds or removes a STUN server on the running server,
// as the public relays' page would, with a line of the record; the
// devices get it with their next map, at once.
func (s *Server) handleAdminSetSTUN(w http.ResponseWriter, r *http.Request) {
	var req AdminSTUNRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var err error
	switch {
	case (req.Add == "") == (req.Remove == ""):
		err = errInvalid("加一个，或者去掉一个")
	case req.Add != "":
		_, err = s.opsAddSTUN(nil, req.Add)
	default:
		err = s.opsRemoveSTUN(nil, req.Remove)
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.adminSTUN())
}

// AdminSTUNServers lists the STUN servers, through the admin socket at
// path.
func AdminSTUNServers(path string) ([]AdminSTUN, error) {
	var out []AdminSTUN
	return out, adminCall(path, "GET", "/stun", nil, &out)
}

// AdminSetSTUN adds or removes a STUN server, through the admin socket at
// path, and lists them after.
func AdminSetSTUN(path string, req AdminSTUNRequest) ([]AdminSTUN, error) {
	var out []AdminSTUN
	return out, adminCall(path, "POST", "/stun", req, &out)
}
