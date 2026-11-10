package model

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func world() *Snapshot {
	return &Snapshot{
		Version: 7,
		Me:      "me",
		Self:    "mac",
		LANs: []*LAN{{
			ID: "weekend", Name: "周末联机", Owner: "me",
			Members: []PersonID{"me", "jie"},
			Devices: []DeviceID{"mac", "nas"},
			Services: map[DeviceID][]Service{
				"mac": {{Key: "tcp/445", Kind: "files", Name: "共享文件夹"}},
				"nas": {{Key: "tcp/5000", Kind: "web", Name: "网站", On: true}},
			},
		}},
		Devices: map[DeviceID]*Device{
			"mac":  {ID: "mac", Owner: "me", Refused: map[PersonID]bool{}},
			"game": {ID: "game", Owner: "me", Catalog: []Service{{Key: "udp/27015", Kind: "game", On: true}}, Refused: map[PersonID]bool{}},
			"nas":  {ID: "nas", Owner: "jie"},
		},
		Mine:  []DeviceID{"mac", "game"},
		Local: []Service{{Key: "tcp/5900", Kind: "screen", Name: "远程桌面"}},
	}
}

// Every op shows on a copy: the snapshot it is laid over never changes, as
// the view may still be reading it.
func TestWithNeverChangesTheSnapshot(t *testing.T) {
	ops := []Op{
		{Kind: OpLANAddDevice, LAN: "weekend", Device: "game"},
		{Kind: OpLANRemoveDevice, LAN: "weekend", Device: "mac"},
		{Kind: OpServiceSet, LAN: "weekend", Device: "mac", Service: "tcp/445", On: true},
		{Kind: OpServiceSet, LAN: "weekend", Device: "mac", Service: "tcp/5900", On: true},
		{Kind: OpDeviceRefuse, Device: "mac", Person: "jie", Refuse: true},
		{Kind: OpLANRename, LAN: "weekend", Name: "周末"},
	}
	for _, op := range ops {
		s := world()
		before := world()
		if n := s.With(op); n == s {
			t.Errorf("%s changed nothing", op.Kind)
		}
		if !reflect.DeepEqual(s, before) {
			t.Errorf("%s changed the snapshot it was laid over", op.Kind)
		}
	}
}

func TestWith(t *testing.T) {
	s := world()
	n := s.With(Op{Kind: OpLANAddDevice, LAN: "weekend", Device: "game"})
	if l := n.LAN("weekend"); !slices.Equal(l.Devices, []DeviceID{"mac", "nas", "game"}) || !l.Services["game"][0].On {
		t.Errorf("game joined as %v, opening %v", l.Devices, l.Services["game"])
	}
	if !slices.Equal(n.Tray("weekend"), nil) {
		t.Errorf("the tray holds %v", n.Tray("weekend"))
	}
	n = s.With(Op{Kind: OpLANRemoveDevice, LAN: "weekend", Device: "mac"})
	if l := n.LAN("weekend"); l.Has("mac") || len(l.Services["mac"]) != 1 {
		t.Errorf("mac left as %v; what it opened there %v", l.Devices, l.Services["mac"])
	}
	n = s.With(Op{Kind: OpServiceSet, LAN: "weekend", Device: "mac", Service: "tcp/5900", On: true})
	if svcs := n.LAN("weekend").Services["mac"]; len(svcs) != 2 || svcs[1].Key != "tcp/5900" || !svcs[1].On {
		t.Errorf("opening what this computer listens on: %v", svcs)
	}
	if n := s.With(Op{Kind: OpServiceSet, LAN: "weekend", Device: "nas", Service: "tcp/22", On: true}); n != s {
		t.Error("a service no one has came out of nothing")
	}
	n = s.With(Op{Kind: OpDeviceRefuse, Device: "mac", Person: "jie", Refuse: true})
	if !n.Devices["mac"].Refused["jie"] {
		t.Error("阿杰 is not refused")
	}
	if back := n.With(Op{Kind: OpDeviceRefuse, Device: "mac", Person: "jie"}); back.Devices["mac"].Refused["jie"] {
		t.Error("阿杰 is not let back")
	}
}

// An op in effect already changes nothing, so the view can lay an op over a
// snapshot that has it, as one does between the store's confirmation and
// the snapshot that carries it.
func TestWithIsIdempotent(t *testing.T) {
	for _, op := range []Op{
		{Kind: OpLANAddDevice, LAN: "weekend", Device: "nas"},
		{Kind: OpLANRemoveDevice, LAN: "weekend", Device: "game"},
		{Kind: OpServiceSet, LAN: "weekend", Device: "nas", Service: "tcp/5000", On: true},
		{Kind: OpDeviceRefuse, Device: "mac", Person: "jie"},
		{Kind: OpLANCreate, Name: "工作室"},
		{Kind: OpInviteAccept, Invite: "deneb://join/x"},
	} {
		if s := world(); s.With(op) != s {
			t.Errorf("%s, in effect already, made a new snapshot", op.Kind)
		}
	}
}

// The path of a device the engine has said nothing about: this computer is
// itself; others are being connected to, or offline as the server says.
func TestPath(t *testing.T) {
	s := world()
	s.Devices["nas"].Online = true
	if k := s.Path("mac").Kind; k != PathSelf {
		t.Errorf("this computer is %s", k)
	}
	if k := s.Path("nas").Kind; k != PathConnecting {
		t.Errorf("an online device not reported yet is %s", k)
	}
	if k := s.Path("game").Kind; k != PathOffline {
		t.Errorf("an offline device is %s", k)
	}
	// A LAN made on another of my devices, without this computer: what is
	// in it, online, is apart, as nothing will connect to it.
	s.LANs = append(s.LANs, &LAN{ID: "lab", Name: "实验室", Owner: "me", Members: []PersonID{"me"}, Devices: []DeviceID{"game", "box"}})
	s.Devices["box"] = &Device{ID: "box", Owner: "me", Online: true}
	s.Devices["game"].Online = true
	if k := s.Path("box").Kind; k != PathApart {
		t.Errorf("a device in no LAN with this computer is %s", k)
	}
	if k := s.Path("nas").Kind; k != PathConnecting {
		t.Errorf("a device in a LAN with this computer is %s", k)
	}
}

// Colors are the same for a person on every start and in every LAN, and
// blue is mine alone.
func TestColorFor(t *testing.T) {
	if ColorFor("me", "me") != 0 {
		t.Error("I am not blue")
	}
	for _, id := range []PersonID{"jie", "mia", "leo", "p_7k7djadzcl"} {
		c := ColorFor("me", id)
		if c == 0 || c >= NumColors || c != ColorFor("someone", id) {
			t.Errorf("%s gets color %d", id, c)
		}
	}
}

// M2's ops show on a copy as they will be once in effect (DESIGN §6.7–§6.9).
func TestWithM2(t *testing.T) {
	s := world()
	s.People = map[PersonID]*Person{"me": {ID: "me"}, "jie": {ID: "jie"}, "lin": {ID: "lin"}}
	l := s.LANs[0]
	l.Pending = []DeviceID{"pi"}
	l.PendingMembers = []PersonID{"lin"}
	s.Devices["pi"] = &Device{ID: "pi", Owner: "jie", Catalog: []Service{{Key: "tcp/22", Kind: "shell", On: true}}}
	s.Invitations = []Invitation{{ID: "inv", LAN: "games", LANName: "桌游夜"}}
	s.Devices["game"].Login = Login{Expires: time.Now().Add(10 * 24 * time.Hour), RenewFrom: time.Now().Add(-time.Hour)}

	n := s.With(Op{Kind: OpDeviceApprove, LAN: "weekend", Device: "pi"})
	if nl := n.LAN("weekend"); !nl.Has("pi") || nl.HasPending("pi") || len(nl.Services["pi"]) != 1 {
		t.Errorf("approved, pi is in %v, waits in %v, opens %v", nl.Devices, nl.Pending, nl.Services["pi"])
	}
	n = s.With(Op{Kind: OpDeviceReject, LAN: "weekend", Device: "pi"})
	if nl := n.LAN("weekend"); nl.Has("pi") || nl.HasPending("pi") {
		t.Errorf("turned away, pi is in %v, waits in %v", nl.Devices, nl.Pending)
	}
	n = s.With(Op{Kind: OpLANRemoveDevice, LAN: "weekend", Device: "pi"})
	if nl := n.LAN("weekend"); nl.HasPending("pi") {
		t.Error("taken back while it waited, pi still waits")
	}
	n = s.With(Op{Kind: OpMemberApprove, LAN: "weekend", Person: "lin"})
	if nl := n.LAN("weekend"); !slices.Contains(nl.Members, "lin") || len(nl.PendingMembers) != 0 {
		t.Errorf("let in, members %v, waiting %v", nl.Members, nl.PendingMembers)
	}
	n = s.With(Op{Kind: OpMemberReject, LAN: "weekend", Person: "lin"})
	if nl := n.LAN("weekend"); slices.Contains(nl.Members, "lin") || len(nl.PendingMembers) != 0 {
		t.Errorf("turned away, members %v, waiting %v", nl.Members, nl.PendingMembers)
	}
	n = s.With(Op{Kind: OpLANInvite, LAN: "weekend", Person: "lin"})
	if nl := n.LAN("weekend"); len(nl.Invited) != 1 || nl.Invited[0].Person != "lin" || nl.Invited[0].By != "me" {
		t.Errorf("invited by name: %v", nl.Invited)
	}
	if back := n.With(Op{Kind: OpInviteRevoke, LAN: "weekend", Person: "lin"}); len(back.LAN("weekend").Invited) != 0 {
		t.Error("the invite taken back stays")
	}
	if n := s.With(Op{Kind: OpLANInvite, LAN: "weekend", Person: "jie"}); n != s {
		t.Error("a member was invited again")
	}
	if n := s.With(Op{Kind: OpInviteDecline, Invitation: "inv"}); len(n.Invitations) != 0 {
		t.Error("the invitation declined stays")
	}
	if n := s.With(Op{Kind: OpDeviceRenew, Device: "game"}); n.Devices["game"].Login.Due(time.Now()) {
		t.Error("renewed, the login still asks to be renewed")
	}
	if n := s.With(Op{Kind: OpDeviceRename, Device: "game", Name: "客厅的游戏机"}); n.Devices["game"].Name != "客厅的游戏机" {
		t.Error("not renamed")
	}
	if n := s.With(Op{Kind: OpDeviceSetKind, Device: "game", DevKind: "desktop"}); n.Devices["game"].Kind != "desktop" {
		t.Error("its kind did not change")
	}
	n = s.With(Op{Kind: OpLANSetApproveDevices, LAN: "weekend", On: true})
	if !n.LAN("weekend").ApproveDevices || n.LAN("weekend").ApproveMembers {
		t.Error("the switches did not change as asked")
	}
	if !reflect.DeepEqual(s.LAN("weekend").Pending, []DeviceID{"pi"}) {
		t.Error("an op changed the snapshot it was laid over")
	}
}

// A login asks to be renewed from RenewFrom on, and once it ran out.
func TestLoginDue(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		l   Login
		due bool
	}{
		{Login{}, false},
		{Login{RenewFrom: now.Add(time.Hour)}, false},
		{Login{RenewFrom: now.Add(-time.Hour)}, true},
		{Login{Expired: true}, true},
	} {
		if got := c.l.Due(now); got != c.due {
			t.Errorf("%+v: due %v", c.l, got)
		}
	}
}

// A device brought into a LAN whose owner lets new devices in waits for the
// owner, as the server has it, unless it is the owner's own; one brought in
// by someone who waits to be let in waits too.
func TestAddDeviceWaitsForTheOwner(t *testing.T) {
	s := &Snapshot{Me: "me", Devices: map[DeviceID]*Device{
		"mine": {ID: "mine", Owner: "me"}, "leos": {ID: "leos", Owner: "leo"},
	}, LANs: []*LAN{
		{ID: "books", Owner: "leo", ApproveDevices: true, Services: map[DeviceID][]Service{}},
		{ID: "open", Owner: "leo", Services: map[DeviceID][]Service{}},
		{ID: "waits", Owner: "leo", Waiting: true, Services: map[DeviceID][]Service{}},
	}}
	add := func(lan LANID, dev DeviceID) *LAN {
		return s.With(Op{Kind: OpLANAddDevice, LAN: lan, Device: dev}).LAN(lan)
	}
	if l := add("books", "mine"); !l.HasPending("mine") || l.Has("mine") {
		t.Errorf("mine into Leo's books: devices %v, waiting %v", l.Devices, l.Pending)
	}
	if l := add("books", "leos"); !l.Has("leos") || l.HasPending("leos") {
		t.Errorf("Leo's own into his books: devices %v, waiting %v", l.Devices, l.Pending)
	}
	if l := add("open", "mine"); !l.Has("mine") {
		t.Errorf("mine into a LAN that lets anything in: devices %v, waiting %v", l.Devices, l.Pending)
	}
	if l := add("waits", "mine"); !l.HasPending("mine") {
		t.Errorf("mine into a LAN I wait for: devices %v, waiting %v", l.Devices, l.Pending)
	}
	// Asked once, asking again changes nothing.
	n := s.With(Op{Kind: OpLANAddDevice, LAN: "books", Device: "mine"})
	if again := n.With(Op{Kind: OpLANAddDevice, LAN: "books", Device: "mine"}); len(again.LAN("books").Pending) != 1 {
		t.Errorf("asked twice: waiting %v", again.LAN("books").Pending)
	}
}
