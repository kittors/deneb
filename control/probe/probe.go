// Package probe checks a relay from outside, as a device would reach it
// (M7, design/plan/m7/relay-install-api.md §5): a TLS handshake with its
// DERP port, which must show its certificate, and a STUN request to its
// STUN port. The control server checks from where it is, and the watchdog
// on another machine from there (cmd/deneb-watchdog).
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
)

// Timeout bounds one probe of one address, TCP and UDP together.
const Timeout = 6 * time.Second

// Relay checks one address of a relay: its TCP and UDP results, From left
// for the caller to fill.
func Relay(ctx context.Context, t proto.ProbeTarget) proto.RelayProbe {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	p := proto.RelayProbe{Family: t.Family, Addr: t.Addr, UDP: proto.RelayProbeNone}
	var wg sync.WaitGroup
	if t.STUNPort != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.UDP = STUN(ctx, netip.AddrPortFrom(t.Addr, t.STUNPort))
		}()
	}
	p.TCP = TLS(ctx, netip.AddrPortFrom(t.Addr, t.DERPPort), t.Pin, t.HostName)
	wg.Wait()
	return p
}

// TLS makes a TLS handshake with addr and says how it went
// (proto.RelayProbe*): ok only if the certificate's key is pin.
func TLS(ctx context.Context, addr netip.AddrPort, pin, host string) string {
	cfg, err := proto.PinnedTLSConfig(pin)
	if err != nil {
		return proto.RelayProbeError
	}
	// The pin's check, noting whether it was the one that failed.
	var wrongPin bool
	verify := cfg.VerifyConnection
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if err := verify(cs); err != nil {
			wrongPin = true
			return err
		}
		return nil
	}
	// SNI only for a name: an address sends none.
	if _, err := netip.ParseAddr(host); host != "" && err != nil {
		cfg.ServerName = host
	}
	// The relay's own TLS is 1.3, but anything answering the port is to be
	// told apart from silence.
	cfg.MinVersion = tls.VersionTLS12
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return dialResult(err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		switch {
		case wrongPin:
			return proto.RelayProbePin
		case isTimeout(err):
			return proto.RelayProbeTimeout
		}
		return proto.RelayProbeTLS
	}
	return proto.RelayProbeOK
}

// STUN sends a STUN binding request to addr, three times at most, and says
// whether an answer to it came (proto.RelayProbe*).
func STUN(ctx context.Context, addr netip.AddrPort) string {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr.String())
	if err != nil {
		return dialResult(err)
	}
	defer conn.Close()
	buf := make([]byte, 1500)
	for range 3 {
		tx := stun.NewTxID()
		if _, err := conn.Write(stun.Request(tx)); err != nil {
			return dialResult(err)
		}
		deadline := time.Now().Add(1500 * time.Millisecond)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		conn.SetReadDeadline(deadline)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				// An ICMP port unreachable comes back as a refused read on a
				// connected UDP socket: nothing listens.
				if errors.Is(err, syscall.ECONNREFUSED) {
					return proto.RelayProbeRefused
				}
				break // timed out: ask again
			}
			if got, _, err := stun.ParseResponse(buf[:n]); err == nil && got == tx {
				return proto.RelayProbeOK
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return proto.RelayProbeTimeout
}

// dialResult is what a failed dial says of the address.
func dialResult(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return proto.RelayProbeRefused
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EADDRNOTAVAIL), errors.Is(err, syscall.EAFNOSUPPORT):
		// This side has no route to that family: nothing about the relay.
		return proto.RelayProbeNoRoute
	case errors.Is(err, syscall.EHOSTUNREACH):
		return proto.RelayProbeUnreachable
	case isTimeout(err):
		return proto.RelayProbeTimeout
	}
	return proto.RelayProbeError
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}

// Targets are the addresses of rl to check, one a family, with its ports
// and pin.
func Targets(rl *proto.Relay) []proto.ProbeTarget {
	var out []proto.ProbeTarget
	for _, a := range []netip.Addr{rl.IPv4, rl.IPv6} {
		if !a.IsValid() {
			continue
		}
		out = append(out, proto.ProbeTarget{Family: Family(a), Addr: a, DERPPort: rl.DERPPort, STUNPort: rl.STUNPort, Pin: rl.Pin, HostName: rl.HostName})
	}
	return out
}

// Family is "ipv4" or "ipv6".
func Family(a netip.Addr) string {
	if a.Unmap().Is4() {
		return "ipv4"
	}
	return "ipv6"
}

// Public reports whether a is an address on the Internet: not loopback,
// link-local, private (RFC 1918, fc00::/7), a carrier's NAT (100.64/10),
// multicast or unspecified.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Describe is a probe's target as the log says it.
func Describe(t proto.ProbeTarget) string {
	return net.JoinHostPort(t.Addr.String(), strconv.Itoa(int(t.DERPPort))) + "/tcp " +
		net.JoinHostPort(t.Addr.String(), strconv.Itoa(int(t.STUNPort))) + "/udp"
}
