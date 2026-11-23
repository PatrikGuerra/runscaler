package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS admin (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  password_hash BLOB    NOT NULL,
  salt          BLOB    NOT NULL,
  iterations    INTEGER NOT NULL,
  created_at    TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash BLOB PRIMARY KEY,
  csrf_token TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  id     INTEGER PRIMARY KEY,
  at     TEXT NOT NULL,
  kind   TEXT NOT NULL,
  detail TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_at ON events (at);
CREATE INDEX IF NOT EXISTS events_kind_at ON events (kind, at);
`

// Store is the panel's SQLite database: the admin password, sessions and the
// event history (runscaler's own counters reset on every restart).
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Event is one row of the history.
type Event struct {
	ID     int64           `json:"id"`
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"`
	Detail json.RawMessage `json:"detail"`
}

// AdminRecord is the stored password hash.
type AdminRecord struct {
	Hash       []byte
	Salt       []byte
	Iterations int
}

var errAdminExists = errors.New("admin already configured")

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", schema} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database: %w", err)
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, err
	}
	switch version {
	case 0:
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
			_ = db.Close()
			return nil, err
		}
	case schemaVersion:
	default:
		_ = db.Close()
		return nil, fmt.Errorf("unsupported panel database version %d", version)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (s *Store) AddEvent(ctx context.Context, kind string, detail any) error {
	if detail == nil {
		detail = struct{}{}
	}
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO events (at, kind, detail) VALUES (?, ?, ?)`, ts(s.now()), kind, string(body))
	return err
}

// ListEvents returns events newest first. before is an event id (0 = from the
// newest); kind filters when not empty.
func (s *Store) ListEvents(ctx context.Context, kind string, before int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, kind, detail FROM events WHERE id < ? AND (? = '' OR kind = ?) ORDER BY id DESC LIMIT ?`,
		before, kind, kind, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []Event{}
	for rows.Next() {
		var e Event
		var at, detail string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &detail); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		e.Detail = json.RawMessage(detail)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) PruneEvents(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, ts(olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) Admin(ctx context.Context) (AdminRecord, bool, error) {
	var a AdminRecord
	err := s.db.QueryRowContext(ctx, `SELECT password_hash, salt, iterations FROM admin WHERE id = 1`).Scan(&a.Hash, &a.Salt, &a.Iterations)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminRecord{}, false, nil
	}
	return a, err == nil, err
}

// CreateAdmin stores the password once; a second call fails.
func (s *Store) CreateAdmin(ctx context.Context, a AdminRecord) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO admin (id, password_hash, salt, iterations, created_at) VALUES (1, ?, ?, ?, ?)`,
		a.Hash, a.Salt, a.Iterations, ts(s.now()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errAdminExists
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, csrf string, ttl time.Duration) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, csrf_token, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, csrf, ts(now), ts(now.Add(ttl)))
	return err
}

// Session returns the CSRF token of a live session.
func (s *Store) Session(ctx context.Context, tokenHash []byte) (string, bool, error) {
	var csrf, expires string
	err := s.db.QueryRowContext(ctx, `SELECT csrf_token, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&csrf, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	exp, _ := time.Parse(time.RFC3339Nano, expires)
	if !s.now().Before(exp) {
		_ = s.DeleteSession(ctx, tokenHash)
		return "", false, nil
	}
	return csrf, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, ts(s.now()))
	return err
}
