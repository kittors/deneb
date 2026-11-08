package proto

import (
	"net/netip"
	"time"
)

// UserView is one person's world, as their client draws it: their LANs,
// who and what is in them, all of their own devices, and their invites. The
// engine passes it to the client as it is. It carries the same version as
// the NetMap it came with.
//
// It holds nothing from LANs the person is not in. Another person's device
// shows only the LANs both are in, and only the services opened to them.
type UserView struct {
	Version uint64   `json:"version"`
	Me      PersonID `json:"me"`
	// People are the person and everyone in their LANs, sorted by ID.
	People []Person `json:"people"`
	// LANs are the LANs the person is in, in the order they joined them.
	LANs []LANView `json:"lans"`
	// Devices are all of the person's own devices, the ones in no LAN too
	// (the tray is "my devices that are not in this LAN"), and the others'
	// devices in the person's LANs. Sorted by ID.
	Devices []DeviceView `json:"devices"`
	// Relays are the ones the person's LANs use, sorted by ID.
	Relays []Relay `json:"relays"`
	Seats  Seats   `json:"seats"`
	// Invitations are the invites made out to this person that wait for an
	// answer: the card "阿杰邀请你加入「周末联机」", no link needed. Answer
	// with invite.accept or invite.decline and the invite's id. (M2)
	Invitations []InvitePreview `json:"invitations,omitempty"`
	// Features are what the server can do beyond M1, so a client hides the
	// controls a server cannot serve yet. An M1 server sends none. (M2)
	Features []string `json:"features,omitempty"`
}

// The features a server lists in UserView.Features.
const (
	// FeatureApprove: lan.setApproveDevices, lan.setApproveMembers,
	// device.approve, device.reject, member.approve, member.reject; the
	// fields LANView.approveDevices/approveMembers/pending, LANDevice.pending,
	// Member.State "pending".
	FeatureApprove = "approve"
	// FeatureInvitePerson: lan.invite with a person, invite.accept and
	// invite.decline with an invitation, invite.revoke by whoever made it;
	// UserView.invitations, LANView.invited.
	FeatureInvitePerson = "invitePerson"
	// FeatureRename: device.rename (the DNS label follows) and
	// device.setKind. M1 servers do both too, without the label.
	FeatureRename = "rename"
	// FeatureRenew: device.renew and DeviceView.login.
	FeatureRenew = "renew"
	// FeatureDNSName: device.setDNSName.
	FeatureDNSName = "dnsName"
)

// M2Features are the features of an M2 server.
var M2Features = []string{FeatureApprove, FeatureDNSName, FeatureInvitePerson, FeatureRename, FeatureRenew}

// Person is someone, as others see them. The owner color is not here: on
// everyone's own screen "me" is blue and the others get colors by a fixed
// rule on the client (DESIGN §3.3).
type Person struct {
	ID          PersonID `json:"id"`
	Name        string   `json:"name"`            // DNS label, the owner part of device names: "jie"
	DisplayName string   `json:"displayName"`     // "阿杰"
	Initial     string   `json:"initial"`         // what their ball shows: "杰"
	Email       string   `json:"email,omitempty"` // the person's own only
}

// LANView is one LAN, as one of its members sees it.
type LANView struct {
	ID        LANID     `json:"id"`
	Name      string    `json:"name"`
	Owner     PersonID  `json:"owner"`
	CreatedAt time.Time `json:"createdAt"`
	// Members are in the order they joined; the owner comes first.
	Members []Member `json:"members"`
	// Devices are in the order they joined.
	Devices []LANDevice `json:"devices"`
	Relays  []RelayID   `json:"relays"`
	// Invites are the LAN's pending invites: all of them for the owner,
	// the ones they made for any other member.
	Invites []InviteView `json:"invites,omitempty"`
	// Invited are the people an invite is made out to who have not
	// answered yet, the dashed ball "等他接受": the same invites as above,
	// by person. (M2)
	Invited []InvitedPerson `json:"invited,omitempty"`

	// ApproveDevices and ApproveMembers are the LAN's switches "新设备要同
	// 意" and "新成员要同意": a device, or a person, brought in by anyone but
	// the owner waits for the owner. Every member sees them. (M2)
	ApproveDevices bool `json:"approveDevices,omitempty"`
	ApproveMembers bool `json:"approveMembers,omitempty"`
	// Pending is set when the viewer is a member waiting for the owner:
	// the LAN shows only its name and owner until the owner agrees. (M2)
	Pending bool `json:"pending,omitempty"`
}

// Member is one person in a LAN. There are two roles only (DESIGN §2).
type Member struct {
	Person   PersonID    `json:"person"`
	Role     Role        `json:"role"`
	State    MemberState `json:"state"`
	JoinedAt time.Time   `json:"joinedAt"`
}

// Role is what a person is in a LAN.
type Role string

const (
	RoleOwner  Role = "owner"  // 拥有者: who made it
	RoleMember Role = "member" // 成员: everyone else
)

// MemberState is where a person is on their way into a LAN. A pending
// member shows up only for the LAN's owner (and, as LANView.Pending, for
// themselves); they and their devices are in no one's network map.
type MemberState string

const (
	MemberJoined  MemberState = "joined"  // 已加入
	MemberInvited MemberState = "invited" // 已邀请, from M2
	MemberPending MemberState = "pending" // 待同意, from M2
)

// LANDevice is one device in a LAN.
type LANDevice struct {
	Device   DeviceID  `json:"device"`
	JoinedAt time.Time `json:"joinedAt"`
	// Paused is set when the device paused this LAN on itself; it touches
	// no one else's devices.
	Paused bool `json:"paused,omitempty"`
	// Pending is set while the device waits for the LAN's owner (the dashed
	// ball). The owner sees every pending device, the device's owner sees
	// theirs, and no one else sees any; a pending device is in no network
	// map. (M2)
	Pending bool `json:"pending,omitempty"`
	// Services are, for my own devices, every service with whether it is on
	// in this LAN; for the others' devices, only the ones on.
	Services []ServiceState `json:"services"`
}

// ServiceState is a service and whether it is open in one LAN. Its key
// (Service.Key) is what service.set and the local /v1/open name it by.
type ServiceState struct {
	Service
	On bool `json:"on"`
}

// DeviceView is one device, as someone who can see it sees it.
type DeviceView struct {
	ID          DeviceID       `json:"id"`
	Owner       PersonID       `json:"owner"`
	Name        string         `json:"name"`        // DNS label: "nas"
	DisplayName string         `json:"displayName"` // "阿杰的 NAS"
	FQDN        string         `json:"fqdn"`        // "nas.jie.deneb.internal"
	Kind        DeviceKind     `json:"kind"`
	OS          OS             `json:"os"`
	Addrs       []netip.Prefix `json:"addrs"`
	Online      bool           `json:"online"`
	LastSeen    time.Time      `json:"lastSeen,omitzero"`
	// LANs are the LANs it is in that the viewer is in too, sorted by ID.
	LANs []LANID `json:"lans"`
	// Services is the catalog of the services set up on it; it is there for
	// the viewer's own devices only.
	Services []Service `json:"services,omitempty"`
	// Refused are the people its owner refused on it: there for the viewer's
	// own devices only. Refusals are per device (DESIGN §6.5).
	Refused []PersonID `json:"refused,omitempty"`
	// RefusesMe is set on another's device whose owner refused the viewer
	// on it: the card says "用不了这台电脑".
	RefusesMe bool `json:"refusesMe,omitempty"`
	// Login is how long the device's login lasts. (M2)
	Login LoginInfo `json:"login,omitzero"`
}

// LoginInfo is where a device's login stands. A device that keeps coming
// back renews it by itself, so the prompt shows only for one that has been
// away a long time. Others' devices carry only Expired. (M2)
type LoginInfo struct {
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// RenewFrom is when the interface starts to say "需要重新登录" on the
	// device: its owner can renew it with device.renew from any of their
	// devices.
	RenewFrom time.Time `json:"renewFrom,omitzero"`
	// Expired is set once it ran out: the device is in no network map, and
	// for others it reads "登录过期". Renewing, or logging in on it again,
	// brings it back as it was.
	Expired bool `json:"expired,omitempty"`
}

// InvitedPerson is someone invited by name who has not answered yet. (M2)
type InvitedPerson struct {
	Person    PersonID  `json:"person"`
	InvitedBy PersonID  `json:"invitedBy"`
	ExpiresAt time.Time `json:"expiresAt"`
	Invite    InviteID  `json:"invite"` // invite.revoke calls it off
}

// InviteView is an invite a LAN's owner made.
type InviteView struct {
	ID      InviteID `json:"id"`
	LAN     LANID    `json:"lan"`
	Inviter PersonID `json:"inviter"`
	// Person is whom the invite is for, when it was made out to someone
	// (a person's ball dropped on a LAN); only they can use it. (M2)
	Person    PersonID    `json:"person,omitempty"`
	URL       string      `json:"url"` // the link to send: https://<server>/i/<token>
	CreatedAt time.Time   `json:"createdAt"`
	ExpiresAt time.Time   `json:"expiresAt"`
	State     InviteState `json:"state"`
}

// InviteState is where an invite is. A link lets in one person.
type InviteState string

const (
	InvitePending  InviteState = "pending"
	InviteAccepted InviteState = "accepted"
	InviteDeclined InviteState = "declined"
	InviteRevoked  InviteState = "revoked"
	InviteExpired  InviteState = "expired"
)

// InvitePreview is what someone holding an invite link may know before they
// accept: the card "阿杰邀请你加入「周末联机」". It says nothing about who
// else is in the LAN or what is in it.
type InvitePreview struct {
	Invite    InviteID    `json:"invite"`
	LAN       LANID       `json:"lan"`
	LANName   string      `json:"lanName"`
	Inviter   Person      `json:"inviter"`
	State     InviteState `json:"state"`
	ExpiresAt time.Time   `json:"expiresAt"`
	// AlreadyIn is set when the asking person is in the LAN already.
	AlreadyIn bool `json:"alreadyIn,omitempty"`
	// NeedsApproval is set when accepting leaves the person waiting for
	// the LAN's owner: a member other than the owner invited them and the
	// LAN wants new members approved. (M2)
	NeedsApproval bool `json:"needsApproval,omitempty"`
}

// Seats is how many seats a person uses (pricing-and-relays §1.4): the
// distinct people who joined the LANs they own (themselves included) and,
// from M3, those they share devices with. People invited but not yet in do
// not count. M1 counts them; M3 starts to enforce the limit.
type Seats struct {
	Used     int        `json:"used"`
	Limit    int        `json:"limit"`
	Enforced bool       `json:"enforced"`
	People   []PersonID `json:"people"`
}
