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

func TestGetServerByID(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	srv, err := store.CreateServer(ctx, "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	got, err := store.GetServerByID(ctx, srv.ID)
	if err != nil || got == nil {
		t.Fatalf("get by id: %v, %v", got, err)
	}
	if got.Name != "calendar" {
		t.Fatalf("expected name calendar, got %q", got.Name)
	}

	missing, err := store.GetServerByID(ctx, 9999)
	if err != nil {
		t.Fatalf("get missing by id: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for a missing server id, got %+v", missing)
	}
}

func TestListServersEmpty(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("expected no servers, got %+v", servers)
	}
}

func TestListServersOrderedByName(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	if _, err := store.CreateServer(ctx, "zeta", ""); err != nil {
		t.Fatalf("create zeta: %v", err)
	}
	if _, err := store.CreateServer(ctx, "alpha", ""); err != nil {
		t.Fatalf("create alpha: %v", err)
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 2 || servers[0].Name != "alpha" || servers[1].Name != "zeta" {
		t.Fatalf("expected [alpha, zeta], got %+v", servers)
	}
}

func TestGetManifestByHash(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")

	id, err := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: "{}", State: StatePending})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetManifestByHash(ctx, srv.ID, "h1")
	if err != nil || got == nil {
		t.Fatalf("get by hash: %v, %v", got, err)
	}
	if got.ID != id {
		t.Fatalf("expected id %d, got %d", id, got.ID)
	}

	missing, err := store.GetManifestByHash(ctx, srv.ID, "nope")
	if err != nil {
		t.Fatalf("get missing by hash: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for an unknown hash, got %+v", missing)
	}
}

func TestGetApprovedManifestNone(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")
	if _, err := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: "{}", State: StatePending}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	approved, err := store.GetApprovedManifest(ctx, srv.ID)
	if err != nil {
		t.Fatalf("get approved: %v", err)
	}
	if approved != nil {
		t.Fatalf("expected nil when no manifest is APPROVED, got %+v", approved)
	}
}

func TestGetApprovedManifestFound(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")
	id, err := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: "{}", State: StatePending})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateManifestState(ctx, id, StateApproved); err != nil {
		t.Fatalf("update state: %v", err)
	}

	approved, err := store.GetApprovedManifest(ctx, srv.ID)
	if err != nil || approved == nil {
		t.Fatalf("get approved: %v, %v", approved, err)
	}
	if approved.ID != id {
		t.Fatalf("expected the approved manifest %d, got %+v", id, approved)
	}
}

// TestWithTxForwardsReadsToTheSameTransaction exercises txStore's read
// methods (GetServerByID, ListServers, GetManifestByHash, GetApprovedManifest)
// through WithTx, not just SQLiteStore's — the two forward to shared helpers
// (finding S1) but are still two separate call paths worth pinning: a read
// inside an in-flight transaction must see that transaction's own writes
// before it commits.
func TestWithTxForwardsReadsToTheSameTransaction(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	err := store.WithTx(ctx, func(tx Store) error {
		srv, err := tx.CreateServer(ctx, "a", "")
		if err != nil {
			return err
		}
		if _, err := tx.GetServerByID(ctx, srv.ID); err != nil {
			return err
		}
		if _, err := tx.GetServerByName(ctx, "a"); err != nil {
			return err
		}
		if _, err := tx.ListServers(ctx); err != nil {
			return err
		}

		id, err := tx.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: "{}", State: StatePending})
		if err != nil {
			return err
		}
		if _, err := tx.GetManifestByHash(ctx, srv.ID, "h1"); err != nil {
			return err
		}
		if err := tx.UpdateManifestState(ctx, id, StateApproved); err != nil {
			return err
		}
		if _, err := tx.GetApprovedManifest(ctx, srv.ID); err != nil {
			return err
		}
		if _, err := tx.ListPendingManifests(ctx); err != nil {
			return err
		}
		if _, err := tx.InsertApproval(ctx, &Approval{ManifestID: id, Decision: DecisionApproved, Username: "eric"}); err != nil {
			return err
		}
		_, err = tx.ListApprovalsForManifest(ctx, id)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// Committed: visible outside the transaction too.
	approved, err := store.GetApprovedManifest(ctx, 1)
	if err != nil || approved == nil {
		t.Fatalf("expected the committed approval visible after WithTx returns: %v, %v", approved, err)
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
