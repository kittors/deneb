package control

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// Relays (M4; implementation.md §2.6.3, pricing-and-relays §2.3). A relay
// is public (ours, for everyone logged in, rate-limited), or someone's own
// (their server, later a device at home) that serves only the LANs they
// drag it into and the shares they give. Every LAN and share has a list
// of relays its devices try in order: its own first, then the fallback.
// A relay lets in exactly the devices it is on a list of, so the lists
// are the admission rule too.

// relayMeta is what the server keeps about a relay beyond what devices see.
type relayMeta struct {
	// SecretHash is the hash of the secret the relay program got when it
	// registered; empty before. Only the hash is kept, as for a password.
	SecretHash string
	// CreatedAt is when it was made (relay.create, or its first
	// registration with a join code); zero for a relay from the seed,
	// which the seed manages.
	CreatedAt time.Time
	// RegisteredAt is when its program first registered: "刚上线".
	RegisteredAt time.Time
	// Weight and Capacity are its owner's settings (the plan's §16).
	Weight   int
	Capacity int
}

// relayJoin is a join code not used yet (relay.join, relay.create,
// relay.token, deneb-control relay-add): it makes a relay when it is
// used, or sets an existing one up again.
type relayJoin struct {
	ID        string
	Owner     proto.PersonID // empty for a public relay's
	Relay     proto.RelayID  // the relay it sets up again; empty for a new one
	Source    proto.RelaySource
	Name      string // the new relay's name; empty: from the region it reports
	RateLimit int64  // a new public relay's
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// live reports whether the code still works at now.
func (j *relayJoin) live(now time.Time) bool { return now.Before(j.ExpiresAt) }

// ready reports whether a relay can be dialed: the seed gave it an
// address, or its program registered.
func ready(rl *proto.Relay) bool { return rl != nil && rl.DERPPort != 0 }

// finishRelays works out the fallback and each LAN's list once the
// relays and the LANs are loaded.
func (w *World) finishRelays() {
	w.fallback = nil
	seen := map[proto.RelayID]bool{}
	for _, id := range w.defaults {
		if ready(w.relays[id]) && !seen[id] {
			seen[id] = true
			w.fallback = append(w.fallback, id)
		}
	}
	var public []proto.RelayID
	for id, rl := range w.relays {
		if rl.Source == proto.RelayPublic && ready(rl) && !seen[id] {
			public = append(public, id)
		}
	}
	slices.Sort(public)
	w.fallback = append(w.fallback, public...)
	for _, l := range w.lans {
		l.relays = w.withFallback(l.added)
	}
}

// withFallback is the ready relays of own, then the fallback relays not
// among them.
func (w *World) withFallback(own []proto.RelayID) []proto.RelayID {
	out := []proto.RelayID{}
	for _, list := range [][]proto.RelayID{own, w.fallback} {
		for _, id := range list {
			if ready(w.relays[id]) && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// ownRelays are p's own relays, public ones being no one's, sorted by ID.
func (w *World) ownRelays(p proto.PersonID) []proto.RelayID {
	var out []proto.RelayID
	for id, rl := range w.relays {
		if rl.Owner == p && rl.Source != proto.RelayPublic {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// relaysOf is every relay device d may use: the fallback, and every relay
// on its list of a scope it is in, but none it is kicked off. Its network
// map lists exactly these.
func (w *World) relaysOf(d *device) map[proto.RelayID]bool {
	m := map[proto.RelayID]bool{}
	for _, id := range w.listFor(d, nil) {
		m[id] = true
	}
	for _, l := range w.lansOf(d) {
		for _, id := range w.listFor(d, l.added) {
			m[id] = true
		}
	}
	for _, sh := range w.sharesOf(d) {
		for _, id := range w.listFor(d, w.shareOwn(sh)) {
			m[id] = true
		}
	}
	return m
}

// admits reports whether relay id lets in the device whose node key is k
// (§2.6.3): only a device someone is logged in on whose login has not run
// out; on a public relay any such device, on someone's own relay one it
// is on a list of: in a LAN its owner dragged it into, or in a share they
// gave.
func (w *World) admits(id proto.RelayID, k proto.NodePublic) bool {
	rl, d := w.relays[id], w.byNodeKey[k]
	if !ready(rl) || d == nil || !d.usable() || w.kicked(id, d) {
		return false
	}
	if rl.Source == proto.RelayPublic {
		return true
	}
	return w.relaysOf(d)[id]
}

// myRelays are p's relays as their cards show them, sorted by ID.
func (w *World) myRelays(p proto.PersonID, now time.Time, isOnline online) []proto.RelayView {
	out := []proto.RelayView{}
	for _, id := range w.ownRelays(p) {
		out = append(out, w.relayView(id, p, now, isOnline))
	}
	return out
}

// relayView is relay id's card as p sees it: the LANs are the ones p is
// in that it is dragged into.
func (w *World) relayView(id proto.RelayID, p proto.PersonID, now time.Time, isOnline online) proto.RelayView {
	rl, m := w.relays[id], w.relayMeta[id]
	rv := proto.RelayView{ID: id, LANs: []proto.LANID{}, Registered: ready(rl), Default: slices.Contains(w.defaults, id)}
	if m != nil {
		rv.Seeded, rv.Since, rv.Weight, rv.Capacity = m.CreatedAt.IsZero(), m.RegisteredAt, m.Weight, m.Capacity
	}
	rv.Full = w.relayFull != nil && w.relayFull(id)
	// A code waiting to set it up: the earliest to run out.
	for _, j := range w.joins {
		if j.Relay == id && j.live(now) && (rv.TokenExpiresAt.IsZero() || j.ExpiresAt.Before(rv.TokenExpiresAt)) {
			rv.TokenExpiresAt = j.ExpiresAt
		}
	}
	for _, l := range w.lans {
		if slices.Contains(l.added, id) && l.joined(p) {
			rv.LANs = append(rv.LANs, l.ID)
		}
	}
	slices.Sort(rv.LANs)
	switch {
	case m == nil || m.SecretHash == "":
		// One that never checks in (the seed's derper): up while a
		// device online says it is connected to it, as the engines
		// see it, rather than "不在线" for want of a sync.
		rv.Online = w.inUse(id, isOnline)
	case w.relayStatus != nil:
		// When it last checked in matters once it is offline; while
		// online it would only make every view differ every minute.
		var seen time.Time
		if rv.Online, seen = w.relayStatus(id); !rv.Online {
			rv.LastSeen = seen
		}
	}
	return rv
}

// inUse reports whether a device online says it is connected to relay
// id: on its Report.Relays, or as its home for an engine from before M4.
func (w *World) inUse(id proto.RelayID, isOnline online) bool {
	if isOnline == nil {
		return false
	}
	for _, d := range w.devices {
		if !d.usable() || !isOnline(d.ID) {
			continue
		}
		if slices.Contains(d.Relays, id) || len(d.Relays) == 0 && d.HomeRelay == id {
			return true
		}
	}
	return false
}

// Relay ops.

// maxRelays is how many relays one person may have.
const maxRelays = 10

// relayJoinLife is how long a join code works: long enough to paste it
// on the server, short enough that a code seen over a shoulder is soon
// worthless (implementation.md §3 M4, the plan's §13).
const relayJoinLife = time.Hour

// maxPendingJoins is how many unused join codes for new relays one
// person may have. A relay has one code to set it up again at most: a
// new one replaces it.
const maxPendingJoins = 5

// newRelayToken is a registration token. It says what it is, since it
// turns up in shell histories and docker inspect; it works once.
func newRelayToken() string { return "deneb-relay-" + strings.ToLower(randomCode(26)) }

// newRelaySecret is a relay's secret: 256 random bits.
func newRelaySecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret is how tokens and secrets are kept: a hash of 256-bit random
// strings needs no salt or stretching.
func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// newJoin writes a join code and returns it with its token, which is in
// the database only as a hash.
func newJoin(tx *sql.Tx, j relayJoin) (*relayJoin, string, error) {
	tok := newRelayToken()
	j.ID, j.TokenHash = newID("j"), hashSecret(tok)
	var relay any
	if j.Relay != "" {
		relay = j.Relay
	}
	_, err := tx.Exec(`INSERT INTO relay_joins (id, owner, relay, source, name, rate_limit, token_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Owner, relay, j.Source, j.Name, j.RateLimit, j.TokenHash, ms(j.CreatedAt), ms(j.ExpiresAt))
	return &j, tok, err
}

// joinView is a join code as relay.join's result carries it: the code,
// and the command that takes it to the server.
func (s *Server) joinView(j *relayJoin, tok string) *proto.RelayJoin {
	code := proto.MakeRelayJoinCode(s.cfg.PublicURL, s.cfg.Pin, tok)
	return &proto.RelayJoin{ID: j.ID, Code: code, Command: s.relayCommand(code), Relay: j.Relay, ExpiresAt: j.ExpiresAt.UTC()}
}

// relayCommand is the one line that sets a relay up on a server with
// Docker: host network, its data in a volume that outlives the container,
// back up by itself after a reboot.
func (s *Server) relayCommand(code string) string {
	// The log is kept to 3 files of 10 MB: it has the addresses that
	// connect, and on the owner's machine it would otherwise grow for as
	// long as the relay runs (PRIVACY.md).
	return "docker run -d --name deneb-relay --restart unless-stopped --network host " +
		"--log-opt max-size=10m --log-opt max-file=3 -v deneb-relay:/data " +
		s.cfg.RelayImage + " --join " + code + " --region auto"
}

// pendingJoins counts p's join codes for new relays that still work.
func (w *World) pendingJoins(p proto.PersonID, now time.Time) int {
	n := 0
	for _, j := range w.joins {
		if j.Owner == p && j.Relay == "" && j.live(now) {
			n++
		}
	}
	return n
}

// myJoins are p's join codes that still work, oldest first.
func (w *World) myJoins(p proto.PersonID, now time.Time) []proto.RelayJoinView {
	out := []proto.RelayJoinView{}
	for _, j := range w.joins {
		if j.Owner == p && j.live(now) {
			out = append(out, proto.RelayJoinView{ID: j.ID, Relay: j.Relay, CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt})
		}
	}
	slices.SortFunc(out, func(a, b proto.RelayJoinView) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// join makes a join code: for a new relay of the asking person's, or a
// new public relay (public, which only an operator asks for), or to set
// relay up again. A public relay's code is no one's (Owner empty), so any
// operator sees it waiting and may take it back.
func (c *opCtx) join(relay proto.RelayID, name string, public bool) (*relayJoin, string, error) {
	owner := c.me
	if public {
		owner = ""
	}
	switch {
	case relay != "":
		// The code from the last "重新安装" stops working.
		if err := c.exec(`DELETE FROM relay_joins WHERE relay = ?`, relay); err != nil {
			return nil, "", err
		}
	case c.w.pendingJoins(owner, c.now) >= maxPendingJoins:
		return nil, "", errInvalid(fmt.Sprintf("已经有 %d 个没用的加入码，先撤销不用的", maxPendingJoins))
	case !public && len(c.w.ownRelays(c.me)) >= maxRelays:
		return nil, "", errInvalid(fmt.Sprintf("最多 %d 个中转站，先删掉一个不用的", maxRelays))
	}
	j := relayJoin{Owner: owner, Relay: relay, Source: proto.RelayServer, Name: name, CreatedAt: c.now, ExpiresAt: c.now.Add(relayJoinLife)}
	if public {
		j.Source = proto.RelayPublic
		if relay == "" {
			j.RateLimit = defaultPublicRateLimit
		}
	}
	return newJoin(c.tx, j)
}

func opRelayJoin(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	name := ""
	if strings.TrimSpace(a.Name) != "" {
		var err error
		if name, err = checkName(a.Name); err != nil {
			return err
		}
	}
	public := a.Public
	if a.Relay != "" {
		rl, err := c.relayToManage(a.Relay, "重新安装", false)
		if err != nil {
			return err
		}
		public = rl.Source == proto.RelayPublic
	} else if public && !c.operator() {
		return errForbidden("只有部署管理员能加公共中转站")
	}
	j, tok, err := c.join(a.Relay, name, public)
	if err != nil {
		return err
	}
	out.RelayJoin = c.s.joinView(j, tok)
	if !public {
		return nil
	}
	text := "为加一台公共中转站生成了安装命令"
	if name != "" {
		text = "为加一台公共中转站「" + name + "」生成了安装命令"
	}
	if rl := c.w.relays[a.Relay]; rl != nil {
		text = "为公共中转站「" + rl.Name + "」重新生成了安装命令"
	}
	return c.recordOps(&proto.Relay{ID: a.Relay, Source: proto.RelayPublic}, "relay.join", text)
}

func opRelayRevokeJoin(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	j := c.w.joins[a.Join]
	// A public relay's code is the operators'.
	mine := j != nil && (j.Owner == c.me || j.Owner == "" && j.Source == proto.RelayPublic && c.operator())
	if !mine || !j.live(c.now) {
		return errNotFound("找不到这个加入码，或者它已经用过、过期了")
	}
	if err := c.exec(`DELETE FROM relay_joins WHERE id = ?`, j.ID); err != nil {
		return err
	}
	if j.Source != proto.RelayPublic {
		return nil
	}
	text := "作废了一条加公共中转站的安装命令"
	if rl := c.w.relays[j.Relay]; rl != nil {
		text = "作废了公共中转站「" + rl.Name + "」的一条安装命令"
	}
	return c.recordOps(&proto.Relay{ID: j.Relay, Source: proto.RelayPublic}, "relay.revokeJoin", text)
}

// opRelayCreate makes the relay first and a code for it (the form the
// engine line's topology uses); relay.join makes it when the code is
// used.
func opRelayCreate(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	name, err := checkName(a.Name)
	if err != nil {
		return err
	}
	if len(c.w.ownRelays(c.me)) >= maxRelays {
		return errInvalid(fmt.Sprintf("最多 %d 个中转站，先删掉一个不用的", maxRelays))
	}
	id := proto.RelayID(newID("r"))
	if err := c.exec(`INSERT INTO relays (id, name, source, owner, host_name, derp_port, created_at)
			VALUES (?, ?, ?, ?, '', 0, ?)`, id, name, proto.RelayServer, c.me, ms(c.now)); err != nil {
		return err
	}
	j, tok, err := c.join(id, "", false)
	if err != nil {
		return err
	}
	out.Relay, out.RelayToken, out.RelayJoin = id, tok, c.s.joinView(j, tok)
	return nil
}

func opRelayToken(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.ownRelay(a.Relay, "重新生成登记口令", true)
	if err != nil {
		return err
	}
	j, tok, err := c.join(rl.ID, "", false)
	if err != nil {
		return err
	}
	out.Relay, out.RelayToken, out.RelayJoin = rl.ID, tok, c.s.joinView(j, tok)
	return nil
}

func opRelayRename(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	rl, err := c.relayToManage(a.Relay, "改名", false)
	if err != nil {
		return err
	}
	name, err := checkName(a.Name)
	if err != nil {
		return err
	}
	if name == rl.Name {
		return errNoChange
	}
	if err := c.exec(`UPDATE relays SET name = ? WHERE id = ?`, name, rl.ID); err != nil {
		return err
	}
	return c.recordOps(rl, "relay.rename", "把公共中转站「"+rl.Name+"」改名叫「"+name+"」")
}

func opRelayDelete(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	if rl := c.w.relays[a.Relay]; rl != nil && rl.Source == proto.RelayPublic {
		// Everyone falls back on the public relays (DESIGN §14.12 M4-13).
		return errForbidden("公共中转站拿不掉：要下线一台，在服务器上停掉它，经它的设备会自动改走别的")
	}
	rl, err := c.ownRelay(a.Relay, "删除", true)
	if err != nil {
		return err
	}
	// Its LANs' lists and its usage go with it (ON DELETE CASCADE).
	return c.exec(`DELETE FROM relays WHERE id = ?`, rl.ID)
}

func opRelayAdd(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	rl, err := c.ownRelay(a.Relay, "拖进局域网", false)
	if err != nil {
		return err
	}
	if slices.Contains(l.added, rl.ID) {
		return errNoChange
	}
	return c.exec(`INSERT INTO lan_relays (lan, relay, position)
		VALUES (?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM lan_relays WHERE lan = ?))`, l.ID, rl.ID, l.ID)
}

func opRelayRemove(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	rl := c.w.relays[a.Relay]
	if rl == nil || !slices.Contains(l.added, rl.ID) {
		return errNotFound("这个局域网里没有这个中转站")
	}
	// The LAN's owner may say no to someone's relay too: its owner sees
	// who connects through it and how much (pricing-and-relays §2.4).
	if rl.Owner != c.me && l.Owner != c.me {
		return errForbidden("只有中转站的主人和局域网的拥有者能把它拿出去")
	}
	return c.exec(`DELETE FROM lan_relays WHERE lan = ? AND relay = ?`, l.ID, rl.ID)
}

// ownRelay finds one of the asking person's relays. Someone else's relay
// that they see answers with whose it is; one they do not see does not
// exist for them. managed refuses a relay from the seed, which the seed
// file manages, for the ops that would change it.
func (c *opCtx) ownRelay(id proto.RelayID, verb string, managed bool) (*proto.Relay, error) {
	rl := c.w.relays[id]
	if rl == nil || !c.seesRelay(rl) {
		return nil, errNotFound("找不到这个中转站")
	}
	if rl.Source == proto.RelayPublic {
		return nil, errForbidden("这是公共中转站，所有局域网都会用上它，不用拖")
	}
	m := c.w.relayMeta[id]
	seeded := m == nil || m.CreatedAt.IsZero()
	// A default relay is on every LAN's list: whose it is stays out of
	// the answer, as it does out of the views.
	if seeded && (managed || rl.Owner != c.me) || slices.Contains(c.w.defaults, id) && rl.Owner != c.me {
		return nil, errForbidden("这是部署时登记的中转站，不能在这里" + verb)
	}
	if rl.Owner != c.me {
		name := "别人"
		if p := c.w.people[rl.Owner]; p != nil {
			name = p.DisplayName
		}
		return nil, errForbidden("这是" + spaced(name) + "的中转站，只有" + spaced(name) + "能" + verb)
	}
	return rl, nil
}

// seesRelay reports whether the asking person sees relay rl: theirs,
// public, or on a list of a LAN they are in.
func (c *opCtx) seesRelay(rl *proto.Relay) bool {
	if rl.Owner == c.me || rl.Source == proto.RelayPublic {
		return true
	}
	for _, l := range c.w.lans {
		if l.joined(c.me) && (slices.Contains(l.relays, rl.ID) || slices.Contains(l.added, rl.ID)) {
			return true
		}
	}
	return false
}

// relayLive is which relays checked in lately. It is not in the world:
// like a device being online, it comes and goes without a change, and a
// flip publishes a new version.
type relayLive struct {
	mu     sync.Mutex
	seen   map[proto.RelayID]time.Time
	online map[proto.RelayID]bool // as last published; missing: never synced
	load   map[proto.RelayID]*relayLoad
	full   map[proto.RelayID]bool // as last published
}

// relayOfflineAfter is how long a relay that stopped syncing (every
// minute) still shows online.
const relayOfflineAfter = 3 * time.Minute

func newRelayLive() *relayLive {
	return &relayLive{seen: map[proto.RelayID]time.Time{}, online: map[proto.RelayID]bool{},
		load: map[proto.RelayID]*relayLoad{}, full: map[proto.RelayID]bool{}}
}

// presume counts a registered relay online from now, when the server
// starts: it hears from it within a minute, and a relay that never syncs
// goes offline after relayOfflineAfter. Without it, a restart would put
// every relay last on every list until its next sync.
func (l *relayLive) presume(id proto.RelayID, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.online[id]; !ok {
		l.online[id], l.seen[id] = true, now
	}
}

// offline reports whether relay id, which syncs, stopped. A relay that
// never syncs (the seed's) never is.
func (l *relayLive) offline(id proto.RelayID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	on, known := l.online[id]
	return known && !on
}

func (l *relayLive) isFull(id proto.RelayID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.full[id]
}

// report keeps a sync's load, and reports whether the relay's being full
// changed.
func (l *relayLive) report(id proto.RelayID, ld *relayLoad, capacity int) (full, changed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load[id] = ld
	was := l.full[id]
	full = isFull(was, ld.devices, capacity, ld.health.CPU)
	l.full[id] = full
	return full, full != was
}

// lastLoad is relay id's last sync's load, or nil.
func (l *relayLive) lastLoad(id proto.RelayID) *relayLoad {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.load[id]
}

// checkIn notes a sync and reports whether the relay just came online.
func (l *relayLive) checkIn(id proto.RelayID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen[id] = now
	if l.online[id] {
		return false
	}
	l.online[id] = true
	return true
}

// expire marks offline the relays not heard from for relayOfflineAfter,
// and returns them.
func (l *relayLive) expire(now time.Time) []proto.RelayID {
	l.mu.Lock()
	defer l.mu.Unlock()
	var gone []proto.RelayID
	for id, on := range l.online {
		if on && now.Sub(l.seen[id]) >= relayOfflineAfter {
			l.online[id] = false
			gone = append(gone, id)
		}
	}
	slices.Sort(gone)
	return gone
}

// status is whether the relay is online, as last published, and when it
// last checked in.
func (l *relayLive) status(id proto.RelayID) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.online[id], l.seen[id]
}
