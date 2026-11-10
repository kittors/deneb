package model

import (
	"slices"
	"time"
)

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

	// M2 (DESIGN §6.7–§6.9). lan.invite also takes a Person, inviting them
	// by name; invite.accept and invite.decline take an Invitation instead
	// of a link.
	OpLANSetApproveDevices OpKind = "lan.setApproveDevices" // LAN, On
	OpLANSetApproveMembers OpKind = "lan.setApproveMembers" // LAN, On
	OpDeviceApprove        OpKind = "device.approve"        // LAN, Device
	OpDeviceReject         OpKind = "device.reject"         // LAN, Device
	OpMemberApprove        OpKind = "member.approve"        // LAN, Person
	OpMemberReject         OpKind = "member.reject"         // LAN, Person
	OpInviteRevoke         OpKind = "invite.revoke"         // Invite (an Invited's id), or LAN and Person
	OpDeviceRenew          OpKind = "device.renew"          // Device
	OpDeviceRename         OpKind = "device.rename"         // Device, Name
	OpDeviceSetKind        OpKind = "device.setKind"        // Device, DevKind
	// OpLANAddRelay is the reason's "加一个中转站": the LAN's devices that
	// nothing reaches go through a relay. The demo plays it; the real one
	// comes with relays in M4.
	OpLANAddRelay OpKind = "lan.addRelay" // LAN
	// OpLANPause pauses the LAN on this computer (On), or resumes it (off).
	// The engine does it, not the server.
	OpLANPause OpKind = "lan.pause" // LAN, On
	// OpMemberRemove takes a person out of a LAN of mine, their devices
	// with them: what freeing a seat does (DESIGN §6.11).
	OpMemberRemove OpKind = "member.remove" // LAN, Person

	// M3 (DESIGN §6.11).
	//
	// share.create gives one of my devices to one person, opening the
	// services named (keys); giving a device back that was taken back is
	// undo, and with no services named it comes back as it was.
	// share.revoke takes it back: by Share, or by Device and Person when the
	// view has not heard its id yet. share.release tells the server the
	// toast that offered undo went: the seat it kept comes free.
	// share.leave gives back, to whoever gave it, a device given to me.
	OpShareCreate  OpKind = "share.create"  // Device, Person, Services
	OpShareRevoke  OpKind = "share.revoke"  // Share, or Device and Person
	OpShareRelease OpKind = "share.release" // Share, or Device and Person
	OpShareLeave   OpKind = "share.leave"   // Share
	OpLANSetMode   OpKind = "lan.setMode"   // LAN, Mode

	// M4 (DESIGN §6.12). relay.add and relay.remove drag one of my relays
	// into a LAN and out; relay.kick and relay.unkick keep a person off a
	// relay of mine, or let them back; relay.join asks for an install
	// command, relay.revokeJoin takes one back that was not run.
	OpRelayAdd        OpKind = "relay.add"        // LAN, Relay
	OpRelayRemove     OpKind = "relay.remove"     // LAN, Relay
	OpRelayRename     OpKind = "relay.rename"     // Relay, Name
	OpRelayKick       OpKind = "relay.kick"       // Relay, Person
	OpRelayUnkick     OpKind = "relay.unkick"     // Relay, Person
	OpRelayJoin       OpKind = "relay.join"       // → Command, Join
	OpRelayRevokeJoin OpKind = "relay.revokeJoin" // Join
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
	On      bool   // service.set: open it, or close it; the approve switches
	Refuse  bool   // device.refuse: refuse them, or let them back

	DevKind    string // device.setKind
	Invitation string // invite.accept, invite.decline: an Invitation's ID

	// M3. service.set takes Share (or Person, for a share the view has not
	// heard the id of) instead of LAN, to switch a service in a share.
	Share    string
	Services []string // share.create: the keys of the services it opens
	Mode     LANMode  // lan.setMode

	// M4.
	Relay RelayID
	Join  string // relay.revokeJoin: a RelayJoin's ID
}

// Result is what an op that went through answers.
type Result struct {
	// Version is the one the op took effect in: the view keeps the op laid
	// over the snapshot until the snapshot is at least this new.
	Version uint64
	LAN     LANID  // lan.create, invite.accept: the LAN made or joined
	Link    string // lan.invite: the link to send
	Share   string // share.create: the share made, or given back (M3)
	// relay.join: the install command, and the join it is (M4).
	Command string
	Join    string
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
		if l == nil || l.Has(op.Device) || l.HasPending(op.Device) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		if d := s.Devices[op.Device]; l.Waiting || l.ApproveDevices && d != nil && d.Owner != l.Owner {
			// Brought in by someone but the owner, where the owner lets new
			// devices in, or by one who waits to be let in: it waits, as
			// the server will have it, rather than show in and then not.
			nl.Pending = append(slices.Clone(l.Pending), op.Device)
			return n
		}
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
		if l != nil && l.HasPending(op.Device) {
			// Mine, taken back while it waited to be let in.
			n, nl := s.withLAN(op.LAN)
			nl.Pending = slices.DeleteFunc(slices.Clone(l.Pending), func(d DeviceID) bool { return d == op.Device })
			return n
		}
		if l == nil || !l.Has(op.Device) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		// What it opened here stays with the LAN, so that undo brings it
		// all back.
		nl.Devices = slices.DeleteFunc(slices.Clone(l.Devices), func(d DeviceID) bool { return d == op.Device })
		return n
	case OpServiceSet:
		if op.LAN == "" && (op.Share != "" || op.Person != "") {
			return s.withShareService(op)
		}
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
	case OpLANPause:
		l := s.LAN(op.LAN)
		if l == nil || l.Paused == op.On {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Paused = op.On
		return n
	case OpLANSetApproveDevices, OpLANSetApproveMembers:
		l := s.LAN(op.LAN)
		if l == nil {
			return s
		}
		devices := op.Kind == OpLANSetApproveDevices
		if devices && l.ApproveDevices == op.On || !devices && l.ApproveMembers == op.On {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		if devices {
			nl.ApproveDevices = op.On
		} else {
			nl.ApproveMembers = op.On
		}
		return n
	case OpDeviceApprove, OpDeviceReject:
		l := s.LAN(op.LAN)
		if l == nil || !l.HasPending(op.Device) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Pending = slices.DeleteFunc(slices.Clone(l.Pending), func(d DeviceID) bool { return d == op.Device })
		if op.Kind == OpDeviceApprove {
			nl.Devices = append(slices.Clone(l.Devices), op.Device)
			if _, ok := l.Services[op.Device]; !ok {
				if d := s.Devices[op.Device]; d != nil {
					nl.Services[op.Device] = slices.Clone(d.Catalog)
				}
			}
		}
		return n
	case OpMemberApprove, OpMemberReject:
		l := s.LAN(op.LAN)
		if l == nil || !slices.Contains(l.PendingMembers, op.Person) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.PendingMembers = slices.DeleteFunc(slices.Clone(l.PendingMembers), func(p PersonID) bool { return p == op.Person })
		if op.Kind == OpMemberApprove {
			nl.Members = append(slices.Clone(l.Members), op.Person)
		}
		return n
	case OpLANInvite:
		// By name: they wait for their answer in the LAN's ball stack. By
		// link, nothing shows until someone uses it.
		l := s.LAN(op.LAN)
		if l == nil || op.Person == "" || slices.Contains(l.Members, op.Person) ||
			slices.ContainsFunc(l.Invited, func(v Invited) bool { return v.Person == op.Person }) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		nl.Invited = append(slices.Clone(l.Invited), Invited{Person: op.Person, By: s.Me})
		return n
	case OpInviteRevoke:
		for _, l := range s.LANs {
			i := slices.IndexFunc(l.Invited, func(v Invited) bool {
				return op.Invite != "" && v.Invite == op.Invite || op.Invite == "" && l.ID == op.LAN && v.Person == op.Person
			})
			if i < 0 {
				continue
			}
			n, nl := s.withLAN(l.ID)
			nl.Invited = slices.Delete(slices.Clone(l.Invited), i, i+1)
			return n
		}
		return s
	case OpInviteAccept, OpInviteDecline:
		// The LAN an accepted invitation brings comes with the store: only
		// the invitation goes at once.
		i := slices.IndexFunc(s.Invitations, func(v Invitation) bool { return op.Invitation != "" && v.ID == op.Invitation })
		if i < 0 {
			return s
		}
		n := s.clone()
		n.Invitations = slices.Delete(slices.Clone(s.Invitations), i, i+1)
		return n
	case OpShareCreate, OpShareRevoke, OpShareLeave, OpLANSetMode, OpMemberRemove:
		return s.withM3(op)
	case OpRelayAdd, OpRelayRemove, OpRelayRename, OpRelayKick, OpRelayUnkick, OpRelayJoin, OpRelayRevokeJoin:
		return s.withM4(op)
	case OpDeviceRenew, OpDeviceRename, OpDeviceSetKind:
		d := s.Devices[op.Device]
		if d == nil {
			return s
		}
		nd := *d
		switch op.Kind {
		case OpDeviceRenew:
			// The new dates come with the store; until then it no longer asks.
			if !d.Login.Due(time.Now()) {
				return s
			}
			nd.Login = Login{Expires: d.Login.Expires}
		case OpDeviceRename:
			if op.Name == "" || d.Name == op.Name {
				return s
			}
			nd.Name = op.Name
		case OpDeviceSetKind:
			if op.DevKind == "" || d.Kind == op.DevKind {
				return s
			}
			nd.Kind = op.DevKind
		}
		n := s.clone()
		n.Devices = cloneMap(s.Devices)
		n.Devices[op.Device] = &nd
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
