package main

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindControl pins a socket to one interface with IP_BOUND_IF. With a proxy's
// TUN owning the routing table (Clash/mihomo routes 1/8..128/1 into utun),
// this is how our underlay traffic leaves through the real Wi-Fi instead of
// being re-originated by the proxy, without touching the proxy or routes.
func bindControl(ifname string) (func(network, address string, c syscall.RawConn) error, error) {
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("bind-if %s: %w", ifname, err)
	}
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifi.Index)
		})
		if err != nil {
			return err
		}
		return serr
	}, nil
}
