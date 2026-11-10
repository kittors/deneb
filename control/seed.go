package control

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kittors/deneb/proto"
)

// Seed is what a deployment starts with: the test accounts and their
// verification codes (M1 sends no email), the relays, and optionally a
// fixed address space. It is applied at every start; accounts are matched
// by email and relays by id, so editing the file and restarting is how
// they change.
type Seed struct {
	Accounts []SeedAccount `json:"accounts"`
	Relays   []SeedRelay   `json:"relays"`
	// Network fixes the address space at the first start; empty picks at
	// random. Later starts keep what the first one picked.
	Network struct {
		IPv4 string `json:"ipv4,omitempty"`
		IPv6 string `json:"ipv6,omitempty"`
	} `json:"network"`
}

// SeedAccount is one test account.
type SeedAccount struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Initial     string `json:"initial,omitempty"` // default: from DisplayName
	Name        string `json:"name,omitempty"`    // DNS label; default: from the email
	Code        string `json:"code"`              // the verification code to log in with
}

// SeedRelay is one relay. Default (the default) puts it in every LAN that
// has no relay list of its own, in seed order.
type SeedRelay struct {
	proto.Relay
	OwnerEmail string `json:"ownerEmail,omitempty"`
	Default    *bool  `json:"default,omitempty"`
}

// LoadSeed reads a seed file.
func LoadSeed(path string) (Seed, error) {
	var s Seed
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// HKSpareRelay stands in for the derper the user already runs on hk-spare
// (Deneb is only a client of it and changes nothing there, implementation.md
// §0.1). The address is a documentation one: the real one is only in the
// production seed, which is not in the repository. To have a development
// server hand out a real relay, give it a seed file with -seed.
func HKSpareRelay() SeedRelay {
	return SeedRelay{
		Relay: proto.Relay{
			ID:       "hk",
			Name:     "香港",
			Source:   proto.RelayServer,
			HostName: "derp.example.com",
			IPv4:     netip.MustParseAddr("198.51.100.4"),
			DERPPort: 33443,
			STUNPort: 3478,
		},
		OwnerEmail: "kittors@deneb.test",
	}
}

// DevCode is every account's verification code in development.
const DevCode = "000000"

// DevSeed is the development server's seed: the people of the design's
// samples, all with DevCode, and hk-spare's relay.
func DevSeed() Seed {
	var s Seed
	for _, a := range []SeedAccount{
		{Email: "kittors@deneb.test", DisplayName: "kittors", Initial: "K", Name: "kittors"},
		{Email: "jie@deneb.test", DisplayName: "阿杰", Initial: "杰", Name: "jie"},
		{Email: "mia@deneb.test", DisplayName: "Mia", Initial: "M", Name: "mia"},
		{Email: "leo@deneb.test", DisplayName: "Leo", Initial: "L", Name: "leo"},
	} {
		a.Code = DevCode
		s.Accounts = append(s.Accounts, a)
	}
	s.Relays = []SeedRelay{HKSpareRelay()}
	return s
}

// ParseRelayFlag reads a relay written as key=value pairs, as the -relay
// flag takes it: "id=local,name=本地,host=derper.deneb.test,ipv4=172.30.0.3,derp=443,stun=3478".
func ParseRelayFlag(s string) (SeedRelay, error) {
	r := SeedRelay{Relay: proto.Relay{Source: proto.RelayServer}}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return r, fmt.Errorf("relay %q: %q is not key=value", s, kv)
		}
		var err error
		switch k {
		case "id":
			r.ID = proto.RelayID(v)
		case "name":
			r.Name = v
		case "host":
			r.HostName = v
		case "ipv4":
			r.IPv4, err = netip.ParseAddr(v)
		case "ipv6":
			r.IPv6, err = netip.ParseAddr(v)
		case "derp":
			r.DERPPort, err = parsePort(v)
		case "stun":
			r.STUNPort, err = parsePort(v)
		case "source":
			r.Source = proto.RelaySource(v)
		case "owner":
			r.OwnerEmail = v
		default:
			return r, fmt.Errorf("relay %q: unknown key %q", s, k)
		}
		if err != nil {
			return r, fmt.Errorf("relay %q: %s: %w", s, k, err)
		}
	}
	if r.ID == "" {
		r.ID = proto.RelayID(proto.MakeLabel(r.Name, "relay"))
	}
	return r, r.check()
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	return uint16(n), err
}

func (r SeedRelay) check() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("relay has no id")
	case r.Name == "":
		return fmt.Errorf("relay %s has no name", r.ID)
	case r.HostName == "":
		return fmt.Errorf("relay %s has no host", r.ID)
	case r.DERPPort == 0:
		return fmt.Errorf("relay %s has no DERP port", r.ID)
	case r.Source != proto.RelayServer && r.Source != proto.RelayHome && r.Source != proto.RelayPublic:
		return fmt.Errorf("relay %s: source %q", r.ID, r.Source)
	}
	return nil
}

// initialOf is the ball's letter for a name: the first letter of a Latin
// name, upper-cased ("kittors" → "K"), the last character of a Chinese
// one ("阿杰" → "杰"), as people call each other by it.
func initialOf(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return "?"
	}
	if r < 0x80 || unicode.Is(unicode.Latin, r) {
		return string(unicode.ToUpper(r))
	}
	last, _ := utf8.DecodeLastRuneInString(name)
	return string(last)
}

// applySeed brings the database in line with the seed.
func (s *Server) applySeed() error {
	seed := s.cfg.Seed
	codes := map[string]string{}
	for _, a := range seed.Accounts {
		e := normEmail(a.Email)
		if !validEmail(e) {
			return fmt.Errorf("seed: account email %q", a.Email)
		}
		if a.Code == "" {
			continue // a real account: it gets its codes by mail
		}
		// A fixed code never changes and is in a file: only for test
		// accounts, at domains where no one receives mail (M7 review).
		if !isTestEmail(e) {
			return fmt.Errorf("seed: account %s has a fixed code, which only test accounts may have (at .test, .example, .invalid, .localhost or example.com); leave it out and the code goes by mail", e)
		}
		codes[e] = a.Code
	}
	for _, r := range seed.Relays {
		if r.Source == "" {
			r.Source = proto.RelayServer
		}
		if err := r.check(); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	s.testCodes = codes

	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		changed := false
		taken := map[string]bool{}
		for _, p := range w.people {
			taken[p.Name] = true
		}
		for _, a := range seed.Accounts {
			e := normEmail(a.Email)
			display := strings.TrimSpace(a.DisplayName)
			if display == "" {
				display = e[:strings.IndexByte(e, '@')]
			}
			initial := a.Initial
			if initial == "" {
				initial = initialOf(display)
			}
			if p := w.byEmail[e]; p != nil {
				if p.DisplayName != display || p.Initial != initial {
					if _, err := tx.Exec(`UPDATE people SET display_name = ?, initial = ? WHERE id = ?`, display, initial, p.ID); err != nil {
						return err
					}
					changed = true
				}
				continue
			}
			label := a.Name
			if label == "" {
				label = proto.MakeLabel(e[:strings.IndexByte(e, '@')], "user")
			}
			if !proto.ValidLabel(label) {
				return fmt.Errorf("seed: account %s: name %q is not a DNS label", e, label)
			}
			base := label
			for n := 2; taken[label]; n++ {
				label = base + "-" + strconv.Itoa(n)
			}
			taken[label] = true
			if _, err := tx.Exec(`INSERT INTO people (id, email, name, display_name, initial, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				newID("p"), e, label, display, initial, ms(s.now())); err != nil {
				return err
			}
			changed = true
		}

		// Relays: the seed's are upserted and become the defaults, in its
		// order; any other relay stops being a default.
		inSeed := map[proto.RelayID]bool{}
		for pos, r := range seed.Relays {
			if r.Source == "" {
				r.Source = proto.RelayServer
			}
			inSeed[r.ID] = true
			owner := r.Owner
			if r.OwnerEmail != "" {
				// The owner may be an account added by this same seed, not
				// yet in w; read it from the transaction.
				var id string
				err := tx.QueryRow(`SELECT id FROM people WHERE email = ?`, normEmail(r.OwnerEmail)).Scan(&id)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				owner = proto.PersonID(id)
			}
			dpos := pos
			if r.Default != nil && !*r.Default {
				dpos = -1
			}
			v4, v6 := "", ""
			if r.IPv4.IsValid() {
				v4 = r.IPv4.String()
			}
			if r.IPv6.IsValid() {
				v6 = r.IPv6.String()
			}
			old := w.relays[r.ID]
			if old != nil && old.Name == r.Name && old.Source == r.Source && old.Owner == owner && old.HostName == r.HostName &&
				old.IPv4 == r.IPv4 && old.IPv6 == r.IPv6 && old.DERPPort == r.DERPPort && old.STUNPort == r.STUNPort &&
				defaultPos(w, r.ID) == dpos {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO relays (id, name, source, owner, host_name, ipv4, ipv6, derp_port, stun_port, default_pos)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT (id) DO UPDATE SET name = excluded.name, source = excluded.source, owner = excluded.owner,
						host_name = excluded.host_name, ipv4 = excluded.ipv4, ipv6 = excluded.ipv6,
						derp_port = excluded.derp_port, stun_port = excluded.stun_port, default_pos = excluded.default_pos`,
				r.ID, r.Name, r.Source, owner, r.HostName, v4, v6, r.DERPPort, r.STUNPort, dpos); err != nil {
				return err
			}
			changed = true
		}
		for id := range w.relays {
			if !inSeed[id] && defaultPos(w, id) >= 0 {
				if _, err := tx.Exec(`UPDATE relays SET default_pos = -1 WHERE id = ?`, id); err != nil {
					return err
				}
				changed = true
			}
		}
		if !changed {
			return errNoChange
		}
		return nil
	})
	return err
}

// defaultPos is relay id's stored default position, or -1.
func defaultPos(w *World, id proto.RelayID) int {
	if p, ok := w.relayPos[id]; ok {
		return p
	}
	return -1
}
