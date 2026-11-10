package client

import (
	"fmt"
	"slices"
	"time"

	"github.com/kittors/deneb/client/model"
)

// M3 in the demo (DESIGN §6.11): Simulate("m3") brings it on the sample,
// as an M3 server would: 阿明 gave me his NAS, and 设计部, his LAN that
// only hands out devices, its GPU 主机; my seats are held to six, five of
// them taken (me, Mia, 阿杰, Leo, 小林).

// demoM3 is what the demo keeps for M3's ops beyond the snapshot: the
// shares taken back, to give back as they were (undo), the people whose
// seat a share taken back keeps until its toast goes (share.release), and
// which of the demo's seat limits is on.
type demoM3 struct {
	revoked map[string]model.Share // by device + "/" + person
	held    map[model.PersonID]bool
	limit   int // index into demoLimits
}

// demoLimits are the seat limits ⌥⌘S steps through: six, as the product
// gives; then seven and ten, to show "还能加 2 人" and nothing at all.
var demoLimits = []int{6, 7, 10}

// m3Op decides one of M3's ops, as the M3 server does, or returns an error
// it would answer with.
func (d *Demo) m3Op(s *model.Snapshot, op model.Op) (*model.Snapshot, error) {
	if d.m3.revoked == nil {
		d.m3.revoked, d.m3.held = map[string]model.Share{}, map[model.PersonID]bool{}
	}
	switch op.Kind {
	case model.OpShareCreate:
		dev := s.Devices[op.Device]
		if dev == nil {
			return s, nil
		}
		if dev.ShareTo(op.Person) != nil {
			return s, &model.Error{Code: model.ErrConflict, Message: "已经有了"}
		}
		if s.Seats.Full() && !s.Seats.Holds(op.Person) {
			return s, &model.Error{Code: model.ErrSeatsFull, Message: fmt.Sprintf("%d 个位置都用上了", s.Seats.Limit)}
		}
		key := string(op.Device) + "/" + string(op.Person)
		old, had := d.m3.revoked[key]
		n := s.With(op)
		nd := *n.Devices[op.Device]
		nd.Shares = slices.Clone(nd.Shares)
		for i := range nd.Shares {
			if nd.Shares[i].Person != op.Person {
				continue
			}
			nd.Shares[i].ID = "s-" + key
			if had {
				// Given back after it was taken back: as it was (undo).
				nd.Shares[i].ID = old.ID
				if op.Services == nil {
					nd.Shares[i].Services = old.Services
				}
			}
		}
		n.Devices = maps(n.Devices)
		n.Devices[op.Device] = &nd
		delete(d.m3.revoked, key)
		delete(d.m3.held, op.Person)
		return d.reseat(n), nil
	case model.OpShareRevoke, model.OpShareRelease:
		for id, dev := range s.Devices {
			for _, sh := range dev.Shares {
				if op.Share != "" && sh.ID == op.Share || op.Share == "" && id == op.Device && sh.Person == op.Person {
					if op.Kind == model.OpShareRelease {
						return s, nil // not taken back: nothing to free
					}
					d.m3.revoked[string(id)+"/"+string(sh.Person)] = sh
					d.m3.held[sh.Person] = true
					return d.reseat(s.With(op)), nil
				}
			}
		}
		if op.Kind == model.OpShareRelease {
			for key, sh := range d.m3.revoked {
				if op.Share != "" && sh.ID == op.Share || op.Share == "" && key == string(op.Device)+"/"+string(op.Person) {
					delete(d.m3.held, sh.Person)
				}
			}
			return d.reseat(s), nil
		}
		return s, nil
	case model.OpMemberRemove:
		return d.reseat(s.With(op)), nil
	}
	return s.With(op), nil
}

// reseat counts my seats anew, as the server does: me, the people in the
// LANs I made, those I gave a device to, and those a share taken back still
// keeps a seat for.
func (d *Demo) reseat(s *model.Snapshot) *model.Snapshot {
	if !s.Seats.Enforced {
		return s
	}
	people := []model.PersonID{s.Me}
	add := func(p model.PersonID) {
		if p != "" && !slices.Contains(people, p) {
			people = append(people, p)
		}
	}
	for _, l := range s.LANs {
		if l.Owner == s.Me {
			for _, p := range l.Members {
				add(p)
			}
		}
	}
	for _, id := range s.Mine {
		if dev := s.Devices[id]; dev != nil {
			for _, sh := range dev.Shares {
				add(sh.Person)
			}
		}
	}
	held := make([]model.PersonID, 0, len(d.m3.held))
	for p := range d.m3.held {
		held = append(held, p)
	}
	slices.Sort(held)
	for _, p := range held {
		add(p)
	}
	n := *s
	n.Seats.People = people
	n.Seats.Used = len(people)
	n.Seats.Left = max(n.Seats.Limit-len(people), 0)
	if slices.Equal(people, s.Seats.People) && n.Seats.Left == s.Seats.Left {
		return s
	}
	return &n
}

// m3Event plays one of M3's outside events on s (see Simulate), or returns
// s:
//
//   - "m3": M3 comes on, as above.
//   - "seats": the next of demoLimits.
//   - "share-in": 小美 gives me her printer, or takes it back.
//   - "invitation-full": Leo invites me into 桌游夜 by name, his seats full.
func (d *Demo) m3Event(s *model.Snapshot, event string, now time.Time) *model.Snapshot {
	n := *s
	switch event {
	case "m3":
		if s.Can(model.FeatureShare) {
			return s
		}
		n.Features = maps(s.Features)
		for _, f := range []string{model.FeatureShare, model.FeatureSeats, model.FeatureShareMode, model.FeaturePublicDevice} {
			n.Features[f] = true
		}
		n.People = maps(s.People)
		n.People["ming"] = &model.Person{ID: "ming", Name: "阿明", Initial: "明", Handle: "ming", Color: 5}
		n.People["mei"] = &model.Person{ID: "mei", Name: "小美", Initial: "美", Handle: "mei", Color: 1}
		n.Devices = maps(s.Devices)
		files := model.Service{Key: "tcp/445", Kind: "files", Name: "共享文件夹", Proto: "tcp", Port: 445, Ports: "445", On: true}
		web := model.Service{Key: "tcp/8080", Kind: "web", Name: "网页", Proto: "tcp", Port: 8080, Ports: "8080", On: true}
		screen := model.Service{Key: "tcp/3389", Kind: "screen", Name: "远程桌面", Proto: "tcp", Port: 3389, Ports: "3389", On: true}
		n.Devices["mingnas"] = &model.Device{ID: "mingnas", Owner: "ming", Name: "阿明的 NAS", Kind: "nas", OS: "linux",
			Addr: "10.67.0.31", FQDN: "nas.ming.deneb.internal", Online: true, Refused: map[model.PersonID]bool{}}
		n.Devices["gpu"] = &model.Device{ID: "gpu", Owner: "ming", Name: "GPU 主机", Kind: "server", OS: "linux",
			Addr: "10.67.0.32", FQDN: "gpu.design.deneb.internal", Online: true, PublicLAN: "design", Refused: map[model.PersonID]bool{}}
		n.Shared = []model.SharedDevice{
			{Share: "s-gpu", Device: "gpu", From: "ming", FromLAN: "设计部", Services: []model.Service{screen}, At: now.Add(-50 * time.Hour)},
			{Share: "s-mingnas", Device: "mingnas", From: "ming", Services: []model.Service{files, web}, At: now.Add(-26 * time.Hour)},
		}
		n.ShareLANs = []*model.LAN{{ID: "design", Name: "设计部", Owner: "ming", Members: []model.PersonID{"ming", s.Me},
			Mode: model.ModeShare, Services: map[model.DeviceID][]model.Service{}}}
		n.Paths = maps(s.Paths)
		n.Paths["gpu"] = model.Path{Kind: model.PathDirect, RTT: 19 * time.Millisecond}
		n.Paths["mingnas"] = model.Path{Kind: model.PathRelay, Relay: "hk", RTT: 58 * time.Millisecond}
		n.Seats = model.Seats{Enforced: true, Limit: demoLimits[0]}
		d.m3.limit = 0
		return d.reseat(&n)
	case "seats":
		if !s.Seats.Enforced {
			return s
		}
		d.m3.limit = (d.m3.limit + 1) % len(demoLimits)
		n.Seats.Limit = demoLimits[d.m3.limit]
		n.Seats.People = nil // counted anew
		return d.reseat(&n)
	case "share-in":
		if !s.Can(model.FeatureShare) {
			return s
		}
		if s.SharedOf("meiprint") != nil {
			n.Shared = slices.DeleteFunc(slices.Clone(s.Shared), func(sh model.SharedDevice) bool { return sh.Device == "meiprint" })
			return &n
		}
		n.Devices = maps(s.Devices)
		n.Devices["meiprint"] = &model.Device{ID: "meiprint", Owner: "mei", Name: "小美的打印机", Kind: "other", OS: "other",
			Addr: "10.67.0.33", FQDN: "printer.mei.deneb.internal", Online: true, Refused: map[model.PersonID]bool{}}
		n.Shared = append(slices.Clone(s.Shared), model.SharedDevice{Share: "s-meiprint", Device: "meiprint", From: "mei",
			Services: []model.Service{{Key: "tcp/631", Kind: "other", Name: "打印", Proto: "tcp", Port: 631, Ports: "631", On: true}}, At: now})
		n.Paths = maps(s.Paths)
		n.Paths["meiprint"] = model.Path{Kind: model.PathConnecting, Since: now}
		if !d.manual {
			time.AfterFunc(1600*time.Millisecond, func() { d.Simulate("share-in-up") })
		}
		return &n
	case "share-in-up":
		if s.SharedOf("meiprint") == nil || s.Paths["meiprint"].Kind != model.PathConnecting {
			return s
		}
		n.Paths = maps(s.Paths)
		n.Paths["meiprint"] = model.Path{Kind: model.PathDirect, RTT: 7 * time.Millisecond, Since: now}
		return &n
	case "invitation-full":
		if len(s.Invitations) > 0 || s.LAN(demoInviteLAN) != nil {
			return s
		}
		n.Invitations = []model.Invitation{{ID: "inv-boardgame", LAN: demoInviteLAN, LANName: "桌游夜",
			Inviter: *s.People["leo"], Expires: now.Add(7 * 24 * time.Hour), SeatsFull: true}}
		return &n
	}
	return s
}
