package main

import (
	"encoding/binary"
	"log"
	"net/netip"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// answerDNS is the kernel-TUN mode resolver. The OS only sends it queries for
// *.deneb.internal (via /etc/resolver/deneb.internal), and those queries go
// to an address inside Deneb's own block, so they travel through Deneb's
// interface and a proxy's DNS hijack on its own TUN never sees them.
// Anything else is answered REFUSED rather than forwarded: this resolver is
// not anyone's general-purpose DNS.
func (n *node) answerDNS(pkt []byte) []byte {
	h, ok := parseIPv4(pkt)
	if !ok || h.proto != protoUDP || h.dst != n.resolverAddr || h.dport != 53 {
		return nil
	}
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 {
		return []byte{} // swallow garbage aimed at the resolver
	}
	var p dnsmessage.Parser
	hdr, err := p.Start(pkt[ihl+8:])
	if err != nil {
		return []byte{}
	}
	q, err := p.Question()
	if err != nil {
		return []byte{}
	}
	resp := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired},
		Questions: []dnsmessage.Question{q},
	}
	name := strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")
	verdict := ""
	if name != nameSuffix && !strings.HasSuffix(name, "."+nameSuffix) {
		resp.RCode = dnsmessage.RCodeRefused
		verdict = "refused (not a Deneb name)"
	} else if addr, _, err := n.resolve(name); err != nil {
		resp.RCode = dnsmessage.RCodeNameError
		verdict = "NXDOMAIN: " + err.Error()
	} else if q.Type == dnsmessage.TypeA {
		resp.Answers = []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 5},
			Body:   &dnsmessage.AResource{A: addr.As4()},
		}}
		verdict = addr.String()
	} else {
		verdict = "no " + q.Type.String() + " record (name exists)"
	}
	out, err := resp.Pack()
	if err != nil {
		return []byte{}
	}
	log.Printf("[dns] %s %s from %s -> %s", q.Type, name, h.src, verdict)
	return udp4(n.resolverAddr, 53, h.src, h.sport, out)
}

// udp4 builds an IPv4/UDP packet with valid checksums.
func udp4(src netip.Addr, sport uint16, dst netip.Addr, dport uint16, payload []byte) []byte {
	pkt := make([]byte, 28+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = protoUDP
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:], ipChecksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:], sport)
	binary.BigEndian.PutUint16(pkt[22:], dport)
	binary.BigEndian.PutUint16(pkt[24:], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	// UDP checksum over the pseudo-header, header and payload.
	ph := make([]byte, 0, 12+8+len(payload)+1)
	ph = append(ph, s4[:]...)
	ph = append(ph, d4[:]...)
	ph = append(ph, 0, protoUDP)
	ph = binary.BigEndian.AppendUint16(ph, uint16(8+len(payload)))
	ph = append(ph, pkt[20:]...)
	if len(ph)%2 == 1 {
		ph = append(ph, 0)
	}
	cs := ipChecksum(ph)
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(pkt[26:], cs)
	return pkt
}
