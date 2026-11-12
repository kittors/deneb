package client

import (
	"context"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

// M7 (DESIGN §6.15): deleting a device goes as any op does (device.delete);
// deleting the account takes three arguments of its own and answers what
// went; logging this computer out is the engine's, not the server's.

// logout logs this computer out on its engine: the server keeps the device,
// and its address, for the next login.
func (l *Live) logout(ctx context.Context) (model.Result, error) {
	if err := l.lc.Logout(ctx); err != nil {
		return model.Result{}, modelError(err)
	}
	return model.Result{}, nil
}

// accountArgs puts account.delete's arguments in args: only say what
// would go; what becomes of my LANs with others in them; my email again.
func accountArgs(op model.Op, args *proto.OpArgs) {
	if op.Kind != model.OpAccountDelete {
		return
	}
	args.DryRun, args.OwnedLANs, args.Confirm = op.DryRun, op.OwnedLANs, op.Confirm
}

// accountAnswer is account.delete's answer as the view takes it, or nil.
func accountAnswer(a *proto.AccountDeletion) *model.AccountDeletion {
	if a == nil {
		return nil
	}
	m := &model.AccountDeletion{DryRun: a.DryRun, Shares: a.Shares, Invites: a.Invites}
	for _, id := range a.Devices {
		m.Devices = append(m.Devices, model.DeviceID(id))
	}
	lans := func(ids []proto.LANID) []model.LANID {
		var out []model.LANID
		for _, id := range ids {
			out = append(out, model.LANID(id))
		}
		return out
	}
	m.LANs, m.Left, m.Blocked = lans(a.LANs), lans(a.Left), lans(a.Blocked)
	for _, t := range a.Transferred {
		m.Transferred = append(m.Transferred, model.Transfer{LAN: model.LANID(t.LAN), To: model.PersonID(t.To)})
	}
	relays := func(ids []proto.RelayID) []model.RelayID {
		var out []model.RelayID
		for _, id := range ids {
			out = append(out, model.RelayID(id))
		}
		return out
	}
	m.Relays, m.RelaysInUse = relays(a.Relays), relays(a.RelaysInUse)
	return m
}
