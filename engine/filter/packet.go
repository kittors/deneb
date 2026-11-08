package filter

import (
	"encoding/binary"
	"net/netip"
)

// IP protocol numbers the filter understands. Anything else is dropped.
const (
	ProtoICMP = 1
	ProtoTCP  = 6
	ProtoUDP  = 17
)

// ICMPv4 types the filter reasons about.
const (
	icmpEchoReply    = 0
	icmpUnreachable  = 3
	icmpEchoRequest  = 8
	icmpTimeExceeded = 11
	icmpParamProblem = 12
)

// Packet is what the filter reads from an IPv4 header and the start of its
// payload. It never trusts a length it has not checked.
type Packet struct {
	Proto        uint8
	Src, Dst     netip.Addr
	SPort, DPort uint16 // ICMP echo: both hold the echo identifier
	ID           uint16 // IPv4 identification, for fragments

	TCPSYN    bool // SYN without ACK: a brand new TCP connection
	TCPFinRst bool // FIN or RST: the connection is ending

	ICMPType uint8
	// Inner is the packet an ICMP error quotes (unreachable, time
	// exceeded): the one it is an error about.
	Inner *Packet

	FirstFragment bool   // more fragments follow; this one has the L4 header
	Fragment      bool   // a later fragment: no L4 header at all
	FragOffset    uint16 // in bytes
}

// Parse reads an IPv4 packet. ok is false for anything it cannot fully
// read, which the filter then drops: it fails closed.
func Parse(b []byte) (Packet, bool) {
	return parse(b, false)
}

func parse(b []byte, quoted bool) (Packet, bool) {
	var h Packet
	if len(b) < 20 || b[0]>>4 != 4 {
		return h, false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl {
		return h, false
	}
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if !quoted {
		// A quoted header inside an ICMP error carries the original total
		// length, of which only the start is quoted; elsewhere the claimed
		// length must fit the bytes we have.
		if total < ihl || total > len(b) {
			return h, false
		}
		b = b[:total]
	}
	h.Proto = b[9]
	h.ID = binary.BigEndian.Uint16(b[4:6])
	h.Src = netip.AddrFrom4([4]byte(b[12:16]))
	h.Dst = netip.AddrFrom4([4]byte(b[16:20]))
	frag := binary.BigEndian.Uint16(b[6:8])
	more := frag&0x2000 != 0
	h.FragOffset = (frag & 0x1fff) * 8
	if h.FragOffset > 0 {
		h.Fragment = true
		return h, true
	}
	h.FirstFragment = more
	l4 := b[ihl:]
	switch h.Proto {
	case ProtoTCP:
		// The whole fixed header must be in the first fragment, or a later
		// fragment could rewrite flags the filter already judged (RFC 1858).
		need := 20
		if quoted {
			need = 8 // an ICMP error quotes at least 8 bytes: the ports
		}
		if len(l4) < need {
			return h, false
		}
		h.SPort = binary.BigEndian.Uint16(l4[0:2])
		h.DPort = binary.BigEndian.Uint16(l4[2:4])
		if !quoted {
			flags := l4[13]
			h.TCPSYN = flags&0x02 != 0 && flags&0x10 == 0
			h.TCPFinRst = flags&0x05 != 0
		}
	case ProtoUDP:
		if len(l4) < 8 {
			return h, false
		}
		h.SPort = binary.BigEndian.Uint16(l4[0:2])
		h.DPort = binary.BigEndian.Uint16(l4[2:4])
	case ProtoICMP:
		if len(l4) < 8 {
			return h, false
		}
		h.ICMPType = l4[0]
		switch h.ICMPType {
		case icmpEchoRequest, icmpEchoReply:
			id := binary.BigEndian.Uint16(l4[4:6])
			h.SPort, h.DPort = id, id
		case icmpUnreachable, icmpTimeExceeded, icmpParamProblem:
			if quoted {
				return h, false // an error about an error is never needed
			}
			inner, ok := parse(l4[8:], true)
			if !ok || inner.Fragment {
				return h, false
			}
			h.Inner = &inner
		}
	default:
		return h, false
	}
	return h, true
}
