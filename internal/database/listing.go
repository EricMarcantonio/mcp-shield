package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Page bounds one page of a list query.
//
// Every listing goes through this: an endpoint that returns "all the rows"
// is fine on a laptop and a denial-of-service on a gateway that has been
// fingerprinting a fleet for a year. Limit is clamped rather than rejected,
// so a caller asking for a million rows gets the maximum instead of an
// error it has no way to act on.
type Page struct {
	Limit  int // rows to return; zero or negative means DefaultPageLimit
	Offset int // rows to skip; negative is treated as zero
}

const (
	// DefaultPageLimit is what a caller that named no limit receives.
	DefaultPageLimit = 50
	// MaxPageLimit is the most any caller can receive in one response.
	MaxPageLimit = 200
)

func resolveLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPageLimit
	case limit > MaxPageLimit:
		return MaxPageLimit
	default:
		return limit
	}
}

// Effective reports the page that will actually be applied: Limit clamped
// into [1, MaxPageLimit] and Offset floored at zero. The API layer echoes
// this back to clients, so what a caller is told it got is computed from the
// same rule the query used rather than restated beside it.
func (p Page) Effective() Page {
	limit, offset := p.resolve()
	return Page{Limit: limit, Offset: offset}
}

func (p Page) resolve() (limit, offset int) {
	offset = p.Offset
	if offset < 0 {
		offset = 0
	}
	return resolveLimit(p.Limit), offset
}

// ManifestFilter narrows a manifest listing. A nil or empty field is not a
// filter, so the zero value lists every manifest.
type ManifestFilter struct {
	ServerID *int64
	Hash     string   // exact match; only unique within one server
	States   []string // any of these states; empty means every state
	Page     Page
}

// DecisionFilter narrows a decision listing. The zero value is the
// cross-server feed: every decision anyone has made, newest first.
type DecisionFilter struct {
	ManifestID *int64
	ServerID   *int64
	Page       Page
}

// Decision is one approvals row joined to the manifest and server it
// concerns.
//
// The join is done here rather than by the caller looking each id up in
// turn: a cross-server feed of the last fifty decisions would otherwise be
// a hundred extra round trips, and the answer to "who approved what" is not
// useful without the what.
type Decision struct {
	Approval
	ServerID     int64
	ServerName   string
	ManifestHash string
}

// listManifests returns manifests newest first.
//
// The order is (created_at DESC, id DESC), not created_at alone. Two rows
// written in the same instant would otherwise tie, and SQL is free to break
// that tie differently on each query — which shows up as a row appearing on
// two consecutive pages, or on neither.
func listManifests(ctx context.Context, e execer, f ManifestFilter) ([]ManifestRecord, error) {
	where, args := f.conditions()
	limit, offset := f.Page.resolve()
	args = append(args, limit, offset)

	rows, err := e.QueryContext(ctx, `
		SELECT id, server_id, hash, canonical_json, state, diff_json, created_at
		FROM manifests`+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("database: list manifests: %w", err)
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

func (f ManifestFilter) conditions() (where string, args []any) {
	var clauses []string
	if f.ServerID != nil {
		clauses = append(clauses, "server_id = ?")
		args = append(args, *f.ServerID)
	}
	if f.Hash != "" {
		clauses = append(clauses, "hash = ?")
		args = append(args, f.Hash)
	}
	if len(f.States) > 0 {
		clauses = append(clauses, "state IN ("+placeholders(len(f.States))+")")
		for _, state := range f.States {
			args = append(args, state)
		}
	}
	return joinClauses(clauses), args
}

// listDecisions returns the audit trail newest first, each row carrying the
// server and manifest hash it concerns.
func listDecisions(ctx context.Context, e execer, f DecisionFilter) ([]Decision, error) {
	where, args := f.conditions()
	limit, offset := f.Page.resolve()
	args = append(args, limit, offset)

	rows, err := e.QueryContext(ctx, `
		SELECT a.id, a.manifest_id, a.decision, a.username, a.reason, a.created_at,
		       m.hash, s.id, s.name
		FROM approvals a
		JOIN manifests m ON m.id = a.manifest_id
		JOIN servers s ON s.id = m.server_id`+where+`
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("database: list decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Decision
	for rows.Next() {
		var d Decision
		var reason sql.NullString
		if err := rows.Scan(&d.ID, &d.ManifestID, &d.Decision, &d.Username, &reason, &d.CreatedAt,
			&d.ManifestHash, &d.ServerID, &d.ServerName); err != nil {
			return nil, fmt.Errorf("database: list decisions: scan: %w", err)
		}
		d.Reason = reason.String
		out = append(out, d)
	}
	return out, rows.Err()
}

func (f DecisionFilter) conditions() (where string, args []any) {
	var clauses []string
	if f.ManifestID != nil {
		clauses = append(clauses, "a.manifest_id = ?")
		args = append(args, *f.ManifestID)
	}
	if f.ServerID != nil {
		clauses = append(clauses, "m.server_id = ?")
		args = append(args, *f.ServerID)
	}
	return joinClauses(clauses), args
}

func joinClauses(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(clauses, " AND ")
}

// placeholders builds "?, ?, ?" for an IN clause. n is a slice length the
// caller already holds, never caller-supplied text, so nothing here can
// carry an injection.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
