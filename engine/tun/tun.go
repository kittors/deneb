// Package tun is kernel mode: a real interface with the device's address,
// one route for the deployment's block, and DNS for deneb.internal only, so
// ordinary programs reach peers by name without a proxy (plan §2.3). M1 has
// it on macOS only; Windows (Wintun + NRPT) and Linux come in M5.
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
}

// Undo records the changes made, so a crashed engine's leftovers can be
// removed at the next start.
type Undo struct {
	Interface    string       `json:"interface"`
	Route        netip.Prefix `json:"route"`
	ResolverFile string       `json:"resolverFile,omitempty"`
	ResolverBody string       `json:"resolverBody,omitempty"`
}

const undoFile = "kernel-undo.json"

// Fake wraps a device a test made as a Kernel that changed nothing on the
// system: Close only closes the device.
func Fake(dev wgtun.Device, name string) *Kernel {
	return &Kernel{Dev: dev, Name: name, undo: &Undo{}}
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
			return fmt.Errorf("block %s overlaps %s, which other software relies on", block, r)
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
	logf("[tun] undoing what an earlier run left: %+v", *u)
	undo(u, logf)
	os.Remove(filepath.Join(stateDir, undoFile))
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
