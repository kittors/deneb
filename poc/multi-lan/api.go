package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
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

// serveAPI is a loopback-only control surface used by the test scripts. It
// stands in for the UI: pausing a LAN here is "turning the LAN's switch off"
// in DESIGN.md scene 3.
func (n *node) serveAPI(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", n.apiStatus)
	mux.HandleFunc("/events", n.apiEvents)
	mux.HandleFunc("/ping", n.apiPing)
	mux.HandleFunc("/http", n.apiHTTP)
	mux.HandleFunc("/resolve", n.apiResolve)
	mux.HandleFunc("/tick", n.apiTick)
	mux.HandleFunc("/pull", n.apiPull)
	mux.HandleFunc("/block", n.apiBlock)
	mux.HandleFunc("/lan", n.apiLAN)
	mux.HandleFunc("/inject", n.apiInject)
	// Lets the (unprivileged) test scripts stop a root node and have it undo
	// its route and interface, instead of needing sudo to kill it.
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"stopping": true})
		go func() { time.Sleep(200 * time.Millisecond); n.stop("api") }()
	})
	log.Printf("control API on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func httpErr(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	writeJSON(w, map[string]string{"error": err.Error()})
}

// target resolves ?to= (a name or an address) and returns the peer if the
// address belongs to one; attack tests deliberately aim at non-peers.
func (n *node) target(r *http.Request) (netip.Addr, *nodePeer, error) {
	to := r.URL.Query().Get("to")
	a, _, err := n.resolve(to)
	if err != nil {
		return netip.Addr{}, nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range n.peers {
		if p.addr == a {
			return a, p, nil
		}
	}
	return a, nil, nil
}

func (n *node) apiStatus(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	var lans []map[string]any
	for _, l := range n.cfg.LANs {
		lans = append(lans, map[string]any{"id": l.ID, "name": l.Name, "active": n.active[l.ID], "open": l.Open})
	}
	peers := append([]*nodePeer(nil), n.peers...)
	n.mu.Unlock()
	var ps []map[string]any
	for _, p := range peers {
		st := n.ms.status(p.ps)
		ps = append(ps, map[string]any{"name": p.conf.Label(), "addr": p.addr.String(), "sharedLANs": p.conf.LANs, "path": st})
	}
	n.ms.mu.Lock()
	relayUp, stunAddr := n.ms.derpUp, n.ms.stunAddr
	n.ms.mu.Unlock()
	writeJSON(w, map[string]any{
		"self":       n.cfg.Name + "." + n.cfg.Owner,
		"addr":       n.self.String(),
		"udpPort":    n.ms.port,
		"relay":      map[string]any{"host": n.cfg.Relay.Host, "ip": n.cfg.Relay.IP, "connected": relayUp},
		"publicAddr": stunAddr.String(),
		"lans":       lans,
		"peers":      ps,
		"drops":      n.ft.dropCounts(),
	})
}

func (n *node) apiEvents(w http.ResponseWriter, r *http.Request) {
	since := time.Time{}
	if ms, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); err == nil {
		since = time.UnixMilli(ms)
	}
	writeJSON(w, n.events.since(since))
}

type pingResult struct {
	Seq    int       `json:"seq"`
	SentAt time.Time `json:"sentAt"`
	Path   string    `json:"pathAtSend"`
	RTTms  float64   `json:"rttMs"` // -1 = lost
}

func (n *node) apiPing(w http.ResponseWriter, r *http.Request) {
	dst, p, err := n.target(r)
	if err != nil {
		httpErr(w, 400, err)
		return
	}
	q := r.URL.Query()
	count, _ := strconv.Atoi(q.Get("n"))
	if count <= 0 {
		count = 10
	}
	interval, err := time.ParseDuration(q.Get("interval"))
	if err != nil {
		interval = 200 * time.Millisecond
	}
	timeout, err := time.ParseDuration(q.Get("timeout"))
	if err != nil {
		timeout = 2 * time.Second
	}
	res, err := n.ping(dst, p, count, interval, timeout)
	if err != nil {
		httpErr(w, 500, err)
		return
	}
	lost, sum, nOK := 0, 0.0, 0
	minR, maxR := 1e9, 0.0
	for _, x := range res {
		if x.RTTms < 0 {
			lost++
			continue
		}
		nOK++
		sum += x.RTTms
		minR, maxR = min(minR, x.RTTms), max(maxR, x.RTTms)
	}
	summary := map[string]any{"to": dst.String(), "sent": count, "lost": lost}
	if nOK > 0 {
		summary["minMs"], summary["avgMs"], summary["maxMs"] = minR, sum/float64(nOK), maxR
	}
	if q.Get("summary") == "1" {
		writeJSON(w, summary)
		return
	}
	writeJSON(w, map[string]any{"summary": summary, "pings": res})
}

// ping measures overlay round trips with a UDP echo (port 7) through the
// tunnel. It does not use ICMP: the netstack ICMP reader can miss replies
// that arrive in a burst (e.g. right after a handshake), which would show up
// as fake loss exactly when we want to measure real loss.
func (n *node) ping(dst netip.Addr, p *nodePeer, count int, interval, timeout time.Duration) ([]pingResult, error) {
	c, err := n.onet.DialUDP(netip.AddrPortFrom(dst, 7))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	type reply struct {
		seq int
		at  time.Time
	}
	replies := make(chan reply, 1024)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buf := make([]byte, 64)
		for {
			c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			nn, err := c.Read(buf)
			select {
			case <-stop:
				return
			default:
			}
			if err != nil || nn < 4 {
				continue
			}
			replies <- reply{seq: int(binary.BigEndian.Uint32(buf[:4])), at: time.Now()}
		}
	}()
	res := make([]pingResult, count)
	record := func(rp reply) {
		if rp.seq >= 0 && rp.seq < count && res[rp.seq].RTTms < 0 {
			if d := rp.at.Sub(res[rp.seq].SentAt); d <= timeout {
				res[rp.seq].RTTms = float64(d.Microseconds()) / 1000
			}
		}
	}
	msg := make([]byte, 16)
	for i := 0; i < count; i++ {
		path := "-"
		if p != nil {
			path = n.ms.pathOf(p.ps)
		}
		binary.BigEndian.PutUint32(msg[:4], uint32(i))
		res[i] = pingResult{Seq: i, SentAt: time.Now(), Path: path, RTTms: -1}
		c.Write(msg)
		next := res[i].SentAt.Add(interval)
		for wait := time.Until(next); wait > 0; wait = time.Until(next) {
			select {
			case rp := <-replies:
				record(rp)
			case <-time.After(wait):
			}
		}
	}
	end := time.Now().Add(timeout)
	for wait := time.Until(end); wait > 0; wait = time.Until(end) {
		select {
		case rp := <-replies:
			record(rp)
		case <-time.After(wait):
		}
	}
	return res, nil
}

func (n *node) dialOverlay(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip, _, err := n.resolve(host)
	if err != nil {
		return nil, err
	}
	pn, _ := strconv.Atoi(port)
	return n.onet.DialTCP(ctx, netip.AddrPortFrom(ip, uint16(pn)))
}

func (n *node) apiHTTP(w http.ResponseWriter, r *http.Request) {
	c := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{DialContext: n.dialOverlay}}
	resp, err := c.Get(r.URL.Query().Get("url"))
	if err != nil {
		httpErr(w, 502, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	writeJSON(w, map[string]any{"status": resp.StatusCode, "body": strings.TrimSpace(string(body))})
}

func (n *node) apiResolve(w http.ResponseWriter, r *http.Request) {
	a, hits, err := n.resolve(r.URL.Query().Get("name"))
	out := map[string]any{"candidates": hits}
	if err != nil {
		out["error"] = err.Error()
	} else {
		out["addr"] = a.String()
	}
	writeJSON(w, out)
}

type gap struct {
	At    time.Time `json:"at"`
	GapMs int64     `json:"gapMs"`
	Open  bool      `json:"stillStalledAtEnd,omitempty"`
}

// apiTick holds one TCP connection to the peer's :7000 for `secs` seconds and
// reports how long the stream stalled and whether the connection survived.
func (n *node) apiTick(w http.ResponseWriter, r *http.Request) {
	dst, _, err := n.target(r)
	if err != nil {
		httpErr(w, 400, err)
		return
	}
	secs, _ := strconv.Atoi(r.URL.Query().Get("secs"))
	if secs <= 0 {
		secs = 30
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	c, err := n.onet.DialTCP(ctx, netip.AddrPortFrom(dst, 7000))
	cancel()
	if err != nil {
		httpErr(w, 502, err)
		return
	}
	defer c.Close()
	connectMs := time.Since(start).Milliseconds()
	end := start.Add(time.Duration(secs) * time.Second)
	c.SetReadDeadline(end)
	br := bufio.NewReader(c)
	var lines, lastSeq int64 = 0, -1
	seqHoles := 0
	last := time.Now()
	var maxGap int64
	var gaps []gap
	var arrivals []int64 // ms since start, one per line, to plot recovery
	var readErr string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if time.Now().Before(end.Add(-50 * time.Millisecond)) {
				readErr = err.Error()
			}
			break
		}
		now := time.Now()
		seq, _ := strconv.ParseInt(strings.Fields(line)[0], 10, 64)
		if lastSeq >= 0 && seq != lastSeq+1 {
			seqHoles++
		}
		lastSeq = seq
		g := now.Sub(last).Milliseconds()
		if lines > 0 && g > maxGap {
			maxGap = g
		}
		if lines > 0 && g > 300 {
			gaps = append(gaps, gap{At: last, GapMs: g})
		}
		arrivals = append(arrivals, now.Sub(start).Milliseconds())
		last = now
		lines++
	}
	// A stream that never resumes must count as one long stall, not vanish.
	if readErr == "" {
		if tail := time.Since(last).Milliseconds(); tail > 300 {
			gaps = append(gaps, gap{At: last, GapMs: tail, Open: true})
			maxGap = max(maxGap, tail)
		}
	}
	writeJSON(w, map[string]any{
		"to": dst.String(), "connectMs": connectMs, "lines": lines, "seqHoles": seqHoles,
		"maxGapMs": maxGap, "stalls": gaps, "connectionError": readErr, "arrivalsMs": arrivals,
		"events": n.events.since(start),
	})
}

func (n *node) apiPull(w http.ResponseWriter, r *http.Request) {
	dst, p, err := n.target(r)
	if err != nil {
		httpErr(w, 400, err)
		return
	}
	size, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
	if size <= 0 {
		size = 5 << 20
	}
	path := "-"
	if p != nil {
		path = n.ms.pathOf(p.ps)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	c, err := n.onet.DialTCP(ctx, netip.AddrPortFrom(dst, 9000))
	cancel()
	if err != nil {
		httpErr(w, 502, err)
		return
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(120 * time.Second))
	start := time.Now()
	fmt.Fprintf(c, "%d\n", size)
	got, err := io.Copy(io.Discard, c)
	el := time.Since(start)
	out := map[string]any{"to": dst.String(), "pathAtStart": path, "bytes": got, "seconds": el.Seconds(),
		"mbps": float64(got*8) / el.Seconds() / 1e6}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, out)
}

func (n *node) apiBlock(w http.ResponseWriter, r *http.Request) {
	p := n.peerByLabel(r.URL.Query().Get("peer"))
	if p == nil {
		httpErr(w, 404, fmt.Errorf("no such peer"))
		return
	}
	on := r.URL.Query().Get("on") != "0"
	n.ms.setBlocked(p.ps, on)
	writeJSON(w, map[string]any{"peer": p.conf.Label(), "udpBlocked": on, "at": time.Now()})
}

func (n *node) apiLAN(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	on := r.URL.Query().Get("on") != "0"
	if err := n.setLAN(id, on); err != nil {
		httpErr(w, 400, err)
		return
	}
	writeJSON(w, map[string]any{"lan": id, "active": on, "at": time.Now()})
}

// apiInject pushes a hand-made IPv4/UDP packet with an arbitrary source
// address into WireGuard, skipping this node's filter. It emulates a broken
// or malicious device trying to forward another LAN's traffic, to show that
// the receiver's cryptokey routing (AllowedIPs = the sender's own /32) drops
// it as well.
func (n *node) apiInject(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, err1 := netip.ParseAddr(q.Get("src"))
	dst, err2 := netip.ParseAddr(q.Get("dst"))
	dport, _ := strconv.Atoi(q.Get("dport"))
	if err1 != nil || err2 != nil || !src.Is4() || !dst.Is4() {
		httpErr(w, 400, fmt.Errorf("need IPv4 src and dst"))
		return
	}
	payload := []byte("spoofed by deneb-poc")
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = protoUDP
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:], ipChecksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:], 40000)
	binary.BigEndian.PutUint16(pkt[22:], uint16(dport))
	binary.BigEndian.PutUint16(pkt[24:], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	count, _ := strconv.Atoi(q.Get("n"))
	if count <= 0 {
		count = 3
	}
	for i := 0; i < count; i++ {
		n.ft.inject <- append([]byte(nil), pkt...)
	}
	n.events.add("test", "", fmt.Sprintf("injected %d spoofed UDP packets %s -> %s:%d, bypassing the local filter", count, src, dst, dport))
	writeJSON(w, map[string]any{"injected": count, "src": src.String(), "dst": dst.String()})
}

func ipChecksum(h []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(h); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(h[i:]))
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}
