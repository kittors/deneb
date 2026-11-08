package magicsock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"net/netip"
	"slices"
	"time"

	"golang.org/x/crypto/nacl/box"

	"github.com/kittors/deneb/proto"
)

// Disco messages find and keep direct paths. They are sealed with the disco
// keys from the network map (NaCl box), so only the two devices can read or
// forge them, and they travel over UDP or through the relay.
//
// Wire format: magic | sender's disco public key (32) | nonce (24) | box.
// The prototype sealed them with the WireGuard keys; v1 uses separate disco
// keys, as Tailscale does, so a disco bug can never touch WireGuard's.
const discoMagic = "DNBDS1"

const (
	relayAlive    = 12 * time.Second // a relay pong this recent means the relay path works
	connectingFor = 5 * time.Second  // how long a new peer may stay "connecting"
	pingTimeout   = 2 * time.Second  // a ping older than this without a pong is lost
	lossWindow    = 10 * time.Second // loss is measured over this window
	fullEvery     = 30 * time.Second // ping every candidate this often, even when direct
	cmmEvery      = 5 * time.Second  // call-me-maybe this often while not direct

	// After idle, the path shown stays as it was until the first answer
	// (or this long), so a line does not flash "connecting" each time
	// someone starts using it again.
	wakeGrace = 1500 * time.Millisecond
	// For this long after waking, a packet goes both to the last direct
	// address and through the relay: whichever still works answers first.
	wakeDual = 2 * time.Second

	// Loss-aware choice (M2): a direct path that answers but drops packets
	// gives way to the relay, as on a lossy line TCP stalls even though
	// pings get through (multi-lan-tech §6.8).
	// It is behind Options.PathByLoss, off by default: on real networks a
	// first version flapped on pongs that were merely late (11-08 kernel
	// round). The thresholds below are meant to switch only on a line that
	// stays bad, and to stay put once switched.
	fastEvery   = 250 * time.Millisecond  // ping a busy or lossy direct path this often
	lossDegrade = 0.20                    // sustained loss at this level moves to the relay
	lossRecover = 0.02                    // a clean stretch below this takes the direct path back
	lossMinSent = 16                      // pings judged in the window before deciding
	lossMinLost = 5                       // and lost: a few unlucky pings are not a bad line
	lossSpread  = 1500 * time.Millisecond // lost pings spread over at least this: one short hiccup is not a bad line
	busyFor     = 2 * time.Second         // traffic this recent keeps the direct path under fast pings
	degradeOver = 8 * time.Second         // loss is measured over this (pings judged after pingTimeout)
	minOnRelay  = 20 * time.Second        // once moved to the relay for loss or delay, stay at least this long
	recoverOver = 10 * time.Second        // the clean stretch to come back is measured over this
	recoverMin  = 30                      // with at least this many pings
	slowFor     = 5 * time.Second         // a slow direct path must stay slow this long
)

type pathKind uint8

const (
	kindConnecting pathKind = iota
	kindDirect
	kindRelay
	kindUnreachable
)

func (k pathKind) String() string {
	return [...]string{"connecting", "direct", "relay", "unreachable"}[k]
}

type peerState struct {
	cfg         PeerConfig
	shared      [32]byte // precomputed NaCl box key with the peer's disco key
	ep          *peerEndpoint
	active      bool
	activatedAt time.Time

	cands    map[netip.AddrPort]time.Time // candidate direct addresses, when last seen
	best     netip.AddrPort
	bestRTT  time.Duration
	lastPong time.Time
	lastFull time.Time
	lastCMM  time.Time

	relayRTT      time.Duration
	lastRelayPong time.Time
	lastRelayPing time.Time

	pending map[string]pendingPing
	probes  []probe // recent pings, for loss

	blocked bool // test knob: behave as if UDP between us were blocked

	// pendingInit is the last WireGuard handshake initiation sent while no
	// path to the peer was confirmed. If the peer was not yet on the relay,
	// it was lost, and wireguard-go would wait 5 s to retry; it is sent
	// again the moment the peer first answers. A duplicate is harmless: the
	// responder rejects a replayed initiation by its timestamp.
	pendingInit   []byte
	pendingInitAt time.Time
	initResentAt  time.Time // last time pendingInit went out again

	peerInstance string // the peer engine's instance id, from its disco messages

	lastActive time.Time // last WireGuard packet to or from the peer
	idle       bool      // no traffic for Options.Idle: no heartbeat, path shown as it was
	wokeAt     time.Time // when traffic came back after idle

	degraded   bool          // the direct path works but is lossy or slow: use the relay
	reason     string        // why: "lossy" or "slow"
	degradedAt time.Time     // when it moved to the relay for that
	slowSince  time.Time     // since when the direct path has been much slower than the relay
	srtt       time.Duration // smoothed direct round trip
	pingsTx    uint64        // disco pings sent, direct and relay (tests count them)

	lossTx, lossRx float64 // test knob: drop this share of UDP to / from the peer

	kind         pathKind
	kindEndpoint netip.AddrPort
	kindRelay    proto.RelayID
	since        time.Time
	prevKind     pathKind // the kind before this one: "relay first, then direct"

	txDirect, txRelay, rxDirect, rxRelay uint64
}

type pendingPing struct {
	to    netip.AddrPort // invalid for a relay ping
	relay bool
	at    time.Time
}

type probe struct {
	tx    string
	to    netip.AddrPort
	relay bool
	at    time.Time
	acked bool
}

type discoMsg struct {
	T     string   `json:"t"`             // ping | pong | cmm (call me maybe)
	Tx    string   `json:"tx,omitempty"`  // ping/pong transaction id
	Src   string   `json:"src,omitempty"` // pong over UDP: where the ping came from, as the responder saw it
	Eps   []string `json:"eps,omitempty"` // cmm: the sender's candidate endpoints
	Relay bool     `json:"r,omitempty"`   // ping/pong over the relay
	// Instance is the sender engine's id for this start. When it changes,
	// the peer restarted and lost its WireGuard sessions.
	Instance string `json:"i,omitempty"`
}

func isDisco(b []byte) bool {
	return len(b) > len(discoMagic) && string(b[:len(discoMagic)]) == discoMagic
}

func (c *Conn) seal(p *peerState, m discoMsg) []byte {
	m.Instance = c.instance
	plain, _ := json.Marshal(m)
	var nonce [24]byte
	rand.Read(nonce[:])
	out := make([]byte, 0, len(discoMagic)+32+24+len(plain)+box.Overhead)
	out = append(out, discoMagic...)
	out = append(out, c.discoPub[:]...)
	out = append(out, nonce[:]...)
	return box.SealAfterPrecomputation(out, plain, &nonce, &p.shared)
}

func (c *Conn) sendUDPDisco(p *peerState, to netip.AddrPort, m discoMsg) {
	c.mu.Lock()
	blocked := p.blocked || p.lossTx > 0 && mrand.Float64() < p.lossTx
	c.mu.Unlock()
	if blocked {
		return
	}
	c.sock().WriteToUDPAddrPort(c.seal(p, m), to)
}

func (c *Conn) sendRelayDisco(p *peerState, m discoMsg) {
	c.sendRelay(p, c.seal(p, m))
}

func newTx() string {
	var tx [12]byte
	rand.Read(tx[:])
	return hex.EncodeToString(tx[:])
}

func (c *Conn) recordLocked(p *peerState, pr probe) {
	p.pending[pr.tx] = pendingPing{to: pr.to, relay: pr.relay, at: pr.at}
	p.probes = append(p.probes, pr)
	if len(p.probes) > 96 {
		p.probes = p.probes[len(p.probes)-64:]
	}
}

func (c *Conn) sendPing(p *peerState, to netip.AddrPort) {
	tx := newTx()
	c.mu.Lock()
	c.recordLocked(p, probe{tx: tx, to: to, at: time.Now()})
	p.pingsTx++
	c.mu.Unlock()
	c.sendUDPDisco(p, to, discoMsg{T: "ping", Tx: tx})
}

// sendRelayPing measures the relay path: whether the peer answers there,
// and how fast. Without it, "via the relay" would be a guess.
func (c *Conn) sendRelayPing(p *peerState) {
	tx := newTx()
	c.mu.Lock()
	now := time.Now()
	p.lastRelayPing = now
	c.recordLocked(p, probe{tx: tx, relay: true, at: now})
	p.pingsTx++
	c.mu.Unlock()
	c.sendRelayDisco(p, discoMsg{T: "ping", Tx: tx, Relay: true})
}

func (c *Conn) sendCMM(p *peerState) {
	eps := c.Endpoints()
	strs := make([]string, len(eps))
	for i, e := range eps {
		strs[i] = e.String()
	}
	c.mu.Lock()
	p.lastCMM = time.Now()
	c.mu.Unlock()
	c.sendRelayDisco(p, discoMsg{T: "cmm", Eps: strs})
}

// handleDisco handles a disco message from UDP (src set) or from a relay
// (relay set, relaySrc is the node key the relay vouches for).
func (c *Conn) handleDisco(b []byte, src netip.AddrPort, relay proto.RelayID, relaySrc proto.NodePublic) {
	const hdr = len(discoMagic) + 32 + 24
	if len(b) < hdr+box.Overhead {
		return
	}
	var sender proto.DiscoPublic
	copy(sender[:], b[len(discoMagic):len(discoMagic)+32])
	viaRelay := relay != ""
	c.mu.Lock()
	var p *peerState
	if viaRelay {
		// The relay vouches for the node key; the disco key must be the
		// one the network map gives for it.
		if q := c.peers[relaySrc]; q != nil && q.cfg.DiscoKey == sender {
			p = q
		}
	} else {
		p = c.byDisco[sender]
	}
	if p == nil || !p.active || (!viaRelay && (p.blocked || p.lossRx > 0 && mrand.Float64() < p.lossRx)) {
		// Devices that share no active scope with us get no answer at
		// all: they cannot even learn where this device can be reached.
		c.mu.Unlock()
		return
	}
	shared := p.shared
	c.mu.Unlock()
	var nonce [24]byte
	copy(nonce[:], b[len(discoMagic)+32:hdr])
	plain, ok := box.OpenAfterPrecomputation(nil, b[hdr:], &nonce, &shared)
	if !ok {
		return
	}
	var m discoMsg
	if json.Unmarshal(plain, &m) != nil {
		return
	}
	if m.Instance != "" {
		c.mu.Lock()
		restarted := p.peerInstance != "" && p.peerInstance != m.Instance
		p.peerInstance = m.Instance
		c.mu.Unlock()
		if restarted && c.opts.OnPeerRestart != nil {
			// Without this, traffic we start would go on with a session the
			// peer forgot, until WireGuard gives up on it after 15 seconds.
			c.logf("[path] %s restarted; starting a new handshake", p.cfg.Label)
			c.opts.OnPeerRestart(p.cfg.NodeKey)
		}
	}
	switch m.T {
	case "ping":
		if viaRelay {
			c.sendRelayDisco(p, discoMsg{T: "pong", Tx: m.Tx, Relay: true})
			return
		}
		c.learn(p, src)
		c.sendUDPDisco(p, src, discoMsg{T: "pong", Tx: m.Tx, Src: src.String()})
		if !c.directTrusted(p) {
			c.sendPing(p, src) // they reach us from src: punch back right away
		}
	case "pong":
		if viaRelay {
			c.handleRelayPong(p, m)
		} else {
			c.handlePong(p, m, src)
		}
	case "cmm":
		for _, s := range m.Eps {
			if ap, err := netip.ParseAddrPort(s); err == nil {
				c.learn(p, ap)
				c.sendPing(p, ap)
			}
		}
	}
}

func (c *Conn) learn(p *peerState, ap netip.AddrPort) {
	c.mu.Lock()
	p.cands[ap] = time.Now()
	c.byAddr[ap] = p
	c.mu.Unlock()
}

func (c *Conn) ack(p *peerState, tx string) {
	for i := len(p.probes) - 1; i >= 0; i-- {
		if p.probes[i].tx == tx {
			p.probes[i].acked = true
			return
		}
	}
}

func (c *Conn) handlePong(p *peerState, m discoMsg, src netip.AddrPort) {
	now := time.Now()
	c.mu.Lock()
	pp, ok := p.pending[m.Tx]
	if !ok || pp.relay || pp.to != src {
		c.mu.Unlock()
		return
	}
	delete(p.pending, m.Tx)
	c.ack(p, m.Tx)
	rtt := now.Sub(pp.at)
	p.cands[src] = now
	c.byAddr[src] = p
	trusted := c.directTrustedLocked(p, now)
	if !trusted {
		// The direct path was dead and answers again: what it lost while
		// dead says nothing about how lossy it is now.
		p.probes = slices.DeleteFunc(p.probes, func(pr probe) bool { return !pr.relay })
	}
	if src == p.best || !trusted {
		if p.srtt == 0 || src != p.best {
			p.srtt = rtt
		} else {
			p.srtt = (p.srtt*7 + rtt) / 8
		}
	}
	var ev string
	switch {
	case !trusted:
		if p.best != src {
			ev = fmt.Sprintf("direct path found: %s rtt=%s", src, rtt.Round(100*time.Microsecond))
		}
		p.best, p.bestRTT, p.lastPong = src, rtt, now
	case src == p.best:
		p.bestRTT, p.lastPong = rtt, now
	case rtt < p.bestRTT*8/10:
		// Clearly better, e.g. both devices turn out to be on one Wi-Fi.
		ev = fmt.Sprintf("better direct path: %s rtt=%s (was %s)", src, rtt.Round(100*time.Microsecond), p.best)
		p.best, p.bestRTT, p.lastPong = src, rtt, now
	}
	pongSelfChanged := false
	if self, err := netip.ParseAddrPort(m.Src); err == nil && self != c.pongSelf {
		c.pongSelf, pongSelfChanged = self, true
	}
	label := p.cfg.Label
	init := c.takePendingInitLocked(p, now)
	best, blocked := p.best, p.blocked
	c.mu.Unlock()
	if init != nil && !blocked {
		c.sock().WriteToUDPAddrPort(init, best)
	}
	if ev != "" {
		c.logf("[path] %s: %s", label, ev)
	}
	c.updatePath(p)
	if pongSelfChanged {
		c.noteEndpoints()
	}
}

func (c *Conn) handleRelayPong(p *peerState, m discoMsg) {
	now := time.Now()
	c.mu.Lock()
	pp, ok := p.pending[m.Tx]
	if !ok || !pp.relay {
		c.mu.Unlock()
		return
	}
	delete(p.pending, m.Tx)
	c.ack(p, m.Tx)
	p.relayRTT = now.Sub(pp.at)
	p.lastRelayPong = now
	init := c.takePendingInitLocked(p, now)
	c.mu.Unlock()
	if init != nil {
		c.sendRelay(p, init)
	}
	c.updatePath(p)
}

// takePendingInitLocked returns the handshake initiation to send again on
// an answer from the peer. It stays pending until the peer's handshake
// response arrives (or 5 s pass): the first copy may reach a peer that has
// just put us back in its map but not yet in WireGuard, and be dropped.
func (c *Conn) takePendingInitLocked(p *peerState, now time.Time) []byte {
	b := p.pendingInit
	if b == nil || now.Sub(p.pendingInitAt) > 5*time.Second {
		p.pendingInit = nil
		return nil
	}
	if now.Sub(p.initResentAt) < 100*time.Millisecond {
		return nil
	}
	p.initResentAt = now
	return b
}

// noteHandshakeResponseLocked: the peer answered our handshake, nothing is
// pending any more.
func noteHandshakeResponseLocked(p *peerState, b []byte) {
	if len(b) == 92 && b[0] == 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		p.pendingInit = nil
	}
}

func (c *Conn) directTrusted(p *peerState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.directTrustedLocked(p, time.Now())
}

// directTrustedLocked deliberately ignores the test-only blocked flag: when
// a network starts dropping UDP nobody is told, so the switch to the relay
// must come from missing pongs, exactly as it would in the field.
func (c *Conn) directTrustedLocked(p *peerState, now time.Time) bool {
	return p.best.IsValid() && now.Sub(p.lastPong) < c.opts.Trust
}

func (c *Conn) kindLocked(p *peerState, now time.Time) (pathKind, netip.AddrPort, proto.RelayID) {
	direct := c.directTrustedLocked(p, now)
	relayUp := c.relayUpLocked(p, now)
	if direct && !(p.degraded && relayUp) {
		return kindDirect, p.best, ""
	}
	if relayUp {
		return kindRelay, netip.AddrPort{}, c.relayForLocked(p)
	}
	if direct { // lossy, but the relay is down: a poor path beats none
		return kindDirect, p.best, ""
	}
	if now.Sub(p.activatedAt) < connectingFor {
		return kindConnecting, netip.AddrPort{}, ""
	}
	return kindUnreachable, netip.AddrPort{}, ""
}

func (c *Conn) relayUpLocked(p *peerState, now time.Time) bool {
	return !p.lastRelayPong.IsZero() && now.Sub(p.lastRelayPong) < relayAlive
}

// sendDirectLocked is whether WireGuard packets go direct right now.
func (c *Conn) sendDirectLocked(p *peerState, now time.Time) bool {
	return c.directTrustedLocked(p, now) && !(p.degraded && c.relayUpLocked(p, now))
}

// noteTrafficLocked marks a WireGuard packet to or from p. It reports
// whether p was idle; the caller then wakes it.
func (c *Conn) noteTrafficLocked(p *peerState, now time.Time) bool {
	p.lastActive = now
	if !p.idle {
		return false
	}
	p.idle, p.wokeAt = false, now
	p.activatedAt = now // nothing answering yet means "connecting", not "unreachable"
	return true
}

// wake probes a peer whose traffic just came back: the relay, a
// call-me-maybe with our endpoints, and every address we know for it.
func (c *Conn) wake(p *peerState) {
	c.logf("[idle] %s: traffic again, probing", p.cfg.Label)
	c.probe(p)
}

// frozenLocked: the path shown does not change while the peer is idle, nor
// right after it wakes, until something answers.
func (c *Conn) frozenLocked(p *peerState, now time.Time) bool {
	if p.idle {
		return true
	}
	if p.kind == kindConnecting || p.wokeAt.IsZero() || now.Sub(p.wokeAt) >= wakeGrace {
		return false
	}
	if p.kind == kindDirect {
		// Shown direct: wait for the direct path's answer. The relay's
		// often comes first, and the line would flash "relay" for nothing.
		return p.lastPong.Before(p.wokeAt)
	}
	return p.lastPong.Before(p.wokeAt) && p.lastRelayPong.Before(p.wokeAt)
}

// judgeLocked weighs the direct path against the relay by loss and delay,
// with hysteresis so the choice does not flap. It returns a line for the
// log when the choice changes. Off unless Options.PathByLoss.
func (c *Conn) judgeLocked(p *peerState, now time.Time) string {
	if !c.opts.PathByLoss || !p.best.IsValid() || !c.directTrustedLocked(p, now) {
		// No direct path, or a dead one (no pong for Trust): that is the
		// relay anyway, and when it answers again it is judged afresh.
		if p.degraded {
			p.degraded, p.reason = false, ""
		}
		p.slowSince = time.Time{}
		return ""
	}
	direct := func(pr probe) bool { return !pr.relay && pr.to == p.best }
	// A ping counts as lost only after pingTimeout: on Wi-Fi a pong is
	// often late, and late is not lost.
	sent, lost, spread := countLossSpread(p.probes, now, degradeOver, pingTimeout, direct)
	loss := 0.0
	if sent > 0 {
		loss = float64(lost) / float64(sent)
	}
	relayUp := c.relayUpLocked(p, now)
	relayLoss := 0.0
	if s, l, _ := countLossSpread(p.probes, now, degradeOver, pingTimeout, func(pr probe) bool { return pr.relay }); s > 0 {
		relayLoss = float64(l) / float64(s)
	}
	if p.srtt > 0 && p.relayRTT > 0 && p.srtt > 2*p.relayRTT+50*time.Millisecond {
		if p.slowSince.IsZero() {
			p.slowSince = now
		}
	} else {
		p.slowSince = time.Time{}
	}
	switch {
	case !p.degraded && relayUp && sent >= lossMinSent && lost >= lossMinLost && loss >= lossDegrade &&
		spread >= lossSpread && relayLoss <= loss/2:
		p.degraded, p.reason, p.degradedAt = true, "lossy", now
		return fmt.Sprintf("direct path loses %.0f%% of pings (%d of %d over %s); using the relay", loss*100, lost, sent, spread.Round(100*time.Millisecond))
	case !p.degraded && relayUp && !p.slowSince.IsZero() && now.Sub(p.slowSince) >= slowFor:
		p.degraded, p.reason, p.degradedAt = true, "slow", now
		return fmt.Sprintf("direct path takes %s, the relay %s; using the relay", p.srtt.Round(time.Millisecond), p.relayRTT.Round(time.Millisecond))
	case p.degraded && now.Sub(p.degradedAt) < minOnRelay:
		return "" // stay put for a while: no flapping
	case p.degraded && p.reason == "lossy":
		rsent, rlost, _ := countLossSpread(p.probes, now, recoverOver, pingTimeout, direct)
		if rsent >= recoverMin && float64(rlost) <= lossRecover*float64(rsent) {
			p.degraded, p.reason = false, ""
			return fmt.Sprintf("direct path clean again (%d of %d lost); back to direct", rlost, rsent)
		}
	case p.degraded && p.reason == "slow" && p.srtt < p.relayRTT*3/2+30*time.Millisecond:
		p.degraded, p.reason = false, ""
		return "direct path fast again; back to direct"
	}
	return ""
}

// updatePath recomputes the peer's path and reports a change the moment it
// happens, so measurements can line packet loss up with the switch.
func (c *Conn) updatePath(p *peerState) {
	now := time.Now()
	c.mu.Lock()
	if !p.active || c.frozenLocked(p, now) {
		c.mu.Unlock()
		return
	}
	k, ep, relay := c.kindLocked(p, now)
	if k == p.kind && ep == p.kindEndpoint && relay == p.kindRelay {
		c.mu.Unlock()
		return
	}
	old := p.kind
	if k != p.kind {
		p.since = now
		p.prevKind = old
	}
	p.kind, p.kindEndpoint, p.kindRelay = k, ep, relay
	label, id := p.cfg.Label, p.cfg.ID
	if k != kindDirect && old == kindDirect {
		// Trust just ran out: learn the relay's state now rather than
		// at the next heartbeat.
		go c.sendRelayPing(p)
	}
	c.mu.Unlock()
	if k != old {
		detail := k.String()
		switch k {
		case kindDirect:
			detail += " via " + ep.String()
		case kindRelay:
			detail += " via " + string(relay)
		}
		c.logf("[switch] %s: %s -> %s", label, old, detail)
	}
	if c.opts.OnPath != nil {
		c.opts.OnPath(id)
	}
}

func (c *Conn) heartbeatLoop() {
	t := time.NewTicker(c.opts.Heartbeat)
	defer t.Stop()
	fast := time.NewTicker(fastEvery)
	defer fast.Stop()
	hs := time.NewTicker(100 * time.Millisecond)
	defer hs.Stop()
	lastWall := time.Now().Round(0) // wall clock only: it runs on while the machine sleeps
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			wall := time.Now().Round(0)
			if jump := wall.Sub(lastWall); jump > 5*c.opts.Heartbeat && jump > 10*time.Second && c.opts.OnTimeJump != nil {
				c.logf("[net] %s passed between two heartbeats: the machine slept", jump.Round(time.Second))
				c.opts.OnTimeJump(jump)
			}
			lastWall = wall
			c.heartbeat()
		case <-fast.C:
			c.fastProbe()
		case <-hs.C:
			c.resendInits()
		}
	}
}

// resendInits sends a handshake initiation again when the peer's response
// has not come within a few round trips (at most for 5 s; WireGuard itself
// retries after that). A copy that arrives twice is refused by the peer as
// a replay, so this only helps when the initiation itself was lost.
func (c *Conn) resendInits() {
	now := time.Now()
	type job struct {
		p     *peerState
		b     []byte
		to    netip.AddrPort
		relay bool
	}
	var jobs []job
	c.mu.Lock()
	for _, p := range c.peers {
		if !p.active || p.pendingInit == nil || now.Sub(p.pendingInitAt) > 5*time.Second {
			continue
		}
		every := min(max(3*p.srtt, 150*time.Millisecond), time.Second)
		if p.srtt == 0 {
			every = 300 * time.Millisecond
		}
		if now.Sub(p.initResentAt) < every || now.Sub(p.pendingInitAt) < every {
			continue
		}
		p.initResentAt = now
		j := job{p: p, b: p.pendingInit, relay: !c.sendDirectLocked(p, now)}
		if c.sendDirectLocked(p, now) || (p.best.IsValid() && !p.blocked) {
			j.to = p.best
		}
		jobs = append(jobs, j)
	}
	c.mu.Unlock()
	for _, j := range jobs {
		if j.to.IsValid() {
			c.sock().WriteToUDPAddrPort(j.b, j.to)
		}
		if j.relay {
			c.sendRelay(j.p, j.b)
		}
	}
}

// lossSeenLocked: the direct path lost a ping lately, so it is measured
// more closely until it is judged.
func (p *peerState) lossSeenLocked(now time.Time) bool {
	_, lost := countLoss(p.probes, now, degradeOver, pingTimeout, func(pr probe) bool { return !pr.relay && pr.to == p.best })
	return lost > 0
}

// countLossSpread counts like countLoss, but a run of lost pings in a row
// counts for at most two: a hiccup (a second of nothing) is not a lossy
// line, scattered losses are. spread is how far apart in time the first and
// the last lost ping were.
func countLossSpread(ps []probe, now time.Time, window, judgeAfter time.Duration, match func(probe) bool) (sent, lost int, spread time.Duration) {
	var first, last time.Time
	run := 0
	for _, pr := range ps { // in the order sent
		age := now.Sub(pr.at)
		if age > window || age < judgeAfter || !match(pr) {
			continue
		}
		sent++
		if pr.acked {
			run = 0
			continue
		}
		run++
		if run <= 2 {
			lost++
		}
		if first.IsZero() || pr.at.Before(first) {
			first = pr.at
		}
		if pr.at.After(last) {
			last = pr.at
		}
	}
	if !first.IsZero() {
		spread = last.Sub(first)
	}
	return sent, lost, spread
}

// lostAfter is how long an unanswered ping on the direct path is given
// before it counts as lost: four round trips, at least 250 ms, at most
// pingTimeout. Waiting the full 2 s for every ping would take a lossy line
// many seconds to show.
func (p *peerState) lostAfter() time.Duration {
	return min(max(4*p.srtt, 250*time.Millisecond), pingTimeout)
}

func (p *peerState) relayLostAfter() time.Duration {
	return min(max(4*p.relayRTT, 500*time.Millisecond), pingTimeout)
}

// fastProbe pings direct paths four times a second while traffic runs over
// them, or while they are under suspicion, so a lossy line is told from a
// lucky one within seconds: TCP backs off further every second it waits.
func (c *Conn) fastProbe() {
	if !c.opts.PathByLoss {
		return
	}
	now := time.Now()
	var ps []*peerState
	c.mu.Lock()
	for _, p := range c.peers {
		busy := now.Sub(p.lastActive) < busyFor // TCP running over it: a bad line must show fast
		if p.active && !p.idle && p.best.IsValid() && (busy || p.degraded || p.lossSeenLocked(now)) {
			ps = append(ps, p)
		}
	}
	var judged []string
	for _, p := range ps {
		if msg := c.judgeLocked(p, now); msg != "" {
			judged = append(judged, p.cfg.Label+": "+msg)
		}
	}
	c.mu.Unlock()
	for _, l := range judged {
		c.logf("[path] %s", l)
	}
	for _, p := range ps {
		c.mu.Lock()
		best := p.best
		c.mu.Unlock()
		c.sendPing(p, best)
		c.updatePath(p)
	}
}

func (c *Conn) heartbeat() {
	now := time.Now()
	type job struct {
		p          *peerState
		trusted    bool
		best       netip.AddrPort
		cands      []netip.AddrPort
		cmm, relay bool
	}
	var jobs []job
	var idled, judged []string
	c.mu.Lock()
	for _, p := range c.peers {
		if !p.active {
			continue
		}
		for id, pp := range p.pending {
			if now.Sub(pp.at) > 5*time.Second {
				delete(p.pending, id)
			}
		}
		if !p.idle && now.Sub(p.lastActive) > c.opts.Idle {
			p.idle = true
			idled = append(idled, p.cfg.Label)
		}
		if p.idle {
			continue // no traffic, no pings: the path shown stays as it was
		}
		if msg := c.judgeLocked(p, now); msg != "" {
			judged = append(judged, p.cfg.Label+": "+msg)
		}
		j := job{p: p, trusted: c.directTrustedLocked(p, now), best: p.best}
		if !j.trusted || now.Sub(p.lastFull) > fullEvery {
			p.lastFull = now
			for a, seen := range p.cands {
				if now.Sub(seen) < 2*time.Minute || a == p.best {
					j.cands = append(j.cands, a)
				}
			}
		}
		j.cmm = !j.trusted && now.Sub(p.lastCMM) > cmmEvery
		every := 5 * time.Second // keeps the relay path known while direct
		if !j.trusted {
			every = 2 * c.opts.Heartbeat
		}
		if c.opts.PathByLoss && (p.degraded || p.lossSeenLocked(now)) {
			every = c.opts.Heartbeat // judging the relay needs its loss too
		}
		j.relay = now.Sub(p.lastRelayPing) >= every
		jobs = append(jobs, j)
	}
	c.mu.Unlock()
	for _, l := range idled {
		c.logf("[idle] %s: no traffic for %s; heartbeat stopped", l, c.opts.Idle)
	}
	c.mu.Lock()
	closed := c.closeIdleRelaysLocked(now)
	c.mu.Unlock()
	for _, n := range closed {
		c.logf("[relay] closed the unused connection to %s", n)
	}
	for _, l := range judged {
		c.logf("[path] %s", l)
	}
	for _, j := range jobs {
		c.updatePath(j.p)
		if j.trusted {
			c.sendPing(j.p, j.best) // keeps NAT mappings open and the path trusted
		}
		for _, a := range j.cands {
			if a != j.best || !j.trusted {
				c.sendPing(j.p, a)
			}
		}
		if j.cmm {
			c.sendCMM(j.p)
		}
		if j.relay {
			c.sendRelayPing(j.p)
		}
	}
}

// PathStatus is one peer's path as this device sees it right now.
type PathStatus struct {
	Kind       string // "direct", "relay", "connecting" or "unreachable"
	Endpoint   netip.AddrPort
	Relay      proto.RelayID
	RTT        time.Duration
	Loss       float32 // over the last 10 s on the path in use
	Since      time.Time
	Candidates []netip.AddrPort
	Blocked    bool
	Idle       bool   // no traffic lately: no heartbeat, Kind is the last one seen
	Reason     string // why the relay although direct works: "lossy" or "slow"
	Prev       string // the kind before this one, however briefly it lasted

	TxDirect, TxRelay, RxDirect, RxRelay uint64
	PingsTx                              uint64
}

// Status reports the path to the peer with node key k.
func (c *Conn) Status(k proto.NodePublic) (PathStatus, bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.peers[k]
	if p == nil || !p.active {
		return PathStatus{}, false
	}
	kind, ep, relay := c.kindLocked(p, now)
	if c.frozenLocked(p, now) {
		kind, ep, relay = p.kind, p.kindEndpoint, p.kindRelay
	}
	st := PathStatus{Kind: kind.String(), Endpoint: ep, Relay: relay, Since: p.since, Blocked: p.blocked,
		Idle: p.idle, Prev: p.prevKind.String(), TxDirect: p.txDirect, TxRelay: p.txRelay, RxDirect: p.rxDirect, RxRelay: p.rxRelay, PingsTx: p.pingsTx}
	if kind == kindRelay && p.degraded {
		st.Reason = p.reason
	}
	if kind != p.kind {
		st.Since = now // updatePath has not run yet
	}
	switch kind {
	case kindDirect:
		st.RTT = p.bestRTT
		if p.srtt > 0 {
			st.RTT = p.srtt
		}
		st.Loss = lossOf(p.probes, now, func(pr probe) bool { return !pr.relay && pr.to == p.best })
	case kindRelay:
		st.RTT = p.relayRTT
		st.Loss = lossOf(p.probes, now, func(pr probe) bool { return pr.relay })
	}
	for a := range p.cands {
		st.Candidates = append(st.Candidates, a)
	}
	return st, true
}

func lossOf(ps []probe, now time.Time, match func(probe) bool) float32 {
	sent, lost := countLoss(ps, now, lossWindow, pingTimeout, match)
	if sent == 0 {
		return 0
	}
	return float32(lost) / float32(sent)
}

// countLoss counts the pings of the last window that are old enough to be
// judged (judgeAfter), and those of them nobody answered.
func countLoss(ps []probe, now time.Time, window, judgeAfter time.Duration, match func(probe) bool) (sent, lost int) {
	for _, pr := range ps {
		age := now.Sub(pr.at)
		if age > window || age < judgeAfter || !match(pr) {
			continue
		}
		sent++
		if !pr.acked {
			lost++
		}
	}
	return sent, lost
}

// SendPath says how a packet to k would go right now: "direct" with the
// endpoint, or "relay" with the relay.
func (c *Conn) SendPath(k proto.NodePublic) (kind string, ep netip.AddrPort, relay proto.RelayID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.peers[k]
	if p == nil {
		return "", netip.AddrPort{}, ""
	}
	if c.sendDirectLocked(p, time.Now()) {
		return "direct", p.best, ""
	}
	return "relay", netip.AddrPort{}, c.relayForLocked(p)
}

// SetBlocked is the test knob of acceptance item 10: behave as if every UDP
// packet between this device and k were lost, in both directions. It
// touches no firewall, and neither end is told.
func (c *Conn) SetBlocked(k proto.NodePublic, on bool) bool {
	c.mu.Lock()
	p := c.peers[k]
	if p != nil {
		p.blocked = on
	}
	c.mu.Unlock()
	if p == nil {
		return false
	}
	c.logf("[test] %s: UDP blocked=%v", p.cfg.Label, on)
	c.updatePath(p)
	return true
}

// SetLoss is the test knob of M2's "bad line": drop this share of UDP
// packets, WireGuard's and disco's, to (tx) and from (rx) the peer k. The
// relay is untouched, like the TCP connection to a real relay would be.
func (c *Conn) SetLoss(k proto.NodePublic, tx, rx float64) bool {
	c.mu.Lock()
	p := c.peers[k]
	if p != nil {
		p.lossTx, p.lossRx = tx, rx
	}
	c.mu.Unlock()
	if p == nil {
		return false
	}
	c.logf("[test] %s: UDP loss %.0f%% out, %.0f%% in", p.cfg.Label, tx*100, rx*100)
	return true
}
