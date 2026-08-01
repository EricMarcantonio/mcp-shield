package database

import (
	"context"
	"testing"
)

// seedHistory builds two servers with a spread of manifest states and
// decisions, so the filter tests all read against the same known fixture.
//
// calendar: h1 SUPERSEDED (approved then superseded), h2 APPROVED, h3 PENDING
// files:    f1 REJECTED
func seedHistory(t *testing.T, store Store) (calendarID, filesID int64) {
	t.Helper()
	ctx := context.Background()

	calendar, err := store.CreateServer(ctx, "calendar", "")
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	files, err := store.CreateServer(ctx, "files", "")
	if err != nil {
		t.Fatalf("create files: %v", err)
	}

	insert := func(serverID int64, hash, state string) int64 {
		t.Helper()
		id, err := store.InsertManifest(ctx, &ManifestRecord{
			ServerID: serverID, Hash: hash, CanonicalJSON: "{}", State: state,
		})
		if err != nil {
			t.Fatalf("insert manifest %s: %v", hash, err)
		}
		return id
	}
	decide := func(manifestID int64, decision, username, reason string) {
		t.Helper()
		if _, err := store.InsertApproval(ctx, &Approval{
			ManifestID: manifestID, Decision: decision, Username: username, Reason: reason,
		}); err != nil {
			t.Fatalf("insert approval: %v", err)
		}
	}

	h1 := insert(calendar.ID, "h1", StateSuperseded)
	h2 := insert(calendar.ID, "h2", StateApproved)
	insert(calendar.ID, "h3", StatePending)
	f1 := insert(files.ID, "f1", StateRejected)

	decide(h1, DecisionApproved, "eric", "baseline")
	decide(h2, DecisionApproved, "sam", "reviewed the new tool")
	decide(f1, DecisionRejected, "eric", "execute_command is not acceptable")

	return calendar.ID, files.ID
}

func hashesOf(records []ManifestRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.Hash)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestListManifestsReturnsNewestFirst pins the ordering a history view
// depends on. created_at alone is not enough: rows written in the same
// instant would tie, and an unstable order silently breaks pagination by
// returning the same row on two pages.
func TestListManifestsReturnsNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	got, err := store.ListManifests(ctx, ManifestFilter{})
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	want := []string{"f1", "h3", "h2", "h1"}
	if !equalStrings(hashesOf(got), want) {
		t.Fatalf("hashes = %v, want %v (newest first)", hashesOf(got), want)
	}
}

func TestListManifestsFiltersByServer(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	_, filesID := seedHistory(t, store)

	got, err := store.ListManifests(ctx, ManifestFilter{ServerID: &filesID})
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	if !equalStrings(hashesOf(got), []string{"f1"}) {
		t.Fatalf("hashes = %v, want only the files server's manifest", hashesOf(got))
	}
}

func TestListManifestsFiltersByState(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	cases := map[string]struct {
		states []string
		want   []string
	}{
		"one state":      {[]string{StatePending}, []string{"h3"}},
		"several states": {[]string{StateApproved, StateRejected}, []string{"f1", "h2"}},
		"no match":       {[]string{"NO_SUCH_STATE"}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := store.ListManifests(ctx, ManifestFilter{States: tc.states})
			if err != nil {
				t.Fatalf("list manifests: %v", err)
			}
			if !equalStrings(hashesOf(got), tc.want) {
				t.Fatalf("hashes = %v, want %v", hashesOf(got), tc.want)
			}
		})
	}
}

// TestListManifestsFiltersByHash is what makes a hash usable as a lookup key
// even though a manifest is addressed by id: a hash is only unique per
// server, so it is a filter rather than an identifier.
func TestListManifestsFiltersByHash(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	calendarID, _ := seedHistory(t, store)

	got, err := store.ListManifests(ctx, ManifestFilter{ServerID: &calendarID, Hash: "h2"})
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	if !equalStrings(hashesOf(got), []string{"h2"}) {
		t.Fatalf("hashes = %v, want [h2]", hashesOf(got))
	}
}

func TestListManifestsPagesWithLimitAndOffset(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	first, err := store.ListManifests(ctx, ManifestFilter{Page: Page{Limit: 2}})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if !equalStrings(hashesOf(first), []string{"f1", "h3"}) {
		t.Fatalf("first page = %v, want [f1 h3]", hashesOf(first))
	}

	second, err := store.ListManifests(ctx, ManifestFilter{Page: Page{Limit: 2, Offset: 2}})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if !equalStrings(hashesOf(second), []string{"h2", "h1"}) {
		t.Fatalf("second page = %v, want [h2 h1]", hashesOf(second))
	}

	past, err := store.ListManifests(ctx, ManifestFilter{Page: Page{Limit: 2, Offset: 99}})
	if err != nil {
		t.Fatalf("page past the end: %v", err)
	}
	if len(past) != 0 {
		t.Fatalf("a page past the end must be empty, got %v", hashesOf(past))
	}
}

// TestListQueriesAreAlwaysBounded is the reason Page exists at all: an
// unbounded list endpoint is a denial-of-service waiting for a busy gateway.
func TestListQueriesAreAlwaysBounded(t *testing.T) {
	if resolveLimit(0) != DefaultPageLimit {
		t.Fatalf("an unset limit resolved to %d, want the default %d", resolveLimit(0), DefaultPageLimit)
	}
	if resolveLimit(-5) != DefaultPageLimit {
		t.Fatalf("a negative limit resolved to %d, want the default %d", resolveLimit(-5), DefaultPageLimit)
	}
	if resolveLimit(MaxPageLimit+1) != MaxPageLimit {
		t.Fatalf("an over-large limit resolved to %d, want it clamped to %d", resolveLimit(MaxPageLimit+1), MaxPageLimit)
	}
	if resolveLimit(10) != 10 {
		t.Fatalf("an in-range limit was not honoured: %d", resolveLimit(10))
	}
}

func TestListDecisionsSpansEveryServerNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	got, err := store.ListDecisions(ctx, DecisionFilter{})
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d decisions, want all 3 across both servers", len(got))
	}
	wantUsers := []string{"eric", "sam", "eric"}
	for i, want := range wantUsers {
		if got[i].Username != want {
			t.Fatalf("decision %d username = %q, want %q (newest first)", i, got[i].Username, want)
		}
	}
	if got[0].Decision != DecisionRejected {
		t.Fatalf("newest decision = %q, want the rejection", got[0].Decision)
	}
}

// TestListDecisionsCarriesItsServerAndManifest is the difference between an
// audit trail and a list of opaque ids: a cross-server feed has to say which
// server and which capability set each decision was about.
func TestListDecisionsCarriesItsServerAndManifest(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	got, err := store.ListDecisions(ctx, DecisionFilter{})
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	newest := got[0]
	if newest.ServerName != "files" {
		t.Fatalf("ServerName = %q, want files", newest.ServerName)
	}
	if newest.ManifestHash != "f1" {
		t.Fatalf("ManifestHash = %q, want f1", newest.ManifestHash)
	}
	if newest.Reason != "execute_command is not acceptable" {
		t.Fatalf("Reason = %q, want the recorded reason", newest.Reason)
	}
	if newest.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero; a decision with no timestamp is not an audit record")
	}
}

func TestListDecisionsFiltersByManifest(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	calendarID, _ := seedHistory(t, store)

	manifests, err := store.ListManifests(ctx, ManifestFilter{ServerID: &calendarID, Hash: "h2"})
	if err != nil || len(manifests) != 1 {
		t.Fatalf("locate h2: %v, %v", manifests, err)
	}
	manifestID := manifests[0].ID

	got, err := store.ListDecisions(ctx, DecisionFilter{ManifestID: &manifestID})
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(got) != 1 || got[0].Username != "sam" {
		t.Fatalf("got %+v, want only sam's decision on h2", got)
	}
}

func TestListDecisionsFiltersByServer(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	calendarID, _ := seedHistory(t, store)

	got, err := store.ListDecisions(ctx, DecisionFilter{ServerID: &calendarID})
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d decisions, want the 2 recorded against calendar", len(got))
	}
	for _, d := range got {
		if d.ServerName != "calendar" {
			t.Fatalf("decision from server %q leaked into a calendar-filtered query", d.ServerName)
		}
	}
}

func TestListDecisionsPagesWithLimitAndOffset(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	first, err := store.ListDecisions(ctx, DecisionFilter{Page: Page{Limit: 1}})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 1 || first[0].ManifestHash != "f1" {
		t.Fatalf("first page = %+v, want the newest decision only", first)
	}

	second, err := store.ListDecisions(ctx, DecisionFilter{Page: Page{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 1 || second[0].ManifestHash != "h2" {
		t.Fatalf("second page = %+v, want the second-newest decision", second)
	}
}

// TestListingsWorkInsideATransaction keeps the txStore path honest: both new
// methods are reachable through WithTx like every other read.
func TestListingsWorkInsideATransaction(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedHistory(t, store)

	err := store.WithTx(ctx, func(tx Store) error {
		if _, err := tx.ListManifests(ctx, ManifestFilter{}); err != nil {
			return err
		}
		_, err := tx.ListDecisions(ctx, DecisionFilter{})
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
}
