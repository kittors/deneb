package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type node struct {
	cfg    *Config
	self   netip.Addr
	events *eventLog
	ms     *magicSock
	ft     *filterTUN
	onet   overlayNet
	dev    *device.Device

	resolverAddr netip.Addr // kernel-TUN mode: DNS for deneb.internal, inside the block
	stopOnce     sync.Once
	undo         func() // kernel-TUN mode: removes the route and the utun

	mu       sync.Mutex
	active   map[string]bool // lan id -> joined and not paused
	peers    []*nodePeer
	extraOut map[string][]netip.Prefix // attack tests: peer label -> extra allowed IPs
	noFilter bool
}

type nodePeer struct {
	conf PeerConf
	key  [32]byte
	addr netip.Addr
	ps   *peerState
	inWG bool
}

func cmdNode(args []string) {
	fs := flag.NewFlagSet("node", flag.ExitOnError)
	cfgPath := fs.String("config", "", "device config from `gen`")
	apiAddr := fs.String("api", "127.0.0.1:47777", "local control API (HTTP)")
	socksAddr := fs.String("socks", "", "local SOCKS5 proxy for apps on this machine, e.g. 127.0.0.1:41080")
	bindIf := fs.String("bind-if", "", "pin underlay sockets to this interface (macOS: IP_BOUND_IF)")
	hb := fs.Duration("heartbeat", 3*time.Second, "direct-path heartbeat (Tailscale: 3s)")
	trust := fs.Duration("trust", 6500*time.Millisecond, "how long a direct path is trusted without a pong (Tailscale: 6.5s)")
	pause := fs.String("paused", "", "comma-separated LAN ids to start paused")
	noFilter := fs.Bool("insecure-no-filter", false, "attack tests only: disable the LAN filter")
	kernelTUN := fs.Bool("tun", false, "macOS, root: use a real utun with one route for the address block, instead of the user-space stack")
	preflight := fs.Bool("preflight", false, "with -tun: only check that the address block is unused here, change nothing, and exit")
	var attack multiFlag
	fs.Var(&attack, "attack-route", "attack tests only: peerLabel=cidr, route cidr via that peer (repeatable)")
	fs.Parse(args)
	if *cfgPath == "" {
		log.Fatal("-config is required")
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	priv, err := parseHexKey(cfg.PrivateKey)
	if err != nil {
		log.Fatal(err)
	}
	self, err := netip.ParseAddr(cfg.Address)
	if err != nil {
		log.Fatal(err)
	}
	block, err := netip.ParsePrefix(cfg.AddressBlock)
	if err != nil || !block.Contains(self) {
		log.Fatalf("config addressBlock %q must contain %s", cfg.AddressBlock, self)
	}
	if *preflight {
		if err := preflightKernelTUN(block); err != nil {
			log.Fatalf("[tun] preflight failed, nothing changed: %v", err)
		}
		log.Printf("[tun] preflight ok for %s; nothing was changed", block)
		return
	}

	n := &node{cfg: cfg, self: self, events: newEventLog(), active: map[string]bool{}, extraOut: map[string][]netip.Prefix{}, noFilter: *noFilter}
	for _, l := range cfg.LANs {
		n.active[l.ID] = true
	}
	for _, id := range strings.Split(*pause, ",") {
		if id != "" {
			n.active[id] = false
		}
	}
	for _, a := range attack {
		label, cidr, ok := strings.Cut(a, "=")
		pfx, err := netip.ParsePrefix(cidr)
		if !ok || err != nil {
			log.Fatalf("bad -attack-route %q", a)
		}
		n.extraOut[label] = append(n.extraOut[label], pfx)
	}

	n.ms, err = newMagicSock(priv, cfg.Relay, sockOpts{port: cfg.ListenPort, bindIf: *bindIf, heartbeat: *hb, trust: *trust, overlay: block}, n.events)
	if err != nil {
		log.Fatal(err)
	}
	for _, pc := range cfg.Peers {
		k, err := parseHexKey(pc.PublicKey)
		if err != nil {
			log.Fatal(err)
		}
		a, err := netip.ParseAddr(pc.Address)
		if err != nil {
			log.Fatal(err)
		}
		var hints []netip.AddrPort
		for _, e := range pc.Endpoints {
			if ap, err := netip.ParseAddrPort(e); err == nil {
				hints = append(hints, ap)
			}
		}
		np := &nodePeer{conf: pc, key: k, addr: a}
		np.ps = n.ms.addPeer(k, pc.Label(), hints)
		n.peers = append(n.peers, np)
	}

	// One interface (or one user-space stack) with one address, whatever the
	// number of LANs.
	if *kernelTUN {
		tdev, undo, err := setupKernelTUN(self, block, 1280)
		if err != nil {
			log.Fatal(err)
		}
		n.undo = undo
		n.onet = kernelNet{self: self}
		n.ft = newFilterTUN(tdev)
		n.resolverAddr = nthAddr(block, 53)
		n.ft.local = n.answerDNS
	} else {
		tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{self}, nil, 1280)
		if err != nil {
			log.Fatal(err)
		}
		n.onet = userspaceNet{tnet: tnet, self: self}
		n.ft = newFilterTUN(tdev)
	}
	n.dev = device.NewDevice(n.ft, &wgBind{ms: n.ms}, &device.Logger{Verbosef: wgVerbose, Errorf: wgErrorf})
	n.dev.DisableSomeRoamingForBrokenMobileSemantics()
	if err := n.dev.IpcSet("private_key=" + hex.EncodeToString(priv[:]) + "\n"); err != nil {
		log.Fatal(err)
	}
	n.applyNetmap("startup")
	if err := n.dev.Up(); err != nil {
		log.Fatal(err)
	}
	n.ms.start()
	n.startServices()
	go n.serveAPI(*apiAddr)
	if *socksAddr != "" {
		go n.serveSOCKS(*socksAddr)
	}
	var lans []string
	for _, l := range cfg.LANs {
		state := "on"
		if !n.active[l.ID] {
			state = "paused"
		}
		lans = append(lans, fmt.Sprintf("%s(%s,%s)", l.ID, l.Name, state))
	}
	n.events.add("start", "", fmt.Sprintf("%s.%s addr=%s udp=%d lans=%s bind-if=%q", cfg.Name, cfg.Owner, self, n.ms.port, strings.Join(lans, " "), *bindIf))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	<-sig
	n.stop("signal")
}

// stop tears everything down exactly once: WireGuard first, then (kernel
// mode) the route and the utun, so nothing is left on the machine.
func (n *node) stop(why string) {
	n.stopOnce.Do(func() {
		log.Printf("stopping (%s)", why)
		n.dev.Close()
		if n.undo != nil {
			n.undo()
		}
	})
	os.Exit(0)
}

func nthAddr(p netip.Prefix, n int) netip.Addr {
	a := p.Masked().Addr()
	for i := 0; i < n; i++ {
		a = a.Next()
	}
	return a
}

// applyNetmap is what a control-plane push does: recompute which peers are
// reachable (they share at least one active LAN), then update WireGuard, the
// filter and the name table. It never touches the socket, the relay
// connection, the key or the local address, so other LANs are not disturbed.
func (n *node) applyNetmap(why string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	pol := &policy{self: n.self, peers: map[netip.Addr]*peerPol{}, open: map[string][]svc{}, noFilter: n.noFilter}
	for _, l := range n.cfg.LANs {
		if !n.active[l.ID] {
			continue
		}
		for _, s := range l.Open {
			sv, err := parseService(s)
			if err != nil {
				log.Fatal(err)
			}
			pol.open[l.ID] = append(pol.open[l.ID], sv...)
		}
	}
	var uapi strings.Builder
	for _, p := range n.peers {
		var lans []string
		for _, id := range p.conf.LANs {
			if n.active[id] {
				lans = append(lans, id)
			}
		}
		want := len(lans) > 0
		if want {
			pol.peers[p.addr] = &peerPol{label: p.conf.Label(), lans: lans}
			pol.extraOut = append(pol.extraOut, n.extraOut[p.conf.Label()]...)
		}
		switch {
		case want && !p.inWG:
			fmt.Fprintf(&uapi, "public_key=%s\nendpoint=%s\nallowed_ip=%s/32\n", hex.EncodeToString(p.key[:]), hex.EncodeToString(p.key[:]), p.addr)
			for _, extra := range n.extraOut[p.conf.Label()] {
				fmt.Fprintf(&uapi, "allowed_ip=%s\n", extra)
			}
			p.inWG = true
			n.ms.setActive(p.ps, true)
		case !want && p.inWG:
			fmt.Fprintf(&uapi, "public_key=%s\nremove=true\n", hex.EncodeToString(p.key[:]))
			p.inWG = false
			n.ms.setActive(p.ps, false)
			n.ft.forgetPeer(p.addr)
		}
	}
	n.ft.pol.Store(pol)
	if uapi.Len() > 0 {
		if err := n.dev.IpcSet(uapi.String()); err != nil {
			log.Printf("IpcSet: %v", err)
		}
	}
	var on []string
	for id, a := range n.active {
		if a {
			on = append(on, id)
		}
	}
	slices.Sort(on)
	n.events.add("netmap", "", fmt.Sprintf("%s: active lans=%v, reachable peers=%d", why, on, len(pol.peers)))
}

func (n *node) setLAN(id string, on bool) error {
	n.mu.Lock()
	if _, ok := n.active[id]; !ok {
		n.mu.Unlock()
		return fmt.Errorf("not a member of lan %q", id)
	}
	n.active[id] = on
	n.mu.Unlock()
	verb := "resume"
	if !on {
		verb = "pause"
	}
	n.applyNetmap(verb + " " + id)
	return nil
}

func (n *node) peerByLabel(label string) *nodePeer {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range n.peers {
		if p.conf.Label() == label {
			return p
		}
	}
	return nil
}

func wgVerbose(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	// Keep only what the experiments need: handshakes (path warm-up) and
	// cryptokey-routing drops (the receiver-side half of LAN isolation).
	if strings.Contains(s, "disallowed") || strings.Contains(s, "Received handshake response") {
		log.Printf("[wg] %s", s)
	}
}

func wgErrorf(format string, args ...any) { log.Printf("[wg] error: "+format, args...) }

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// eventLog keeps timestamped state changes so measurements can be lined up
// with path switches after the fact.
type eventLog struct {
	mu  sync.Mutex
	evs []event
}

type event struct {
	T      time.Time `json:"t"`
	Kind   string    `json:"kind"`
	Peer   string    `json:"peer,omitempty"`
	Detail string    `json:"detail"`
}

func newEventLog() *eventLog { return &eventLog{} }

func (e *eventLog) add(kind, peer, detail string) {
	ev := event{T: time.Now(), Kind: kind, Peer: peer, Detail: detail}
	e.mu.Lock()
	e.evs = append(e.evs, ev)
	if len(e.evs) > 5000 {
		e.evs = e.evs[1000:]
	}
	e.mu.Unlock()
	if peer != "" {
		log.Printf("[%s] %s: %s", kind, peer, detail)
	} else {
		log.Printf("[%s] %s", kind, detail)
	}
}

func (e *eventLog) since(t time.Time) []event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []event
	for _, ev := range e.evs {
		if !ev.T.Before(t) {
			out = append(out, ev)
		}
	}
	return out
}
