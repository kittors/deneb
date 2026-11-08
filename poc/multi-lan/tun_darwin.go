package main

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

// Ranges another tool on a typical Chinese user's Mac already depends on.
// Putting Deneb's interface address in 100.64/10 makes the Tailscale app see
// "another CGNAT interface" and switch from one /10 route to per-peer /32
// routes, reconfiguring its network extension (tailscale ipn/ipnlocal
// shouldUseOneCGNATRoute + netmon HasCGNATInterface). 198.18/15 is where
// Clash and Surge keep their fake IPs and TUN address.
var reservedBlocks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
}

// preflightKernelTUN checks, without changing anything, that the block is
// unused on this machine: no interface address inside it and no route more
// specific than the catch-all ones (default route, a proxy's /1../8 routes).
func preflightKernelTUN(block netip.Prefix) error {
	if !block.Addr().Is4() || block.Bits() < 16 {
		return fmt.Errorf("block %s: need an IPv4 /16 or longer", block)
	}
	for _, r := range reservedBlocks {
		if r.Overlaps(block) {
			return fmt.Errorf("block %s overlaps %s, which other software on this Mac relies on", block, r)
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && block.Contains(p.Addr()) {
			return fmt.Errorf("an interface already has %s inside %s", p.Addr(), block)
		}
	}
	first := block.Masked().Addr().Next()
	last := lastAddr(block)
	for _, probe := range []netip.Addr{first, last} {
		bits, iface, err := routeFor(probe)
		if err != nil {
			return err
		}
		if bits >= block.Bits() {
			return fmt.Errorf("an existing /%d route via %s already covers %s", bits, iface, probe)
		}
		log.Printf("[tun] preflight: %s is currently covered only by a /%d route via %s", probe, bits, iface)
	}
	return nil
}

// setupKernelTUN creates one utun, gives it this device's address and adds
// one route for the whole block. Nothing else on the system is touched; the
// returned cleanup removes the route, and closing the utun removes it.
func setupKernelTUN(self netip.Addr, block netip.Prefix, mtu int) (tun.Device, func(), error) {
	if os.Geteuid() != 0 {
		return nil, nil, fmt.Errorf("-tun needs root (creating a utun and a route)")
	}
	if err := preflightKernelTUN(block); err != nil {
		return nil, nil, err
	}
	dev, err := tun.CreateTUN("utun", mtu)
	if err != nil {
		return nil, nil, err
	}
	name, _ := dev.Name()
	cleanup := func() {
		run("route", "-q", "-n", "delete", "-inet", block.String(), "-interface", name)
		dev.Close()
		log.Printf("[tun] removed route %s and interface %s", block, name)
	}
	if err := run("ifconfig", name, "inet", self.String(), self.String(), "netmask", "255.255.255.255", "up"); err != nil {
		dev.Close()
		return nil, nil, err
	}
	if err := run("route", "-q", "-n", "add", "-inet", block.String(), "-interface", name); err != nil {
		dev.Close()
		return nil, nil, err
	}
	if _, iface, err := routeFor(lastAddr(block)); err != nil || iface != name {
		cleanup()
		return nil, nil, fmt.Errorf("route check failed: %s goes via %q (%v)", lastAddr(block), iface, err)
	}
	log.Printf("[tun] %s up: %s, route %s -> %s", name, self, block, name)
	return dev, cleanup, nil
}

func run(name string, args ...string) error {
	log.Printf("[tun] exec: %s %s", name, strings.Join(args, " "))
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// routeFor asks the kernel which route it would use for ip (read-only).
func routeFor(ip netip.Addr) (bits int, iface string, err error) {
	out, err := exec.Command("route", "-n", "get", ip.String()).CombinedOutput()
	if err != nil {
		return 0, "", fmt.Errorf("route get %s: %v", ip, err)
	}
	bits = 32 // host route unless a mask is printed
	for _, line := range strings.Split(string(out), "\n") {
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
				b := m.As4()
				bits = 0
				for _, x := range b {
					for ; x&0x80 != 0; x <<= 1 {
						bits++
					}
				}
			}
		case "interface":
			iface = v
		}
	}
	return bits, iface, nil
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := (uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])) | host
	v-- // skip the broadcast-looking address
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
