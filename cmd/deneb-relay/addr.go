package main

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Where devices reach the relay (design/plan/m7/relay-install.md §5.2).
// A public address on an interface (most VPSes) is what the relay reports;
// with none there (a cloud's 1:1 NAT, a home router, a carrier's NAT), it
// reports nothing, and the control server takes the address its requests
// come from, which the probe from outside then confirms or not.

var (
	cgnat     = netip.MustParsePrefix("100.64.0.0/10")
	benchmark = netip.MustParsePrefix("198.18.0.0/15")
	reserved  = netip.MustParsePrefix("240.0.0.0/4")
	thisNet   = netip.MustParsePrefix("0.0.0.0/8")
	ietf      = netip.MustParsePrefix("192.0.0.0/24")
	sixToFour = netip.MustParsePrefix("2002::/16")
	teredo    = netip.MustParsePrefix("2001::/32")
)

// publicAddr: a is an address the Internet routes to this machine.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	if a.Is4() {
		return !cgnat.Contains(a) && !benchmark.Contains(a) && !reserved.Contains(a) && !thisNet.Contains(a) && !ietf.Contains(a)
	}
	// Tunnels through someone else's relays are no place for one.
	return !sixToFour.Contains(a) && !teredo.Contains(a)
}

// tunnelIface: an interface of a tunnel or of containers, whose addresses
// are not where devices reach this machine.
func tunnelIface(name string) bool {
	for _, p := range []string{"tailscale", "wg", "tun", "tap", "utun", "zt", "docker", "br-", "veth", "virbr", "lxc", "cni", "flannel", "deneb", "podman", "kube"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ifaceAddr is an address on an interface, as publicAddrs weighs it.
type ifaceAddr struct {
	iface string
	addr  netip.Addr
	// stable: not an IPv6 privacy address, which changes every day or
	// so, nor one being given up.
	stable bool
}

// pickPublic picks the public IPv4 and IPv6 address the relay reports:
// stable before temporary, the default route's interface first.
func pickPublic(addrs []ifaceAddr, defaultIface string) (v4, v6 netip.Addr) {
	score := func(a ifaceAddr) int {
		s := 0
		if a.stable {
			s += 2
		}
		if a.iface == defaultIface {
			s++
		}
		return s
	}
	best4, best6 := -1, -1
	for _, a := range addrs {
		if !publicAddr(a.addr) || tunnelIface(a.iface) {
			continue
		}
		s := score(a)
		if a.addr.Unmap().Is4() {
			if s > best4 {
				v4, best4 = a.addr.Unmap(), s
			}
		} else if s > best6 {
			v6, best6 = a.addr, s
		}
	}
	return v4, v6
}

// ifaceAddrs are the addresses on this machine's interfaces that are up,
// loopback left out.
func ifaceAddrs() []ifaceAddr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	flags := ipv6Flags()
	var all []ifaceAddr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			p, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := p.Addr()
			all = append(all, ifaceAddr{iface: ifc.Name, addr: ip, stable: !ip.Is6() || flags[ip]&unstableIPv6 == 0})
		}
	}
	return all
}

// publicAddrs are this machine's public addresses, from its interfaces.
func publicAddrs() (v4, v6 netip.Addr) {
	return pickPublic(ifaceAddrs(), defaultRouteIface())
}

// findAddrs are the public addresses, and every address on the
// interfaces (RelayInfo.Local: what tells the control server whether the
// machine is behind a NAT), tunnels' and containers' and link-local ones
// left out.
func findAddrs() (v4, v6 netip.Addr, local []netip.Addr) {
	all := ifaceAddrs()
	v4, v6 = pickPublic(all, defaultRouteIface())
	return v4, v6, localOf(all)
}

func localOf(all []ifaceAddr) []netip.Addr {
	var local []netip.Addr
	for _, a := range all {
		ip := a.addr.Unmap()
		if tunnelIface(a.iface) || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() || slices.Contains(local, ip) {
			continue
		}
		local = append(local, ip)
	}
	slices.SortFunc(local, func(a, b netip.Addr) int { return a.Compare(b) })
	return local
}

// The IPv6 address flags of /proc/net/if_inet6 that make an address no
// good to report: a privacy address (IFA_F_TEMPORARY), one failing or
// still checking for duplicates, one being given up.
const unstableIPv6 = 0x01 | 0x08 | 0x20 | 0x40

// addrsLine says what publicAddrs found, for install and status.
func addrsLine(v4, v6 netip.Addr) string {
	var p []string
	if v4.IsValid() {
		p = append(p, "IPv4 "+v4.String())
	}
	if v6.IsValid() {
		p = append(p, "IPv6 "+v6.String())
	}
	return strings.Join(p, "，")
}

// watchAddrs looks at the relay's addresses every so often and, when they
// changed (a home line's address does now and then), puts them in the
// info its next sync carries, at once. find gives the addresses now;
// fixed ones (-ipv4, -ipv6) it leaves alone.
func (r *relay) watchAddrs(ctx context.Context, every time.Duration, fixed4, fixed6 bool, find func() (netip.Addr, netip.Addr, []netip.Addr)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v4, v6, local := find()
		r.mu.Lock()
		next := r.reported
		if !fixed4 {
			next.IPv4 = v4
		}
		if !fixed6 {
			next.IPv6 = v6
		}
		next.Local = local
		changed := next.IPv4 != r.reported.IPv4 || next.IPv6 != r.reported.IPv6 || !slices.Equal(next.Local, r.reported.Local)
		if changed {
			r.logf("address changed: %s → %s", orNone(addrsLine(r.reported.IPv4, r.reported.IPv6)), orNone(addrsLine(next.IPv4, next.IPv6)))
			r.reported = next
			info := next
			r.info = &info
		}
		r.mu.Unlock()
		if changed {
			r.nudge()
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "none on the interfaces"
	}
	return s
}
