package client

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

// M4's relays from the server (DESIGN §6.12): whose each is and where it
// is reached, from the user view; for my own, whether it is up, since when,
// and the install commands waiting to be run; from the relays' usage, how
// busy each of mine is, what it carried this month, who is on it now and
// whom I keep off it.

// usageEvery is how often the store asks for my relays' usage: their cards
// say who is on them now and what they carried this month.
const usageEvery = 30 * time.Second

// relayUsage asks the engine for my relays' usage now and every
// usageEvery, while the server has relays and someone is logged in; again
// at once after an op that changes it (kicks).
func (l *Live) relayUsage(ctx context.Context) {
	for {
		s := l.Snapshot()
		if s.LoggedIn && s.Can(model.FeatureRelays) {
			l.fetchUsage(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-l.usageNow:
		case <-time.After(usageEvery):
		}
	}
}

// fetchUsage takes in my relays' usage, as the server has it now.
func (l *Live) fetchUsage(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	raw, err := l.lc.RelayUsage(c)
	cancel()
	if err != nil {
		return // the engine is away, or an older one: the cards say less
	}
	var u proto.RelayUsage
	if json.Unmarshal(raw, &u) != nil {
		return
	}
	l.update(func(time.Time) { l.usage = &u })
}

// refreshUsage asks for the usage again soon, after an op that changes it.
func (l *Live) refreshUsage() {
	select {
	case l.usageNow <- struct{}{}:
	default:
	}
}

// takeRelays puts the view's relays on s: every one the person's LANs and
// shares use, and their own. A relay the deployment lists for every LAN
// (the seed file's) is no one's to drag or take away, as the public ones
// are not, and shows as one of them.
func takeRelays(s *model.Snapshot, v *proto.UserView, usage *proto.RelayUsage, joins map[string]string) {
	if !s.Can(model.FeatureRelays) {
		// A server before M4: the relays' names, nothing of whose they are
		// (a relay of M1–M3 shows as it did).
		for _, r := range v.Relays {
			s.Relays[model.RelayID(r.ID)] = &model.Relay{ID: model.RelayID(r.ID), Name: r.Name}
		}
		return
	}
	mine := map[proto.RelayID]proto.RelayView{}
	for _, rv := range v.MyRelays {
		mine[rv.ID] = rv
	}
	month := map[proto.RelayID]proto.RelayMonth{}
	if usage != nil {
		for _, m := range usage.Relays {
			month[m.Relay] = m
		}
	}
	for _, r := range v.Relays {
		mr := &model.Relay{ID: model.RelayID(r.ID), Name: r.Name, Owner: model.PersonID(r.Owner),
			Source: model.RelaySource(r.Source), Addr: relayAddr(r), RateLimit: r.RateLimit}
		rv, isMine := mine[r.ID]
		if isMine && rv.Default {
			mr.Source, mr.Owner = model.RelayPublic, ""
		}
		if isMine && !rv.Default {
			// A Deneb relay that stopped checking in is down: the devices
			// that went through it go through the public one meanwhile.
			mr.Down = rv.Registered && !rv.Online && !rv.Seeded
			mr.LastSeen, mr.Since = rv.LastSeen, rv.Since
			if rv.Full {
				mr.Load.Busy = model.BusyFull
			}
		}
		// A name that is only the address says nothing the address does
		// not: the ball and the card show the address, sentences whose it
		// is (DESIGN §6.12).
		if mr.Name == mr.Addr {
			mr.Name = ""
		}
		if m, ok := month[r.ID]; ok && isMine {
			takeMonth(mr, m, s.Me)
		}
		s.Relays[mr.ID] = mr
	}
	for _, j := range v.RelayJoins {
		s.RelayJoins = append(s.RelayJoins, model.RelayJoin{ID: j.ID, Command: joins[j.ID], Expires: j.ExpiresAt})
	}
	// The LANs' relays that someone dragged in: mine where my relay says
	// it is, the others' wherever the LAN lists them.
	for _, l := range s.LANs {
		l.AddedRelays = nil
		for _, id := range l.Relays {
			r := s.Relays[id]
			if r == nil || r.Public() {
				continue
			}
			if rv, ok := mine[proto.RelayID(id)]; ok && !slices.Contains(rv.LANs, proto.LANID(l.ID)) {
				continue
			}
			l.AddedRelays = append(l.AddedRelays, id)
		}
	}
}

// relayAddr is where a relay is reached, as its card shows it: its IPv4
// address, else its IPv6 one, else its name.
func relayAddr(r proto.Relay) string {
	switch {
	case r.IPv4.IsValid():
		return r.IPv4.String()
	case r.IPv6.IsValid():
		return r.IPv6.String()
	}
	return r.HostName
}

// takeMonth puts on my relay mr what its usage says: how busy it is, what it
// carried this month for each LAN and for the devices I gave away, who is
// on it now besides me, and whom I keep off it.
func takeMonth(mr *model.Relay, m proto.RelayMonth, me model.PersonID) {
	if ld := m.Load; ld != nil {
		mr.Load.People, mr.Load.Devices = ld.People, ld.Devices
		mr.Load.Uptime = time.Duration(ld.Uptime) * time.Second
		switch {
		case ld.Full || mr.Load.Busy == model.BusyFull:
			mr.Load.Busy = model.BusyFull
		case ld.CPU >= 0.7:
			mr.Load.Busy = model.BusySome
		}
	}
	for _, u := range m.LANs {
		if u.Bytes > 0 {
			mr.Month = append(mr.Month, model.RelayMonth{LAN: model.LANID(u.LAN), Bytes: u.Bytes})
		}
	}
	var shared int64
	for _, u := range m.Shares {
		shared += u.Bytes
	}
	if shared > 0 {
		mr.Month = append(mr.Month, model.RelayMonth{Shares: true, Bytes: shared})
	}
	for _, c := range m.Connected {
		if p := model.PersonID(c.Person); p != "" && p != me && !slices.Contains(mr.Using, p) {
			mr.Using = append(mr.Using, p)
		}
	}
	for _, k := range m.Kicks {
		if k.Person == "" {
			continue // a device kept off: the card lists people
		}
		mr.Kicks = append(mr.Kicks, model.RelayKick{Person: model.PersonID(k.Person), At: k.At, Lifted: k.LiftedAt})
	}
}

// kickID is the kick in force that keeps who off relay, as relay.unkick
// names it: "" when the usage lists none.
func (l *Live) kickID(relay model.RelayID, who model.PersonID) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.usage == nil {
		return ""
	}
	for _, m := range l.usage.Relays {
		if m.Relay != proto.RelayID(relay) {
			continue
		}
		for _, k := range m.Kicks {
			if k.Person == proto.PersonID(who) && k.LiftedAt.IsZero() {
				return k.ID
			}
		}
	}
	return ""
}

// relayArgs fills in what an M4 op names beyond the common args: the relay,
// the join code to take back, the kick to lift.
func (l *Live) relayArgs(op model.Op, args *proto.OpArgs) error {
	switch op.Kind {
	case model.OpRelayAdd, model.OpRelayRemove, model.OpRelayRename, model.OpRelayKick, model.OpRelayUnkick:
		args.Relay = proto.RelayID(op.Relay)
	case model.OpRelayRevokeJoin:
		args.Join = op.Join
	}
	if op.Kind == model.OpRelayUnkick {
		args.Person = ""
		if args.Kick = l.kickID(op.Relay, op.Person); args.Kick == "" {
			return &model.Error{Code: model.ErrInvalid, Message: "找不到这条不让用的记录"}
		}
	}
	return nil
}

// relaysDone keeps what an M4 op answered that the view does not carry: the
// install command, which the server shows only this once. Kicks change the
// usage, which is asked for again.
func (l *Live) relaysDone(op model.Op, out proto.OpOutput, r *model.Result) {
	switch op.Kind {
	case model.OpRelayJoin:
		if j := out.RelayJoin; j != nil {
			r.Command, r.Join = j.Command, j.ID
			l.mu.Lock()
			if l.joins == nil {
				l.joins = map[string]string{}
			}
			l.joins[j.ID] = j.Command
			l.mu.Unlock()
		}
	case model.OpRelayKick, model.OpRelayUnkick, model.OpRelayAdd, model.OpRelayRemove:
		l.refreshUsage()
	}
}
