// Package approval implements the manifest approval state machine: the
// hard part of mcp-shield isn't hashing JSON, it's deciding "is this
// capability change acceptable?" and never letting that decision be
// bypassed or silently reversed.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
)

// FailMode controls what happens when traffic hits an unapproved manifest.
type FailMode string

const (
	// FailModeBlock refuses traffic for any non-APPROVED manifest. Default.
	FailModeBlock FailMode = "block"
	// FailModeWarn allows traffic through but flags it as unapproved. Only
	// meant for initial rollout/observation, never the production default.
	FailModeWarn FailMode = "warn"
)

var (
	ErrNotPending        = errors.New("approval: manifest is not in PENDING state")
	ErrInvalidTransition = errors.New("approval: invalid state transition")
)

type Workflow struct {
	store    database.Store
	failMode FailMode
}

func New(store database.Store, failMode FailMode) *Workflow {
	if failMode == "" {
		failMode = FailModeBlock
	}
	return &Workflow{store: store, failMode: failMode}
}

// CheckAndRecord is the gate: given a live manifest fetched from an
// upstream server, it looks up (or creates) the matching manifest row and
// reports whether traffic should be allowed.
//
//   - Hash matches an APPROVED row              -> allowed.
//   - Hash matches any other state (PENDING/REJECTED/SUPERSEDED) -> not
//     allowed (unless failMode is warn), no new row created.
//   - Hash is new                                -> a PENDING row is
//     created (diffed + risk-classified against the current approved
//     baseline, if any) and traffic is not allowed (unless failMode is warn).
func (w *Workflow) CheckAndRecord(ctx context.Context, serverID int64, m *manifest.Manifest) (allowed, warn bool, manifestID int64, state string, err error) {
	canonical, err := manifest.Canonicalize(m)
	if err != nil {
		return false, false, 0, "", fmt.Errorf("approval: canonicalize: %w", err)
	}
	hash := manifest.Hash(canonical)

	existing, err := w.store.GetManifestByHash(ctx, serverID, hash)
	if err != nil {
		return false, false, 0, "", fmt.Errorf("approval: lookup manifest: %w", err)
	}
	if existing != nil {
		allowed := existing.State == database.StateApproved
		if !allowed && w.failMode == FailModeWarn {
			return true, true, existing.ID, existing.State, nil
		}
		return allowed, false, existing.ID, existing.State, nil
	}

	baseline, err := w.store.GetApprovedManifest(ctx, serverID)
	if err != nil {
		return false, false, 0, "", fmt.Errorf("approval: lookup baseline: %w", err)
	}
	var baselineManifest *manifest.Manifest
	if baseline != nil {
		bm, err := manifestFromRecord(baseline)
		if err != nil {
			return false, false, 0, "", fmt.Errorf("approval: decode baseline: %w", err)
		}
		baselineManifest = bm
	}

	d := diff.Compare(baselineManifest, m)
	risk, _ := diff.ClassifyRisk(d)
	diffBytes, err := json.Marshal(d)
	if err != nil {
		return false, false, 0, "", fmt.Errorf("approval: marshal diff: %w", err)
	}
	diffJSON := string(diffBytes)

	rec := &database.ManifestRecord{
		ServerID:      serverID,
		Hash:          hash,
		CanonicalJSON: string(canonical),
		State:         database.StatePending,
		RiskLevel:     risk,
		DiffJSON:      diffJSON,
	}
	id, err := w.store.InsertManifest(ctx, rec)
	if err != nil {
		return false, false, 0, "", fmt.Errorf("approval: insert manifest: %w", err)
	}

	if w.failMode == FailModeWarn {
		return true, true, id, database.StatePending, nil
	}
	return false, false, id, database.StatePending, nil
}

// Approve marks a PENDING manifest APPROVED, supersedes the server's
// previous APPROVED manifest (if any), and records an immutable audit
// entry — all inside one transaction.
func (w *Workflow) Approve(ctx context.Context, manifestID int64, username, reason string) error {
	return w.store.WithTx(ctx, func(tx database.Store) error {
		rec, err := tx.GetManifestByID(ctx, manifestID)
		if err != nil {
			return err
		}
		if rec.State != database.StatePending {
			return fmt.Errorf("%w: manifest %d is %s", ErrNotPending, manifestID, rec.State)
		}

		if prior, err := tx.GetApprovedManifest(ctx, rec.ServerID); err != nil {
			return err
		} else if prior != nil {
			if err := tx.UpdateManifestState(ctx, prior.ID, database.StateSuperseded); err != nil {
				return err
			}
		}

		if err := tx.UpdateManifestState(ctx, manifestID, database.StateApproved); err != nil {
			return err
		}
		_, err = tx.InsertApproval(ctx, &database.Approval{
			ManifestID: manifestID,
			Decision:   database.DecisionApproved,
			Username:   username,
			Reason:     reason,
		})
		return err
	})
}

// Reject marks a PENDING manifest REJECTED and records an immutable audit
// entry. Traffic for that manifest hash remains blocked; the row is never
// deleted or reused.
func (w *Workflow) Reject(ctx context.Context, manifestID int64, username, reason string) error {
	return w.store.WithTx(ctx, func(tx database.Store) error {
		rec, err := tx.GetManifestByID(ctx, manifestID)
		if err != nil {
			return err
		}
		if rec.State != database.StatePending {
			return fmt.Errorf("%w: manifest %d is %s", ErrNotPending, manifestID, rec.State)
		}

		if err := tx.UpdateManifestState(ctx, manifestID, database.StateRejected); err != nil {
			return err
		}
		_, err = tx.InsertApproval(ctx, &database.Approval{
			ManifestID: manifestID,
			Decision:   database.DecisionRejected,
			Username:   username,
			Reason:     reason,
		})
		return err
	})
}

func manifestFromRecord(rec *database.ManifestRecord) (*manifest.Manifest, error) {
	return manifest.FromCanonicalJSON([]byte(rec.CanonicalJSON))
}
