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
	// Share names a share, for share.revoke and service.set. (M3)
	Share ShareID `json:"share,omitempty"`
	// Services are what share.create opens: keys from the device's
	// catalog. (M3)
	Services []ServiceKey `json:"services,omitempty"`
	// Mode is lan.setMode's new mode. (M3)
	Mode LANMode `json:"mode,omitempty"`
	// Relay is the relay a relay op is about. (M4)
	Relay RelayID `json:"relay,omitempty"`
	// Join is the join code relay.revokeJoin takes back: its id. (M4)
	Join string `json:"join,omitempty"`
	// Weight and Capacity are relay.setWeight's and relay.setCapacity's.
	// (M4)
	Weight   *int `json:"weight,omitempty"`
	Capacity *int `json:"capacity,omitempty"`
	// Kick is the kick relay.unkick lifts. (M4)
	Kick string `json:"kick,omitempty"`
	// Public: relay.join makes a code for a new public relay rather than
	// one of the asker's own. The deployment's operators only. (M4)
	Public bool `json:"public,omitempty"`
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
	// From M3, Share instead of LAN switches the service in a share.
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
	// Device. The device's owner (a public device's: its LAN's owner), from
	// any of their devices or a browser, logged in or logged out
	// (UserView.LoggedOutDevices): the device goes for good. Its place in
	// its LANs, its shares, what it opened, whom it refused and its kicks go
	// with it; its address cools down 30 days before anyone gets it. One
	// online is logged out at once, as when someone else logs in on its
	// machine: its stream ends with StreamFrame.LoggedOut, its calls get
	// ErrNotLoggedIn, and logging in there again makes a new device with a
	// new address. There is no undo: a client that offers one holds the op
	// back until its toast goes. (M7)
	OpDeviceDelete OpType = "device.delete"
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

	// M3.

	// Device, Person, optionally Services → Share. The device's owner; for
	// a public device, its LAN's owner. The person's devices see this one
	// device and nothing else of the giver's; they reach it, it does not
	// reach them. Giving it to someone who has it is refused ("已经有了");
	// giving it again after share.revoke brings the share back as it was,
	// which is undo. A person new to the giver's seats needs a free seat
	// (seats_full otherwise). Giving it cancels a refusal of the person on
	// the device.
	OpShareCreate OpType = "share.create"
	// Share. Whoever gave it, or manages the device now. The person keeps
	// their seat while the toast offers undo, so undo (share.create) cannot
	// find it taken (pricing-and-relays §1.5 item 7): until share.release,
	// or ten seconds at most.
	OpShareRevoke OpType = "share.revoke"
	// Share. Whoever revoked it, when the toast "已收回 · 撤销" goes without
	// undo: the seat it kept comes free at once.
	OpShareRelease OpType = "share.release"
	// Share. The person it was given to, who does not want it any more. The
	// giver hears nothing beyond the share going from their view; the seat
	// comes free (if nothing else keeps the person there).
	OpShareLeave OpType = "share.leave"
	// LAN, Mode. The owner. To "share", members' devices leave the LAN
	// (their shares stay) and no member's device comes in after; back to
	// "lan", members add their devices again themselves.
	OpLANSetMode OpType = "lan.setMode"
	// Device, LAN (empty: a person's device again). The device's owner, who
	// must own the LAN: it becomes the LAN's public device, managed by the
	// LAN's owner, in that LAN only.
	OpDeviceSetPublic OpType = "device.setPublic"

	// Relays (M4): "我的服务器", a relay on the person's own server.
	//
	// Name. Anyone: a new relay of theirs (ten at most), and a
	// registration token for the relay program, in Result.RelayToken, shown
	// this once. It works once, within a week; the relay is on no list
	// until its program registers with it.
	OpRelayCreate OpType = "relay.create"
	// Relay. The owner: a new registration token, to set the relay up
	// again (on a new machine, say). When it is used, the old program's
	// secret stops working.
	OpRelayToken OpType = "relay.token"
	// Relay, Name. The owner.
	OpRelayRename OpType = "relay.rename"
	// Relay. The owner: it leaves every LAN and share it was on.
	OpRelayDelete OpType = "relay.delete"
	// LAN, Relay. The relay's owner, in a LAN they are in (dragged in,
	// scene 2): the LAN's devices try it before the fallback.
	OpRelayAdd OpType = "relay.add"
	// LAN, Relay. The relay's owner or the LAN's owner: dragged back to the
	// tray. Undo is relay.add again.
	OpRelayRemove OpType = "relay.remove"
	// "用我自己的服务器", with no form to fill (M4): a join code and the
	// one-line install command that carries it, in Result.RelayJoin. It
	// works once, within an hour; the relay comes into being when the
	// command runs and registers with it, named after the region it
	// reports. Relay (optional): the owner's existing relay, to set it up
	// again on a new machine; it stays the same relay, LANs and all.
	// Name (optional): a name rather than the region's. Public: a code for
	// a new public relay, for the deployment's operators (the ops page);
	// it starts as a standby, each connection limited to 2 MB/s.
	//
	// On a public relay, the deployment's operators run relay.join (to set
	// it up again), relay.revokeJoin, relay.rename, relay.setWeight,
	// relay.setCapacity, relay.kick and relay.unkick as owners do on their
	// own; on one the seed file lists, only the kicks. No one drags a
	// public relay into a LAN or deletes it.
	OpRelayJoin OpType = "relay.join"
	// Join. The code's owner (an operator, for a public relay's): the code
	// no longer works.
	OpRelayRevokeJoin OpType = "relay.revokeJoin"
	// Relay, Weight (0–100; 1 by default). The owner: in a LAN with several
	// relays, each device's list puts one first, chosen by weight; 0 is a
	// standby, used only when the others cannot be.
	OpRelaySetWeight OpType = "relay.setWeight"
	// Relay, Capacity (devices at once; 0 is no limit). The owner: at
	// capacity the relay counts as full and is put last on lists.
	OpRelaySetCapacity OpType = "relay.setCapacity"
	// Relay, and Person or Device. The owner: off the relay at once and
	// from then on; recorded with who and when. Result.Kick.
	OpRelayKick OpType = "relay.kick"
	// Kick. The relay's owner: the kick is lifted, and recorded so.
	OpRelayUnkick OpType = "relay.unkick"
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
	Share  ShareID     `json:"share,omitempty"` // share.create (M3)
	Relay  RelayID     `json:"relay,omitempty"` // relay.create (M4)
	// RelayToken is the relay program's registration token, from
	// relay.create and relay.token: shown once, never again. (M4)
	RelayToken string `json:"relayToken,omitempty"`
	// RelayJoin is relay.join's code and install command. (M4)
	RelayJoin *RelayJoin `json:"relayJoin,omitempty"`
	Kick      string     `json:"kick,omitempty"` // relay.kick (M4)
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
