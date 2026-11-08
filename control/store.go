package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite" // pure Go, so the server cross-compiles into a scratch image
)

// The database is one SQLite file. Every change goes through Server.change,
// which holds the one write lock, so SQLite only ever sees one writer.
//
// Times are Unix milliseconds. Lists that only the server reads (a
// device's endpoints, its paused scopes, a service's ports) are JSON text.
const schema = `
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS people (
	id           TEXT PRIMARY KEY,
	email        TEXT NOT NULL UNIQUE,
	name         TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL,
	initial      TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS lans (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	owner      TEXT NOT NULL REFERENCES people(id),
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS members (
	lan       TEXT NOT NULL REFERENCES lans(id) ON DELETE CASCADE,
	person    TEXT NOT NULL REFERENCES people(id),
	role      TEXT NOT NULL,
	state     TEXT NOT NULL,
	joined_at INTEGER NOT NULL,
	PRIMARY KEY (lan, person)
);

CREATE TABLE IF NOT EXISTS devices (
	id             TEXT PRIMARY KEY,
	owner          TEXT NOT NULL REFERENCES people(id),
	machine_key    TEXT NOT NULL UNIQUE,
	node_key       TEXT NOT NULL UNIQUE,
	disco_key      TEXT NOT NULL,
	name           TEXT NOT NULL,
	display_name   TEXT NOT NULL,
	kind           TEXT NOT NULL,
	os             TEXT NOT NULL,
	hostname       TEXT NOT NULL,
	ipv4           TEXT NOT NULL UNIQUE,
	ipv6           TEXT NOT NULL UNIQUE,
	endpoints      TEXT NOT NULL DEFAULT '[]',
	home_relay     TEXT NOT NULL DEFAULT '',
	paused         TEXT NOT NULL DEFAULT '[]',
	engine_version TEXT NOT NULL DEFAULT '',
	logged_out     INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	last_seen      INTEGER NOT NULL DEFAULT 0,
	UNIQUE (owner, name)
);

CREATE TABLE IF NOT EXISTS lan_devices (
	lan       TEXT NOT NULL REFERENCES lans(id) ON DELETE CASCADE,
	device    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	joined_at INTEGER NOT NULL,
	PRIMARY KEY (lan, device)
);

-- A device's catalog of services, and per scope which of them are open.
-- Opens outlive the device's stay in a LAN, so taking it out and putting
-- it back (undo) brings its switches back as they were.
CREATE TABLE IF NOT EXISTS services (
	device TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	key    TEXT NOT NULL,
	kind   TEXT NOT NULL,
	name   TEXT NOT NULL,
	ports  TEXT NOT NULL,
	PRIMARY KEY (device, key)
);

CREATE TABLE IF NOT EXISTS opens (
	device TEXT NOT NULL,
	scope  TEXT NOT NULL,
	key    TEXT NOT NULL,
	PRIMARY KEY (device, scope, key),
	FOREIGN KEY (device, key) REFERENCES services(device, key) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS refusals (
	device TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	person TEXT NOT NULL REFERENCES people(id),
	PRIMARY KEY (device, person)
);

CREATE TABLE IF NOT EXISTS invites (
	id         TEXT PRIMARY KEY,
	lan        TEXT NOT NULL REFERENCES lans(id) ON DELETE CASCADE,
	inviter    TEXT NOT NULL REFERENCES people(id),
	token      TEXT NOT NULL UNIQUE,
	state      TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_by    TEXT NOT NULL DEFAULT '',
	used_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS relays (
	id        TEXT PRIMARY KEY,
	name      TEXT NOT NULL,
	source    TEXT NOT NULL,
	owner     TEXT NOT NULL DEFAULT '',
	host_name TEXT NOT NULL,
	ipv4      TEXT NOT NULL DEFAULT '',
	ipv6      TEXT NOT NULL DEFAULT '',
	derp_port INTEGER NOT NULL,
	stun_port INTEGER NOT NULL DEFAULT 0,
	-- position among the relays every LAN uses unless it has its own list;
	-- -1 for a relay no LAN gets by default
	default_pos INTEGER NOT NULL DEFAULT -1
);

-- A LAN's own relay list (M4). A LAN with no rows uses the default relays.
CREATE TABLE IF NOT EXISTS lan_relays (
	lan      TEXT NOT NULL REFERENCES lans(id) ON DELETE CASCADE,
	relay    TEXT NOT NULL REFERENCES relays(id) ON DELETE CASCADE,
	position INTEGER NOT NULL,
	PRIMARY KEY (lan, relay)
);

-- Addresses of deleted devices, held back for 30 days so a peer that
-- still remembers one does not reach a stranger.
CREATE TABLE IF NOT EXISTS released_addrs (
	ipv4        TEXT PRIMARY KEY,
	released_at INTEGER NOT NULL
);
`

// openDB opens (and if need be creates) the database in dir.
func openDB(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "deneb.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// One connection: writes are serialized by Server.mu anyway, and a
	// single connection keeps the pragmas above in force for every query.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrations bring a database up to date, in order; PRAGMA user_version
// counts how many have run. The schema above is M1's and stays as it is:
// every change after it is a migration, so an old database upgrades the
// same way a new one is built.
var migrations = []string{
	// 1 (M2): approvals, invites to a person, login expiry.
	`ALTER TABLE lans ADD COLUMN approve_devices INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE lans ADD COLUMN approve_members INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE lan_devices ADD COLUMN state TEXT NOT NULL DEFAULT 'joined';
	ALTER TABLE invites ADD COLUMN person TEXT NOT NULL DEFAULT '';
	ALTER TABLE devices ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;`,
	// 2 (M2): a DNS label set by hand stays when the device is renamed.
	`ALTER TABLE devices ADD COLUMN name_pinned INTEGER NOT NULL DEFAULT 0;`,
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

// querier is what both *sql.DB and *sql.Tx offer.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getSetting(q querier, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func setSetting(q querier, key, value string) error {
	_, err := q.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func getVersion(q querier) (uint64, error) {
	v, ok, err := getSetting(q, "version")
	if err != nil || !ok {
		return 0, err
	}
	return strconv.ParseUint(v, 10, 64)
}
