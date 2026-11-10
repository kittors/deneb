package control

import (
	"cmp"
	"database/sql"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/kittors/deneb/proto"
)

// Relays through the admin socket (M4). The public relays are ours, so
// the operator makes them here and sets their rate limit; people make
// their own with relay.create.

// AdminRelay is one relay as the operator sees it.
type AdminRelay struct {
	ID         proto.RelayID     `json:"id"`
	Name       string            `json:"name"`
	Source     proto.RelaySource `json:"source"`
	Owner      string            `json:"owner,omitempty"` // the owner's email
	HostName   string            `json:"hostName,omitempty"`
	IPv4       netip.Addr        `json:"ipv4,omitzero"`
	IPv6       netip.Addr        `json:"ipv6,omitzero"`
	DERPPort   uint16            `json:"derpPort,omitempty"`
	STUNPort   uint16            `json:"stunPort,omitempty"`
	Pin        string            `json:"pin,omitempty"`
	RateLimit  int64             `json:"rateLimit,omitempty"` // bytes a second per connection
	Default    bool              `json:"default,omitempty"`
	Seeded     bool              `json:"seeded,omitempty"`
	Registered bool              `json:"registered"`
	Online     bool              `json:"online"`
	LastSeen   time.Time         `json:"lastSeen,omitzero"`
	LANs       int               `json:"lans"` // dragged into
	Weight     int               `json:"weight"`
	Capacity   int               `json:"capacity,omitempty"`
	Kicks      int               `json:"kicks,omitempty"` // in force
	// Load is its last sync's, if it synced since the server started.
	Load *proto.RelayLoad `json:"load,omitempty"`
}

// AdminRelayKickRequest kicks a person (by email) or a device off a relay,
// or (Kick) lifts a kick.
type AdminRelayKickRequest struct {
	Relay  proto.RelayID  `json:"relay,omitempty"`
	Email  string         `json:"email,omitempty"`
	Device proto.DeviceID `json:"device,omitempty"`
	Kick   string         `json:"kick,omitempty"`
}

// AdminRelaySetRequest sets a relay's weight or capacity.
type AdminRelaySetRequest struct {
	Relay    proto.RelayID `json:"relay"`
	Weight   *int          `json:"weight,omitempty"`
	Capacity *int          `json:"capacity,omitempty"`
}

// AdminRelayAddRequest makes a join code for a public relay: the relay
// comes into being when the code is used.
type AdminRelayAddRequest struct {
	Name      string `json:"name,omitempty"` // empty: the region it reports
	RateLimit int64  `json:"rateLimit"`      // bytes a second per connection; zero is none
}

// AdminRelayTokenRequest asks for a new registration token for a relay.
type AdminRelayTokenRequest struct {
	Relay proto.RelayID `json:"relay"`
}

// AdminRelayToken is a join code for the relay program, shown once: the
// one-line install command that carries it, and the bare token for
// deneb-relay -token.
type AdminRelayToken struct {
	Relay   proto.RelayID `json:"relay,omitempty"` // the relay it sets up again; empty for a new one
	Name    string        `json:"name,omitempty"`
	Token   string        `json:"token"`
	Code    string        `json:"code"`
	Command string        `json:"command"`
	Expires time.Time     `json:"expires"`
}

// AdminRelayRateRequest sets a public relay's rate limit.
type AdminRelayRateRequest struct {
	Relay     proto.RelayID `json:"relay"`
	RateLimit int64         `json:"rateLimit"`
}

func (s *Server) adminRelayRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /relays", s.handleAdminRelays)
	m.HandleFunc("POST /relays", s.handleAdminRelayAdd)
	m.HandleFunc("POST /relay-token", s.handleAdminRelayToken)
	m.HandleFunc("POST /relay-rate", s.handleAdminRelayRate)
	m.HandleFunc("POST /relay-kick", s.handleAdminRelayKick)
	m.HandleFunc("POST /relay-unkick", s.handleAdminRelayUnkick)
	m.HandleFunc("POST /relay-set", s.handleAdminRelaySet)
}

func (s *Server) adminRelay(w *World, id proto.RelayID) AdminRelay {
	rl, m := w.relays[id], w.relayMeta[id]
	a := AdminRelay{
		ID: id, Name: rl.Name, Source: rl.Source, HostName: rl.HostName, IPv4: rl.IPv4, IPv6: rl.IPv6,
		DERPPort: rl.DERPPort, STUNPort: rl.STUNPort, Pin: rl.Pin, RateLimit: rl.RateLimit,
		Default: slices.Contains(w.defaults, id), Seeded: m == nil || m.CreatedAt.IsZero(), Registered: ready(rl),
	}
	if p := w.people[rl.Owner]; p != nil {
		a.Owner = p.Email
	}
	a.Online, a.LastSeen = s.relayLive.status(id)
	for _, l := range w.lans {
		if slices.Contains(l.added, id) {
			a.LANs++
		}
	}
	if m != nil {
		a.Weight, a.Capacity = m.Weight, m.Capacity
	}
	a.Kicks = len(w.kickedPerson[id]) + len(w.kickedDevice[id])
	var mo proto.RelayMonth
	s.relayNow(w, rl.Owner, id, &mo, s.now())
	a.Load = mo.Load
	return a
}

func (s *Server) handleAdminRelayKick(w http.ResponseWriter, r *http.Request) {
	var req AdminRelayKickRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var id string
	if _, err := s.change("", func(tx *sql.Tx, wd *World) error {
		if wd.relays[req.Relay] == nil {
			return errNotFound("没有这个中转站：" + string(req.Relay))
		}
		var person proto.PersonID
		if req.Email != "" {
			p := wd.byEmail[normEmail(req.Email)]
			if p == nil {
				return errNotFound("没有这个账号：" + req.Email)
			}
			person = p.ID
		}
		var err error
		id, err = kick(tx, wd, req.Relay, person, req.Device, "", s.now())
		return err
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("admin: kicked %s%s off relay %s (kick %s)", req.Email, req.Device, req.Relay, id)
	writeJSON(w, http.StatusOK, map[string]string{"kick": id})
}

func (s *Server) handleAdminRelayUnkick(w http.ResponseWriter, r *http.Request) {
	var req AdminRelayKickRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if _, err := s.change("", func(tx *sql.Tx, wd *World) error {
		k := wd.kicks[req.Kick]
		if k == nil || !k.active() {
			return errNotFound("没有这次踢人，或者已经撤销了：" + req.Kick)
		}
		return unkick(tx, k.ID, "", s.now())
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("admin: kick %s lifted", req.Kick)
	writeJSON(w, http.StatusOK, map[string]string{"kick": req.Kick})
}

func (s *Server) handleAdminRelaySet(w http.ResponseWriter, r *http.Request) {
	var req AdminRelaySetRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	switch {
	case req.Weight != nil && (*req.Weight < 0 || *req.Weight > maxRelayWeight):
		s.writeErr(w, r, errInvalid("权重要在 0 到 100 之间"))
		return
	case req.Capacity != nil && (*req.Capacity < 0 || *req.Capacity > maxRelayCapacity):
		s.writeErr(w, r, errInvalid("容量不对"))
		return
	}
	if _, err := s.change("", func(tx *sql.Tx, wd *World) error {
		if wd.relays[req.Relay] == nil {
			return errNotFound("没有这个中转站：" + string(req.Relay))
		}
		if req.Weight != nil {
			if _, err := tx.Exec(`UPDATE relays SET weight = ? WHERE id = ?`, *req.Weight, req.Relay); err != nil {
				return err
			}
		}
		if req.Capacity != nil {
			if _, err := tx.Exec(`UPDATE relays SET capacity = ? WHERE id = ?`, *req.Capacity, req.Relay); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	st := s.state.Load()
	a := s.adminRelay(st.w, req.Relay)
	s.logf("admin: relay %s weight %d, capacity %d; version %d", req.Relay, a.Weight, a.Capacity, st.v)
	writeJSON(w, http.StatusOK, a)
}

// AdminRelayKick kicks a person (by email) or a device off a relay.
func AdminRelayKick(path string, relay proto.RelayID, email string, device proto.DeviceID) (string, error) {
	var out map[string]string
	err := adminCall(path, "POST", "/relay-kick", AdminRelayKickRequest{Relay: relay, Email: email, Device: device}, &out)
	return out["kick"], err
}

// AdminRelayUnkick lifts a kick.
func AdminRelayUnkick(path, kick string) error {
	var out map[string]string
	return adminCall(path, "POST", "/relay-unkick", AdminRelayKickRequest{Kick: kick}, &out)
}

// AdminRelaySet sets a relay's weight or capacity; nil leaves it.
func AdminRelaySet(path string, relay proto.RelayID, weight, capacity *int) (*AdminRelay, error) {
	var out AdminRelay
	if err := adminCall(path, "POST", "/relay-set", AdminRelaySetRequest{Relay: relay, Weight: weight, Capacity: capacity}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Server) handleAdminRelays(w http.ResponseWriter, r *http.Request) {
	st := s.state.Load()
	out := []AdminRelay{}
	for id := range st.w.relays {
		out = append(out, s.adminRelay(st.w, id))
	}
	slices.SortFunc(out, func(a, b AdminRelay) int { return cmp.Compare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminRelayAdd(w http.ResponseWriter, r *http.Request) {
	var req AdminRelayAddRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	name := ""
	if req.Name != "" {
		var err error
		if name, err = checkName(req.Name); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	if req.RateLimit < 0 {
		s.writeErr(w, r, errInvalid("限速不能是负的"))
		return
	}
	now := s.now()
	var j *relayJoin
	var tok string
	if _, err := s.change("", func(tx *sql.Tx, _ *World) error {
		var err error
		j, tok, err = newJoin(tx, relayJoin{Source: proto.RelayPublic, Name: name, RateLimit: req.RateLimit, CreatedAt: now, ExpiresAt: now.Add(relayJoinLife)})
		return err
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("admin: join code %s for a public relay %q, %d bytes/s per connection", j.ID, name, req.RateLimit)
	writeJSON(w, http.StatusOK, s.adminToken(j, tok))
}

func (s *Server) handleAdminRelayToken(w http.ResponseWriter, r *http.Request) {
	var req AdminRelayTokenRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.now()
	var j *relayJoin
	var tok string
	if _, err := s.change("", func(tx *sql.Tx, wd *World) error {
		rl, m := wd.relays[req.Relay], wd.relayMeta[req.Relay]
		if rl == nil {
			return errNotFound("没有这个中转站：" + string(req.Relay))
		}
		if m == nil || m.CreatedAt.IsZero() {
			return errForbidden("这个中转站在种子文件里，地址由种子文件给，不用登记")
		}
		// The last code to set it up again stops working.
		if _, err := tx.Exec(`DELETE FROM relay_joins WHERE relay = ?`, rl.ID); err != nil {
			return err
		}
		var err error
		j, tok, err = newJoin(tx, relayJoin{Owner: rl.Owner, Relay: rl.ID, Source: rl.Source, Name: rl.Name, CreatedAt: now, ExpiresAt: now.Add(relayJoinLife)})
		return err
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.logf("admin: join code %s to set relay %s (%s) up again", j.ID, req.Relay, j.Name)
	writeJSON(w, http.StatusOK, s.adminToken(j, tok))
}

func (s *Server) handleAdminRelayRate(w http.ResponseWriter, r *http.Request) {
	var req AdminRelayRateRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.RateLimit < 0 {
		s.writeErr(w, r, errInvalid("限速不能是负的"))
		return
	}
	if _, err := s.change("", func(tx *sql.Tx, wd *World) error {
		rl := wd.relays[req.Relay]
		if rl == nil {
			return errNotFound("没有这个中转站：" + string(req.Relay))
		}
		// Someone's own relay carries their traffic at whatever speed
		// their server has (pricing-and-relays §2.3).
		if rl.Source != proto.RelayPublic {
			return errInvalid("只有公共中转站限速")
		}
		if rl.RateLimit == req.RateLimit {
			return errNoChange
		}
		_, err := tx.Exec(`UPDATE relays SET rate_limit = ? WHERE id = ?`, req.RateLimit, rl.ID)
		return err
	}); err != nil {
		s.writeErr(w, r, err)
		return
	}
	st := s.state.Load()
	s.logf("admin: relay %s now %d bytes/s per connection; version %d", req.Relay, req.RateLimit, st.v)
	writeJSON(w, http.StatusOK, s.adminRelay(st.w, req.Relay))
}

// adminToken is a join code as the admin socket shows it.
func (s *Server) adminToken(j *relayJoin, tok string) AdminRelayToken {
	v := s.joinView(j, tok)
	return AdminRelayToken{Relay: j.Relay, Name: j.Name, Token: tok, Code: v.Code, Command: v.Command, Expires: v.ExpiresAt}
}

// AdminRelays lists the relays, through the admin socket at path.
func AdminRelays(path string) ([]AdminRelay, error) {
	var out []AdminRelay
	return out, adminCall(path, "GET", "/relays", nil, &out)
}

// AdminRelayAdd makes a join code for a new public relay.
func AdminRelayAdd(path, name string, rateLimit int64) (*AdminRelayToken, error) {
	var out AdminRelayToken
	if err := adminCall(path, "POST", "/relays", AdminRelayAddRequest{Name: name, RateLimit: rateLimit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminRelayNewToken gives a relay a new registration token, to set it up
// again.
func AdminRelayNewToken(path string, relay proto.RelayID) (*AdminRelayToken, error) {
	var out AdminRelayToken
	if err := adminCall(path, "POST", "/relay-token", AdminRelayTokenRequest{Relay: relay}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminRelayRate sets a public relay's rate limit, in bytes a second per
// connection.
func AdminRelayRate(path string, relay proto.RelayID, rateLimit int64) (*AdminRelay, error) {
	var out AdminRelay
	if err := adminCall(path, "POST", "/relay-rate", AdminRelayRateRequest{Relay: relay, RateLimit: rateLimit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
