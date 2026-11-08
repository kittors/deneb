package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"time"

	"github.com/kittors/deneb/proto"
)

// Addresses follow multi-lan-tech §3.3: one IPv4 and one IPv6 per device,
// the same in every LAN, from a /16 in 10.0.0.0/8 and a ULA /48 that each
// deployment picks at random once. Never 100.64.0.0/10 (Tailscale reacts to
// it) and never 198.18.0.0/15 (Clash's fake-ip range).

// secondOctets are the /16s in 10.0.0.0/8 a deployment may pick. Left out:
// 10.0–10.47 (home routers, offices, cloud defaults, and Weave's
// 10.32.0.0/12), 10.96–10.111 (Kubernetes services, 10.96.0.0/12),
// 10.128–10.255 (GCP's auto-mode subnets, 10.128.0.0/9), and the round or
// lucky numbers people pick by hand.
var secondOctets = func() []byte {
	var out []byte
	for b := 48; b <= 95; b++ {
		switch b {
		case 50, 55, 60, 64, 66, 70, 77, 80, 88, 90:
			continue
		}
		out = append(out, byte(b))
	}
	return out
}()

// addrCooldown is how long a deleted device's address waits before it is
// given out again (implementation.md §2.2).
const addrCooldown = 30 * 24 * time.Hour

// newNetwork picks a deployment's address space. ipv4 and ipv6 override
// the random picks when valid (a test deployment may want them fixed).
func newNetwork(ipv4, ipv6 string) (proto.Network, error) {
	var n proto.Network
	n.Domain = proto.Domain
	if ipv4 != "" {
		p, err := netip.ParsePrefix(ipv4)
		if err != nil {
			return n, err
		}
		if !p.Addr().Is4() || p.Bits() != 16 || !netip.MustParsePrefix("10.0.0.0/8").Contains(p.Addr()) {
			return n, fmt.Errorf("address block %s: want a /16 in 10.0.0.0/8", p)
		}
		n.IPv4 = p.Masked()
	} else {
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(secondOctets))))
		if err != nil {
			return n, err
		}
		n.IPv4 = netip.PrefixFrom(netip.AddrFrom4([4]byte{10, secondOctets[i.Int64()], 0, 0}), 16)
	}
	if ipv6 != "" {
		p, err := netip.ParsePrefix(ipv6)
		if err != nil {
			return n, err
		}
		if !p.Addr().Is6() || p.Bits() != 48 || p.Addr().As16()[0] != 0xfd {
			return n, fmt.Errorf("address block %s: want a ULA /48 in fd00::/8", p)
		}
		n.IPv6 = p.Masked()
	} else {
		// RFC 4193: fd, then a 40-bit random global id.
		var b [16]byte
		b[0] = 0xfd
		if _, err := rand.Read(b[1:6]); err != nil {
			return n, err
		}
		n.IPv6 = netip.PrefixFrom(netip.AddrFrom16(b), 48)
	}
	dns4 := hostAddr(n.IPv4, 0, 53)
	n.DNS = []netip.Addr{dns4, v6For(n.IPv6, dns4)}
	return n, nil
}

// hostAddr is the address .c.d in a /16.
func hostAddr(block netip.Prefix, c, d byte) netip.Addr {
	a := block.Addr().As4()
	a[2], a[3] = c, d
	return netip.AddrFrom4(a)
}

// v6For maps a device's IPv4 into the ULA /48: the last 32 bits are the
// IPv4 address, so the two always come and go together.
func v6For(block netip.Prefix, v4 netip.Addr) netip.Addr {
	b := block.Addr().As16()
	a := v4.As4()
	copy(b[12:], a[:])
	return netip.AddrFrom16(b)
}

var errAddrsExhausted = errors.New("no free address left in the deployment's block")

// allocAddr finds the lowest free device address: .1.1 up, never .x.0 or
// .x.255 (they look like network and broadcast addresses to people), not
// in use, and not released within the cooldown. .0.x is kept for the
// server side (the DNS address is .0.53).
func allocAddr(q querier, w *World, now time.Time) (netip.Addr, netip.Addr, error) {
	used := map[netip.Addr]bool{}
	for _, d := range w.devices {
		used[d.IPv4] = true
	}
	rows, err := q.QueryContext(context.Background(),
		`SELECT ipv4 FROM released_addrs WHERE released_at > ?`, ms(now.Add(-addrCooldown)))
	if err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return netip.Addr{}, netip.Addr{}, err
		}
		if a, err := netip.ParseAddr(s); err == nil {
			used[a] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	for c := 1; c <= 255; c++ {
		for d := 1; d <= 254; d++ {
			a := hostAddr(w.Network.IPv4, byte(c), byte(d))
			if !used[a] {
				return a, v6For(w.Network.IPv6, a), nil
			}
		}
	}
	return netip.Addr{}, netip.Addr{}, errAddrsExhausted
}

// releaseAddr starts a deleted device's address on its cooldown.
func releaseAddr(q querier, a netip.Addr, now time.Time) error {
	_, err := q.ExecContext(context.Background(),
		`INSERT INTO released_addrs (ipv4, released_at) VALUES (?, ?)
		 ON CONFLICT (ipv4) DO UPDATE SET released_at = excluded.released_at`, a.String(), ms(now))
	return err
}
