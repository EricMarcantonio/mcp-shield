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
