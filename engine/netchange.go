package engine

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"tailscale.com/net/netmon"
	"tailscale.com/util/eventbus"

	"github.com/kittors/deneb/engine/bind"
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
		if cd.IsInitialState {
			return
		}
		if cd.TimeJumped() {
			go e.netChanged(fmt.Sprintf("woke after %s", cd.JumpDuration.Round(time.Second)))
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
	e.kickControl()
	e.requestReport()
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
}
