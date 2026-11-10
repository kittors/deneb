package engine

import (
	"net/netip"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

// TestKernelModeReportsProxyTakeover: in kernel mode, when the system would
// send to the LAN devices from another tunnel's address (Clash's TUN won
// the route), the engine reports problem "proxy" with the rule to add in
// that program, and takes it back once Deneb's interface carries them
// again. A physical interface winning is an address clash, not a proxy.
func TestKernelModeReportsProxyTakeover(t *testing.T) {
	if testing.Short() {
		t.Skip("runs two engines")
	}
	fake := newFakeTUN()
	oldOpen, oldSrc, oldIf, oldEx := openKernel, routeSource, interfaceOf, interfaceExists
	defer func() { openKernel, routeSource, interfaceOf, interfaceExists = oldOpen, oldSrc, oldIf, oldEx }()
	openKernel = func(tun.Config, func(string, ...any)) (*tun.Kernel, error) { return tun.Fake(fake, "fake0"), nil }
	interfaceExists = func(name string) bool { return name == "fake0" }
	var via netip.Addr // the address the system would send from
	routeSource = func(netip.Addr) (netip.Addr, error) { return via, nil }
	interfaceOf = func(a netip.Addr) string {
		switch a.String() {
		case "198.18.0.1":
			return "utun4" // Clash Verge's TUN
		case "192.168.1.13":
			return "en0"
		}
		return ""
	}

	relay, pool := testRelay(t)
	w := &world{relay: relay, members: map[proto.ScopeID][]*tnode{}, open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name string, mode apitype.Mode) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: mode, BindInterface: "none", RelayRoots: pool, Logf: quiet, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: "kittors", person: "p-kittors",
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac", apitype.ModeKernel)
	build := mk(2, "build", apitype.ModeUserspace)
	w.members["lan/studio"] = []*tnode{mac, build}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}
	// Up and connected before looking, as they would be: closing an engine
	// whose relay connection is still being set up waits for it.
	waitPath(t, build, mac, "direct", 5*time.Second)
	problem := func(n *tnode) *apitype.Problem {
		for _, p := range n.e.State().Engine.Problems {
			if p.Code == apitype.ProblemProxy {
				return &p
			}
		}
		return nil
	}

	via = mac.addr // Deneb's own interface
	mac.e.checkProxy()
	if p := problem(mac); p != nil {
		t.Fatalf("reported %+v with the route through Deneb's interface", p)
	}
	via = netip.MustParseAddr("198.18.0.1")
	mac.e.checkProxy()
	if p := problem(mac); p == nil || p.Rule != "IP-CIDR,10.77.0.0/16,DIRECT" {
		t.Fatalf("Clash's TUN took the route: problem %+v, want proxy with IP-CIDR,10.77.0.0/16,DIRECT", p)
	} else {
		t.Logf("reported: %s", p.Message)
	}
	via = mac.addr
	mac.e.checkProxy()
	if p := problem(mac); p != nil {
		t.Fatalf("still reported after the route came back: %+v", p)
	}
	via = netip.MustParseAddr("192.168.1.13")
	mac.e.checkProxy()
	if p := problem(mac); p != nil {
		t.Fatalf("a physical interface winning is no proxy: %+v", p)
	}
	via = netip.MustParseAddr("198.18.0.1")
	build.e.checkProxy() // user-space: its own sockets are pinned, nothing to say
	if p := problem(build); p != nil {
		t.Fatalf("user-space engine reported %+v", p)
	}
}
