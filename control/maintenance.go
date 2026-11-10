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

// RunMaintenance sweeps, and checks the server's health (health.go),
// until ctx ends.
func (s *Server) RunMaintenance(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	h := time.NewTicker(healthEvery)
	defer h.Stop()
	s.checkHealth()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep()
		case <-h.C:
			s.checkHealth()
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
	s.sweepSessions(now)
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
	// A revoked share's seat comes free once undo is past.
	seats := 0
	for _, sh := range st.w.shares {
		if sh.State == shareRevoked && sh.HoldUntil.After(last) && !now.Before(sh.HoldUntil) {
			seats++
		}
	}
	// Relays that stopped checking in go offline on their owners' cards;
	// join codes that ran out leave the list of those waiting.
	gone := s.relayLive.expire(now)
	joins := 0
	for _, j := range st.w.joins {
		if !j.live(now) {
			joins++
		}
	}
	if len(ran) == 0 && invites == 0 && seats == 0 && len(gone) == 0 && joins == 0 {
		return false
	}
	// Nothing to write: the reload at the new now marks the logins as run
	// out, and the views leave the invites out.
	v, err := s.change("", func(tx *sql.Tx, _ *World) error {
		if joins == 0 {
			return nil
		}
		_, err := tx.Exec(`DELETE FROM relay_joins WHERE expires_at <= ?`, ms(now))
		return err
	})
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
	for _, id := range gone {
		s.logf("relay %s offline: no sync for %s; version %d", id, relayOfflineAfter, v)
	}
	if joins > 0 {
		s.logf("sweep: %d relay join codes ran out; version %d", joins, v)
	}
	return true
}
