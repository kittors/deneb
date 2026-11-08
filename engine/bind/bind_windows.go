package bind

import (
	"encoding/binary"
	"net"

	"golang.org/x/sys/windows"
)

const (
	ipUnicastIf   = 31 // IP_UNICAST_IF
	ipv6UnicastIf = 31 // IPV6_UNICAST_IF
)

// Windows: IP_UNICAST_IF. For IPv4 the index goes in network byte order,
// for IPv6 in host order (MSDN, IPPROTO_IP socket options).
func pin(fd uintptr, ifi *net.Interface, network string) error {
	h := windows.Handle(fd)
	if network == "udp6" || network == "tcp6" {
		return windows.SetsockoptInt(h, windows.IPPROTO_IPV6, ipv6UnicastIf, ifi.Index)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(ifi.Index))
	return windows.SetsockoptInt(h, windows.IPPROTO_IP, ipUnicastIf, int(binary.NativeEndian.Uint32(b[:])))
}
