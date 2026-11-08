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
	woke := false
	if !onlyKeepalives(bufs) {
		woke = c.noteTrafficLocked(p, now)
	}
	direct := c.sendDirectLocked(p, now)
	to := p.best
	blackhole := direct && p.blocked // simulated UDP block: lost on the wire
	lossTx := p.lossTx
	stale := p.kind == kindDirect && !direct
	// Just back from idle: the last direct address may well still work, and
	// the relay surely does. Send both ways until one answers; WireGuard
	// drops the copy that arrives second.
	dual := !direct && to.IsValid() && !p.degraded && !p.blocked && now.Sub(p.wokeAt) < wakeDual &&
		now.Sub(p.lastPong) < 3*time.Minute
	confirmed := direct || c.relayUpLocked(p, now)
	for _, b := range bufs {
		if isHandshakeInitiation(b) {
			// Kept until the peer's response arrives, and sent again if it
			// does not come in a few round trips: one lost packet would
			// otherwise cost WireGuard's 5 s retry (a first connection on
			// Wi-Fi, a peer just back from a pause).
			p.pendingInit, p.pendingInitAt = append([]byte(nil), b...), now
			p.initResentAt = time.Time{}
			if confirmed {
				p.initResentAt = now // this copy goes out now, on a known path
			}
		}
	}
	if direct {
		p.txDirect += uint64(len(bufs))
	} else {
		p.txRelay += uint64(len(bufs))
	}
	c.mu.Unlock()
	if woke {
		go c.wake(p)
	}
	if stale {
		c.updatePath(p)
	}
	if blackhole {
		return nil
	}
	var err error
	for _, b := range bufs {
		if direct || dual {
			if lossTx == 0 || mrand.Float64() >= lossTx {
				_, err = c.sock().WriteToUDPAddrPort(b, to)
			}
		}
		if !direct {
			err = c.sendRelay(p, b)
		}
	}
	return err
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
