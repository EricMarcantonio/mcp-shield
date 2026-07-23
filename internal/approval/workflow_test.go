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

func TestCheckAndRecordFirstManifestIsPendingAndFullyBlocked(t *testing.T) {
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read", "calendar_create")

	res, err := wf.CheckAndRecord(context.Background(), serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	// First-ever connect: no approved baseline to diff against, so
	// nothing is "unchanged" yet — fail closed on everything.
	if len(res.SafeTools) != 0 {
		t.Fatalf("expected no safe tools on first-ever connect, got %v", res.SafeTools)
	}
	if res.Warn {
		t.Fatalf("expected warn=false in block mode")
	}
	if res.State != database.StatePending {
		t.Fatalf("expected PENDING, got %s", res.State)
	}
	if res.ManifestID == 0 {
		t.Fatalf("expected non-zero manifest id")
	}
}

func TestApproveThenSameHashIsFullyAllowed(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read", "calendar_create")

	first, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if err := wf.Approve(ctx, first.ManifestID, "eric", "looks fine"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	second, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(second.SafeTools) != 2 || !second.SafeTools["calendar_read"] || !second.SafeTools["calendar_create"] {
		t.Fatalf("expected both tools safe once approved, got %v", second.SafeTools)
	}
	if second.ManifestID != first.ManifestID {
		t.Fatalf("expected same manifest row to be reused, got %d vs %d", second.ManifestID, first.ManifestID)
	}
	if second.State != database.StateApproved {
		t.Fatalf("expected APPROVED, got %s", second.State)
	}
}

func TestApproveSupersedesPriorApproved(t *testing.T) {
	ctx := context.Background()
	wf, store, serverID := newTestWorkflow(t)

	v1 := toolManifest("calendar_read", "calendar_create")
	r1, _ := wf.CheckAndRecord(ctx, serverID, v1)
	if err := wf.Approve(ctx, r1.ManifestID, "eric", ""); err != nil {
		t.Fatalf("approve v1: %v", err)
	}

	v2 := toolManifest("calendar_read", "calendar_create", "upload_attachment")
	r2, _ := wf.CheckAndRecord(ctx, serverID, v2)
	if err := wf.Approve(ctx, r2.ManifestID, "eric", ""); err != nil {
		t.Fatalf("approve v2: %v", err)
	}

	rec1, err := store.GetManifestByID(ctx, r1.ManifestID)
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
	if approved == nil || approved.ID != r2.ManifestID {
		t.Fatalf("expected exactly v2 to be the approved manifest, got %+v", approved)
	}
}

func TestRejectLeavesBlocked(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("delete_calendar", "execute_command")

	first, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Reject(ctx, first.ManifestID, "eric", "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	second, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("re-check after reject: %v", err)
	}
	if len(second.SafeTools) != 0 {
		t.Fatalf("expected no safe tools (no approved baseline at all), got %v", second.SafeTools)
	}
	if second.ManifestID != first.ManifestID {
		t.Fatalf("expected the same REJECTED row to be found, not a new insert, got %d vs %d", second.ManifestID, first.ManifestID)
	}
	if second.State != database.StateRejected {
		t.Fatalf("expected REJECTED, got %s", second.State)
	}
}

func TestRejectedChangeStillAllowsUnchangedTools(t *testing.T) {
	// The behavior this whole redesign is for: approve a baseline, then
	// propose a change that adds a risky tool, reject it — the tools
	// that already existed and didn't change must keep working.
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)

	baseline := toolManifest("calendar_read", "calendar_create")
	r1, _ := wf.CheckAndRecord(ctx, serverID, baseline)
	if err := wf.Approve(ctx, r1.ManifestID, "eric", ""); err != nil {
		t.Fatalf("approve baseline: %v", err)
	}

	risky := toolManifest("calendar_read", "calendar_create", "delete_calendar", "execute_command")
	r2, err := wf.CheckAndRecord(ctx, serverID, risky)
	if err != nil {
		t.Fatalf("check risky: %v", err)
	}
	if err := wf.Reject(ctx, r2.ManifestID, "eric", "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	after, err := wf.CheckAndRecord(ctx, serverID, risky)
	if err != nil {
		t.Fatalf("re-check after reject: %v", err)
	}
	if after.State != database.StateRejected {
		t.Fatalf("expected REJECTED, got %s", after.State)
	}
	if !after.SafeTools["calendar_read"] || !after.SafeTools["calendar_create"] {
		t.Fatalf("expected unchanged baseline tools to stay safe after rejection, got %v", after.SafeTools)
	}
	if after.SafeTools["delete_calendar"] || after.SafeTools["execute_command"] {
		t.Fatalf("expected the rejected new tools to stay blocked, got %v", after.SafeTools)
	}
}

func TestApproveNonPendingIsRejected(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read")

	first, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Approve(ctx, first.ManifestID, "eric", ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}

	// Approving an already-APPROVED manifest is an illegal transition.
	if err := wf.Approve(ctx, first.ManifestID, "eric", ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("expected ErrNotPending, got %v", err)
	}
}

func TestRejectNonPendingIsRejected(t *testing.T) {
	ctx := context.Background()
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest("calendar_read")

	first, _ := wf.CheckAndRecord(ctx, serverID, m)
	if err := wf.Reject(ctx, first.ManifestID, "eric", "no"); err != nil {
		t.Fatalf("first reject: %v", err)
	}
	if err := wf.Reject(ctx, first.ManifestID, "eric", "no again"); !errors.Is(err, ErrNotPending) {
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

	res, err := wf.CheckAndRecord(ctx, srv.ID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	if !res.SafeTools["calendar_read"] {
		t.Fatalf("expected warn mode to allow traffic, got %v", res.SafeTools)
	}
	if !res.Warn {
		t.Fatalf("expected warn=true for an unapproved manifest in warn mode")
	}
	if res.State != database.StatePending {
		t.Fatalf("expected PENDING, got %s", res.State)
	}
}
