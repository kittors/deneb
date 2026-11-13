package control

import (
	"cmp"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
)

// The deployment's console for its operators (M7 batch 3, DESIGN §14.15;
// design/plan/m7/ops-console-api.md): what deneb-control's commands show
// and do, on the Web, for the deployment's operators alone; anyone else
// gets 403. What it shows stays within PRIVACY.md §9: never what devices
// report of their addresses and ports, no tokens or codes, no LAN's members
// or services. LANs are only counted, but for the one card that asks what
// becomes of the LANs of an account being deleted: the names of those it
// owns with others in them, and of the earliest of those others. Each
// change goes into the operators' record (opsrecord.go).

// OpsWho is a person as a line of the console names them.
type OpsWho struct {
	ID      proto.PersonID `json:"id"`
	Name    string         `json:"name"`
	Initial string         `json:"initial"`
}

// OpsPerson is a person in the console's list.
type OpsPerson struct {
	ID            proto.PersonID `json:"id"`
	Name          string         `json:"name"`
	Initial       string         `json:"initial"`
	Email         string         `json:"email"`
	Created       time.Time      `json:"created"`
	LastSeen      time.Time      `json:"lastSeen,omitzero"`
	Online        bool           `json:"online"`
	Devices       int            `json:"devices"`
	DevicesOnline int            `json:"devicesOnline"`
	Operator      bool           `json:"operator,omitempty"`
	Test          bool           `json:"test,omitempty"`
}

// OpsDevice is a device in the console: its Deneb address, never the
// addresses and ports it reports.
type OpsDevice struct {
	ID       proto.DeviceID   `json:"id"`
	Name     string           `json:"name"`
	Kind     proto.DeviceKind `json:"kind"`
	OS       proto.OS         `json:"os"`
	Engine   string           `json:"engine"`
	State    string           `json:"state"` // online, offline, loggedOut, expired
	LastSeen time.Time        `json:"lastSeen,omitzero"`
	Expires  time.Time        `json:"expires,omitzero"`
	Address  string           `json:"address"`
	Owner    OpsWho           `json:"owner"`
}

// OpsOwnedLAN is a LAN of the person's with others in it: deleting the
// account asks what becomes of it, as deleting one's own does.
type OpsOwnedLAN struct {
	ID     proto.LANID `json:"id"`
	Name   string      `json:"name"`
	Others int         `json:"others"`
	First  string      `json:"first"` // the earliest of the others, who would get it
}

// OpsSeats are a person's seats.
type OpsSeats struct {
	Used    int  `json:"used"`
	Limit   int  `json:"limit"` // what holds for them; 0: no limit
	Own     bool `json:"own"`   // their own limit, not the deployment's
	Default int  `json:"default"`
}

// OpsBrowsers are a person's browsers logged in.
type OpsBrowsers struct {
	Count  int       `json:"count"`
	Newest time.Time `json:"newest,omitzero"`
}

// OpsLANCount is how many LANs a person is in, and owns.
type OpsLANCount struct {
	Count int `json:"count"`
	Owned int `json:"owned"`
}

// OpsPersonDetail is the person's card.
type OpsPersonDetail struct {
	OpsPerson
	DeviceList []OpsDevice   `json:"deviceList"`
	Seats      OpsSeats      `json:"seats"`
	Browsers   OpsBrowsers   `json:"browsers"`
	LANs       OpsLANCount   `json:"lans"`
	Owned      []OpsOwnedLAN `json:"owned"`
	Relays     int           `json:"relays"`
}

// OpsAlert is an alert, now or resolved.
type OpsAlert struct {
	Key      string    `json:"key"`
	Level    string    `json:"level"` // bad, warn
	Title    string    `json:"title"`
	Text     string    `json:"text,omitempty"`
	At       time.Time `json:"at"`
	Resolved time.Time `json:"resolved,omitzero"`
	Relay    string    `json:"relay,omitempty"` // a public relay's name, for "去看香港"
}

// OpsServer is the server, on the console's first page.
type OpsServer struct {
	Started     time.Time `json:"started"`
	Version     string    `json:"version"`
	Memory      int64     `json:"memory"`
	MemoryLimit int64     `json:"memoryLimit"` // 0: none known
	DiskFree    float64   `json:"diskFree"`    // the share free, 0 to 1
	Backup      time.Time `json:"backup,omitzero"`
	Backups     int       `json:"backups"`
	BackupAt    string    `json:"backupAt,omitempty"` // when the newest was written, "08:00" (UTC+8)
}

// OpsRelays are the public relays, on the console's first page.
type OpsRelays struct {
	Count  int      `json:"count"`
	Online int      `json:"online"`
	Names  []string `json:"names"`
	Full   []string `json:"full"`
	Busy   []string `json:"busy"`
}

// OpsCounts are the deployment's numbers.
type OpsCounts struct {
	People      int     `json:"people"`
	NewThisWeek int     `json:"newThisWeek"`
	Devices     int     `json:"devices"`
	Online      int     `json:"online"`
	LANs        int     `json:"lans"`
	LANPeople   float64 `json:"lanPeople"` // people in a LAN, on average
	Browsers    int     `json:"browsers"`  // people logged in in a browser
}

// OpsOverview is the console's first page.
type OpsOverview struct {
	Alerts []OpsAlert `json:"alerts"`
	Recent []OpsAlert `json:"recent"` // the last 7 days', resolved
	Server OpsServer  `json:"server"`
	Relays OpsRelays  `json:"relays"`
	Counts OpsCounts  `json:"counts"`
}

// OpsLogin is a device waiting to log in, which an operator may let in to
// a test account (deneb-control approve). ID is not its login code.
type OpsLogin struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname"`
	OS       proto.OS  `json:"os"`
	Created  time.Time `json:"created"`
}

// OpsAct is what an operator does: an act and its arguments.
type OpsAct struct {
	Act  string            `json:"act"`
	Args map[string]string `json:"args"`
}

// How many of a list the console gets at once, and at most.
const (
	opsPage    = 30
	opsMaxPage = 100
)

func (s *Server) consoleRoutes(m *http.ServeMux, post func(http.HandlerFunc) http.Handler) {
	get := func(path string, h func(http.ResponseWriter, *http.Request, *person)) {
		m.HandleFunc("GET /api/v1/operator/"+path, s.web(s.operatorOnly(h)))
	}
	get("overview", s.handleOpsOverview)
	get("backups", s.handleOpsBackups)
	get("people", s.handleOpsPeople)
	get("people/{id}", s.handleOpsPerson)
	get("devices", s.handleOpsDevices)
	get("logins", s.handleOpsLogins)
	get("records", s.handleOpsRecords)
	get("log", s.handleOpsLog)
	m.Handle("POST /api/v1/operator/act", post(s.web(s.operatorOnly(s.handleOpsAct))))
}

// operatorOnly lets the deployment's operators through; anyone else gets
// 403, as for a page that is not theirs.
func (s *Server) operatorOnly(h func(http.ResponseWriter, *http.Request, *person)) func(http.ResponseWriter, *http.Request, *webSession) {
	return func(w http.ResponseWriter, r *http.Request, ws *webSession) {
		p := s.state.Load().w.people[ws.person]
		if p == nil || !p.Operator {
			s.writeErr(w, r, errForbidden("只有部署管理员能看"))
			return
		}
		h(w, r, p)
	}
}

// ---- what the console shows ----------------------------------------------

func whoOf(p *person) OpsWho {
	if p == nil {
		return OpsWho{}
	}
	return OpsWho{ID: p.ID, Name: p.DisplayName, Initial: p.Initial}
}

// deviceState is a device's state as the console says it.
func deviceState(d *device, isOnline online) string {
	switch {
	case d.LoggedOut:
		return "loggedOut"
	case d.expired:
		return "expired"
	case isOnline(d.ID):
		return "online"
	}
	return "offline"
}

func opsDevice(w *World, d *device, isOnline online) OpsDevice {
	return OpsDevice{ID: d.ID, Name: d.DisplayName, Kind: d.Kind, OS: d.OS, Engine: d.EngineVersion,
		State: deviceState(d, isOnline), LastSeen: d.LastSeen.UTC(), Expires: d.ExpiresAt.UTC(),
		Address: d.IPv4.String(), Owner: whoOf(w.people[d.Owner])}
}

// devicesOf are each person's devices, by display name.
func devicesOf(w *World) map[proto.PersonID][]*device {
	out := map[proto.PersonID][]*device{}
	for _, d := range w.devices {
		out[d.Owner] = append(out[d.Owner], d)
	}
	for _, ds := range out {
		slices.SortFunc(ds, func(a, b *device) int {
			return cmp.Or(strings.Compare(a.DisplayName, b.DisplayName), strings.Compare(string(a.ID), string(b.ID)))
		})
	}
	return out
}

func (s *Server) opsPerson(p *person, ds []*device, isOnline online) OpsPerson {
	_, test := s.testCodes[p.Email]
	o := OpsPerson{ID: p.ID, Name: p.DisplayName, Initial: p.Initial, Email: p.Email, Created: p.CreatedAt.UTC(),
		Devices: len(ds), Operator: p.Operator, Test: test}
	for _, d := range ds {
		if isOnline(d.ID) {
			o.DevicesOnline++
			o.Online = true
		}
		if d.LastSeen.After(o.LastSeen) {
			o.LastSeen = d.LastSeen.UTC()
		}
	}
	return o
}

func (s *Server) opsDetail(w *World, p *person, isOnline online) (OpsPersonDetail, error) {
	ds := devicesOf(w)[p.ID]
	d := OpsPersonDetail{OpsPerson: s.opsPerson(p, ds, isOnline), DeviceList: []OpsDevice{}, Owned: []OpsOwnedLAN{}}
	for _, x := range ds {
		d.DeviceList = append(d.DeviceList, opsDevice(w, x, isOnline))
	}
	st := w.seats(p.ID, s.now())
	d.Seats = OpsSeats{Used: st.Used, Limit: max(st.Limit, 0), Own: p.SeatLimit != 0, Default: max(s.cfg.SeatLimit, 0)}
	var newest int64
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(created_at), 0) FROM web_sessions WHERE person = ? AND expires_at > ?`,
		p.ID, ms(s.now())).Scan(&d.Browsers.Count, &newest); err != nil {
		return d, err
	}
	d.Browsers.Newest = fromMS(newest).UTC()
	lans := make([]*lan, 0, len(w.lans))
	for _, l := range w.lans {
		lans = append(lans, l)
	}
	slices.SortFunc(lans, func(a, b *lan) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, l := range lans {
		if l.joined(p.ID) {
			d.LANs.Count++
		}
		if l.Owner != p.ID {
			continue
		}
		d.LANs.Owned++
		if heir := l.heir(p.ID); heir != "" {
			others := 0
			for _, m := range l.members {
				if m.Person != p.ID && m.State == proto.MemberJoined {
					others++
				}
			}
			d.Owned = append(d.Owned, OpsOwnedLAN{ID: l.ID, Name: l.Name, Others: others, First: w.people[heir].DisplayName})
		}
	}
	d.Relays = len(w.ownRelays(p.ID))
	return d, nil
}

func (s *Server) handleOpsOverview(w http.ResponseWriter, r *http.Request, _ *person) {
	st := s.state.Load()
	now := s.now()
	isOnline := s.hub.onlineSnapshot()
	v := OpsOverview{Alerts: []OpsAlert{}, Recent: []OpsAlert{}}
	active, recent := s.Alerts()
	for _, a := range active {
		v.Alerts = append(v.Alerts, opsAlert(st.w, a))
	}
	slices.Reverse(v.Alerts) // newest first
	v.Recent = resolvedAlerts(st.w, recent, now.Add(-7*24*time.Hour))

	v.Server = OpsServer{Started: s.started.UTC(), Version: buildVersion(), Memory: s.health.memory(), MemoryLimit: memoryLimit()}
	if free, total, err := s.health.disk(s.cfg.DataDir); err == nil && total > 0 {
		v.Server.DiskFree = float64(free) / float64(total)
	}
	if bs, err := s.backups(); err == nil {
		v.Server.Backups = len(bs)
		if len(bs) > 0 {
			v.Server.Backup = bs[0].Written.UTC()
			v.Server.BackupAt = bs[0].Written.In(chinaTime).Format("15:04")
		}
	}

	v.Relays = OpsRelays{Names: []string{}, Full: []string{}, Busy: []string{}}
	for _, id := range st.w.publicRelays() {
		rl := st.w.relays[id]
		if !ready(rl) {
			continue
		}
		v.Relays.Count++
		v.Relays.Names = append(v.Relays.Names, rl.Name)
		if !s.relayLive.offline(id) {
			v.Relays.Online++
		}
		switch {
		case s.relayLive.isFull(id):
			v.Relays.Full = append(v.Relays.Full, rl.Name)
		case s.relayBusy(st.w, id):
			v.Relays.Busy = append(v.Relays.Busy, rl.Name)
		}
	}

	week := now.Add(-7 * 24 * time.Hour)
	for _, p := range st.w.people {
		v.Counts.People++
		if p.CreatedAt.After(week) {
			v.Counts.NewThisWeek++
		}
	}
	for _, d := range st.w.devices {
		v.Counts.Devices++
		if isOnline(d.ID) {
			v.Counts.Online++
		}
	}
	members := 0
	for _, l := range st.w.lans {
		v.Counts.LANs++
		for _, m := range l.members {
			if m.State == proto.MemberJoined {
				members++
			}
		}
	}
	if v.Counts.LANs > 0 {
		v.Counts.LANPeople = float64(members*10/v.Counts.LANs) / 10
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT person) FROM web_sessions WHERE expires_at > ?`, ms(now)).Scan(&v.Counts.Browsers); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// relayBusy: a public relay near its capacity or the CPU it may use, but
// not full yet: three quarters of either.
func (s *Server) relayBusy(w *World, id proto.RelayID) bool {
	ld := s.relayLive.lastLoad(id)
	if ld == nil {
		return false
	}
	capacity := 0
	if m := w.relayMeta[id]; m != nil {
		capacity = m.Capacity
	}
	return capacity > 0 && float64(ld.devices) >= 0.75*float64(capacity) || ld.health.CPU >= 0.75*fullCPU
}

// alertLevel: what is broken is "bad" (red), what is tight "warn".
func alertLevel(key string) string {
	if strings.HasPrefix(key, "relay-offline/") || key == "backup-failed" {
		return "bad"
	}
	return "warn"
}

func opsAlert(w *World, a alert.Alert) OpsAlert {
	o := OpsAlert{Key: a.Key, Level: alertLevel(a.Key), Title: a.Title, Text: a.Text, At: a.At.UTC()}
	if _, id, ok := strings.Cut(a.Key, "/"); ok && strings.HasPrefix(a.Key, "relay-") {
		if rl := w.relays[proto.RelayID(id)]; rl != nil {
			o.Relay = rl.Name
		}
	}
	return o
}

// resolvedAlerts are the alerts that fired and were resolved since since,
// newest first, each with when it was resolved.
func resolvedAlerts(w *World, records []alert.Record, since time.Time) []OpsAlert {
	firing := map[string]alert.Alert{}
	out := []OpsAlert{}
	for _, r := range records { // oldest first
		switch r.Kind {
		case alert.Firing:
			if _, seen := firing[r.Key]; !seen {
				firing[r.Key] = r.Alert
			}
		case alert.Resolved:
			f, ok := firing[r.Key]
			if !ok {
				continue
			}
			delete(firing, r.Key)
			if r.At.Before(since) {
				continue
			}
			o := opsAlert(w, f)
			o.Resolved = r.At.UTC()
			out = append(out, o)
		}
	}
	slices.Reverse(out)
	return out
}

// buildVersion is the commit the server was built from, as Go wrote it
// into the binary: "153a93d", "-dirty" when the tree had changes.
func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "开发版"
	}
	var rev, dirty string
	for _, st := range bi.Settings {
		switch st.Key {
		case "vcs.revision":
			rev = st.Value
		case "vcs.modified":
			if st.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if rev == "" {
		return "开发版"
	}
	return rev + dirty
}

// memoryLimit is the container's memory limit (cgroup v2, then v1), or 0.
func memoryLimit() int64 {
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err == nil && v > 0 && v < 1<<60 {
			return v
		}
	}
	return 0
}

func (s *Server) handleOpsBackups(w http.ResponseWriter, r *http.Request, _ *person) {
	bs, err := s.backups()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if bs == nil {
		bs = []Backup{}
	}
	writeJSON(w, http.StatusOK, bs)
}

// page reads ?offset= and ?limit=.
func page(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = opsPage
	}
	return max(offset, 0), min(limit, opsMaxPage)
}

func cut[T any](xs []T, offset, limit int) []T {
	offset = min(offset, len(xs))
	out := xs[offset:min(len(xs), offset+limit)]
	if out == nil {
		out = []T{}
	}
	return out
}

// handleOpsPeople lists people, the oldest account first (newest first
// with filter=new): q in their name or email; filter new (this week),
// online, operators or test.
func (s *Server) handleOpsPeople(w http.ResponseWriter, r *http.Request, _ *person) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filter := r.URL.Query().Get("filter")
	offset, limit := page(r)
	st := s.state.Load()
	isOnline := s.hub.onlineSnapshot()
	byOwner := devicesOf(st.w)
	week := s.now().Add(-7 * 24 * time.Hour)
	out := []OpsPerson{}
	for _, p := range st.w.people {
		if q != "" && !strings.Contains(strings.ToLower(p.DisplayName+" "+p.Email), q) {
			continue
		}
		o := s.opsPerson(p, byOwner[p.ID], isOnline)
		switch filter {
		case "":
		case "new":
			if !p.CreatedAt.After(week) {
				continue
			}
		case "online":
			if !o.Online {
				continue
			}
		case "operators":
			if !o.Operator {
				continue
			}
		case "test":
			if !o.Test {
				continue
			}
		default:
			s.writeErr(w, r, errInvalid("不认识的筛法："+filter))
			return
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b OpsPerson) int {
		c := a.Created.Compare(b.Created)
		if filter == "new" {
			c = -c
		}
		return cmp.Or(c, strings.Compare(a.Email, b.Email))
	})
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "people": cut(out, offset, limit)})
}

func (s *Server) handleOpsPerson(w http.ResponseWriter, r *http.Request, _ *person) {
	st := s.state.Load()
	p := st.w.people[proto.PersonID(r.PathValue("id"))]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个人了"))
		return
	}
	d, err := s.opsDetail(st.w, p, s.hub.onlineSnapshot())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleOpsDevices lists devices, by their owners' emails, then name: q in
// the device's name or its owner's name or email; state one of
// deviceState's.
func (s *Server) handleOpsDevices(w http.ResponseWriter, r *http.Request, _ *person) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	state := r.URL.Query().Get("state")
	switch state {
	case "", "online", "offline", "loggedOut", "expired":
	default:
		s.writeErr(w, r, errInvalid("不认识的状态："+state))
		return
	}
	offset, limit := page(r)
	st := s.state.Load()
	isOnline := s.hub.onlineSnapshot()
	type row struct {
		email string
		d     OpsDevice
	}
	var rows []row
	online := 0
	for _, d := range st.w.devices {
		o := opsDevice(st.w, d, isOnline)
		if o.State == "online" {
			online++
		}
		email := ""
		if p := st.w.people[d.Owner]; p != nil {
			email = p.Email
		}
		if q != "" && !strings.Contains(strings.ToLower(o.Name+" "+o.Owner.Name+" "+email), q) {
			continue
		}
		if state != "" && o.State != state {
			continue
		}
		rows = append(rows, row{email, o})
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(strings.Compare(a.email, b.email), strings.Compare(a.d.Name, b.d.Name), strings.Compare(string(a.d.ID), string(b.d.ID)))
	})
	out := make([]OpsDevice, len(rows))
	for i, x := range rows {
		out[i] = x.d
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "all": len(st.w.devices), "online": online, "devices": cut(out, offset, limit)})
}

// opsLoginID is a waiting login's id on the console: not its code, which
// lets the device in.
func opsLoginID(code string) string { return "l_" + hashSecret("console:" + code)[:12] }

func (s *Server) handleOpsLogins(w http.ResponseWriter, r *http.Request, _ *person) {
	logins := []OpsLogin{}
	for _, l := range s.logins.list(s.now()) {
		logins = append(logins, OpsLogin{ID: opsLoginID(l.Code), Hostname: l.Hostname, OS: l.OS, Created: l.Created})
	}
	w0 := s.state.Load().w
	accounts := []OpsWho{}
	for e := range s.testCodes {
		if p := w0.byEmail[e]; p != nil {
			accounts = append(accounts, OpsWho{ID: p.ID, Name: p.Email, Initial: p.Initial})
		}
	}
	slices.SortFunc(accounts, func(a, b OpsWho) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{"logins": logins, "accounts": accounts})
}

// handleOpsRecords is the record, newest first: ?before= (RFC 3339) for
// what came before, ?limit= (200, at most 1000).
func (s *Server) handleOpsRecords(w http.ResponseWriter, r *http.Request, _ *person) {
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			s.writeErr(w, r, errInvalid("before 要是 RFC 3339 的时间"))
			return
		}
		before = t
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	recs, err := s.opsRecords(before, min(limit, 1000))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func (s *Server) handleOpsLog(w http.ResponseWriter, r *http.Request, _ *person) {
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "", "login", "device", "op", "alert", "error", "other":
	default:
		s.writeErr(w, r, errInvalid("不认识的种类："+kind))
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	res, err := s.consoleLog(kind, after, before)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- what the console does -----------------------------------------------

func (s *Server) handleOpsAct(w http.ResponseWriter, r *http.Request, by *person) {
	var req OpsAct
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	arg := func(k string) string { return strings.TrimSpace(req.Args[k]) }
	person := proto.PersonID(arg("person"))
	device := proto.DeviceID(arg("device"))
	detail := func() {
		st := s.state.Load()
		p := st.w.people[person]
		if p == nil {
			s.writeErr(w, r, errNotFound("没有这个人了"))
			return
		}
		d, err := s.opsDetail(st.w, p, s.hub.onlineSnapshot())
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
	var err error
	switch req.Act {
	case "seats.set":
		n, convErr := strconv.Atoi(arg("limit"))
		if convErr != nil {
			s.writeErr(w, r, errInvalid("上限要是一个数"))
			return
		}
		if err = s.opsSetSeats(by, person, n); err == nil {
			detail()
			return
		}
	case "sessions.revoke":
		if _, err = s.opsRevokeSessions(by, person); err == nil {
			detail()
			return
		}
	case "operator.set":
		if err = s.opsSetOperator(by, person, arg("on") == "true"); err == nil {
			detail()
			return
		}
	case "device.relogin":
		if err = s.opsRelogin(by, device); err == nil {
			st := s.state.Load()
			if d := st.w.devices[device]; d != nil {
				writeJSON(w, http.StatusOK, opsDevice(st.w, d, s.hub.onlineSnapshot()))
				return
			}
			err = errNotFound("没有这台设备了")
		}
	case "device.delete":
		if _, err = s.opsDeleteDevice(by, device); err == nil {
			writeJSON(w, http.StatusOK, struct{}{})
			return
		}
	case "account.delete":
		choices := map[proto.LANID]string{}
		for k, v := range req.Args {
			id, ok := strings.CutPrefix(k, "lan:")
			if !ok {
				continue
			}
			if v != proto.AccountTransfer && v != proto.AccountDeleteLANs {
				s.writeErr(w, r, errInvalid("局域网要么 transfer，要么 delete"))
				return
			}
			choices[proto.LANID(id)] = v
		}
		if _, err = s.opsDeleteAccount(by, person, arg("confirm"), func(l *lan) string { return choices[l.ID] }); err == nil {
			writeJSON(w, http.StatusOK, struct{}{})
			return
		}
	case "login.approve":
		if err = s.opsApprove(by, arg("login"), arg("email")); err == nil {
			writeJSON(w, http.StatusOK, struct{}{})
			return
		}
	default:
		err = errInvalid("没有这种操作：" + req.Act)
	}
	s.writeErr(w, r, err)
}

// recordAlone writes a line of the record for a change made outside the
// world's change (sessions, logins), in its own transaction, and logs it.
func (s *Server) recordAlone(by opsActor, act, target, text string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordOps(tx, s.now(), by, act, target, text, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.logOps(by, act, text)
	return nil
}

// seatsWord is a seat limit as the record says it.
func seatsWord(n int) string {
	if n <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d 个", n)
}

// opsSetSeats sets a person's seat limit: limit above zero, -1 for none, 0
// (or the deployment's own number) the deployment's again. by nil is the
// admin socket.
func (s *Server) opsSetSeats(by *person, id proto.PersonID, limit int) error {
	if limit < -1 || limit > 10000 {
		return errInvalid("位置上限不对：1 到 10000，0 是恢复部署的默认")
	}
	if limit == s.cfg.SeatLimit {
		limit = 0
	}
	actor := actorOf(by)
	var text string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		p := w.people[id]
		if p == nil {
			return errNotFound("没有这个人了")
		}
		if p.SeatLimit == limit {
			return errNoChange
		}
		if _, err := tx.Exec(`UPDATE people SET seat_limit = ? WHERE id = ?`, limit, p.ID); err != nil {
			return err
		}
		switch {
		case limit == 0:
			text = fmt.Sprintf("把%s的位置上限恢复成了部署的默认（%s）", p.DisplayName, seatsWord(s.cfg.SeatLimit))
		case limit < 0:
			text = fmt.Sprintf("把%s的位置上限改成了不限", p.DisplayName)
		default:
			text = fmt.Sprintf("把%s的位置上限调成了 %d", p.DisplayName, limit)
		}
		text = pangu(text)
		return recordOps(tx, s.now(), actor, "seats.set", string(p.ID), text, "")
	})
	if err == nil && text != "" {
		s.logOps(actor, "seats.set", text)
	}
	return err
}

// opsRevokeSessions logs all a person's browsers out, and says how many.
func (s *Server) opsRevokeSessions(by *person, id proto.PersonID) (int64, error) {
	p := s.state.Load().w.people[id]
	if p == nil {
		return 0, errNotFound("没有这个人了")
	}
	actor := actorOf(by)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM web_sessions WHERE person = ?`, p.ID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	text := pangu(fmt.Sprintf("让%s在 %d 个浏览器里都退出了", p.DisplayName, n))
	if n > 0 {
		if err := recordOps(tx, s.now(), actor, "sessions.revoke", string(p.ID), text, ""); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if n > 0 {
		s.logOps(actor, "sessions.revoke", text)
	}
	return n, nil
}

// opsSetOperator makes a person an operator, or (on false) no longer one:
// never the asking operator themselves, so the console always has one who
// can undo it.
func (s *Server) opsSetOperator(by *person, id proto.PersonID, on bool) error {
	if !on && by != nil && by.ID == id {
		return errConflict("不能不让自己管：请另一位部署管理员来做")
	}
	actor := actorOf(by)
	var text string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		p := w.people[id]
		if p == nil {
			return errNotFound("没有这个人了")
		}
		if p.Operator == on {
			return errNoChange
		}
		if _, err := tx.Exec(`UPDATE people SET operator = ? WHERE id = ?`, on, p.ID); err != nil {
			return err
		}
		text = pangu("把" + p.DisplayName + "设成了部署管理员")
		if !on {
			text = pangu("不让" + p.DisplayName + "管这个部署了")
		}
		return recordOps(tx, s.now(), actor, "operator.set", string(p.ID), text, "")
	})
	if err == nil && text != "" {
		s.logOps(actor, "operator.set", text)
	}
	return err
}

// opsRelogin makes a device's login run out now: it leaves every network
// map at once, and its owner logs in on it again (the same address).
func (s *Server) opsRelogin(by *person, id proto.DeviceID) error {
	actor := actorOf(by)
	var text string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		d := w.devices[id]
		switch {
		case d == nil:
			return errNotFound("没有这台设备了")
		case d.LoggedOut:
			return errConflict("这台设备已经登出了，主人要在上面重新登录")
		case d.expired:
			return errNoChange
		}
		now := s.now()
		if _, err := tx.Exec(`UPDATE devices SET expires_at = ? WHERE id = ?`, ms(now), d.ID); err != nil {
			return err
		}
		text = pangu(fmt.Sprintf("让%s的「%s」重新登录", w.people[d.Owner].DisplayName, d.DisplayName))
		return recordOps(tx, now, actor, "device.relogin", string(d.ID), text, "")
	})
	if err == nil && text != "" {
		s.logOps(actor, "device.relogin", text)
	}
	return err
}

// opsDeleteDevice deletes a device, whoever's it is, as device.delete does
// for its owner, and says what it was.
func (s *Server) opsDeleteDevice(by *person, id proto.DeviceID) (AdminDevice, error) {
	actor := actorOf(by)
	var gone AdminDevice
	var text string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		d := w.devices[id]
		if d == nil {
			return errNotFound("没有这台设备了")
		}
		gone = adminDevice(w, d, s.hub.onlineSnapshot())
		now := s.now()
		if err := deleteDevice(tx, d, now); err != nil {
			return err
		}
		owner := ""
		if p := w.people[d.Owner]; p != nil {
			owner = p.DisplayName
		}
		text = pangu(fmt.Sprintf("删了%s的「%s」", owner, d.DisplayName))
		return recordOps(tx, now, actor, "device.delete", string(d.ID), text, "")
	})
	if err == nil {
		s.logOps(actor, "device.delete", text)
	}
	return gone, err
}

// opsDeleteAccount deletes a person's account for them, as account.delete
// does for its owner: confirm must be their email; never the asking
// operator's own (theirs is deleted from their account's card, as
// anyone's); choose says, LAN by LAN, what becomes of those they own with
// others in them. Done, it mails them that it went at their request, not
// saying who did it; a mail that does not go out is logged, and the
// deletion stands.
func (s *Server) opsDeleteAccount(by *person, id proto.PersonID, confirm string, choose func(*lan) string) (*proto.AccountDeletion, error) {
	actor := actorOf(by)
	var c *opCtx
	var view *proto.AccountDeletion
	var email, text string
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		p := w.people[id]
		if p == nil {
			return errNotFound("没有这个人了")
		}
		if normEmail(confirm) != p.Email {
			return errInvalid("邮箱对不上，再看一眼")
		}
		if by != nil && p.ID == by.ID {
			return errConflict("自己的账号在账号卡片里注销")
		}
		if s.seeded(p.Email) {
			return errForbidden("这个账号写在部署的种子文件里，服务器每次启动都会把它建回来：要注销，先把它从种子文件里拿掉")
		}
		pl := w.planAccountDeletionEach(p.ID, choose)
		if len(pl.blocked) > 0 {
			names := make([]string, len(pl.blocked))
			for i, l := range pl.blocked {
				names[i] = "「" + l.Name + "」"
			}
			return errConflict(strings.Join(names, "、") + "里还有别人：先选好转给别人还是整个删掉")
		}
		now := s.now()
		c = &opCtx{s: s, tx: tx, w: w, me: p.ID, now: now}
		view = pl.view(false)
		if err := c.deleteAccount(p, pl, view); err != nil {
			return err
		}
		email = p.Email
		text = fmt.Sprintf("替%s（%s）注销了账号", p.DisplayName, p.Email)
		for _, t := range pl.transfer {
			text += fmt.Sprintf("，「%s」转给了%s", w.lans[t.LAN].Name, w.people[t.To].DisplayName)
		}
		for _, l := range pl.deleteLANs {
			if l.heir(p.ID) != "" {
				text += fmt.Sprintf("，删了「%s」", l.Name)
			}
		}
		text = pangu(text)
		return recordOps(tx, now, actor, "account.delete", string(p.ID), text, "")
	})
	if err != nil {
		return nil, err
	}
	for _, f := range c.after {
		f()
	}
	s.logOps(actor, "account.delete", text)
	s.mailDeleted(email)
	return view, nil
}

// opsApprove lets a waiting device in to a test account, as deneb-control
// approve does; login is its console id (OpsLogin.ID).
func (s *Server) opsApprove(by *person, login, email string) error {
	code := ""
	for _, l := range s.logins.list(s.now()) {
		if opsLoginID(l.Code) == login {
			code = l.Code
		}
	}
	if code == "" {
		return errNotFound("这台设备已经不等了")
	}
	res, err := s.adminApprove(code, email)
	if err != nil {
		return err
	}
	name := res.Name
	if name == "" {
		name = res.Hostname
	}
	return s.recordAlone(actorOf(by), "login.approve", string(res.Device), pangu(fmt.Sprintf("批准了「%s」登进 %s", name, res.Email)))
}
