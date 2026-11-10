package engine

import (
	"cmp"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"tailscale.com/net/netmon"
	"tailscale.com/util/eventbus"

	"golang.zx2c4.com/wireguard/device"

	"github.com/kittors/deneb/engine/bind"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// M2: after the network changes (another Wi-Fi, Wi-Fi to Ethernet, a phone
// hotspot) or the machine wakes from sleep, every connection must be back
// within 30 s, and no line may show an error meanwhile. The engine notices
// both, then starts over in one go: the UDP socket on the interface that
// now carries the default route, the relays redialed, every peer shown
// "connecting" and probed at once, the control stream reconnected, the new
// endpoints reported.

// watchNetwork follows the network through the operating system's change
// notices. Only what Deneb's paths depend on counts: the default route's
// interface and its addresses. Other interfaces coming and going (Docker
// bridges, a proxy restarting its TUN, Deneb's own utun in kernel mode)
// would only make every line flash "connecting".
func (e *Engine) watchNetwork() {
	bus := eventbus.New()
	mon, err := netmon.New(bus, func(string, ...any) {}) // netmon's own log is chatty
	if err != nil {
		e.logf("[net] cannot watch the network (%v); only waking from sleep is noticed", err)
		bus.Close()
		return
	}
	last := netSignature()
	mon.RegisterChangeCallback(func(cd *netmon.ChangeDelta) {
		e.checkOnline() // every notice, not debounced like netChanged
		if cd.IsInitialState {
			return
		}
		if cd.TimeJumped() {
			go e.woke(cd.JumpDuration)
			return
		}
		if sig := netSignature(); sig != last {
			last = sig
			go e.netChanged("the network changed (" + sig + ")")
		}
	})
	mon.Start()
	e.mu.Lock()
	e.netmon, e.bus = mon, bus
	e.mu.Unlock()
	e.checkOnline()
}

// offline reports whether there is no network to send on: no default
// route, or its interface without an address. When the default route is a
// tunnel (Clash, Surge in TUN mode keep theirs when Wi-Fi goes), the
// interface the engine is bound to stands in for it.
func offline(bound string) bool {
	name, err := netmon.DefaultRouteInterface()
	if err != nil || name == "" {
		return true
	}
	if bind.IsTunnel(name) {
		if bound == "" {
			return false // nothing pinned: the tunnel's program has its own way out
		}
		name = bound
	}
	return !hasAddress(name)
}

// hasAddress: the interface is up with an IPv4 address or a global IPv6
// one (a link-local address alone reaches nothing beyond the link).
func hasAddress(name string) bool {
	ifc, err := net.InterfaceByName(name)
	if err != nil || ifc.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLoopback() {
			continue
		}
		if ipn.IP.To4() != nil || ipn.IP.IsGlobalUnicast() {
			return true
		}
	}
	return false
}

// localNetwork reports ProblemLocalNetwork while the system refuses
// packets to the local network (magicsock's OnLocalNetwork).
func (e *Engine) localNetwork(refused bool) {
	e.mu.Lock()
	if refused {
		e.problems[apitype.ProblemLocalNetwork] = apitype.Problem{Code: apitype.ProblemLocalNetwork,
			Message: "系统没让 Deneb 连本地网络，同一个局域网里的设备只能绕远路连。到「系统设置 → 隐私与安全性 → 本地网络」里打开 Deneb。"}
	} else {
		delete(e.problems, apitype.ProblemLocalNetwork)
	}
	e.mu.Unlock()
	e.publishState()
}

// checkOnline reports ProblemNoNetwork while there is no network, and
// takes it back when there is again.
func (e *Engine) checkOnline() {
	bound, _ := e.ms.BoundInterface()
	e.mu.Lock()
	check := e.test.offline
	e.mu.Unlock()
	if check == nil {
		check = offline
	}
	off := check(bound)
	e.mu.Lock()
	_, had := e.problems[apitype.ProblemNoNetwork]
	if off {
		e.problems[apitype.ProblemNoNetwork] = apitype.Problem{Code: apitype.ProblemNoNetwork, Message: "这台电脑没联网。"}
	} else {
		delete(e.problems, apitype.ProblemNoNetwork)
	}
	e.mu.Unlock()
	if off == had {
		return
	}
	if off {
		e.logf("[net] no network: no default route, or %s has no address", cmp.Or(bound, "its interface"))
	} else {
		e.logf("[net] the network is back")
	}
	e.publishState()
}

// netSignature is the default route's interface and its IPv4 addresses.
func netSignature() string {
	name, err := bind.DefaultInterface()
	if err != nil || name == "" {
		return "no default route"
	}
	sig := []string{name}
	if ifc, err := net.InterfaceByName(name); err == nil {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				sig = append(sig, ipn.IP.String())
			}
		}
	}
	slices.Sort(sig[1:])
	return strings.Join(sig, " ")
}

// woke handles the machine waking from sleep. The heartbeat notices it
// first (the wall clock jumped between two ticks), netmon often seconds
// later: one sleep, one reconnect (test Mac, 20:30: the second notice, 6 s
// on, reset every path and the relay again).
func (e *Engine) woke(d time.Duration) {
	e.netMu.Lock()
	again := !e.lastWake.IsZero() && time.Since(e.lastWake) < 30*time.Second
	if !again {
		e.lastWake = time.Now()
	}
	e.netMu.Unlock()
	if again {
		e.logf("[net] woke after %s: the same sleep, already handled", d.Round(time.Second))
		return
	}
	e.expireSessions()
	e.netChanged(fmt.Sprintf("woke after %s", d.Round(time.Second)))
}

// expireSessions drops every WireGuard session after a sleep. WireGuard
// times its sessions on the monotonic clock, which stands still while the
// machine sleeps: after ten minutes asleep it would go on sending with
// sessions the peers dropped long ago (they count real time), and start new
// ones only once nothing came back for 15 s (test Mac, 20:30: the paths
// showed direct at once, pings went unanswered for 13-16 s). The next packet
// to each peer now starts a handshake. A handshake WireGuard had just
// started goes too; magicsock keeps it as unanswered, so it is started
// over within a second (resendInits) rather than 5 s on.
func (e *Engine) expireSessions() {
	e.mu.Lock()
	dev := e.dev
	keys := make([]proto.NodePublic, 0, len(e.wgPeers))
	for k := range e.wgPeers {
		keys = append(keys, k)
	}
	e.mu.Unlock()
	if dev == nil {
		return
	}
	for _, k := range keys {
		if p := dev.LookupPeer(device.NoisePublicKey(k)); p != nil {
			p.ExpireCurrentKeypairs()
		}
	}
	e.logf("[wg] slept: the sessions with %d peers start over", len(keys))
}

// newHandshake starts a WireGuard handshake with k now, past WireGuard's
// limit of one initiation every 5 s (magicsock's OnStaleHandshake). The
// current session goes with it: a handshake was under way anyway.
func (e *Engine) newHandshake(k proto.NodePublic) {
	e.mu.Lock()
	dev := e.dev
	e.mu.Unlock()
	if dev == nil {
		return
	}
	if p := dev.LookupPeer(device.NoisePublicKey(k)); p != nil {
		e.logf("[wg] %x…: handshake unanswered after the path came back; starting a new one", k[:4])
		p.ExpireCurrentKeypairs()
		p.SendHandshakeInitiation(false)
	}
}

// netChanged reconnects everything. Several notices for one change (the
// route, then an address, then the clock) make one reconnect.
func (e *Engine) netChanged(reason string) {
	e.netMu.Lock()
	defer e.netMu.Unlock()
	if time.Since(e.lastNetChange) < 2*time.Second {
		return
	}
	e.lastNetChange = time.Now()
	if e.opts.BindInterface == "auto" {
		if name, err := bind.DefaultInterface(); err == nil && name != "" {
			e.mu.Lock()
			cur, cc := e.bindIf, e.cc
			e.mu.Unlock()
			if name != cur {
				if err := e.ms.Rebind(name); err != nil {
					e.logf("[net] cannot move the UDP socket to %s: %v", name, err)
				} else {
					bound, _ := e.ms.BoundInterface()
					e.mu.Lock()
					e.bindIf = bound
					e.mu.Unlock()
					if cc != nil {
						cc.SetBindInterface(bound)
					}
				}
			}
		}
	}
	e.ms.ResetPaths(reason)
	e.mu.Lock()
	cc := e.cc
	e.mu.Unlock()
	if cc != nil {
		// Connections made on the old network may be dead without knowing
		// it: the stream, reports and ops all go on new ones.
		cc.ResetConnections()
	}
	e.kickControl()
	e.requestReport()
	e.checkProxy() // a proxy turned on with the network change shows at once
}

// kickControl ends the control stream so the loop reconnects at once, on
// the new network, instead of noticing 45 s later that it went quiet.
func (e *Engine) kickControl() {
	e.mu.Lock()
	kick := e.streamKick
	e.mu.Unlock()
	if kick != nil {
		kick()
	}
	select { // and ends a back-off under way (controlLoop)
	case e.ctlWake <- struct{}{}:
	default:
	}
}
