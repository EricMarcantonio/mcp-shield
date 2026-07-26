package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// TestGateAdapterCreatesServerOnFirstSight covers gateAdapter.CheckAndRecord
// (internal/app/app.go:138-163), the bridge between mcp.Gate and
// approval.Workflow that had zero test coverage: it resolves-or-creates the
// database Server row for a server name the gateway hasn't seen before, then
// delegates to the workflow. The first call for a brand new server name must
// create exactly one Server row (not one per call) and — since there is no
// approved baseline yet — report empty safe sets, the fail-closed starting
// state.
func TestGateAdapterCreatesServerOnFirstSight(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	workflow := approval.New(store, approval.FailModeBlock)
	gate := &gateAdapter{store: store, workflow: workflow}

	ctx := context.Background()
	first, err := gate.CheckAndRecord(ctx, "newsrv", nil, nil, nil)
	if err != nil {
		t.Fatalf("first CheckAndRecord: %v", err)
	}
	if len(first.SafeTools) != 0 || len(first.SafePrompts) != 0 || len(first.SafeResources) != 0 {
		t.Fatalf("expected empty safe sets for a server with no approved baseline, got %+v", first)
	}

	second, err := gate.CheckAndRecord(ctx, "newsrv", nil, nil, nil)
	if err != nil {
		t.Fatalf("second CheckAndRecord: %v", err)
	}
	if len(second.SafeTools) != 0 || len(second.SafePrompts) != 0 || len(second.SafeResources) != 0 {
		t.Fatalf("expected empty safe sets on the second call too, got %+v", second)
	}

	srv, err := store.GetServerByName(ctx, "newsrv")
	if err != nil {
		t.Fatalf("get server by name: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a server row to have been created")
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected exactly one server row after two calls for the same name, got %d", len(servers))
	}
}
