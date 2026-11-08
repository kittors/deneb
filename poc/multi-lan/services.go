package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// startServices runs three tiny services on the overlay address only. They
// stand in for "remote desktop / shared folder / website" in the product, and
// whether a peer may reach them is decided by the LAN filter.
func (n *node) startServices() {
	// :80 tells the caller who answered and which LANs the two devices share,
	// so isolation results are self-explanatory in curl output.
	if ln, err := n.onet.ListenTCP(80); err == nil {
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remote, _ := netip.ParseAddrPort(r.RemoteAddr)
			shared := "-"
			if pp := n.ft.pol.Load().peers[remote.Addr()]; pp != nil {
				shared = strings.Join(n.lanNames(pp.lans), ",")
			}
			fmt.Fprintf(w, "hello from %s.%s (%s); you are %s; LANs we share: %s\n", n.cfg.Name, n.cfg.Owner, n.self, remote.Addr(), shared)
		})}
		go srv.Serve(ln)
	} else {
		log.Printf("listen :80: %v", err)
	}
	// udp/7 echoes datagrams back; the "ping" measurements use it.
	if uc, err := n.onet.ListenUDP(7); err == nil {
		go func() {
			buf := make([]byte, 1500)
			for {
				nn, from, err := uc.ReadFrom(buf)
				if err != nil {
					return
				}
				uc.WriteTo(buf[:nn], from)
			}
		}()
	} else {
		log.Printf("listen udp/7: %v", err)
	}
	// :7000 streams a numbered line every 100 ms over one TCP connection, so a
	// client can measure how long the stream stalls during a path switch and
	// whether the connection survives it.
	if ln, err := n.onet.ListenTCP(7000); err == nil {
		go acceptLoop(ln, func(c net.Conn) {
			defer c.Close()
			for seq := 0; ; seq++ {
				if _, err := fmt.Fprintf(c, "%d %d\n", seq, time.Now().UnixMilli()); err != nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
	// :9000 sends back as many bytes as asked for (throughput check).
	if ln, err := n.onet.ListenTCP(9000); err == nil {
		go acceptLoop(ln, func(c net.Conn) {
			defer c.Close()
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				return
			}
			size, _ := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
			if size <= 0 || size > 200<<20 {
				return
			}
			io.CopyN(c, zeroReader{}, size)
		})
	}
}

func acceptLoop(ln net.Listener, handle func(net.Conn)) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go handle(c)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
