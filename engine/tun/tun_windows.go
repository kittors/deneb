package tun

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	wgtun "golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// adapterName is the Wintun adapter's name, as `Get-NetAdapter` shows it.
const adapterName = "Deneb"

// NRPT: the DNS client's name resolution policy, one registry key per rule
// (what Add-DnsClientNrptRule writes). Deneb's has a fixed name, so it is
// found again after a crash. Rules under the group policy key, if there
// are any, override all local ones.
const (
	nrptLocal  = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	nrptPolicy = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`
	nrptRule   = nrptRuleName
)

// platform is the Windows side of a Kernel: its routes, one /32 per peer
// and one for the DNS address.
type platform struct {
	mu     sync.Mutex
	luid   winipcfg.LUID
	routes map[netip.Addr]bool
}

// Open creates the Wintun adapter "Deneb" with the device's address, a /32
// route to the DNS address (SetPeers adds one per peer), and an NRPT rule
// sending the domain, and only it, to the engine. Nothing else changes: no
// default route, no DNS servers on any adapter, no firewall rule. Close
// removes the rule and the adapter, which takes its routes with it; after
// a crash, the next start does (CleanupStale).
func Open(cfg Config, logf func(string, ...any)) (*Kernel, error) {
	if logf == nil {
		logf = log.Printf
	}
	if err := Preflight(cfg.Block, nil); err != nil {
		return nil, err
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, fmt.Errorf("%w: run as administrator, or as the Deneb service", ErrNoPermission)
	}
	if err := wintunHere(); err != nil {
		return nil, err
	}
	u := &Undo{Interface: adapterName, Route: cfg.Block}
	if cfg.DNS.IsValid() && cfg.Domain != "" {
		u.NRPT = nrptRule
	}
	// Written before the first change, so a crash is undone next time.
	if err := saveUndo(cfg.StateDir, u); err != nil {
		return nil, err
	}
	wgtun.WintunTunnelType = adapterName // the adapter's description: not "WireGuard", to tell it apart
	dev, err := wgtun.CreateTUN(adapterName, cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("create the Wintun adapter: %w", err)
	}
	nt, ok := dev.(*wgtun.NativeTun)
	if !ok {
		dev.Close()
		return nil, errors.New("not a Wintun device")
	}
	k := &Kernel{Dev: dev, Name: adapterName, cfg: cfg, undo: u}
	k.plat.luid, k.plat.routes = winipcfg.LUID(nt.LUID()), map[netip.Addr]bool{}
	fail := func(err error) (*Kernel, error) {
		k.Close(logf)
		return nil, err
	}
	if err := k.plat.luid.SetIPAddresses([]netip.Prefix{netip.PrefixFrom(cfg.Self, 32)}); err != nil {
		return fail(fmt.Errorf("address %s: %w", cfg.Self, err))
	}
	if err := setMTU(k.plat.luid, cfg.MTU); err != nil {
		return fail(err)
	}
	// No DNS servers on the adapter: names reach the engine through the
	// NRPT rule only, for the domain only.
	k.plat.luid.SetDNS(windows.AF_INET, nil, nil)
	if cfg.DNS.IsValid() {
		if err := k.addRoute(cfg.DNS); err != nil {
			return fail(err)
		}
	}
	if u.NRPT != "" {
		if err := writeNRPT(u.NRPT, cfg.Domain, cfg.DNS); err != nil {
			return fail(fmt.Errorf("NRPT rule for .%s: %w", cfg.Domain, err))
		}
		flushDNS()
	}
	logf("[tun] %s up: %s, a /32 route per peer, NRPT .%s -> %s", adapterName, cfg.Self, cfg.Domain, cfg.DNS)
	return k, nil
}

// SetPeers keeps one /32 route through the adapter per peer address, and
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
	added, removed := 0, 0
	for a := range want {
		if !k.plat.routes[a] {
			if err := k.addRoute(a); err != nil {
				logf("[tun] %v", err)
				continue
			}
			added++
		}
	}
	for a := range k.plat.routes {
		if !want[a] {
			if err := k.plat.luid.DeleteRoute(netip.PrefixFrom(a, 32), netip.IPv4Unspecified()); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				logf("[tun] remove route %s/32: %v", a, err)
				continue
			}
			delete(k.plat.routes, a)
			removed++
		}
	}
	if added+removed > 0 {
		logf("[tun] routes through %s: %d added, %d removed, %d in all", adapterName, added, removed, len(k.plat.routes))
	}
}

func (k *Kernel) addRoute(a netip.Addr) error {
	err := k.plat.luid.AddRoute(netip.PrefixFrom(a, 32), netip.IPv4Unspecified(), 0)
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
		return fmt.Errorf("route %s/32 via %s: %w", a, adapterName, err)
	}
	k.plat.routes[a] = true
	return nil
}

// Close removes the NRPT rule, then the adapter with its routes.
func (k *Kernel) Close(logf func(string, ...any)) {
	if logf == nil {
		logf = log.Printf
	}
	if k.undo != nil && k.undo.NRPT != "" {
		removeNRPT(k.undo.NRPT, k.cfg.Domain, logf)
		flushDNS()
	}
	k.Dev.Close()
	if k.cfg.StateDir != "" {
		os.Remove(filepath.Join(k.cfg.StateDir, undoFile))
	}
	logf("[tun] removed %s, its routes and the NRPT rule", adapterName)
}

// undo removes what a crashed engine left: its NRPT rule. The adapter went
// with the process that created it (Wintun 0.14 ties an adapter to its
// creator's handle), its routes with it.
func undo(u *Undo, logf func(string, ...any)) {
	if u.NRPT != "" {
		removeNRPT(u.NRPT, "", logf)
		flushDNS()
	}
}

// Preflight checks, changing nothing, that the block is free here: no
// adapter address inside it, and no route for part of it that a /32 per
// peer would cut into or that would take a peer's traffic. Routes covering
// more than the block (the default route) are fine.
func Preflight(block netip.Prefix, _ func(netip.Addr) (int, string, error)) error {
	if err := CheckBlock(block); err != nil {
		return err
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && block.Contains(p.Addr()) {
			return fmt.Errorf("%w: an adapter already has %s inside %s", ErrConflict, p.Addr(), block)
		}
	}
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return fmt.Errorf("read the routes: %w", err)
	}
	ours, _ := adapterByName(adapterNames(), adapterName) // another engine's routes are no conflict
	for _, r := range rows {
		p := r.DestinationPrefix.Prefix()
		if r.InterfaceLUID == ours || !p.Addr().Is4() || p.Bits() < block.Bits() || !p.Overlaps(block) {
			continue
		}
		return fmt.Errorf("%w: an existing route for %s is inside %s", ErrConflict, p, block)
	}
	return nil
}

// Plan says, changing nothing, what Open would do here with cfg and these
// peers, after the same checks; an error is what would stop it.
func Plan(cfg Config, peers []netip.Addr) ([]string, error) {
	var steps []string
	if err := CheckBlock(cfg.Block); err != nil {
		return nil, err
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && cfg.Block.Contains(p.Addr()) {
			return nil, fmt.Errorf("an adapter already has %s inside %s", p.Addr(), cfg.Block)
		}
	}
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return nil, fmt.Errorf("read the routes: %w", err)
	}
	names := adapterNames()
	for _, r := range rows {
		p := r.DestinationPrefix.Prefix()
		if p.Addr().Is4() && p.Bits() >= cfg.Block.Bits() && p.Overlaps(cfg.Block) {
			return nil, fmt.Errorf("an existing route for %s (via %s) is inside %s", p, names[r.InterfaceLUID], cfg.Block)
		}
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		steps = append(steps, "不是管理员：要以管理员运行（服务就是），否则建不了网卡、写不了 NRPT")
	}
	if err := wintunHere(); err != nil {
		steps = append(steps, "缺 wintun.dll："+err.Error())
	}
	if _, exists := adapterByName(names, adapterName); exists {
		return nil, fmt.Errorf("已经有一张叫 %s 的网卡：另一个引擎正在用内核模式？", adapterName)
	}
	if luid, ok := adapterByName(names, "Tailscale"); ok {
		n := 0
		for _, r := range rows {
			if r.InterfaceLUID == luid && r.DestinationPrefix.Prefix().Addr().Is4() {
				n++
			}
		}
		steps = append(steps, fmt.Sprintf("Tailscale 在：它的网卡上有 %d 条 IPv4 路由，都不在 %s 里，互不相干；它的 NRPT 规则不动", n, cfg.Block))
	}
	steps = append(steps,
		fmt.Sprintf("建一张 Wintun 网卡 %s，地址 %s/32，IP 层 MTU %d（IPv4、IPv6 都设），网卡上不设 DNS 服务器", adapterName, cfg.Self, cfg.MTU),
		fmt.Sprintf("经它加 %d 条 /32 路由（每个对端一条，DNS 地址 %s 一条）；%s 里别的地址不经过它", len(peers)+1, cfg.DNS, cfg.Block))
	if cfg.DNS.IsValid() && cfg.Domain != "" {
		rules, err := nrptRules()
		if err != nil {
			return nil, fmt.Errorf("read the NRPT rules: %w", err)
		}
		for key, ns := range rules {
			if key != nrptRule && slices.ContainsFunc(ns, func(n string) bool { return strings.EqualFold(strings.Trim(n, "."), cfg.Domain) }) {
				return nil, fmt.Errorf("NRPT rule %s already sends .%s elsewhere", key, cfg.Domain)
			}
		}
		steps = append(steps, fmt.Sprintf("加一条 NRPT 规则（注册表 %s\\%s）：只有 .%s 下的名字问 %s；现有 %d 条规则不动", nrptLocal, nrptRule, cfg.Domain, cfg.DNS, len(rules)))
		if n, _ := policyNRPTRules(); n > 0 {
			steps = append(steps, fmt.Sprintf("注意：组策略下发了 %d 条 NRPT 规则，有它们时本机的规则不生效，.%s 的名字会解析不了", n, cfg.Domain))
		}
	}
	steps = append(steps, "引擎经物理网卡收发（IP_UNICAST_IF），不改默认路由、别的 DNS、防火墙；停下时撤掉规则和网卡")
	return steps, nil
}

// setMTU sets the adapter's IP-layer MTU (NlMtu), IPv4 and IPv6. Wintun
// adapters come up with 65535: Windows would hand the engine packets of
// any size instead of fragmenting them, and those the tunnel can't carry
// are lost. An adapter without IPv6 (turned off on the machine) has no
// IPv6 row.
func setMTU(luid winipcfg.LUID, mtu int) error {
	for _, f := range []struct {
		family winipcfg.AddressFamily
		name   string
	}{{windows.AF_INET, "IPv4"}, {windows.AF_INET6, "IPv6"}} {
		row, err := luid.IPInterface(f.family)
		if f.family == windows.AF_INET6 && errors.Is(err, windows.ERROR_NOT_FOUND) {
			continue
		}
		if err == nil {
			row.NLMTU = uint32(mtu)
			err = row.Set()
		}
		if err != nil {
			return fmt.Errorf("%s MTU %d: %w", f.name, mtu, err)
		}
	}
	return nil
}

// wintunHere: wintun.dll is beside the executable, where wireguard-go
// loads it from.
func wintunHere() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("%s not found (it ships beside denebd.exe)", p)
	}
	return nil
}

// adapterNames maps adapters' LUIDs to their names, for messages.
func adapterNames() map[winipcfg.LUID]string {
	names := map[winipcfg.LUID]string{}
	aa, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		return names
	}
	for _, a := range aa {
		names[a.LUID] = a.FriendlyName()
	}
	return names
}

func adapterByName(names map[winipcfg.LUID]string, name string) (winipcfg.LUID, bool) {
	for luid, n := range names {
		if strings.EqualFold(n, name) {
			return luid, true
		}
	}
	return 0, false
}

// nrptRules lists the local NRPT rules: key name to the namespaces it
// covers.
func nrptRules() (map[string][]string, error) {
	rules := map[string][]string{}
	base, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptLocal, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	defer base.Close()
	keys, err := base.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	for _, name := range keys {
		k, err := registry.OpenKey(base, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		ns, _, _ := k.GetStringsValue("Name")
		k.Close()
		rules[name] = ns
	}
	return rules, nil
}

// policyNRPTRules counts the group policy's NRPT rules.
func policyNRPTRules() (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptPolicy, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return 0, nil
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	return len(names), err
}

// writeNRPT adds Deneb's rule: names under domain go to dns, and nothing
// else changes.
func writeNRPT(key, domain string, dns netip.Addr) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, nrptLocal+`\`+key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for _, set := range []func() error{
		func() error { return k.SetDWordValue("Version", 2) },
		func() error { return k.SetStringsValue("Name", []string{"." + domain}) },
		func() error { return k.SetStringValue("GenericDNSServers", dns.String()) },
		func() error { return k.SetDWordValue("ConfigOptions", 8) }, // generic DNS servers
		func() error { return k.SetStringValue("IPSECCARestriction", "") },
	} {
		if err := set(); err != nil {
			return err
		}
	}
	return nil
}

// removeNRPT deletes Deneb's rule. With domain set, only if it still
// covers that domain: a key changed by someone else is left.
func removeNRPT(key, domain string, logf func(string, ...any)) {
	rules, err := nrptRules()
	if err != nil {
		logf("[tun] NRPT: %v", err)
		return
	}
	ns, ok := rules[key]
	if !ok {
		return
	}
	if domain != "" && !slices.Contains(ns, "."+domain) {
		logf("[tun] the NRPT rule %s was changed by someone else (%v); leaving it", key, ns)
		return
	}
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, nrptLocal+`\`+key); err != nil {
		logf("[tun] remove the NRPT rule: %v", err)
	}
}

var procFlush = windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")

// flushDNS empties the DNS client's cache, so a rule takes effect at once.
func flushDNS() {
	if procFlush.Find() == nil {
		procFlush.Call()
	}
}
