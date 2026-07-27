// Package database provides SQLite-backed storage for servers, manifests,
// and approval records. Manifest content is append-only: the only mutation
// ever performed on an existing manifest row is a state transition — see
// Store.UpdateManifestState.
package database

import "time"

// Manifest lifecycle states.
const (
	StatePending    = "PENDING"
	StateApproved   = "APPROVED"
	StateRejected   = "REJECTED"
	StateSuperseded = "SUPERSEDED"
)

// Approval decisions.
const (
	DecisionApproved = "APPROVED"
	DecisionRejected = "REJECTED"
)

// Notification event types written to the outbox. These strings are part of
// the webhook payload's public contract (the "event" field), so they are
// defined once here rather than spelled out at each call site.
const (
	EventManifestPending  = "manifest.pending"
	EventManifestApproved = "manifest.approved"
	EventManifestRejected = "manifest.rejected"
)

type Server struct {
	ID        int64
	Name      string
	Endpoint  string
	CreatedAt time.Time
}

type ManifestRecord struct {
	ID            int64
	ServerID      int64
	Hash          string
	CanonicalJSON string
	State         string
	DiffJSON      string // "" if no prior baseline existed
	CreatedAt     time.Time
}

type Approval struct {
	ID         int64
	ManifestID int64
	Decision   string
	Username   string
	Reason     string
	CreatedAt  time.Time
}

// OutboxRow is one pending notification. It carries no payload: the event
// body is composed from the manifest at delivery time, so a change to the
// payload format never has to migrate rows already queued. ID doubles as the
// receiver's idempotency key, which is what makes at-least-once delivery
// safe to deduplicate on the far side.
type OutboxRow struct {
	ID            int64
	EventType     string
	ManifestID    int64
	Attempts      int
	NextAttemptAt time.Time
	DeliveredAt   *time.Time // nil until a 2xx has been observed
	LastError     string
	CreatedAt     time.Time
}
