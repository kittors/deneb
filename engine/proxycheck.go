package engine

import (
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/kittors/deneb/engine/bind"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// Another program's tunnel taking Deneb's traffic (DESIGN §6.8). In
// user-space mode the engine's own sockets are pinned to the physical
// interface, so Clash, Surge or v2rayN in TUN mode never carry them and
// there is nothing to say. In kernel mode ordinary programs reach the LAN
// devices through Deneb's interface, by a route the system picks; if a
// proxy's or VPN's tunnel wins that route, those programs' connections go
// into the proxy and fail. The engine then reports problem "proxy" with
// the rule to add in that program. It never changes the program itself.

const proxyEvery = 30 * time.Second

// routeSource is the address the system would send from to dst. A UDP
// socket "connected" there sends nothing; the kernel only picks a route.
var routeSource = func(dst netip.Addr) (netip.Addr, error) {
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(dst, 9)))
	if err != nil {
		return netip.Addr{}, err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap(), nil
}

// interfaceOf names the interface that has address a ("" if none does).
var interfaceOf = func(a netip.Addr) string {
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipn.IP); ok && ip.Unmap() == a {
					return ifc.Name
				}
			}
		}
	}
	return ""
}

// interfaceExists: kernel mode's interface is a real one (tests use a fake
// device that the system knows nothing about, so its routes cannot be
// looked at).
var interfaceExists = func(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

func (e *Engine) proxyLoop() {
	t := time.NewTicker(proxyEvery)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			e.checkProxy()
		}
	}
}

// checkProxy asks the system which interface it would send to the overlay
// through: the resolver's address and a peer's. Leaving from this device's
// own overlay address means Deneb's interface; leaving from another
// tunnel's address means that tunnel took the route. (Leaving from a
// physical interface would be an address clash with the local network,
// not a proxy, and is not reported here.)
func (e *Engine) checkProxy() {
	e.mu.Lock()
	nm, self, kern := e.nm, e.self, e.kern
	e.mu.Unlock()
	if kern == nil || nm == nil || !self.IsValid() || !interfaceExists(kern.Name) {
		return
	}
	var dsts []netip.Addr
	for _, d := range nm.Network.DNS {
		if d.Is4() && nm.Network.IPv4.Contains(d) {
			dsts = append(dsts, d)
			break
		}
	}
	for _, p := range nm.Peers {
		if a := firstV4(p.Addrs); a.IsValid() {
			dsts = append(dsts, a)
			break
		}
	}
	for _, d := range dsts {
		src, err := routeSource(d)
		if err != nil || src == self {
			continue
		}
		name := interfaceOf(src)
		if name == "" || name == kern.Name || !bind.IsTunnel(name) {
			continue
		}
		rule := "IP-CIDR," + nm.Network.IPv4.String() + ",DIRECT"
		msg := fmt.Sprintf("别的程序的网卡 %s 接走了去局域网设备（%s）的连接，像 Clash、Surge 的增强模式，或者别的 VPN。在它的规则里加上 %s，局域网里的设备就能连上。", name, d, rule)
		e.mu.Lock()
		prev, had := e.problems[apitype.ProblemProxy]
		e.problems[apitype.ProblemProxy] = apitype.Problem{Code: apitype.ProblemProxy, Message: msg, Rule: rule}
		e.mu.Unlock()
		if !had || prev.Message != msg {
			e.logf("[engine] problem %s: %s", apitype.ProblemProxy, msg)
			e.publishState()
		}
		return
	}
	e.mu.Lock()
	_, had := e.problems[apitype.ProblemProxy]
	delete(e.problems, apitype.ProblemProxy)
	e.mu.Unlock()
	if had {
		e.logf("[engine] connections to the LAN devices go through Deneb's interface again")
		e.publishState()
	}
}
