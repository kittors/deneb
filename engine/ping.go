package engine

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/dns"
	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// The engine pings with ICMP echo through the tunnel, made and read by the
// engine itself: the probe passes the same visibility and filter checks as
// any packet, and it does not depend on the user-space stack's ping socket,
// which loses replies that arrive in a burst (multi-lan-tech §6.7, item 1).
type pinger struct {
	mu    sync.Mutex
	waits map[uint32]chan time.Time // echo id<<16 | seq
}

func newPinger() *pinger { return &pinger{waits: map[uint32]chan time.Time{}} }

// consume takes echo replies to the engine's own pings (filter.TUN's
// ConsumeIn hook).
func (p *pinger) consume(pkt []byte) bool {
	if len(pkt) < 28 || pkt[0]>>4 != 4 || pkt[9] != filter.ProtoICMP {
		return false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 || pkt[ihl] != 0 { // echo reply
		return false
	}
	id := binary.BigEndian.Uint16(pkt[ihl+4:])
	seq := binary.BigEndian.Uint16(pkt[ihl+6:])
	p.mu.Lock()
	ch := p.waits[uint32(id)<<16|uint32(seq)]
	p.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- time.Now():
	default:
	}
	return true
}

func icmpEcho(src, dst netip.Addr, id, seq uint16) []byte {
	payload := make([]byte, 24)
	copy(payload, "deneb ping")
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = filter.ProtoICMP
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:], dns.Checksum(pkt[:20], 0))
	pkt[20] = 8 // echo request
	binary.BigEndian.PutUint16(pkt[24:], id)
	binary.BigEndian.PutUint16(pkt[26:], seq)
	copy(pkt[28:], payload)
	binary.BigEndian.PutUint16(pkt[22:], dns.Checksum(pkt[20:], 0))
	return pkt
}

// Ping probes a peer n times.
func (e *Engine) Ping(ctx context.Context, to string, n int, interval, timeout time.Duration, each func(apitype.PingResult)) (*apitype.PingSummary, error) {
	ent, err := e.resolve(to)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	self, tw := e.self, e.tunw
	e.mu.Unlock()
	if tw == nil {
		return nil, errNotLoggedIn
	}
	if ent.Addr == self {
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "这就是这台设备。"}
	}
	peer, ok := e.peerByAddr(ent.Addr)
	if !ok {
		return nil, &apitype.Error{Code: apitype.ErrNotFound, Message: fmt.Sprintf("在你的局域网里找不到 %s。", to)}
	}
	if peer.RefusedByPeer {
		return nil, &apitype.Error{Code: apitype.ErrRefused, Message: "这台设备的主人没让你用它。"}
	}
	var idb [2]byte
	rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:]) | 0x8000
	sum := &apitype.PingSummary{To: to, Addr: ent.Addr.String(), Device: apitype.DeviceID(ent.Device)}
	var total float64
	for seq := 0; seq < n; seq++ {
		if seq > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		}
		res := apitype.PingResult{Seq: seq}
		kind, ep, relay := e.ms.SendPath(peer.NodeKey)
		res.Path = apitype.PathKind(kind)
		if kind == "direct" {
			res.Endpoint = ep.String()
		} else {
			res.Endpoint = string(relay)
		}
		key := uint32(id)<<16 | uint32(seq)
		ch := make(chan time.Time, 1)
		e.pinger.mu.Lock()
		e.pinger.waits[key] = ch
		e.pinger.mu.Unlock()
		start := time.Now()
		if err := tw.Send(icmpEcho(self, ent.Addr, id, uint16(seq))); err != nil {
			res.Err = err.Error()
		} else {
			select {
			case at := <-ch:
				res.RTTMs = float64(at.Sub(start).Microseconds()) / 1000
			case <-time.After(timeout):
				res.Err = "timeout"
			case <-ctx.Done():
				res.Err = "canceled"
			}
		}
		e.pinger.mu.Lock()
		delete(e.pinger.waits, key)
		e.pinger.mu.Unlock()
		sum.Sent++
		if res.Err == "" {
			sum.Received++
			total += res.RTTMs
			if sum.MinMs == 0 || res.RTTMs < sum.MinMs {
				sum.MinMs = res.RTTMs
			}
			sum.MaxMs = max(sum.MaxMs, res.RTTMs)
		}
		sum.Results = append(sum.Results, res)
		if each != nil {
			each(res)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if sum.Received > 0 {
		sum.AvgMs = total / float64(sum.Received)
	}
	return sum, nil
}
