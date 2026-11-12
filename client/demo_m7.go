package client

import (
	"slices"
	"strings"

	"github.com/kittors/deneb/client/model"
)

// M7 in the demo (DESIGN §6.15): deleting a device, deleting the account,
// logging this computer out, as the M7 server and the engine do them.

// demoEmail is the email the demo's kittors signed in with.
const demoEmail = "kittors@example.com"

// demoSeedRefusal is the server's words for an account its deployment's seed
// names (design/plan/m7/account-delete.md §2).
const demoSeedRefusal = "这个账号写在部署的种子文件里，服务器每次启动都会把它建回来：要注销，先请部署管理员把它从种子文件里拿掉"

// m7Event plays one of M7's outside events on s, or returns s:
//   - "m7": the account as M7 has it: my email; I am in 桌游夜 too (Leo's),
//     and have 家里 of my own, no one else in it.
//   - "m7-seed": the deployment's seed names my account: deleting it is
//     refused, the dry run too.
func (d *Demo) m7Event(s *model.Snapshot, event string) *model.Snapshot {
	switch event {
	case "m7":
		n := *s
		n.Email = demoEmail
		m := withInvitedLAN(&n)
		m, _ = withNewLAN(m, "家里")
		return m
	case "m7-seed":
		d.m7seed = true
		n := *s
		return &n
	}
	return s
}

// m7Op decides one of M7's ops, as the M7 server and the engine would.
func (d *Demo) m7Op(s *model.Snapshot, op model.Op, res *model.Result) (*model.Snapshot, error) {
	switch op.Kind {
	case model.OpDeviceDelete:
		if op.Device == s.Self {
			// This computer: logged out at once, as the server has it.
			return loggedOut(s), nil
		}
		return s.With(op), nil
	case model.OpLogout:
		return loggedOut(s), nil
	case model.OpAccountDelete:
		if d.m7seed {
			return s, &model.Error{Code: model.ErrForbidden, Message: demoSeedRefusal}
		}
		if op.OwnedLANs != "" && op.OwnedLANs != model.AccountTransfer && op.OwnedLANs != model.AccountDeleteLANs {
			return s, &model.Error{Code: model.ErrInvalid, Message: "ownedLANs 只能是 transfer 或 delete"}
		}
		acc := accountDeletion(s, op.OwnedLANs)
		acc.DryRun = op.DryRun
		res.Account = acc
		if op.DryRun {
			return s, nil
		}
		if len(acc.Blocked) > 0 {
			var names []string
			for _, id := range acc.Blocked {
				names = append(names, "「"+s.LAN(id).Name+"」")
			}
			return s, &model.Error{Code: model.ErrConflict, Message: "你是" + strings.Join(names, "、") + "的拥有者，里面还有别人：先选好转给别人还是整个删掉，再注销"}
		}
		if !strings.EqualFold(strings.TrimSpace(op.Confirm), s.Email) {
			return s, &model.Error{Code: model.ErrInvalid, Message: "要注销，请再输一遍你的邮箱"}
		}
		return loggedOut(s), nil
	}
	return s, nil
}

// loggedOut is s once this computer is logged out: the window goes to its
// login.
func loggedOut(s *model.Snapshot) *model.Snapshot {
	n := *s
	n.LoggedIn, n.Loaded = false, false
	return &n
}

// accountDeletion says what deleting my account deletes, the LANs I own
// that have others in them as owned says (§4.1 of the design).
func accountDeletion(s *model.Snapshot, owned string) *model.AccountDeletion {
	acc := &model.AccountDeletion{Devices: slices.Clone(s.Mine)}
	for _, l := range append(slices.Clone(s.LANs), s.ShareLANs...) {
		if l.Owner != s.Me {
			acc.Left = append(acc.Left, l.ID)
			continue
		}
		var others []model.PersonID
		for _, m := range l.Members {
			if m != s.Me {
				others = append(others, m)
			}
		}
		switch {
		case len(others) == 0:
			acc.LANs = append(acc.LANs, l.ID)
		case owned == model.AccountTransfer:
			acc.Transferred = append(acc.Transferred, model.Transfer{LAN: l.ID, To: others[0]})
		case owned == model.AccountDeleteLANs:
			acc.LANs = append(acc.LANs, l.ID)
		default:
			acc.Blocked = append(acc.Blocked, l.ID)
		}
	}
	for id, r := range s.Relays {
		if r.Owner != s.Me || r.Public() {
			continue
		}
		acc.Relays = append(acc.Relays, id)
		inUse := len(r.Using) > 0
		for _, l := range s.LANs {
			if l.Added(id) && len(l.Members) > 1 {
				inUse = true
			}
		}
		if inUse {
			acc.RelaysInUse = append(acc.RelaysInUse, id)
		}
	}
	slices.Sort(acc.Relays)
	slices.Sort(acc.RelaysInUse)
	for _, id := range s.Mine {
		if dev := s.Devices[id]; dev != nil {
			acc.Shares += len(dev.Shares)
		}
	}
	acc.Shares += len(s.Shared)
	acc.Invites = len(s.Invitations)
	for _, l := range s.LANs {
		for _, inv := range l.Invited {
			if inv.By == s.Me {
				acc.Invites++
			}
		}
	}
	return acc
}
