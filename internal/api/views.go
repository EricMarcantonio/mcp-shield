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

func toPendingView(ctx context.Context, store database.Store, m database.ManifestRecord) (PendingManifestView, error) {
	srv, err := store.GetServerByID(ctx, m.ServerID)
	if err != nil {
		return PendingManifestView{}, err
	}
	name := "unknown"
	if srv != nil {
		name = srv.Name
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
	srv, err := store.GetServerByID(ctx, m.ServerID)
	if err != nil {
		return ManifestView{}, err
	}
	name := "unknown"
	if srv != nil {
		name = srv.Name
	}
	return ManifestView{
		ID: m.ID, Server: name, Hash: m.Hash, State: m.State, Risk: m.RiskLevel, CreatedAt: m.CreatedAt,
	}, nil
}
