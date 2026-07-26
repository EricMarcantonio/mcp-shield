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

// mustBuild builds a tool-only manifest, failing the test if the capability
// set is inadmissible. Admissibility is manifest.Build's concern and is
// tested there; these tests are about the HTTP surface.
func mustBuild(t *testing.T, tools []mcp.Tool) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Build(tools, nil, nil)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return m
}

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

	m := mustBuild(t, []mcp.Tool{{Name: "calendar_read"}})
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

	m := mustBuild(t, []mcp.Tool{{Name: "calendar_read"}})
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

	// re-approving the same (now APPROVED) manifest should 409. The body
	// carries a username because an unattributable decision is refused with
	// a 400 before the manifest's state is ever consulted.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(id)+"/approve", strings.NewReader(`{"username":"eric"}`))
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

	m := mustBuild(t, []mcp.Tool{{Name: "calendar_read"}})
	res, err := wf.CheckAndRecord(ctx, serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	id := res.ManifestID

	if err := wf.Reject(ctx, id, "eric", "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(id)+"/reject", strings.NewReader(`{"username":"eric"}`))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 re-rejecting an already-rejected manifest, got %d", rr.Code)
	}
}

// TestDecisionBodiesThatMustNotBeRecorded guards the fix for design doc
// finding C4. handleDecision used to ignore the JSON decoder's error
// entirely and then fill an empty username in as "unknown", so a garbled
// approval request returned 200 and wrote an audit row under an identity
// nobody supplied. The approvals table is the record of who authorized a
// capability change; a decision the gateway cannot attribute must be
// refused, not attributed to an invented user.
func TestDecisionBodiesThatMustNotBeRecorded(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{not json`},
		{"truncated json", `{"username": 42`},
		{"wrong type for username", `{"username": 42}`},
		{"absent username", `{}`},
		{"empty body", ``},
		{"blank username", `{"username":"   "}`},
	}
	for _, tc := range cases {
		for _, decision := range []string{"approve", "reject"} {
			t.Run(tc.name+"/"+decision, func(t *testing.T) {
				s, store, wf, serverID := newTestServer(t)
				ctx := context.Background()

				res, err := wf.CheckAndRecord(ctx, serverID, mustBuild(t, []mcp.Tool{{Name: "calendar_read"}}))
				if err != nil {
					t.Fatalf("check and record: %v", err)
				}

				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost,
					"/api/manifests/"+itoa(res.ManifestID)+"/"+decision, strings.NewReader(tc.body))
				s.ServeHTTP(rr, req)

				if rr.Code != http.StatusBadRequest {
					t.Fatalf("expected 400 for an unattributable decision, got %d: %s", rr.Code, rr.Body.String())
				}
				history, err := store.ListApprovalsForManifest(ctx, res.ManifestID)
				if err != nil {
					t.Fatalf("list approvals: %v", err)
				}
				if len(history) != 0 {
					t.Fatalf("a refused decision must leave no audit row, got %+v", history)
				}
			})
		}
	}
}

func TestApproveRecordsSuppliedUsernameVerbatim(t *testing.T) {
	s, store, wf, serverID := newTestServer(t)
	ctx := context.Background()

	res, err := wf.CheckAndRecord(ctx, serverID, mustBuild(t, []mcp.Tool{{Name: "calendar_read"}}))
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/manifests/"+itoa(res.ManifestID)+"/approve",
		strings.NewReader(`{"username":"alice","reason":"reviewed the diff"}`))
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	history, err := store.ListApprovalsForManifest(ctx, res.ManifestID)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	if len(history) != 1 || history[0].Username != "alice" || history[0].Reason != "reviewed the diff" {
		t.Fatalf("expected the supplied identity recorded verbatim, got %+v", history)
	}
}

func TestGetManifestDiffEndpoint(t *testing.T) {
	s, store, wf, serverID := newTestServer(t)
	ctx := context.Background()

	m := mustBuild(t, []mcp.Tool{{Name: "calendar_read"}})
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
