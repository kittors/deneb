package bind

import (
	"net"

	"golang.org/x/sys/unix"
)

// macOS: IP_BOUND_IF, which the prototype used (multi-lan-tech §6.5).
func pin(fd uintptr, ifi *net.Interface, network string) error {
	if network == "udp6" || network == "tcp6" {
		return unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, ifi.Index)
	}
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifi.Index)
}
