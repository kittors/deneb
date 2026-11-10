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
	TCPRst    bool // RST: never answered with another
	// TCPEnd is the sequence number after this segment (SEQ + data, plus
	// one each for SYN and FIN): the next one its receiver expects once it
	// has it. What a reset must carry to be taken (RFC 5961).
	TCPEnd uint32

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
			h.TCPRst = flags&0x04 != 0
			end := binary.BigEndian.Uint32(l4[4:8])
			if off := int(l4[12]>>4) * 4; off >= 20 && off <= len(l4) {
				end += uint32(len(l4) - off)
			}
			if flags&0x02 != 0 {
				end++
			}
			if flags&0x01 != 0 {
				end++
			}
			h.TCPEnd = end
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

// ResetFor makes the TCP reset that answers b, an IPv4 TCP segment of a
// connection this side does not have (RFC 9293 §3.10.7.1): from b's
// destination to its source, SEQ=SEG.ACK if b carries an ACK, else SEQ=0
// with ACK=SEG.SEQ+SEG.LEN. Nil for a reset (never answered) or anything
// it cannot read.
func ResetFor(b []byte) []byte {
	if len(b) < 20 || b[0]>>4 != 4 || b[9] != ProtoTCP {
		return nil
	}
	ihl := int(b[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if ihl < 20 || total > len(b) || total < ihl+20 {
		return nil
	}
	seg := b[ihl:total]
	off := int(seg[12]>>4) * 4
	flags := seg[13]
	if off < 20 || off > len(seg) || flags&0x04 != 0 {
		return nil
	}
	segLen := uint32(len(seg) - off)
	if flags&0x02 != 0 { // SYN
		segLen++
	}
	if flags&0x01 != 0 { // FIN
		segLen++
	}
	r := make([]byte, 40)
	r[0] = 0x45
	binary.BigEndian.PutUint16(r[2:], 40)
	r[8] = 64
	r[9] = ProtoTCP
	copy(r[12:16], b[16:20])
	copy(r[16:20], b[12:16])
	binary.BigEndian.PutUint16(r[10:], checksum(r[:20], 0))
	t := r[20:]
	copy(t[0:2], seg[2:4])
	copy(t[2:4], seg[0:2])
	t[12] = 5 << 4
	if flags&0x10 != 0 {
		copy(t[4:8], seg[8:12]) // SEQ = SEG.ACK
		t[13] = 0x04            // RST
	} else {
		binary.BigEndian.PutUint32(t[8:], binary.BigEndian.Uint32(seg[4:8])+segLen)
		t[13] = 0x14 // RST, ACK
	}
	var pseudo uint32 = ProtoTCP + uint32(len(t))
	for i := 12; i < 20; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(r[i:]))
	}
	binary.BigEndian.PutUint16(t[16:], checksum(t, pseudo))
	return r
}

// checksum is the Internet checksum of b, starting from sum (a pseudo-
// header's, for TCP).
func checksum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// resetWith makes a TCP reset from src to dst (each "address:port"), with
// SEQ=seq: the reset of a connection this side cut, to the end that
// expects seq next.
func resetWith(src, dst netip.AddrPort, seq uint32) []byte {
	if !src.Addr().Is4() || !dst.Addr().Is4() {
		return nil
	}
	r := make([]byte, 40)
	r[0] = 0x45
	binary.BigEndian.PutUint16(r[2:], 40)
	r[8] = 64
	r[9] = ProtoTCP
	s4, d4 := src.Addr().As4(), dst.Addr().As4()
	copy(r[12:16], s4[:])
	copy(r[16:20], d4[:])
	binary.BigEndian.PutUint16(r[10:], checksum(r[:20], 0))
	t := r[20:]
	binary.BigEndian.PutUint16(t[0:], src.Port())
	binary.BigEndian.PutUint16(t[2:], dst.Port())
	binary.BigEndian.PutUint32(t[4:], seq)
	t[12] = 5 << 4
	t[13] = 0x04 // RST
	var pseudo uint32 = ProtoTCP + uint32(len(t))
	for i := 12; i < 20; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(r[i:]))
	}
	binary.BigEndian.PutUint16(t[16:], checksum(t, pseudo))
	return r
}
