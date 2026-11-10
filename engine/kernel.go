package engine

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

// KernelPlan is what a kernel-mode engine on stateDir would set up, from
// the network map it last had: the interface's settings and the peers'
// addresses (denebd preflight). It changes nothing.
func KernelPlan(stateDir string, mtu int) (tun.Config, []netip.Addr, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, "netmap.json"))
	if err != nil {
		return tun.Config{}, nil, errors.New("no network map yet: run the engine once (user-space mode is fine) and log in, then check again")
	}
	var nm proto.NetMap
	if err := json.Unmarshal(b, &nm); err != nil {
		return tun.Config{}, nil, err
	}
	if mtu == 0 {
		mtu = 1280
	}
	cfg := tun.Config{Self: firstV4(nm.Self.Addrs), Block: nm.Network.IPv4, DNS: firstV4Addr(nm.Network.DNS),
		Domain: nm.Network.Domain, MTU: mtu, StateDir: stateDir}
	var peers []netip.Addr
	for _, p := range nm.Peers {
		if a := firstV4(p.Addrs); a.IsValid() {
			peers = append(peers, a)
		}
	}
	return cfg, peers, nil
}

// UndoKernel removes what a kernel-mode engine that was killed left behind
// (the resolver file, the route), from the record in its state directory.
// It refuses while an engine runs on that state: that one still needs them.
func UndoKernel(stateDir string, logf func(string, ...any)) error {
	unlock, err := lockState(stateDir, 0)
	if err != nil {
		return err
	}
	defer unlock()
	tun.CleanupStale(stateDir, logf)
	return nil
}
