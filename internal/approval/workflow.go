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

// CheckResult reports the gate's decision for one manifest snapshot. A
// manifest that is not fully APPROVED is no longer all-or-nothing: tools
// (and prompts/resources) that are byte-identical to the current approved
// baseline stay in Safe* and keep working, while only the new or changed
// ones are withheld pending a decision. A server with no approved baseline
// at all (first-ever connect) has empty Safe* sets, which is the fail
// closed starting state.
type CheckResult struct {
	ManifestID int64
	State      string
	Warn       bool // failMode is warn: everything is allowed, this is advisory only

	SafeTools     map[string]bool
	SafePrompts   map[string]bool
	SafeResources map[string]bool
}

// CheckAndRecord is the gate: given a live manifest fetched from an
// upstream server, it looks up (or creates) the matching manifest row and
// computes which tools/prompts/resources are safe to expose right now.
//
//   - Hash matches the current APPROVED baseline exactly -> everything safe.
//   - Otherwise (hash is PENDING/REJECTED/SUPERSEDED, or new)         ->
//     only the subset unchanged from the current approved baseline is
//     safe; new or changed items are withheld. A brand new manifest row
//     is inserted (diffed against the baseline) the first time a given
//     hash is seen; a hash seen before just reuses its existing row/state.
//   - failMode warn overrides withholding: everything is marked safe,
//     Warn is set, but the manifest's real state is still recorded as-is.
func (w *Workflow) CheckAndRecord(ctx context.Context, serverID int64, m *manifest.Manifest) (*CheckResult, error) {
	canonical, err := manifest.Canonicalize(m)
	if err != nil {
		return nil, fmt.Errorf("approval: canonicalize: %w", err)
	}
	hash := manifest.Hash(canonical)

	baseline, err := w.approvedBaseline(ctx, serverID)
	if err != nil {
		return nil, err
	}

	// Fast path: this exact snapshot is the approved baseline itself, so
	// there is nothing to diff — everything is safe.
	if baseline != nil && baseline.Hash == hash {
		return w.allowAll(baseline.ID, database.StateApproved, false, m), nil
	}

	var baselineManifest *manifest.Manifest
	if baseline != nil {
		if baselineManifest, err = manifestFromRecord(baseline); err != nil {
			return nil, fmt.Errorf("approval: decode baseline: %w", err)
		}
	}
	d := diff.Compare(baselineManifest, m)

	manifestID, state, err := w.findOrInsertManifest(ctx, serverID, hash, canonical, d)
	if err != nil {
		return nil, err
	}

	// warn mode observes without withholding: every capability is reported
	// safe, but the manifest's real state is still recorded as-is.
	if w.failMode == FailModeWarn {
		return w.allowAll(manifestID, state, true, m), nil
	}

	safeTools, safePrompts, safeResources := unchangedSets(m, d)
	return &CheckResult{
		ManifestID: manifestID, State: state,
		SafeTools: safeTools, SafePrompts: safePrompts, SafeResources: safeResources,
	}, nil
}

// approvedBaseline returns the server's approved manifest, or nil when it has
// none yet. A first-ever connect is an ordinary state, not a failure —
// everything in the manifest is then reported as added.
func (w *Workflow) approvedBaseline(ctx context.Context, serverID int64) (*database.ManifestRecord, error) {
	baseline, err := w.store.GetApprovedManifest(ctx, serverID)
	if errors.Is(err, database.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approval: lookup baseline: %w", err)
	}
	return baseline, nil
}

// findOrInsertManifest returns the id and state for this hash, inserting a
// new PENDING row (diffed against the baseline) the first time the hash is
// seen. A hash seen before reuses its existing row, so an operator's earlier
// decision on it is never quietly reset by a reconnect.
func (w *Workflow) findOrInsertManifest(ctx context.Context, serverID int64, hash string, canonical []byte, d *diff.Diff) (int64, string, error) {
	existing, err := w.store.GetManifestByHash(ctx, serverID, hash)
	switch {
	case err == nil:
		return existing.ID, existing.State, nil
	case !errors.Is(err, database.ErrNotFound):
		return 0, "", fmt.Errorf("approval: lookup manifest: %w", err)
	}

	diffBytes, err := json.Marshal(d)
	if err != nil {
		return 0, "", fmt.Errorf("approval: marshal diff: %w", err)
	}
	id, err := w.store.InsertManifest(ctx, &database.ManifestRecord{
		ServerID:      serverID,
		Hash:          hash,
		CanonicalJSON: string(canonical),
		State:         database.StatePending,
		DiffJSON:      string(diffBytes),
	})
	if err != nil {
		return 0, "", fmt.Errorf("approval: insert manifest: %w", err)
	}
	return id, database.StatePending, nil
}

// allowAll builds a result marking every advertised capability safe. Used by
// the approved-baseline fast path and by warn mode — the two cases where
// nothing is withheld.
func (w *Workflow) allowAll(manifestID int64, state string, warn bool, m *manifest.Manifest) *CheckResult {
	return &CheckResult{
		ManifestID:    manifestID,
		State:         state,
		Warn:          warn,
		SafeTools:     allToolNames(m),
		SafePrompts:   allPromptNames(m),
		SafeResources: allResourceURIs(m),
	}
}

func allToolNames(m *manifest.Manifest) map[string]bool {
	out := make(map[string]bool, len(m.Tools))
	for _, t := range m.Tools {
		out[t.Name] = true
	}
	return out
}

func allPromptNames(m *manifest.Manifest) map[string]bool {
	out := make(map[string]bool, len(m.Prompts))
	for _, p := range m.Prompts {
		out[p.Name] = true
	}
	return out
}

func allResourceURIs(m *manifest.Manifest) map[string]bool {
	out := make(map[string]bool, len(m.Resources))
	for _, r := range m.Resources {
		out[r.URI] = true
	}
	return out
}

// unchangedSets returns the tools/prompts/resources present in m that the
// diff did not mark as added or changed relative to the baseline — i.e.
// the subset that's identical to what's already approved and therefore
// safe to keep exposing even while the rest of the manifest is pending or
// rejected.
func unchangedSets(m *manifest.Manifest, d *diff.Diff) (tools, prompts, resources map[string]bool) {
	touchedTools := toSet(d.AddedTools)
	for _, tc := range d.ChangedTools {
		touchedTools[tc.Name] = true
	}
	touchedPrompts := toSet(d.AddedPrompts)
	for _, pc := range d.ChangedPrompts {
		touchedPrompts[pc.Name] = true
	}
	touchedResources := toSet(d.AddedResources)
	for _, rc := range d.ChangedResources {
		touchedResources[rc.URI] = true
	}

	tools = make(map[string]bool)
	for _, t := range m.Tools {
		if !touchedTools[t.Name] {
			tools[t.Name] = true
		}
	}
	prompts = make(map[string]bool)
	for _, p := range m.Prompts {
		if !touchedPrompts[p.Name] {
			prompts[p.Name] = true
		}
	}
	resources = make(map[string]bool)
	for _, r := range m.Resources {
		if !touchedResources[r.URI] {
			resources[r.URI] = true
		}
	}
	return tools, prompts, resources
}

func toSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
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

		// Approving the very first manifest for a server supersedes nothing.
		prior, err := tx.GetApprovedManifest(ctx, rec.ServerID)
		if err != nil && !errors.Is(err, database.ErrNotFound) {
			return err
		}
		if err == nil {
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
