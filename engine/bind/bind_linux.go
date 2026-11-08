package bind

import (
	"net"

	"golang.org/x/sys/unix"
)

// Linux: SO_BINDTODEVICE. It needs CAP_NET_RAW on older kernels; the engine
// falls back to unpinned sockets when it fails.
func pin(fd uintptr, ifi *net.Interface, network string) error {
	return unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, ifi.Name)
}
