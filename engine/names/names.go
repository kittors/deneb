// Package names implements Deneb's naming rule (multi-lan-tech §3.4):
//
//   - box.lin[.deneb.internal] is device "box" of owner "lin": one formal
//     name per device, whatever the number of scopes it is in;
//   - box alone resolves only when exactly one visible device is called
//     "box"; otherwise it is refused with the candidates, never guessed.
//
// Only devices that share an active scope are in the table, so names from a
// paused scope, or from scopes this device is not in, never resolve.
package names

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/kittors/deneb/proto"
)

// Entry is one device the table can name.
type Entry struct {
	Device    proto.DeviceID
	Name      string // DNS label: "box"
	OwnerName string // owner's DNS label: "lin"
	FQDN      string // "box.lin.deneb.internal"
	Addr      netip.Addr
	Scopes    []proto.ScopeID
}

// Table is immutable; the engine builds a new one with every network map.
type Table struct {
	domain  string
	entries []Entry
}

// NewTable makes a table for domain (normally proto.Domain).
func NewTable(domain string, entries []Entry) *Table {
	if domain == "" {
		domain = proto.Domain
	}
	return &Table{domain: strings.ToLower(domain), entries: entries}
}

// Domain is the suffix the table answers for.
func (t *Table) Domain() string { return t.domain }

// Entries lists the devices the table can name.
func (t *Table) Entries() []Entry { return t.entries }

// NotFoundError means no visible device has that name.
type NotFoundError struct{ Query string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s: no such device in your active LANs", e.Query)
}

// AmbiguousError means a short name matches several devices.
type AmbiguousError struct {
	Query      string
	Candidates []Entry
}

func (e *AmbiguousError) Error() string {
	var list []string
	for _, c := range e.Candidates {
		list = append(list, c.FQDN)
	}
	return fmt.Sprintf("%q matches %d devices: %s", e.Query, len(e.Candidates), strings.Join(list, ", "))
}

// IsDenebName reports whether q is under the table's domain.
func (t *Table) IsDenebName(q string) bool {
	q = normalize(q)
	return q == t.domain || strings.HasSuffix(q, "."+t.domain)
}

func normalize(q string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), ".")
}

// Resolve finds the one device q names: an address, a device id, a short
// name, name.owner, or the formal name.
func (t *Table) Resolve(q string) (Entry, error) {
	n := normalize(q)
	if a, err := netip.ParseAddr(n); err == nil {
		for _, e := range t.entries {
			if e.Addr == a {
				return e, nil
			}
		}
		return Entry{}, &NotFoundError{Query: q}
	}
	for _, e := range t.entries {
		if string(e.Device) == strings.TrimSpace(q) {
			return e, nil
		}
	}
	n = strings.TrimSuffix(n, "."+t.domain)
	parts := strings.Split(n, ".")
	if len(parts) > 2 || n == "" {
		return Entry{}, &NotFoundError{Query: q}
	}
	var hits []Entry
	for _, e := range t.entries {
		if e.Name == parts[0] && (len(parts) == 1 || e.OwnerName == parts[1]) {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 0:
		return Entry{}, &NotFoundError{Query: q}
	case 1:
		return hits[0], nil
	}
	return Entry{}, &AmbiguousError{Query: q, Candidates: hits}
}
