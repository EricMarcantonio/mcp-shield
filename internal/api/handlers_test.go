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
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// newTestServer's database.Store return is unused by every current caller
// (unparam flags this) but is kept as test infrastructure for Phase 4 tests
// that need direct store access (e.g. dashboard/gate-adapter tests).
func newTestServer(t *testing.T) (*Server, database.Store, *approval.Workflow, int64) { //nolint:unparam // store return kept for Phase 4 tests that need direct store access
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

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
