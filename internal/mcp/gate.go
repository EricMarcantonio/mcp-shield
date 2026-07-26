package mcp

import "context"

// GateDecision reports which tools/prompts/resources are currently safe
// to expose and call for one server. It's deliberately not a single
// allow/deny bool: a manifest that isn't fully approved still lets
// through whatever is unchanged from the last approved baseline, so a
// rejected or pending capability change only withholds the specific new
// or changed items, not the entire server.
type GateDecision struct {
	ManifestID int64
	State      string // PENDING, APPROVED, REJECTED, or SUPERSEDED
	Warn       bool

	SafeTools     map[string]bool
	SafePrompts   map[string]bool
	SafeResources map[string]bool
}

// Gate is implemented by the approval workflow. It is defined here (in
// terms of this package's own Tool/Prompt/Resource types) rather than in
// terms of a manifest.Manifest, so that internal/mcp never has to import
// internal/manifest — manifest already imports mcp for its Tool/Prompt/
// Resource types, and a reverse import would create a cycle. The adapter
// that bridges this interface to approval.Workflow lives in internal/app,
// which is free to import both.
type Gate interface {
	CheckAndRecord(ctx context.Context, serverName string, tools []Tool, prompts []Prompt, resources []Resource) (*GateDecision, error)
}

// stateRejected mirrors database.StateRejected. It is kept string-typed
// rather than imported so internal/mcp does not depend on internal/database;
// TestStateStringsMatchDatabase pins the two together.
const stateRejected = "REJECTED"

// blockedCode distinguishes "an approver said no" from "nobody has looked at
// this yet", so a client can tell a permanent refusal from a transient one.
func blockedCode(state string) int {
	if state == stateRejected {
		return CodeManifestRejected
	}
	return CodeManifestPending
}
