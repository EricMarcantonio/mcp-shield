package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrNotFound is returned by lookups that require the row to exist
// (GetManifestByID) when it does not.
var ErrNotFound = errors.New("database: not found")

const schema = `
CREATE TABLE IF NOT EXISTS servers (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  endpoint TEXT,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS manifests (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL REFERENCES servers(id),
  hash TEXT NOT NULL,
  canonical_json TEXT NOT NULL,
  state TEXT NOT NULL,
  risk_level TEXT,
  diff_json TEXT,
  created_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_manifests_server_hash ON manifests(server_id, hash);
CREATE INDEX IF NOT EXISTS idx_manifests_server_state ON manifests(server_id, state);

CREATE TABLE IF NOT EXISTS approvals (
  id INTEGER PRIMARY KEY,
  manifest_id INTEGER NOT NULL REFERENCES manifests(id),
  decision TEXT NOT NULL,
  username TEXT NOT NULL,
  reason TEXT,
  created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_approvals_manifest ON approvals(manifest_id);
`

// Store is the persistence interface used by the approval workflow and API
// layers. Note what is deliberately absent: there is no method that updates
// a manifest's hash or canonical_json after insert. UpdateManifestState is
// the only mutation on an existing manifest row, so manifest content is
// physically immutable once written, not just immutable by convention.
type Store interface {
	CreateServer(ctx context.Context, name, endpoint string) (*Server, error)
	GetServerByName(ctx context.Context, name string) (*Server, error)
	GetServerByID(ctx context.Context, id int64) (*Server, error)
	ListServers(ctx context.Context) ([]Server, error)

	InsertManifest(ctx context.Context, m *ManifestRecord) (int64, error)
	GetManifestByHash(ctx context.Context, serverID int64, hash string) (*ManifestRecord, error)
	GetManifestByID(ctx context.Context, id int64) (*ManifestRecord, error)
	GetApprovedManifest(ctx context.Context, serverID int64) (*ManifestRecord, error)
	ListPendingManifests(ctx context.Context) ([]ManifestRecord, error)
	UpdateManifestState(ctx context.Context, id int64, newState string) error

	InsertApproval(ctx context.Context, a *Approval) (int64, error)
	ListApprovalsForManifest(ctx context.Context, manifestID int64) ([]Approval, error)

	WithTx(ctx context.Context, fn func(Store) error) error
}

// execer is satisfied by both *sql.DB and *sql.Tx, letting the query
// helpers below run against either a plain connection or an active
// transaction without duplicating SQL.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SQLiteStore is the top-level Store implementation backed by a *sql.DB.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (creating if necessary) a SQLite database at path, applies the
// schema, and configures WAL journaling and foreign keys.
func Open(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite + WAL: single writer keeps this simple for MVP scale

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA foreign_keys = ON;",
	}
	ctx := context.Background()
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("database: pragma %q: %w", p, err)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: apply schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) CreateServer(ctx context.Context, name, endpoint string) (*Server, error) {
	return createServer(ctx, s.db, name, endpoint)
}

func (s *SQLiteStore) GetServerByName(ctx context.Context, name string) (*Server, error) {
	return getServerByName(ctx, s.db, name)
}

func (s *SQLiteStore) GetServerByID(ctx context.Context, id int64) (*Server, error) {
	return getServerByID(ctx, s.db, id)
}

func (s *SQLiteStore) ListServers(ctx context.Context) ([]Server, error) {
	return listServers(ctx, s.db)
}

func (s *SQLiteStore) InsertManifest(ctx context.Context, m *ManifestRecord) (int64, error) {
	return insertManifest(ctx, s.db, m)
}

func (s *SQLiteStore) GetManifestByHash(ctx context.Context, serverID int64, hash string) (*ManifestRecord, error) {
	return getManifestByHash(ctx, s.db, serverID, hash)
}

func (s *SQLiteStore) GetManifestByID(ctx context.Context, id int64) (*ManifestRecord, error) {
	return getManifestByID(ctx, s.db, id)
}

func (s *SQLiteStore) GetApprovedManifest(ctx context.Context, serverID int64) (*ManifestRecord, error) {
	return getApprovedManifest(ctx, s.db, serverID)
}

func (s *SQLiteStore) ListPendingManifests(ctx context.Context) ([]ManifestRecord, error) {
	return listPendingManifests(ctx, s.db)
}

func (s *SQLiteStore) UpdateManifestState(ctx context.Context, id int64, newState string) error {
	return updateManifestState(ctx, s.db, id, newState)
}

func (s *SQLiteStore) InsertApproval(ctx context.Context, a *Approval) (int64, error) {
	return insertApproval(ctx, s.db, a)
}

func (s *SQLiteStore) ListApprovalsForManifest(ctx context.Context, manifestID int64) ([]Approval, error) {
	return listApprovalsForManifest(ctx, s.db, manifestID)
}

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: begin tx: %w", err)
	}
	if err := fn(&txStore{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: commit tx: %w", err)
	}
	return nil
}

// txStore is a Store bound to an in-flight transaction, handed to the
// callback passed to WithTx so multi-step writes (e.g. approve = supersede
// old row + flip new row's state + insert audit record) are atomic.
type txStore struct {
	tx *sql.Tx
}

func (s *txStore) CreateServer(ctx context.Context, name, endpoint string) (*Server, error) {
	return createServer(ctx, s.tx, name, endpoint)
}

func (s *txStore) GetServerByName(ctx context.Context, name string) (*Server, error) {
	return getServerByName(ctx, s.tx, name)
}

func (s *txStore) GetServerByID(ctx context.Context, id int64) (*Server, error) {
	return getServerByID(ctx, s.tx, id)
}

func (s *txStore) ListServers(ctx context.Context) ([]Server, error) {
	return listServers(ctx, s.tx)
}

func (s *txStore) InsertManifest(ctx context.Context, m *ManifestRecord) (int64, error) {
	return insertManifest(ctx, s.tx, m)
}

func (s *txStore) GetManifestByHash(ctx context.Context, serverID int64, hash string) (*ManifestRecord, error) {
	return getManifestByHash(ctx, s.tx, serverID, hash)
}

func (s *txStore) GetManifestByID(ctx context.Context, id int64) (*ManifestRecord, error) {
	return getManifestByID(ctx, s.tx, id)
}

func (s *txStore) GetApprovedManifest(ctx context.Context, serverID int64) (*ManifestRecord, error) {
	return getApprovedManifest(ctx, s.tx, serverID)
}

func (s *txStore) ListPendingManifests(ctx context.Context) ([]ManifestRecord, error) {
	return listPendingManifests(ctx, s.tx)
}

func (s *txStore) UpdateManifestState(ctx context.Context, id int64, newState string) error {
	return updateManifestState(ctx, s.tx, id, newState)
}

func (s *txStore) InsertApproval(ctx context.Context, a *Approval) (int64, error) {
	return insertApproval(ctx, s.tx, a)
}

func (s *txStore) ListApprovalsForManifest(ctx context.Context, manifestID int64) ([]Approval, error) {
	return listApprovalsForManifest(ctx, s.tx, manifestID)
}

func (s *txStore) WithTx(_ context.Context, fn func(Store) error) error {
	// Nested transactions aren't supported; a callback already running
	// inside WithTx just continues on the same transaction.
	return fn(s)
}

// --- shared query helpers, parameterized over execer -----------------------

func createServer(ctx context.Context, e execer, name, endpoint string) (*Server, error) {
	now := time.Now().UTC()
	res, err := e.ExecContext(ctx, `INSERT INTO servers (name, endpoint, created_at) VALUES (?, ?, ?)`, name, endpoint, now)
	if err != nil {
		return nil, fmt.Errorf("database: create server: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("database: create server: last insert id: %w", err)
	}
	return &Server{ID: id, Name: name, Endpoint: endpoint, CreatedAt: now}, nil
}

func getServerByName(ctx context.Context, e execer, name string) (*Server, error) {
	row := e.QueryRowContext(ctx, `SELECT id, name, endpoint, created_at FROM servers WHERE name = ?`, name)
	var s Server
	if err := row.Scan(&s.ID, &s.Name, &s.Endpoint, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("database: get server by name: %w", err)
	}
	return &s, nil
}

func getServerByID(ctx context.Context, e execer, id int64) (*Server, error) {
	row := e.QueryRowContext(ctx, `SELECT id, name, endpoint, created_at FROM servers WHERE id = ?`, id)
	var s Server
	if err := row.Scan(&s.ID, &s.Name, &s.Endpoint, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("database: get server by id: %w", err)
	}
	return &s, nil
}

func listServers(ctx context.Context, e execer) ([]Server, error) {
	rows, err := e.QueryContext(ctx, `SELECT id, name, endpoint, created_at FROM servers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("database: list servers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.Name, &s.Endpoint, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("database: list servers: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func insertManifest(ctx context.Context, e execer, m *ManifestRecord) (int64, error) {
	now := time.Now().UTC()
	res, err := e.ExecContext(ctx, `
		INSERT INTO manifests (server_id, hash, canonical_json, state, risk_level, diff_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ServerID, m.Hash, m.CanonicalJSON, m.State, nullIfEmpty(m.RiskLevel), nullIfEmpty(m.DiffJSON), now)
	if err != nil {
		return 0, fmt.Errorf("database: insert manifest: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: insert manifest: last insert id: %w", err)
	}
	m.CreatedAt = now
	return id, nil
}

func getManifestByHash(ctx context.Context, e execer, serverID int64, hash string) (*ManifestRecord, error) {
	row := e.QueryRowContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, risk_level, diff_json, created_at
		FROM manifests WHERE server_id = ? AND hash = ?`, serverID, hash)
	return scanManifest(row)
}

func getManifestByID(ctx context.Context, e execer, id int64) (*ManifestRecord, error) {
	row := e.QueryRowContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, risk_level, diff_json, created_at
		FROM manifests WHERE id = ?`, id)
	m, err := scanManifest(row)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNotFound
	}
	return m, nil
}

func getApprovedManifest(ctx context.Context, e execer, serverID int64) (*ManifestRecord, error) {
	row := e.QueryRowContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, risk_level, diff_json, created_at
		FROM manifests WHERE server_id = ? AND state = ?`, serverID, StateApproved)
	return scanManifest(row)
}

func listPendingManifests(ctx context.Context, e execer) ([]ManifestRecord, error) {
	rows, err := e.QueryContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, risk_level, diff_json, created_at
		FROM manifests WHERE state = ? ORDER BY created_at`, StatePending)
	if err != nil {
		return nil, fmt.Errorf("database: list pending manifests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ManifestRecord
	for rows.Next() {
		m, err := scanManifestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func updateManifestState(ctx context.Context, e execer, id int64, newState string) error {
	res, err := e.ExecContext(ctx, `UPDATE manifests SET state = ? WHERE id = ?`, newState, id)
	if err != nil {
		return fmt.Errorf("database: update manifest state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("database: update manifest state: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func insertApproval(ctx context.Context, e execer, a *Approval) (int64, error) {
	now := time.Now().UTC()
	res, err := e.ExecContext(ctx, `
		INSERT INTO approvals (manifest_id, decision, username, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`, a.ManifestID, a.Decision, a.Username, a.Reason, now)
	if err != nil {
		return 0, fmt.Errorf("database: insert approval: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: insert approval: last insert id: %w", err)
	}
	a.CreatedAt = now
	return id, nil
}

func listApprovalsForManifest(ctx context.Context, e execer, manifestID int64) ([]Approval, error) {
	rows, err := e.QueryContext(ctx, `
		SELECT id, manifest_id, decision, username, reason, created_at
		FROM approvals WHERE manifest_id = ? ORDER BY created_at`, manifestID)
	if err != nil {
		return nil, fmt.Errorf("database: list approvals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Approval
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.ID, &a.ManifestID, &a.Decision, &a.Username, &a.Reason, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("database: list approvals: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanner is satisfied by *sql.Row and *sql.Rows, letting scanManifest be
// reused for both single-row and multi-row queries.
type scanner interface {
	Scan(dest ...any) error
}

func scanManifest(row *sql.Row) (*ManifestRecord, error) {
	m, err := scanManifestRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func scanManifestRow(row scanner) (*ManifestRecord, error) {
	var m ManifestRecord
	var riskLevel, diffJSON sql.NullString
	if err := row.Scan(&m.ID, &m.ServerID, &m.Hash, &m.CanonicalJSON, &m.State, &riskLevel, &diffJSON, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("database: scan manifest: %w", err)
	}
	m.RiskLevel = riskLevel.String
	m.DiffJSON = diffJSON.String
	return &m, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
