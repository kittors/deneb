// Package bind pins the engine's Internet-facing sockets (hole punching
// UDP, STUN, the relay's TLS) to the physical interface, so a proxy's TUN
// never carries them (multi-lan-tech §3.7, §6.5): unpinned, a "direct" path
// quietly becomes a detour through the proxy server at five times the delay.
package bind

import (
	"fmt"
	"net"
	"strings"
	"syscall"

	"tailscale.com/net/netmon"
)

// ControlFunc is what net.Dialer.Control and net.ListenConfig.Control take.
type ControlFunc = func(network, address string, c syscall.RawConn) error

// Control returns a ControlFunc that pins sockets to the named interface.
func Control(ifname string) (ControlFunc, error) {
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("bind to %s: %w", ifname, err)
	}
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = pin(fd, ifi, network) }); err != nil {
			return err
		}
		if serr != nil {
			return fmt.Errorf("bind to %s: %w", ifi.Name, serr)
		}
		return nil
	}, nil
}

// DefaultInterface is the interface that owns the default route: Wi-Fi or
// Ethernet. A proxy that takes the whole Internet usually adds 1/8..128/1
// routes and leaves the default route alone (Clash on this Mac does), so the
// default route still names the physical interface. If it names a tunnel,
// pinning to it would only make things worse, so it says so instead.
func DefaultInterface() (string, error) {
	name, err := netmon.DefaultRouteInterface()
	if err != nil {
		return "", err
	}
	if IsTunnel(name) {
		return "", fmt.Errorf("the default route goes through %s, a tunnel; not pinning", name)
	}
	return name, nil
}

// IsTunnel reports whether an interface name looks like a VPN or proxy
// tunnel rather than a physical link.
func IsTunnel(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"utun", "tun", "tap", "wg", "tailscale", "ipsec", "ppp", "gif", "stf", "wintun", "zt", "deneb"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
