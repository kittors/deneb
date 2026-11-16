// deneb-front is an entrance to the control server on another machine
// (design/plan/control-reachability-20261116.md). Where the control
// server's own address cannot be reached, a device connects here, and
// deneb-front passes the connection on unopened to the control server's
// -front-listen, saying in a PROXY header whom it is from. TLS ends at the
// control server: the device checks its pin as on its own address, and
// deneb-front sees addresses and amounts, never what is said. It logs no
// device's address.
//
//	deneb-front -listen :47443 -to <control server>:47444
//
// The control server's address comes from -to or DENEB_FRONT_TO, the
// deployment's (deploy/front), never the repository's.
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/kittors/deneb/control/front"
)

const (
	// maxConns bounds the connections at once: each is two sockets and
	// two goroutines.
	maxConns = 4096
	// dialWait is how long the control server has to answer.
	dialWait = 10 * time.Second
	// lingerAfter: once one side of a connection is done, how long the
	// other has to finish before both close.
	lingerAfter = 30 * time.Second
)

func main() {
	listen := flag.String("listen", ":47443", "address to listen on")
	to := flag.String("to", os.Getenv("DENEB_FRONT_TO"), "the control server's -front-listen, host:port ($DENEB_FRONT_TO)")
	flag.Parse()
	if *to == "" {
		log.Fatal("-to: the control server's -front-listen, as <control server>:47444")
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("passing connections on %s to %s", *listen, *to)
	e := newEntrance(*to, log.Printf)
	log.Fatal(e.serve(ln))
}

// entrance passes each connection it accepts on to the control server.
type entrance struct {
	to    string
	slots chan struct{}
	logf  func(string, ...any)

	mu     sync.Mutex
	warned map[string]time.Time // what went wrong, logged once a minute
}

func newEntrance(to string, logf func(string, ...any)) *entrance {
	return &entrance{to: to, slots: make(chan struct{}, maxConns), logf: logf, warned: map[string]time.Time{}}
}

func (e *entrance) serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			e.warn("accept", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case e.slots <- struct{}{}:
			go func() {
				defer func() { <-e.slots }()
				e.pass(c)
			}()
		default:
			c.Close()
			e.warn("full", errors.New("too many connections at once"))
		}
	}
}

// pass passes c on to the control server, both ways, behind a PROXY
// header with c's address.
func (e *entrance) pass(c net.Conn) {
	defer c.Close()
	src, err := netip.ParseAddrPort(c.RemoteAddr().String())
	dst, err2 := netip.ParseAddrPort(c.LocalAddr().String())
	if err != nil || err2 != nil {
		return
	}
	u, err := (&net.Dialer{Timeout: dialWait, KeepAlive: 30 * time.Second}).Dial("tcp", e.to)
	if err != nil {
		e.warn("dial", err)
		return
	}
	defer u.Close()
	if _, err := u.Write(front.Header(src, dst)); err != nil {
		e.warn("dial", err)
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(u, c); closeWrite(u); done <- struct{}{} }()
	go func() { io.Copy(c, u); closeWrite(c); done <- struct{}{} }()
	<-done
	// One side is done: the other has a moment to finish, then both
	// close, so a side that never ends holds nothing for long.
	deadline := time.Now().Add(lingerAfter)
	c.SetDeadline(deadline)
	u.SetDeadline(deadline)
	<-done
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	}
}

// warn logs what went wrong, once a minute for each kind.
func (e *entrance) warn(kind string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.warned[kind]) < time.Minute {
		return
	}
	e.warned[kind] = time.Now()
	e.logf("deneb-front: %s: %v", kind, err)
}
