package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
)

// --- fixtures ---------------------------------------------------------------

func openStore(t *testing.T) *database.SQLiteStore {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seedPendingManifest writes a server plus a PENDING manifest whose diff
// records one added tool, and returns the manifest id.
func seedPendingManifest(t *testing.T, store database.Store) int64 {
	t.Helper()
	ctx := context.Background()
	srv, err := store.CreateServer(ctx, "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	d := &diff.Diff{AddedTools: []string{"upload_attachment"}}
	diffJSON, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	id, err := store.InsertManifest(ctx, &database.ManifestRecord{
		ServerID: srv.ID, Hash: "abc123", CanonicalJSON: "{}",
		State: database.StatePending, DiffJSON: string(diffJSON),
	})
	if err != nil {
		t.Fatalf("insert manifest: %v", err)
	}
	return id
}

func testConfig() *Config {
	return &Config{
		Events:       []string{database.EventManifestPending},
		MaxAttempts:  DefaultMaxAttempts,
		DashboardURL: "http://localhost:8081",
	}
}

// recordingNotifier is a Notifier under test control: it records every event
// it sees and answers with whatever the test wants next.
type recordingNotifier struct {
	name     string
	mu       sync.Mutex
	events   []Event
	respond  func(n int) error // n is the 1-based call count
	callSeen atomic.Int64
}

func (r *recordingNotifier) Name() string { return r.name }

func (r *recordingNotifier) Notify(_ context.Context, ev Event) error {
	n := int(r.callSeen.Add(1))
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	if r.respond == nil {
		return nil
	}
	return r.respond(n)
}

func (r *recordingNotifier) received() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// --- delivery ---------------------------------------------------------------

func TestDispatcherDeliversDueEvent(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	eventID, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "ops"}
	d := NewDispatcher(store, []Notifier{target}, testConfig())
	d.pollOnce(ctx)

	got := target.received()
	if len(got) != 1 {
		t.Fatalf("expected exactly one delivery, got %d", len(got))
	}
	ev := got[0]
	if ev.Schema != SchemaVersion || ev.EventID != eventID || ev.ManifestID != manifestID {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Server != "calendar" || ev.Hash != "abc123" {
		t.Fatalf("event was not composed from the manifest row: %+v", ev)
	}
	if len(ev.Changes) != 1 || ev.Changes[0] != "Added tool: upload_attachment" {
		t.Fatalf("changes must come from diff.Summarize of the stored diff, got %v", ev.Changes)
	}
	if ev.DashboardURL != "http://localhost:8081/manifests/"+strconv.FormatInt(manifestID, 10) {
		t.Fatalf("expected a deep link to the manifest, got %q", ev.DashboardURL)
	}

	due, err := store.DueNotifications(ctx, time.Now().UTC(), DefaultMaxAttempts, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a delivered event must not remain due, got %+v", due)
	}
}

// TestDispatcherSkipsUnsubscribedEventTypes keeps the workflow ignorant of
// notification config: the gate enqueues every event, the dispatcher decides
// what an operator asked to hear about.
func TestDispatcherSkipsUnsubscribedEventTypes(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestApproved, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "ops"}
	d := NewDispatcher(store, []Notifier{target}, testConfig()) // subscribes to pending only
	d.pollOnce(ctx)

	if got := target.received(); len(got) != 0 {
		t.Fatalf("expected no delivery for an unsubscribed event type, got %+v", got)
	}
	due, _ := store.DueNotifications(ctx, time.Now().UTC(), DefaultMaxAttempts, 10)
	if len(due) != 0 {
		t.Fatalf("an unsubscribed event must be retired, not retried forever, got %+v", due)
	}
}

// --- retry ------------------------------------------------------------------

func TestDispatcherRetriesWithGrowingBackoff(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "flaky", respond: func(n int) error {
		if n < 3 {
			return errors.New("notify: webhook \"flaky\": received HTTP 500")
		}
		return nil
	}}

	// Anchored to real time, not a literal: rows are enqueued with
	// time.Now(), so a fake clock in the past would find nothing due.
	start := time.Now().UTC().Add(time.Second)
	clock := start
	d := NewDispatcher(store, []Notifier{target}, testConfig())
	d.now = func() time.Time { return clock }

	// Attempt 1 fails: rescheduled a minute out, not immediately retryable.
	d.pollOnce(ctx)
	assertScheduledAt(t, store, clock, start.Add(time.Minute), 1)

	// Still not due 30 seconds later.
	clock = start.Add(30 * time.Second)
	d.pollOnce(ctx)
	if got := len(target.received()); got != 1 {
		t.Fatalf("backoff was not honoured: %d attempts by 30s", got)
	}

	// Attempt 2 fails: the interval grows.
	clock = start.Add(time.Minute)
	d.pollOnce(ctx)
	assertScheduledAt(t, store, clock, clock.Add(5*time.Minute), 2)

	// Attempt 3 succeeds.
	clock = start.Add(10 * time.Minute)
	d.pollOnce(ctx)
	if got := len(target.received()); got != 3 {
		t.Fatalf("expected 3 attempts total, got %d", got)
	}
	remaining, _ := store.DueNotifications(ctx, clock.Add(24*time.Hour), DefaultMaxAttempts, 10)
	if len(remaining) != 0 {
		t.Fatalf("expected the event to be delivered, still queued: %+v", remaining)
	}
}

func assertScheduledAt(t *testing.T, store database.Store, now, want time.Time, wantAttempts int) {
	t.Helper()
	rows, err := store.DueNotifications(context.Background(), now.Add(365*24*time.Hour), DefaultMaxAttempts, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one queued row, got %d", len(rows))
	}
	if rows[0].Attempts != wantAttempts {
		t.Fatalf("expected %d attempts recorded, got %d", wantAttempts, rows[0].Attempts)
	}
	if !rows[0].NextAttemptAt.Equal(want) {
		t.Fatalf("expected next attempt at %v, got %v", want, rows[0].NextAttemptAt)
	}
	if rows[0].LastError == "" {
		t.Fatal("a failed attempt must record why, or the failed view tells an operator nothing")
	}
}

func TestBackoffGrowsThenPlateausAtTheCap(t *testing.T) {
	want := []time.Duration{
		time.Minute, 5 * time.Minute, 25 * time.Minute,
		2 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	}
	for attempts, expected := range want {
		if got := backoffFor(attempts); got != expected {
			t.Fatalf("backoffFor(%d) = %v, want %v", attempts, got, expected)
		}
	}
	if got := backoffFor(99); got != 24*time.Hour {
		t.Fatalf("backoff must plateau at the cap, got %v", got)
	}
}

func TestDispatcherGivesUpAfterMaxAttemptsAndLeavesTheEventVisible(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "dead", respond: func(int) error {
		return errors.New("notify: webhook \"dead\": post failed")
	}}
	cfg := testConfig()
	cfg.MaxAttempts = 2

	clock := time.Now().UTC().Add(time.Second)
	d := NewDispatcher(store, []Notifier{target}, cfg)
	d.now = func() time.Time { return clock }

	for range 5 {
		d.pollOnce(ctx)
		clock = clock.Add(48 * time.Hour) // always past the next attempt time
	}

	if got := len(target.received()); got != 2 {
		t.Fatalf("expected the dispatcher to stop after MaxAttempts, got %d attempts", got)
	}

	failed, err := store.ListUndeliveredNotifications(ctx, cfg.MaxAttempts)
	if err != nil {
		t.Fatalf("list undelivered: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("a permanently failed event must stay visible, got %+v", failed)
	}
	if failed[0].LastError == "" {
		t.Fatal("the failed view must say why it failed")
	}
}

// TestExhaustedEventsDoNotStarveNewOnes is the reason DueNotifications takes
// a maxAttempts bound rather than being filtered in the dispatcher. Rows are
// delivered oldest-first, so a target that was down for a week would fill
// every batch with events it has already given up on and a fresh "your tool
// just vanished" event would never be reached.
func TestExhaustedEventsDoNotStarveNewOnes(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)

	cfg := testConfig()
	cfg.MaxAttempts = 1

	deadline := time.Now().UTC().Add(time.Second)
	for range dispatchBatchSize {
		id, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID)
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if err := store.MarkNotificationFailed(ctx, id, deadline, "target was down"); err != nil {
			t.Fatalf("mark failed: %v", err)
		}
	}
	fresh, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "recovered"}
	d := NewDispatcher(store, []Notifier{target}, cfg)
	d.now = func() time.Time { return deadline.Add(time.Hour) }
	d.pollOnce(ctx)

	got := target.received()
	if len(got) != 1 || got[0].EventID != fresh {
		t.Fatalf("expected the one deliverable event to get through, got %+v", got)
	}
}

// --- isolation --------------------------------------------------------------

// TestNotifierPanicIsContained: a panicking target must not take the
// dispatcher goroutine — and therefore the process — down with it, and must
// not prevent the other targets from being tried.
func TestNotifierPanicIsContained(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	panicking := &recordingNotifier{name: "broken", respond: func(int) error {
		panic("notifier exploded")
	}}
	healthy := &recordingNotifier{name: "ops"}

	d := NewDispatcher(store, []Notifier{panicking, healthy}, testConfig())
	d.pollOnce(ctx) // must return normally

	if len(healthy.received()) != 1 {
		t.Fatal("a panicking target must not stop the others from being tried")
	}
	// The event failed overall (one target never got it) so it is retried,
	// not silently marked delivered.
	rows, _ := store.DueNotifications(ctx, time.Now().Add(48*time.Hour).UTC(), DefaultMaxAttempts, 10)
	if len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("expected the event to be retried after a panic, got %+v", rows)
	}
}

// TestDispatcherSurvivesAStoreThatFailsToMarkDelivered is the at-least-once
// property: if the process dies (or the write fails) between a successful
// POST and the mark, the event is redelivered. The receiver deduplicates on
// event_id, which is why that id must be identical across the redelivery.
func TestDispatcherRedeliversWhenTheDeliveredMarkIsLost(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	eventID, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	crashing := &crashBeforeMarkStore{Store: store, failMark: true}
	target := &recordingNotifier{name: "ops"}

	d := NewDispatcher(crashing, []Notifier{target}, testConfig())
	d.pollOnce(ctx) // POST succeeds, the mark is lost

	if len(target.received()) != 1 {
		t.Fatalf("expected one delivery before the lost mark, got %d", len(target.received()))
	}

	// Restart: the outbox still holds the row, so it replays.
	crashing.failMark = false
	d2 := NewDispatcher(crashing, []Notifier{target}, testConfig())
	d2.pollOnce(ctx)

	got := target.received()
	if len(got) != 2 {
		t.Fatalf("expected a redelivery after the lost mark, got %d deliveries", len(got))
	}
	if got[0].EventID != eventID || got[1].EventID != eventID {
		t.Fatalf("redelivery must reuse the same event_id so receivers can dedupe: %d then %d",
			got[0].EventID, got[1].EventID)
	}
}

// crashBeforeMarkStore simulates a process that dies after the webhook POST
// but before the outbox row is marked delivered.
type crashBeforeMarkStore struct {
	database.Store
	failMark bool
}

func (s *crashBeforeMarkStore) MarkNotificationDelivered(ctx context.Context, id int64, now time.Time) error {
	if s.failMark {
		return errors.New("simulated crash before mark")
	}
	return s.Store.MarkNotificationDelivered(ctx, id, now)
}

// TestDispatcherToleratesAnEventItCannotCompose: a manifest whose stored
// diff cannot be decoded yields a recorded failure, not a panic and not a
// silent drop. (The obvious version of this — an outbox row pointing at a
// manifest that does not exist — is impossible: the foreign key rejects it
// at enqueue time.)
func TestDispatcherToleratesAnEventItCannotCompose(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	srv, err := store.CreateServer(ctx, "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	manifestID, err := store.InsertManifest(ctx, &database.ManifestRecord{
		ServerID: srv.ID, Hash: "abc123", CanonicalJSON: "{}",
		State: database.StatePending, DiffJSON: "{ this is not json",
	})
	if err != nil {
		t.Fatalf("insert manifest: %v", err)
	}
	id, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	target := &recordingNotifier{name: "ops"}
	d := NewDispatcher(store, []Notifier{target}, testConfig())
	d.pollOnce(ctx)

	if len(target.received()) != 0 {
		t.Fatal("nothing should be delivered for an event that cannot be composed")
	}
	rows, _ := store.DueNotifications(ctx, time.Now().Add(48*time.Hour).UTC(), DefaultMaxAttempts, 10)
	if len(rows) != 1 || rows[0].ID != id || rows[0].Attempts != 1 || rows[0].LastError == "" {
		t.Fatalf("expected a recorded failure, got %+v", rows)
	}
}

// --- Run --------------------------------------------------------------------

func TestRunPollsUntilItsContextIsCancelled(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	delivered := make(chan struct{})
	var once sync.Once
	target := &recordingNotifier{name: "ops", respond: func(int) error {
		once.Do(func() { close(delivered) })
		return nil
	}}

	d := NewDispatcher(store, []Notifier{target}, testConfig())
	d.interval = time.Millisecond

	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		d.Run(runCtx)
		close(stopped)
	}()

	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run never delivered the queued event")
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// TestRunWithAHungTargetStillReturnsOnCancel: the shutdown path must not
// depend on a target answering.
func TestRunWithAHungTargetStillReturnsOnCancel(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	manifestID := seedPendingManifest(t, store)
	if _, err := store.EnqueueNotification(ctx, database.EventManifestPending, manifestID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	hung := NewWebhook(WebhookConfig{Name: "hung", URL: srv.URL})
	hung.timeout = 200 * time.Millisecond

	d := NewDispatcher(store, []Notifier{hung}, testConfig())
	d.interval = time.Millisecond

	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		d.Run(runCtx)
		close(stopped)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return while a target was hung")
	}
}
