package model

import (
	"slices"
	"time"
)

// M4's relays (DESIGN §6.12): the public ones that are always there, my
// own servers, which I drag into the LANs I like, and my friends', which
// they drag into theirs.

// RelaySource is where a relay comes from (implementation.md §2.2).
type RelaySource string

const (
	RelayPublic RelaySource = "public" // 公共: ours, the fallback of every LAN
	RelayServer RelaySource = "server" // 我的服务器, 阿杰的服务器: someone's own
	RelayHome   RelaySource = "home"   // 家里的 NAS: a device at home, someone's own
)

// Busy is how busy a relay is: not, somewhat, or full, when new
// connections go to another.
type Busy string

const (
	BusyNo   Busy = ""
	BusySome Busy = "busy"
	BusyFull Busy = "full"
)

// RelayLoad is how a relay of mine is doing now.
type RelayLoad struct {
	Busy    Busy
	People  int
	Devices int
	// Uptime is how long it has run since it last started.
	Uptime time.Duration
}

// RelayMonth is what a relay of mine carried this month for one LAN, or
// for the devices I gave away (Shares).
type RelayMonth struct {
	LAN    LANID
	Shares bool
	Bytes  int64
}

// RelayKick is a person I would not let use a relay of mine: in force
// until Lifted.
type RelayKick struct {
	Person PersonID
	At     time.Time
	Lifted time.Time
}

// RelayJoin is an install command for a relay of mine, waiting to be run on
// a server: one hour, once. Command is empty when this client did not ask
// for it: the server shows it only once.
type RelayJoin struct {
	ID      string
	Command string
	Expires time.Time
}

// Label is what the relay is called: its name, or where it is reached
// while it has none.
func (r *Relay) Label() string {
	switch {
	case r.Name != "":
		return r.Name
	case r.Addr != "":
		return r.Addr
	}
	return "中转站"
}

// Public reports whether it is ours, the fallback: no one's to drag.
func (r *Relay) Public() bool { return r.Source == RelayPublic }

// Kick is the kick of who in force on the relay, if any.
func (r *Relay) Kick(who PersonID) *RelayKick {
	for i := range r.Kicks {
		if k := &r.Kicks[i]; k.Person == who && k.Lifted.IsZero() {
			return k
		}
	}
	return nil
}

// MineRelay reports whether the relay is one of my own.
func (s *Snapshot) MineRelay(r *Relay) bool {
	return r != nil && r.Source != RelayPublic && r.Source != "" && r.Owner == s.Me
}

// MyRelays are my own relays, by ID.
func (s *Snapshot) MyRelays() []*Relay {
	var out []*Relay
	for _, r := range s.Relays {
		if s.MineRelay(r) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b *Relay) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// Added reports whether someone dragged the relay into the LAN.
func (l *LAN) Added(id RelayID) bool { return slices.Contains(l.AddedRelays, id) }

// withM4 is With for M4's relay ops.
func (s *Snapshot) withM4(op Op) *Snapshot {
	switch op.Kind {
	case OpRelayAdd, OpRelayRemove:
		l := s.LAN(op.LAN)
		if l == nil || s.Relays[op.Relay] == nil || l.Added(op.Relay) == (op.Kind == OpRelayAdd) {
			return s
		}
		n, nl := s.withLAN(op.LAN)
		if op.Kind == OpRelayAdd {
			// After the others dragged in, before the fallback.
			nl.AddedRelays = append(slices.Clone(l.AddedRelays), op.Relay)
			rest := slices.DeleteFunc(slices.Clone(l.Relays), func(id RelayID) bool { return l.Added(id) || id == op.Relay })
			nl.Relays = append(slices.Clone(nl.AddedRelays), rest...)
			return n
		}
		gone := func(id RelayID) bool { return id == op.Relay }
		nl.AddedRelays = slices.DeleteFunc(slices.Clone(l.AddedRelays), gone)
		nl.Relays = slices.DeleteFunc(slices.Clone(l.Relays), gone)
		return n
	case OpRelayRename, OpRelayKick, OpRelayUnkick:
		r := s.Relays[op.Relay]
		if r == nil {
			return s
		}
		nr := *r
		switch op.Kind {
		case OpRelayRename:
			if op.Name == "" || op.Name == r.Name {
				return s
			}
			nr.Name = op.Name
		case OpRelayKick:
			if op.Person == "" || r.Kick(op.Person) != nil {
				return s
			}
			nr.Kicks = append([]RelayKick{{Person: op.Person, At: time.Now()}}, r.Kicks...)
			nr.Using = slices.DeleteFunc(slices.Clone(r.Using), func(p PersonID) bool { return p == op.Person })
		case OpRelayUnkick:
			k := r.Kick(op.Person)
			if k == nil {
				return s
			}
			nr.Kicks = slices.Clone(r.Kicks)
			for i := range nr.Kicks {
				if nr.Kicks[i].Person == op.Person && nr.Kicks[i].Lifted.IsZero() {
					nr.Kicks[i].Lifted = time.Now()
				}
			}
		}
		n := s.clone()
		n.Relays = cloneMap(s.Relays)
		n.Relays[op.Relay] = &nr
		return n
	case OpRelayRevokeJoin:
		i := slices.IndexFunc(s.RelayJoins, func(j RelayJoin) bool { return j.ID == op.Join })
		if i < 0 {
			return s
		}
		n := s.clone()
		n.RelayJoins = slices.Delete(slices.Clone(s.RelayJoins), i, i+1)
		return n
	}
	// relay.join: the command is the server's to make.
	return s
}
