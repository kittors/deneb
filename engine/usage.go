package engine

import (
	"net/netip"
	"slices"

	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// usesFor names the filter's uses with a network map: which device, which
// service as this device opened it, through which LANs (M2: "who is using
// which of my services", scene 5). Uses by addresses the map does not know
// are left out.
func usesFor(nm *proto.NetMap, us []filter.Use) []apitype.ServiceUse {
	out := []apitype.ServiceUse{}
	if nm == nil || len(us) == 0 {
		return out
	}
	byAddr := map[netip.Addr]*proto.Peer{}
	for i := range nm.Peers {
		if a := firstV4(nm.Peers[i].Addrs); a.IsValid() {
			byAddr[a] = &nm.Peers[i]
		}
	}
	for _, u := range us {
		p := byAddr[u.Peer]
		if p == nil {
			continue
		}
		ip := proto.TCP
		if u.Proto == filter.ProtoUDP {
			ip = proto.UDP
		}
		su := apitype.ServiceUse{DeviceID: apitype.DeviceID(p.ID), Scopes: []apitype.ScopeID{}, Proto: string(ip), Port: u.Port,
			Conns: u.Conns, Since: u.Since, LastSeen: u.LastSeen}
		for _, s := range nm.Scopes {
			if !slices.Contains(p.Scopes, s.ID) {
				continue
			}
			for _, svc := range s.Open {
				if svc.Allows(ip, u.Port) {
					su.Scopes = append(su.Scopes, apitype.ScopeID(s.ID))
					if su.Kind == "" {
						su.Kind, su.Name = string(svc.Kind), svc.Name
					}
					break
				}
			}
		}
		out = append(out, su)
	}
	return out
}

// usingFor names the peers' services this device uses (filter.Using): which
// device, which port, since when, through which of the LANs both are in.
// The engine does not know what the peer calls the service; the user view
// does.
func usingFor(nm *proto.NetMap, us []filter.Use, paused func(proto.Scope) bool) []apitype.ServiceUse {
	out := []apitype.ServiceUse{}
	if nm == nil || len(us) == 0 {
		return out
	}
	byAddr := map[netip.Addr]*proto.Peer{}
	for i := range nm.Peers {
		if a := firstV4(nm.Peers[i].Addrs); a.IsValid() {
			byAddr[a] = &nm.Peers[i]
		}
	}
	for _, u := range us {
		p := byAddr[u.Peer]
		if p == nil {
			continue
		}
		ip := proto.TCP
		if u.Proto == filter.ProtoUDP {
			ip = proto.UDP
		}
		su := apitype.ServiceUse{DeviceID: apitype.DeviceID(p.ID), Scopes: []apitype.ScopeID{}, Proto: string(ip), Port: u.Port,
			Conns: u.Conns, Since: u.Since, LastSeen: u.LastSeen}
		for _, s := range nm.Scopes {
			if slices.Contains(p.Scopes, s.ID) && !paused(s) {
				su.Scopes = append(su.Scopes, apitype.ScopeID(s.ID))
			}
		}
		out = append(out, su)
	}
	return out
}

// usageKey is what makes a change of usage worth an event: who uses which
// port over how many connections.
func usageKey(us []apitype.ServiceUse) string {
	var b []byte
	for _, u := range us {
		b = append(b, u.DeviceID...)
		b = append(b, ' ', byte(u.Port>>8), byte(u.Port), byte(u.Conns), ';')
		b = append(b, u.Proto...)
	}
	return string(b)
}
