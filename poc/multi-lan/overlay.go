package main

import (
	"context"
	"net"
	"net/netip"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// overlayNet is how this node's own code (test services, SOCKS5, the control
// API) reaches the overlay. In user-space mode that is the gVisor stack; in
// kernel-TUN mode it is the ordinary OS stack bound to the overlay address,
// which is exactly what any other app on the machine would use.
type overlayNet interface {
	DialTCP(ctx context.Context, to netip.AddrPort) (net.Conn, error)
	ListenTCP(port int) (net.Listener, error)
	DialUDP(to netip.AddrPort) (net.Conn, error)
	ListenUDP(port int) (net.PacketConn, error)
}

type userspaceNet struct {
	tnet *netstack.Net
	self netip.Addr
}

func (u userspaceNet) DialTCP(ctx context.Context, to netip.AddrPort) (net.Conn, error) {
	return u.tnet.DialContextTCPAddrPort(ctx, to)
}

func (u userspaceNet) ListenTCP(port int) (net.Listener, error) {
	return u.tnet.ListenTCP(&net.TCPAddr{Port: port})
}

func (u userspaceNet) DialUDP(to netip.AddrPort) (net.Conn, error) {
	return u.tnet.DialUDPAddrPort(netip.AddrPortFrom(u.self, 0), to)
}

func (u userspaceNet) ListenUDP(port int) (net.PacketConn, error) {
	return u.tnet.ListenUDPAddrPort(netip.AddrPortFrom(u.self, uint16(port)))
}

// kernelNet binds every socket to the overlay address, so the OS routes it
// through Deneb's interface and the peer sees the right source address.
type kernelNet struct{ self netip.Addr }

func (k kernelNet) DialTCP(ctx context.Context, to netip.AddrPort) (net.Conn, error) {
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: k.self.AsSlice()}}
	return d.DialContext(ctx, "tcp4", to.String())
}

func (k kernelNet) ListenTCP(port int) (net.Listener, error) {
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: k.self.AsSlice(), Port: port})
}

func (k kernelNet) DialUDP(to netip.AddrPort) (net.Conn, error) {
	return net.DialUDP("udp4", &net.UDPAddr{IP: k.self.AsSlice()}, net.UDPAddrFromAddrPort(to))
}

func (k kernelNet) ListenUDP(port int) (net.PacketConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{IP: k.self.AsSlice(), Port: port})
}
