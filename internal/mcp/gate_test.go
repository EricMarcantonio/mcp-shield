package mcp

import (
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// TestStateStringsMatchDatabase guards the one string internal/mcp compares
// manifest states against. The mcp package deliberately does not import
// internal/database — the state arrives as a plain string on GateDecision —
// so nothing but this test stops the two definitions from drifting apart. If
// they did, blockedCode would silently report every rejected manifest as
// merely pending, telling a client to retry something an approver refused.
//
// The test file may import internal/database freely: database does not
// import mcp, so there is no cycle.
func TestStateStringsMatchDatabase(t *testing.T) {
	if stateRejected != database.StateRejected {
		t.Fatalf("mcp.stateRejected = %q but database.StateRejected = %q", stateRejected, database.StateRejected)
	}
}

func TestBlockedCodeDistinguishesRejectedFromPending(t *testing.T) {
	cases := []struct {
		state string
		want  int
	}{
		{database.StateRejected, CodeManifestRejected},
		{database.StatePending, CodeManifestPending},
		{database.StateSuperseded, CodeManifestPending},
		{"", CodeManifestPending},
	}
	for _, tc := range cases {
		if got := blockedCode(tc.state); got != tc.want {
			t.Errorf("blockedCode(%q) = %d, want %d", tc.state, got, tc.want)
		}
	}
}
