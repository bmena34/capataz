// Package store persists a timeline of session events to SQLite so the TUI
// can show per-session history.
package store

import (
	"database/sql"

	_ "modernc.org/sqlite"

	"github.com/bmena34/capataz/internal/proto"
)

// Store wraps the events database.
type Store struct{ db *sql.DB }

// Record is one persisted event.
type Record struct {
	TS        int64
	SessionID string
	Kind      string
	State     string
	Cwd       string
	Summary   string
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	ts         INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	kind       TEXT NOT NULL,
	state      TEXT NOT NULL,
	cwd        TEXT,
	summary    TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, ts);

CREATE TABLE IF NOT EXISTS sessions (
	session_id      TEXT PRIMARY KEY,
	cwd             TEXT,
	tmux_pane       TEXT,
	transcript_path TEXT,
	state           TEXT,
	last_event      TEXT,
	last_change     INTEGER,
	started         INTEGER,
	summary         TEXT,
	context_tokens  INTEGER,
	out_tokens      INTEGER,
	model           TEXT,
	state_since     INTEGER,
	cost_usd        REAL,
	git_branch      TEXT,
	git_dirty       INTEGER
);
`

// migrations adds columns to a pre-existing sessions table. Each is a no-op
// (duplicate-column error, ignored) on databases already at the latest schema.
var migrations = []string{
	"ALTER TABLE sessions ADD COLUMN state_since INTEGER",
	"ALTER TABLE sessions ADD COLUMN cost_usd REAL",
	"ALTER TABLE sessions ADD COLUMN git_branch TEXT",
	"ALTER TABLE sessions ADD COLUMN git_dirty INTEGER",
}

// Open opens (and migrates) the database for read/write. Used by the daemon.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	for _, m := range migrations {
		_, _ = db.Exec(m) // ignore "duplicate column" on already-migrated DBs
	}
	return &Store{db: db}, nil
}

// OpenReadOnly opens the database read-only. Used by the TUI. Returns an error
// if the database does not exist yet.
func OpenReadOnly(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Insert appends an event.
func (s *Store) Insert(r Record) error {
	_, err := s.db.Exec(
		`INSERT INTO events (ts, session_id, kind, state, cwd, summary) VALUES (?, ?, ?, ?, ?, ?)`,
		r.TS, r.SessionID, r.Kind, r.State, r.Cwd, r.Summary,
	)
	return err
}

// History returns the most recent events for a session, newest first.
func (s *Store) History(sessionID string, limit int) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT ts, session_id, kind, state, cwd, summary FROM events
		 WHERE session_id = ? ORDER BY ts DESC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.TS, &r.SessionID, &r.Kind, &r.State, &r.Cwd, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveSession upserts the current state of a session so it survives a daemon
// restart.
func (s *Store) SaveSession(x proto.Session) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions
		   (session_id, cwd, tmux_pane, transcript_path, state, last_event, last_change, started, summary, context_tokens, out_tokens, model, state_since, cost_usd, git_branch, git_dirty)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   cwd=excluded.cwd, tmux_pane=excluded.tmux_pane, transcript_path=excluded.transcript_path,
		   state=excluded.state, last_event=excluded.last_event, last_change=excluded.last_change,
		   started=excluded.started, summary=excluded.summary, context_tokens=excluded.context_tokens,
		   out_tokens=excluded.out_tokens, model=excluded.model, state_since=excluded.state_since,
		   cost_usd=excluded.cost_usd, git_branch=excluded.git_branch, git_dirty=excluded.git_dirty`,
		x.ID, x.Cwd, x.TmuxPane, x.TranscriptPath, string(x.State), x.LastEvent, x.LastChange,
		x.Started, x.Summary, x.ContextTokens, x.OutTokens, x.Model, x.StateSince, x.CostUSD, x.GitBranch, x.GitDirty,
	)
	return err
}

// DeleteSession removes a session (on SessionEnd).
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, id)
	return err
}

// LoadSessions returns all persisted sessions (for repopulating on startup).
func (s *Store) LoadSessions() ([]proto.Session, error) {
	rows, err := s.db.Query(
		`SELECT session_id, cwd, tmux_pane, transcript_path, state, last_event, last_change, started, summary, context_tokens, out_tokens, model, state_since, cost_usd, git_branch, git_dirty
		 FROM sessions`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proto.Session
	for rows.Next() {
		var x proto.Session
		var st string
		if err := rows.Scan(&x.ID, &x.Cwd, &x.TmuxPane, &x.TranscriptPath, &st, &x.LastEvent,
			&x.LastChange, &x.Started, &x.Summary, &x.ContextTokens, &x.OutTokens, &x.Model,
			&x.StateSince, &x.CostUSD, &x.GitBranch, &x.GitDirty); err != nil {
			return nil, err
		}
		x.State = proto.State(st)
		out = append(out, x)
	}
	return out, rows.Err()
}
