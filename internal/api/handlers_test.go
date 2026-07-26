package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// newTestServer's database.Store return is unused by every current caller
// (unparam flags this) but is kept as test infrastructure for Phase 4 tests
// that need direct store access (e.g. dashboard/gate-adapter tests).
func newTestServer(t *testing.T) (*Server, database.Store, *approval.Workflow, int64) {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv, err := store.CreateServer(context.Background(), "calendar", "")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	wf := approval.New(store, approval.FailModeBlock)
	// nonexistent templates dir: tests exercise the JSON API only, and
	// NewServer degrades the HTML dashboard gracefully in that case.
	s := NewServer(store, wf, filepath.Join(t.TempDir(), "no-such-templates"))
	return s, store, wf, srv.ID
}

func TestHealthz(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestListServersEndpoint(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/servers", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var servers []database.Server
	if err := json.Unmarshal(rr.Body.Bytes(), &servers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "calendar" {
		t.Fatalf("expected the one seeded server, got %+v", servers)
	}
}

func TestGetManifestEndpoint(t *testing.T) {
	s, _, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/"+itoa(res.ManifestID), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var view ManifestView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.ID != res.ManifestID || view.Server != "calendar" || view.State != database.StatePending {
		t.Fatalf("unexpected manifest view: %+v", view)
	}
}

func TestParseIDRejectsNonNumericPathValue(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/not-a-number", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-numeric manifest id, got %d", rr.Code)
	}
}

func TestListPendingEmpty(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/pending", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var rows []PendingManifestView
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty pending list, got %v", rows)
	}
}

func TestApproveAndRejectFlow(t *testing.T) {
	s, _, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	id := res.ManifestID

	// pending should now list it
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/pending", nil))
	var rows []PendingManifestView
	_ = json.Unmarshal(rr.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("expected pending list to contain manifest %d, got %v", id, rows)
	}

	// approve
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(id)+"/approve", strings.NewReader(`{"username":"eric","reason":"ok"}`))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 approving, got %d: %s", rr.Code, rr.Body.String())
	}

	// pending list now empty
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/pending", nil))
	rows = nil
	_ = json.Unmarshal(rr.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Fatalf("expected empty pending list after approval, got %v", rows)
	}

	// re-approving the same (now APPROVED) manifest should 409
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(id)+"/approve", strings.NewReader(`{}`))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 re-approving an approved manifest, got %d", rr.Code)
	}
}

func TestGetUnknownManifest404(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/9999", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestRejectNonPendingConflicts(t *testing.T) {
	s, _, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	id := res.ManifestID

	if err := wf.Reject(ctx, id, "eric", "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(id)+"/reject", strings.NewReader(`{}`))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 re-rejecting an already-rejected manifest, got %d", rr.Code)
	}
}

// TestApproveMalformedJSONBodyStillSucceedsWithFabricatedIdentity pins a
// known bug (design doc finding C4, internal/api/handlers.go:163-169):
// handleDecision ignores the JSON decoder's error entirely
// (`_ = json.NewDecoder(r.Body).Decode(&body)`), so a malformed request body
// doesn't fail the request — it just leaves body.Username empty, which
// handleDecision then silently fills in as "unknown". For a tool whose
// product is the audit trail, a garbled approval request should be a 400,
// not a 200 recorded under a fabricated identity. Not fixed here — Phase 5
// owns it. This test documents today's actual behavior so the fix in
// Phase 5 has something concrete to change.
func TestApproveMalformedJSONBodyStillSucceedsWithFabricatedIdentity(t *testing.T) {
	s, store, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(res.ManifestID)+"/approve", strings.NewReader(`{not json`))
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("known-bug pin broke: expected today's behavior (200 despite malformed body), got %d: %s", rr.Code, rr.Body.String())
	}

	history, err := store.ListApprovalsForManifest(ctx, res.ManifestID)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	if len(history) != 1 || history[0].Username != "unknown" {
		t.Fatalf("known-bug pin broke: expected a decision fabricated under username \"unknown\", got %+v", history)
	}
}

func TestGetManifestDiffEndpoint(t *testing.T) {
	s, store, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/manifests/"+itoa(res.ManifestID)+"/diff", nil)
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var d diff.Diff
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if len(d.AddedTools) != 1 || d.AddedTools[0] != "calendar_read" {
		t.Fatalf("expected round-tripped diff to show calendar_read added, got %+v", d)
	}

	// A manifest inserted with no DiffJSON (e.g. one predating this field,
	// or inserted outside the workflow) must report the diff as a literal
	// JSON null, not an empty string or a decode error.
	emptyDiffID, err := store.InsertManifest(ctx, &database.ManifestRecord{
		ServerID: serverID, Hash: "deadbeef", CanonicalJSON: "{}", State: database.StatePending,
	})
	if err != nil {
		t.Fatalf("insert manifest with no diff: %v", err)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/manifests/"+itoa(emptyDiffID)+"/diff", nil)
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "null" {
		t.Fatalf("expected literal null for a manifest with no diff, got %q", got)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
