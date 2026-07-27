package approval

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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
	t.Cleanup(func() { _ = store.Close() })

	srv, err := store.CreateServer(context.Background(), "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	return New(store, FailModeBlock), store, srv.ID
}

func toolManifest(t *testing.T, names ...string) *manifest.Manifest {
	t.Helper()
	tools := make([]mcp.Tool, len(names))
	for i, n := range names {
		tools[i] = mcp.Tool{Name: n}
	}
	m, err := manifest.Build(tools, nil, nil)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return m
}

func TestCheckAndRecordFirstManifestIsPendingAndFullyBlocked(t *testing.T) {
	wf, _, serverID := newTestWorkflow(t)
	m := toolManifest(t, "calendar_read", "calendar_create")

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
	m := toolManifest(t, "calendar_read", "calendar_create")

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

	v1 := toolManifest(t, "calendar_read", "calendar_create")
	r1, _ := wf.CheckAndRecord(ctx, serverID, v1)
	if err := wf.Approve(ctx, r1.ManifestID, "eric", ""); err != nil {
		t.Fatalf("approve v1: %v", err)
	}

	v2 := toolManifest(t, "calendar_read", "calendar_create", "upload_attachment")
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
	m := toolManifest(t, "delete_calendar", "execute_command")

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

	baseline := toolManifest(t, "calendar_read", "calendar_create")
	r1, _ := wf.CheckAndRecord(ctx, serverID, baseline)
	if err := wf.Approve(ctx, r1.ManifestID, "eric", ""); err != nil {
		t.Fatalf("approve baseline: %v", err)
	}

	risky := toolManifest(t, "calendar_read", "calendar_create", "delete_calendar", "execute_command")
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
	m := toolManifest(t, "calendar_read")

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
	m := toolManifest(t, "calendar_read")

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
	t.Cleanup(func() { _ = store.Close() })
	srv, _ := store.CreateServer(ctx, "calendar", "")

	wf := New(store, FailModeWarn)
	m := toolManifest(t, "calendar_read")

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

// --- notifications ----------------------------------------------------------

func newNotifyingWorkflow(t *testing.T) (*Workflow, database.Store, int64) {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, err := store.CreateServer(context.Background(), "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	return New(store, FailModeBlock, WithNotifications()), store, srv.ID
}

func queuedNotifications(t *testing.T, store database.Store) []database.OutboxRow {
	t.Helper()
	rows, err := store.DueNotifications(context.Background(), time.Now().UTC().Add(time.Hour), 100, 100)
	if err != nil {
		t.Fatalf("due notifications: %v", err)
	}
	return rows
}

func TestNotificationsAreOffUnlessRequested(t *testing.T) {
	ctx := context.Background()
	wf, store, serverID := newTestWorkflow(t)

	if _, err := wf.CheckAndRecord(ctx, serverID, toolManifest(t, "calendar_read")); err != nil {
		t.Fatalf("check and record: %v", err)
	}
	if rows := queuedNotifications(t, store); len(rows) != 0 {
		t.Fatalf("a workflow built without WithNotifications must enqueue nothing, got %+v", rows)
	}
}

func TestNewPendingManifestEnqueuesNotification(t *testing.T) {
	ctx := context.Background()
	wf, store, serverID := newNotifyingWorkflow(t)
	m := toolManifest(t, "calendar_read")

	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}

	rows := queuedNotifications(t, store)
	if len(rows) != 1 {
		t.Fatalf("expected one queued notification, got %d", len(rows))
	}
	if rows[0].EventType != database.EventManifestPending || rows[0].ManifestID != res.ManifestID {
		t.Fatalf("unexpected outbox row: %+v", rows[0])
	}

	// A reconnect re-presents the same hash. The manifest row is reused, so
	// no second event: a flapping upstream must not become a notification
	// storm for a change the approver has already been told about.
	if _, err := wf.CheckAndRecord(ctx, serverID, m); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if rows := queuedNotifications(t, store); len(rows) != 1 {
		t.Fatalf("re-seeing a known hash must not enqueue again, got %d rows", len(rows))
	}
}

func TestApproveAndRejectEnqueueTheirDecisions(t *testing.T) {
	ctx := context.Background()
	wf, store, serverID := newNotifyingWorkflow(t)

	approved, err := wf.CheckAndRecord(ctx, serverID, toolManifest(t, "calendar_read"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := wf.Approve(ctx, approved.ManifestID, "eric", "fine"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	rejected, err := wf.CheckAndRecord(ctx, serverID, toolManifest(t, "calendar_read", "calendar_delete"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := wf.Reject(ctx, rejected.ManifestID, "eric", "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	var seen []string
	for _, row := range queuedNotifications(t, store) {
		seen = append(seen, row.EventType)
	}
	want := []string{
		database.EventManifestPending,
		database.EventManifestApproved,
		database.EventManifestPending,
		database.EventManifestRejected,
	}
	if len(seen) != len(want) {
		t.Fatalf("expected %v, got %v", want, seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("expected %v in outbox id order, got %v", want, seen)
		}
	}
}

// TestManifestAndItsNotificationCommitTogether is the transactional outbox
// property seen from the gate's side. If the enqueue fails, the manifest
// insert must fail with it — otherwise the gate could withhold a capability
// that no event will ever announce, which is the silent failure this whole
// feature exists to remove.
func TestManifestAndItsNotificationCommitTogether(t *testing.T) {
	ctx := context.Background()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, _ := store.CreateServer(ctx, "calendar", "")

	failing := &enqueueFailingStore{Store: store}
	wf := New(failing, FailModeBlock, WithNotifications())

	if _, err := wf.CheckAndRecord(ctx, srv.ID, toolManifest(t, "calendar_read")); err == nil {
		t.Fatal("expected CheckAndRecord to fail when the outbox write fails")
	}

	pending, err := store.ListPendingManifests(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("the manifest must roll back with its notification, got %+v", pending)
	}
}

type enqueueFailingStore struct {
	database.Store
}

func (s *enqueueFailingStore) EnqueueNotification(context.Context, string, int64) (int64, error) {
	return 0, errors.New("outbox is unavailable")
}

func (s *enqueueFailingStore) WithTx(ctx context.Context, fn func(database.Store) error) error {
	return s.Store.WithTx(ctx, func(tx database.Store) error {
		return fn(&enqueueFailingStore{Store: tx})
	})
}
