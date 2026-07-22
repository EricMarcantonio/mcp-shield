package database

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestServerCRUD(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	srv, err := store.CreateServer(ctx, "calendar", "stdio:calendar")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if srv.ID == 0 {
		t.Fatalf("expected non-zero id")
	}

	got, err := store.GetServerByName(ctx, "calendar")
	if err != nil || got == nil {
		t.Fatalf("get by name: %v, %v", got, err)
	}
	if got.ID != srv.ID {
		t.Fatalf("expected same id, got %d vs %d", got.ID, srv.ID)
	}

	missing, err := store.GetServerByName(ctx, "nope")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for missing server")
	}
}

func TestManifestCompositeUniqueIndex(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	srvA, _ := store.CreateServer(ctx, "a", "")
	srvB, _ := store.CreateServer(ctx, "b", "")

	rec := &ManifestRecord{ServerID: srvA.ID, Hash: "deadbeef", CanonicalJSON: "{}", State: StatePending}
	if _, err := store.InsertManifest(ctx, rec); err != nil {
		t.Fatalf("insert manifest for A: %v", err)
	}

	// Same hash, different server: must succeed (composite unique key).
	recB := &ManifestRecord{ServerID: srvB.ID, Hash: "deadbeef", CanonicalJSON: "{}", State: StatePending}
	if _, err := store.InsertManifest(ctx, recB); err != nil {
		t.Fatalf("expected identical hash to be allowed for a different server, got: %v", err)
	}

	// Same hash, same server: must fail (duplicate).
	recADup := &ManifestRecord{ServerID: srvA.ID, Hash: "deadbeef", CanonicalJSON: "{}", State: StatePending}
	if _, err := store.InsertManifest(ctx, recADup); err == nil {
		t.Fatalf("expected duplicate (server_id, hash) insert to fail")
	}
}

func TestUpdateManifestStateOnlyTouchesState(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	srv, _ := store.CreateServer(ctx, "a", "")
	rec := &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: `{"tools":[]}`, State: StatePending}
	id, err := store.InsertManifest(ctx, rec)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.UpdateManifestState(ctx, id, StateApproved); err != nil {
		t.Fatalf("update state: %v", err)
	}

	got, err := store.GetManifestByID(ctx, id)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.State != StateApproved {
		t.Fatalf("expected state APPROVED, got %s", got.State)
	}
	if got.Hash != "h1" || got.CanonicalJSON != `{"tools":[]}` {
		t.Fatalf("expected hash/canonical_json untouched by state update, got hash=%s json=%s", got.Hash, got.CanonicalJSON)
	}
}

func TestUpdateManifestStateNotFound(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.UpdateManifestState(ctx, 999, StateApproved); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListPendingManifests(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")

	pendingID, _ := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "p1", CanonicalJSON: "{}", State: StatePending})
	_, _ = store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "p2", CanonicalJSON: "{}", State: StateApproved})

	pending, err := store.ListPendingManifests(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != pendingID {
		t.Fatalf("expected exactly the one pending manifest, got %+v", pending)
	}
}

func TestApprovalAuditTrail(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")
	manifestID, _ := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h", CanonicalJSON: "{}", State: StatePending})

	_, err := store.InsertApproval(ctx, &Approval{ManifestID: manifestID, Decision: DecisionApproved, Username: "eric", Reason: "looks fine"})
	if err != nil {
		t.Fatalf("insert approval: %v", err)
	}

	history, err := store.ListApprovalsForManifest(ctx, manifestID)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	if len(history) != 1 || history[0].Decision != DecisionApproved {
		t.Fatalf("expected one APPROVED entry, got %+v", history)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")

	sentinel := &txSentinelErr{}
	err := store.WithTx(ctx, func(tx Store) error {
		if _, err := tx.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h", CanonicalJSON: "{}", State: StatePending}); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	pending, _ := store.ListPendingManifests(ctx)
	if len(pending) != 0 {
		t.Fatalf("expected rollback to discard the insert, got %+v", pending)
	}
}

type txSentinelErr struct{}

func (e *txSentinelErr) Error() string { return "sentinel" }
