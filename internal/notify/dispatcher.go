package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
)

// backoffSchedule is the delay before the next attempt, indexed by the
// number of attempts already made, plateauing at the last entry. Roughly 15
// hours across the default six attempts, then daily — long enough to ride
// out a receiver outage, short enough that a withheld capability is not
// discovered a week later.
var backoffSchedule = []time.Duration{
	time.Minute,
	5 * time.Minute,
	25 * time.Minute,
	2 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// dispatchBatchSize bounds how many events one poll cycle handles, so a
// large backlog is worked through steadily instead of in one long burst.
const dispatchBatchSize = 50

// defaultPollInterval is how often the outbox is checked. Polling latency is
// irrelevant against a human approval loop, and a poll is one indexed query.
const defaultPollInterval = 2 * time.Second

func backoffFor(attempts int) time.Duration {
	if attempts >= len(backoffSchedule) {
		return backoffSchedule[len(backoffSchedule)-1]
	}
	return backoffSchedule[attempts]
}

// Dispatcher drains the notification outbox to its targets.
//
// It is the only part of this package that does network I/O, and nothing in
// a gate decision's path ever calls it. It owns its own context and
// goroutine, recovers from panicking targets, and treats every delivery
// failure as "retry later" — there is no error it can produce that reaches
// the gate.
type Dispatcher struct {
	store    database.Store
	targets  []Notifier
	events   map[string]bool
	maxTries int
	baseURL  string

	// now and interval are injection points for tests, not configuration.
	now      func() time.Time
	interval time.Duration
}

// NewDispatcher builds a dispatcher for the configured targets. cfg must be
// non-nil; a nil config means notifications are disabled, and the caller
// should not build a dispatcher at all.
func NewDispatcher(store database.Store, targets []Notifier, cfg *Config) *Dispatcher {
	subscribed := make(map[string]bool, len(cfg.Events))
	for _, e := range cfg.Events {
		subscribed[e] = true
	}
	return &Dispatcher{
		store:    store,
		targets:  targets,
		events:   subscribed,
		maxTries: cfg.MaxAttempts,
		baseURL:  strings.TrimSuffix(cfg.DashboardURL, "/"),
		now:      func() time.Time { return time.Now().UTC() },
		interval: defaultPollInterval,
	}
}

// Run polls the outbox until ctx is cancelled. Call it in a goroutine; it
// returns only on cancellation.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	slog.Info("notification dispatcher started", "targets", d.targetNames(), "max_attempts", d.maxTries)
	for {
		select {
		case <-ctx.Done():
			slog.Info("notification dispatcher stopped")
			return
		case <-ticker.C:
			d.pollOnce(ctx)
		}
	}
}

// targetNames lists the targets by configured name. Never by URL: a webhook
// URL is a credential, and this string goes to the log.
func (d *Dispatcher) targetNames() []string {
	names := make([]string, 0, len(d.targets))
	for _, t := range d.targets {
		names = append(names, t.Name())
	}
	return names
}

// pollOnce delivers one batch of due events. It never returns an error:
// every failure is either recorded against the outbox row for retry or
// logged, because there is no caller who could usefully act on one.
func (d *Dispatcher) pollOnce(ctx context.Context) {
	rows, err := d.store.DueNotifications(ctx, d.now(), d.maxTries, dispatchBatchSize)
	if err != nil {
		slog.Error("notification outbox query failed", "error", err)
		return
	}
	for _, row := range rows {
		d.deliver(ctx, row)
	}
}

// deliver handles one outbox row: compose, fan out, record the outcome.
func (d *Dispatcher) deliver(ctx context.Context, row database.OutboxRow) {
	if !d.events[row.EventType] {
		d.retire(ctx, row, "not subscribed")
		return
	}

	ev, err := d.composeEvent(ctx, row)
	if err != nil {
		d.recordFailure(ctx, row, err)
		return
	}
	if err := d.fanOut(ctx, ev); err != nil {
		d.recordFailure(ctx, row, err)
		return
	}
	d.markDelivered(ctx, row)
}

// fanOut sends one event to every target. A single failing target fails the
// whole event, so it is retried to all of them; receivers deduplicate on
// event_id, which is why that id is stable across redeliveries.
func (d *Dispatcher) fanOut(ctx context.Context, ev Event) error {
	var failures []error
	for _, target := range d.targets {
		if err := notifySafely(ctx, target, ev); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// notifySafely calls a target and converts a panic into an ordinary error.
// A contributed Notifier is third-party code running inside the gateway
// process; the availability of the gate must not depend on its quality.
func notifySafely(ctx context.Context, target Notifier, ev Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("notification target panicked", "target", target.Name(), "panic", r)
			err = fmt.Errorf("notify: target %q panicked: %v", target.Name(), r)
		}
	}()
	return target.Notify(ctx, ev)
}

// composeEvent builds the payload from the manifest at delivery time. The
// outbox row stores no payload, so a change to Event never has to migrate
// rows that are already queued.
func (d *Dispatcher) composeEvent(ctx context.Context, row database.OutboxRow) (Event, error) {
	rec, err := d.store.GetManifestByID(ctx, row.ManifestID)
	if err != nil {
		return Event{}, fmt.Errorf("notify: load manifest %d: %w", row.ManifestID, err)
	}
	srv, err := d.store.GetServerByID(ctx, rec.ServerID)
	if err != nil {
		return Event{}, fmt.Errorf("notify: load server %d: %w", rec.ServerID, err)
	}
	changes, err := summarizeStoredDiff(rec.DiffJSON)
	if err != nil {
		return Event{}, err
	}
	return Event{
		Schema:       SchemaVersion,
		Event:        row.EventType,
		EventID:      row.ID,
		Server:       srv.Name,
		ManifestID:   rec.ID,
		Hash:         rec.Hash,
		Changes:      changes,
		DashboardURL: d.manifestURL(rec.ID),
		CreatedAt:    row.CreatedAt,
	}, nil
}

// summarizeStoredDiff renders the stored diff as human-readable lines. An
// empty diff_json is the first-ever manifest for a server, which has no
// baseline to compare against — an ordinary state, not a failure.
func summarizeStoredDiff(diffJSON string) ([]string, error) {
	if diffJSON == "" {
		return []string{}, nil
	}
	var d diff.Diff
	if err := json.Unmarshal([]byte(diffJSON), &d); err != nil {
		return nil, fmt.Errorf("notify: decode stored diff: %w", err)
	}
	changes := diff.Summarize(&d)
	if changes == nil {
		return []string{}, nil
	}
	return changes, nil
}

// manifestURL builds the approver's deep link, or "" when no dashboard URL
// is configured. Guessing a hostname would produce a link the approver
// cannot open, which is worse than no link.
func (d *Dispatcher) manifestURL(manifestID int64) string {
	if d.baseURL == "" {
		return ""
	}
	return d.baseURL + "/manifests/" + strconv.FormatInt(manifestID, 10)
}

// retire marks an event the operator did not subscribe to as handled, so it
// does not sit in the outbox being reconsidered forever.
func (d *Dispatcher) retire(ctx context.Context, row database.OutboxRow, reason string) {
	if err := d.store.MarkNotificationDelivered(ctx, row.ID, d.now()); err != nil {
		slog.Error("could not retire notification", "event_id", row.ID, "reason", reason, "error", err)
	}
}

func (d *Dispatcher) markDelivered(ctx context.Context, row database.OutboxRow) {
	if err := d.store.MarkNotificationDelivered(ctx, row.ID, d.now()); err != nil {
		// The POST already succeeded. Leaving the row queued means it is
		// redelivered, which is exactly the at-least-once contract the
		// receiver deduplicates on event_id.
		slog.Error("notification delivered but could not be marked; it will be redelivered",
			"event_id", row.ID, "error", err)
	}
}

// recordFailure schedules the retry and stores why. The error text comes
// from the target, which names itself rather than its URL.
func (d *Dispatcher) recordFailure(ctx context.Context, row database.OutboxRow, cause error) {
	attempts := row.Attempts + 1
	next := d.now().Add(backoffFor(row.Attempts))

	if attempts >= d.maxTries {
		slog.Error("notification permanently failed; see GET /api/notifications/failed",
			"event_id", row.ID, "event", row.EventType, "attempts", attempts, "error", cause)
	} else {
		slog.Warn("notification delivery failed; will retry",
			"event_id", row.ID, "event", row.EventType, "attempts", attempts, "next_attempt", next, "error", cause)
	}

	if err := d.store.MarkNotificationFailed(ctx, row.ID, next, cause.Error()); err != nil {
		slog.Error("could not record notification failure", "event_id", row.ID, "error", err)
	}
}
