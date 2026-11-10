package client

import (
	"slices"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

// M3's part of the live store (DESIGN §6.11): what the server's user view
// says of shares, the devices given to me, the LANs that only hand out
// devices and the seats, in the snapshot; and the share an op names.

// deviceFrom is a device of the user view as the snapshot has it.
func deviceFrom(d proto.DeviceView) *model.Device {
	md := &model.Device{
		ID: model.DeviceID(d.ID), Owner: model.PersonID(d.Owner), Name: d.DisplayName,
		Kind: string(d.Kind), OS: string(d.OS), FQDN: d.FQDN, Online: d.Online,
		RefusesMe: d.RefusesMe, Refused: map[model.PersonID]bool{}, PublicLAN: model.LANID(d.PublicLAN),
	}
	if md.Name == "" {
		md.Name = d.Name
	}
	for _, a := range d.Addrs {
		if a.Addr().Is4() && md.Addr == "" {
			md.Addr = a.Addr().String()
		} else if a.Addr().Is6() && md.Addr6 == "" {
			md.Addr6 = a.Addr().String()
		}
	}
	for _, p := range d.Refused {
		md.Refused[model.PersonID(p)] = true
	}
	// M2: when it was last online, and how long its login lasts.
	md.LastSeen = d.LastSeen
	md.Login = model.Login{Expires: d.Login.ExpiresAt, RenewFrom: d.Login.RenewFrom, Expired: d.Login.Expired}
	for _, svc := range d.Services {
		md.Catalog = append(md.Catalog, service(svc, false))
	}
	// M3: whom it is given to, each share with every service the device
	// has, the ones it opens to them on, as the card switches them.
	for _, sh := range d.Shares {
		open := map[proto.ServiceKey]bool{}
		for _, svc := range sh.Services {
			open[svc.Key()] = true
		}
		ms := model.Share{ID: string(sh.ID), Person: model.PersonID(sh.Person)}
		seen := map[proto.ServiceKey]bool{}
		for _, svc := range d.Services {
			seen[svc.Key()] = true
			ms.Services = append(ms.Services, service(svc, open[svc.Key()]))
		}
		for _, svc := range sh.Services {
			if !seen[svc.Key()] {
				ms.Services = append(ms.Services, service(svc, true))
			}
		}
		md.Shares = append(md.Shares, ms)
	}
	return md
}

// takeShared puts the devices given to me in the snapshot: each in
// Devices, and in Shared with where it comes from and what it opens to me.
func takeShared(s *model.Snapshot, v *proto.UserView) {
	for _, sd := range v.SharedWithMe {
		md := deviceFrom(sd.Device)
		if _, ok := s.Devices[md.ID]; !ok {
			s.Devices[md.ID] = md
		}
		sh := model.SharedDevice{Share: string(sd.Share), Device: md.ID, From: model.PersonID(sd.From), FromLAN: sd.FromLAN, At: sd.SharedAt}
		for _, svc := range sd.Services {
			sh.Services = append(sh.Services, service(svc, true))
		}
		s.Shared = append(s.Shared, sh)
	}
}

// seatsOf is the user view's seats as the snapshot has them.
func seatsOf(v proto.Seats) model.Seats {
	s := model.Seats{Enforced: v.Enforced, Limit: v.Limit, Used: v.Used, Left: v.Left}
	for _, p := range v.People {
		s.People = append(s.People, model.PersonID(p))
	}
	return s
}

// lanMode is a LAN's mode as the snapshot has it: a LAN, or one that only
// hands out devices.
func lanMode(m proto.LANMode) model.LANMode {
	if m == proto.LANModeShare {
		return model.ModeShare
	}
	return model.ModeLAN
}

// shareArgs fills in what an M3 op says of its share: the services
// share.create opens, and the share itself where the view named it by its
// device and person, as it does before the server has answered with its
// id.
func (l *Live) shareArgs(op model.Op, args *proto.OpArgs) error {
	switch op.Kind {
	case model.OpShareCreate:
		for _, k := range op.Services {
			args.Services = append(args.Services, proto.ServiceKey(k))
		}
	case model.OpShareRevoke, model.OpShareRelease, model.OpServiceSet:
		if op.Kind == model.OpServiceSet && op.LAN != "" || args.Share != "" {
			return nil
		}
		if op.Kind == model.OpServiceSet && op.Person == "" {
			return nil
		}
		id := l.shareID(op)
		if id == "" {
			// For share.release: nothing kept back to free, and the server
			// frees the seat in ten seconds anyway.
			return &model.Error{Code: model.ErrInvalid, Message: "找不到这次分享"}
		}
		args.Share = id
		// service.set in a share names no LAN, nor the person.
		if op.Kind == model.OpServiceSet {
			args.Person = ""
		}
	}
	return nil
}

// shareID finds the share of op.Device to op.Person: a live one in the
// view, or, for share.release, one this store took back.
func (l *Live) shareID(op model.Op) proto.ShareID {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := string(op.Device) + "/" + string(op.Person)
	if op.Kind == model.OpShareRelease {
		return l.taken[key]
	}
	if l.view == nil {
		return ""
	}
	i := slices.IndexFunc(l.view.Devices, func(d proto.DeviceView) bool { return d.ID == proto.DeviceID(op.Device) })
	if i < 0 {
		return ""
	}
	for _, sh := range l.view.Devices[i].Shares {
		if sh.Person == proto.PersonID(op.Person) {
			return sh.ID
		}
	}
	return ""
}

// sharesDone keeps the share each share.revoke that went through took
// back, by its device and person, for the share.release that may follow
// once the view no longer lists it.
func (l *Live) sharesDone(op model.Op, args proto.OpArgs, r model.Result) {
	if op.Kind != model.OpShareRevoke || args.Share == "" || op.Device == "" || op.Person == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.taken == nil {
		l.taken = map[string]proto.ShareID{}
	}
	l.taken[string(op.Device)+"/"+string(op.Person)] = args.Share
}
