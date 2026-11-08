package engine

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"golang.zx2c4.com/wireguard/device"
	wgtun "golang.zx2c4.com/wireguard/tun"

	"github.com/kittors/deneb/engine/dns"
	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/engine/names"
	"github.com/kittors/deneb/engine/netstack"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

// openKernel makes kernel mode's interface; tests swap in a fake one.
var openKernel = tun.Open

// ensureDataPathLocked makes WireGuard and the local stack for the device's
// address. One stack, one interface, whatever the number of scopes; it is
// only remade if the device's address itself changes (a new login).
func (e *Engine) ensureDataPathLocked(self netip.Addr, nm *proto.NetMap) error {
	if !self.IsValid() {
		return fmt.Errorf("the network map gives this device no IPv4 address")
	}
	if e.dev != nil && e.self == self {
		return nil
	}
	e.teardownDataPathLocked()

	var inner wgtun.Device
	switch e.opts.Mode {
	case apitype.ModeKernel:
		dns4 := firstV4Addr(nm.Network.DNS)
		k, err := openKernel(tun.Config{
			Self: self, Block: nm.Network.IPv4, DNS: dns4, Domain: nm.Network.Domain,
			MTU: e.opts.MTU, StateDir: e.opts.StateDir,
		}, e.logf)
		if err != nil {
			return fmt.Errorf("kernel mode: %w", err)
		}
		e.kern = k
		inner = k.Dev
		if dns4.IsValid() {
			e.dnsr = dns.New(dns4, e.logf)
			e.dnsr.OnRevived = e.dnsRevived
		}
	default:
		s, err := netstack.New([]netip.Addr{self}, e.opts.MTU, e.logf)
		if err != nil {
			return err
		}
		s.Forward(netstack.Forwarding{})
		e.stack = s
		inner = s
	}
	tw := filter.NewTUN(inner, e.f)
	tw.ConsumeIn = e.pinger.consume
	if e.dnsr != nil {
		tw.LocalOut = e.dnsr.Answer
	}
	tw.Start()
	dev := device.NewDevice(tw, e.ms.Bind(), &device.Logger{Verbosef: e.wgVerbose, Errorf: e.wgErrorf})
	// With roaming on, a packet from a new address would move the peer's
	// endpoint; magicsock picks paths itself, so roaming stays off.
	dev.DisableSomeRoamingForBrokenMobileSemantics()
	priv := e.nodePriv.Raw32()
	if err := dev.IpcSet("private_key=" + fmt.Sprintf("%x", priv[:]) + "\n"); err != nil {
		dev.Close()
		return err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return err
	}
	e.self, e.tunw, e.dev = self, tw, dev
	e.wgPeers = map[proto.NodePublic]wgPeer{}
	e.logf("[engine] data path up: %s mode, address %s", e.opts.Mode, self)
	if e.stack != nil && e.opts.SOCKS != "" && !e.socksOn {
		e.socksOn = true
		go e.serveSOCKS(e.opts.SOCKS)
	}
	e.startTestServicesLocked()
	return nil
}

func (e *Engine) teardownDataPathLocked() {
	if e.dev != nil {
		e.dev.Close() // closes the TUN wrapper, which closes the stack or utun
		e.dev = nil
	}
	if e.kern != nil {
		e.kern.Close(e.logf)
		e.kern = nil
	}
	e.stack, e.tunw, e.dnsr = nil, nil, nil
	e.self = netip.Addr{}
	e.wgPeers = map[proto.NodePublic]wgPeer{}
}

// pausedLocked is whether this device paused a scope. Its own record wins
// over the server's echo, so a pause takes effect without the server.
func (e *Engine) pausedLocked(s proto.Scope) bool {
	if slices.Contains(e.st.Paused, s.ID) {
		return true
	}
	return false
}

// applyNetmapLocked swaps in the three tables of a new network map: the
// filter policy, the names, the relays and peers of magicsock, and
// WireGuard's peers. Nothing else is touched.
func (e *Engine) applyNetmapLocked(nm *proto.NetMap, version uint64) {
	self := firstV4(nm.Self.Addrs)
	if err := e.ensureDataPathLocked(self, nm); err != nil {
		e.problems["datapath"] = apitype.Problem{Code: "datapath", Message: err.Error()}
		e.logf("[engine] cannot set up the data path: %v", err)
		e.nm = nm
		e.bumpVersionLocked(version)
		return
	}
	delete(e.problems, "datapath")

	// Forget local pauses of scopes this device is no longer in.
	kept := e.st.Paused[:0]
	for _, id := range e.st.Paused {
		if slices.ContainsFunc(nm.Scopes, func(s proto.Scope) bool { return s.ID == id }) {
			kept = append(kept, id)
		}
	}
	e.st.Paused = kept

	active := map[proto.ScopeID]bool{}
	paused := map[proto.ScopeID]bool{}
	pol := &filter.Policy{Self: self, Peers: map[netip.Addr]*filter.Peer{}, Open: map[string][]filter.Rule{}}
	for _, s := range nm.Scopes {
		if e.pausedLocked(s) {
			paused[s.ID] = true
			continue
		}
		active[s.ID] = true
		pol.Open[string(s.ID)] = rulesFor(s.Open)
	}
	selfEntry := names.Entry{Device: nm.Self.ID, Name: nm.Self.Name, OwnerName: nm.Self.OwnerName, FQDN: nm.Self.FQDN, Addr: self}
	for id := range active {
		selfEntry.Scopes = append(selfEntry.Scopes, id)
	}
	entries := []names.Entry{selfEntry}
	var msPeers []magicsock.PeerConfig
	want := map[proto.NodePublic]wgPeer{}
	for _, p := range nm.Peers {
		var shared []string
		var sharedIDs []proto.ScopeID
		inPaused := false
		for _, id := range p.Scopes {
			if active[id] {
				shared = append(shared, string(id))
				sharedIDs = append(sharedIDs, id)
			} else if paused[id] {
				inPaused = true
			}
		}
		addr := firstV4(p.Addrs)
		if (len(shared) == 0 && !inPaused) || !addr.IsValid() || p.NodeKey.IsZero() {
			continue
		}
		label := p.Name + "." + p.OwnerName
		// A peer of a LAN paused here stays known to WireGuard and
		// magicsock, keys and paths kept, so it is back the moment the LAN
		// resumes (M2: within 0.5 s) instead of after a new handshake. The
		// filter and the names leave it out, so nothing passes meanwhile;
		// with no traffic it soon idles and costs no pings.
		msPeers = append(msPeers, magicsock.PeerConfig{
			ID: p.ID, Label: label, NodeKey: p.NodeKey, DiscoKey: p.DiscoKey,
			Endpoints: p.Endpoints, HomeRelay: p.HomeRelay,
		})
		want[p.NodeKey] = wgPeer{addr: addr, extra: e.test.extraAllowed[p.ID]}
		if len(shared) == 0 {
			continue // every shared scope is paused here: invisible
		}
		pol.Peers[addr] = &filter.Peer{Label: label, Scopes: shared, RefusedByMe: p.RefusedByMe, RefusedByPeer: p.RefusedByPeer}
		entries = append(entries, names.Entry{Device: p.ID, Name: p.Name, OwnerName: p.OwnerName, FQDN: p.FQDN, Addr: addr, Scopes: sharedIDs})
	}
	for _, ps := range e.test.extraAllowed {
		pol.ExtraOut = append(pol.ExtraOut, ps...)
	}
	pol.Off = e.test.filterOff

	if cut := e.f.SetPolicy(pol); len(cut) > 0 {
		// Named with the map they were allowed by: the service is gone
		// from the new one.
		if uses := usesFor(e.nm, cut); len(uses) > 0 {
			go e.publish(apitype.Event{Type: apitype.EventCut, Cut: uses})
		}
	}
	tbl := names.NewTable(nm.Network.Domain, entries)
	e.names.Store(tbl)
	if e.dnsr != nil {
		e.dnsr.SetTable(tbl)
	}
	home := chooseHome(nm, active)
	if e.opts.HomeRelay != "" && slices.ContainsFunc(nm.Relays, func(r proto.Relay) bool { return r.ID == e.opts.HomeRelay }) {
		home = e.opts.HomeRelay
	}
	if home != e.homeRelay {
		e.homeRelay = home
		e.requestReport()
	}
	e.ms.SetRelays(nm.Relays, home)
	e.ms.SetPeers(msPeers)
	e.configureWGLocked(want)
	e.nm = nm
	e.bumpVersionLocked(version)
}

func (e *Engine) bumpVersionLocked(v uint64) {
	if v > e.version || v == 0 {
		if v != 0 {
			e.version = v
		}
		close(e.versionCh)
		e.versionCh = make(chan struct{})
	}
}

// chooseHome picks the relay peers reach this device through: the first
// relay of the first active scope. M4 picks by latency.
func chooseHome(nm *proto.NetMap, active map[proto.ScopeID]bool) proto.RelayID {
	known := map[proto.RelayID]bool{}
	for _, r := range nm.Relays {
		known[r.ID] = true
	}
	for _, s := range nm.Scopes {
		if !active[s.ID] {
			continue
		}
		for _, id := range s.Relays {
			if known[id] {
				return id
			}
		}
	}
	for _, s := range nm.Scopes {
		for _, id := range s.Relays {
			if known[id] {
				return id
			}
		}
	}
	if len(nm.Relays) > 0 {
		return nm.Relays[0].ID
	}
	return ""
}

// rulesFor expands services into port rules: a remote desktop may be both
// tcp/3389 and udp/3389, one switch for people, two rules for the filter.
func rulesFor(svcs []proto.Service) []filter.Rule {
	var out []filter.Rule
	for _, s := range svcs {
		for _, p := range s.Ports {
			pr := uint8(filter.ProtoTCP)
			if p.Proto == proto.UDP {
				pr = filter.ProtoUDP
			} else if p.Proto != proto.TCP {
				continue
			}
			out = append(out, filter.Rule{Proto: pr, First: p.First, Last: p.Last})
		}
	}
	return out
}

// configureWGLocked tells WireGuard the peers it may talk to, each allowed
// only its own address (layer 4: a peer cannot send with another's source).
func (e *Engine) configureWGLocked(want map[proto.NodePublic]wgPeer) {
	if e.dev == nil {
		return
	}
	var b strings.Builder
	for k := range e.wgPeers {
		if _, ok := want[k]; !ok {
			fmt.Fprintf(&b, "public_key=%x\nremove=true\n", k[:])
		}
	}
	for k, p := range want {
		old, ok := e.wgPeers[k]
		if ok && old.addr == p.addr && slices.Equal(old.extra, p.extra) {
			continue
		}
		fmt.Fprintf(&b, "public_key=%x\n", k[:])
		if ok {
			b.WriteString("replace_allowed_ips=true\n")
		} else {
			fmt.Fprintf(&b, "endpoint=%s\n", magicsock.EndpointString(k))
		}
		fmt.Fprintf(&b, "allowed_ip=%s/32\n", p.addr)
		for _, x := range p.extra {
			fmt.Fprintf(&b, "allowed_ip=%s\n", x)
		}
	}
	if b.Len() > 0 {
		if err := e.dev.IpcSet(b.String()); err != nil {
			e.logf("[wg] configure peers: %v", err)
		}
	}
	e.wgPeers = want
}

// rekey drops the WireGuard session with a peer whose engine restarted and
// starts a new handshake at once, so traffic this side starts does not sit
// on a session the peer no longer has.
func (e *Engine) rekey(k proto.NodePublic) {
	e.mu.Lock()
	dev := e.dev
	e.mu.Unlock()
	if dev == nil {
		return
	}
	if p := dev.LookupPeer(device.NoisePublicKey(k)); p != nil {
		p.ExpireCurrentKeypairs()
		p.SendHandshakeInitiation(false)
	}
}

func (e *Engine) wgVerbose(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	// Keep what the tests need: handshakes (path warm-up) and source-address
	// drops, the receiving half of scope isolation (layer 4).
	if strings.Contains(s, "disallowed") || strings.Contains(s, "Received handshake response") {
		e.logf("[wg] %s", s)
	}
}

func (e *Engine) wgErrorf(format string, args ...any) { e.logf("[wg] error: "+format, args...) }

// flushDNS empties the system's DNS caches; tests count calls instead.
var flushDNS = flushSystemDNSCaches

// dnsRevived: names this engine told the system do not exist are back (a
// device put back into a LAN). macOS would keep answering "no such name"
// from its caches for a while, past the 1 s SOA; flush them.
func (e *Engine) dnsRevived(revived []string) {
	e.dnsFlushMu.Lock()
	defer e.dnsFlushMu.Unlock()
	e.mu.Lock()
	off := e.test.noDNSFlush
	e.mu.Unlock()
	if off {
		e.logf("[dns] %s resolve again; system caches left alone (test)", strings.Join(revived, ", "))
		return
	}
	if err := flushDNS(); err != nil {
		e.logf("[dns] %s resolve again, but flushing the system's DNS caches failed: %v", strings.Join(revived, ", "), err)
		return
	}
	e.logf("[dns] %s resolve again; flushed the system's DNS caches", strings.Join(revived, ", "))
}
