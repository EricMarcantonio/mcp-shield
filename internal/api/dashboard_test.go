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

// TestManifestDetailRendersChangedPromptsAndResources verifies by rendering,
// not by reading: fixing diff.Summarize alone still leaves the dashboard
// blind, because manifest_detail.html had no "Changed prompts" or "Changed
// resources" section at all. A prompt-argument change is a real injection
// vector, and an approver clicking Approve must be able to see it.
func TestManifestDetailRendersChangedPromptsAndResources(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPromptAndResourceChange(t, s, serverID)

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/manifests/"+itoa(id), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Changed prompts", "greeting_prompt",
		"Changed resources", "file:///quarterly.csv",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered manifest detail page is missing %q; an approver cannot see this change:\n%s", want, body)
		}
	}
}

// TestDashboardDecisionWithoutUsernameRejected mirrors the JSON API: the
// dashboard templates always post an explicit username, so a form arriving
// without one was hand-crafted, and substituting a default would put an
// identity nobody supplied into the audit trail.
func TestDashboardDecisionWithoutUsernameRejected(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPendingManifest(t, s, serverID)

	rr := postDashboardForm(t, s, "/manifests/"+itoa(id)+"/approve", url.Values{"reason": {"no name"}})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unattributable decision, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestDashboardRepeatDecisionIsConflictNot500 pins design doc finding M2:
// the dashboard mapped every decision error to 500, while the JSON API
// already distinguished ErrNotPending (409) and ErrNotFound (404). The same
// error must produce the same status on both surfaces.
func TestDashboardRepeatDecisionIsConflictNot500(t *testing.T) {
	s, serverID := newDashboardTestServer(t, realTemplatesDir)
	id := seedPendingManifest(t, s, serverID)

	form := url.Values{"username": {"eric"}}
	if rr := postDashboardForm(t, s, "/manifests/"+itoa(id)+"/approve", form); rr.Code != http.StatusSeeOther {
		t.Fatalf("first approval should succeed, got %d: %s", rr.Code, rr.Body.String())
	}

	rr := postDashboardForm(t, s, "/manifests/"+itoa(id)+"/approve", form)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 re-approving an already-approved manifest, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDashboardDecisionOnMissingManifestIs404(t *testing.T) {
	s, _ := newDashboardTestServer(t, realTemplatesDir)

	rr := postDashboardForm(t, s, "/manifests/9999/approve", url.Values{"username": {"eric"}})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a manifest that does not exist, got %d: %s", rr.Code, rr.Body.String())
	}
}

func postDashboardForm(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}

// seedPromptAndResourceChange approves a baseline carrying one prompt and
// one resource, then records a second manifest in which only the prompt's
// arguments and the resource's MIME type differ. It returns the PENDING
// manifest's ID, whose stored diff therefore contains ChangedPrompts and
// ChangedResources and nothing else.
func seedPromptAndResourceChange(t *testing.T, s *Server, serverID int64) int64 {
	t.Helper()
	ctx := context.Background()

	baseline := mustBuildFull(t,
		[]mcp.Prompt{{Name: "greeting_prompt", Arguments: []mcp.PromptArgument{{Name: "topic"}}}},
		[]mcp.Resource{{URI: "file:///quarterly.csv", MimeType: "text/csv"}})
	approved, err := s.workflow.CheckAndRecord(ctx, serverID, baseline)
	if err != nil {
		t.Fatalf("record baseline: %v", err)
	}
	if err := s.workflow.Approve(ctx, approved.ManifestID, "eric", "baseline"); err != nil {
		t.Fatalf("approve baseline: %v", err)
	}

	changed := mustBuildFull(t,
		[]mcp.Prompt{{Name: "greeting_prompt", Arguments: []mcp.PromptArgument{{Name: "topic"}, {Name: "system_instructions"}}}},
		[]mcp.Resource{{URI: "file:///quarterly.csv", MimeType: "application/json"}})
	res, err := s.workflow.CheckAndRecord(ctx, serverID, changed)
	if err != nil {
		t.Fatalf("record changed manifest: %v", err)
	}
	return res.ManifestID
}

func mustBuildFull(t *testing.T, prompts []mcp.Prompt, resources []mcp.Resource) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Build(nil, prompts, resources)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return m
}

// seedPendingManifest records one PENDING manifest for serverID (a single
// tool, no prior baseline, so it's reported as an added-tool change) and
// returns its manifest ID.
func seedPendingManifest(t *testing.T, s *Server, serverID int64) int64 {
	t.Helper()
	m := mustBuild(t, []mcp.Tool{{Name: "calendar_read"}})
	res, err := s.workflow.CheckAndRecord(context.Background(), serverID, m)
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	return res.ManifestID
}
