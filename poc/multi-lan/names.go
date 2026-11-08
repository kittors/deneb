package main

import (
	"fmt"
	"net/netip"
	"strings"
)

// nameSuffix uses .internal, which ICANN reserved in 2024 for private use, so
// it can never collide with a public name.
const nameSuffix = "deneb.internal"

type nameHit struct {
	FQDN  string   `json:"fqdn"`
	Addr  string   `json:"addr"`
	LANs  []string `json:"lans"`
	Owner string   `json:"owner"`
}

// resolve implements the naming rule proposed in the doc:
//   - box.lin[.deneb.internal]: device "box" owned by "lin" (canonical, one
//     name per device no matter how many LANs it is in);
//   - box: allowed only when exactly one visible device is called "box";
//     otherwise refuse and list the candidates instead of guessing.
//
// Only devices in an active shared LAN are visible, so names from a paused
// LAN, or from LANs this device is not in, never resolve.
func (n *node) resolve(q string) (netip.Addr, []nameHit, error) {
	q = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), ".")
	if a, err := netip.ParseAddr(q); err == nil {
		return a, nil, nil
	}
	q = strings.TrimSuffix(q, "."+nameSuffix)
	parts := strings.Split(q, ".")
	if len(parts) > 2 {
		return netip.Addr{}, nil, fmt.Errorf("%s: not a Deneb name", q)
	}
	pol := n.ft.pol.Load()
	var hits []nameHit
	var addrs []netip.Addr
	consider := func(name, owner string, a netip.Addr, lans []string) {
		if name != parts[0] || (len(parts) == 2 && owner != parts[1]) {
			return
		}
		hits = append(hits, nameHit{FQDN: name + "." + owner + "." + nameSuffix, Addr: a.String(), LANs: n.lanNames(lans), Owner: owner})
		addrs = append(addrs, a)
	}
	var mine []string
	for _, l := range n.cfg.LANs {
		if n.isActive(l.ID) {
			mine = append(mine, l.ID)
		}
	}
	consider(n.cfg.Name, n.cfg.Owner, n.self, mine)
	n.mu.Lock()
	for _, p := range n.peers {
		if pp := pol.peers[p.addr]; pp != nil {
			consider(p.conf.Name, p.conf.Owner, p.addr, pp.lans)
		}
	}
	n.mu.Unlock()
	switch len(hits) {
	case 0:
		return netip.Addr{}, nil, fmt.Errorf("%s: no such device in your active LANs", q)
	case 1:
		return addrs[0], hits, nil
	}
	var list []string
	for _, h := range hits {
		list = append(list, fmt.Sprintf("%s (%s)", h.FQDN, strings.Join(h.LANs, ", ")))
	}
	return netip.Addr{}, hits, fmt.Errorf("%q is ambiguous, %d devices share that name: %s", q, len(hits), strings.Join(list, "; "))
}

func (n *node) isActive(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.active[id]
}

func (n *node) lanNames(ids []string) []string {
	var out []string
	for _, id := range ids {
		name := id
		for _, l := range n.cfg.LANs {
			if l.ID == id {
				name = l.Name
			}
		}
		out = append(out, name)
	}
	return out
}
