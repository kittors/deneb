package main

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// serveSOCKS is the user-space mode's door for ordinary apps on this machine
// (curl --socks5-hostname ...). Names are resolved inside Deneb, so neither
// the system resolver nor a proxy's fake-ip DNS ever sees them.
func (n *node) serveSOCKS(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("socks: %v", err)
		return
	}
	n.events.add("socks", "", "SOCKS5 for local apps on "+addr)
	acceptLoop(ln, n.handleSOCKS)
}

func (n *node) handleSOCKS(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
		return
	}
	c.Write([]byte{5, 0}) // no authentication: the listener is on loopback only
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[1] != 1 {
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0}) // only CONNECT
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
	ip, _, err := n.resolve(host)
	if err != nil {
		log.Printf("[socks] %s: %v", host, err)
		c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0}) // host unreachable
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	up, err := n.onet.DialTCP(ctx, netip.AddrPortFrom(ip, port))
	if err != nil {
		log.Printf("[socks] dial %s:%d (%s): %v", host, port, ip, err)
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer up.Close()
	c.SetDeadline(time.Time{})
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	log.Printf("[socks] %s -> %s", host, net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}
