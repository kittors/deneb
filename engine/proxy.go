package engine

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// dialPeer opens a TCP connection to a peer from this device's address:
// through the user-space stack, or the OS in kernel mode.
func (e *Engine) dialPeer(ctx context.Context, to netip.AddrPort) (net.Conn, error) {
	e.mu.Lock()
	s, self := e.stack, e.self
	e.mu.Unlock()
	if s != nil {
		return s.DialTCP(ctx, to)
	}
	if !self.IsValid() {
		return nil, errNotLoggedIn
	}
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: self.AsSlice()}}
	return d.DialContext(ctx, "tcp4", to.String())
}

// serveSOCKS is the user-space mode's door for ordinary programs on this
// machine (curl --socks5-hostname). Names are resolved in the engine, so
// neither the system resolver nor a proxy's fake-ip DNS sees them.
func (e *Engine) serveSOCKS(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		e.problem("socks", fmt.Sprintf("SOCKS5 %s 开不起来：%v", addr, err))
		return
	}
	e.logf("[socks] SOCKS5 for local programs on %s", addr)
	go func() {
		<-e.ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go e.handleSOCKS(c)
	}
}

func (e *Engine) handleSOCKS(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
		return
	}
	c.Write([]byte{5, 0}) // no authentication: loopback only
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[1] != 1 {
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0}) // CONNECT only
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = netip.AddrFrom4([4]byte(b)).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb)
	ent, err := e.resolve(host)
	if err != nil {
		c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0}) // host unreachable
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
	up, err := e.dialPeer(ctx, netip.AddrPortFrom(ent.Addr, port))
	cancel()
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer up.Close()
	c.SetDeadline(time.Time{})
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	pipe(c, up)
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

type portmap struct {
	ln   net.Listener
	port uint16
}

// Open gives the system an address for one service of a peer: its name in
// kernel mode, or a local port mapping in user-space mode, which lives as
// long as the engine.
func (e *Engine) Open(ctx context.Context, req apitype.OpenRequest) (*apitype.OpenResult, error) {
	if req.Port == 0 {
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "要打开哪个端口？"}
	}
	if req.Proto != "" && req.Proto != "tcp" {
		return nil, &apitype.Error{Code: apitype.ErrInvalid, Message: "现在只能打开 TCP 服务。"}
	}
	ent, err := e.resolve(string(req.Device))
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stack == nil {
		return &apitype.OpenResult{Host: ent.FQDN, Port: req.Port, Addr: net.JoinHostPort(ent.FQDN, strconv.Itoa(int(req.Port)))}, nil
	}
	k := fmt.Sprintf("%s|%d", ent.Device, req.Port)
	if pm := e.portmaps[k]; pm != nil {
		return &apitype.OpenResult{Host: "127.0.0.1", Port: pm.port, Addr: fmt.Sprintf("127.0.0.1:%d", pm.port)}, nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	pm := &portmap{ln: ln, port: uint16(ln.Addr().(*net.TCPAddr).Port)}
	e.portmaps[k] = pm
	target := netip.AddrPortFrom(ent.Addr, req.Port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
				up, err := e.dialPeer(ctx, target)
				cancel()
				if err != nil {
					return
				}
				defer up.Close()
				pipe(c, up)
			}()
		}
	}()
	e.logf("[open] %s:%d mapped to 127.0.0.1:%d", ent.FQDN, req.Port, pm.port)
	return &apitype.OpenResult{Host: "127.0.0.1", Port: pm.port, Addr: fmt.Sprintf("127.0.0.1:%d", pm.port)}, nil
}

// commonServices are what "开放给大家的" offers, by the kinds people know.
var commonServices = []struct {
	kind proto.ServiceKind
	port uint16
}{
	{proto.ServiceScreen, 3389}, {proto.ServiceScreen, 5900},
	{proto.ServiceFiles, 445}, {proto.ServiceFiles, 548},
	{proto.ServiceShell, 22},
	{proto.ServiceWeb, 80}, {proto.ServiceWeb, 443}, {proto.ServiceWeb, 3000}, {proto.ServiceWeb, 5000},
	{proto.ServiceWeb, 8000}, {proto.ServiceWeb, 8080}, {proto.ServiceWeb, 8443},
}

// Services finds which common services listen on this machine. A port
// answering is not enough: macOS's AirPlay Receiver (on by default) holds
// 5000 and 7000 and answers HTTP, so each kind is told by how it speaks.
func (e *Engine) Services(ctx context.Context) ([]apitype.LocalService, error) {
	var wg sync.WaitGroup
	found := make([]bool, len(commonServices))
	for i, s := range commonServices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = probeService(net.JoinHostPort("127.0.0.1", strconv.Itoa(int(s.port))), s.kind, s.port)
		}()
	}
	wg.Wait()
	out := []apitype.LocalService{}
	for i, s := range commonServices {
		if found[i] {
			out = append(out, apitype.LocalService{Kind: string(s.kind), Proto: "tcp", Port: s.port})
		}
	}
	return out, nil
}

// probeService reports whether a service of this kind listens at addr.
func probeService(addr string, kind proto.ServiceKind, port uint16) bool {
	const timeout = 400 * time.Millisecond
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	switch {
	case kind == proto.ServiceShell:
		return bannerHas(c, "SSH-") // sshd speaks first
	case kind == proto.ServiceScreen && port == 5900:
		return bannerHas(c, "RFB ") // VNC, macOS screen sharing
	case kind == proto.ServiceWeb && (port == 443 || port == 8443):
		tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}) // only asking "is it TLS?"
		return tc.Handshake() == nil
	case kind == proto.ServiceWeb:
		fmt.Fprintf(c, "HEAD / HTTP/1.0\r\nHost: localhost\r\n\r\n")
		head, _ := io.ReadAll(io.LimitReader(c, 4096))
		h := string(head)
		// AirPlay answers "Server: AirTunes/..." with Apple headers.
		return strings.HasPrefix(h, "HTTP/") && !strings.Contains(h, "AirTunes") && !strings.Contains(h, "X-Apple-")
	}
	return true // RDP, SMB and AFP wait for the client to speak first
}

func bannerHas(c net.Conn, prefix string) bool {
	buf := make([]byte, len(prefix))
	_, err := io.ReadFull(c, buf)
	return err == nil && string(buf) == prefix
}
