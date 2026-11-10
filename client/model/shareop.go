package model

import "slices"

// withM3 is With for M3's ops (DESIGN §6.11).
func (s *Snapshot) withM3(op Op) *Snapshot {
	switch op.Kind {
	case OpShareCreate:
		d := s.Devices[op.Device]
		if d == nil || op.Person == "" || op.Person == d.Owner || d.ShareTo(op.Person) != nil {
			return s
		}
		nd := *d
		sh := Share{Person: op.Person}
		for _, svc := range s.ServicesOf(op.Device) {
			// Every service it has, the ones named on: the card lists them
			// all, to switch.
			svc.On = slices.Contains(op.Services, svc.Key)
			sh.Services = append(sh.Services, svc)
		}
		nd.Shares = append(slices.Clone(d.Shares), sh)
		if d.Refused[op.Person] {
			// Giving it says yes to them: a refusal of them on it goes.
			nd.Refused = cloneMap(d.Refused)
			delete(nd.Refused, op.Person)
		}
		n := s.clone()
		n.Devices = cloneMap(s.Devices)
		n.Devices[op.Device] = &nd
		if s.Seats.Enforced && !s.Seats.Holds(op.Person) {
			n.Seats.People = append(slices.Clone(s.Seats.People), op.Person)
			n.Seats.Used++
			n.Seats.Left--
		}
		return n
	case OpShareRevoke:
		id, i := s.findShare(op)
		if id == "" {
			return s
		}
		d := s.Devices[id]
		nd := *d
		nd.Shares = slices.Delete(slices.Clone(d.Shares), i, i+1)
		n := s.clone()
		n.Devices = cloneMap(s.Devices)
		n.Devices[id] = &nd
		// The seat stays until the toast goes (share.release): it comes
		// with the store.
		return n
	case OpShareLeave:
		i := slices.IndexFunc(s.Shared, func(sh SharedDevice) bool {
			return op.Share != "" && sh.Share == op.Share || op.Share == "" && sh.Device == op.Device
		})
		if i < 0 {
			return s
		}
		n := s.clone()
		n.Shared = slices.Delete(slices.Clone(s.Shared), i, i+1)
		return n
	case OpLANSetMode:
		l := s.LAN(op.LAN)
		if l == nil || l.Mode == op.Mode {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Mode = op.Mode
		if op.Mode == ModeShare {
			// Only the owner's devices and the LAN's own stay.
			nl.Devices = slices.DeleteFunc(slices.Clone(l.Devices), func(id DeviceID) bool {
				d := s.Devices[id]
				return d != nil && d.Owner != l.Owner && d.PublicLAN != l.ID
			})
			nl.Pending = slices.DeleteFunc(slices.Clone(l.Pending), func(id DeviceID) bool {
				d := s.Devices[id]
				return d != nil && d.Owner != l.Owner
			})
		}
		return n
	case OpMemberRemove:
		l := s.LAN(op.LAN)
		if l == nil || op.Person == l.Owner || !slices.Contains(l.Members, op.Person) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Members = slices.DeleteFunc(slices.Clone(l.Members), func(p PersonID) bool { return p == op.Person })
		theirs := func(id DeviceID) bool { d := s.Devices[id]; return d != nil && d.Owner == op.Person }
		nl.Devices = slices.DeleteFunc(slices.Clone(l.Devices), theirs)
		nl.Pending = slices.DeleteFunc(slices.Clone(l.Pending), theirs)
		return n
	}
	return s
}

// withShareService is With for service.set in a share: the service goes on
// or off for that person alone.
func (s *Snapshot) withShareService(op Op) *Snapshot {
	id, i := s.findShare(op)
	if id == "" {
		return s
	}
	d := s.Devices[id]
	sh := d.Shares[i]
	j := slices.IndexFunc(sh.Services, func(v Service) bool { return v.Key == op.Service })
	if j < 0 || sh.Services[j].On == op.On {
		return s
	}
	sh.Services = slices.Clone(sh.Services)
	sh.Services[j].On = op.On
	nd := *d
	nd.Shares = slices.Clone(d.Shares)
	nd.Shares[i] = sh
	n := s.clone()
	n.Devices = cloneMap(s.Devices)
	n.Devices[id] = &nd
	return n
}

// findShare finds the share an op names, by its id or by its device and
// person: the device it is of, and where in its Shares; "" if none.
func (s *Snapshot) findShare(op Op) (DeviceID, int) {
	for id, d := range s.Devices {
		for i, sh := range d.Shares {
			if op.Share != "" && sh.ID == op.Share || op.Share == "" && id == op.Device && sh.Person == op.Person {
				return id, i
			}
		}
	}
	return "", -1
}

// ServicesOf is every service one of my devices has, each once, as it is in
// its first LAN or else in its catalog: what a share of it can open.
func (s *Snapshot) ServicesOf(dev DeviceID) []Service {
	var out []Service
	seen := map[string]bool{}
	add := func(v Service) {
		if !seen[v.Key] {
			seen[v.Key] = true
			out = append(out, v)
		}
	}
	for _, l := range s.LANs {
		for _, v := range l.Services[dev] {
			add(v)
		}
	}
	if d := s.Devices[dev]; d != nil {
		for _, v := range d.Catalog {
			add(v)
		}
	}
	return out
}

// OpenOf are the keys of the services one of my devices opens now: on in
// any of its LANs, or, in none, on in its catalog. A share of it opens
// these to begin with (DESIGN §6.11).
func (s *Snapshot) OpenOf(dev DeviceID) []string {
	var keys []string
	inLAN := false
	for _, l := range s.LANs {
		if !l.Has(dev) {
			continue
		}
		inLAN = true
		for _, v := range l.Services[dev] {
			if v.On && !slices.Contains(keys, v.Key) {
				keys = append(keys, v.Key)
			}
		}
	}
	if d := s.Devices[dev]; d != nil && !inLAN {
		for _, v := range d.Catalog {
			if v.On {
				keys = append(keys, v.Key)
			}
		}
	}
	return keys
}
