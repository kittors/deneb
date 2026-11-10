package magicsock

import (
	"encoding/hex"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"

	"github.com/kittors/deneb/proto"
)

// Bind adapts Conn to wireguard-go's conn.Bind. The sockets live in Conn for
// the whole process; Open and Close only gate the receive function, because
// wireguard-go re-opens its bind on some configuration changes.
func (c *Conn) Bind() conn.Bind { return &wgBind{c: c} }

type wgBind struct {
	c      *Conn
	mu     sync.Mutex
	closed chan struct{}
}

func (b *wgBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	closed := make(chan struct{})
	b.closed = closed
	b.mu.Unlock()
	recv := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		var rx rxPacket
		select {
		case <-closed:
			return 0, net.ErrClosed
		case <-b.c.closed:
			return 0, net.ErrClosed
		case rx = <-b.c.rx:
		}
		sizes[0] = copy(packets[0], rx.data)
		eps[0] = rx.ep
		n := 1
		for n < len(packets) {
			select {
			case rx = <-b.c.rx:
				sizes[n] = copy(packets[n], rx.data)
				eps[n] = rx.ep
				n++
			default:
				return n, nil
			}
		}
		return n, nil
	}
	return []conn.ReceiveFunc{recv}, b.c.port, nil
}

func (b *wgBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != nil {
		select {
		case <-b.closed:
		default:
			close(b.closed)
		}
	}
	return nil
}

func (b *wgBind) SetMark(uint32) error { return nil }
func (b *wgBind) BatchSize() int       { return 8 }

func (b *wgBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	switch e := ep.(type) {
	case *peerEndpoint:
		return b.c.sendWG(bufs, e.key)
	case *addrEndpoint:
		// Only cookie replies under load are addressed to a packet's source.
		for _, buf := range bufs {
			b.c.sock().WriteToUDPAddrPort(buf, e.addr)
		}
		return nil
	}
	return errors.New("unexpected endpoint type")
}

// ParseEndpoint takes the peer's node key in hex, which the engine puts in
// the UAPI "endpoint=" line: the endpoint is the peer, and Conn picks the
// path packet by packet.
func (b *wgBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("bad endpoint %q", s)
	}
	k := proto.NodePublic(raw)
	b.c.mu.Lock()
	p := b.c.peers[k]
	b.c.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown peer %s", k)
	}
	return p.ep, nil
}

// EndpointString is what the engine writes after "endpoint=" for a peer.
func EndpointString(k proto.NodePublic) string { return hex.EncodeToString(k[:]) }

// sendWG is WireGuard's send path.
func (c *Conn) sendWG(bufs [][]byte, k proto.NodePublic) error {
	now := time.Now()
	c.mu.Lock()
	p := c.peers[k]
	if p == nil || !p.active {
		c.mu.Unlock()
		return nil // the peer just left; WireGuard will drop it soon
	}
	if p.dropHS > 0 { // DropHandshakesForTest
		kept := make([][]byte, 0, len(bufs))
		for _, b := range bufs {
			if p.dropHS > 0 && isHandshakeMsg(b) {
				p.dropHS--
				continue
			}
			kept = append(kept, b)
		}
		if bufs = kept; len(bufs) == 0 {
			c.mu.Unlock()
			return nil
		}
	}
	wakeUp := false
	if !onlyKeepalives(bufs) {
		wakeUp = c.noteTrafficLocked(p, now)
	}
	direct := c.sendDirectLocked(p, now)
	to := p.best
	blocked := p.blocked // simulated UDP block: direct copies are lost on the wire
	blackhole := direct && blocked
	lossTx := p.lossTx
	dup := p.dup
	// The direct path missed its last pongs (a hiccup, or dead): send both
	// ways until it answers again or Hold runs out. WireGuard drops the
	// copy that arrives second. The relay copy is skipped while the relay
	// connection is still being opened, not to stall the direct one.
	held := !direct && c.directHeldLocked(p, now)
	heldRelay := held && c.relayReadyLocked(p)
	enterHold := held && !p.holding
	stale := p.kind == kindDirect && !direct && !held
	// Just back from idle: the last direct address may well still work, and
	// the relay surely does. Send both ways until one answers.
	woke := !direct && to.IsValid() && !p.degraded && now.Sub(p.wokeAt) < wakeDual &&
		now.Sub(p.lastPong) < 3*time.Minute
	// The relay connection is not up yet (redialed after a network change
	// or a sleep) and the direct path not confirmed: through the relay a
	// packet would wait for the dial, or go with it if the dial fails. The
	// last direct address may well work: the packet goes there, handshake
	// messages only there, and a data packet tries the relay in the
	// background (test Mac, 22:17:28: the first ping after the network came
	// back went only to a relay still connecting, 40 ms before the direct
	// path answered, and was lost).
	noRelay := !direct && !c.relayReadyLocked(p) && to.IsValid() && !blocked
	dual := held || woke || noRelay
	viaRelay := !direct && (!held || heldRelay || woke) && !noRelay
	// Just back from idle (or a pause, or a network change) with the
	// direct path trusted on one pong: after a long idle the handshake
	// takes a round trip, and by its end the wake's direct ping has
	// answered once. One answer says little on a lossy line, and the first
	// exchange after a pause weighs the most (a ping, a connection's SYN, a
	// DNS answer): for wakeDual small packets go through the relay as
	// well. The NAS at 19:34 on the line to Hong Kong: both copies of the
	// first ping lost, answered on the retry.
	wakeRelay := direct && !p.freshAt.IsZero() && now.Sub(p.freshAt) < wakeDual && c.relayReadyLocked(p)
	// The peer drops repeated handshake messages (it claimed
	// proto.CapHandshakeDedupe): while the direct path is unproven, each
	// handshake message goes both direct and through the relay, so neither
	// a relay waiting out a lost TCP segment nor a lost datagram holds the
	// handshake up (20:08: the response 0.7 s late through the relay).
	dualHS := p.cfg.DualHandshake && to.IsValid() && !blocked && !c.noHSDedupe.Load()
	drop := p.dropDirect
	confirmed := direct || c.relayUpLocked(p, now)
	for _, b := range bufs {
		if isHandshakeInitiation(b) {
			// Kept until the session it makes is in use, and sent again if
			// the peer's response does not come in a few round trips: one
			// lost packet would otherwise cost WireGuard's 5 s retry (a
			// first connection on Wi-Fi, a peer just back from a pause).
			p.pendingInit, p.pendingInitAt = append([]byte(nil), b...), now
			p.initResentAt, p.respSeen = time.Time{}, false
			if confirmed {
				p.initResentAt = now // this copy goes out now, on a known path
			}
		} else {
			noteSessionInUseLocked(p, b)
		}
	}
	if direct || dual {
		p.txDirect += uint64(len(bufs))
	}
	if viaRelay {
		p.txRelay += uint64(len(bufs))
	}
	if wakeRelay {
		var n uint64
		for _, b := range bufs {
			if wakeCopy(b) {
				n++
			}
		}
		p.txRelay += n
		p.txWake += n
	}
	if dualHS && (viaRelay || wakeRelay) {
		for _, b := range bufs {
			if isHandshakeMsg(b) {
				p.txHSBoth++
			}
		}
	}
	c.mu.Unlock()
	if wakeUp {
		go c.wake(p)
	}
	if stale || enterHold {
		c.updatePath(p)
	}
	if blackhole && !wakeRelay {
		return nil
	}
	// A handshake message goes one way only, once. Two copies of an
	// initiation landing a moment apart can each draw a response (the
	// peer's WireGuard checks for replays and records the new one in two
	// steps), and then we keep one session while the peer keeps the other:
	// nothing gets through until the next handshake, 15 s or more later.
	// Sent both ways, it takes the relay, which surely arrives. Unless the
	// peer drops the second copy before its WireGuard (dualHS): both ways.
	var err error
	sendDirect := direct || dual
	if (sendDirect || dualHS) && !blocked {
		var twice uint64
		dropped := 0
		for _, b := range bufs {
			hs := isHandshakeMsg(b)
			if !sendDirect && !hs {
				continue // only the handshake goes direct (dualHS)
			}
			if hs && !direct && viaRelay && !dualHS {
				continue // through the relay below, one way only
			}
			copies := 1
			if dup && !hs && len(b) <= dupMaxSize {
				copies = 2 // the peer's WireGuard drops whichever arrives second
				twice++
			}
			for i := 0; i < copies; i++ {
				if !hs && dropped < drop {
					dropped++ // DropDirectForTest
					continue
				}
				if lossTx == 0 || mrand.Float64() >= lossTx {
					err = c.writeTo(p, b, to)
				}
			}
		}
		if twice > 0 || dropped > 0 {
			c.mu.Lock()
			p.txDup += twice
			p.dropDirect = max(0, p.dropDirect-dropped)
			c.mu.Unlock()
		}
	}
	switch {
	case viaRelay:
		for _, b := range bufs {
			if e := c.sendRelay(p, b); e != nil && !dual {
				err = e
			}
		}
	case wakeRelay:
		// The direct copies went first; these only cover their loss.
		for _, b := range bufs {
			if wakeCopy(b) || dualHS && isHandshakeMsg(b) {
				c.sendRelay(p, b)
			}
		}
	case noRelay:
		// In the background, not to hold up WireGuard for the dial; at most
		// one at a time (relayAsync), the rest went direct only.
		for _, b := range bufs {
			if !isHandshakeMsg(b) {
				cp := append([]byte(nil), b...)
				c.relayAsync(p, func() { c.sendRelay(p, cp) })
				break
			}
		}
	}
	return err
}

// wakeCopy: a packet sent through the relay as well just after waking
// (sendWG). Not a handshake message, which goes one way only, and small:
// the first exchange after a pause is small packets, and a transfer that
// starts at once should not pour through the relay for two seconds.
func wakeCopy(b []byte) bool {
	return len(b) <= dupMaxSize && !isHandshakeMsg(b)
}

// isKeepalive recognizes WireGuard's keepalive: an empty data message, 32
// bytes. WireGuard sends one 10 s after receiving data if it sent nothing;
// it is not traffic, and counting it would keep idle peers awake.
func isKeepalive(b []byte) bool {
	return len(b) == 32 && b[0] == 4 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

func onlyKeepalives(bufs [][]byte) bool {
	for _, b := range bufs {
		if !isKeepalive(b) {
			return false
		}
	}
	return true
}

// isHandshakeMsg recognizes WireGuard's handshake messages: initiation
// (type 1, 148 bytes), response (type 2, 92) and cookie reply (type 3, 64).
func isHandshakeMsg(b []byte) bool {
	if len(b) < 4 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
		return false
	}
	return b[0] == 1 && len(b) == 148 || b[0] == 2 && len(b) == 92 || b[0] == 3 && len(b) == 64
}

// isHandshakeInitiation recognizes WireGuard's first handshake message:
// type 1, 148 bytes.
func isHandshakeInitiation(b []byte) bool {
	return len(b) == 148 && b[0] == 1 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

type wgEndpoint = conn.Endpoint

type peerEndpoint struct{ key proto.NodePublic }

func (e *peerEndpoint) ClearSrc()           {}
func (e *peerEndpoint) SrcToString() string { return "" }
func (e *peerEndpoint) DstToString() string { return hex.EncodeToString(e.key[:]) }
func (e *peerEndpoint) DstToBytes() []byte  { return e.key[:] }
func (e *peerEndpoint) DstIP() netip.Addr   { return netip.Addr{} }
func (e *peerEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }

// addrEndpoint marks a packet from a UDP address not yet tied to a peer.
type addrEndpoint struct{ addr netip.AddrPort }

func (e *addrEndpoint) ClearSrc()           {}
func (e *addrEndpoint) SrcToString() string { return "" }
func (e *addrEndpoint) DstToString() string { return e.addr.String() }
func (e *addrEndpoint) DstToBytes() []byte  { b, _ := e.addr.MarshalBinary(); return b }
func (e *addrEndpoint) DstIP() netip.Addr   { return e.addr.Addr() }
func (e *addrEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }
