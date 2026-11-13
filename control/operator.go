package control

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// The deployment's operators (M4). Whoever runs the server says who they
// are, with deneb-control operators on the server itself; there is no
// form for it. On the ops page (DESIGN §14.12 M4-13) they manage the
// public relays the way people manage their own (the plan's §16): add one
// with the one-line command, weights, kicks. Everyone else sees a public
// relay and nothing more (DESIGN §14.8).

// Two choices still wait on the user (DESIGN §14.12, 要你定 3 and 4); each
// is this one constant.
const (
	// newPublicRelayWeight is a newly installed public relay's weight. 0
	// is a standby: a machine just set up takes no share of everyone at
	// once, until an operator gives it one ("多用"). 1 would put it to
	// work at once.
	newPublicRelayWeight = 0
	// operatorTopPeople is how many of a public relay's heaviest users the
	// ops page names ("用得最多的"), with their traffic and nothing else.
	operatorTopPeople = 5
)

// defaultPublicRateLimit is each connection's limit on a public relay
// added from the ops page: 2 MB/s, as the public relays have it (DESIGN
// §6.12, "每条连接限速 2 MB/s"). deneb-control relay-rate changes it.
const defaultPublicRateLimit = 2_000_000

// operator reports whether the asking person is one of the deployment's
// operators.
func (c *opCtx) operator() bool {
	p := c.w.people[c.me]
	return p != nil && p.Operator
}

// relayToManage finds the relay an owner's op is about: the asker's own
// (ownRelay), or, for an operator, a public relay. One the seed file sets
// up or lists is the seed file's to change, its name and address and
// place on the lists, but its kicks are not (seedOK): the seed has no say
// in who may use a relay.
func (c *opCtx) relayToManage(id proto.RelayID, verb string, seedOK bool) (*proto.Relay, error) {
	rl := c.w.relays[id]
	if rl == nil || rl.Source != proto.RelayPublic {
		return c.ownRelay(id, verb, true)
	}
	if !c.operator() {
		return nil, errForbidden("这是公共中转站，只有部署管理员能" + verb)
	}
	m := c.w.relayMeta[id]
	if !seedOK && (m == nil || m.CreatedAt.IsZero() || slices.Contains(c.w.defaults, id)) {
		return nil, errForbidden("这个公共中转站写在部署的种子文件里，要" + verb + "得改种子文件")
	}
	return rl, nil
}

// publicRelays are the public relays, sorted by ID.
func (w *World) publicRelays() []proto.RelayID {
	var out []proto.RelayID
	for id, rl := range w.relays {
		if rl.Source == proto.RelayPublic {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// operatorView is an operator's part of the view: the public relays'
// cards and their codes waiting to be used (a public relay's code is no
// one's: Owner empty).
func (w *World) operatorView(now time.Time, isOnline online) *proto.OperatorView {
	v := &proto.OperatorView{Relays: []proto.RelayView{}, Joins: w.myJoins("", now)}
	for _, id := range w.publicRelays() {
		v.Relays = append(v.Relays, w.relayView(id, "", now, isOnline))
	}
	return v
}

// operatorUsage is the ops page's numbers: each public relay's month, its
// load, its kicks, and its heaviest users by traffic alone. It names no
// device and never says who with whom; the people on the relays now are
// only counted.
func (s *Server) operatorUsage(w *World, now time.Time) (*proto.OperatorUsage, error) {
	now = now.UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := &proto.OperatorUsage{Month: now.Format("2006-01"), Relays: []proto.RelayMonth{}}
	pub := w.publicRelays()
	used, err := s.usageSince(first, pub)
	if err != nil {
		return nil, err
	}
	devices, people := map[proto.DeviceID]bool{}, map[proto.PersonID]bool{}
	for _, id := range pub {
		m := proto.RelayMonth{Relay: id, LANs: []proto.UsageLine{}, Shares: []proto.UsageLine{}, Devices: []proto.UsageLine{}}
		byPerson := map[proto.PersonID]int64{}
		for did, b := range used[id] {
			// What it carried counts each byte once, on the sending side.
			m.Bytes += b.sent
			// A device since deleted is no one's any more: its bytes stay
			// in the total, it names no one.
			if d := w.devices[did]; d != nil && b.sent+b.received > 0 {
				byPerson[d.Owner] += b.sent + b.received
			}
		}
		m.Users = len(byPerson)
		m.Top = w.topPeople(byPerson, operatorTopPeople)
		m.Kicks = w.kicksOf(id, now.AddDate(0, 0, -30))
		if ld := s.relayLive.lastLoad(id); ld != nil {
			m.Load = s.loadView(id, ld)
			for _, k := range ld.connected {
				if d := w.byNodeKey[k]; d != nil {
					devices[d.ID], people[d.Owner] = true, true
				}
			}
		}
		out.Relays = append(out.Relays, m)
	}
	out.People, out.Devices = len(people), len(devices)
	return out, nil
}

// topPeople are the n people with the most bytes, most first, as their
// balls show them: no email.
func (w *World) topPeople(bytes map[proto.PersonID]int64, n int) []proto.PersonBytes {
	var out []proto.PersonBytes
	for id, b := range bytes {
		if p := w.people[id]; p != nil {
			pp := p.Person
			pp.Email = ""
			out = append(out, proto.PersonBytes{Person: pp, Bytes: b})
		}
	}
	slices.SortFunc(out, func(a, b proto.PersonBytes) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return cmp.Compare(a.Person.ID, b.Person.ID)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *Server) handleWebOperatorUsage(w http.ResponseWriter, r *http.Request, ws *webSession) {
	st := s.state.Load()
	if p := st.w.people[ws.person]; p == nil || !p.Operator {
		s.writeErr(w, r, errForbidden("只有部署管理员能看运营页"))
		return
	}
	u, err := s.operatorUsage(st.w, s.now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// Through the admin socket: who the operators are.

// AdminOperator is one of the deployment's operators.
type AdminOperator struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// AdminOperatorRequest makes the account with Email an operator, or with
// Remove no longer one.
type AdminOperatorRequest struct {
	Email  string `json:"email"`
	Remove bool   `json:"remove,omitempty"`
}

func adminOperators(w *World) []AdminOperator {
	out := []AdminOperator{}
	for _, p := range w.people {
		if p.Operator {
			out = append(out, AdminOperator{Email: p.Email, DisplayName: p.DisplayName})
		}
	}
	slices.SortFunc(out, func(a, b AdminOperator) int { return strings.Compare(a.Email, b.Email) })
	return out
}

func (s *Server) handleAdminOperators(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, adminOperators(s.state.Load().w))
}

// handleAdminSetOperator changes who is an operator on the running server:
// their views get the ops page, or lose it, at once.
func (s *Server) handleAdminSetOperator(w http.ResponseWriter, r *http.Request) {
	var req AdminOperatorRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	e := normEmail(req.Email)
	p := s.state.Load().w.byEmail[e]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个账号："+e))
		return
	}
	// As the console's operator.set, with a line of the record.
	if err := s.opsSetOperator(nil, p.ID, !req.Remove); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminOperators(s.state.Load().w))
}

// AdminOperators lists the operators, through the admin socket at path.
func AdminOperators(path string) ([]AdminOperator, error) {
	var out []AdminOperator
	return out, adminCall(path, "GET", "/operators", nil, &out)
}

// AdminSetOperator makes the account with email an operator, or (remove)
// no longer one, and lists the operators after.
func AdminSetOperator(path, email string, remove bool) ([]AdminOperator, error) {
	var out []AdminOperator
	return out, adminCall(path, "POST", "/operators", AdminOperatorRequest{Email: email, Remove: remove}, &out)
}
