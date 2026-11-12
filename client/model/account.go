package model

import "slices"

// M7 (DESIGN §6.15): what deletes for good, and logging out.
const (
	// device.delete deletes one of my devices (a public one: its LAN's
	// owner does): logged out at once, out of every LAN, its shares with
	// it; logging in there again makes a new device, with a new address.
	OpDeviceDelete OpKind = "device.delete" // Device
	// account.delete deletes my account and what is mine. DryRun only says
	// what would go and what blocks it (Result.Account).
	OpAccountDelete OpKind = "account.delete" // DryRun, OwnedLANs, Confirm → Account
	// logout logs this computer out: the engine does it, not the server.
	// Logging in again, it keeps its address.
	OpLogout OpKind = "logout"
)

// What account.delete does with the LANs I own that have others in them.
const (
	AccountTransfer   = "transfer" // each to its member who joined first
	AccountDeleteLANs = "delete"   // each deleted, lost to everyone in it
)

// AccountDeletion is what account.delete deletes, or would (DryRun), as the
// server says (design/plan/m7/account-delete.md §2).
type AccountDeletion struct {
	DryRun      bool
	Devices     []DeviceID
	LANs        []LANID    // deleted: mine alone, or chosen to be
	Transferred []Transfer // AccountTransfer: who gets each
	Left        []LANID    // the others' LANs I leave
	Blocked     []LANID    // mine with others in them, nothing chosen (DryRun only)
	Relays      []RelayID
	RelaysInUse []RelayID // of Relays, those others use: they fall back on the public ones
	Shares      int
	Invites     int
}

// Transfer is one of my LANs handed over as my account goes.
type Transfer struct {
	LAN LANID
	To  PersonID
}

// withoutDevice is s with the device of id gone for good: out of every LAN,
// waiting or in, and out of mine.
func (s *Snapshot) withoutDevice(id DeviceID) *Snapshot {
	if s.Devices[id] == nil {
		return s
	}
	n := s.clone()
	n.Devices = cloneMap(s.Devices)
	delete(n.Devices, id)
	n.Paths = cloneMap(s.Paths)
	delete(n.Paths, id)
	not := func(d DeviceID) bool { return d == id }
	n.Mine = slices.DeleteFunc(slices.Clone(s.Mine), not)
	n.LANs = slices.Clone(s.LANs)
	for i, l := range n.LANs {
		if l.Has(id) || l.HasPending(id) {
			nl := *l
			nl.Devices = slices.DeleteFunc(slices.Clone(l.Devices), not)
			nl.Pending = slices.DeleteFunc(slices.Clone(l.Pending), not)
			n.LANs[i] = &nl
		}
	}
	return n
}
