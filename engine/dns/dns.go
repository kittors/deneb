// Package dns answers *.deneb.internal for ordinary programs in kernel
// mode. The system sends only those names here (macOS: the file in
// /etc/resolver), and to an address inside Deneb's own block, so the queries
// travel through Deneb's interface and a proxy's DNS hijack on its own TUN
// never sees them (multi-lan-tech §6.8). Anything else is answered REFUSED
// rather than forwarded: this is nobody's general-purpose resolver.
package dns

import (
	"encoding/binary"
	"log"
	"net/netip"
	"strings"
	"sync/atomic"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/names"
)

// Responder answers DNS packets addressed to its address.
type Responder struct {
	Addr  netip.Addr // inside the deployment's block, e.g. 10.77.0.53
	table atomic.Pointer[names.Table]
	logf  func(format string, args ...any)

	// OnRevived is called (in its own goroutine) with names a new table has
	// that the one before did not: a device joined or was put back into a
	// LAN. macOS keeps "no such name" in caches of its own well past the
	// SOA's minimum (11-08 kernel round), so the engine flushes them then.
	OnRevived func(names []string)
}

func New(addr netip.Addr, logf func(format string, args ...any)) *Responder {
	if logf == nil {
		logf = log.Printf
	}
	r := &Responder{Addr: addr, logf: logf}
	r.table.Store(names.NewTable("", nil))
	return r
}

// SetTable swaps in the names of a new network map.
func (r *Responder) SetTable(t *names.Table) {
	old := r.table.Swap(t)
	if old == nil || r.OnRevived == nil {
		return
	}
	// Names the old table did not have: a device joined, or came back to a
	// LAN. The system may have been told they do not exist and keep that
	// in caches of its own; whether a query for them reached this responder
	// or was answered from such a cache, the engine flushes them.
	had := map[string]bool{}
	for _, e := range old.Entries() {
		had[e.FQDN] = true
	}
	var added []string
	for _, e := range t.Entries() {
		if e.FQDN != "" && !had[e.FQDN] {
			added = append(added, e.FQDN)
		}
	}
	if len(added) > 0 {
		go r.OnRevived(added)
	}
}

// Answer is a filter.TUN LocalOut hook: it takes UDP packets to Addr:53 and
// returns the reply. ok is false for every other packet.
func (r *Responder) Answer(pkt []byte) (reply []byte, ok bool) {
	h, parsed := filter.Parse(pkt)
	if !parsed || h.Proto != filter.ProtoUDP || h.Dst != r.Addr || h.DPort != 53 || h.Fragment {
		return nil, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	var p dnsmessage.Parser
	hdr, err := p.Start(pkt[ihl+8:])
	if err != nil {
		return nil, true // swallow garbage aimed at the resolver
	}
	q, err := p.Question()
	if err != nil {
		return nil, true
	}
	resp := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired},
		Questions: []dnsmessage.Question{q},
	}
	t := r.table.Load()
	name := q.Name.String()
	switch {
	case !t.IsDenebName(name):
		resp.RCode = dnsmessage.RCodeRefused
	default:
		e, err := t.Resolve(name)
		switch {
		case err != nil:
			resp.RCode = dnsmessage.RCodeNameError
			resp.Authorities = negativeSOA(t.Domain())
		case q.Type == dnsmessage.TypeA && e.Addr.Is4():
			resp.Answers = []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 5},
				Body:   &dnsmessage.AResource{A: e.Addr.As4()},
			}}
		default:
			// The name exists but has no record of this type (M1 has
			// no IPv6 inside the tunnel): NOERROR with no answer.
			resp.Authorities = negativeSOA(t.Domain())
		}
	}
	out, err := resp.Pack()
	if err != nil {
		return nil, true
	}
	return UDP4(r.Addr, 53, h.Src, h.SPort, out), true
}

// UDP4 builds an IPv4/UDP packet with valid checksums.
func UDP4(src netip.Addr, sport uint16, dst netip.Addr, dport uint16, payload []byte) []byte {
	pkt := make([]byte, 28+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = filter.ProtoUDP
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:], Checksum(pkt[:20], 0))
	binary.BigEndian.PutUint16(pkt[20:], sport)
	binary.BigEndian.PutUint16(pkt[22:], dport)
	binary.BigEndian.PutUint16(pkt[24:], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	var ph [12]byte
	copy(ph[0:4], s4[:])
	copy(ph[4:8], d4[:])
	ph[9] = filter.ProtoUDP
	binary.BigEndian.PutUint16(ph[10:], uint16(8+len(payload)))
	cs := Checksum(pkt[20:], sum(ph[:]))
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(pkt[26:], cs)
	return pkt
}

// Checksum is the Internet checksum of b, starting from a partial sum.
func Checksum(b []byte, initial uint32) uint16 {
	s := initial + sum(b)
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

func sum(b []byte) uint32 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	return s
}

// negativeSOA goes with "no such name" and "no such record": per RFC 2308
// a resolver caches the negative answer for the SOA's minimum, 1 s here.
// Without it macOS's resolver kept "no such name" for longer than 8 s, so a
// device put back into a LAN stayed unresolvable after it answered pings.
func negativeSOA(domain string) []dnsmessage.Resource {
	zone, err := dnsmessage.NewName(strings.TrimSuffix(domain, ".") + ".")
	if err != nil {
		return nil
	}
	ns, _ := dnsmessage.NewName("ns." + zone.String())
	mbox, _ := dnsmessage.NewName("hostmaster." + zone.String())
	return []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{Name: zone, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 1},
		Body:   &dnsmessage.SOAResource{NS: ns, MBox: mbox, Serial: 1, Refresh: 60, Retry: 10, Expire: 600, MinTTL: 1},
	}}
}
