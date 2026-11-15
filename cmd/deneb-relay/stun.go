package main

import (
	"errors"
	"net"
	"net/netip"
	"time"

	"tailscale.com/net/pktinfo"
	"tailscale.com/net/stun"
)

// stunServer answers STUN binding requests: a device asks it the address
// it is seen from. tailscale.com/net/stunserver does the same, but closes
// its socket in a goroutine of its own once its context ends, which may
// run after run has returned, the port still held
// (TestRemovedRelayHoldsNoPort). This one's socket is closed by close: the
// port is free when it returns.
type stunServer struct {
	pc *net.UDPConn
	// pktInfo: the kernel tells which of this machine's addresses each
	// request came to, and the answer goes from that one. On a server with
	// several addresses, an answer from another may not get through
	// (tailscale/tailscale#21404, which stunserver handles the same way).
	pktInfo bool
	done    chan struct{} // closed when serve returns
}

// listenSTUN binds addr and answers there until close.
func listenSTUN(addr string, logf func(string, ...any)) (*stunServer, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	pc, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	s := &stunServer{pc: pc, done: make(chan struct{})}
	if err := pktinfo.Enable(pc); err == nil {
		s.pktInfo = true
	} else if !errors.Is(err, errors.ErrUnsupported) {
		logf("STUN: answering from the address the system picks: %v", err)
	}
	go s.serve(logf)
	return s, nil
}

// LocalAddr is the address it answers on.
func (s *stunServer) LocalAddr() net.Addr { return s.pc.LocalAddr() }

// close closes the socket, and waits for serve to stop reading it.
func (s *stunServer) close() {
	s.pc.Close()
	<-s.done
}

func (s *stunServer) serve(logf func(string, ...any)) {
	defer close(s.done)
	var buf [64 << 10]byte
	var oob, oobOut [256]byte
	for {
		n, oobn, _, remote, err := s.pc.ReadMsgUDPAddrPort(buf[:], oob[:])
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			logf("STUN: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		pkt := buf[:n]
		if !stun.Is(pkt) {
			continue
		}
		txid, err := stun.ParseBindingRequest(pkt)
		if err != nil {
			continue
		}
		remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
		res := stun.Response(txid, remote)
		var local netip.Addr
		if s.pktInfo {
			local = pktinfo.Dst(oob[:oobn])
		}
		if src := pktinfo.AppendSrc(oobOut[:0], local); len(src) > 0 {
			s.pc.WriteMsgUDPAddrPort(res, src, remote)
		} else {
			s.pc.WriteToUDPAddrPort(res, remote)
		}
	}
}
