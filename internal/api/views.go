package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
)

// PendingManifestView is the shape returned by GET /api/manifests/pending.
type PendingManifestView struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	Changes   []string  `json:"changes"`
	CreatedAt time.Time `json:"created_at"`
}

// ManifestView is the shape returned by GET /api/manifests/{id}.
type ManifestView struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// FailedNotificationView is the shape returned by
// GET /api/notifications/failed: an event the dispatcher gave up on.
//
// LastError is the target's own error text, which names the target by its
// configured name and never by its URL — a webhook URL is a
// capability-bearing credential and this endpoint is a place operators copy
// output from.
type FailedNotificationView struct {
	EventID    int64     `json:"event_id"`
	Event      string    `json:"event"`
	Server     string    `json:"server"`
	ManifestID int64     `json:"manifest_id"`
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
}

func toFailedNotificationView(ctx context.Context, store database.Store, row database.OutboxRow) (FailedNotificationView, error) {
	rec, err := store.GetManifestByID(ctx, row.ManifestID)
	if err != nil {
		return FailedNotificationView{}, err
	}
	name, err := serverNameFor(ctx, store, rec.ServerID)
	if err != nil {
		return FailedNotificationView{}, err
	}
	return FailedNotificationView{
		EventID: row.ID, Event: row.EventType, Server: name,
		ManifestID: row.ManifestID, Attempts: row.Attempts,
		LastError: row.LastError, CreatedAt: row.CreatedAt,
	}, nil
}

// serverNameFor resolves the server a manifest belongs to. The error is
// propagated rather than papered over with a placeholder name: a manifest row
// pointing at a server that does not exist is referential corruption, and
// showing an approver "unknown" instead of failing hides it at exactly the
// moment they are deciding whether to trust that manifest.
func serverNameFor(ctx context.Context, store database.Store, serverID int64) (string, error) {
	srv, err := store.GetServerByID(ctx, serverID)
	if err != nil {
		return "", err
	}
	return srv.Name, nil
}

func toPendingView(ctx context.Context, store database.Store, m database.ManifestRecord) (PendingManifestView, error) {
	name, err := serverNameFor(ctx, store, m.ServerID)
	if err != nil {
		return PendingManifestView{}, err
	}

	var changes []string
	if m.DiffJSON != "" {
		var d diff.Diff
		if err := json.Unmarshal([]byte(m.DiffJSON), &d); err == nil {
			changes = diff.Summarize(&d)
		}
	}

	return PendingManifestView{
		ID: m.ID, Server: name, Hash: m.Hash,
		Changes: changes, CreatedAt: m.CreatedAt,
	}, nil
}

func toManifestView(ctx context.Context, store database.Store, m *database.ManifestRecord) (ManifestView, error) {
	name, err := serverNameFor(ctx, store, m.ServerID)
	if err != nil {
		return ManifestView{}, err
	}
	return ManifestView{
		ID: m.ID, Server: name, Hash: m.Hash, State: m.State, CreatedAt: m.CreatedAt,
	}, nil
}
