package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

const (
	protoICMP = 1
	protoTCP  = 6
	protoUDP  = 17
)

type svc struct {
	proto uint8
	port  uint16 // 0 for ICMP
}

// policy is an immutable snapshot; the node swaps in a new one whenever a LAN
// is paused, resumed, joined or left.
type policy struct {
	self     netip.Addr
	peers    map[netip.Addr]*peerPol // only peers that share an *active* LAN
	open     map[string][]svc        // lan id -> services this device opens to that LAN
	extraOut []netip.Prefix          // attack tests only: let this node *send* to non-peers
	noFilter bool                    // attack tests only: turn the filter off entirely
}

type peerPol struct {
	label string
	lans  []string // active LANs shared with this peer
}

func (p *policy) allows(lan string, h pktInfo) bool {
	for _, s := range p.open[lan] {
		if s.proto != h.proto {
			continue
		}
		if h.proto == protoICMP || s.port == h.dport {
			return true
		}
	}
	return false
}

type pktInfo struct {
	proto        uint8
	src, dst     netip.Addr
	sport, dport uint16 // ICMP echo: both hold the echo identifier
	tcpSYN       bool   // SYN without ACK: a brand new TCP connection
	icmpEcho     bool   // echo request or reply
}

func parseIPv4(b []byte) (pktInfo, bool) {
	var h pktInfo
	if len(b) < 20 || b[0]>>4 != 4 {
		return h, false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl {
		return h, false
	}
	h.proto = b[9]
	h.src = netip.AddrFrom4([4]byte(b[12:16]))
	h.dst = netip.AddrFrom4([4]byte(b[16:20]))
	l4 := b[ihl:]
	switch h.proto {
	case protoTCP:
		if len(l4) < 14 {
			return h, false
		}
		h.sport = binary.BigEndian.Uint16(l4[0:2])
		h.dport = binary.BigEndian.Uint16(l4[2:4])
		flags := l4[13]
		h.tcpSYN = flags&0x02 != 0 && flags&0x10 == 0
	case protoUDP:
		if len(l4) < 8 {
			return h, false
		}
		h.sport = binary.BigEndian.Uint16(l4[0:2])
		h.dport = binary.BigEndian.Uint16(l4[2:4])
	case protoICMP:
		if len(l4) < 8 {
			return h, false
		}
		if l4[0] == 8 || l4[0] == 0 {
			h.icmpEcho = true
			id := binary.BigEndian.Uint16(l4[4:6])
			h.sport, h.dport = id, id
		}
	}
	return h, true
}

// flowKey is always written from this device's point of view, so a reply in
// either direction maps to the same key.
type flowKey struct {
	proto         uint8
	local, remote netip.AddrPort
}

// filterTUN sits between WireGuard and the user-space TCP/IP stack. It is the
// only thing that keeps LANs apart on a device that is in several of them, so
// it is written to fail closed: anything it does not understand is dropped.
type filterTUN struct {
	inner tun.Device
	pol   atomic.Pointer[policy]

	mu    sync.Mutex
	flows map[flowKey]time.Time
	drops map[string]uint64
	seen  map[string]time.Time // rate-limits drop logging per (reason, src, dst)

	out    chan []byte // packets from the stack, pumped by readLoop
	inject chan []byte // spoof tests: packets that bypass the filter on purpose
	errc   chan error

	// local answers packets addressed to services that live inside Deneb
	// itself (the DNS responder in kernel-TUN mode). It returns a reply to
	// hand back to the OS, or nil if the packet is not for it.
	local func(pkt []byte) []byte
}

// tunHeadroom is the space kept in front of each packet: the macOS utun
// driver prepends a 4-byte address-family header, and wireguard-go uses 16.
const tunHeadroom = 16

func newFilterTUN(inner tun.Device) *filterTUN {
	f := &filterTUN{
		inner:  inner,
		flows:  map[flowKey]time.Time{},
		drops:  map[string]uint64{},
		seen:   map[string]time.Time{},
		out:    make(chan []byte, 256),
		inject: make(chan []byte, 16),
		errc:   make(chan error, 1),
	}
	f.pol.Store(&policy{})
	go f.readLoop()
	go f.gcLoop()
	return f
}

func (f *filterTUN) readLoop() {
	bufs := [][]byte{make([]byte, tunHeadroom+65535)}
	sizes := []int{0}
	for {
		n, err := f.inner.Read(bufs, sizes, tunHeadroom)
		if err != nil {
			f.errc <- err
			return
		}
		for i := 0; i < n; i++ {
			pkt := bufs[i][tunHeadroom : tunHeadroom+sizes[i]]
			if f.local != nil {
				if reply := f.local(pkt); reply != nil {
					if len(reply) > 0 { // an empty reply means "swallow it"
						f.writeToOS(reply)
					}
					continue
				}
			}
			f.out <- append([]byte(nil), pkt...)
		}
	}
}

// writeToOS hands a packet generated inside Deneb back to the local stack.
func (f *filterTUN) writeToOS(pkt []byte) {
	buf := make([]byte, tunHeadroom+len(pkt))
	copy(buf[tunHeadroom:], pkt)
	if _, err := f.inner.Write([][]byte{buf}, tunHeadroom); err != nil {
		log.Printf("[filter] write local reply: %v", err)
	}
}

func (f *filterTUN) gcLoop() {
	for range time.Tick(30 * time.Second) {
		f.mu.Lock()
		for k, t := range f.flows {
			if time.Since(t) > 2*time.Minute {
				delete(f.flows, k)
			}
		}
		f.mu.Unlock()
	}
}

// Read hands outbound packets (stack -> WireGuard) to wireguard-go.
func (f *filterTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		var pkt []byte
		bypass := false
		select {
		case pkt = <-f.out:
		case pkt = <-f.inject:
			bypass = true
		case err := <-f.errc:
			return 0, err
		}
		if !bypass {
			if ok, why := f.allowOut(pkt); !ok {
				f.noteDrop("out", why, pkt)
				continue
			}
		}
		sizes[0] = copy(bufs[0][offset:], pkt)
		return 1, nil
	}
}

// Write takes inbound packets (WireGuard -> stack). wireguard-go has already
// checked that the source address belongs to the sending peer.
func (f *filterTUN) Write(bufs [][]byte, offset int) (int, error) {
	keep := bufs[:0:0]
	for _, b := range bufs {
		pkt := b[offset:]
		if ok, why := f.allowIn(pkt); !ok {
			f.noteDrop("in", why, pkt)
			continue
		}
		keep = append(keep, b)
	}
	if len(keep) == 0 {
		return len(bufs), nil
	}
	if _, err := f.inner.Write(keep, offset); err != nil {
		return 0, err
	}
	return len(bufs), nil
}

func (f *filterTUN) allowIn(pkt []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := parseIPv4(pkt)
	if !ok {
		return false, "not-ipv4"
	}
	if pol.noFilter {
		return true, ""
	}
	// Rule 1: never act as a router. A packet that is not addressed to this
	// device is someone trying to go *through* it, e.g. into another LAN.
	if h.dst != pol.self {
		return false, "transit-in"
	}
	pp := pol.peers[h.src]
	if pp == nil {
		return false, "not-in-an-active-shared-lan"
	}
	key := flowKey{h.proto, netip.AddrPortFrom(h.dst, h.dport), netip.AddrPortFrom(h.src, h.sport)}
	if f.touchFlow(key) {
		return true, "" // reply to something this device started, or an accepted flow
	}
	if h.proto == protoTCP && !h.tcpSYN {
		return false, "tcp-without-flow"
	}
	// Rule 2: a new inbound flow is allowed only if this device opened that
	// service to a LAN that the sender shares with it.
	for _, lan := range pp.lans {
		if pol.allows(lan, h) {
			f.addFlow(key)
			return true, ""
		}
	}
	return false, "service-not-open-to-this-lan"
}

func (f *filterTUN) allowOut(pkt []byte) (bool, string) {
	pol := f.pol.Load()
	h, ok := parseIPv4(pkt)
	if !ok {
		return false, "not-ipv4"
	}
	if pol.noFilter {
		return true, ""
	}
	// Rule 1, outbound half: only send packets that this device originated.
	// This is what stops transit if the OS ever forwards a packet back out
	// (kernel IP forwarding is on by default on any Linux host running Docker).
	if h.src != pol.self {
		return false, "transit-out"
	}
	key := flowKey{h.proto, netip.AddrPortFrom(h.src, h.sport), netip.AddrPortFrom(h.dst, h.dport)}
	if f.touchFlow(key) {
		return true, ""
	}
	if pol.peers[h.dst] == nil {
		allowed := false
		for _, p := range pol.extraOut {
			if p.Contains(h.dst) {
				allowed = true
			}
		}
		if !allowed {
			return false, "not-a-visible-peer"
		}
	}
	f.addFlow(key)
	return true, ""
}

func (f *filterTUN) touchFlow(k flowKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.flows[k]; ok {
		f.flows[k] = time.Now()
		return true
	}
	return false
}

func (f *filterTUN) addFlow(k flowKey) {
	f.mu.Lock()
	f.flows[k] = time.Now()
	f.mu.Unlock()
}

// forgetPeer drops tracked flows with a peer, so pausing a LAN also cuts the
// replies of connections that were already open.
func (f *filterTUN) forgetPeer(a netip.Addr) {
	f.mu.Lock()
	for k := range f.flows {
		if k.remote.Addr() == a {
			delete(f.flows, k)
		}
	}
	f.mu.Unlock()
}

func (f *filterTUN) noteDrop(dir, why string, pkt []byte) {
	h, _ := parseIPv4(pkt)
	f.mu.Lock()
	f.drops[dir+":"+why]++
	k := fmt.Sprintf("%s|%s|%s|%s", dir, why, h.src, h.dst)
	logIt := time.Since(f.seen[k]) > 5*time.Second
	if logIt {
		f.seen[k] = time.Now()
	}
	f.mu.Unlock()
	if logIt {
		log.Printf("[filter] drop %s %s: %s -> %s proto=%d dport=%d", dir, why, h.src, h.dst, h.proto, h.dport)
	}
}

func (f *filterTUN) dropCounts() map[string]uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := make(map[string]uint64, len(f.drops))
	for k, v := range f.drops {
		m[k] = v
	}
	return m
}

func (f *filterTUN) File() *os.File           { return nil }
func (f *filterTUN) MTU() (int, error)        { return f.inner.MTU() }
func (f *filterTUN) Name() (string, error)    { return "deneb-filter", nil }
func (f *filterTUN) Events() <-chan tun.Event { return f.inner.Events() }
func (f *filterTUN) Close() error             { return f.inner.Close() }
func (f *filterTUN) BatchSize() int           { return 1 }
