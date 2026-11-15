package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// settingsFile is config.json in the data directory: what `install` chose
// and `run` uses, and how the relay was installed, for a later install,
// update, status or uninstall (design/plan/m7/relay-install.md §4). The
// Docker image has none; its flags say everything, as before.
const settingsFile = "config.json"

// Default ports: DERP over TLS, and STUN.
const (
	defaultTCPPort = 47445
	defaultUDPPort = 47478
)

// settings is config.json. Flags given to `run` win over it.
type settings struct {
	// Control and ControlPin are the control server, from the join code.
	Control    string `json:"control"`
	ControlPin string `json:"controlPin,omitempty"`

	Listen   string `json:"listen,omitempty"` // ":47445"
	STUN     string `json:"stun,omitempty"`   // ":47478"
	HostName string `json:"hostName,omitempty"`
	// IPv4 and IPv6 are given by hand (install --ipv4); without them the
	// relay finds its public addresses itself (addr.go).
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
	// DERPPort and STUNPort are the ports devices reach, when a port
	// forwarding set by hand makes them differ from the ones it listens on.
	DERPPort uint16 `json:"derpPort,omitempty"`
	STUNPort uint16 `json:"stunPort,omitempty"`
	Region   string `json:"region,omitempty"`
	// NoUpdate turns the automatic updates off (install --no-update).
	NoUpdate bool `json:"noUpdate,omitempty"`
	// NoPortMap keeps it from asking the router for port mappings.
	NoPortMap bool `json:"noPortMap,omitempty"`

	Install *installed `json:"install,omitempty"`
}

// installed is how `install` set the relay up.
type installed struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	// Scope is "system" (as root: a system service, its own user) or
	// "user" (no root: the service of the user who installed it).
	Scope string `json:"scope"`
	Init  string `json:"init"` // initSystemd, initOpenRC, ...
	Bin   string `json:"bin"`  // the program
	Link  string `json:"link,omitempty"`
	// Service is the unit, init script or crontab it wrote ("crontab:<user>"
	// for a line in a crontab).
	Service string `json:"service,omitempty"`
	User    string `json:"user,omitempty"` // the user the relay runs as
	// MadeUser: install made that user, so uninstall removes it.
	MadeUser bool   `json:"madeUser,omitempty"`
	Log      string `json:"log,omitempty"` // its own log file, where there is no journal
	// Firewall are the commands that take back what install opened in the
	// system's firewall, for uninstall.
	Firewall [][]string `json:"firewall,omitempty"`
	// FirewallKind is the host firewall install found in front of the
	// ports (proto.RelayFirewall*), and FirewallOpen whether they are open
	// there: the relay tells the control server, whose advice depends on it.
	FirewallKind string `json:"firewallKind,omitempty"`
	FirewallOpen bool   `json:"firewallOpen,omitempty"`
	// FromDocker is the container it took over, left stopped.
	FromDocker string `json:"fromDocker,omitempty"`
}

func loadSettings(dir string) (*settings, error) {
	b, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", settingsFile, err)
	}
	return &s, nil
}

func saveSettings(dir string, s *settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeFile(dir, settingsFile, append(b, '\n'))
}

// installKind is how the relay runs, as the control server names it.
func (in *installed) installKind() string {
	switch in.Init {
	case initSystemd:
		if in.Scope == scopeUser {
			return proto.RelayInstallSystemdUser
		}
		return proto.RelayInstallSystemd
	case initOpenRC:
		return proto.RelayInstallOpenRC
	case initProcd:
		return proto.RelayInstallProcd
	case initCron:
		return proto.RelayInstallCron
	}
	return proto.RelayInstallForeground
}

// startsFile notes when the relay started within the last hour, one unix
// time a line: how many times it restarted tells a relay that keeps
// failing (RelayInfo.Restarts).
const startsFile = "starts"

// noteStart notes a start at now and returns how many came before it
// within the hour.
func noteStart(dir string, now time.Time) int {
	b, _ := os.ReadFile(filepath.Join(dir, startsFile))
	var keep []string
	for _, l := range strings.Fields(string(b)) {
		if t, err := strconv.ParseInt(l, 10, 64); err == nil && now.Sub(time.Unix(t, 0)) < time.Hour {
			keep = append(keep, l)
		}
	}
	before := len(keep)
	keep = append(keep, strconv.FormatInt(now.Unix(), 10))
	writeFile(dir, startsFile, []byte(strings.Join(keep, "\n")+"\n"))
	return before
}

// tcpPort and udpPort are the ports it listens on.
func (s *settings) tcpPort() int { return portOf(s.Listen, defaultTCPPort) }
func (s *settings) udpPort() int { return portOf(s.STUN, defaultUDPPort) }
