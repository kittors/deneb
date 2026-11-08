package proto

import "fmt"

// Op is one thing a person asks for: what a drag or a click on the client
// turns into (implementation.md §2.6.2). The client hands it to the engine
// (POST /v1/ops, engine/localapi/apitype.OpRequest has the same shape), and
// the engine passes it to the control server as it is (POST PathOps). Undo
// is the reverse op: the server keeps no undo log.
type Op struct {
	// ID is the idempotency key, made by whoever first sends the op. The
	// server applies an id once and answers a retry with the first answer,
	// so the engine may resend an op whose answer it never got.
	ID   string `json:"id"`
	Op   OpType `json:"op"`
	Args OpArgs `json:"args"`
}

// OpArgs are the arguments of every op in one struct; each OpType lists the
// fields it reads, and the others stay empty.
type OpArgs struct {
	LAN     LANID      `json:"lan,omitempty"`
	Device  DeviceID   `json:"device,omitempty"`
	Person  PersonID   `json:"person,omitempty"`
	Name    string     `json:"name,omitempty"`
	Kind    DeviceKind `json:"kind,omitempty"`
	Invite  string     `json:"invite,omitempty"`
	Service *Service   `json:"service,omitempty"`
	On      bool       `json:"on,omitempty"`
	Refuse  bool       `json:"refuse,omitempty"`
	// Invitation names an invite made out to the asker, from
	// UserView.Invitations, for invite.accept and invite.decline. (M2)
	Invitation InviteID `json:"invitation,omitempty"`
}

// OpType names an operation. Next to each: the args it reads, then what it
// answers with in OpResult.Result, and who may do it.
type OpType string

const (
	// M1.

	// Name → LAN. Anyone. The device that sent it joins the LAN too: the
	// computer in use is in every LAN made or joined on it (DESIGN §6.2).
	OpLANCreate OpType = "lan.create"
	OpLANRename OpType = "lan.rename" // LAN, Name. The owner.
	OpLANDelete OpType = "lan.delete" // LAN. The owner.

	// LAN, optionally Person → Invite, with its link. Any member; from M2
	// an invite can be made out to a Person (their ball dropped on the
	// LAN), and only they can use it. People the owner invites join at
	// once; people another member invites wait for the owner if the LAN
	// wants new members approved.
	OpLANInvite OpType = "lan.invite"
	// Invite (the link, or its token), or Invitation (the id of an invite
	// made out to the asker) → LAN. The device that sent it joins the LAN
	// too, as with lan.create, or waits with its owner.
	OpInviteAccept  OpType = "invite.accept"
	OpInviteDecline OpType = "invite.decline" // Invite or Invitation, as for invite.accept.
	// Invite (its InviteID, as in LANView.Invites and LANView.Invited). The
	// owner, or whoever made it: this is also how an invite made out to
	// someone is called off.
	OpInviteRevoke OpType = "invite.revoke"

	// LAN, Device. The device's owner, who must be in the LAN. If the LAN
	// wants new devices approved and they are not its owner, the device
	// waits for the owner (LANDevice.Pending).
	OpLANAddDevice    OpType = "lan.addDevice"
	OpLANRemoveDevice OpType = "lan.removeDevice" // LAN, Device. The device's owner; also takes back a pending one.

	// Device, LAN, Service, On. The device's owner; the device must be in
	// the LAN. A service set up for the first time joins the device's
	// catalog. Turning it off keeps it in the catalog, and taking the device
	// out of a LAN keeps what it opened there, so undo brings it all back.
	OpServiceSet OpType = "service.set"
	// Device, Person, Refuse (false lets them back). The device's owner.
	OpDeviceRefuse OpType = "device.refuse"

	// M2.

	// Device, Name (what people see; Chinese and spaces are fine). The
	// device's owner. The DNS label follows the new name when the name has
	// letters or digits to make one from, unless it was set by hand with
	// device.setDNSName.
	OpDeviceRename  OpType = "device.rename"
	OpDeviceSetKind OpType = "device.setKind" // Device, Kind. The device's owner.
	// Device, Name (the DNS label): its FQDN changes, and later renames
	// leave it alone. The device's owner, from the command line or "更多";
	// two of one person's devices cannot share it.
	OpDeviceSetDNSName OpType = "device.setDNSName"
	// Device. The device's owner, from any of their devices: the login
	// starts a new lifetime, and an expired device comes back ("点一下就续上").
	OpDeviceRenew OpType = "device.renew"
	// LAN, Person. The owner; the person's devices leave the LAN with them.
	// It also turns down a person waiting to join.
	OpMemberRemove OpType = "member.remove"
	// LAN. A member who is not the owner; their devices leave with them.
	OpLANLeave OpType = "lan.leave"

	OpLANSetApproveDevices OpType = "lan.setApproveDevices" // LAN, On. The owner: "新设备要同意".
	OpLANSetApproveMembers OpType = "lan.setApproveMembers" // LAN, On. The owner: "新成员要同意".
	OpDeviceApprove        OpType = "device.approve"        // LAN, Device. The owner lets a waiting device in.
	OpDeviceReject         OpType = "device.reject"         // LAN, Device. The owner turns a waiting device away.
	// LAN, Person. The owner lets a waiting person in, with the devices
	// that wait with them.
	OpMemberApprove OpType = "member.approve"
	// LAN, Person. The owner turns down a person waiting to join; the
	// devices waiting with them go too.
	OpMemberReject OpType = "member.reject"

	// Later: share.create, share.revoke (M3); relay.add, relay.remove (M4).
)

// OpResult is the answer to an op that went through. One that did not
// comes back as an ErrorBody with a 4xx status instead.
type OpResult struct {
	// Version is the version the change landed in. The client keeps its
	// "waiting for the server" overlay until a snapshot at least this new
	// arrives. An op that changed nothing answers with the current version.
	Version uint64    `json:"version"`
	Result  *OpOutput `json:"result,omitempty"`
}

// OpOutput is what some ops answer with, beyond the version.
type OpOutput struct {
	LAN    LANID       `json:"lan,omitempty"`
	Invite *InviteView `json:"invite,omitempty"`
}

// Error is how the control server says no. Code is for programs; Message is
// one sentence in Chinese the client shows as it is, such as
// "这是阿杰的设备，只有阿杰能移出". The engine passes both on unchanged.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// ErrorBody is the body of every answer that is not 2xx: {"error": {...}}.
type ErrorBody struct {
	Error *Error `json:"error"`
}

// ErrorCode says what kind of no it is. The strings are the same as the
// local interface's (engine/localapi/apitype), which adds its own, such as
// "offline" for a control server out of reach.
type ErrorCode string

const (
	ErrUnauthorized ErrorCode = "unauthorized"  // 401: no token, or it expired; get a new one and retry
	ErrNotLoggedIn  ErrorCode = "not_logged_in" // 403: the machine is known, but no one is logged in on it
	ErrForbidden    ErrorCode = "forbidden"     // 403: this person may not do this
	ErrNotFound     ErrorCode = "not_found"     // 404: no such thing, or not one this person can see
	ErrInvalid      ErrorCode = "invalid"       // 400: the request makes no sense
	ErrConflict     ErrorCode = "conflict"      // 409: it clashes with how things are: a used invite, a taken name
	ErrSeatsFull    ErrorCode = "seats_full"    // 403: from M3
	ErrInternal     ErrorCode = "internal"      // 500
)
