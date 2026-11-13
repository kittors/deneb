package control

import (
	"database/sql"
	"time"

	"github.com/kittors/deneb/proto"
)

// The operators' record (M7, DESIGN §14.15; design/plan/m7/ops-console-api.md):
// each change an operator makes, a line: when, who, to whom, what, how it
// went. The Web console, the public relays' page and the admin socket all
// write it; the log gets a line too ("operator <email>: <the sentence>").
// What an operator only looked at is not written. Kept a year (cleanup).

// opsRecordKeep is how long the record keeps a line.
const opsRecordKeep = 365 * 24 * time.Hour

// opsActor is who made a change: an operator, or the admin socket (no
// person: whoever is on the server's own machine).
type opsActor struct {
	ID    proto.PersonID
	Name  string
	Email string
}

// adminSocket is the actor of a change made with deneb-control's commands.
var adminSocket = opsActor{Name: "服务器上的命令"}

func actorOf(p *person) opsActor {
	if p == nil {
		return adminSocket
	}
	return opsActor{ID: p.ID, Name: p.DisplayName, Email: p.Email}
}

// recordOps writes a line of the record in tx, with the change it is
// about: one goes in only with the other. text is the sentence without
// who did it ("把 Leo 的位置上限调成了 8"), as the console shows it after
// their name.
func recordOps(tx *sql.Tx, now time.Time, by opsActor, act, target, text, result string) error {
	if result == "" {
		result = "ok"
	}
	_, err := tx.Exec(`INSERT INTO ops_records (at, by_person, by_name, by_email, act, target, text, result) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ms(now), by.ID, by.Name, by.Email, act, target, text, result)
	return err
}

// logOps is the record's line in the log, once the change is in.
func (s *Server) logOps(by opsActor, act, text string) {
	who := by.Email
	if who == "" {
		who = "(the admin socket)"
	}
	s.logf("operator %s: %s [%s]", who, text, act)
}

// recordOps, inside an op, writes the line for an operator's change to a
// public relay (the public relays' page): with the op, and its log line
// once the change is in. Someone's change to their own relay is not the
// record's.
func (c *opCtx) recordOps(rl *proto.Relay, act, text string) error {
	if rl == nil || rl.Source != proto.RelayPublic {
		return nil
	}
	by := actorOf(c.w.people[c.me])
	if err := recordOps(c.tx, c.now, by, act, string(rl.ID), pangu(text), "ok"); err != nil {
		return err
	}
	c.after = append(c.after, func() { c.s.logOps(by, act, pangu(text)) })
	return nil
}

// OpsRecord is a line of the record, as the console shows it.
type OpsRecord struct {
	At   time.Time `json:"at"`
	By   OpsWho    `json:"by"`
	Text string    `json:"text"`
}

// opsRecords are the lines before before (zero: any), newest first, at
// most limit.
func (s *Server) opsRecords(before time.Time, limit int) ([]OpsRecord, error) {
	q := `SELECT at, by_person, by_name, text FROM ops_records`
	args := []any{}
	if !before.IsZero() {
		q += ` WHERE at < ?`
		args = append(args, ms(before))
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := s.state.Load().w.people
	out := []OpsRecord{}
	for rows.Next() {
		var at int64
		var r OpsRecord
		if err := rows.Scan(&at, &r.By.ID, &r.By.Name, &r.Text); err != nil {
			return nil, err
		}
		r.At = fromMS(at).UTC()
		if p := people[r.By.ID]; p != nil {
			r.By.Initial = p.Initial
		} else {
			r.By.Initial = initialOf(r.By.Name)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dropOldRecords drops the lines older than a year; the daily cleanup
// runs it.
func (s *Server) dropOldRecords(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM ops_records WHERE at < ?`, ms(now.Add(-opsRecordKeep)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
