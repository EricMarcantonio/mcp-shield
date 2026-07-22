package approval

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

func newTestWorkflow(t *testing.T) (*Workflow, database.Store, int64) {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv, err := store.CreateServer(context.Background(), "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	return New(store, FailModeBlock), store, srv.ID
}

func toolManifest(names ...string) *manifest.Manifest {
	tools := make([]mcp.Tool, len(names))
	for i, n := range names {
		tools[i] = mcp.Tool{Name: n}
	}
	return manifest.Build(tools, nil, nil)
}

func TestCheckAndRecordFirstManifestIsPendingAndBlocked(t *testing.T) {
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read", "calendar_create")

	allowed, warn, id, state, err := wf.CheckAndRecord(context.Background(), serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	if allowed {
		t.Fatalf("expected first-ever manifest to be blocked (fail-closed default)")
	}
	if warn {
		t.Fatalf("expected warn=false in block mode")
	}
	if state != database.StatePending {
		t.Fatalf("expected PENDING, got %s", state)
	}
	if id == 0 {
		t.Fatalf("expected non-zero manifest id")
	}
}

func TestApproveThenSameHashIsAllowed(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read", "calendar_create")

	_, _, id, _, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if err := wf.Approve(ctx, id, "eric", "looks fine"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	allowed, _, id2, state, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if !allowed {
		t.Fatalf("expected approved manifest to be allowed")
	}
	if id2 != id {
		t.Fatalf("expected same manifest row to be reused, got %d vs %d", id2, id)
	}
	if state != database.StateApproved {
		t.Fatalf("expected APPROVED, got %s", state)
	}
}

func TestApproveSupersedesPriorApproved(t *testing.T) {
	ctx := context.Background()
	wf, store, serverID := newTestWorkflow(t)

	v1 := toolManifest("calendar_read", "calendar_create")
	_, _, id1, _, _ := wf.CheckAndRecord(ctx, serverID, v1)
	if err := wf.Approve(ctx, id1, "eric", ""); err != nil {
		t.Fatalf("approve v1: %v", err)
	}

	v2 := toolManifest("calendar_read", "calendar_create", "upload_attachment")
	_, _, id2, _, _ := wf.CheckAndRecord(ctx, serverID, v2)
	if err := wf.Approve(ctx, id2, "eric", ""); err != nil {
		t.Fatalf("approve v2: %v", err)
	}

	rec1, err := store.GetManifestByID(ctx, id1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if rec1.State != database.StateSuperseded {
		t.Fatalf("expected v1 to be SUPERSEDED, got %s", rec1.State)
	}

	approved, err := store.GetApprovedManifest(ctx, serverID)
	if err != nil {
		t.Fatalf("get approved: %v", err)
	}
	if approved == nil || approved.ID != id2 {
		t.Fatalf("expected exactly v2 to be the approved manifest, got %+v", approved)
	}
}

func TestRejectLeavesBlocked(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("delete_calendar", "execute_command")

	_, _, id, _, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Reject(ctx, id, "eric", "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	allowed, _, id2, state, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("re-check after reject: %v", err)
	}
	if allowed {
		t.Fatalf("expected rejected manifest to remain blocked")
	}
	if id2 != id {
		t.Fatalf("expected the same REJECTED row to be found, not a new insert, got %d vs %d", id2, id)
	}
	if state != database.StateRejected {
		t.Fatalf("expected REJECTED, got %s", state)
	}
}

func TestApproveNonPendingIsRejected(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read")

	_, _, id, _, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Approve(ctx, id, "eric", ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}

	// Approving an already-APPROVED manifest is an illegal transition.
	if err := wf.Approve(ctx, id, "eric", ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("expected ErrNotPending, got %v", err)
	}
}

func TestRejectNonPendingIsRejected(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read")

	_, _, id, _, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Reject(ctx, id, "eric", "no"); err != nil {
		t.Fatalf("first reject: %v", err)
	}
	if err := wf.Reject(ctx, id, "eric", "no again"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("expected ErrNotPending, got %v", err)
	}
}

func TestFailModeWarnAllowsButFlags(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv, _ := store.CreateServer(ctx, "calendar", "")

	wf := New(store, FailModeWarn)
	m := toolManifest("calendar_read")

	allowed, warn, _, state, err := wf.CheckAndRecord(ctx, srv.ID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	if !allowed {
		t.Fatalf("expected warn mode to allow traffic")
	}
	if !warn {
		t.Fatalf("expected warn=true for an unapproved manifest in warn mode")
	}
	if state != database.StatePending {
		t.Fatalf("expected PENDING, got %s", state)
	}
}
