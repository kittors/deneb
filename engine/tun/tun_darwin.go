package tun

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// Open creates one utun with the device's address and one route for the
// whole block, and writes /etc/resolver/<domain>. Nothing else on the system
// is touched: no default route, no DNS servers, no firewall, no forwarding
// (multi-lan-tech §6.8).
func Open(cfg Config, logf func(string, ...any)) (*Kernel, error) {
	if logf == nil {
		logf = log.Printf
	}
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("%w: it creates an interface and a route", ErrNoPermission)
	}
	if err := Preflight(cfg.Block, routeFor); err != nil {
		return nil, err
	}
	dev, err := wgtun.CreateTUN("utun", cfg.MTU)
	if err != nil {
		return nil, err
	}
	name, _ := dev.Name()
	u := &Undo{Interface: name, Route: cfg.Block}
	if cfg.DNS.IsValid() && cfg.Domain != "" {
		u.ResolverFile = filepath.Join(resolverDir, cfg.Domain)
		u.ResolverBody = ResolverBody(cfg.DNS)
	}
	// Write the record before changing anything, so a crash between two
	// steps is still undone next time.
	if err := saveUndo(cfg.StateDir, u); err != nil {
		dev.Close()
		return nil, err
	}
	k := &Kernel{Dev: dev, Name: name, cfg: cfg, undo: u}
	fail := func(err error) (*Kernel, error) {
		k.Close(logf)
		return nil, err
	}
	if err := run(logf, "ifconfig", name, "inet", cfg.Self.String(), cfg.Self.String(), "netmask", "255.255.255.255", "up"); err != nil {
		return fail(err)
	}
	if err := run(logf, "route", "-q", "-n", "add", "-inet", cfg.Block.String(), "-interface", name); err != nil {
		return fail(err)
	}
	if bits, iface, err := routeFor(lastAddr(cfg.Block)); err != nil || iface != name || bits != cfg.Block.Bits() {
		return fail(fmt.Errorf("route check failed: %s goes via %q /%d (%v)", lastAddr(cfg.Block), iface, bits, err))
	}
	if u.ResolverFile != "" {
		if err := os.MkdirAll("/etc/resolver", 0o755); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(u.ResolverFile); err == nil {
			return fail(fmt.Errorf("%s exists already; not overwriting it", u.ResolverFile))
		}
		if err := os.WriteFile(u.ResolverFile, []byte(u.ResolverBody), 0o644); err != nil {
			return fail(err)
		}
	}
	logf("[tun] %s up: %s, route %s, DNS for %s at %s", name, cfg.Self, cfg.Block, cfg.Domain, cfg.DNS)
	return k, nil
}

type platform struct{}

// SetPeers changes nothing on macOS: one route covers the whole block.
func (k *Kernel) SetPeers([]netip.Addr, func(string, ...any)) {}

// Plan says, changing nothing, what Open would do here with cfg, after the
// same checks; an error is what would stop it.
func Plan(cfg Config, peers []netip.Addr) ([]string, error) {
	if err := Preflight(cfg.Block, routeFor); err != nil {
		return nil, err
	}
	var steps []string
	if os.Geteuid() != 0 {
		steps = append(steps, "不是 root：要用 sudo 运行，否则建不了网卡")
	}
	steps = append(steps,
		fmt.Sprintf("建一张 utun 网卡，地址 %s", cfg.Self),
		fmt.Sprintf("加一条路由：%s 经这张网卡（%d 个对端都在里面）", cfg.Block, len(peers)))
	if cfg.DNS.IsValid() && cfg.Domain != "" {
		steps = append(steps, fmt.Sprintf("写 /etc/resolver/%s：只有 %s 下的名字问 %s", cfg.Domain, cfg.Domain, cfg.DNS))
	}
	steps = append(steps, "不改默认路由、别的 DNS、防火墙和转发设置；停下时全部撤掉")
	return steps, nil
}

// Close removes the resolver file and the route, then the interface.
func (k *Kernel) Close(logf func(string, ...any)) {
	if logf == nil {
		logf = log.Printf
	}
	undo(k.undo, logf)
	k.Dev.Close()
	if k.cfg.StateDir != "" {
		os.Remove(filepath.Join(k.cfg.StateDir, undoFile))
	}
	logf("[tun] removed %s, route %s and the resolver file", k.Name, k.cfg.Block)
}

func undo(u *Undo, logf func(string, ...any)) {
	removeResolver(u, logf)
	if u.Route.IsValid() && u.Interface != "" {
		if _, err := net.InterfaceByName(u.Interface); err == nil {
			run(logf, "route", "-q", "-n", "delete", "-inet", u.Route.String(), "-interface", u.Interface)
		}
	}
}

// Preflight checks, changing nothing, that the block is free here: no
// interface address inside it and no route more specific than the
// catch-all ones (the default route, a proxy's /1../8 routes).
func Preflight(block netip.Prefix, lookup func(netip.Addr) (int, string, error)) error {
	if err := CheckBlock(block); err != nil {
		return err
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && block.Contains(p.Addr()) {
			return fmt.Errorf("%w: an interface already has %s inside %s", ErrConflict, p.Addr(), block)
		}
	}
	for _, probe := range []netip.Addr{block.Masked().Addr().Next(), lastAddr(block)} {
		bits, iface, err := lookup(probe)
		if err != nil {
			return err
		}
		if bits >= block.Bits() {
			return fmt.Errorf("%w: an existing /%d route via %s already covers %s", ErrConflict, bits, iface, probe)
		}
	}
	return nil
}

func run(logf func(string, ...any), name string, args ...string) error {
	logf("[tun] exec: %s %s", name, strings.Join(args, " "))
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// routeFor asks the kernel which route it would use for ip (read-only).
func routeFor(ip netip.Addr) (int, string, error) {
	out, err := exec.Command("route", "-n", "get", ip.String()).CombinedOutput()
	if err != nil {
		return 0, "", fmt.Errorf("route get %s: %v", ip, err)
	}
	bits, iface := ParseRouteGet(string(out))
	return bits, iface, nil
}
