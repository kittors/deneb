package control

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/kittors/deneb/proto"
)

// Deleting an account (account.delete, M7; design/plan/m7/account-delete.md):
// the person goes, and with them what is theirs alone. Two things are not
// theirs alone. A LAN they own with other people in it waits on their
// choice: handed to the member who joined it first, or deleted for
// everyone. Their own relay, dragged into LANs others use, goes all the
// same: those devices fall back on the public relays, so the client only
// warns.

// accountPlan is what deleting an account does, worked out from the world
// before anything changes: a dry run says exactly what the deletion would.
type accountPlan struct {
	devices     []*device
	deleteLANs  []*lan
	transfer    []proto.LANTransfer
	leave       []*lan
	blocked     []*lan
	relays      []proto.RelayID
	relaysInUse []proto.RelayID
	shares      []string
	invites     int
}

// planAccountDeletion is what deleting person p's account does, owned
// being their choice for the LANs they own with others in them.
func (w *World) planAccountDeletion(p proto.PersonID, owned string) accountPlan {
	return w.planAccountDeletionEach(p, func(*lan) string { return owned })
}

// planAccountDeletionEach is planAccountDeletion with a choice for each
// such LAN (AccountTransfer, AccountDeleteLANs, or "" for none yet): the
// operator's console asks LAN by LAN.
func (w *World) planAccountDeletionEach(p proto.PersonID, choose func(*lan) string) accountPlan {
	var pl accountPlan
	for _, d := range w.devices {
		if d.Owner == p {
			pl.devices = append(pl.devices, d)
		}
	}
	slices.SortFunc(pl.devices, func(a, b *device) int { return cmp.Compare(a.ID, b.ID) })
	lans := make([]*lan, 0, len(w.lans))
	for _, l := range w.lans {
		lans = append(lans, l)
	}
	slices.SortFunc(lans, func(a, b *lan) int { return cmp.Compare(a.ID, b.ID) })
	for _, l := range lans {
		heir := l.heir(p)
		if l.Owner != p {
			// Joined, invited or waiting to be let in: the row goes.
			if l.memberOf[p] != nil {
				pl.leave = append(pl.leave, l)
			}
			continue
		}
		owned := ""
		if heir != "" {
			owned = choose(l)
		}
		switch {
		case heir == "" || owned == proto.AccountDeleteLANs:
			// Theirs alone (those invited or waiting are not in it yet), or
			// they chose so.
			pl.deleteLANs = append(pl.deleteLANs, l)
		case owned == proto.AccountTransfer:
			pl.transfer = append(pl.transfer, proto.LANTransfer{LAN: l.ID, To: heir})
		default:
			pl.blocked = append(pl.blocked, l)
		}
	}
	for id, rl := range w.relays {
		if rl.Owner != p || rl.Source == proto.RelayPublic {
			continue
		}
		pl.relays = append(pl.relays, id)
		// In use: one of the deployment's defaults, which every LAN with no
		// list of its own uses, or dragged into a LAN with others in it.
		pos, ok := w.relayPos[id]
		used := ok && pos >= 0
		for _, l := range lans {
			if !used && slices.Contains(l.added, id) && l.heir(p) != "" {
				used = true
			}
		}
		if used {
			pl.relaysInUse = append(pl.relaysInUse, id)
		}
	}
	slices.Sort(pl.relays)
	slices.Sort(pl.relaysInUse)
	for id, sh := range w.shares {
		if d := w.devices[sh.Device]; sh.Sharer == p || sh.Receiver == p || d != nil && d.Owner == p {
			pl.shares = append(pl.shares, id)
		}
	}
	slices.Sort(pl.shares)
	for _, i := range w.invites {
		if i.Inviter == p || i.Person == p || i.UsedBy == p {
			pl.invites++
		}
	}
	return pl
}

// heir is the member of l who joined it first, other than p: who gets it
// if p hands it over. Empty when no one else has joined.
func (l *lan) heir(p proto.PersonID) proto.PersonID {
	var first *member
	for _, m := range l.members {
		if m.Person == p || m.State != proto.MemberJoined {
			continue
		}
		if first == nil || m.JoinedAt.Before(first.JoinedAt) || m.JoinedAt.Equal(first.JoinedAt) && m.Person < first.Person {
			first = m
		}
	}
	if first == nil {
		return ""
	}
	return first.Person
}

func (pl accountPlan) view(dry bool) *proto.AccountDeletion {
	v := &proto.AccountDeletion{DryRun: dry, Devices: []proto.DeviceID{}, LANs: []proto.LANID{}, Left: []proto.LANID{},
		Transferred: pl.transfer, Relays: []proto.RelayID{}, RelaysInUse: pl.relaysInUse, Shares: len(pl.shares), Invites: pl.invites}
	for _, d := range pl.devices {
		v.Devices = append(v.Devices, d.ID)
	}
	for _, l := range pl.deleteLANs {
		v.LANs = append(v.LANs, l.ID)
	}
	for _, l := range pl.leave {
		v.Left = append(v.Left, l.ID)
	}
	for _, l := range pl.blocked {
		v.Blocked = append(v.Blocked, l.ID)
	}
	v.Relays = append(v.Relays, pl.relays...)
	return v
}

// seeded reports whether the deployment's seed names email, as an account
// or as a relay's owner: the next start would make the account again.
func (s *Server) seeded(email string) bool {
	for _, a := range s.cfg.Seed.Accounts {
		if normEmail(a.Email) == email {
			return true
		}
	}
	for _, r := range s.cfg.Seed.Relays {
		if r.OwnerEmail != "" && normEmail(r.OwnerEmail) == email {
			return true
		}
	}
	return false
}

func opAccountDelete(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	p := c.w.people[c.me]
	if p == nil {
		return errNotFound("找不到这个账号")
	}
	if a.OwnedLANs != "" && a.OwnedLANs != proto.AccountTransfer && a.OwnedLANs != proto.AccountDeleteLANs {
		return errInvalid("ownedLANs 只能是 transfer 或 delete")
	}
	if c.s.seeded(p.Email) {
		return errForbidden("这个账号写在部署的种子文件里，服务器每次启动都会把它建回来：要注销，先请部署管理员把它从种子文件里拿掉")
	}
	pl := c.w.planAccountDeletion(p.ID, a.OwnedLANs)
	out.Account = pl.view(a.DryRun)
	if a.DryRun {
		return errNoChange
	}
	if len(pl.blocked) > 0 {
		names := make([]string, len(pl.blocked))
		for i, l := range pl.blocked {
			names[i] = "「" + l.Name + "」"
		}
		return errConflict("你是" + strings.Join(names, "、") + "的拥有者，里面还有别人：先选好转给别人还是整个删掉，再注销")
	}
	if normEmail(a.Confirm) != p.Email {
		return errInvalid("要注销，请再输一遍你的邮箱")
	}
	return c.deleteAccount(p, pl, out.Account)
}

// deleteAccount carries out pl, a plan for p's account with nothing
// blocked: account.delete's, or the operator's console's. v is the plan's
// view, for the log.
func (c *opCtx) deleteAccount(p *person, pl accountPlan, v *proto.AccountDeletion) error {
	// Their LANs: deleted, or handed over; other people's, left.
	for _, l := range pl.deleteLANs {
		if err := c.deleteLAN(l); err != nil {
			return err
		}
	}
	for _, t := range pl.transfer {
		if err := c.exec(`UPDATE lans SET owner = ? WHERE id = ?`, t.To, t.LAN); err != nil {
			return err
		}
		if err := c.exec(`UPDATE members SET role = ? WHERE lan = ? AND person = ?`, proto.RoleOwner, t.LAN, t.To); err != nil {
			return err
		}
		if err := c.dropMember(c.w.lans[t.LAN], p.ID); err != nil {
			return err
		}
	}
	for _, l := range pl.leave {
		if err := c.dropMember(l, p.ID); err != nil {
			return err
		}
	}
	// The shares they give and are given, with what those opened (opens
	// are keyed by scope).
	for _, id := range pl.shares {
		if err := c.exec(`DELETE FROM opens WHERE scope = ?`, string(shareScope(id))); err != nil {
			return err
		}
		if err := c.exec(`DELETE FROM shares WHERE id = ?`, id); err != nil {
			return err
		}
	}
	// Each ? is the person.
	for _, q := range []string{
		// Invites they made, those made out to them and those they used.
		`DELETE FROM invites WHERE inviter = ? OR person = ? OR used_by = ?`,
		// Other people's devices' refusals of them; their own devices' go
		// with the devices.
		`DELETE FROM refusals WHERE person = ?`,
		// Their codes for relays to come, and their relays, with the LANs'
		// lists, the usage counted, the codes and the kicks there (ON
		// DELETE CASCADE).
		`DELETE FROM relay_joins WHERE owner = ?`,
		`DELETE FROM relays WHERE owner = ? AND source <> '` + string(proto.RelayPublic) + `'`,
		// Kicks of them. The kicks they made or lifted on public relays, as
		// an operator, stay in force as the deployment's.
		`DELETE FROM relay_kicks WHERE person = ?`,
		`UPDATE relay_kicks SET by_person = '' WHERE by_person = ?`,
		`UPDATE relay_kicks SET lifted_by = '' WHERE lifted_by = ?`,
	} {
		args := make([]any, strings.Count(q, "?"))
		for i := range args {
			args[i] = p.ID
		}
		if err := c.exec(q, args...); err != nil {
			return err
		}
	}
	// Their devices: logged out at once, their addresses cooling down;
	// what they sent through other people's relays stays in those relays'
	// monthly totals, as no one's (as device.delete).
	for _, d := range pl.devices {
		if err := deleteDevice(c.tx, d, c.now); err != nil {
			return err
		}
	}
	// And the account, with its browsers' sessions (ON DELETE CASCADE):
	// their streams end with LoggedOut when the change is published.
	if err := c.exec(`DELETE FROM people WHERE id = ?`, p.ID); err != nil {
		return err
	}
	c.after = append(c.after, func() {
		// Once it is gone, its email is one with no account: logged masked.
		c.s.logf("account %s %s deleted: %d devices; LANs: %d deleted, %d handed over, %d left; %d relays (%d in use); %d shares; %d invites",
			p.ID, c.s.logEmail(p.Email), len(v.Devices), len(v.LANs), len(v.Transferred), len(v.Left), len(v.Relays), len(v.RelaysInUse), v.Shares, v.Invites)
	})
	return nil
}

// Through the admin socket (deneb-control account-delete): the operator
// deletes an account as its owner would, for someone who cannot reach the
// button.

// AdminAccountDeleteRequest deletes the account with Email; OwnedLANs and
// DryRun are account.delete's.
type AdminAccountDeleteRequest struct {
	Email     string `json:"email"`
	OwnedLANs string `json:"ownedLANs,omitempty"`
	DryRun    bool   `json:"dryRun,omitempty"`
}

func (s *Server) handleAdminAccountDelete(w http.ResponseWriter, r *http.Request) {
	var req AdminAccountDeleteRequest
	if err := readJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	email := normEmail(req.Email)
	p := s.state.Load().w.byEmail[email]
	if p == nil {
		s.writeErr(w, r, errNotFound("没有这个账号："+email))
		return
	}
	if !req.DryRun {
		// As the console's account.delete: a line of the record, and the
		// mail to them.
		switch req.OwnedLANs {
		case "", proto.AccountTransfer, proto.AccountDeleteLANs:
		default:
			s.writeErr(w, r, errInvalid("ownedLANs 只能是 transfer 或 delete"))
			return
		}
		v, err := s.opsDeleteAccount(nil, p.ID, email, func(*lan) string { return req.OwnedLANs })
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, proto.OpResult{Version: s.Version(), Result: &proto.OpOutput{Account: v}})
		return
	}
	op := proto.Op{ID: "admin", Op: proto.OpAccountDelete,
		Args: proto.OpArgs{Confirm: email, OwnedLANs: req.OwnedLANs, DryRun: true}}
	s.mu.Lock()
	e := s.applyOp("", p.ID, op)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	w.Write(e.body)
}

// AdminAccountDelete deletes an account, or with DryRun says what would
// go, through the admin socket at path.
func AdminAccountDelete(path string, req AdminAccountDeleteRequest) (*proto.AccountDeletion, error) {
	var out proto.OpResult
	if err := adminCall(path, "POST", "/account-delete", req, &out); err != nil {
		return nil, err
	}
	if out.Result == nil || out.Result.Account == nil {
		return nil, errors.New("服务器没说删了什么")
	}
	return out.Result.Account, nil
}
