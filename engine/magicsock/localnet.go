package magicsock

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"syscall"
	"time"
)

// The local network: devices on the same LAN reach each other there, not
// through the router's public address.
//
// On 11-09 both test Macs' engines found every peer on their LAN only
// through the home's public address: a hairpin through the router's NAT
// that held 5-20 s at a time, then the relay. Their pings to 192.168.1.x
// never left: macOS's local network privacy refuses them with "no route to
// host" to a program without the permission (an unbundled command-line
// engine, started over SSH or from a session long gone), while public
// addresses go out as usual. The engine dropped the error, and nothing
// showed why. Now the refusal is noticed (noteSend), logged once and
// reported (Options.OnLocalNetwork), and a local address that answers is
// preferred to a public one (betterAddr).

// isLAN: an address on a local network (private IPv4, IPv6 ULA, or
// link-local), as opposed to a public one. The CGNAT block (Tailscale's,
// a mobile carrier's) is neither.
func isLAN(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLinkLocalUnicast()
}

// betterAddr: src, which just answered in rtt, should replace best (rtt
// bestRTT) as the direct path. A local address replaces a public one unless
// it is much slower: on the same LAN the path stays off the router's NAT and
// the Internet, and one Wi-Fi sample says little about which is faster. A
// public address never replaces a local one that answers. Otherwise only a
// clearly faster one does, so the path does not flap on jitter.
func betterAddr(src netip.AddrPort, rtt time.Duration, best netip.AddrPort, bestRTT time.Duration) bool {
	srcLAN, bestLAN := isLAN(src.Addr()), isLAN(best.Addr())
	switch {
	case srcLAN && !bestLAN:
		return rtt <= 2*bestRTT+5*time.Millisecond
	case bestLAN && !srcLAN:
		return false
	}
	return rtt < bestRTT*8/10
}

// testLocal is RefuseLocalForTest's setting.
type testLocal struct {
	pfx    netip.Prefix // counts as the local network
	refuse bool         // and sends there fail as macOS fails them
}

// RefuseLocalForTest makes the addresses in pfx count as the local network
// (the in-process tests' peers are on loopback), and, while refuse is set,
// sends to them fail the way macOS fails them for a program without local
// network permission.
func (c *Conn) RefuseLocalForTest(pfx netip.Prefix, refuse bool) {
	c.testLocal.Store(&testLocal{pfx: pfx, refuse: refuse})
}

func (c *Conn) isLocal(a netip.Addr) bool {
	if tl := c.testLocal.Load(); tl != nil && tl.pfx.Contains(a) {
		return true
	}
	return isLAN(a)
}

// writeTo sends b to the peer at to, noting a refusal for the local network.
func (c *Conn) writeTo(p *peerState, b []byte, to netip.AddrPort) error {
	if c.opts.RelayOnly {
		return nil // nothing goes direct; the socket is on the loopback address
	}
	var err error
	if tl := c.testLocal.Load(); tl != nil && tl.refuse && tl.pfx.Contains(to.Addr()) {
		err = &net.OpError{Op: "write", Net: "udp4", Err: syscall.EHOSTUNREACH}
	} else {
		_, err = c.sock().WriteToUDPAddrPort(b, to)
	}
	if err != nil {
		c.noteSendErr(p, to, err)
	}
	return err
}

// noteSendErr: a send to a local-network address failed with "no route to
// host". On macOS that is the local network privacy, whatever the route
// says; the first one is logged and reported, until a device on the local
// network answers (noteLocalAnswer). Other failures say nothing about it.
func (c *Conn) noteSendErr(p *peerState, to netip.AddrPort, err error) {
	if !errors.Is(err, syscall.EHOSTUNREACH) || !c.isLocal(to.Addr()) ||
		runtime.GOOS != "darwin" && c.testLocal.Load() == nil {
		return
	}
	c.mu.Lock()
	p.localRefused = time.Now()
	c.mu.Unlock()
	if c.localRefused.Swap(true) {
		return
	}
	c.logf("[path] the system refuses packets to the local network (%s: %v): devices on this LAN are reached the long way. On macOS, allow Deneb under System Settings > Privacy & Security > Local Network", to, err)
	if c.opts.OnLocalNetwork != nil {
		go c.opts.OnLocalNetwork(true)
	}
}

// noteLocalAnswer: a pong came from src. From a local-network address, it
// answered our ping: packets to the local network go out (again).
func (c *Conn) noteLocalAnswer(src netip.AddrPort) {
	if !c.isLocal(src.Addr()) || !c.localRefused.Swap(false) {
		return
	}
	c.logf("[path] packets to the local network go out again (%s answered)", src)
	if c.opts.OnLocalNetwork != nil {
		go c.opts.OnLocalNetwork(false)
	}
}

// LocalRefused reports whether the system refuses packets to the local
// network (see noteSendErr).
func (c *Conn) LocalRefused() bool { return c.localRefused.Load() }
