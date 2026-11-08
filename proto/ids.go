package proto

import "strings"

// PersonID names a person: one account. Ids are opaque to everyone but the
// control server; they never change, so a client can key colors and saved
// layouts on them.
type PersonID string

// LANID names a LAN.
type LANID string

// DeviceID names a device. A device keeps its id (and its address) across
// logins by the same person on the same machine.
type DeviceID string

// InviteID names an invite. It is not the invite's link token: the id can be
// shown to members, the token is the secret that lets someone in.
type InviteID string

// RelayID names a relay. Empty means none.
type RelayID string

// ScopeID names a scope: a set of devices that may see one another, and
// that a device opens services to. In M1 every scope is a LAN, "lan/<id>";
// from M3 a share is one too, "share/<id>". Engines treat the id as opaque
// and never parse it to make a decision.
type ScopeID string

const lanScopePrefix = "lan/"

// LANScope is the scope of a LAN.
func LANScope(id LANID) ScopeID { return ScopeID(lanScopePrefix + string(id)) }

// LAN reports the LAN a scope is, if it is one.
func (s ScopeID) LAN() (LANID, bool) {
	id, ok := strings.CutPrefix(string(s), lanScopePrefix)
	if !ok || id == "" {
		return "", false
	}
	return LANID(id), true
}
