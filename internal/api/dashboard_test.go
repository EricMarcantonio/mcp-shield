package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// realTemplatesDir points at the actual dashboard templates shipped with the
// project, resolved relative to this package's directory, so these tests
// exercise real template rendering rather than a fake.
const realTemplatesDir = "../../web/dashboard/templates"

// newDashboardTestServer is like newTestServer (handlers_test.go) but lets
// the caller choose the templates directory, since these tests need the
// real one to exercise actual HTML rendering.
func newDashboardTestServer(t *testing.T, templatesDir string) (*Server, int64) {
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
	s := NewServer(store, wf, templatesDir)
	return s, srv.ID
}

func TestDashboardHomeRendersPending(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	seedPendingManifest(t, s, serverID)

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Pending Manifest Approvals") {
		t.Fatalf("expected pending page heading, got body: %s", body)
	}
	if !strings.Contains(body, "calendar_read") {
		t.Fatalf("expected the pending manifest's change summary in the body, got: %s", body)
	}
}

func TestDashboardManifestDetailRenders(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPendingManifest(t, s, serverID)

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/manifests/"+itoa(id), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "PENDING") {
		t.Fatalf("expected manifest state PENDING in body, got: %s", body)
	}
}

func TestDashboardServersRenders(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	seedPendingManifest(t, s, serverID)

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/servers", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "calendar") {
		t.Fatalf("expected the seeded server's name in the body, got: %s", body)
	}
	// servers.html truncates ApprovedHash to 12 chars, so only the prefix
	// of the "(none approved)" placeholder survives rendering.
	if !strings.Contains(body, "(none approv") {
		t.Fatalf("expected the no-approved-baseline placeholder, got: %s", body)
	}
}

func TestDashboardRejectRedirects(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPendingManifest(t, s, serverID)

	form := url.Values{"username": {"eric"}, "reason": {"not safe"}}
	req := httptest.NewRequest(http.MethodPost, "/manifests/"+itoa(id)+"/reject", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDashboard503WithoutTemplates(t *testing.T) {
	s, _ := newDashboardTestServer(t, filepath.Join(t.TempDir(), "no-such-templates"))

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when templates fail to load, got %d", rr.Code)
	}
}

func TestDashboardDecisionRedirects(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPendingManifest(t, s, serverID)

	form := url.Values{"username": {"eric"}, "reason": {"looks fine"}}
	req := httptest.NewRequest(http.MethodPost, "/manifests/"+itoa(id)+"/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
}

// seedPendingManifest records one PENDING manifest for serverID (a single
// tool, no prior baseline, so it's reported as an added-tool change) and
// returns its manifest ID.
func seedPendingManifest(t *testing.T, s *Server, serverID int64) int64 {
	t.Helper()
	m := manifest.Build([]mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	res, err := s.workflow.CheckAndRecord(context.Background(), serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	return res.ManifestID
}
