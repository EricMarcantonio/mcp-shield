package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrNotFound is returned by every Store lookup when the row does not
// exist. There is exactly one absence convention: no lookup returns a nil
// object with a nil error, so callers use errors.Is rather than a nil check
// that is easy to forget and fails as a nil dereference when they do.
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
// layers. Lookups return ErrNotFound when the row does not exist; no method
// returns a nil object with a nil error. Note what is deliberately absent: there is no method that updates
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
	queries
	db *sql.DB
}

// dbDirPerm is the mode of the directory holding the database. 0700, not
// 0750: this database is the approvals audit trail — the record of who
// authorized which capability change — so no group or other user has any
// business reading it or the WAL and shared-memory files SQLite writes
// beside it.
const dbDirPerm fs.FileMode = 0o700

// Open opens (creating if necessary) a SQLite database at path, applies the
// schema, and configures WAL journaling and foreign keys. The parent
// directory is created if it does not exist.
func Open(path string) (*SQLiteStore, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

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
	return &SQLiteStore{queries: queries{e: db}, db: db}, nil
}

// ensureParentDir creates the directory holding the database file. It lives
// here rather than in main so every entry point benefits: a release binary or
// container starting in a fresh directory used to die on the first PRAGMA
// with "unable to open database file (14)", because the default
// DATABASE_PATH is "data/mcp.db" and nothing created "data/".
//
// Paths that name no directory are left alone: a bare filename, and SQLite's
// in-memory DSNs (":memory:", "file::memory:...") which are not filesystem
// paths at all.
func ensureParentDir(path string) error {
	if isInMemoryDSN(path) {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == string(filepath.Separator) {
		return nil
	}
	if err := os.MkdirAll(dir, dbDirPerm); err != nil {
		return fmt.Errorf("database: create directory %q: %w", dir, err)
	}
	warnIfDirectoryIsWorldReadable(dir)
	return nil
}

func isInMemoryDSN(path string) bool {
	return path == ":memory:" || strings.HasPrefix(path, "file::memory:")
}

// warnIfDirectoryIsWorldReadable reports a pre-existing directory that other
// local users can read. MkdirAll leaves an existing directory's mode alone,
// and silently tightening a directory an operator deliberately created would
// be a surprise — so this warns instead of changing it or refusing to start.
func warnIfDirectoryIsWorldReadable(dir string) {
	info, err := os.Stat(dir)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn("database directory is readable by other users; it holds the approvals audit trail",
			"dir", dir, "mode", fmt.Sprintf("%#o", perm), "recommended", fmt.Sprintf("%#o", dbDirPerm))
	}
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// queries implements every data method of Store once, against whichever
// execer it is bound to. Both Store implementations embed it, so a new
// method is written here alone instead of three times (helper, SQLiteStore
// forwarder, txStore forwarder).
type queries struct{ e execer }

func (q queries) CreateServer(ctx context.Context, name, endpoint string) (*Server, error) {
	return createServer(ctx, q.e, name, endpoint)
}

func (q queries) GetServerByName(ctx context.Context, name string) (*Server, error) {
	return getServerByName(ctx, q.e, name)
}

func (q queries) GetServerByID(ctx context.Context, id int64) (*Server, error) {
	return getServerByID(ctx, q.e, id)
}

func (q queries) ListServers(ctx context.Context) ([]Server, error) {
	return listServers(ctx, q.e)
}

func (q queries) InsertManifest(ctx context.Context, m *ManifestRecord) (int64, error) {
	return insertManifest(ctx, q.e, m)
}

func (q queries) GetManifestByHash(ctx context.Context, serverID int64, hash string) (*ManifestRecord, error) {
	return getManifestByHash(ctx, q.e, serverID, hash)
}

func (q queries) GetManifestByID(ctx context.Context, id int64) (*ManifestRecord, error) {
	return getManifestByID(ctx, q.e, id)
}

func (q queries) GetApprovedManifest(ctx context.Context, serverID int64) (*ManifestRecord, error) {
	return getApprovedManifest(ctx, q.e, serverID)
}

func (q queries) ListPendingManifests(ctx context.Context) ([]ManifestRecord, error) {
	return listPendingManifests(ctx, q.e)
}

func (q queries) UpdateManifestState(ctx context.Context, id int64, newState string) error {
	return updateManifestState(ctx, q.e, id, newState)
}

func (q queries) InsertApproval(ctx context.Context, a *Approval) (int64, error) {
	return insertApproval(ctx, q.e, a)
}

func (q queries) ListApprovalsForManifest(ctx context.Context, manifestID int64) ([]Approval, error) {
	return listApprovalsForManifest(ctx, q.e, manifestID)
}

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: begin tx: %w", err)
	}
	if err := fn(&txStore{queries{e: tx}}); err != nil {
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
type txStore struct{ queries }

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
			return nil, ErrNotFound
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
			return nil, ErrNotFound
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
		INSERT INTO manifests (server_id, hash, canonical_json, state, diff_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		m.ServerID, m.Hash, m.CanonicalJSON, m.State, nullIfEmpty(m.DiffJSON), now)
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
		SELECT id, server_id, hash, canonical_json, state, diff_json, created_at
		FROM manifests WHERE server_id = ? AND hash = ?`, serverID, hash)
	return scanManifest(row)
}

func getManifestByID(ctx context.Context, e execer, id int64) (*ManifestRecord, error) {
	row := e.QueryRowContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, diff_json, created_at
		FROM manifests WHERE id = ?`, id)
	// scanManifest already maps a missing row to ErrNotFound.
	return scanManifest(row)
}

func getApprovedManifest(ctx context.Context, e execer, serverID int64) (*ManifestRecord, error) {
	row := e.QueryRowContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, diff_json, created_at
		FROM manifests WHERE server_id = ? AND state = ?`, serverID, StateApproved)
	return scanManifest(row)
}

func listPendingManifests(ctx context.Context, e execer) ([]ManifestRecord, error) {
	rows, err := e.QueryContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, diff_json, created_at
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
		return nil, ErrNotFound
	}
	return m, err
}

func scanManifestRow(row scanner) (*ManifestRecord, error) {
	var m ManifestRecord
	var diffJSON sql.NullString
	if err := row.Scan(&m.ID, &m.ServerID, &m.Hash, &m.CanonicalJSON, &m.State, &diffJSON, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("database: scan manifest: %w", err)
	}
	m.DiffJSON = diffJSON.String
	return &m, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
