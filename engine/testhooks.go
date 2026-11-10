//go:build denebtest

package engine

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/engine/dns"
	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// The test build (-tags denebtest) adds what the acceptance tests need and a
// product must not have: knobs that simulate failures and stage attacks
// (multi-lan-tech §6.2, §6.4), and small services on the overlay address.
// Plan §3 M1 item 6: "注入接口只放在测试构建里".

// TestBuild reports whether this binary has the test endpoints.
const TestBuild = true

// Test service ports, on the overlay address only.
const (
	testWebPort  = 80   // says who answered, who asked, and the scopes they share
	testTickPort = 7000 // a numbered line every 100 ms: stalls and breaks show
	testEchoPort = 7    // UDP echo
	testSinkPort = 9000 // sends back as many bytes as asked for
)

func (e *Engine) listenOverlayLocked(port uint16) (net.Listener, error) {
	if e.stack != nil {
		return e.stack.ListenTCP(port)
	}
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: e.self.AsSlice(), Port: int(port)})
}

func (e *Engine) startTestServicesLocked() {
	self := e.self
	if ln, err := e.listenOverlayLocked(testWebPort); err == nil {
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remote, _ := netip.ParseAddrPort(r.RemoteAddr)
			shared := "-"
			if pp := e.f.Policy().Peers[remote.Addr()]; pp != nil {
				shared = strings.Join(pp.Scopes, ",")
			}
			e.mu.Lock()
			me := ""
			if e.nm != nil {
				me = e.nm.Self.FQDN
			}
			e.mu.Unlock()
			fmt.Fprintf(w, "hello from %s (%s); you are %s; scopes we share: %s\n", me, self, remote.Addr(), shared)
		})}
		go srv.Serve(ln)
	} else {
		e.logf("[test] listen :%d: %v", testWebPort, err)
	}
	if ln, err := e.listenOverlayLocked(testTickPort); err == nil {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					for seq := 0; ; seq++ {
						if _, err := fmt.Fprintf(c, "%d %d\n", seq, time.Now().UnixMilli()); err != nil {
							return
						}
						time.Sleep(100 * time.Millisecond)
					}
				}()
			}
		}()
	}
	if ln, err := e.listenOverlayLocked(testSinkPort); err == nil {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					line, err := bufio.NewReader(c).ReadString('\n')
					if err != nil {
						return
					}
					n, _ := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
					if n > 0 && n <= 200<<20 {
						io.CopyN(c, zeros{}, n)
					}
				}()
			}
		}()
	}
	var uc net.PacketConn
	var err error
	if e.stack != nil {
		uc, err = e.stack.ListenUDP(testEchoPort)
	} else {
		uc, err = net.ListenUDP("udp4", &net.UDPAddr{IP: self.AsSlice(), Port: testEchoPort})
	}
	if err == nil {
		go func() {
			buf := make([]byte, 1500)
			for {
				n, from, err := uc.ReadFrom(buf)
				if err != nil {
					return
				}
				uc.WriteTo(buf[:n], from)
			}
		}()
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// TestHandler serves /v1/test/...
func (e *Engine) TestHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/test/block", e.testBlock)
	mux.HandleFunc("POST /v1/test/loss", e.testLoss)
	mux.HandleFunc("POST /v1/test/dup", e.testDup)
	mux.HandleFunc("POST /v1/test/relaylate", e.testRelayLate)
	mux.HandleFunc("POST /v1/test/dnsflush", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode string `json:"mode"` // off, on, now
		}
		if !readBody(w, r, &req) {
			return
		}
		switch req.Mode {
		case "off", "on":
			e.mu.Lock()
			e.test.noDNSFlush = req.Mode == "off"
			e.mu.Unlock()
		case "now":
			if err := flushDNS(); err != nil {
				localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInternal, Message: err.Error()})
				return
			}
		default:
			localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "mode: off, on or now"})
			return
		}
		writeJSON(w, map[string]any{"mode": req.Mode})
	})
	mux.HandleFunc("GET /v1/test/goroutines", func(w http.ResponseWriter, r *http.Request) {
		// Every goroutine's stack: what is blocked where, during a stall.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		pprof.Lookup("goroutine").WriteTo(w, 2)
	})
	mux.HandleFunc("POST /v1/test/trace", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Port uint16 `json:"port"`
		}
		if !readBody(w, r, &req) {
			return
		}
		e.mu.Lock()
		tw := e.tunw
		e.mu.Unlock()
		if tw != nil {
			tw.SetTrace(req.Port, e.logf)
		}
		writeJSON(w, map[string]any{"port": req.Port})
	})
	mux.HandleFunc("POST /v1/test/netchange", func(w http.ResponseWriter, r *http.Request) {
		go e.netChanged("asked through the local API (test)")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/test/attack-route", e.testAttackRoute)
	mux.HandleFunc("POST /v1/test/filter", e.testFilter)
	mux.HandleFunc("POST /v1/test/inject", e.testInject)
	mux.HandleFunc("GET /v1/test/tick", e.testTick)
	mux.HandleFunc("GET /v1/test/http", e.testHTTP)
	mux.HandleFunc("GET /v1/test/drops", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"drops": e.f.Drops(), "flows": e.f.Flows()})
	})
	mux.HandleFunc("GET /v1/test/paths", e.testPaths)
	mux.HandleFunc("GET /v1/test/ping-addr", e.testPingAddr)
	mux.HandleFunc("GET /v1/test/probe", e.testProbe)
	mux.HandleFunc("POST /v1/test/shutdown", func(w http.ResponseWriter, r *http.Request) {
		e.logf("[test] stop asked for through the local API")
		w.WriteHeader(http.StatusNoContent)
		e.requestStop()
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: err.Error()})
		return false
	}
	return true
}

// peerByName resolves a name (or id or address) to a network map peer.
func (e *Engine) peerByName(name string) (proto.Peer, error) {
	ent, err := e.resolve(name)
	if err != nil {
		// A peer whose scopes are all paused is not in the name table.
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.nm != nil {
			for _, p := range e.nm.Peers {
				if string(p.ID) == name || p.Name+"."+p.OwnerName == name {
					return p, nil
				}
			}
		}
		return proto.Peer{}, err
	}
	p, ok := e.peerByAddr(ent.Addr)
	if !ok {
		return proto.Peer{}, &apitype.Error{Code: apitype.ErrNotFound, Message: name + " is not a peer"}
	}
	return p, nil
}

// testBlock drops every UDP packet between this device and one peer, in
// both directions, inside the engine (acceptance item 10; no firewall).
func (e *Engine) testBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Peer string `json:"peer"`
		On   bool   `json:"on"`
	}
	if !readBody(w, r, &req) {
		return
	}
	p, err := e.peerByName(req.Peer)
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	e.ms.SetBlocked(p.NodeKey, req.On)
	writeJSON(w, map[string]any{"peer": p.FQDN, "blocked": req.On})
}

// testLoss is M2's bad line: drop a share of the UDP to and from a peer.
func (e *Engine) testLoss(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Peer string  `json:"peer"`
		Tx   float64 `json:"tx"`
		Rx   float64 `json:"rx"`
	}
	if !readBody(w, r, &req) {
		return
	}
	p, err := e.peerByName(req.Peer)
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	e.ms.SetLoss(p.NodeKey, req.Tx, req.Rx)
	writeJSON(w, map[string]any{"peer": p.FQDN, "tx": req.Tx, "rx": req.Rx})
}

// testRelayLate makes a share of a peer's relay pongs come late: a line
// that loses TCP too, where the relay is no cleaner than the direct path.
func (e *Engine) testRelayLate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Peer string  `json:"peer"`
		Frac float64 `json:"frac"`
		Ms   int     `json:"ms"`
	}
	if !readBody(w, r, &req) {
		return
	}
	p, err := e.peerByName(req.Peer)
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	e.ms.SetRelayLate(p.NodeKey, req.Frac, time.Duration(req.Ms)*time.Millisecond)
	writeJSON(w, map[string]any{"peer": p.FQDN, "frac": req.Frac, "ms": req.Ms})
}

// testDup switches "small packets twice on a lossy direct path" at run
// time: auto, on or off (an A/B on a real line without a restart).
func (e *Engine) testDup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !readBody(w, r, &req) {
		return
	}
	switch req.Mode {
	case "auto", "on", "off":
	default:
		localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "要 auto、on 或 off"})
		return
	}
	e.ms.SetDupSmall(req.Mode)
	e.logf("[test] small packets twice: %s", req.Mode)
	writeJSON(w, map[string]any{"mode": req.Mode})
}

// testAttackRoute makes this device route a prefix to a peer and send to
// it, as a member of one scope trying to reach another through a device that
// is in both (multi-lan-tech §6.4). The victim's filter must drop it.
func (e *Engine) testAttackRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Peer   string `json:"peer"`
		Prefix string `json:"prefix"`
		Clear  bool   `json:"clear"`
	}
	if !readBody(w, r, &req) {
		return
	}
	p, err := e.peerByName(req.Peer)
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	e.mu.Lock()
	if e.test.extraAllowed == nil {
		e.test.extraAllowed = map[proto.DeviceID][]netip.Prefix{}
	}
	if req.Clear {
		delete(e.test.extraAllowed, p.ID)
	} else {
		pfx, err := netip.ParsePrefix(req.Prefix)
		if err != nil {
			e.mu.Unlock()
			localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: err.Error()})
			return
		}
		e.test.extraAllowed[p.ID] = append(e.test.extraAllowed[p.ID], pfx)
	}
	if e.nm != nil {
		e.applyNetmapLocked(e.nm, 0)
	}
	e.mu.Unlock()
	writeJSON(w, map[string]any{"peer": p.FQDN, "prefix": req.Prefix, "clear": req.Clear})
}

func (e *Engine) testFilter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Off bool `json:"off"`
	}
	if !readBody(w, r, &req) {
		return
	}
	e.mu.Lock()
	e.test.filterOff = req.Off
	if e.nm != nil {
		e.applyNetmapLocked(e.nm, 0)
	}
	e.mu.Unlock()
	writeJSON(w, map[string]any{"filterOff": req.Off})
}

// testInject sends a packet with a forged source address, skipping this
// device's filter: the receiving peer's WireGuard must drop it, since the
// source is not the sender's own address (layer 4).
func (e *Engine) testInject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Src   string `json:"src"`
		Dst   string `json:"dst"`
		DPort uint16 `json:"dport"`
		Count int    `json:"count"`
	}
	if !readBody(w, r, &req) {
		return
	}
	src, err1 := netip.ParseAddr(req.Src)
	dst, err2 := netip.ParseAddr(req.Dst)
	if err1 != nil || err2 != nil {
		localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "src and dst must be addresses"})
		return
	}
	e.mu.Lock()
	tw := e.tunw
	e.mu.Unlock()
	if tw == nil {
		localapi.WriteError(w, errNotLoggedIn)
		return
	}
	n := max(req.Count, 1)
	for i := 0; i < n; i++ {
		tw.InjectUnfiltered(tcpSYN(src, dst, uint16(40000+i), req.DPort))
	}
	writeJSON(w, map[string]any{"injected": n, "src": src.String(), "dst": dst.String(), "dport": req.DPort})
}

func tcpSYN(src, dst netip.Addr, sport, dport uint16) []byte {
	b := make([]byte, 40)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], 40)
	b[8] = 64
	b[9] = filter.ProtoTCP
	s4, d4 := src.As4(), dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	binary.BigEndian.PutUint16(b[10:], dns.Checksum(b[:20], 0))
	binary.BigEndian.PutUint16(b[20:], sport)
	binary.BigEndian.PutUint16(b[22:], dport)
	binary.BigEndian.PutUint32(b[24:], 1)
	b[32] = 5 << 4
	b[33] = 0x02 // SYN
	binary.BigEndian.PutUint16(b[34:], 65535)
	var ph [12]byte
	copy(ph[0:4], s4[:])
	copy(ph[4:8], d4[:])
	ph[9] = filter.ProtoTCP
	binary.BigEndian.PutUint16(ph[10:], 20)
	var s uint32
	for i := 0; i < 12; i += 2 {
		s += uint32(ph[i])<<8 | uint32(ph[i+1])
	}
	binary.BigEndian.PutUint16(b[36:], dns.Checksum(b[20:], s))
	return b
}

// testTick holds a connection to a peer's tick service for a while and
// reports how the stream went: lines received, the longest stall, whether
// it broke. It is how "the long connection stays up" is measured.
func (e *Engine) testTick(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	secs, _ := strconv.Atoi(q.Get("seconds"))
	if secs <= 0 || secs > 600 {
		secs = 10
	}
	port := uint16(testTickPort)
	if p, err := strconv.Atoi(q.Get("port")); err == nil && p > 0 {
		port = uint16(p)
	}
	ent, err := e.resolve(q.Get("to"))
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	c, err := e.dialPeer(ctx, netip.AddrPortFrom(ent.Addr, port))
	cancel()
	if err != nil {
		localapi.WriteError(w, &apitype.Error{Code: apitype.ErrRefused, Message: "connect: " + err.Error()})
		return
	}
	defer c.Close()
	type gap struct {
		At time.Time `json:"at"` // when the stall ended
		Ms int64     `json:"ms"`
	}
	type result struct {
		Lines      int    `json:"lines"`
		FirstSeq   int    `json:"firstSeq"`
		LastSeq    int    `json:"lastSeq"`
		MaxGapMs   int64  `json:"maxGapMs"`
		Broken     bool   `json:"broken"`
		Error      string `json:"error,omitempty"`
		DurationMs int64  `json:"durationMs"`
		// Gaps are the stalls over 300 ms (lines come every 100 ms), with
		// wall-clock times to line up with what the screen did then.
		Gaps []gap `json:"gaps,omitempty"`
	}
	res := result{FirstSeq: -1}
	start := time.Now()
	deadline := start.Add(time.Duration(secs) * time.Second)
	c.SetReadDeadline(deadline)
	sc := bufio.NewScanner(c)
	last := start
	for sc.Scan() {
		now := time.Now()
		if g := now.Sub(last).Milliseconds(); res.Lines > 0 {
			res.MaxGapMs = max(res.MaxGapMs, g)
			if g > 300 && len(res.Gaps) < 100 {
				res.Gaps = append(res.Gaps, gap{At: now, Ms: g})
			}
		}
		last = now
		var seq int
		fmt.Sscan(strings.Fields(sc.Text())[0], &seq)
		if res.FirstSeq < 0 {
			res.FirstSeq = seq
		}
		res.LastSeq = seq
		res.Lines++
	}
	res.DurationMs = time.Since(start).Milliseconds()
	if err := sc.Err(); err != nil {
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			res.Broken, res.Error = true, err.Error()
		}
	} else if time.Now().Before(deadline) {
		res.Broken, res.Error = true, "closed by peer"
	}
	if g := deadline.Sub(last).Milliseconds(); g > res.MaxGapMs && !res.Broken {
		res.MaxGapMs = g
	}
	writeJSON(w, res)
}

// testHTTP fetches a URL path from a peer through the overlay, so headless
// test nodes can try a service without a SOCKS-aware curl.
func (e *Engine) testHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port := 80
	if p, err := strconv.Atoi(q.Get("port")); err == nil && p > 0 {
		port = p
	}
	ent, err := e.resolve(q.Get("to"))
	if err != nil {
		localapi.WriteError(w, err)
		return
	}
	hc := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return e.dialPeer(ctx, netip.AddrPortFrom(ent.Addr, uint16(port)))
		}},
	}
	resp, err := hc.Get(fmt.Sprintf("http://%s:%d%s", ent.FQDN, port, q.Get("path")))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	writeJSON(w, map[string]any{"ok": true, "status": resp.StatusCode, "body": string(b)})
}

func (e *Engine) testPaths(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]any{}
	if e.nm != nil {
		for _, p := range e.nm.Peers {
			st, ok := e.ms.Status(p.NodeKey)
			if !ok {
				out[p.FQDN] = "inactive"
				continue
			}
			cands := make([]string, 0, len(st.Candidates))
			for _, c := range st.Candidates {
				cands = append(cands, c.String())
			}
			out[p.FQDN] = map[string]any{
				"kind": st.Kind, "endpoint": st.Endpoint.String(), "relay": st.Relay, "rttMs": st.RTT.Milliseconds(),
				"loss": st.Loss, "since": st.Since, "candidates": cands, "blocked": st.Blocked,
				"idle": st.Idle, "held": st.Held, "dup": st.Dup, "txDup": st.TxDup, "txWake": st.TxWake, "hsBoth": st.HSBoth, "txHSBoth": st.TxHSBoth, "rxHSDupes": st.RxHSDupes, "localRefused": st.LocalRefused, "backedOff": st.BackedOff, "reason": st.Reason, "prev": st.Prev, "pingsTx": st.PingsTx, "pingsRelay": st.PingsRelay, "pingsRx": st.PingsRx, "pongsRx": st.PongsRx,
				"txDirect": st.TxDirect, "txRelay": st.TxRelay, "rxDirect": st.RxDirect, "rxRelay": st.RxRelay,
			}
		}
	}
	writeJSON(w, out)
}

// testPingAddr pings a bare address through the filter, with no name or
// peer lookup: the transit attack sends to an address the attacker cannot
// see, which is exactly what the filters must stop.
func (e *Engine) testPingAddr(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dst, err := netip.ParseAddr(q.Get("addr"))
	if err != nil {
		localapi.WriteError(w, &apitype.Error{Code: apitype.ErrInvalid, Message: "addr"})
		return
	}
	n, _ := strconv.Atoi(q.Get("n"))
	n = min(max(n, 1), 20)
	e.mu.Lock()
	self, tw := e.self, e.tunw
	e.mu.Unlock()
	if tw == nil {
		localapi.WriteError(w, errNotLoggedIn)
		return
	}
	id := uint16(time.Now().UnixNano()) | 0x8000
	got := 0
	for seq := 0; seq < n; seq++ {
		key := uint32(id)<<16 | uint32(seq)
		ch := make(chan time.Time, 1)
		e.pinger.mu.Lock()
		e.pinger.waits[key] = ch
		e.pinger.mu.Unlock()
		tw.Send(icmpEcho(self, dst, id, uint16(seq)))
		select {
		case <-ch:
			got++
		case <-time.After(700 * time.Millisecond):
		}
		e.pinger.mu.Lock()
		delete(e.pinger.waits, key)
		e.pinger.mu.Unlock()
	}
	writeJSON(w, map[string]any{"addr": dst.String(), "sent": n, "received": got})
}

// testProbe tries a peer again and again and streams one line per try, with
// its time: acceptance items 7 and 9 line these up with the moments the
// screen was dragged or clicked. kind is ping, tcp or http. The name is
// resolved at every try, since the peer may vanish and come back.
func (e *Engine) testProbe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to, kind := q.Get("to"), q.Get("kind")
	if kind == "" {
		kind = "ping"
	}
	port := 80
	if p, err := strconv.Atoi(q.Get("port")); err == nil && p > 0 {
		port = p
	}
	interval, err := time.ParseDuration(q.Get("interval"))
	if err != nil || interval < 100*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	secs, _ := strconv.Atoi(q.Get("seconds"))
	if secs <= 0 || secs > 900 {
		secs = 60
	}
	timeout := min(time.Second, max(interval, 500*time.Millisecond))
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	type line struct {
		T   time.Time `json:"t"`
		OK  bool      `json:"ok"`
		Ms  float64   `json:"ms,omitempty"`
		Err string    `json:"err,omitempty"`
	}
	end := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(end) && r.Context().Err() == nil {
		start := time.Now()
		res := line{T: start}
		switch kind {
		case "ping":
			sum, err := e.Ping(r.Context(), to, 1, 0, timeout, nil)
			switch {
			case err != nil:
				res.Err = err.Error()
			case sum.Received == 0:
				res.Err = "no answer"
			default:
				res.OK, res.Ms = true, sum.Results[0].RTTMs
			}
		default:
			ent, err := e.resolve(to)
			if err != nil {
				res.Err = err.Error()
				break
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			c, err := e.dialPeer(ctx, netip.AddrPortFrom(ent.Addr, uint16(port)))
			if err == nil && kind == "http" {
				c.SetDeadline(time.Now().Add(timeout))
				fmt.Fprintf(c, "HEAD / HTTP/1.0\r\n\r\n")
				buf := make([]byte, 12)
				if _, rerr := io.ReadFull(c, buf); rerr != nil || !strings.HasPrefix(string(buf), "HTTP/") {
					err = fmt.Errorf("no HTTP answer: %v", rerr)
				}
			}
			cancel()
			if c != nil {
				c.Close()
			}
			if err != nil {
				res.Err = err.Error()
			} else {
				res.OK, res.Ms = true, float64(time.Since(start).Microseconds())/1000
			}
		}
		enc.Encode(res)
		if flusher != nil {
			flusher.Flush()
		}
		if d := interval - time.Since(start); d > 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(d):
			}
		}
	}
}
