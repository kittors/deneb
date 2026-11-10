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
	// Relays are the ones the person's LANs and shares use, and their own
	// (M4), sorted by ID.
	Relays []Relay `json:"relays"`
	// MyRelays are the person's own relays, for the tray and their cards
	// (M4). Sorted by ID.
	MyRelays []RelayView `json:"myRelays,omitempty"`
	// RelayJoins are the person's join codes not used yet, oldest first
	// (M4): "等待安装", each with a way to take it back.
	RelayJoins []RelayJoinView `json:"relayJoins,omitempty"`
	// Operator is there for the deployment's operators only, and is what
	// shows the "运营" page (DESIGN §14.12 M4-13): the public relays, to
	// manage as people manage their own. (M4)
	Operator *OperatorView `json:"operator,omitempty"`
	// LoggedOutDevices are the person's devices that were logged out (and
	// the public devices of their LANs that were), for "已登出" and a way to
	// delete them (device.delete). They are in no map and in no one else's
	// view; the LANs are the ones they are still in, for when they log in
	// again. Sorted by ID. (M7)
	LoggedOutDevices []DeviceView `json:"loggedOutDevices,omitempty"`
	Seats            Seats        `json:"seats"`
	// Invitations are the invites made out to this person that wait for an
	// answer: the card "阿杰邀请你加入「周末联机」", no link needed. Answer
	// with invite.accept or invite.decline and the invite's id. (M2)
	Invitations []InvitePreview `json:"invitations,omitempty"`
	// Features are what the server can do beyond M1, so a client hides the
	// controls a server cannot serve yet. An M1 server sends none. (M2)
	Features []string `json:"features,omitempty"`
	// SharedWithMe are the devices others gave this person, one by one:
	// the sidebar's "共享给我". (M3)
	SharedWithMe []SharedDevice `json:"sharedWithMe,omitempty"`
}

// SharedDevice is a device given to the viewer (M3). It says what the
// viewer may use and nothing more: not the LANs the device is in, not
// whom else it is shared with, not the services kept closed.
type SharedDevice struct {
	Share  ShareID    `json:"share"`
	Device DeviceView `json:"device"`
	// From is who gave it. For a public device FromLAN names its LAN, as
	// the sidebar writes "工作室 · 直接连上".
	From     PersonID  `json:"from"`
	FromLAN  string    `json:"fromLAN,omitempty"`
	Services []Service `json:"services"` // what it opens to the viewer
	SharedAt time.Time `json:"sharedAt"`
}

// ShareView is one share of a device, as whoever manages the device sees
// it (M3).
type ShareView struct {
	ID        ShareID   `json:"id"`
	Person    PersonID  `json:"person"` // whom it is given to
	Sharer    PersonID  `json:"sharer"`
	Services  []Service `json:"services"` // what it opens to them
	CreatedAt time.Time `json:"createdAt"`
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
	// FeatureShare: share.create, share.revoke, service.set on a share;
	// UserView.SharedWithMe, DeviceView.Shares. (M3)
	FeatureShare = "share"
	// FeaturePublicDevice: device.setPublic, DeviceView.PublicLAN. (M3)
	FeaturePublicDevice = "publicDevice"
	// FeatureSeats: seats are enforced; Seats.Left, InvitePreview.SeatsFull,
	// the error seats_full; share.release. (M3)
	FeatureSeats = "seats"
	// FeatureShareMode: lan.setMode and LANView.Mode; share.leave. (M3)
	FeatureShareMode = "shareMode"
	// FeatureRelays: relay.create and the other relay ops, MyRelays,
	// LANView.AddedRelays, the relay usage. (M4)
	FeatureRelays = "relays"
)

// M2Features are the features of an M2 server.
var M2Features = []string{FeatureApprove, FeatureDNSName, FeatureInvitePerson, FeatureRename, FeatureRenew}

// M3Features are the features of an M3 server: M2's and three more.
var M3Features = []string{FeatureApprove, FeatureDNSName, FeatureInvitePerson, FeaturePublicDevice, FeatureRename, FeatureRenew, FeatureSeats, FeatureShare, FeatureShareMode}

// M4Features are the features of an M4 server: M3's and the relays.
var M4Features = []string{FeatureApprove, FeatureDNSName, FeatureInvitePerson, FeaturePublicDevice, FeatureRelays, FeatureRename, FeatureRenew, FeatureSeats, FeatureShare, FeatureShareMode}

// RelayView is one of my relays as I, its owner, see it (M4). Its card
// says where it came from, what it can see and how much it carried; the
// words come from the relay's Source and Owner, and the amounts from
// RelayUsage, asked for when the card opens.
type RelayView struct {
	ID RelayID `json:"id"`
	// LANs are where it is dragged in, sorted by ID.
	LANs []LANID `json:"lans"`
	// Registered: the relay program has signed in once, so the relay has
	// an address and a pin and is on its LANs' lists. Until then the card
	// says how to set it up.
	Registered bool `json:"registered"`
	// TokenExpiresAt is when the registration token from relay.create or
	// relay.token runs out, while one is waiting to be used.
	TokenExpiresAt time.Time `json:"tokenExpiresAt,omitzero"`
	// Online: a Deneb relay checked in within the last few minutes; one
	// that never checks in (Seeded, the derper on hk-spare) is online while
	// a device online says it is connected to it. LastSeen, while a Deneb
	// relay is offline, is when it last checked in.
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	// Seeded: the deployment set it up (the derper on hk-spare), so it
	// cannot be renamed, deleted or set up again from the client.
	Seeded bool `json:"seeded,omitempty"`
	// Since is when it first registered: "刚上线" for a while after.
	Since time.Time `json:"since,omitzero"`
	// Weight and Capacity are its owner's settings (relay.setWeight,
	// relay.setCapacity); Full, that it is at capacity or its CPU is, so
	// devices are put elsewhere.
	Weight   int  `json:"weight"`
	Capacity int  `json:"capacity,omitempty"`
	Full     bool `json:"full,omitempty"`
	// Default: the deployment's seed file lists it, so every LAN tries it
	// in the seed's order before the weighted public relays; its weight
	// moves nothing, and its name and setting up are the seed file's.
	Default bool `json:"default,omitempty"`
}

// OperatorView is what a deployment operator manages on the ops page
// (DESIGN §14.12 M4-13). The numbers are in GET /api/v1/operator/relay-usage.
type OperatorView struct {
	// Relays are the public relays, as MyRelays shows one's own, sorted
	// by ID. A new one starts as a standby (weight 0).
	Relays []RelayView `json:"relays"`
	// Joins are the join codes for public relays not used yet, oldest
	// first: "等待安装", each with relay.revokeJoin to take it back.
	Joins []RelayJoinView `json:"joins"`
}

// OperatorUsage is the public relays' month and now, for the ops page
// (DESIGN §14.12 M4-13). It never says whose devices or who with whom;
// of the people only the heaviest few, by traffic alone.
type OperatorUsage struct {
	Month string `json:"month"` // "2026-11", in UTC
	// People and Devices are everyone on a public relay now, each counted
	// once however many they are on: "现在 128 个人、342 台设备在经它们中转".
	People  int `json:"people"`
	Devices int `json:"devices"`
	// Relays are the public relays, sorted by ID: Bytes, Top, Users, Load
	// and Kicks; LANs, Shares and Devices stay empty and Connected out.
	Relays []RelayMonth `json:"relays"`
}

// PersonBytes is how much one person sent and received through a relay.
type PersonBytes struct {
	Person Person `json:"person"` // no email
	Bytes  int64  `json:"bytes"`
}

// RelayUsage is what my relays carried this month (M4): "这个月替周末联机
// 转了多少". It is counted per device and never who with whom
// (pricing-and-relays §2.3, §2.4).
type RelayUsage struct {
	Month  string       `json:"month"` // "2026-11", in UTC
	Relays []RelayMonth `json:"relays"`
}

// RelayMonth is one relay's month.
type RelayMonth struct {
	Relay RelayID `json:"relay"`
	// Bytes is all it carried: what devices sent through it.
	Bytes int64 `json:"bytes"`
	// LANs are what its devices in each LAN that uses the relay sent
	// through it. A device in two such LANs counts in both, so the client
	// says "约".
	LANs []UsageLine `json:"lans"`
	// Shares are what the receiver's devices of each of my shares sent and
	// received through it.
	Shares []UsageLine `json:"shares"`
	// Devices are what each device I see sent and received through it.
	Devices []UsageLine `json:"devices"`
	// Load is how it is doing, from its last sync; nil for a relay that
	// has not synced since the server started (the seed's never does).
	Load *RelayLoad `json:"load,omitempty"`
	// Connected are who is on it now: the devices I see, and only the
	// person for the others (a share's receiver's devices).
	Connected []RelayClient `json:"connected,omitempty"`
	// Kicks are the kicks in force, and the ones lifted in the last 30
	// days, newest first.
	Kicks []RelayKick `json:"kicks,omitempty"`
	// Top and Users are a public relay's, on the ops page only: this
	// month's heaviest users, most first, at most five ("用得最多的"), and
	// how many people used it at all ("还有 81 个人" is Users − len(Top)).
	Top   []PersonBytes `json:"top,omitempty"`
	Users int           `json:"users,omitempty"`
}

// RelayLoad is a relay's load at its last sync.
type RelayLoad struct {
	At          time.Time `json:"at"`
	Devices     int       `json:"devices"` // on it now
	People      int       `json:"people"`
	Connections int       `json:"connections"`
	BytesPerSec int64     `json:"bytesPerSec"`
	CPU         float64   `json:"cpu"` // 0–1
	Memory      int64     `json:"memory"`
	MemoryLimit int64     `json:"memoryLimit,omitempty"`
	Uptime      int64     `json:"uptime"` // seconds
	Full        bool      `json:"full,omitempty"`
}

// RelayClient is someone on a relay now.
type RelayClient struct {
	Device DeviceID `json:"device,omitempty"`
	Person PersonID `json:"person"`
}

// RelayKick is a person or a device kicked off a relay (relay.kick).
type RelayKick struct {
	ID       string    `json:"id"`
	Person   PersonID  `json:"person,omitempty"`
	Device   DeviceID  `json:"device,omitempty"`
	By       PersonID  `json:"by,omitempty"` // empty: the deployment's admin
	At       time.Time `json:"at"`
	LiftedAt time.Time `json:"liftedAt,omitzero"`
	LiftedBy PersonID  `json:"liftedBy,omitempty"`
}

// UsageLine is one LAN's, share's or device's amount.
type UsageLine struct {
	LAN    LANID    `json:"lan,omitempty"`
	Share  ShareID  `json:"share,omitempty"`
	Device DeviceID `json:"device,omitempty"`
	Bytes  int64    `json:"bytes"`
}

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
	// Relays are the relays the LAN uses, in the order its devices try
	// them: the ones dragged in (AddedRelays), then the fallback, the
	// deployment's default and public ones.
	Relays []RelayID `json:"relays"`
	// AddedRelays are the relays people dragged in, in the order they
	// did (M4): each is its owner's, and dragging it back out is
	// relay.remove.
	AddedRelays []RelayID `json:"addedRelays,omitempty"`
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
	// Mode is how the LAN works (M3): as a LAN, where members' devices see
	// one another (scene 5), or for sharing devices, where it holds the
	// owner's and the public devices and members get only what is shared
	// with them (scene 4). A member of a sharing LAN sees its name and its
	// owner, not its devices or its other members; the devices they are
	// given show in UserView.SharedWithMe. Empty means "lan".
	Mode LANMode `json:"mode,omitempty"`
}

// LANMode is how a LAN works (M3).
type LANMode string

const (
	LANModeLAN   LANMode = "lan"   // 局域网模式: members' devices see one another
	LANModeShare LANMode = "share" // 设备共享模式: members get devices shared with them
)

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
	// Shares are whom the device is given to, live shares only: there for
	// whoever manages it (its owner; for a public device, its LAN's
	// owner). (M3)
	Shares []ShareView `json:"shares,omitempty"`
	// PublicLAN is set on a public device: the LAN it belongs to; others
	// see it as "工作室的设备". (M3)
	PublicLAN LANID `json:"publicLAN,omitempty"`
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
	// SeatsFull is set when the owner's seats are full: accepting leaves
	// the person waiting until the owner frees one ("阿杰的位置满了，腾出来
	// 就能加入", pricing-and-relays §1.5 item 5). (M3)
	SeatsFull bool `json:"seatsFull,omitempty"`
	// Members are who joined the LAN, without emails, and Devices how many
	// devices are in it, for the card: "里面有 3 个人，5 台设备". Only in a
	// link's preview (the invite endpoints), only while the invite still
	// works or for someone in already: an old link tells nothing of the
	// LAN any more. A view's Invitations leave them out. (M3)
	Members []Person `json:"members,omitempty"`
	Devices int      `json:"devices"`
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
	// Left is Limit minus Used, when Enforced: at 2 or fewer the "+" reads
	// "还能加 2 人", at 0 it turns into the dashed circle (§1.5). (M3)
	Left int `json:"left"`
}
