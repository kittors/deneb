package tun

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"
	wgtun "golang.zx2c4.com/wireguard/tun"
)

// routeProto marks the routes this engine adds ("proto 68" in `ip route`),
// to tell them from everyone else's.
const routeProto = 68

// platform is the Linux side of a Kernel: its routes, one /32 per peer and
// one for the DNS address.
type platform struct {
	mu     sync.Mutex
	index  uint32
	routes map[netip.Addr]bool
}

// Open creates a tun interface with the device's address and a /32 route to
// the DNS address (SetPeers adds one per peer), and gives systemd-resolved
// the domain for that interface only: a routing domain, never a default
// route. Nothing else on the system changes: no default route, no DNS for
// other names, no firewall rule, no forwarding. The interface goes away
// with the engine's process (a tun is removed when its last file closes),
// and its routes and resolver settings with it.
func Open(cfg Config, logf func(string, ...any)) (*Kernel, error) {
	if logf == nil {
		logf = log.Printf
	}
	if err := Preflight(cfg.Block, nil); err != nil {
		return nil, err
	}
	resolved := cfg.DNS.IsValid() && cfg.Domain != ""
	if resolved && !resolvedActive() {
		return nil, ErrNoResolver
	}
	var dev wgtun.Device
	var err error
	for i := 0; i < 10 && dev == nil; i++ {
		name := fmt.Sprintf("deneb%d", i)
		if _, e := net.InterfaceByName(name); e == nil {
			continue // another engine's
		}
		dev, err = wgtun.CreateTUN(name, cfg.MTU)
	}
	if dev == nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("%w (or CAP_NET_ADMIN): %v", ErrNoPermission, err)
		}
		return nil, fmt.Errorf("create the interface: %w", err)
	}
	name, _ := dev.Name()
	u := &Undo{Interface: name, Route: cfg.Block, Resolved: resolved}
	// Written before the first change, like on macOS; here the kernel
	// undoes it all when the process dies, so the record is only for show.
	if err := saveUndo(cfg.StateDir, u); err != nil {
		dev.Close()
		return nil, err
	}
	k := &Kernel{Dev: dev, Name: name, cfg: cfg, undo: u}
	k.plat.routes = map[netip.Addr]bool{}
	fail := func(err error) (*Kernel, error) {
		k.Close(logf)
		return nil, err
	}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return fail(err)
	}
	k.plat.index = uint32(ifi.Index)
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	self := net.IP(cfg.Self.AsSlice())
	if err := conn.Address.New(&rtnetlink.AddressMessage{
		Family: unix.AF_INET, PrefixLength: 32, Scope: unix.RT_SCOPE_UNIVERSE, Index: k.plat.index,
		Attributes: &rtnetlink.AddressAttributes{Address: self, Local: self},
	}); err != nil {
		return fail(fmt.Errorf("address %s on %s: %w", cfg.Self, name, err))
	}
	if err := conn.Link.Set(&rtnetlink.LinkMessage{Family: unix.AF_UNSPEC, Index: k.plat.index, Flags: unix.IFF_UP, Change: unix.IFF_UP}); err != nil {
		return fail(fmt.Errorf("bring %s up: %w", name, err))
	}
	if cfg.DNS.IsValid() {
		if err := k.addRoute(conn, cfg.DNS); err != nil {
			return fail(err)
		}
	}
	if resolved {
		for _, args := range [][]string{{"dns", name, cfg.DNS.String()}, {"domain", name, "~" + cfg.Domain}} {
			if err := run(logf, "resolvectl", args...); err != nil {
				return fail(err)
			}
		}
		// Never asked for other names (a routing domain alone already says
		// so), nor used for LLMNR or mDNS: nobody answers multicast here.
		// Versions without one of these ignore it.
		for _, args := range [][]string{{"default-route", name, "false"}, {"llmnr", name, "no"}, {"mdns", name, "no"}} {
			run(logf, "resolvectl", args...)
		}
	}
	logf("[tun] %s up: %s, a /32 route per peer, DNS for %s at %s", name, cfg.Self, cfg.Domain, cfg.DNS)
	return k, nil
}

// SetPeers keeps one /32 route through the interface per peer address, and
// the DNS address's: added for new peers, removed for those gone.
func (k *Kernel) SetPeers(addrs []netip.Addr, logf func(string, ...any)) {
	if k.fake {
		return
	}
	if logf == nil {
		logf = log.Printf
	}
	k.plat.mu.Lock()
	defer k.plat.mu.Unlock()
	want := map[netip.Addr]bool{}
	for _, a := range addrs {
		if a.Is4() && k.cfg.Block.Contains(a) {
			want[a] = true
		}
	}
	if k.cfg.DNS.IsValid() {
		want[k.cfg.DNS] = true
	}
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		logf("[tun] routes: %v", err)
		return
	}
	defer conn.Close()
	added, removed := 0, 0
	for a := range want {
		if !k.plat.routes[a] {
			if err := k.addRoute(conn, a); err != nil {
				logf("[tun] route %s/32: %v", a, err)
				continue
			}
			added++
		}
	}
	for a := range k.plat.routes {
		if !want[a] {
			err := conn.Route.Delete(k.route(a))
			if err != nil && !errors.Is(err, unix.ESRCH) {
				logf("[tun] remove route %s/32: %v", a, err)
				continue
			}
			delete(k.plat.routes, a)
			removed++
		}
	}
	if added+removed > 0 {
		logf("[tun] routes through %s: %d added, %d removed, %d in all", k.Name, added, removed, len(k.plat.routes))
	}
}

func (k *Kernel) route(a netip.Addr) *rtnetlink.RouteMessage {
	return &rtnetlink.RouteMessage{
		Family: unix.AF_INET, Table: unix.RT_TABLE_MAIN, Protocol: routeProto, Scope: unix.RT_SCOPE_LINK,
		Type: unix.RTN_UNICAST, DstLength: 32,
		Attributes: rtnetlink.RouteAttributes{Dst: net.IP(a.AsSlice()), OutIface: k.plat.index},
	}
}

func (k *Kernel) addRoute(conn *rtnetlink.Conn, a netip.Addr) error {
	if err := conn.Route.Replace(k.route(a)); err != nil {
		return fmt.Errorf("route %s/32 via %s: %w", a, k.Name, err)
	}
	k.plat.routes[a] = true
	return nil
}

// Close gives systemd-resolved's settings back and closes the interface,
// which takes its address and routes with it.
func (k *Kernel) Close(logf func(string, ...any)) {
	if logf == nil {
		logf = log.Printf
	}
	if k.undo != nil && k.undo.Resolved {
		run(logf, "resolvectl", "revert", k.Name)
	}
	k.Dev.Close()
	if k.cfg.StateDir != "" {
		os.Remove(filepath.Join(k.cfg.StateDir, undoFile))
	}
	logf("[tun] removed %s, its routes and its resolver settings", k.Name)
}

// undo has nothing to do after a crash: the kernel removed the interface
// with the engine's process, and systemd-resolved forgot its settings with
// the interface. (An interface by that name now is another engine's.)
func undo(*Undo, func(string, ...any)) {}

// Preflight checks, changing nothing, that the block is free here: no
// interface address inside it, and no route for part of it (a Docker
// network, a VPN's) that a /32 per peer would cut into or that would take
// a peer's traffic. Routes covering more than the block (the default
// route, a 10.0.0.0/8) are fine: a /32 is more specific.
func Preflight(block netip.Prefix, _ func(netip.Addr) (int, string, error)) error {
	if err := CheckBlock(block); err != nil {
		return err
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && block.Contains(p.Addr()) {
			return fmt.Errorf("%w: an interface already has %s inside %s", ErrConflict, p.Addr(), block)
		}
	}
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("read the routes: %w", err)
	}
	defer conn.Close()
	routes, err := conn.Route.List()
	if err != nil {
		return fmt.Errorf("read the routes: %w", err)
	}
	for _, r := range routes {
		if r.Family != unix.AF_INET || r.Protocol == routeProto || r.Table == unix.RT_TABLE_LOCAL || r.Attributes.Dst == nil {
			continue
		}
		dst, ok := netip.AddrFromSlice(r.Attributes.Dst.To4())
		if !ok {
			continue
		}
		p := netip.PrefixFrom(dst, int(r.DstLength))
		if p.Bits() >= block.Bits() && p.Overlaps(block) {
			return fmt.Errorf("%w: an existing route for %s is inside %s", ErrConflict, p, block)
		}
	}
	return nil
}

// resolvedActive: systemd-resolved runs, and programs ask it for names:
// /etc/resolv.conf points at its stub, or the hosts line in nsswitch.conf
// goes through it. Otherwise a routing domain given to it changes nothing.
func resolvedActive() bool {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return false
	}
	if _, err := os.Stat("/run/systemd/resolve"); err != nil {
		return false
	}
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil && strings.Contains(string(b), "127.0.0.53") {
		return true
	}
	b, err := os.ReadFile("/etc/nsswitch.conf")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "hosts:" && strings.Contains(line, "resolve") {
			return true
		}
	}
	return false
}

func run(logf func(string, ...any), name string, args ...string) error {
	logf("[tun] exec: %s %s", name, strings.Join(args, " "))
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Plan says, changing nothing, what Open would do here with cfg and these
// peers, after the same checks; an error is what would stop it.
func Plan(cfg Config, peers []netip.Addr) ([]string, error) {
	var steps []string
	if err := Preflight(cfg.Block, nil); err != nil {
		return nil, err
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return nil, fmt.Errorf("no /dev/net/tun: %w", err)
	}
	if os.Geteuid() != 0 {
		steps = append(steps, "不是 root：要以 root 运行（或给 CAP_NET_ADMIN），否则建不了网卡")
	}
	if cfg.DNS.IsValid() && cfg.Domain != "" && !resolvedActive() {
		steps = append(steps, fmt.Sprintf("系统查名字不经过 systemd-resolved：%s 下的名字交不给引擎，会改用用户态（SOCKS5），不建网卡、不改路由", cfg.Domain))
		return steps, nil
	}
	name := "deneb0"
	for i := 0; i < 10; i++ {
		name = fmt.Sprintf("deneb%d", i)
		if _, err := net.InterfaceByName(name); err != nil {
			break
		}
	}
	steps = append(steps,
		fmt.Sprintf("建一张 tun 网卡 %s，地址 %s/32，MTU %d；引擎退出时网卡连同它的路由、DNS 设置一起消失", name, cfg.Self, cfg.MTU),
		fmt.Sprintf("经 %s 加 %d 条 /32 路由（每个对端一条，DNS 地址 %s 一条），标记 proto %d；%s 里别的地址不经过它", name, len(peers)+1, cfg.DNS, routeProto, cfg.Block))
	if cfg.DNS.IsValid() && cfg.Domain != "" {
		steps = append(steps, fmt.Sprintf("systemd-resolved：%s 的 DNS 设为 %s，只管 ~%s，不当默认（resolvectl dns / domain / default-route）；别的名字照旧", name, cfg.DNS, cfg.Domain))
	}
	if b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err == nil && strings.TrimSpace(string(b)) == "1" {
		steps = append(steps, "这台机器开着 IP 转发（装了 Docker 就会）：别处转进网卡的包，引擎的过滤在出口拦掉（transit-out），不当路由器")
	}
	steps = append(steps, "不改默认路由、别的 DNS、防火墙和转发设置")
	return steps, nil
}
