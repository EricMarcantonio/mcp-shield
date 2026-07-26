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
	Risk      string    `json:"risk"`
	Changes   []string  `json:"changes"`
	CreatedAt time.Time `json:"created_at"`
}

// ManifestView is the shape returned by GET /api/manifests/{id}.
type ManifestView struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	State     string    `json:"state"`
	Risk      string    `json:"risk,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
		ID: m.ID, Server: name, Hash: m.Hash, Risk: m.RiskLevel,
		Changes: changes, CreatedAt: m.CreatedAt,
	}, nil
}

func toManifestView(ctx context.Context, store database.Store, m *database.ManifestRecord) (ManifestView, error) {
	name, err := serverNameFor(ctx, store, m.ServerID)
	if err != nil {
		return ManifestView{}, err
	}
	return ManifestView{
		ID: m.ID, Server: name, Hash: m.Hash, State: m.State, Risk: m.RiskLevel, CreatedAt: m.CreatedAt,
	}, nil
}
