package model

import "slices"

// OpKind names an operation (implementation.md §2.6.2). The strings are the
// control server's.
type OpKind string

const (
	OpLANCreate       OpKind = "lan.create"       // Name → LAN; this computer joins it too
	OpLANRename       OpKind = "lan.rename"       // LAN, Name
	OpLANInvite       OpKind = "lan.invite"       // LAN → Link
	OpInviteAccept    OpKind = "invite.accept"    // Invite → LAN; this computer joins it too
	OpInviteDecline   OpKind = "invite.decline"   // Invite
	OpLANAddDevice    OpKind = "lan.addDevice"    // LAN, Device
	OpLANRemoveDevice OpKind = "lan.removeDevice" // LAN, Device
	OpServiceSet      OpKind = "service.set"      // LAN, Device, Service, On
	OpDeviceRefuse    OpKind = "device.refuse"    // Device, Person, Refuse
)

// Op is one thing the person asks for: what a drag or a click turns into.
// Undo is the reverse op.
type Op struct {
	// ID is the idempotency key. The store makes one when it is empty and
	// keeps it across retries, so an op is applied once however often it is
	// sent.
	ID      string
	Kind    OpKind
	LAN     LANID
	Device  DeviceID
	Person  PersonID
	Name    string
	Invite  string // the link, or its token
	Service string // the service's Key
	On      bool   // service.set: open it, or close it
	Refuse  bool   // device.refuse: refuse them, or let them back
}

// Result is what an op that went through answers.
type Result struct {
	// Version is the one the op took effect in: the view keeps the op laid
	// over the snapshot until the snapshot is at least this new.
	Version uint64
	LAN     LANID  // lan.create, invite.accept: the LAN made or joined
	Link    string // lan.invite: the link to send
}

// With returns the snapshot as it will be once op takes effect, for the view
// to show at once while the store has not confirmed it (§2.7 step 5). An op
// whose outcome only the server knows (making a LAN, accepting an invite)
// changes nothing here, nor does one that is in effect already, so laying
// an op over a snapshot that has it is harmless. s itself never changes:
// what op changes is copied.
func (s *Snapshot) With(op Op) *Snapshot {
	switch op.Kind {
	case OpLANAddDevice:
		l := s.LAN(op.LAN)
		if l == nil || l.Has(op.Device) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Devices = append(slices.Clone(l.Devices), op.Device)
		// It brings what it opens to this LAN: what it opened here before,
		// or what it has set up.
		if _, ok := l.Services[op.Device]; !ok {
			if d := s.Devices[op.Device]; d != nil {
				nl.Services[op.Device] = slices.Clone(d.Catalog)
			}
		}
		return n
	case OpLANRemoveDevice:
		l := s.LAN(op.LAN)
		if l == nil || !l.Has(op.Device) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		// What it opened here stays with the LAN, so that undo brings it
		// all back.
		nl.Devices = slices.DeleteFunc(slices.Clone(l.Devices), func(d DeviceID) bool { return d == op.Device })
		return n
	case OpServiceSet:
		l := s.LAN(op.LAN)
		if l == nil {
			return s
		}
		i := slices.IndexFunc(l.Services[op.Device], func(v Service) bool { return v.Key == op.Service })
		if i >= 0 && l.Services[op.Device][i].On == op.On {
			return s
		}
		svcs := slices.Clone(l.Services[op.Device])
		if i < 0 {
			// One of this computer's own, opened for the first time: it
			// joins the list as it goes on.
			j := slices.IndexFunc(s.Local, func(v Service) bool { return v.Key == op.Service })
			if op.Device != s.Self || j < 0 {
				return s
			}
			svcs, i = append(svcs, s.Local[j]), len(svcs)
		}
		n, nl := s.withLAN(op.LAN)
		svcs[i].On = op.On
		nl.Services[op.Device] = svcs
		return n
	case OpDeviceRefuse:
		d := s.Devices[op.Device]
		if d == nil || d.Refused[op.Person] == op.Refuse {
			return s
		}
		n := s.clone()
		nd := *d
		nd.Refused = make(map[PersonID]bool, len(d.Refused)+1)
		for k, v := range d.Refused {
			nd.Refused[k] = v
		}
		if op.Refuse {
			nd.Refused[op.Person] = true
		} else {
			delete(nd.Refused, op.Person)
		}
		n.Devices = cloneMap(s.Devices)
		n.Devices[op.Device] = &nd
		return n
	case OpLANRename:
		l := s.LAN(op.LAN)
		if l == nil || l.Name == op.Name || op.Name == "" {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Name = op.Name
		return n
	}
	return s
}

// clone copies the snapshot's top level: the maps and lists in it are
// shared until whoever changes one copies it.
func (s *Snapshot) clone() *Snapshot {
	n := *s
	return &n
}

// withLAN returns a copy of s whose LAN of id is a copy too, to change, with
// its own Services map.
func (s *Snapshot) withLAN(id LANID) (*Snapshot, *LAN) {
	n := s.clone()
	n.LANs = slices.Clone(s.LANs)
	for i, l := range n.LANs {
		if l.ID == id {
			nl := *l
			nl.Services = cloneMap(l.Services)
			n.LANs[i] = &nl
			return n, &nl
		}
	}
	return n, nil
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	n := make(map[K]V, len(m)+1)
	for k, v := range m {
		n[k] = v
	}
	return n
}
