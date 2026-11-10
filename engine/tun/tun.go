// Package tun is kernel mode: a real interface with the device's address,
// routes to the peers through it, and DNS for deneb.internal only, so
// ordinary programs reach peers by name without a proxy (plan §2.3). On
// macOS one route covers the deployment's block; on Linux (M5) each peer
// gets a /32 route (SetPeers) and systemd-resolved hands the domain to the
// engine; Windows (Wintun + NRPT) comes next.
//
// It changes the system, so the engine only uses it when asked to
// (-mode kernel, run as root), and it writes down every change in the state
// directory before making it, so a crash is undone at the next start.
package tun

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// Config says what to set up.
type Config struct {
	Self     netip.Addr   // this device's address
	Block    netip.Prefix // the deployment's /16: one route covers it
	DNS      netip.Addr   // where the engine answers Domain, inside Block
	Domain   string       // "deneb.internal"
	MTU      int
	StateDir string // where the undo record is kept
}

// Kernel is a live interface. Close undoes everything.
type Kernel struct {
	Dev  wgtun.Device
	Name string
	cfg  Config
	undo *Undo
	plat platform // per system: the /32 routes on Linux and Windows
	fake bool     // a test's device (Fake): no routes to keep
}

// Undo records the changes made, so a crashed engine's leftovers can be
// removed at the next start.
type Undo struct {
	Interface    string       `json:"interface"`
	Route        netip.Prefix `json:"route"`
	ResolverFile string       `json:"resolverFile,omitempty"`
	ResolverBody string       `json:"resolverBody,omitempty"`
	Resolved     bool         `json:"resolved,omitempty"` // Linux: systemd-resolved was given the domain for Interface
	NRPT         string       `json:"nrpt,omitempty"`     // Windows: the NRPT rule's registry key
}

const undoFile = "kernel-undo.json"

// resolverDir is where macOS reads per-domain resolvers; a variable for
// tests.
var resolverDir = "/etc/resolver"

// nrptRuleName is the registry key of Deneb's NRPT rule on Windows
// (tun_windows.go), fixed so that it is found again after a crash.
const nrptRuleName = `{8F3B6C2E-1D4A-4E7B-9A5C-DE0EB2026001}`

// ErrNoResolver: on Linux, systemd-resolved is not what the system asks for
// names, so the domain could not be handed to the engine. The engine then
// runs in user-space mode instead (plan §2.4).
var ErrNoResolver = errors.New("systemd-resolved is not this system's resolver")

// ErrConflict: the block is in use here already: an address or a route
// inside it (a Docker network, another VPN), or another tunnel's range.
// ErrNoPermission: kernel mode needs root, or an administrator on Windows.
// Both, like ErrNoResolver, make the engine run in user-space mode instead
// (plan §2.3: no administrator rights, or an address conflict).
var (
	ErrConflict     = errors.New("the address block is in use here")
	ErrNoPermission = errors.New("kernel mode needs root (an administrator on Windows)")
)

// Fake wraps a device a test made as a Kernel that changed nothing on the
// system: Close only closes the device.
func Fake(dev wgtun.Device, name string) *Kernel {
	return &Kernel{Dev: dev, Name: name, undo: &Undo{}, fake: true}
}

// ReservedBlocks are ranges other software on a typical user's computer
// already depends on (multi-lan-tech §3.3): Tailscale's CGNAT range, and
// where Clash and Surge keep their fake IPs.
var ReservedBlocks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
}

// CheckBlock refuses blocks that would collide with other tunnels.
func CheckBlock(block netip.Prefix) error {
	if !block.Addr().Is4() || block.Bits() < 8 {
		return fmt.Errorf("block %s: need an IPv4 prefix of /8 or longer", block)
	}
	for _, r := range ReservedBlocks {
		if r.Overlaps(block) {
			return fmt.Errorf("%w: block %s overlaps %s, which other software relies on", ErrConflict, block, r)
		}
	}
	return nil
}

// ResolverBody is the /etc/resolver file that sends Domain, and nothing
// else, to the engine.
func ResolverBody(dns netip.Addr) string {
	return fmt.Sprintf("# Written by Deneb; removed when the Deneb engine stops.\nnameserver %s\nport 53\n", dns)
}

func saveUndo(dir string, u *Undo) error {
	b, _ := json.MarshalIndent(u, "", "  ")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, undoFile), b, 0o600)
}

func loadUndo(dir string) (*Undo, error) {
	b, err := os.ReadFile(filepath.Join(dir, undoFile))
	if err != nil {
		return nil, err
	}
	var u Undo
	return &u, json.Unmarshal(b, &u)
}

// CleanupStale removes what a crashed engine left behind, as recorded in
// its undo file. It only removes a resolver file whose content is ours.
func CleanupStale(stateDir string, logf func(string, ...any)) {
	u, err := loadUndo(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		logf("[tun] unreadable undo record: %v", err)
		return
	}
	if err := u.check(); err != nil {
		// Only what Deneb itself writes is acted on: a record from a state
		// directory someone else can write could otherwise have root or
		// SYSTEM delete their choice of file, route or rule (M7 review).
		logf("[tun] undo record refused: %v: %+v", err, *u)
		os.Remove(filepath.Join(stateDir, undoFile))
		return
	}
	logf("[tun] undoing what an earlier run left: %+v", *u)
	undo(u, logf)
	os.Remove(filepath.Join(stateDir, undoFile))
}

// check refuses an undo record Deneb would not have written: an
// interface not named as Deneb names its own, a route other than an
// address block, a resolver file outside /etc/resolver or not written by
// Deneb, an NRPT rule other than Deneb's.
func (u *Undo) check() error {
	if u.Interface != "" && !strings.HasPrefix(u.Interface, "utun") && !strings.HasPrefix(u.Interface, "deneb") && u.Interface != "Deneb" {
		return fmt.Errorf("interface %q", u.Interface)
	}
	if u.Route.IsValid() && (!u.Route.Addr().Is4() || u.Route.Bits() < 8) {
		return fmt.Errorf("route %v", u.Route)
	}
	if u.ResolverFile != "" {
		if filepath.Dir(u.ResolverFile) != resolverDir || !strings.HasPrefix(u.ResolverBody, "# Written by Deneb") {
			return fmt.Errorf("resolver file %q", u.ResolverFile)
		}
	}
	if u.NRPT != "" && u.NRPT != nrptRuleName {
		return fmt.Errorf("NRPT rule %q", u.NRPT)
	}
	return nil
}

func removeResolver(u *Undo, logf func(string, ...any)) {
	if u.ResolverFile == "" {
		return
	}
	b, err := os.ReadFile(u.ResolverFile)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(b)) != strings.TrimSpace(u.ResolverBody) {
		logf("[tun] %s was changed by someone else; leaving it", u.ResolverFile)
		return
	}
	if err := os.Remove(u.ResolverFile); err != nil {
		logf("[tun] remove %s: %v", u.ResolverFile, err)
	}
}
