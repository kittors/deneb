package magicsock

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"net/netip"
	"slices"
	"sync/atomic"
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
	dupMaxSize    = 512              // WireGuard packets up to this size (keystrokes, ACKs, small answers) go twice
	dupOn         = 0.05             // on a direct path that loses this much over lossWindow
	dupMinLost    = 3                // and at least this many heartbeat pings: one or two unlucky ones on a clean line are not it
	dupOff        = 0.02             // until it is back under this
	// The loss shown (PathStatus.Loss) is over lossWindow, and only once
	// lossMinJudged heartbeat pings were judged in it: over 10 s, one ping
	// lost in eight read as "12%" and two as "25%" right after a start.
	lossWindow    = 30 * time.Second
	lossMinJudged = 20
	fullEvery     = 30 * time.Second // ping every candidate this often, even when direct
	cmmEvery      = 5 * time.Second  // call-me-maybe this often while not direct
	cmmFresh      = 12 * time.Second // a call-me-maybe after this long without one: the peer starts over

	// After idle, the path shown stays as it was until the first answer
	// (or this long), so a line does not flash "connecting" each time
	// someone starts using it again.
	wakeGrace = 1500 * time.Millisecond
	// For this long after waking, a packet goes both to the last direct
	// address and through the relay: whichever still works answers first.
	// Small packets go through the relay as well even once the direct
	// path has answered (sendWG).
	wakeDual = WakeDual
	// A handshake initiation unanswered this long when a relay comes up is
	// started over (restartUnansweredHandshakes): a response through the
	// relay would have come by then.
	staleInit = 100 * time.Millisecond
	// A line idle on the relay because the direct path lost too much gets
	// one direct ping every idleRecheck; once the direct path answered all
	// of at least idleCleanPings over idleCleanFor, it is direct again
	// (liftIdleVerdict).
	idleRecheck    = 4 * time.Second
	idleCleanFor   = 20 * time.Second
	idleCleanPings = 4
	// An idle line shown on the relay or as unreachable is asked again
	// Options.IdleReprobe after it idled, then twice as long each time up to
	// this (reprobeDueLocked).
	idleReprobeMax = 5 * time.Minute
	// A direct address that drops within flakyWithin of being taken up,
	// twice within flakyForget, is not taken up again for a while as long as
	// the relay works (noteDropLocked): from Options.FlakyBackoff, twice as
	// long each time, up to flakyBackoffMax. The hairpin through the home's
	// public address (11-09) answered a few seconds, then nothing, then
	// again: under traffic the line went direct, relay, direct. Dropped
	// once, it comes back at once (UDP blocked and let through, M2 item 2);
	// a minute without a drop clears it.
	flakyWithin     = time.Minute
	flakyForget     = 10 * time.Minute
	flakyBackoffMax = 30 * time.Minute

	// Loss-aware choice (M2): a direct path that answers but drops packets
	// gives way to the relay, as on a lossy line TCP stalls even though
	// pings get through (multi-lan-tech §6.8).
	// It is behind Options.PathByLoss, off by default: on real networks a
	// first version flapped on pongs that were merely late (11-08 kernel
	// round). The thresholds below are meant to switch only on a line that
	// stays bad, and to stay put once switched.
	fastEvery    = 250 * time.Millisecond  // ping a busy or lossy direct path this often
	lossDegrade  = 0.15                    // sustained loss at this level moves to the relay (if the relay is clearly cleaner)
	lossRecover  = 0.02                    // a clean stretch below this takes the direct path back
	lossMinSent  = 16                      // pings judged in the window before deciding
	lossMinLost  = 5                       // and lost: a few unlucky pings are not a bad line
	lossSpread   = 1500 * time.Millisecond // lost pings spread over at least this: one short hiccup is not a bad line
	busyFor      = 2 * time.Second         // traffic this recent keeps the direct path under fast pings
	busyTrust    = time.Second             // trust under traffic (see trustForLocked)
	busyHoldMore = 2 * time.Second         // and the hold beyond it
	degradeOver  = 8 * time.Second         // loss is measured over this (pings judged after pingTimeout)
	// The relay must be clearly cleaner than the direct path to be worth
	// it: answered late counts as bad on both (the relay is TCP: on a line
	// that loses TCP too, its pings come back late rather than not at all).
	lateBy          = 150 * time.Millisecond // an answer this much over the path's best is late
	relayMinJudged  = 30                     // relay answers on record before the relay may be called clean
	relayCleanMax   = 0.03                   // and at most this share of the record bad: a clean relay, not a lucky one
	relayHistLen    = 120                    // the record: the relay's last answers, across windows and minutes
	relayJudgeEvery = 125 * time.Millisecond // how often the relay is pinged while a line is judged
	minOnRelay      = 20 * time.Second       // once moved to the relay for loss or delay, stay at least this long
	recoverOver     = 10 * time.Second       // the clean stretch to come back is measured over this
	recoverMin      = 30                     // with at least this many pings
	slowFor         = 5 * time.Second        // a slow direct path must stay slow this long
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
	pingsRelay uint64        // of which through the relay

	lossTx, lossRx float64 // test knob: drop this share of UDP to / from the peer
	// relayLate, relayLateBy: test knob: this share of the relay's pongs
	// come relayLateBy late, as on a line that loses TCP too.
	relayLate   float64
	relayLateBy time.Duration

	// relayHist is the relay's record: its last relayHistLen answers,
	// true for bad (late or lost). A few seconds of pings can look clean
	// by luck on a relay that is late one time in ten (the line to Hong
	// Kong); its record over minutes cannot.
	relayHist  [relayHistLen]bool
	relayHistN int // entries in use
	relayHistI int // next to write

	// holding: the direct path missed its pongs for Trust but not yet for
	// Hold, so packets go both ways (see directHeldLocked).
	holding   bool
	holdFrom  time.Time   // the last pong before the silence
	relayBusy atomic.Bool // a disco send through the relay is under way (relayAsync)

	dup   bool   // small packets go twice on the direct path (Options.DupSmall)
	txDup uint64 // packets sent a second time

	txWake       uint64        // small packets also sent through the relay just after waking (sendWG)
	txHSBoth     uint64        // handshake messages sent both direct and through the relay (sendWG, dualHS)
	rxHSDupes    uint64        // repeated handshake messages from this peer, dropped (deliver)
	freshAt      time.Time     // woke, resumed, the network changed, or the peer called (cmm): the direct path is unproven
	idleCheck    time.Time     // the last direct ping of a line idle on the relay for loss (heartbeat)
	respIndex    [4]byte       // the sender index of the peer's response to the pending initiation
	respSeen     bool          // and that response came (noteHandshakeResponseLocked)
	lastCMMIn    time.Time     // the last call-me-maybe from the peer
	localRefused time.Time     // the system last refused a packet to one of its local-network addresses (noteSendErr)
	reprobeAt    time.Time     // idle and shown on the relay or unreachable: last asked again (reprobeDueLocked)
	reprobeEvery time.Duration // and how long until the next time; 0 until it idles that way
	relayHSDelay time.Duration // test knob: handshake messages through the relay go out this late
	dropDirect   int           // test knob: the next WireGuard data packets sent direct are lost
	dropHS       int           // test knob: the next handshake messages to the peer are lost, both ways

	// The direct path in use was taken up at upSince: found, found again,
	// or a better one (handlePong). flaky are the direct addresses that
	// dropped soon after being taken up (noteDropLocked).
	upSince time.Time
	flaky   map[netip.AddrPort]*flakyAddr

	kind         pathKind
	kindEndpoint netip.AddrPort
	kindRelay    proto.RelayID
	since        time.Time
	prevKind     pathKind // the kind before this one: "relay first, then direct"

	txDirect, txRelay, rxDirect, rxRelay uint64
}

// flakyAddr: a direct address that dropped within flakyWithin of being
// taken up (noteDropLocked).
type flakyAddr struct {
	drops   int       // within flakyForget of each other
	last    time.Time // the last drop
	until   time.Time // not taken up before this while the relay works
	backoff time.Duration
}

type pendingPing struct {
	to    netip.AddrPort // invalid for a relay ping
	relay bool
	at    time.Time
}

type probe struct {
	tx      string
	to      netip.AddrPort
	relay   bool
	fast    bool // an extra ping (fastProbe, entering hold): not in the loss shown
	at      time.Time
	acked   bool
	rtt     time.Duration // when acked
	settled bool          // a relay ping already in relayHist
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
	c.writeTo(p, c.seal(p, m), to)
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
	// Room for the windows judged (10 s at up to a dozen pings a second
	// while a line is judged): short of it, a recovery could never count
	// enough clean pings.
	if len(p.probes) > 256 {
		p.probes = p.probes[len(p.probes)-192:]
	}
}

func (c *Conn) sendPing(p *peerState, to netip.AddrPort) { c.ping(p, to, false) }

// sendFastPing is a ping beyond the heartbeat's: four a second while a
// path is held or judged. A short silence would lose all of them at once,
// so they stay out of the loss shown.
func (c *Conn) sendFastPing(p *peerState, to netip.AddrPort) { c.ping(p, to, true) }

func (c *Conn) ping(p *peerState, to netip.AddrPort, fast bool) {
	tx := newTx()
	c.mu.Lock()
	c.recordLocked(p, probe{tx: tx, to: to, fast: fast, at: time.Now()})
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
	p.pingsRelay++
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
		if c.punchBack(p, src) {
			c.sendPing(p, src) // they reach us from src: punch back right away
		}
	case "pong":
		if viaRelay {
			c.handleRelayPong(p, m)
		} else {
			c.handlePong(p, m, src)
		}
	case "cmm":
		// The peer is probing afresh: it woke, resumed a LAN or its network
		// changed. For a moment small packets go through the relay as well
		// (sendWG): the direct path may not carry them yet. Not the
		// call-me-maybe it sends every cmmEvery while it hears nothing
		// direct: on a lossy line that is ongoing, and sending twice and
		// holding deal with it.
		c.mu.Lock()
		if now := time.Now(); now.Sub(p.lastCMMIn) > cmmFresh {
			p.freshAt = now
		}
		p.lastCMMIn = time.Now()
		c.mu.Unlock()
		for _, s := range m.Eps {
			if ap, err := netip.ParseAddrPort(s); err == nil {
				c.learn(p, ap)
				c.sendPing(p, ap)
			}
		}
	}
}

// punchBack: a ping from src while the direct path is not trusted is
// answered with a ping back, which opens our NAT towards src. Not when the
// peer is idle and src is the address already in use: an engine that pings
// all the time (M1) would otherwise draw a ping from us every few seconds,
// and an idle peer gets none (traffic wakes it, and the wake probes).
func (c *Conn) punchBack(p *peerState, src netip.AddrPort) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.directTrustedLocked(p, time.Now()) && !(p.idle && src == p.best)
}

func (c *Conn) learn(p *peerState, ap netip.AddrPort) {
	c.mu.Lock()
	p.cands[ap] = time.Now()
	c.byAddr[ap] = p
	c.mu.Unlock()
}

func (c *Conn) ack(p *peerState, tx string, rtt time.Duration) {
	for i := len(p.probes) - 1; i >= 0; i-- {
		if p.probes[i].tx == tx {
			p.probes[i].acked, p.probes[i].rtt = true, rtt
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
	rtt := now.Sub(pp.at)
	c.ack(p, m.Tx, rtt)
	p.cands[src] = now
	c.byAddr[src] = p
	trusted := c.directTrustedLocked(p, now)
	// Backed off (noteDropLocked): it answers, and would drop again in a
	// few seconds. The relay keeps the line; the answer is only noted.
	shun := (!trusted || src != p.best) && c.shunnedLocked(p, src, now)
	if !trusted && !c.directHeldLocked(p, now) && !p.degraded && !shun {
		// The direct path was dead and answers again: what it lost while
		// dead says nothing about how lossy it is now. (A held path was
		// only silent a moment: that is part of how lossy it is. So is the
		// silence of a path on the relay for loss, idle and asked every
		// few seconds: clearing then, its record never reached the pings
		// it takes to come back.)
		p.probes = slices.DeleteFunc(p.probes, func(pr probe) bool { return !pr.relay })
	}
	if (src == p.best || !trusted) && !shun {
		// After a silence past Hold (idle, or a drop) the line may have
		// changed since its last measure: this pong's round trip stands
		// alone. Mixed one eighth at a time, the 108-150 ms of 13:42 stayed
		// near 130 ms on a line back at 4 ms after 13 minutes idle (11-09,
		// build to the NAS), and the handshake resends waited for it.
		if p.srtt == 0 || src != p.best || now.Sub(p.lastPong) > c.opts.Hold {
			p.srtt = rtt
		} else {
			p.srtt = (p.srtt*7 + rtt) / 8
		}
	}
	var ev string
	switch {
	case shun:
	case !trusted:
		if p.best != src {
			ev = fmt.Sprintf("direct path found: %s rtt=%s", src, rtt.Round(100*time.Microsecond))
		}
		p.best, p.bestRTT, p.lastPong, p.upSince = src, rtt, now, now
	case src == p.best:
		p.bestRTT, p.lastPong = rtt, now
	case betterAddr(src, rtt, p.best, p.bestRTT):
		// Clearly faster (both devices turn out to be on one Wi-Fi), or on
		// the local network where the path was a public address.
		ev = fmt.Sprintf("better direct path: %s rtt=%s (was %s)", src, rtt.Round(100*time.Microsecond), p.best)
		p.best, p.bestRTT, p.lastPong, p.upSince = src, rtt, now, now
	}
	pongSelfChanged := false
	if self, err := netip.ParseAddrPort(m.Src); err == nil && self != c.pongSelf {
		c.pongSelf, pongSelfChanged = self, true
	}
	label := p.cfg.Label
	var init []byte
	if !shun {
		init = c.takePendingInitLocked(p, now)
	}
	best, blocked := p.best, p.blocked
	c.mu.Unlock()
	if init != nil && !blocked {
		c.writeTo(p, init, best)
	}
	if ev != "" {
		c.logf("[path] %s: %s", label, ev)
	}
	c.noteLocalAnswer(src)
	c.updatePath(p)
	if pongSelfChanged {
		c.noteEndpoints()
	}
}

func (c *Conn) handleRelayPong(p *peerState, m discoMsg) {
	c.mu.Lock()
	late, by := p.relayLate, p.relayLateBy
	c.mu.Unlock()
	if late > 0 && mrand.Float64() < late {
		time.AfterFunc(by, func() { c.relayPong(p, m) })
		return
	}
	c.relayPong(p, m)
}

func (c *Conn) relayPong(p *peerState, m discoMsg) {
	now := time.Now()
	c.mu.Lock()
	pp, ok := p.pending[m.Tx]
	if !ok || !pp.relay {
		c.mu.Unlock()
		return
	}
	delete(p.pending, m.Tx)
	c.ack(p, m.Tx, now.Sub(pp.at))
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
	// Not within 100 ms of the last copy, first one included: a copy
	// landing right after another can split the session (see sendWG).
	if now.Sub(p.initResentAt) < 100*time.Millisecond || now.Sub(p.pendingInitAt) < 100*time.Millisecond {
		return nil
	}
	p.initResentAt = now
	return b
}

// noteHandshakeResponseLocked notes the peer's response to the initiation
// pending (its receiver index is the initiation's sender index; a late
// response to an earlier one is ignored). It does not end the pending
// initiation: WireGuard may still refuse the response, when it dropped
// the initiation meanwhile (a sleep's expired sessions) or answered the
// peer's own initiation crossing ours (both ends then wait for the other,
// 5 s, colima sleep check). The initiation ends when WireGuard sends data
// on the session the response made (noteSessionInUseLocked).
func noteHandshakeResponseLocked(p *peerState, b []byte) {
	if len(b) != 92 || b[0] != 2 || b[1] != 0 || b[2] != 0 || b[3] != 0 || p.pendingInit == nil {
		return
	}
	if len(p.pendingInit) >= 8 && !bytes.Equal(b[8:12], p.pendingInit[4:8]) {
		return
	}
	copy(p.respIndex[:], b[4:8])
	p.respSeen = true
}

// noteSessionInUseLocked ends the pending initiation once WireGuard sends
// a data message (type 4, a keepalive at least, right after the handshake)
// to the index the peer's response gave: the session is in use.
func noteSessionInUseLocked(p *peerState, b []byte) {
	if p.pendingInit == nil || !p.respSeen || len(b) < 16 || b[0] != 4 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
		return
	}
	if bytes.Equal(b[4:8], p.respIndex[:]) {
		p.pendingInit, p.respSeen = nil, false
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
	return p.best.IsValid() && now.Sub(p.lastPong) < c.trustForLocked(p, now)
}

// busyLocked: traffic runs over the path and, with PathByLoss, fastProbe
// pings it four times a second, so silence means something sooner.
func (c *Conn) busyLocked(p *peerState, now time.Time) bool {
	return c.opts.PathByLoss && now.Sub(p.lastActive) < busyFor
}

// trustForLocked is how long the direct path stays trusted after its last
// pong. Under traffic (busyLocked) a second, or four round trips, is four
// pings missed in a row: the relay copies start then, not after 2.5 s, and
// a long connection on a path that died stalls about as long (M2 item 2).
func (c *Conn) trustForLocked(p *peerState, now time.Time) time.Duration {
	if c.busyLocked(p, now) {
		return min(c.opts.Trust, max(busyTrust, 4*p.srtt))
	}
	return c.opts.Trust
}

// holdForLocked is how long a silent direct path is still shown as direct
// (see directHeldLocked). Under traffic, three seconds of nothing at four
// pings a second is no hiccup (the line to Hong Kong, losing 14%, would
// lose twelve in a row about never): show the relay.
func (c *Conn) holdForLocked(p *peerState, now time.Time) time.Duration {
	if c.busyLocked(p, now) {
		return min(c.opts.Hold, c.trustForLocked(p, now)+busyHoldMore)
	}
	return c.opts.Hold
}

// directHeldLocked: the direct path in use missed its pongs for Trust, but
// not yet for Hold. Packets then go both direct and through the relay, and
// the path shown stays direct. On a long line two pings in a row go
// missing about once a minute (Wi-Fi, a cross-border link: M1 measured it
// on the real machines), and moving to the relay for that, then back a
// round trip later, is what made the paths jump. A direct path that really
// died loses no more than before: the relay copies have been arriving
// since Trust ran out. It is shown as the relay once Hold runs out too.
func (c *Conn) directHeldLocked(p *peerState, now time.Time) bool {
	return p.best.IsValid() && !p.degraded && p.kind == kindDirect && now.Sub(p.lastPong) < c.holdForLocked(p, now)
}

func (c *Conn) kindLocked(p *peerState, now time.Time) (pathKind, netip.AddrPort, proto.RelayID) {
	direct := c.directTrustedLocked(p, now)
	relayUp := c.relayUpLocked(p, now)
	if direct && !(p.degraded && relayUp) {
		return kindDirect, p.best, ""
	}
	if !direct && c.directHeldLocked(p, now) {
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

// judgeDupLocked decides whether small packets to p go twice: on a direct
// path whose loss over the last 30 s is dupOn or more (with at least
// dupMinLost pings lost), until it falls below dupOff (or always / never,
// by Options.DupSmall). The line to Hong Kong loses 12-14%: on. Ordinary
// Wi-Fi loses a ping now and then, under 2%: off.
func (c *Conn) judgeDupLocked(p *peerState, now time.Time) {
	was := p.dup
	switch c.DupSmall() {
	case "on":
		p.dup = true
	case "off":
		p.dup = false
	default:
		sent, lost := countLoss(p.probes, now, lossWindow, pingTimeout, func(pr probe) bool { return !pr.fast && !pr.relay && pr.to == p.best })
		loss := 0.0
		if sent > 0 {
			loss = float64(lost) / float64(sent)
		}
		switch {
		case sent < lossMinJudged: // too few to tell: as it was (off at first)
		case !p.dup:
			p.dup = loss >= dupOn && lost >= dupMinLost
		default:
			p.dup = loss >= dupOff
		}
	}
	if p.dup != was {
		state := "no longer sent twice"
		if p.dup {
			state = "sent twice"
		}
		go c.logf("[dup] %s: small packets %s on the direct path", p.cfg.Label, state)
	}
}

// DupSmall is the current Options.DupSmall: "auto", "on" or "off".
func (c *Conn) DupSmall() string {
	if m := c.dupMode.Load(); m != nil {
		return *m
	}
	return c.opts.DupSmall
}

// SetDupSmall changes Options.DupSmall at run time (a test knob too).
func (c *Conn) SetDupSmall(mode string) {
	c.dupMode.Store(&mode)
	c.mu.Lock()
	now := time.Now()
	for _, p := range c.peers {
		c.judgeDupLocked(p, now)
	}
	c.mu.Unlock()
}

// relayReadyLocked: the connection to the relay that reaches p is open, so
// a packet sent through it does not wait for a dial.
func (c *Conn) relayReadyLocked(p *peerState) bool {
	rc := c.relays[c.relayForLocked(p)]
	return rc != nil && rc.up
}

// relayAsync runs fn, a disco send through the relay, off the caller's
// goroutine: a relay being dialed, or a slow one, must not hold up the
// heartbeat whose pings keep every direct path trusted. At most one runs
// per peer; a send skipped for that is due again at the next heartbeat.
func (c *Conn) relayAsync(p *peerState, fn func()) {
	if !p.relayBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.relayBusy.Store(false)
		fn()
	}()
}

// noteTrafficLocked marks a WireGuard packet to or from p. It reports
// whether p was idle; the caller then wakes it.
func (c *Conn) noteTrafficLocked(p *peerState, now time.Time) bool {
	p.lastActive = now
	if !p.idle {
		return false
	}
	p.idle, p.wokeAt, p.freshAt = false, now, now
	p.reprobeEvery = 0
	p.activatedAt = now // nothing answering yet means "connecting", not "unreachable"
	return true
}

// wake probes a peer whose traffic just came back: the relay, a
// call-me-maybe with our endpoints, and every address we know for it.
func (c *Conn) wake(p *peerState) {
	c.logf("[idle] %s: traffic again, probing", p.cfg.Label)
	c.probe(p)
}

// idleBetterLocked: the peer is idle, so the path shown stays as it was:
// it does not turn "relay" or "unreachable" for want of pings. But an
// answer showing a better path is news, and shows: a peer that slept
// through our wake and called later, a line that came back while nobody
// used it (11-09 19:30: the test Mac answered direct 75 s after this Mac
// woke, and the screen said "unreachable" until someone pinged it at
// 19:34). A line on the relay for loss has its own way back
// (liftIdleVerdict).
func (c *Conn) idleBetterLocked(p *peerState, now time.Time) bool {
	if !p.idle || p.degraded {
		return false
	}
	k, ep, _ := c.kindLocked(p, now)
	if k == kindDirect && p.kind == kindDirect {
		return ep != p.kindEndpoint // a better direct path (handlePong)
	}
	return pathRank(k) > pathRank(p.kind)
}

// pathRank orders the kinds from worst to best.
func pathRank(k pathKind) int {
	switch k {
	case kindDirect:
		return 3
	case kindRelay:
		return 2
	case kindConnecting:
		return 1
	}
	return 0 // unreachable
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
	if c.opts.PathByLoss && p.best.IsValid() && p.degraded && !c.directTrustedLocked(p, now) && now.Sub(p.lastPong) < c.opts.Hold {
		// On the relay because the direct path is lossy, and now it missed
		// its pongs for Trust: on a lossy line that is no news (four pings
		// lost in a row, a second under traffic, at 36% lost both ways
		// happens most minutes). The verdict stands until the rules below
		// lift it; dropping it here sent the line back direct 2 s after it
		// moved, to be judged and moved again.
		return ""
	}
	if !c.opts.PathByLoss || !p.best.IsValid() || !c.directTrustedLocked(p, now) {
		// No direct path, or a dead one (no pong for Trust, or for Hold
		// once on the relay for loss): that is the relay anyway, and when
		// it answers again it is judged afresh.
		if p.degraded {
			p.degraded, p.reason = false, ""
		}
		p.slowSince = time.Time{}
		return ""
	}
	direct := func(pr probe) bool { return !pr.relay && pr.to == p.best }
	p.settleRelayLocked(now)
	// A ping counts as lost only after pingTimeout: on Wi-Fi a pong is
	// often late, and late is not lost.
	// Judged after a second, not pingTimeout: an answer a second late on
	// a busy line is as good as lost to TCP, and the relay's record (below)
	// keeps a merely jittery line from moving.
	sent, lost, spread := countLossSpread(p.probes, now, degradeOver, time.Second, direct)
	loss := 0.0
	if sent > 0 {
		loss = float64(lost) / float64(sent)
	}
	relayUp := c.relayUpLocked(p, now)
	isRelay := func(pr probe) bool { return pr.relay }
	dsent, dbad := countBad(p.probes, now, degradeOver, direct)
	rsent, rbad := countBad(p.probes, now, degradeOver, isRelay)
	// The relay is clearly cleaner: at most half the direct path's bad
	// answers (debug loss drops UDP only: the relay stays clean; the home
	// line to Hong Kong loses TCP as well, and the relay is no better).
	// And clean right now too: the record looks back 120 answers, and
	// after a clean stretch still says "clean" a while once the relay goes
	// bad (colima lossy-both after lossy-line, 11-09: moved with the relay
	// 3 of 25 late, once the threshold was 15%).
	hn, hbad := p.relayRecord()
	cleaner := hn >= relayMinJudged && float64(hbad) <= relayCleanMax*float64(hn) &&
		rsent > 0 && dsent > 0 && float64(rbad) <= relayCleanMax*float64(rsent) &&
		float64(rbad)/float64(rsent) <= float64(dbad)/float64(dsent)/2
	if p.srtt > 0 && p.relayRTT > 0 && p.srtt > 2*p.relayRTT+50*time.Millisecond {
		if p.slowSince.IsZero() {
			p.slowSince = now
		}
	} else {
		p.slowSince = time.Time{}
	}
	switch {
	case !p.degraded && relayUp && sent >= lossMinSent && lost >= lossMinLost && loss >= lossDegrade &&
		spread >= lossSpread && cleaner:
		p.degraded, p.reason, p.degradedAt = true, "lossy", now
		return fmt.Sprintf("direct path loses %.0f%% of pings (%d of %d over %s), the relay %d of %d late or lost; using the relay",
			loss*100, lost, sent, spread.Round(100*time.Millisecond), rbad, rsent)
	case !p.degraded && relayUp && !p.slowSince.IsZero() && now.Sub(p.slowSince) >= slowFor:
		p.degraded, p.reason, p.degradedAt = true, "slow", now
		return fmt.Sprintf("direct path takes %s, the relay %s; using the relay", p.srtt.Round(time.Millisecond), p.relayRTT.Round(time.Millisecond))
	case p.degraded && now.Sub(p.degradedAt) < minOnRelay:
		return "" // stay put for a while: no flapping
	case p.degraded && p.reason == "lossy":
		csent, clost, _ := countLossSpread(p.probes, now, recoverOver, pingTimeout, direct)
		if csent >= recoverMin && float64(clost) <= lossRecover*float64(csent) {
			p.degraded, p.reason = false, ""
			return fmt.Sprintf("direct path clean again (%d of %d lost); back to direct", clost, csent)
		}
		// The relay turned out no better: back to the faster path.
		ds, db := countBad(p.probes, now, recoverOver, direct)
		rs, rb := countBad(p.probes, now, recoverOver, isRelay)
		if ds >= recoverMin && rs > 0 && float64(rb)/float64(rs) > max(2*relayCleanMax, 0.8*float64(db)/float64(ds)) {
			p.degraded, p.reason = false, ""
			return fmt.Sprintf("the relay is no cleaner (%d of %d late or lost, direct %d of %d); back to direct", rb, rs, db, ds)
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
	if !p.active || c.frozenLocked(p, now) && !c.idleBetterLocked(p, now) {
		c.mu.Unlock()
		return
	}
	trusted := c.directTrustedLocked(p, now)
	held := !trusted && c.directHeldLocked(p, now)
	var holdMsg string
	entered := false
	switch {
	case held && !p.holding:
		p.holding, p.holdFrom, entered = true, p.lastPong, true
		holdMsg = fmt.Sprintf("[hold] %s: no answer from %s for %s; sending direct and through the relay",
			p.cfg.Label, p.best, now.Sub(p.lastPong).Round(100*time.Millisecond))
	case !held && p.holding:
		p.holding = false
		if trusted { // Hold running out instead shows as the switch below
			holdMsg = fmt.Sprintf("[hold] %s: direct answers again (silent for %s)",
				p.cfg.Label, p.lastPong.Sub(p.holdFrom).Round(10*time.Millisecond))
		} else if msg := c.noteDropLocked(p, now); msg != "" {
			defer c.logf("%s", msg)
		}
	}
	best := p.best
	if holdMsg != "" {
		defer func() {
			c.logf("%s", holdMsg)
			if entered { // find out at once whether it was a hiccup
				c.sendFastPing(p, best)
				c.relayAsync(p, func() { c.sendRelayPing(p) })
			}
		}()
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
		p.reprobeEvery = 0 // asked again from IdleReprobe on, if it idles this way
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
	var stale []proto.NodePublic
	c.mu.Lock()
	for k, p := range c.peers {
		if !p.active || p.pendingInit == nil || now.Sub(p.pendingInitAt) > 5*time.Second {
			continue
		}
		every := min(max(3*p.srtt, 150*time.Millisecond), time.Second)
		if p.srtt == 0 {
			every = 300 * time.Millisecond
		}
		if now.Sub(p.pendingInitAt) < every {
			continue
		}
		// Sent again already and still unanswered two rounds on, just
		// after the path came back (a wake, a resume, a network change):
		// the peer most likely got it and its response was lost, through a
		// relay connection being replaced. The same bytes again are a
		// replay: start a new handshake (colima sleep check: 5.3 s,
		// WireGuard's own retry, when one of three responses went missing).
		// Judged by the initiation's age: pongs send it again too, a few
		// times a second just after a wake, and judged by the last copy it
		// would never be stale.
		if p.initResentAt.After(p.pendingInitAt) && now.Sub(p.pendingInitAt) >= 2*every &&
			now.Sub(p.freshAt) < 5*time.Second && c.opts.OnStaleHandshake != nil {
			p.pendingInit = nil
			stale = append(stale, k)
			continue
		}
		if now.Sub(p.initResentAt) < every {
			continue
		}
		p.initResentAt = now
		// One way per copy (see sendWG): direct when it is trusted, else
		// through the relay, else to the last direct address.
		j := job{p: p, b: p.pendingInit}
		switch {
		case c.sendDirectLocked(p, now):
			j.to = p.best
		case c.relayReadyLocked(p):
			j.relay = true
		case p.best.IsValid() && !p.blocked:
			j.to = p.best
		default:
			j.relay = true
		}
		jobs = append(jobs, j)
	}
	c.mu.Unlock()
	for _, k := range stale {
		c.opts.OnStaleHandshake(k)
	}
	for _, j := range jobs {
		if j.to.IsValid() {
			c.writeTo(j.p, j.b, j.to)
		}
		if j.relay {
			p, b := j.p, j.b
			c.relayAsync(p, func() { c.sendRelay(p, b) })
		}
	}
}

// lossSeenLocked: the direct path lost a ping lately, so it is measured
// more closely until it is judged.
func (p *peerState) lossSeenLocked(now time.Time) bool {
	// Judged as soon as an answer is late (countBad), not after
	// pingTimeout: the relay is measured from then on, and the switch has
	// 10 s in all (M2's bad line).
	_, bad := countBad(p.probes, now, degradeOver, func(pr probe) bool { return !pr.relay && pr.to == p.best })
	return bad > 0
}

// settleRelayLocked moves the relay's pings that can be judged into its
// record (relayHist): answered (late or not, by the same rule as countBad),
// or unanswered past the time an answer would have been late.
func (p *peerState) settleRelayLocked(now time.Time) {
	base := time.Duration(-1)
	for _, pr := range p.probes {
		if pr.relay && pr.acked && (base < 0 || pr.rtt < base) {
			base = pr.rtt
		}
	}
	if base < 0 {
		return // no answer yet: no baseline to judge against
	}
	judgeAfter := max(300*time.Millisecond, base+lateBy)
	for i := range p.probes {
		pr := &p.probes[i]
		if !pr.relay || pr.settled || (!pr.acked && now.Sub(pr.at) < judgeAfter) {
			continue
		}
		pr.settled = true
		p.relayHist[p.relayHistI] = !pr.acked || pr.rtt > base+lateBy
		p.relayHistI = (p.relayHistI + 1) % relayHistLen
		p.relayHistN = min(p.relayHistN+1, relayHistLen)
	}
}

// relayRecord is how many answers the relay's record holds, and how many
// of them were bad.
func (p *peerState) relayRecord() (n, bad int) {
	for i := 0; i < p.relayHistN; i++ {
		if p.relayHist[i] {
			bad++
		}
	}
	return p.relayHistN, bad
}

// countBad counts the pings of the window that can be judged, and the bad
// ones among them: answered lateBy over the fastest answer of the window
// (the path's own baseline), or not answered by then. A ping is judged once
// that much time has passed, not after pingTimeout: late is bad here, so
// the relay is measured within seconds, not tens of them.
func countBad(ps []probe, now time.Time, window time.Duration, match func(probe) bool) (sent, bad int) {
	base := time.Duration(-1)
	for _, pr := range ps {
		if age := now.Sub(pr.at); age <= window && match(pr) && pr.acked && (base < 0 || pr.rtt < base) {
			base = pr.rtt
		}
	}
	judgeAfter := pingTimeout // nothing answered yet: no baseline
	if base >= 0 {
		judgeAfter = max(300*time.Millisecond, base+lateBy)
	}
	for _, pr := range ps {
		age := now.Sub(pr.at)
		if age > window || age < judgeAfter || !match(pr) {
			continue
		}
		sent++
		if !pr.acked || pr.rtt > base+lateBy {
			bad++
		}
	}
	return sent, bad
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

// fastProbe pings a held direct path (see directHeldLocked) four times a
// second, so a hiccup ends, and the copies through the relay stop, within a
// round trip of the line coming back. With PathByLoss it also pings direct
// paths that carry traffic or are under suspicion, so a lossy line is told
// from a lucky one within seconds: TCP backs off further every second it
// waits.
func (c *Conn) fastProbe() {
	now := time.Now()
	var ps []*peerState
	c.mu.Lock()
	for _, p := range c.peers {
		if !p.active || p.idle || !p.best.IsValid() {
			continue
		}
		held := !c.directTrustedLocked(p, now) && c.directHeldLocked(p, now)
		busy := now.Sub(p.lastActive) < busyFor // TCP running over it: a bad line must show fast
		if held || c.opts.PathByLoss && (busy || p.degraded || p.lossSeenLocked(now)) {
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
		// While a line is judged, the relay is measured twice a second
		// too: the switch needs it shown clearly cleaner, and the
		// heartbeat's one a second would take most of the 10 s.
		// Not once on the relay: the heartbeat's one a second does there,
		// and the direct path's pings must keep the window to recover by.
		relay := c.opts.PathByLoss && !p.degraded && p.lossSeenLocked(now)
		c.mu.Unlock()
		c.sendFastPing(p, best)
		if relay { // twice per tick: the record fills in a few seconds
			c.relayAsync(p, func() { c.sendRelayPing(p) })
			time.AfterFunc(relayJudgeEvery, func() { c.relayAsync(p, func() { c.sendRelayPing(p) }) })
		}
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
	type reprobe struct {
		p          *peerState
		cands      []netip.AddrPort
		cmm, relay bool
	}
	var reprobes []reprobe
	var idled, judged []string
	var lifted, idlePinged []*peerState
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
		// A held path (silent past Trust) settles first: it answers again,
		// or Hold runs out and the relay shows. Going idle now would freeze
		// "direct" on a path that may be dead.
		held := !c.directTrustedLocked(p, now) && c.directHeldLocked(p, now)
		if !p.idle && now.Sub(p.lastActive) > c.opts.Idle && !held {
			p.idle = true
			idled = append(idled, p.cfg.Label)
		}
		if p.idle {
			// No traffic, no pings: the path shown stays as it was. But a
			// line idle on the relay because its direct path lost too much
			// would show the relay until someone uses it again, however
			// clean the line has become (NAS, 13:16: still on the relay 16
			// minutes after the loss stopped). Its direct path is asked now
			// and then, which does not wake the line nor count for the
			// relay's record.
			if c.opts.PathByLoss && p.degraded && p.best.IsValid() {
				if c.idleCleanLocked(p, now) {
					lifted = append(lifted, p)
				} else if now.Sub(p.idleCheck) >= idleRecheck {
					p.idleCheck = now
					idlePinged = append(idlePinged, p)
				}
			} else if c.reprobeDueLocked(p, now) {
				// Its addresses as it gives them now, and the last that
				// worked; a call-me-maybe so it pings back (both NATs open),
				// and the relay asked too if it was not answering either.
				// Only through a relay already open: no dial for this.
				ready := c.relayReadyLocked(p)
				r := reprobe{p: p, cmm: ready, relay: ready && p.kind == kindUnreachable}
				r.cands = append(r.cands, p.cfg.Endpoints...)
				if p.best.IsValid() && !slices.Contains(r.cands, p.best) {
					r.cands = append(r.cands, p.best)
				}
				reprobes = append(reprobes, r)
			}
			continue
		}
		c.judgeDupLocked(p, now)
		c.settleFlakyLocked(p, now)
		p.settleRelayLocked(now) // the relay's record grows from its background pings too
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
	for _, p := range lifted {
		c.liftIdleVerdict(p)
	}
	for _, p := range idlePinged {
		c.mu.Lock()
		best := p.best
		c.mu.Unlock()
		c.sendPing(p, best)
	}
	for _, r := range reprobes {
		for _, a := range r.cands {
			c.sendPing(r.p, a)
		}
		if r.cmm {
			p, relay := r.p, r.relay
			c.relayAsync(p, func() {
				c.sendCMM(p)
				if relay {
					c.sendRelayPing(p)
				}
			})
		}
	}
	c.mu.Lock()
	closed := c.closeIdleRelaysLocked(now)
	c.mu.Unlock()
	for _, n := range closed {
		c.logf("[relay] closed the unused connection to %s", n)
	}
	c.rechooseRelays() // a skipped relay's backoff may have run out: a list's better relay is back
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
		if j.cmm || j.relay {
			p, cmm, relay := j.p, j.cmm, j.relay
			c.relayAsync(p, func() {
				if cmm {
					c.sendCMM(p)
				}
				if relay {
					c.sendRelayPing(p)
				}
			})
		}
	}
}

// shunnedLocked: a is backed off (noteDropLocked) and the relay works.
func (c *Conn) shunnedLocked(p *peerState, a netip.AddrPort, now time.Time) bool {
	f := p.flaky[a]
	return f != nil && now.Before(f.until) && c.relayUpLocked(p, now)
}

// noteDropLocked: Hold ran out on the direct path in use. If it dropped
// within flakyWithin of being taken up, a second time within flakyForget,
// it is backed off (see flakyWithin). It returns a line for the log, or "".
func (c *Conn) noteDropLocked(p *peerState, now time.Time) string {
	a := p.best
	up := p.holdFrom.Sub(p.upSince) // how long it worked
	if !a.IsValid() || p.upSince.IsZero() || up >= flakyWithin {
		return ""
	}
	if p.flaky == nil {
		p.flaky = map[netip.AddrPort]*flakyAddr{}
	}
	f := p.flaky[a]
	if f == nil || now.Sub(f.last) > flakyForget {
		f = &flakyAddr{}
		p.flaky[a] = f
	}
	f.drops++
	f.last = now
	if f.drops < 2 {
		return ""
	}
	f.backoff = min(max(2*f.backoff, c.opts.FlakyBackoff), flakyBackoffMax)
	f.until = now.Add(f.backoff)
	return fmt.Sprintf("[path] %s: %s dropped %s after it was taken up, %d times lately; the relay keeps the line for %s",
		p.cfg.Label, a, up.Round(100*time.Millisecond), f.drops, f.backoff)
}

// settleFlakyLocked: the direct path in use held for flakyWithin since it
// was taken up, so whatever it dropped before is forgotten.
func (c *Conn) settleFlakyLocked(p *peerState, now time.Time) {
	if f := p.flaky[p.best]; f != nil && c.directTrustedLocked(p, now) && now.Sub(p.upSince) >= flakyWithin {
		delete(p.flaky, p.best)
	}
}

// reprobeDueLocked: an idle peer shown on the relay, or as unreachable, is
// asked again IdleReprobe after it idled that way, then twice as long each
// time up to idleReprobeMax: a direct path back while nobody used the line,
// or a peer back from sleep, then shows (idleBetterLocked). A few pings in
// 5 minutes per such peer; a peer shown direct is left alone.
func (c *Conn) reprobeDueLocked(p *peerState, now time.Time) bool {
	if p.kind == kindDirect || p.kind == kindConnecting {
		return false
	}
	if p.reprobeEvery == 0 {
		p.reprobeEvery, p.reprobeAt = c.opts.IdleReprobe, now
		return false
	}
	if now.Sub(p.reprobeAt) < p.reprobeEvery {
		return false
	}
	p.reprobeAt, p.reprobeEvery = now, min(2*p.reprobeEvery, idleReprobeMax)
	return true
}

// idleCleanLocked: the direct path of a line idle on the relay for loss
// answered every ping lately, at least idleCleanPings over idleCleanFor.
func (c *Conn) idleCleanLocked(p *peerState, now time.Time) bool {
	sent, lost := countLoss(p.probes, now, idleCleanFor, pingTimeout, func(pr probe) bool { return !pr.relay && pr.to == p.best })
	return sent >= idleCleanPings && lost == 0
}

// liftIdleVerdict: a line idle on the relay for loss answers direct again.
// The verdict goes, and the line shows the direct path although it is idle
// (an idle line otherwise shows what it last had).
func (c *Conn) liftIdleVerdict(p *peerState) {
	now := time.Now()
	c.mu.Lock()
	if !p.active || !p.idle || !p.degraded {
		c.mu.Unlock()
		return
	}
	p.degraded, p.reason = false, ""
	old, ep := p.kind, p.best
	if old != kindDirect {
		p.prevKind, p.since = old, now
	}
	p.kind, p.kindEndpoint, p.kindRelay = kindDirect, ep, ""
	label, id := p.cfg.Label, p.cfg.ID
	c.mu.Unlock()
	c.logf("[path] %s: idle, and the direct path answered every ping for %s; direct again", label, idleCleanFor)
	if old != kindDirect {
		c.logf("[switch] %s: %s -> direct via %s", label, old, ep)
	}
	if c.opts.OnPath != nil {
		c.opts.OnPath(id)
	}
}

// PathStatus is one peer's path as this device sees it right now.
type PathStatus struct {
	Kind       string // "direct", "relay", "connecting" or "unreachable"
	Endpoint   netip.AddrPort
	Relay      proto.RelayID
	RTT        time.Duration
	Loss       float32 // over the last 30 s on the path in use; 0 until enough pings were judged
	Since      time.Time
	Candidates []netip.AddrPort
	Blocked    bool
	Idle       bool   // no traffic lately: no heartbeat, Kind is the last one seen
	Reason     string // why the relay although direct works: "lossy" or "slow"
	Prev       string // the kind before this one, however briefly it lasted
	Held       bool   // direct, but silent for Trust: packets go through the relay too
	Dup        bool   // small packets go twice on the direct path
	TxDup      uint64 // packets sent a second time
	TxWake     uint64 // small packets also sent through the relay just after waking
	HSBoth     bool   // the peer drops repeated handshake messages: they go both ways while direct is unproven
	TxHSBoth   uint64 // handshake messages sent both ways
	RxHSDupes  uint64 // repeated handshake messages from the peer, dropped
	// LocalRefused: in the last 5 minutes the system refused a packet to
	// one of the peer's local-network addresses (macOS local network privacy).
	LocalRefused bool
	// BackedOff are direct addresses not taken up for now: they dropped
	// soon after being taken up, twice lately (noteDropLocked).
	BackedOff []netip.AddrPort

	TxDirect, TxRelay, RxDirect, RxRelay uint64
	PingsTx                              uint64
	PingsRelay                           uint64 // of PingsTx, through the relay
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
		Idle: p.idle, Prev: p.prevKind.String(), TxDirect: p.txDirect, TxRelay: p.txRelay, RxDirect: p.rxDirect, RxRelay: p.rxRelay, PingsTx: p.pingsTx, PingsRelay: p.pingsRelay}
	st.Held = !p.idle && !c.directTrustedLocked(p, now) && c.directHeldLocked(p, now)
	st.Dup, st.TxDup, st.TxWake = p.dup, p.txDup, p.txWake
	st.HSBoth, st.TxHSBoth, st.RxHSDupes = p.cfg.DualHandshake && !c.noHSDedupe.Load(), p.txHSBoth, p.rxHSDupes
	st.LocalRefused = !p.localRefused.IsZero() && now.Sub(p.localRefused) < 5*time.Minute
	for a, f := range p.flaky {
		if now.Before(f.until) {
			st.BackedOff = append(st.BackedOff, a)
		}
	}
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
	sent, lost := countLoss(ps, now, lossWindow, pingTimeout, func(pr probe) bool { return !pr.fast && match(pr) })
	if sent < lossMinJudged {
		return 0 // too few to tell yet
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
// SetRelayLate makes a share of the relay's pongs from peer k come late
// (a test knob: a line that loses TCP as well as UDP).
func (c *Conn) SetRelayLate(k proto.NodePublic, frac float64, by time.Duration) bool {
	c.mu.Lock()
	p := c.peers[k]
	if p != nil {
		p.relayLate, p.relayLateBy = frac, by
	}
	c.mu.Unlock()
	if p == nil {
		return false
	}
	c.logf("[test] %s: %.0f%% of relay pongs %s late", p.cfg.Label, frac*100, by)
	return true
}

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

// WakeDual is how long after waking packets also go through the relay.
const WakeDual = 2 * time.Second

// DelayRelayHandshakesForTest makes the handshake messages sent to k
// through the relay go out d late, as when the relay's TCP waits out a
// lost segment on a lossy line (tests: the first packet after a long
// idle, 20:08: the response 0.7 s late).
func (c *Conn) DelayRelayHandshakesForTest(k proto.NodePublic, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[k]; p != nil {
		p.relayHSDelay = d
	}
}

// DropDirectForTest loses the next n WireGuard data packets sent to k on
// the direct path, copies included, as a lossy line would; disco pings and
// handshake messages pass (tests: the first exchange after waking).
func (c *Conn) DropDirectForTest(k proto.NodePublic, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[k]; p != nil {
		p.dropDirect = n
	}
}

// AgeForTest makes the peer's last answers d old, direct and through the
// relay, as after a long idle (tests: the first packet after one).
// SetRTTForTest sets the round trip the peer was last measured at: one
// from before a long idle, on a line that has got faster since.
func (c *Conn) SetRTTForTest(k proto.NodePublic, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[k]; p != nil {
		p.srtt, p.bestRTT = d, d
	}
}

// DropHandshakesForTest loses the next n handshake messages sent to peer
// k, direct and through the relay alike: a response that never arrives.
func (c *Conn) DropHandshakesForTest(k proto.NodePublic, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[k]; p != nil {
		p.dropHS = n
	}
}

func (c *Conn) AgeForTest(k proto.NodePublic, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[k]; p != nil {
		old := time.Now().Add(-d)
		p.lastPong, p.lastRelayPong, p.lastActive = old, old, old
	}
}
