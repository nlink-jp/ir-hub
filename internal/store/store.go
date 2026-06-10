// Package store persists ir-hub runtime state in an embedded SQLite
// database (modernc.org/sqlite, pure Go — keeps CGO_ENABLED=0
// cross-compiles working).
//
// Tables: cases (lifecycle metadata; the AUTOINCREMENT id doubles as
// the case sequence number used in channel names), messages
// (ingested Slack messages, deduplicated by the (channel_id, ts)
// primary key), acl_denials (audit log), meta (schema version).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = "1"

// Case states.
const (
	StateCreating = "creating"
	StateOpen     = "open"
	StateClosed   = "closed"
	StateFailed   = "failed"
)

// Message sources.
const (
	SourceEvent    = "event"
	SourceBackfill = "backfill"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// ErrNotOpen is returned when a state transition requires an open case.
var ErrNotOpen = errors.New("case is not open")

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Option configures a Store.
type Option func(*Store)

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// Open opens (creating if necessary) the database at path and runs
// migrations. The parent directory is created if missing.
func Open(path string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	// modernc.org/sqlite handles concurrent writers poorly; serialize
	// through a single connection (WAL keeps readers cheap anyway).
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS cases (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			title        TEXT NOT NULL,
			severity     TEXT NOT NULL,
			visibility   TEXT NOT NULL CHECK (visibility IN ('public','private')),
			channel_id   TEXT UNIQUE,
			channel_name TEXT,
			state        TEXT NOT NULL DEFAULT 'creating'
			             CHECK (state IN ('creating','open','closed','failed')),
			opened_by    TEXT NOT NULL,
			opened_at    TEXT NOT NULL,
			closed_by    TEXT,
			closed_at    TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cases_state ON cases(state)`,
		`CREATE TABLE IF NOT EXISTS messages (
			channel_id  TEXT NOT NULL,
			ts          TEXT NOT NULL,
			case_id     INTEGER NOT NULL REFERENCES cases(id),
			thread_ts   TEXT,
			user_id     TEXT,
			bot_id      TEXT,
			subtype     TEXT,
			text        TEXT NOT NULL DEFAULT '',
			raw         TEXT NOT NULL,
			source      TEXT NOT NULL CHECK (source IN ('event','backfill')),
			ingested_at TEXT NOT NULL,
			PRIMARY KEY (channel_id, ts)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_case ON messages(case_id, ts)`,
		`CREATE TABLE IF NOT EXISTS acl_denials (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			denied_at  TEXT NOT NULL,
			user_id    TEXT NOT NULL,
			channel_id TEXT,
			entrypoint TEXT NOT NULL,
			action     TEXT,
			reason     TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO NOTHING`, schemaVersion)
	if err != nil {
		return fmt.Errorf("migrate: set schema_version: %w", err)
	}
	return nil
}

// SchemaVersion returns the stored schema version.
func (s *Store) SchemaVersion() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	return v, err
}

func (s *Store) nowRFC3339() string { return s.now().UTC().Format(time.RFC3339) }

// ---- cases ----

type Case struct {
	ID          int64
	Title       string
	Severity    string
	Visibility  string
	ChannelID   string
	ChannelName string
	State       string
	OpenedBy    string
	OpenedAt    time.Time
	ClosedBy    string
	ClosedAt    time.Time // zero when not closed
}

// CreateCase inserts a new case in the 'creating' state, reserving
// its sequence number, and returns it.
func (s *Store) CreateCase(title, severity, visibility, openedBy string) (*Case, error) {
	openedAt := s.nowRFC3339()
	res, err := s.db.Exec(
		`INSERT INTO cases (title, severity, visibility, opened_by, opened_at)
		 VALUES (?, ?, ?, ?, ?)`,
		title, severity, visibility, openedBy, openedAt)
	if err != nil {
		return nil, fmt.Errorf("create case: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create case: id: %w", err)
	}
	return s.CaseByID(id)
}

// ActivateCase records the created Slack channel and opens the case.
func (s *Store) ActivateCase(id int64, channelID, channelName string) error {
	res, err := s.db.Exec(
		`UPDATE cases SET channel_id=?, channel_name=?, state=?
		 WHERE id=? AND state=?`,
		channelID, channelName, StateOpen, id, StateCreating)
	if err != nil {
		return fmt.Errorf("activate case %d: %w", id, err)
	}
	return requireOneRow(res, fmt.Sprintf("activate case %d", id))
}

// FailCase marks a case whose channel creation failed.
func (s *Store) FailCase(id int64) error {
	res, err := s.db.Exec(
		`UPDATE cases SET state=? WHERE id=? AND state=?`,
		StateFailed, id, StateCreating)
	if err != nil {
		return fmt.Errorf("fail case %d: %w", id, err)
	}
	return requireOneRow(res, fmt.Sprintf("fail case %d", id))
}

// CloseCase transitions an open case to closed.
func (s *Store) CloseCase(id int64, closedBy string) error {
	res, err := s.db.Exec(
		`UPDATE cases SET state=?, closed_by=?, closed_at=? WHERE id=? AND state=?`,
		StateClosed, closedBy, s.nowRFC3339(), id, StateOpen)
	if err != nil {
		return fmt.Errorf("close case %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotOpen
	}
	return nil
}

const caseColumns = `id, title, severity, visibility,
	COALESCE(channel_id,''), COALESCE(channel_name,''), state,
	opened_by, opened_at, COALESCE(closed_by,''), COALESCE(closed_at,'')`

// CaseByID returns the case with the given id, or ErrNotFound.
func (s *Store) CaseByID(id int64) (*Case, error) {
	return scanCase(s.db.QueryRow(
		`SELECT `+caseColumns+` FROM cases WHERE id=?`, id))
}

// CaseByChannel returns the case bound to a channel, or ErrNotFound.
func (s *Store) CaseByChannel(channelID string) (*Case, error) {
	return scanCase(s.db.QueryRow(
		`SELECT `+caseColumns+` FROM cases WHERE channel_id=?`, channelID))
}

// ListOpenCases returns all open cases with a channel, ordered by id.
func (s *Store) ListOpenCases() ([]Case, error) {
	rows, err := s.db.Query(
		`SELECT `+caseColumns+` FROM cases
		 WHERE state=? AND channel_id IS NOT NULL ORDER BY id`, StateOpen)
	if err != nil {
		return nil, fmt.Errorf("list open cases: %w", err)
	}
	defer rows.Close()
	var out []Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanCase(row rowScanner) (*Case, error) {
	var c Case
	var openedAt, closedAt string
	err := row.Scan(&c.ID, &c.Title, &c.Severity, &c.Visibility,
		&c.ChannelID, &c.ChannelName, &c.State,
		&c.OpenedBy, &openedAt, &c.ClosedBy, &closedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan case: %w", err)
	}
	if c.OpenedAt, err = time.Parse(time.RFC3339, openedAt); err != nil {
		return nil, fmt.Errorf("parse opened_at %q: %w", openedAt, err)
	}
	if closedAt != "" {
		if c.ClosedAt, err = time.Parse(time.RFC3339, closedAt); err != nil {
			return nil, fmt.Errorf("parse closed_at %q: %w", closedAt, err)
		}
	}
	return &c, nil
}

func requireOneRow(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%s: no matching row in expected state", op)
	}
	return nil
}

// ---- messages ----

type Message struct {
	ChannelID string
	TS        string
	CaseID    int64
	ThreadTS  string
	UserID    string
	BotID     string
	Subtype   string
	Text      string
	Raw       string
	Source    string
}

// InsertMessage stores a message, ignoring duplicates on
// (channel_id, ts). Returns true when a row was actually inserted.
func (s *Store) InsertMessage(m Message) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO messages
		 (channel_id, ts, case_id, thread_ts, user_id, bot_id, subtype, text, raw, source, ingested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ChannelID, m.TS, m.CaseID, m.ThreadTS, m.UserID, m.BotID,
		m.Subtype, m.Text, m.Raw, m.Source, s.nowRFC3339())
	if err != nil {
		return false, fmt.Errorf("insert message %s/%s: %w", m.ChannelID, m.TS, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MaxMessageTS returns the newest stored ts for a channel ("" when
// the channel has no stored messages). Slack ts strings of equal
// integer-part length sort lexicographically in chronological order;
// the epoch digits won't change length until year 2286.
func (s *Store) MaxMessageTS(channelID string) (string, error) {
	var ts sql.NullString
	err := s.db.QueryRow(
		`SELECT MAX(ts) FROM messages WHERE channel_id=?`, channelID).Scan(&ts)
	if err != nil {
		return "", fmt.Errorf("max message ts %s: %w", channelID, err)
	}
	return ts.String, nil
}

// CountMessages returns the number of stored messages for a case.
func (s *Store) CountMessages(caseID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE case_id=?`, caseID).Scan(&n)
	return n, err
}

// ---- ACL audit ----

type Denial struct {
	UserID     string
	ChannelID  string
	Entrypoint string // 'slash' | 'mention' | 'view_submission'
	Action     string // subcommand only; never message bodies
	Reason     string
}

// InsertDenial appends an entry to the ACL audit log.
func (s *Store) InsertDenial(d Denial) error {
	_, err := s.db.Exec(
		`INSERT INTO acl_denials (denied_at, user_id, channel_id, entrypoint, action, reason)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.nowRFC3339(), d.UserID, d.ChannelID, d.Entrypoint, d.Action, d.Reason)
	if err != nil {
		return fmt.Errorf("insert denial: %w", err)
	}
	return nil
}
