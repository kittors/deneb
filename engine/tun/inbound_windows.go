package tun

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Letting the network's devices in (found on the test machine 11-11).
// Windows puts a new adapter on a network of its own and calls it public.
// There its firewall lets in nothing that no rule allows on public
// networks: not ping, not a game the user allowed when Windows asked (that
// dialog ticks private networks only, and blocks the program on public
// ones), not a file share. So, as Tailscale does with its adapter, kernel
// mode
//
//   - makes the adapter's network private, as a home Wi-Fi is: through the
//     Network List Manager, that network only, and only while the adapter
//     is all there is on it;
//   - adds one inbound rule, Deneb-In: what comes from the block to this
//     device's address, any protocol, on private and domain networks. A
//     packet for that address only arrives through the adapter (Windows
//     keeps to the strong host model), and the engine's filter has judged
//     it already: who, and which ports.
//
// Nothing else in the firewall changes. The rule goes when the engine
// stops; after a crash, with the undo record (the next start, `denebd
// service uninstall`), or with undo.ps1. The network stays private: the
// adapter's GUID is fixed (adapterGUID), so Windows finds the same network
// at the next start instead of making a new public one.

// letIn does both off the engine's way: Windows takes a few seconds to put
// a new adapter on a network, and its firewall can be slow to change
// (Tailscale saw minutes). Close stops it, and waits for it.
func (k *Kernel) letIn(logf func(string, ...any)) {
	stop, done := make(chan struct{}), make(chan struct{})
	k.plat.stopIn, k.plat.inDone = stop, done
	go func() {
		defer close(done)
		if err := addFirewallRule(k.cfg.Self, k.cfg.Block); err != nil {
			logf("[tun] firewall rule %s: %v", FirewallRule, err)
		} else {
			logf("[tun] firewall rule %s: lets %s in to %s, on private and domain networks", FirewallRule, k.cfg.Block, k.cfg.Self)
		}
		guid, err := k.plat.luid.GUID()
		if err != nil {
			logf("[tun] network category: the adapter's GUID: %v", err)
			return
		}
		const tries = 30
		var last error
		for try := 1; ; try++ {
			n, changed, found, err := adapterNetwork(*guid, true)
			switch {
			case err != nil:
				last = err
			case changed:
				logf("[tun] network %v: was public, made it private (after %d s)", n, try-1)
				return
			case found && n.category == categoryPublic:
				logf("[tun] network %v: public, and %s is not alone on it, so it stays public", n, adapterName)
				return
			case found:
				logf("[tun] network %v", n)
				return
			}
			if try == tries {
				if last == nil {
					last = fmt.Errorf("Windows has not put %s on a network", adapterName)
				}
				logf("[tun] network category, %d s on: %v; it stays as Windows sets it", tries, last)
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
		}
	}()
}

// stopLettingIn stops letIn and waits for it: a rule it adds after Close
// removed the old one would stay.
func (k *Kernel) stopLettingIn() {
	if k.plat.stopIn != nil {
		close(k.plat.stopIn)
		<-k.plat.inDone
		k.plat.stopIn = nil
	}
}

// netsh runs `netsh advfirewall firewall <args>`, within a time limit.
func netsh(args ...string) error {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "netsh.exe"), append([]string{"advfirewall", "firewall"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// netsh writes in the console's code page; the exit status is what counts.
		return fmt.Errorf("netsh %s: %w: %s", strings.Join(args[:2], " "), err, strings.ToValidUTF8(strings.TrimSpace(string(out)), "?"))
	}
	return nil
}

// ruleExists: there is an inbound rule named name. netsh fails when none
// matches.
func ruleExists(name string) bool {
	return netsh("show", "rule", "name="+name, "dir=in") == nil
}

// removeRule deletes every inbound rule named name, if there is one;
// removed says there was.
func removeRule(name string) (removed bool, err error) {
	if !ruleExists(name) {
		return false, nil
	}
	if err := netsh("delete", "rule", "name="+name, "dir=in"); err != nil {
		return false, err
	}
	return true, nil
}

// firewallRuleExists: there is a rule named Deneb-In.
func firewallRuleExists() bool { return ruleExists(FirewallRule) }

// addFirewallRule adds Deneb-In, for this device's address, in place of
// any rule by that name: an earlier run's that was not removed, which
// netsh would otherwise keep beside the new one.
func addFirewallRule(self netip.Addr, block netip.Prefix) error {
	netsh("delete", "rule", "name="+FirewallRule, "dir=in") // fails when there is none
	return netsh(firewallRuleArgs(self, block)...)
}

// RemoveFirewallRule deletes Deneb's inbound rule (every rule by its name),
// if there is one; removed says there was.
func RemoveFirewallRule() (removed bool, err error) { return removeRule(FirewallRule) }

// AddProcessRule adds Deneb-Process for the program exe, the service's
// engine, in place of any rule by that name (an earlier install's).
func AddProcessRule(exe string) error {
	netsh("delete", "rule", "name="+ProcessRule, "dir=in") // fails when there is none
	return netsh(processRuleArgs(exe)...)
}

// ProcessRuleExists: there is a rule named Deneb-Process.
func ProcessRuleExists() bool { return ruleExists(ProcessRule) }

// RemoveProcessRule deletes Deneb-Process (every rule by its name), if
// there is one; removed says there was.
func RemoveProcessRule() (removed bool, err error) { return removeRule(ProcessRule) }

// The Network List Manager, through its COM interfaces' method tables.
var (
	modole32             = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = modole32.NewProc("CoInitializeEx")
	procCoCreateInstance = modole32.NewProc("CoCreateInstance")
	procSysFreeString    = windows.NewLazySystemDLL("oleaut32.dll").NewProc("SysFreeString")

	clsidNetworkListManager = mustGUID("{DCB00C01-570F-4A9B-8D69-199FDBA5723B}")
	iidINetworkListManager  = mustGUID("{DCB00000-570F-4A9B-8D69-199FDBA5723B}")
)

// comIface is a COM interface pointer: its first word is the address of
// the interface's method table.
type comIface struct{ vtbl *[20]uintptr }

// Methods by their place in the table: IUnknown's three, IDispatch's four,
// then the interface's own, in the order netlistmgr.idl declares them.
const (
	mRelease = 2

	nlmGetNetworkConnections = 9 // INetworkListManager

	enumNext = 8 // IEnumNetworkConnections

	connGetNetwork   = 7 // INetworkConnection
	connGetAdapterID = 12

	netGetName               = 7 // INetwork
	netGetNetworkConnections = 13
	netGetCategory           = 18
	netSetCategory           = 19
)

// NLM_NETWORK_CATEGORY.
const (
	categoryPublic  = 0
	categoryPrivate = 1
	categoryDomain  = 2
)

const (
	clsctxAll         = 0x17       // CLSCTX_ALL
	sFalse            = 1          // S_FALSE
	rpcEChangedMode   = 0x80010106 // RPC_E_CHANGED_MODE
	coinitMultithread = 0          // COINIT_MULTITHREADED
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

func (c *comIface) release() {
	if c != nil {
		syscall.SyscallN(c.vtbl[mRelease], uintptr(unsafe.Pointer(c)))
	}
}

func hresult(what string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("%s: %#08x %v", what, uint32(hr), windows.Errno(uint32(hr)))
}

func categoryName(c int32) string {
	switch c {
	case categoryPublic:
		return "public"
	case categoryPrivate:
		return "private"
	case categoryDomain:
		return "domain"
	}
	return fmt.Sprintf("category %d", c)
}

// network is what the network list says of the network an adapter is on.
type network struct {
	name     string
	category int32
	adapters int // on it, this one too; -1 when Windows does not say
}

func (n network) String() string {
	return fmt.Sprintf("%q (%s, %d adapters on it)", n.name, categoryName(n.category), n.adapters)
}

// adapterNetwork reads the network the adapter guid is on; found is false
// while Windows has not put the adapter on a network yet. With makePrivate,
// a public network that the adapter has to itself is made private
// (changed). One with other adapters on it is left as it is: its category
// is theirs as well.
func adapterNetwork(guid windows.GUID, makePrivate bool) (n network, changed, found bool, err error) {
	found, err = withAdapterNetwork(guid, func(net *comIface) error {
		n.name = networkName(net)
		hr, _, _ := syscall.SyscallN(net.vtbl[netGetCategory], uintptr(unsafe.Pointer(net)), uintptr(unsafe.Pointer(&n.category)))
		if err := hresult("INetwork.GetCategory", hr); err != nil {
			return err
		}
		n.adapters = connections(net)
		if !makePrivate || n.category != categoryPublic || n.adapters > 1 || n.adapters < 0 {
			return nil
		}
		hr, _, _ = syscall.SyscallN(net.vtbl[netSetCategory], uintptr(unsafe.Pointer(net)), categoryPrivate)
		if err := hresult("INetwork.SetCategory", hr); err != nil {
			return err
		}
		n.category, changed = categoryPrivate, true
		return nil
	})
	return n, changed, found, err
}

// withAdapterNetwork calls fn with the network the adapter guid is
// connected to, if Windows has put it on one (found).
func withAdapterNetwork(guid windows.GUID, fn func(net *comIface) error) (found bool, _ error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch hr, _, _ := procCoInitializeEx.Call(0, coinitMultithread); uint32(hr) {
	case 0, sFalse:
		defer windows.CoUninitialize()
	case rpcEChangedMode:
		// The thread has an apartment already: use that one.
	default:
		return false, hresult("CoInitializeEx", hr)
	}
	var nlm *comIface
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidNetworkListManager)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidINetworkListManager)), uintptr(unsafe.Pointer(&nlm)))
	if err := hresult("CoCreateInstance(NetworkListManager)", hr); err != nil {
		return false, err
	}
	defer nlm.release()
	var conns *comIface
	hr, _, _ = syscall.SyscallN(nlm.vtbl[nlmGetNetworkConnections], uintptr(unsafe.Pointer(nlm)), uintptr(unsafe.Pointer(&conns)))
	if err := hresult("INetworkListManager.GetNetworkConnections", hr); err != nil {
		return false, err
	}
	defer conns.release()
	for {
		var conn *comIface
		var n uint32
		hr, _, _ := syscall.SyscallN(conns.vtbl[enumNext], uintptr(unsafe.Pointer(conns)), 1, uintptr(unsafe.Pointer(&conn)), uintptr(unsafe.Pointer(&n)))
		if err := hresult("IEnumNetworkConnections.Next", hr); err != nil {
			return false, err
		}
		if n == 0 || conn == nil {
			return false, nil
		}
		ours, err := onConnection(conn, guid, fn)
		conn.release()
		if ours || err != nil {
			return ours, err
		}
	}
}

// onConnection calls fn with conn's network if conn is the adapter's.
func onConnection(conn *comIface, guid windows.GUID, fn func(net *comIface) error) (bool, error) {
	var id windows.GUID
	hr, _, _ := syscall.SyscallN(conn.vtbl[connGetAdapterID], uintptr(unsafe.Pointer(conn)), uintptr(unsafe.Pointer(&id)))
	if int32(hr) < 0 || id != guid {
		return false, nil
	}
	var net *comIface
	hr, _, _ = syscall.SyscallN(conn.vtbl[connGetNetwork], uintptr(unsafe.Pointer(conn)), uintptr(unsafe.Pointer(&net)))
	if err := hresult("INetworkConnection.GetNetwork", hr); err != nil {
		return true, err
	}
	defer net.release()
	return true, fn(net)
}

// connections counts the adapters on the network; -1 when Windows does
// not say.
func connections(net *comIface) int {
	var e *comIface
	hr, _, _ := syscall.SyscallN(net.vtbl[netGetNetworkConnections], uintptr(unsafe.Pointer(net)), uintptr(unsafe.Pointer(&e)))
	if int32(hr) < 0 || e == nil {
		return -1
	}
	defer e.release()
	for n := 0; ; n++ {
		var c *comIface
		var got uint32
		hr, _, _ := syscall.SyscallN(e.vtbl[enumNext], uintptr(unsafe.Pointer(e)), 1, uintptr(unsafe.Pointer(&c)), uintptr(unsafe.Pointer(&got)))
		if int32(hr) < 0 {
			return -1
		}
		if got == 0 || c == nil {
			return n
		}
		c.release()
	}
}

// networkName is the network's name, as the network list shows it.
func networkName(net *comIface) string {
	var b *uint16
	hr, _, _ := syscall.SyscallN(net.vtbl[netGetName], uintptr(unsafe.Pointer(net)), uintptr(unsafe.Pointer(&b)))
	if int32(hr) < 0 || b == nil {
		return "?"
	}
	defer procSysFreeString.Call(uintptr(unsafe.Pointer(b)))
	return windows.UTF16PtrToString(b)
}
