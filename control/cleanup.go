package control

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// The daily cleanup (PRIVACY.md §7): what is over stays a while, then
// goes. Invites used or run out, shares taken back or given up and kicks
// lifted keep 30 days (the cards show the last 30 days of them); relay
// usage keeps this month and the two before (the cards show this month);
// a device logged out or run out keeps its row, so its owner gets the same
// address back, but not the addresses and ports it reported; the
// operators' record keeps a year. It runs once a day, with the backup, and
// the log says only how many of each went.

// cleanupAfter is how long what is over stays.
const cleanupAfter = 30 * 24 * time.Hour

// usageMonths is how many months of relay usage stay, this one included.
const usageMonths = 3

// cleaned is what one cleanup removed.
type cleaned struct {
	invites, shares, kicks, usage, addresses, records int64
}

func (c cleaned) any() bool {
	return c.invites+c.shares+c.kicks+c.usage+c.addresses+c.records > 0
}

// cleanup removes what is over, as above, and logs how much.
func (s *Server) cleanup() (cleaned, error) {
	now := s.now()
	cutoff := ms(now.Add(-cleanupAfter))
	var c cleaned
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		count := func(res sql.Result, err error) (int64, error) {
			if err != nil {
				return 0, err
			}
			return res.RowsAffected()
		}
		var err error
		// Invites: run out, accepted or declined, or revoked (which has
		// no time of its own: 30 days past when it would have run out).
		if c.invites, err = count(tx.Exec(`DELETE FROM invites WHERE
				(state IN (?, ?) AND expires_at <= ?) OR
				(state IN (?, ?) AND used_at > 0 AND used_at <= ?)`,
			proto.InvitePending, proto.InviteRevoked, cutoff,
			proto.InviteAccepted, proto.InviteDeclined, cutoff)); err != nil {
			return err
		}
		// Shares taken back or given up, and no longer holding a seat; what
		// they opened goes with them (opens are keyed by scope).
		rows, err := tx.Query(`SELECT id FROM shares WHERE state IN (?, ?) AND revoked_at > 0 AND revoked_at <= ? AND hold_until <= ?`,
			shareRevoked, shareLeft, cutoff, ms(now))
		if err != nil {
			return err
		}
		var ids []any
		var scopes []any
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids, scopes = append(ids, id), append(scopes, string(shareScope(id)))
		}
		rows.Close()
		if len(ids) > 0 {
			marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
			if _, err := tx.Exec(`DELETE FROM opens WHERE scope IN (`+marks+`)`, scopes...); err != nil {
				return err
			}
			if c.shares, err = count(tx.Exec(`DELETE FROM shares WHERE id IN (`+marks+`)`, ids...)); err != nil {
				return err
			}
		}
		// Kicks lifted long ago; the ones in force stay.
		if c.kicks, err = count(tx.Exec(`DELETE FROM relay_kicks WHERE lifted_at > 0 AND lifted_at <= ?`, cutoff)); err != nil {
			return err
		}
		// The addresses a device reported, once it is logged out or its
		// login ran out: no one needs them until it comes back, and then
		// it reports them anew.
		if c.addresses, err = count(tx.Exec(`UPDATE devices SET endpoints = '[]' WHERE endpoints <> '[]' AND
				(logged_out = 1 OR (expires_at > 0 AND expires_at <= ?))`, ms(now))); err != nil {
			return err
		}
		if c.invites+c.shares+c.kicks+c.addresses == 0 {
			return errNoChange
		}
		return nil
	})
	if err != nil {
		return c, err
	}
	// Relay usage is not in the world: it goes on its own.
	t := now.UTC()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(usageMonths - 1), 0)
	res, err := s.db.Exec(`DELETE FROM relay_usage WHERE day < ?`, first.Unix()/86400)
	if err != nil {
		return c, err
	}
	c.usage, _ = res.RowsAffected()
	// The operators' record keeps a year (opsrecord.go).
	if c.records, err = s.dropOldRecords(now); err != nil {
		return c, err
	}
	if c.any() {
		s.logf("cleanup: %d invites, %d shares, %d kicks, %d rows of relay usage, %d devices' addresses, %d lines of the operators' record",
			c.invites, c.shares, c.kicks, c.usage, c.addresses, c.records)
	}
	return c, nil
}
