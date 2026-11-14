package control

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/kittors/deneb/proto"
)

func TestDeviceAvatarOwnershipAndPersistence(t *testing.T) {
	w := newM2World(t)
	var b bytes.Buffer
	png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 160, 80)))
	args := proto.OpArgs{Device: w.mac.id, Avatar: b.Bytes()}
	w.jdesk.refused(proto.ErrForbidden, proto.OpDeviceSetAvatar, args)
	w.mac.must(proto.OpDeviceSetAvatar, args)
	have := w.s.state.Load().w.devices[w.mac.id].Avatar
	if len(have) == 0 {
		t.Fatal("avatar not persisted")
	}
	var stored []byte
	if err := w.s.db.QueryRow(`SELECT avatar FROM devices WHERE id = ?`, w.mac.id).Scan(&stored); err != nil || !bytes.Equal(stored, have) {
		t.Fatalf("database avatar: %v", err)
	}
	nm := w.s.state.Load().w.netMap(w.mac.id, 1, nil)
	if !bytes.Equal(nm.Self.Avatar, have) {
		t.Fatal("self network map dropped avatar")
	}
	var seen bool
	for _, d := range w.jdesk.view().Devices {
		if d.ID == w.mac.id {
			seen = bytes.Equal(d.Avatar, have)
		}
	}
	if !seen {
		t.Fatal("member did not receive uploaded avatar")
	}
	w.mac.refused(proto.ErrInvalid, proto.OpDeviceSetAvatar, proto.OpArgs{Device: w.mac.id, Avatar: []byte("<svg/>")})
	w.mac.must(proto.OpDeviceSetKind, proto.OpArgs{Device: w.mac.id, Kind: proto.KindDesktop})
	if len(w.s.state.Load().w.devices[w.mac.id].Avatar) != 0 {
		t.Fatal("built-in icon did not restore glyph")
	}
	w.mac.must(proto.OpDeviceSetAvatar, args)
	w.mac.must(proto.OpDeviceSetAvatar, proto.OpArgs{Device: w.mac.id})
	if len(w.s.state.Load().w.devices[w.mac.id].Avatar) != 0 {
		t.Fatal("avatar removal not persisted")
	}
}
func TestRemovePublishedService(t *testing.T) {
	w := newM2World(t)
	makeService := func(port uint16) proto.Service {
		return proto.Service{Kind: proto.ServiceWeb, Name: "量子大屏", Ports: []proto.PortRange{proto.Port(proto.TCP, port)}}
	}
	old, main := makeService(19080), makeService(80)
	for _, s := range []proto.Service{old, main} {
		w.mac.must(proto.OpServiceSet, proto.OpArgs{Device: w.mac.id, LAN: w.weekend, Service: &s, On: true})
	}
	w.jdesk.refused(proto.ErrForbidden, proto.OpServiceRemove, proto.OpArgs{Device: w.mac.id, Service: &old})
	w.mac.must(proto.OpServiceRemove, proto.OpArgs{Device: w.mac.id, Service: &old})
	d := w.s.state.Load().w.devices[w.mac.id]
	if len(d.services) != 1 || d.services[0].Key() != main.Key() {
		t.Fatalf("wrong service removed: %+v", d.services)
	}
	for _, opened := range d.opens {
		if opened[old.Key()] {
			t.Fatal("removed service still authorized")
		}
	}
	w.mac.must(proto.OpServiceRemove, proto.OpArgs{Device: w.mac.id, Service: &old})
}
