// Package front is the control server's entrances in front of it
// (design/plan/control-reachability-20261116.md): a front on another
// machine that passes TCP on unopened, saying whom from in a PROXY header
// (deneb-front), and a Cloudflare tunnel's cloudflared on the control
// server's own machine, saying it in CF-Connecting-IP.
//
// Through a front, TLS still ends at the control server: a device checks
// the control server's pin as on its own address, and the front sees
// addresses and amounts, never what is said. Through Cloudflare, TLS ends
// at Cloudflare, which sees it all (PRIVACY.md): devices go that way last.
package front

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// signature starts a PROXY protocol v2 header.
var signature = []byte("\r\n\r\n\x00\r\nQUIT\n")

// headerWait is how long a front has to send its header.
const headerWait = 5 * time.Second

// maxHeader bounds a v2 header's addresses and TLVs.
const maxHeader = 1024

// Header is a PROXY protocol v2 header for a connection from src to dst.
func Header(src, dst netip.AddrPort) []byte {
	src, dst = netip.AddrPortFrom(src.Addr().Unmap(), src.Port()), netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	b := append([]byte{}, signature...)
	if src.Addr().Is4() && dst.Addr().Is4() {
		b = append(b, 0x21, 0x11, 0, 12)
		s, d := src.Addr().As4(), dst.Addr().As4()
		b = append(append(b, s[:]...), d[:]...)
	} else {
		b = append(b, 0x21, 0x21, 0, 36)
		s, d := src.Addr().As16(), dst.Addr().As16()
		b = append(append(b, s[:]...), d[:]...)
	}
	b = binary.BigEndian.AppendUint16(b, src.Port())
	return binary.BigEndian.AppendUint16(b, dst.Port())
}

// ReadHeader reads a PROXY header, v2 or v1, from r: the address the
// connection is from, or, for a header that names none (LOCAL, UNKNOWN: a
// front's own health check), the zero AddrPort.
func ReadHeader(r *bufio.Reader) (netip.AddrPort, error) {
	start, err := r.Peek(len(signature))
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("no PROXY header: %w", err)
	}
	if !bytes.Equal(start, signature) {
		return readV1(r)
	}
	var h [16]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return netip.AddrPort{}, err
	}
	n := int(binary.BigEndian.Uint16(h[14:]))
	if h[12]>>4 != 2 || h[12]&0xf > 1 || n > maxHeader {
		return netip.AddrPort{}, errors.New("not a PROXY v2 header")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return netip.AddrPort{}, err
	}
	if h[12]&0xf == 0 { // LOCAL
		return netip.AddrPort{}, nil
	}
	switch h[13] {
	case 0x11: // TCP over IPv4
		if n < 12 {
			return netip.AddrPort{}, errors.New("short PROXY v2 header")
		}
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(body[:4])), binary.BigEndian.Uint16(body[8:])), nil
	case 0x21: // TCP over IPv6
		if n < 36 {
			return netip.AddrPort{}, errors.New("short PROXY v2 header")
		}
		a := netip.AddrFrom16([16]byte(body[:16])).Unmap()
		return netip.AddrPortFrom(a, binary.BigEndian.Uint16(body[32:])), nil
	case 0x00: // UNSPEC
		return netip.AddrPort{}, nil
	}
	return netip.AddrPort{}, fmt.Errorf("PROXY v2 header for family %#x", h[13])
}

// readV1 reads a PROXY v1 header: "PROXY TCP4 src dst sport dport\r\n".
func readV1(r *bufio.Reader) (netip.AddrPort, error) {
	var line []byte
	for len(line) < 108 {
		c, err := r.ReadByte()
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("no PROXY header: %w", err)
		}
		line = append(line, c)
		if c == '\n' {
			break
		}
	}
	s, ok := strings.CutSuffix(string(line), "\r\n")
	f := strings.Fields(s)
	if !ok || len(f) < 2 || f[0] != "PROXY" {
		return netip.AddrPort{}, errors.New("no PROXY header")
	}
	if f[1] == "UNKNOWN" {
		return netip.AddrPort{}, nil
	}
	if len(f) != 6 || f[1] != "TCP4" && f[1] != "TCP6" {
		return netip.AddrPort{}, errors.New("not a PROXY v1 header")
	}
	a, err := netip.ParseAddr(f[2])
	p, err2 := strconv.ParseUint(f[4], 10, 16)
	if err != nil || err2 != nil {
		return netip.AddrPort{}, errors.New("bad PROXY v1 header")
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(p)), nil
}

// Listener wraps ln, where fronts connect. A connection from one of from
// starts with a PROXY header, and its RemoteAddr is the address the
// header names, read when it is first asked for, or the connection first
// read: by then Accept has returned, so a slow front holds up no other. A
// connection from anywhere else is closed at once: no one else can say
// whom they are from.
func Listener(ln net.Listener, from []netip.Prefix, logf func(string, ...any)) net.Listener {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &listener{Listener: ln, from: from, logf: logf}
}

type listener struct {
	net.Listener
	from []netip.Prefix
	logf func(string, ...any)

	mu     sync.Mutex
	warned time.Time // when a stranger was last logged
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer, err := netip.ParseAddrPort(c.RemoteAddr().String())
		if err == nil && Trusted(l.from, peer.Addr()) {
			return &conn{Conn: c, br: bufio.NewReader(c)}, nil
		}
		c.Close()
		l.mu.Lock()
		if time.Since(l.warned) > time.Minute {
			l.warned = time.Now()
			l.logf("front: closed a connection from %s, not a front (-front-from)", c.RemoteAddr())
		}
		l.mu.Unlock()
	}
}

// Trusted reports whether a is in one of the prefixes.
func Trusted(from []netip.Prefix, a netip.Addr) bool {
	a = a.Unmap()
	return slices.ContainsFunc(from, func(p netip.Prefix) bool { return p.Contains(a) })
}

// conn is a front's connection: its header is read once, at first need.
type conn struct {
	net.Conn
	br     *bufio.Reader
	once   sync.Once
	remote net.Addr
	err    error
}

func (c *conn) init() {
	c.once.Do(func() {
		c.Conn.SetReadDeadline(time.Now().Add(headerWait))
		from, err := ReadHeader(c.br)
		c.Conn.SetReadDeadline(time.Time{})
		switch {
		case err != nil:
			c.err = fmt.Errorf("front %s: %w", c.Conn.RemoteAddr(), err)
			c.Conn.Close()
		case from.IsValid():
			c.remote = net.TCPAddrFromAddrPort(from)
		}
	})
}

func (c *conn) Read(b []byte) (int, error) {
	if c.init(); c.err != nil {
		return 0, c.err
	}
	return c.br.Read(b)
}

func (c *conn) RemoteAddr() net.Addr {
	if c.init(); c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

// TunnelHandler serves requests from a Cloudflare tunnel's cloudflared on
// this machine: each one's address is its CF-Connecting-IP, which
// Cloudflare sets whatever the device sent. One from elsewhere, or with
// none, is refused, and an X-Real-IP that came through counts for
// nothing.
func TunnelHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		from, err2 := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
		if err != nil || !peer.Addr().IsLoopback() || err2 != nil {
			http.Error(w, "this entrance is for the Cloudflare tunnel only", http.StatusForbidden)
			return
		}
		r = r.WithContext(r.Context())
		r.Header.Del("X-Real-IP")
		r.RemoteAddr = netip.AddrPortFrom(from.Unmap(), 0).String()
		next.ServeHTTP(w, r)
	})
}
