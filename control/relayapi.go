package control

import (
	"cmp"
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// The relay API (proto/relay.go): a Deneb relay registers once with its
// owner's token, then asks whether each client may come in, and syncs
// every minute: who is still connected, and what each client sent and
// received.

func (s *Server) relayRoutes(m *http.ServeMux) {
	m.HandleFunc("POST "+proto.PathRelayRegister, s.handleRelayRegister)
	m.HandleFunc("POST "+proto.PathRelayAdmit, s.relay(s.handleRelayAdmit))
	m.HandleFunc("POST "+proto.PathRelaySync, s.relay(s.handleRelaySync))
	m.HandleFunc("GET "+proto.PathRelayWatch, s.relay(s.handleRelayWatch))
	m.HandleFunc("/relay/", func(w http.ResponseWriter, r *http.Request) {
		s.writeErr(w, r, errNotFound("没有这个接口"))
	})
}

// relay wraps a relay-API handler: it finds the relay by its secret.
func (s *Server) relay(h func(http.ResponseWriter, *http.Request, *proto.Relay, *World)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		st := s.state.Load()
		id, found := st.w.bySecret[hashSecret(strings.TrimSpace(secret))]
		if !ok || !found || st.w.relays[id] == nil {
			// Deleted, set up again elsewhere, or never registered.
			s.writeErr(w, r, newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "中转站的密钥不对：它被删除了，或者在别处重新登记了"))
			return
		}
		h(w, r, st.w.relays[id], st.w)
	}
}

// hostNameRE is a DNS name, for a relay's TLS name.
var hostNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// relayPlace is a relay's RelayInfo, checked and filled in: what goes in
// its row.
type relayPlace struct {
	host    string
	v4, v6  netip.Addr
	derp    uint16
	stun    uint16
	pin     string
	version string
	region  string // the relay's clue, for a new relay's name
}

// relayPlaceOf checks what a relay says of itself. With no address it takes the
// one the request came from: right for a relay with a public address of
// its own, which is the usual server.
func relayPlaceOf(r *http.Request, in proto.RelayInfo) (relayPlace, error) {
	p := relayPlace{v4: in.IPv4.Unmap(), v6: in.IPv6, derp: in.DERPPort, stun: in.STUNPort, version: in.Version, region: in.Region}
	if p.derp == 0 {
		return p, errInvalid("缺少 DERP 端口")
	}
	p.pin = strings.ToLower(strings.TrimSpace(in.Pin))
	if _, err := proto.PinnedTLSConfig(p.pin); err != nil {
		return p, errInvalid("证书指纹不对：" + in.Pin)
	}
	if p.v4.IsValid() && !p.v4.Is4() || p.v6.IsValid() && (!p.v6.Is6() || p.v6.Is4In6()) {
		return p, errInvalid("IPv4、IPv6 地址填反了")
	}
	if !p.v4.IsValid() && !p.v6.IsValid() {
		from, err := netip.ParseAddr(remoteIP(r))
		if err != nil {
			return p, errInvalid("看不出中转站的地址，请在启动参数里写明")
		}
		if from = from.Unmap(); from.Is4() {
			p.v4 = from
		} else {
			p.v6 = from
		}
	}
	p.host = strings.ToLower(strings.TrimSpace(in.HostName))
	if p.host == "" {
		p.host = cmp.Or(p.v4, p.v6).String()
	} else if _, err := netip.ParseAddr(p.host); err != nil && (len(p.host) > 253 || !hostNameRE.MatchString(p.host)) {
		return p, errInvalid("TLS 名字不对：" + in.HostName)
	}
	return p, nil
}

// same reports whether the relay's row says p already.
func (p relayPlace) same(rl *proto.Relay) bool {
	return rl.HostName == p.host && rl.IPv4 == p.v4 && rl.IPv6 == p.v6 && rl.DERPPort == p.derp && rl.STUNPort == p.stun && rl.Pin == p.pin
}

func (p relayPlace) String() string {
	return fmt.Sprintf("%s %v %v derp %d stun %d, %s, program %q", p.host, p.v4, p.v6, p.derp, p.stun, p.pin, p.version)
}

// write puts p in relay id's row.
func (p relayPlace) write(tx *sql.Tx, id proto.RelayID) error {
	v4, v6 := "", ""
	if p.v4.IsValid() {
		v4 = p.v4.String()
	}
	if p.v6.IsValid() {
		v6 = p.v6.String()
	}
	_, err := tx.Exec(`UPDATE relays SET host_name = ?, ipv4 = ?, ipv6 = ?, derp_port = ?, stun_port = ?, pin = ? WHERE id = ?`,
		p.host, v4, v6, p.derp, p.stun, p.pin, id)
	return err
}

func (s *Server) handleRelayRegister(w http.ResponseWriter, r *http.Request) {
	var req proto.RelayRegisterRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	place, err := relayPlaceOf(r, req.RelayInfo)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	tokenHash := hashSecret(strings.TrimSpace(req.Token))
	secret := newRelaySecret()
	now := s.now()
	var res proto.RelayRegisterResult
	_, err = s.change("", func(tx *sql.Tx, w *World) error {
		var j *relayJoin
		for _, jj := range w.joins {
			if jj.TokenHash == tokenHash {
				j = jj
			}
		}
		if j == nil {
			return newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "加入码不对，或者已经用过、撤销了")
		}
		if !j.live(now) {
			return newErr(http.StatusUnauthorized, proto.ErrUnauthorized, "加入码过期了（一小时内有效），请在管理端重新拿一个")
		}
		id := j.Relay
		if id == "" {
			// A new relay: its name is the place it says it is, unless
			// one was given; failing both, its address, which its owner
			// may rename.
			if j.Source != proto.RelayPublic && len(w.ownRelays(j.Owner)) >= maxRelays {
				return errForbidden(fmt.Sprintf("最多 %d 个中转站，先删掉一个不用的", maxRelays))
			}
			name := cmp.Or(j.Name, regionName(place.region), place.host)
			id = proto.RelayID(newID("r"))
			// A new public relay starts as a standby: a machine just set
			// up takes no share of everyone until an operator gives it one.
			weight := 1
			if j.Source == proto.RelayPublic {
				weight = newPublicRelayWeight
			}
			if _, err := tx.Exec(`INSERT INTO relays (id, name, source, owner, host_name, derp_port, rate_limit, created_at, weight)
					VALUES (?, ?, ?, ?, '', 0, ?, ?, ?)`, id, name, j.Source, j.Owner, j.RateLimit, ms(now), weight); err != nil {
				return err
			}
			res = proto.RelayRegisterResult{Relay: id, Name: name, Source: j.Source, RateLimit: j.RateLimit}
		} else {
			rl := w.relays[id]
			res = proto.RelayRegisterResult{Relay: id, Name: rl.Name, Source: rl.Source, RateLimit: rl.RateLimit}
		}
		if err := place.write(tx, id); err != nil {
			return err
		}
		// The code is used up, and a secret from an earlier registration
		// stops working: the relay was set up again.
		if _, err := tx.Exec(`UPDATE relays SET secret_hash = ?, registered_at = CASE WHEN registered_at = 0 THEN ? ELSE registered_at END WHERE id = ?`,
			hashSecret(secret), ms(now), id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM relay_joins WHERE id = ?`, j.ID); err != nil {
			return err
		}
		res.Secret = secret
		return nil
	})
	if err != nil {
		s.logf("relay register from %s refused: %v", remoteIP(r), err)
		s.writeErr(w, r, err)
		return
	}
	s.logf("relay %s (%s) registered from %s: %s", res.Relay, res.Name, remoteIP(r), place)
	s.relayCheckedIn(res.Relay)
	writeJSON(w, http.StatusOK, res)
}

// admission is whether relay id lets in the device with node key k, and
// if not, why (proto.RelayDeny*).
func (w *World) admission(id proto.RelayID, k proto.NodePublic) (bool, string) {
	if w.admits(id, k) {
		return true, ""
	}
	d := w.byNodeKey[k]
	switch {
	case d == nil || !d.usable():
		return false, proto.RelayDenyLoggedOut
	case w.kicked(id, d):
		return false, proto.RelayDenyKicked
	}
	return false, proto.RelayDenyNotListed
}

func (s *Server) handleRelayAdmit(w http.ResponseWriter, r *http.Request, rl *proto.Relay, wd *World) {
	var req proto.RelayAdmitRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	allow, reason := wd.admission(rl.ID, req.NodePublic)
	if !allow {
		// Who it was, for the log: a device logged out or run out is out
		// of byNodeKey, so look further.
		who := "an unknown node key"
		for _, d := range wd.devices {
			if d.NodeKey == req.NodePublic {
				who = "device " + string(d.ID)
				if !d.usable() {
					who += " (logged out or run out)"
				}
				break
			}
		}
		s.logf("relay %s (%s) turns away %s from %v: %s", rl.ID, rl.Name, who, req.Source, reason)
	}
	writeJSON(w, http.StatusOK, proto.RelayAdmitResult{Allow: allow, Reason: reason})
}

// maxSyncClients bounds a sync; a relay past it is a bigger deployment
// than M4 plans for.
const maxSyncClients = 10000

func (s *Server) handleRelaySync(w http.ResponseWriter, r *http.Request, rl *proto.Relay, wd *World) {
	var req proto.RelaySyncRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if len(req.Connected) > maxSyncClients || len(req.Usage) > maxSyncClients {
		s.writeErr(w, r, errInvalid("一次同步的客户端太多"))
		return
	}
	if req.Info != nil {
		// Just started: where it is now, if that changed.
		place, err := relayPlaceOf(r, *req.Info)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if !place.same(rl) {
			v, err := s.change("", func(tx *sql.Tx, w *World) error {
				if w.relays[rl.ID] == nil {
					return errNotFound("中转站已经删除了")
				}
				return place.write(tx, rl.ID)
			})
			if err != nil {
				s.writeErr(w, r, err)
				return
			}
			s.logf("relay %s (%s) moved: %s; version %d", rl.ID, rl.Name, place, v)
			st := s.state.Load()
			rl, wd = st.w.relays[rl.ID], st.w
		}
	}
	res := proto.RelaySyncResult{Deny: []proto.NodePublic{}, Name: rl.Name, RateLimit: rl.RateLimit}
	for _, k := range req.Connected {
		if !wd.admits(rl.ID, k) {
			res.Deny = append(res.Deny, k)
		}
	}
	if len(res.Deny) > 0 {
		s.logf("relay %s (%s): %d of %d clients may no longer use it", rl.ID, rl.Name, len(res.Deny), len(req.Connected))
	}
	if err := s.recordUsage(rl.ID, req.Usage, wd); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.Health != nil {
		s.relayReported(rl, wd, req)
	}
	s.relayCheckedIn(rl.ID)
	writeJSON(w, http.StatusOK, res)
}

// relayReported keeps a sync's load; when the relay turns full, or no
// longer is, the lists change and a new version says so.
func (s *Server) relayReported(rl *proto.Relay, wd *World, req proto.RelaySyncRequest) {
	h := *req.Health
	h.CPU = min(max(h.CPU, 0), 1)
	ld := &relayLoad{at: s.now(), health: h, connected: slices.Clone(req.Connected)}
	ld.devices, ld.people = wd.loadOf(req.Connected)
	capacity := 0
	if m := wd.relayMeta[rl.ID]; m != nil {
		capacity = m.Capacity
	}
	full, changed := s.relayLive.report(rl.ID, ld, capacity)
	if !changed {
		return
	}
	v, err := s.change("", func(*sql.Tx, *World) error { return nil })
	if err != nil {
		s.logf("relay %s full=%v: %v", rl.ID, full, err)
		return
	}
	s.logf("relay %s (%s) full=%v: %d devices (capacity %d), CPU %.0f%%; version %d", rl.ID, rl.Name, full, ld.devices, capacity, h.CPU*100, v)
}

// relayCheckedIn notes that a relay synced; if it just came online, its
// owner's view says so in a new version.
func (s *Server) relayCheckedIn(id proto.RelayID) {
	if !s.relayLive.checkIn(id, s.now()) {
		return
	}
	v, err := s.change("", func(*sql.Tx, *World) error { return nil })
	if err != nil {
		s.logf("relay %s online: %v", id, err)
		return
	}
	s.logf("relay %s online; version %d", id, v)
}

// maxUsageBytes bounds one client's bytes in one sync: a minute at 100
// Gbit/s, so a broken relay cannot write nonsense.
const maxUsageBytes = 1 << 40

// recordUsage adds a sync's bytes to today's rows, per device. The
// relay counts per node key; a key no device has is dropped.
func (s *Server) recordUsage(relay proto.RelayID, usage []proto.RelayClientUsage, w *World) error {
	if len(usage) == 0 {
		return nil
	}
	var byKey map[proto.NodePublic]*device
	day := s.now().UTC().Unix() / 86400
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dropped := 0
	for _, u := range usage {
		if u.Sent < 0 || u.Received < 0 || u.Sent > maxUsageBytes || u.Received > maxUsageBytes {
			return errInvalid("用量不对")
		}
		if u.Sent == 0 && u.Received == 0 {
			continue
		}
		d := w.byNodeKey[u.Node]
		if d == nil {
			// It logged out or ran out since: still its bytes.
			if byKey == nil {
				byKey = map[proto.NodePublic]*device{}
				for _, dd := range w.devices {
					byKey[dd.NodeKey] = dd
				}
			}
			d = byKey[u.Node]
		}
		if d == nil {
			dropped++
			continue
		}
		if _, err := tx.Exec(`INSERT INTO relay_usage (relay, device, day, sent, received) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (relay, device, day) DO UPDATE SET sent = sent + excluded.sent, received = received + excluded.received`,
			relay, d.ID, day, u.Sent, u.Received); err != nil {
			return err
		}
	}
	if dropped > 0 {
		s.logf("relay %s: usage of %d unknown node keys dropped", relay, dropped)
	}
	return tx.Commit()
}

// handleRelayUsage answers what the asking person's relays carried this
// month, for their cards. It is asked for when a card opens, not pushed:
// it changes all the time and matters only then.
func (s *Server) handleRelayUsage(w http.ResponseWriter, r *http.Request, c *caller) {
	u, err := s.relayUsage(c.st.w, c.dev.Owner, s.now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type deviceBytes struct{ sent, received int64 }

func (s *Server) relayUsage(w *World, me proto.PersonID, now time.Time) (*proto.RelayUsage, error) {
	now = now.UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := &proto.RelayUsage{Month: now.Format("2006-01"), Relays: []proto.RelayMonth{}}
	mine := w.ownRelays(me)
	if len(mine) == 0 {
		return out, nil
	}
	used, err := s.usageSince(first, mine)
	if err != nil {
		return nil, err
	}
	for _, id := range mine {
		m := w.relayMonth(me, id, used[id])
		s.relayNow(w, me, id, &m, now)
		out.Relays = append(out.Relays, m)
	}
	return out, nil
}

// usageSince sums what each device sent and received through each of
// relays since the day first falls on.
func (s *Server) usageSince(first time.Time, relays []proto.RelayID) (map[proto.RelayID]map[proto.DeviceID]deviceBytes, error) {
	used := map[proto.RelayID]map[proto.DeviceID]deviceBytes{}
	args := []any{first.Unix() / 86400}
	marks := make([]string, len(relays))
	for i, id := range relays {
		marks[i] = "?"
		args = append(args, id)
		used[id] = map[proto.DeviceID]deviceBytes{}
	}
	if len(relays) == 0 {
		return used, nil
	}
	rows, err := s.db.Query(`SELECT relay, device, SUM(sent), SUM(received) FROM relay_usage
		WHERE day >= ? AND relay IN (`+strings.Join(marks, ", ")+`) GROUP BY relay, device`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rl proto.RelayID
		var d proto.DeviceID
		var b deviceBytes
		if err := rows.Scan(&rl, &d, &b.sent, &b.received); err != nil {
			return nil, err
		}
		used[rl][d] = b
	}
	return used, rows.Err()
}

// loadView is a relay's last sync as a card shows it.
func (s *Server) loadView(id proto.RelayID, ld *relayLoad) *proto.RelayLoad {
	return &proto.RelayLoad{
		At: ld.at, Devices: ld.devices, People: ld.people, Connections: ld.health.Connections,
		BytesPerSec: ld.health.BytesPerSec, CPU: ld.health.CPU, Memory: ld.health.Memory,
		MemoryLimit: ld.health.MemoryLimit, Uptime: ld.health.Uptime, Full: s.relayLive.isFull(id),
	}
}

// relayNow adds to a relay's card how it is doing now: its last sync's
// load, who is on it, and its kicks.
func (s *Server) relayNow(w *World, me proto.PersonID, id proto.RelayID, m *proto.RelayMonth, now time.Time) {
	m.Kicks = w.kicksOf(id, now.AddDate(0, 0, -30))
	ld := s.relayLive.lastLoad(id)
	if ld == nil {
		return
	}
	m.Load = s.loadView(id, ld)
	// Who: the devices its owner sees (theirs, and those of the LANs they
	// are in); for the others, a share's receiver's, the person only.
	visible := func(d *device) bool {
		if d.Owner == me {
			return true
		}
		for l := range d.lans {
			if ll := w.lans[l]; ll != nil && ll.joined(me) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	for _, k := range ld.connected {
		d := w.byNodeKey[k]
		if d == nil {
			continue
		}
		c := proto.RelayClient{Person: d.Owner}
		if visible(d) {
			c.Device = d.ID
		}
		if key := string(c.Person) + "/" + string(c.Device); !seen[key] {
			seen[key] = true
			m.Connected = append(m.Connected, c)
		}
	}
	slices.SortFunc(m.Connected, func(a, b proto.RelayClient) int {
		if c := cmp.Compare(a.Person, b.Person); c != 0 {
			return c
		}
		return cmp.Compare(a.Device, b.Device)
	})
}

// relayMonth sums one relay's month by LAN, share and device, as its
// owner may see them.
func (w *World) relayMonth(me proto.PersonID, id proto.RelayID, used map[proto.DeviceID]deviceBytes) proto.RelayMonth {
	m := proto.RelayMonth{Relay: id, LANs: []proto.UsageLine{}, Shares: []proto.UsageLine{}, Devices: []proto.UsageLine{}}
	for _, b := range used {
		m.Bytes += b.sent
	}
	seen := map[proto.DeviceID]bool{}
	for _, l := range w.lans {
		if !l.joined(me) || !slices.Contains(l.relays, id) {
			continue
		}
		line := proto.UsageLine{LAN: l.ID}
		for _, ld := range l.devices {
			line.Bytes += used[ld.Device].sent
			seen[ld.Device] = true
		}
		m.LANs = append(m.LANs, line)
	}
	for _, sh := range w.shares {
		if sh.State != shareActive || sh.Sharer != me || !slices.Contains(w.shareRelays(sh), id) {
			continue
		}
		line := proto.UsageLine{Share: proto.ShareID(sh.ID)}
		for did, b := range used {
			if d := w.devices[did]; d != nil && d.Owner == sh.Receiver {
				line.Bytes += b.sent + b.received
			}
		}
		m.Shares = append(m.Shares, line)
	}
	for did, b := range used {
		if d := w.devices[did]; d != nil && (seen[did] || d.Owner == me) && b.sent+b.received > 0 {
			m.Devices = append(m.Devices, proto.UsageLine{Device: did, Bytes: b.sent + b.received})
		}
	}
	slices.SortFunc(m.LANs, func(a, b proto.UsageLine) int { return cmp.Compare(a.LAN, b.LAN) })
	slices.SortFunc(m.Shares, func(a, b proto.UsageLine) int { return cmp.Compare(a.Share, b.Share) })
	slices.SortFunc(m.Devices, func(a, b proto.UsageLine) int { return cmp.Compare(a.Device, b.Device) })
	return m
}
