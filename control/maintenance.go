package control

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// Some changes come from the clock, not from a request: a device's login
// runs out, an invite runs out. They change what devices and people see,
// so a sweep notices them and publishes a new version, the same way a
// request would.

// sweepEvery is how often the clock is checked.
const sweepEvery = 30 * time.Second

// RunMaintenance sweeps until ctx ends.
func (s *Server) RunMaintenance(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep()
		}
	}
}

// sweeper remembers when the last sweep ran, so an invite that ran out
// since is noticed once.
type sweeper struct {
	mu   sync.Mutex
	last time.Time
}

// sweep publishes a new version if a login or an invite ran out since the
// last sweep. It reports whether it did.
func (s *Server) sweep() bool {
	s.sweeps.mu.Lock()
	defer s.sweeps.mu.Unlock()
	now := s.now()
	last := s.sweeps.last
	s.sweeps.last = now

	st := s.state.Load()
	var ran []*device
	for _, d := range st.w.devices {
		if d.usable() && !d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt) {
			ran = append(ran, d)
		}
	}
	invites := 0
	for _, i := range st.w.invites {
		if i.State == proto.InvitePending && i.ExpiresAt.After(last) && !now.Before(i.ExpiresAt) {
			invites++
		}
	}
	if len(ran) == 0 && invites == 0 {
		return false
	}
	// Nothing to write: the reload at the new now marks the logins as run
	// out, and the views leave the invites out.
	v, err := s.change("", func(*sql.Tx, *World) error { return nil })
	if err != nil {
		s.logf("sweep: %v", err)
		return false
	}
	for _, d := range ran {
		owner := ""
		if p := st.w.people[d.Owner]; p != nil {
			owner = p.Email
		}
		s.logf("login: device %s (%s) of %s ran out at %s; version %d", d.ID, d.Name, owner, d.ExpiresAt.UTC().Format(time.RFC3339), v)
	}
	if invites > 0 {
		s.logf("sweep: %d invites ran out; version %d", invites, v)
	}
	return true
}
