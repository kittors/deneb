package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Config is what a control plane would push to one device. It only lists
// peers that share at least one LAN with this device, and for each peer only
// the LANs they share: a member of "studio" never learns that this device is
// also in "weekend", nor who else is there.
type Config struct {
	Name         string     `json:"name"`
	Owner        string     `json:"owner"`
	PrivateKey   string     `json:"privateKey"`   // hex, X25519
	Address      string     `json:"address"`      // one address for this device in every LAN
	AddressBlock string     `json:"addressBlock"` // the deployment's block; kernel mode routes it as one route
	ListenPort   int        `json:"listenPort"`
	Relay        RelayConf  `json:"relay"`
	LANs         []LANConf  `json:"lans"`
	Peers        []PeerConf `json:"peers"`
}

type RelayConf struct {
	Host     string `json:"host"` // TLS server name of the DERP server
	IP       string `json:"ip"`   // dialed directly: local DNS may be hijacked by a proxy's fake-ip mode
	Port     int    `json:"port"`
	STUNPort int    `json:"stunPort"`
}

type LANConf struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Open []string `json:"open"` // services this device opens to members of this LAN: "ping", "tcp/80"
}

type PeerConf struct {
	Name      string   `json:"name"`
	Owner     string   `json:"owner"`
	PublicKey string   `json:"publicKey"`
	Address   string   `json:"address"`
	LANs      []string `json:"lans"`      // only the LANs shared with this device
	Endpoints []string `json:"endpoints"` // static hints, e.g. a server's public ip:port
}

func (p PeerConf) Label() string { return p.Name + "." + p.Owner }

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Topology describes the whole test world; `gen` turns it into one Config per
// device, playing the role of the control plane.
type Topology struct {
	AddressBlock string    `json:"addressBlock"`
	Relay        RelayConf `json:"relay"`
	LANs         []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"lans"`
	Devices []struct {
		Name       string              `json:"name"`
		Owner      string              `json:"owner"`
		ListenPort int                 `json:"listenPort"`
		Endpoints  []string            `json:"endpoints"`
		LANs       map[string][]string `json:"lans"` // lan id -> services opened to that LAN
	} `json:"devices"`
}

func cmdGen(args []string) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	topoPath := fs.String("topology", "topology.json", "topology file")
	outDir := fs.String("out", "configs", "output directory")
	fs.Parse(args)

	b, err := os.ReadFile(*topoPath)
	if err != nil {
		log.Fatal(err)
	}
	var t Topology
	if err := json.Unmarshal(b, &t); err != nil {
		log.Fatal(err)
	}
	block, err := netip.ParsePrefix(t.AddressBlock)
	if err != nil || !block.Addr().Is4() {
		log.Fatalf("addressBlock: need an IPv4 prefix, got %q", t.AddressBlock)
	}
	lanName := map[string]string{}
	for _, l := range t.LANs {
		lanName[l.ID] = l.Name
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	type dev struct {
		priv, pub [32]byte
		addr      netip.Addr
	}
	devs := make([]dev, len(t.Devices))
	next := block.Addr().Next() // skip the network address
	for i, d := range t.Devices {
		if d.Name == "" || d.Owner == "" {
			log.Fatalf("device %d: name and owner are required", i)
		}
		for id := range d.LANs {
			if _, ok := lanName[id]; !ok {
				log.Fatalf("device %s.%s: unknown lan %q", d.Name, d.Owner, id)
			}
		}
		// Keep keys stable across re-generation so running nodes and logs stay comparable.
		priv, err := loadOrCreateKey(filepath.Join(*outDir, "keys", d.Name+"."+d.Owner+".key"))
		if err != nil {
			log.Fatal(err)
		}
		var pub [32]byte
		curve25519.ScalarBaseMult(&pub, &priv)
		// One address per device, allocated once from the block. Isolation between
		// LANs is enforced by the filter, not by giving each LAN its own range.
		if !block.Contains(next) {
			log.Fatal("address block exhausted")
		}
		devs[i] = dev{priv: priv, pub: pub, addr: next}
		next = next.Next()
	}

	for i, d := range t.Devices {
		c := Config{
			AddressBlock: block.String(),
			Name:         d.Name,
			Owner:        d.Owner,
			PrivateKey:   hex.EncodeToString(devs[i].priv[:]),
			Address:      devs[i].addr.String(),
			ListenPort:   d.ListenPort,
			Relay:        t.Relay,
		}
		for _, l := range t.LANs {
			if open, ok := d.LANs[l.ID]; ok {
				c.LANs = append(c.LANs, LANConf{ID: l.ID, Name: l.Name, Open: open})
			}
		}
		for j, e := range t.Devices {
			if i == j {
				continue
			}
			var shared []string
			for id := range d.LANs {
				if _, ok := e.LANs[id]; ok {
					shared = append(shared, id)
				}
			}
			if len(shared) == 0 {
				continue // netmap scoping: devices with no common LAN never learn about each other
			}
			slices.Sort(shared)
			c.Peers = append(c.Peers, PeerConf{
				Name:      e.Name,
				Owner:     e.Owner,
				PublicKey: hex.EncodeToString(devs[j].pub[:]),
				Address:   devs[j].addr.String(),
				LANs:      shared,
				Endpoints: e.Endpoints,
			})
		}
		out, _ := json.MarshalIndent(c, "", "  ")
		path := filepath.Join(*outDir, d.Name+"."+d.Owner+".json")
		if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
			log.Fatal(err)
		}
		var peers []string
		for _, p := range c.Peers {
			peers = append(peers, p.Label()+"("+strings.Join(p.LANs, ",")+")")
		}
		fmt.Printf("%-10s %-14s peers: %s\n", d.Name+"."+d.Owner, c.Address, strings.Join(peers, " "))
	}
}

func loadOrCreateKey(path string) ([32]byte, error) {
	var k [32]byte
	if b, err := os.ReadFile(path); err == nil {
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != 32 {
			return k, fmt.Errorf("%s: bad key", path)
		}
		copy(k[:], raw)
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	// Clamp as X25519 expects (WireGuard does the same).
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return k, err
	}
	return k, os.WriteFile(path, []byte(hex.EncodeToString(k[:])+"\n"), 0o600)
}

func parseHexKey(s string) ([32]byte, error) {
	var k [32]byte
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return k, fmt.Errorf("bad key %q", s)
	}
	copy(k[:], raw)
	return k, nil
}

// parseService turns "ping", "tcp/80" or "udp/53" into protocol/port pairs.
// "ping" covers ICMP echo and the UDP echo on port 7 that the measurements use.
func parseService(s string) ([]svc, error) {
	if s == "ping" {
		return []svc{{proto: protoICMP}, {proto: protoUDP, port: 7}}, nil
	}
	one, err := parseOneService(s)
	return []svc{one}, err
}

func parseOneService(s string) (svc, error) {
	proto, port, ok := strings.Cut(s, "/")
	if !ok {
		return svc{}, fmt.Errorf("bad service %q", s)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return svc{}, fmt.Errorf("bad service %q", s)
	}
	switch proto {
	case "tcp":
		return svc{proto: protoTCP, port: uint16(n)}, nil
	case "udp":
		return svc{proto: protoUDP, port: uint16(n)}, nil
	}
	return svc{}, fmt.Errorf("bad service %q", s)
}
