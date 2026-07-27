// Package notify delivers gate events to operators.
//
// The gate fails closed: a new or changed capability is withheld until a
// human approves it. Without a signal, the observed symptom is "my tool
// vanished" and nobody knows to look. Notification is therefore
// availability-critical — and must never become integrity-critical.
//
// The shape of the package follows from that second half. Events are queued
// by an INSERT inside the transaction that records the manifest (see
// database.Store.EnqueueNotification); everything network-facing happens
// later, in a Dispatcher goroutine with its own context. No exported
// function here is called from a request path, so no target — slow, broken,
// or hostile — can block, delay, or crash a gate decision.
package notify

import (
	"context"
	"time"
)

// SchemaVersion is the version of the Event payload. Receivers should
// reject payloads whose schema they do not recognise rather than guessing.
const SchemaVersion = 1

// Event is the delivered payload. It is composed from the manifest at
// delivery time rather than stored, so changing this struct never requires
// migrating queued outbox rows.
//
// There is deliberately no risk field: risk classification was removed from
// this project, and a field here would reintroduce it as a public contract.
type Event struct {
	Schema       int       `json:"schema"`
	Event        string    `json:"event"`
	EventID      int64     `json:"event_id"` // outbox row id: the receiver's idempotency key
	Server       string    `json:"server"`
	ManifestID   int64     `json:"manifest_id"`
	Hash         string    `json:"hash"`
	Changes      []string  `json:"changes"` // diff.Summarize lines
	DashboardURL string    `json:"dashboard_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Notifier is one delivery target. Implementations must be safe to call
// concurrently and must return rather than block indefinitely; the
// dispatcher bounds them with a context, but a target that ignores it will
// hold a dispatcher slot until its own timeout fires.
//
// Name identifies the target in logs and errors. It must never be derived
// from the target's URL: a Slack or Discord webhook URL is itself a
// capability-bearing credential.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, ev Event) error
}
