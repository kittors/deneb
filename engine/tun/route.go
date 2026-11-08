package tun

import (
	"net/netip"
	"strings"
)

// ParseRouteGet reads `route -n get` output (macOS): the prefix length of
// the route that matched and its interface. A host route prints no mask.
func ParseRouteGet(out string) (bits int, iface string) {
	bits = 32
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "destination":
			if v == "default" {
				bits = 0
			}
		case "mask":
			if m, err := netip.ParseAddr(v); err == nil && m.Is4() {
				bits = 0
				for _, x := range m.As4() {
					for ; x&0x80 != 0; x <<= 1 {
						bits++
					}
				}
			}
		case "interface":
			iface = v
		}
	}
	return bits, iface
}

// lastAddr is the block's last usable-looking address.
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := (uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])) | host
	v-- // skip the broadcast-looking address
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
