package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestOpenCreatesMissingParentDirectory covers first launch on a clean
// machine. DATABASE_PATH defaults to "data/mcp.db" and nothing created
// "data/", so a release binary or container started in a fresh directory
// died on the first PRAGMA with "unable to open database file (14)". It was
// masked in development because docker-compose bind-mounts a volume over
// /app/data and the repo has a committed data/ directory.
func TestOpenCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "mcp.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open with a missing parent directory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateServer(context.Background(), "calendar", ""); err != nil {
		t.Fatalf("store unusable after creating its directory: %v", err)
	}
}

// TestOpenCreatesPrivateDirectory pins the permission bits. This directory
// holds the approvals audit trail — the record of who authorized which
// capability change — so it is not an ordinary cache directory that other
// local users may read.
func TestOpenCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	store, err := Open(filepath.Join(dir, "mcp.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o700) {
		t.Fatalf("expected the audit database directory to be private (0700), got %#o", perm)
	}
}

// TestOpenAcceptsPathsWithoutADirectoryComponent covers a bare filename and
// SQLite's in-memory DSNs, none of which name a directory to create.
func TestOpenAcceptsPathsWithoutADirectoryComponent(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:", "file::memory:?cache=shared"} {
		t.Run(path, func(t *testing.T) {
			store, err := Open(path)
			if err != nil {
				t.Fatalf("open %q: %v", path, err)
			}
			_ = store.Close()
		})
	}
}

// TestAbsentRowsReturnErrNotFound pins the one absence convention. Four
// lookups used to return (nil, nil) while GetManifestByID returned
// ErrNotFound, so every caller had to remember which kind each one was — and
// a forgotten nil check is a nil dereference, not a compile error.
func TestAbsentRowsReturnErrNotFound(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	lookups := map[string]func() (any, error){
		"GetServerByName": func() (any, error) { return store.GetServerByName(ctx, "no-such-server") },
		"GetServerByID":   func() (any, error) { return store.GetServerByID(ctx, 9999) },
		"GetManifestByID": func() (any, error) { return store.GetManifestByID(ctx, 9999) },
		"GetManifestByHash": func() (any, error) {
			return store.GetManifestByHash(ctx, 9999, "no-such-hash")
		},
		"GetApprovedManifest": func() (any, error) { return store.GetApprovedManifest(ctx, 9999) },
	}
	for name, lookup := range lookups {
		t.Run(name, func(t *testing.T) {
			_, err := lookup()
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s on a missing row returned %v, want ErrNotFound", name, err)
			}
		})
	}
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

	if _, err := store.GetServerByName(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a missing server, got %v", err)
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

	if _, err := store.GetServerByID(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a missing server id, got %v", err)
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

	if _, err := store.GetManifestByHash(ctx, srv.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown hash, got %v", err)
	}
}

func TestGetApprovedManifestNone(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")
	if _, err := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h1", CanonicalJSON: "{}", State: StatePending}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := store.GetApprovedManifest(ctx, srv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when no manifest is APPROVED, got %v", err)
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
	if err := store.UpdateManifestState(ctx, 999, StateApproved); !errors.Is(err, ErrNotFound) {
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
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	pending, _ := store.ListPendingManifests(ctx)
	if len(pending) != 0 {
		t.Fatalf("expected rollback to discard the insert, got %+v", pending)
	}
}

type txSentinelErr struct{}

func (e *txSentinelErr) Error() string { return "sentinel" }

// --- notification outbox ----------------------------------------------------

// TestOpenUpgradesAV010DatabaseInPlace covers the one thing v0.1.0 shipping
// changed about this package: user databases are no longer disposable. A
// database created by 0.1.0 has no notification_outbox table, and there is
// no migration framework — only the idempotent schema applied on Open. This
// asserts both halves of "additive and safe": the new table appears, and
// every pre-existing row is still there afterwards.
func TestOpenUpgradesAV010DatabaseInPlace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v010.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	// Verbatim v0.1.0 schema: servers, manifests, approvals. No outbox.
	const v010Schema = `
CREATE TABLE servers (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, endpoint TEXT, created_at DATETIME NOT NULL);
CREATE TABLE manifests (id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL REFERENCES servers(id), hash TEXT NOT NULL,
  canonical_json TEXT NOT NULL, state TEXT NOT NULL, diff_json TEXT, created_at DATETIME NOT NULL);
CREATE UNIQUE INDEX idx_manifests_server_hash ON manifests(server_id, hash);
CREATE TABLE approvals (id INTEGER PRIMARY KEY, manifest_id INTEGER NOT NULL REFERENCES manifests(id),
  decision TEXT NOT NULL, username TEXT NOT NULL, reason TEXT, created_at DATETIME NOT NULL);
INSERT INTO servers (id, name, endpoint, created_at) VALUES (1, 'calendar', '', '2026-01-01 00:00:00+00:00');
INSERT INTO manifests (id, server_id, hash, canonical_json, state, diff_json, created_at)
  VALUES (1, 1, 'abc', '{}', 'APPROVED', NULL, '2026-01-01 00:00:00+00:00');
INSERT INTO approvals (id, manifest_id, decision, username, reason, created_at)
  VALUES (1, 1, 'APPROVED', 'eric', 'baseline', '2026-01-01 00:00:00+00:00');
`
	if _, err := legacy.ExecContext(ctx, v010Schema); err != nil {
		t.Fatalf("build legacy db: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("opening a v0.1.0 database must succeed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv, err := store.GetServerByName(ctx, "calendar")
	if err != nil {
		t.Fatalf("pre-existing server row must survive the upgrade: %v", err)
	}
	rec, err := store.GetApprovedManifest(ctx, srv.ID)
	if err != nil {
		t.Fatalf("pre-existing approved manifest must survive the upgrade: %v", err)
	}
	if rec.Hash != "abc" {
		t.Fatalf("approved baseline changed across the upgrade: %+v", rec)
	}
	history, err := store.ListApprovalsForManifest(ctx, rec.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("pre-existing audit trail must survive the upgrade: %v, %+v", err, history)
	}

	if _, err := store.EnqueueNotification(ctx, EventManifestPending, rec.ID); err != nil {
		t.Fatalf("the outbox table must be created on an existing database: %v", err)
	}
}

func insertTestManifest(t *testing.T, store Store) int64 {
	t.Helper()
	ctx := context.Background()
	srv, err := store.CreateServer(ctx, "outbox-srv", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	id, err := store.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h", CanonicalJSON: "{}", State: StatePending})
	if err != nil {
		t.Fatalf("insert manifest: %v", err)
	}
	return id
}

func TestEnqueuedNotificationIsImmediatelyDue(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	manifestID := insertTestManifest(t, store)

	id, err := store.EnqueueNotification(ctx, EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero outbox row id: it is the receiver's dedupe key")
	}

	due, err := store.DueNotifications(ctx, time.Now().UTC(), 6, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected the new row to be due now, got %d rows", len(due))
	}
	if due[0].ID != id || due[0].EventType != EventManifestPending || due[0].ManifestID != manifestID {
		t.Fatalf("unexpected row: %+v", due[0])
	}
	if due[0].Attempts != 0 || due[0].DeliveredAt != nil {
		t.Fatalf("a fresh row must be undelivered with zero attempts, got %+v", due[0])
	}
}

func TestDueNotificationsExcludesRowsScheduledInTheFuture(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	manifestID := insertTestManifest(t, store)

	id, err := store.EnqueueNotification(ctx, EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	now := time.Now().UTC()
	if err := store.MarkNotificationFailed(ctx, id, now.Add(time.Hour), "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	due, err := store.DueNotifications(ctx, now, 6, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a row rescheduled an hour out must not be due now, got %+v", due)
	}

	later, err := store.DueNotifications(ctx, now.Add(2*time.Hour), 6, 10)
	if err != nil {
		t.Fatalf("due later: %v", err)
	}
	if len(later) != 1 {
		t.Fatalf("expected the row to be due once its backoff elapsed, got %d rows", len(later))
	}
	if later[0].Attempts != 1 || later[0].LastError != "boom" {
		t.Fatalf("MarkNotificationFailed must record attempts and the error, got %+v", later[0])
	}
}

func TestMarkNotificationDeliveredRemovesItFromTheDueSet(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	manifestID := insertTestManifest(t, store)
	id, _ := store.EnqueueNotification(ctx, EventManifestPending, manifestID)

	now := time.Now().UTC()
	if err := store.MarkNotificationDelivered(ctx, id, now); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	due, err := store.DueNotifications(ctx, now.Add(24*time.Hour), 6, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a delivered row must never be redelivered, got %+v", due)
	}
}

func TestMarkNotificationOnMissingRowReportsNotFound(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().UTC()

	if err := store.MarkNotificationDelivered(ctx, 999, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound marking a missing row delivered, got %v", err)
	}
	if err := store.MarkNotificationFailed(ctx, 999, now, "boom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound marking a missing row failed, got %v", err)
	}
}

func TestListUndeliveredNotificationsFiltersByAttempts(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	manifestID := insertTestManifest(t, store)

	exhausted, _ := store.EnqueueNotification(ctx, EventManifestPending, manifestID)
	fresh, _ := store.EnqueueNotification(ctx, EventManifestApproved, manifestID)

	now := time.Now().UTC()
	for range 2 {
		if err := store.MarkNotificationFailed(ctx, exhausted, now, "target refused"); err != nil {
			t.Fatalf("mark failed: %v", err)
		}
	}

	failed, err := store.ListUndeliveredNotifications(ctx, 2)
	if err != nil {
		t.Fatalf("list undelivered: %v", err)
	}
	if len(failed) != 1 || failed[0].ID != exhausted {
		t.Fatalf("expected only the row that burned through its attempts, got %+v", failed)
	}
	if failed[0].Attempts != 2 {
		t.Fatalf("expected 2 recorded attempts, got %d", failed[0].Attempts)
	}

	if err := store.MarkNotificationDelivered(ctx, fresh, now); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	for range 2 {
		if err := store.MarkNotificationFailed(ctx, fresh, now, "late failure"); err != nil {
			t.Fatalf("mark failed: %v", err)
		}
	}
	stillOne, err := store.ListUndeliveredNotifications(ctx, 2)
	if err != nil {
		t.Fatalf("list undelivered: %v", err)
	}
	if len(stillOne) != 1 {
		t.Fatalf("a delivered row must never appear in the failed view, got %+v", stillOne)
	}
}

// TestEnqueueNotificationRollsBackWithItsTransaction is the property the
// whole outbox design rests on: the event exists if and only if the manifest
// row does. If the enqueue could survive a rolled-back manifest insert (or
// vice versa) the gate and the approver would disagree about what happened.
func TestEnqueueNotificationRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	srv, _ := store.CreateServer(ctx, "a", "")

	sentinel := &txSentinelErr{}
	err := store.WithTx(ctx, func(tx Store) error {
		id, err := tx.InsertManifest(ctx, &ManifestRecord{ServerID: srv.ID, Hash: "h", CanonicalJSON: "{}", State: StatePending})
		if err != nil {
			return err
		}
		if _, err := tx.EnqueueNotification(ctx, EventManifestPending, id); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	due, err := store.DueNotifications(ctx, time.Now().UTC(), 6, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("the outbox row must roll back with the manifest insert, got %+v", due)
	}
}

// TestDueNotificationsOrdersByIDAndHonoursLimit pins the ordering promise the
// docs make: per-server monotonic order by outbox id.
func TestDueNotificationsOrdersByIDAndHonoursLimit(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	manifestID := insertTestManifest(t, store)

	var ids []int64
	for range 3 {
		id, err := store.EnqueueNotification(ctx, EventManifestPending, manifestID)
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		ids = append(ids, id)
	}

	due, err := store.DueNotifications(ctx, time.Now().UTC(), 6, 2)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected the limit to be honoured, got %d rows", len(due))
	}
	if due[0].ID != ids[0] || due[1].ID != ids[1] {
		t.Fatalf("expected ascending id order, got %d then %d", due[0].ID, due[1].ID)
	}
}
