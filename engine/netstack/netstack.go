// Package netstack is the engine's user-space network stack (gVisor): the
// "interface" of a device that has no kernel interface, such as a server in
// a container, a machine without administrator rights, or a computer whose
// address range clashes (plan §2.3).
//
// It does what a kernel interface would do for the device:
//
//   - the engine's own sockets (SOCKS5, port mappings, test services) dial
//     and listen on the overlay address through it;
//   - connections peers open to this device are forwarded to the same port
//     on 127.0.0.1, where the device's real services listen. The filter in
//     front has already decided which ports a peer may reach.
//
// The tun.Device part follows wireguard-go's tun/netstack (MIT, Copyright
// WireGuard LLC); the stack is kept here so the forwarders can be installed.
package netstack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// Stack is a gVisor stack with one NIC holding the device's address.
type Stack struct {
	ep       *channel.Endpoint
	stack    *stack.Stack
	events   chan tun.Event
	notify   *channel.NotificationHandle
	incoming chan *buffer.View
	mtu      int
	self     netip.Addr
	logf     func(format string, args ...any)

	closeOnce sync.Once
	done      chan struct{}
}

// New makes a stack that owns addrs (M1: the device's IPv4 address).
func New(addrs []netip.Addr, mtu int, logf func(format string, args ...any)) (*Stack, error) {
	if logf == nil {
		logf = log.Printf
	}
	opts := stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6},
		HandleLocal:        true,
	}
	if runtime.GOOS == "windows" {
		// Windows' monotonic clock moves in 500 µs steps; told so, TCP does
		// not take the jumps for losses (as Tailscale does).
		opts.ClockResolution = 500 * time.Microsecond
	}
	s := &Stack{
		ep:       channel.New(1024, uint32(mtu), ""),
		stack:    stack.New(opts),
		events:   make(chan tun.Event, 10),
		incoming: make(chan *buffer.View, 256),
		mtu:      mtu,
		logf:     logf,
		done:     make(chan struct{}),
	}
	sack := tcpip.TCPSACKEnabled(true) // off by default in gVisor
	if err := s.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &sack); err != nil {
		return nil, fmt.Errorf("enable TCP SACK: %v", err)
	}
	// RACK off, RFC 6675 SACK recovery instead, as Tailscale does: with
	// gVisor's RACK, after a burst of losses a sender can crawl at about two
	// segments per RTO for seconds to tens of seconds
	// (tailscale/tailscale#21946, google/gvisor#16670). Here: a long
	// connection that never recovered after a lossy stretch, its sender idle
	// with data to send (11-08, M2's bad line test).
	recovery := tcpip.TCPRecovery(0)
	if err := s.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &recovery); err != nil {
		return nil, fmt.Errorf("set TCP loss recovery: %v", err)
	}
	cubic := tcpip.CongestionControlOption("cubic") // as on Linux, and in Tailscale's netstack
	if err := s.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &cubic); err != nil {
		return nil, fmt.Errorf("set TCP congestion control: %v", err)
	}
	s.notify = s.ep.AddNotify(s)
	if err := s.stack.CreateNIC(1, s.ep); err != nil {
		return nil, fmt.Errorf("create NIC: %v", err)
	}
	var has4, has6 bool
	for _, ip := range addrs {
		pn := ipv4.ProtocolNumber
		if ip.Is6() {
			pn = ipv6.ProtocolNumber
			has6 = true
		} else {
			has4 = true
			if !s.self.IsValid() {
				s.self = ip
			}
		}
		pa := tcpip.ProtocolAddress{Protocol: pn, AddressWithPrefix: tcpip.AddrFromSlice(ip.AsSlice()).WithPrefix()}
		if err := s.stack.AddProtocolAddress(1, pa, stack.AddressProperties{}); err != nil {
			return nil, fmt.Errorf("add address %v: %v", ip, err)
		}
	}
	if has4 {
		s.stack.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: 1})
	}
	if has6 {
		s.stack.AddRoute(tcpip.Route{Destination: header.IPv6EmptySubnet, NIC: 1})
	}
	s.events <- tun.EventUp
	return s, nil
}

// Self is the stack's IPv4 address.
func (s *Stack) Self() netip.Addr { return s.self }

func (s *Stack) Name() (string, error)    { return "deneb-userspace", nil }
func (s *Stack) File() *os.File           { return nil }
func (s *Stack) Events() <-chan tun.Event { return s.events }
func (s *Stack) MTU() (int, error)        { return s.mtu, nil }
func (s *Stack) BatchSize() int           { return 16 }

// Read hands packets the stack sends to the filter (and on to WireGuard).
func (s *Stack) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	var view *buffer.View
	select {
	case view = <-s.incoming:
	case <-s.done:
		return 0, os.ErrClosed
	}
	n := 0
	for {
		m, err := view.Read(bufs[n][offset:])
		view.Release()
		if err != nil {
			return n, err
		}
		sizes[n] = m
		n++
		if n == len(bufs) {
			return n, nil
		}
		select {
		case view = <-s.incoming:
		default:
			return n, nil
		}
	}
}

// Write injects packets from WireGuard (already filtered) into the stack.
func (s *Stack) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		pkt := b[offset:]
		if len(pkt) == 0 {
			continue
		}
		pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(pkt)})
		switch pkt[0] >> 4 {
		case 4:
			s.ep.InjectInbound(header.IPv4ProtocolNumber, pkb)
		case 6:
			s.ep.InjectInbound(header.IPv6ProtocolNumber, pkb)
		default:
			pkb.DecRef()
			return 0, syscall.EAFNOSUPPORT
		}
	}
	return len(bufs), nil
}

// WriteNotify is called by the channel endpoint when the stack has a packet
// to send.
func (s *Stack) WriteNotify() {
	pkt := s.ep.Read()
	if pkt == nil {
		return
	}
	view := pkt.ToView()
	pkt.DecRef()
	select {
	case s.incoming <- view:
	case <-s.done:
		view.Release()
	}
}

func (s *Stack) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.ep.RemoveNotify(s.notify)
		s.stack.RemoveNIC(1)
		s.stack.Close()
		s.ep.Close()
		close(s.events)
	})
	return nil
}

func fullAddr(ap netip.AddrPort) (tcpip.FullAddress, tcpip.NetworkProtocolNumber) {
	pn := ipv4.ProtocolNumber
	if ap.Addr().Is6() {
		pn = ipv6.ProtocolNumber
	}
	return tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFromSlice(ap.Addr().AsSlice()), Port: ap.Port()}, pn
}

// DialTCP opens a connection to a peer from the overlay address.
func (s *Stack) DialTCP(ctx context.Context, to netip.AddrPort) (net.Conn, error) {
	fa, pn := fullAddr(to)
	c, err := gonet.DialContextTCP(ctx, s.stack, fa, pn)
	if err != nil {
		return nil, err // not a nil *TCPConn in a non-nil interface
	}
	return c, nil
}

// ListenTCP listens on the overlay address. A listener takes precedence
// over the forwarder for its port.
func (s *Stack) ListenTCP(port uint16) (net.Listener, error) {
	fa, pn := fullAddr(netip.AddrPortFrom(s.self, port))
	ln, err := gonet.ListenTCP(s.stack, fa, pn)
	if err != nil {
		return nil, err
	}
	return ln, nil
}

// DialUDP opens a UDP socket from the overlay address to a peer.
func (s *Stack) DialUDP(to netip.AddrPort) (net.Conn, error) {
	lfa, pn := fullAddr(netip.AddrPortFrom(s.self, 0))
	rfa, _ := fullAddr(to)
	c, err := gonet.DialUDP(s.stack, &lfa, &rfa, pn)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListenUDP listens for UDP on the overlay address.
func (s *Stack) ListenUDP(port uint16) (net.PacketConn, error) {
	lfa, pn := fullAddr(netip.AddrPortFrom(s.self, port))
	c, err := gonet.DialUDP(s.stack, &lfa, nil, pn)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Forwarding is how peers' connections reach the device's own services.
type Forwarding struct {
	// Host is where services listen on this machine; "127.0.0.1" by default.
	Host string
	// DialTimeout bounds the connection to the local service.
	DialTimeout time.Duration
}

// Forward sends connections peers open to this device on to the same port
// of the host. A peer reaches only the ports the filter lets in.
func (s *Stack) Forward(fw Forwarding) {
	if fw.Host == "" {
		fw.Host = "127.0.0.1"
	}
	if fw.DialTimeout <= 0 {
		fw.DialTimeout = 5 * time.Second
	}
	tf := tcp.NewForwarder(s.stack, 0, 1024, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		target := net.JoinHostPort(fw.Host, strconv.Itoa(int(id.LocalPort)))
		// Dial the local service first: if nothing listens there, the peer
		// gets a reset, as from a closed port on a real interface.
		out, err := net.DialTimeout("tcp", target, fw.DialTimeout)
		if err != nil {
			r.Complete(true)
			return
		}
		var wq waiter.Queue
		ep, terr := r.CreateEndpoint(&wq)
		if terr != nil {
			r.Complete(true)
			out.Close()
			return
		}
		r.Complete(false)
		ep.SocketOptions().SetKeepAlive(true)
		in := gonet.NewTCPConn(&wq, ep)
		splice(in, out)
	})
	s.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, tf.HandlePacket)

	uf := udp.NewForwarder(s.stack, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		var wq waiter.Queue
		ep, terr := r.CreateEndpoint(&wq)
		if terr != nil {
			return false
		}
		in := gonet.NewUDPConn(&wq, ep)
		target := net.JoinHostPort(fw.Host, strconv.Itoa(int(id.LocalPort)))
		out, err := net.Dial("udp", target)
		if err != nil {
			in.Close()
			return true
		}
		go relayUDP(in, out)
		return true
	})
	s.stack.SetTransportProtocolHandler(udp.ProtocolNumber, uf.HandlePacket)
}

func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		// Half-close where possible so request/response protocols finish.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	// Give the other direction a moment to drain, then tear both down.
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	a.Close()
	b.Close()
}

// relayUDP shuttles datagrams between a peer's flow and the local service
// until neither side has spoken for two minutes.
func relayUDP(in *gonet.UDPConn, out net.Conn) {
	const idle = 2 * time.Minute
	defer in.Close()
	defer out.Close()
	errc := make(chan error, 2)
	go func() {
		buf := make([]byte, 65535)
		for {
			in.SetReadDeadline(time.Now().Add(idle))
			n, err := in.Read(buf)
			if err != nil {
				errc <- err
				return
			}
			out.Write(buf[:n])
		}
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			out.SetReadDeadline(time.Now().Add(idle))
			n, err := out.Read(buf)
			if err != nil {
				errc <- err
				return
			}
			in.Write(buf[:n])
		}
	}()
	<-errc
}

// ErrClosed is returned once the stack is closed.
var ErrClosed = errors.New("netstack closed")
