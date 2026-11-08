package model

import (
	"reflect"
	"slices"
	"testing"
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
