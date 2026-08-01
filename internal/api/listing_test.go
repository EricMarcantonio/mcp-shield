package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// getJSON issues a GET and decodes a 200 body into out, failing the test on
// any other status.
func getJSON(t *testing.T, s *Server, path string, out any) {
	t.Helper()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatalf("GET %s: decode %s: %v", path, rr.Body.String(), err)
	}
}

func getStatus(t *testing.T, s *Server, path string) (int, errorBody) {
	t.Helper()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	var body errorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body
}

// manifestListResponse mirrors the wire shape so the tests read the same
// JSON an integrator would.
type manifestListResponse struct {
	Items      []ManifestSummaryView `json:"items"`
	Pagination PaginationView        `json:"pagination"`
}

type decisionListResponse struct {
	Items      []DecisionView `json:"items"`
	Pagination PaginationView `json:"pagination"`
}

// seedAPIHistory drives real capability changes through the gate so the
// manifests, diffs and decisions under test are the ones the gateway
// actually produces, not hand-built rows.
//
// calendar ends with: v1 APPROVED, v2 REJECTED, v3 PENDING.
func seedAPIHistory(t *testing.T) (*Server, database.Store, int64, int64) {
	t.Helper()
	s, store, wf, serverID := newTestServer(t)
	ctx := context.Background()

	record := func(tools []mcp.Tool) int64 {
		t.Helper()
		res, err := wf.CheckAndRecord(ctx, serverID, mustBuild(t, tools))
		if err != nil {
			t.Fatalf("check and record: %v", err)
		}
		return res.ManifestID
	}

	v1 := record([]mcp.Tool{{Name: "calendar_read"}})
	if err := wf.Approve(ctx, v1, "eric", "baseline"); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	v2 := record([]mcp.Tool{{Name: "calendar_read"}, {Name: "execute_command"}})
	if err := wf.Reject(ctx, v2, "sam", "execute_command is not acceptable"); err != nil {
		t.Fatalf("reject v2: %v", err)
	}
	v3 := record([]mcp.Tool{{Name: "calendar_read"}, {Name: "calendar_create"}})

	_ = v3
	return s, store, serverID, v1
}

func TestServerDetailReportsTheApprovedBaseline(t *testing.T) {
	s, _, _, approvedID := seedAPIHistory(t)

	var got ServerDetailView
	getJSON(t, s, "/api/servers/calendar", &got)

	if got.Name != "calendar" {
		t.Fatalf("name = %q, want calendar", got.Name)
	}
	if got.ApprovedManifest == nil {
		t.Fatal("approved_manifest is null, but this server has an approved baseline")
	}
	if got.ApprovedManifest.ID != approvedID {
		t.Fatalf("approved_manifest.id = %d, want %d", got.ApprovedManifest.ID, approvedID)
	}
	if got.ApprovedManifest.State != database.StateApproved {
		t.Fatalf("approved_manifest.state = %q, want APPROVED", got.ApprovedManifest.State)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at is zero")
	}
}

// TestServerDetailSaysSoWhenNothingIsApproved: a server with no baseline is
// an ordinary state — the fail-closed starting point — and must be
// reported as an explicit null rather than an error or an omitted field.
func TestServerDetailSaysSoWhenNothingIsApproved(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	var got ServerDetailView
	getJSON(t, s, "/api/servers/calendar", &got)

	if got.ApprovedManifest != nil {
		t.Fatalf("approved_manifest = %+v, want null", got.ApprovedManifest)
	}

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/servers/calendar", nil))
	var raw map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	if _, present := raw["approved_manifest"]; !present {
		t.Fatal("approved_manifest must be present and null, not omitted: an absent field reads as a client bug")
	}
}

func TestUnknownServerIsNotFound(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	status, body := getStatus(t, s, "/api/servers/no-such-server")
	if status != http.StatusNotFound || body.Code != CodeNotFound {
		t.Fatalf("got %d/%q, want 404/%s", status, body.Code, CodeNotFound)
	}
}

// TestManifestListShowsEveryStateNotJustPending is the gap this endpoint
// exists to close: ListPendingManifests and GetApprovedManifest were the
// only two ways in, so a UI could not show a server's history at all.
func TestManifestListShowsEveryStateNotJustPending(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got manifestListResponse
	getJSON(t, s, "/api/manifests", &got)

	if len(got.Items) != 3 {
		t.Fatalf("got %d manifests, want all 3 states represented: %+v", len(got.Items), got.Items)
	}
	states := map[string]bool{}
	for _, m := range got.Items {
		states[m.State] = true
		if m.Server != "calendar" {
			t.Fatalf("manifest %d reports server %q", m.ID, m.Server)
		}
		if m.Hash == "" {
			t.Fatalf("manifest %d has no hash", m.ID)
		}
	}
	for _, want := range []string{database.StatePending, database.StateRejected, database.StateApproved} {
		if !states[want] {
			t.Fatalf("state %s missing from %+v", want, got.Items)
		}
	}
}

func TestManifestListIsNewestFirst(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got manifestListResponse
	getJSON(t, s, "/api/manifests", &got)

	if got.Items[0].State != database.StatePending {
		t.Fatalf("first item state = %q, want the most recent manifest (PENDING)", got.Items[0].State)
	}
	for i := 1; i < len(got.Items); i++ {
		if got.Items[i].CreatedAt.After(got.Items[i-1].CreatedAt) {
			t.Fatalf("item %d is newer than item %d; the list is not newest-first", i, i-1)
		}
	}
}

func TestManifestListFiltersByState(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var pending manifestListResponse
	getJSON(t, s, "/api/manifests?state=PENDING", &pending)
	if len(pending.Items) != 1 || pending.Items[0].State != database.StatePending {
		t.Fatalf("state=PENDING returned %+v", pending.Items)
	}

	var decided manifestListResponse
	getJSON(t, s, "/api/manifests?state=APPROVED&state=REJECTED", &decided)
	if len(decided.Items) != 2 {
		t.Fatalf("state=APPROVED&state=REJECTED returned %d items, want 2", len(decided.Items))
	}

	var commaSeparated manifestListResponse
	getJSON(t, s, "/api/manifests?state=APPROVED,REJECTED", &commaSeparated)
	if len(commaSeparated.Items) != 2 {
		t.Fatalf("comma-separated states returned %d items, want 2", len(commaSeparated.Items))
	}
}

// An unknown state is a typo, and returning an empty list for it looks
// exactly like "nothing is in that state".
func TestManifestListRejectsAnUnknownState(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)
	status, body := getStatus(t, s, "/api/manifests?state=APROVED")
	if status != http.StatusBadRequest || body.Code != CodeInvalidRequest {
		t.Fatalf("got %d/%q, want 400/%s", status, body.Code, CodeInvalidRequest)
	}
}

func TestManifestListFiltersByServer(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got manifestListResponse
	getJSON(t, s, "/api/manifests?server=calendar", &got)
	if len(got.Items) != 3 {
		t.Fatalf("got %d manifests for calendar, want 3", len(got.Items))
	}

	status, body := getStatus(t, s, "/api/manifests?server=no-such-server")
	if status != http.StatusNotFound || body.Code != CodeNotFound {
		t.Fatalf("filtering by an unknown server gave %d/%q, want 404/%s", status, body.Code, CodeNotFound)
	}
}

// TestManifestListLooksUpByHash is how a client holding only a hash finds
// the row: a hash identifies content, but it is only unique within one
// server, so it is a filter and never the address.
func TestManifestListLooksUpByHash(t *testing.T) {
	s, _, _, approvedID := seedAPIHistory(t)

	var all manifestListResponse
	getJSON(t, s, "/api/manifests", &all)
	var hash string
	for _, m := range all.Items {
		if m.ID == approvedID {
			hash = m.Hash
		}
	}
	if hash == "" {
		t.Fatal("could not find the approved manifest's hash")
	}

	var got manifestListResponse
	getJSON(t, s, "/api/manifests?server=calendar&hash="+hash, &got)
	if len(got.Items) != 1 || got.Items[0].ID != approvedID {
		t.Fatalf("hash lookup returned %+v, want just manifest %d", got.Items, approvedID)
	}
}

func TestManifestListPaginates(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var first manifestListResponse
	getJSON(t, s, "/api/manifests?limit=2", &first)
	if len(first.Items) != 2 {
		t.Fatalf("limit=2 returned %d items", len(first.Items))
	}
	if !first.Pagination.HasMore {
		t.Fatal("has_more must be true when a further page exists")
	}
	if first.Pagination.Limit != 2 || first.Pagination.Offset != 0 || first.Pagination.Count != 2 {
		t.Fatalf("pagination = %+v", first.Pagination)
	}

	var second manifestListResponse
	getJSON(t, s, "/api/manifests?limit=2&offset=2", &second)
	if len(second.Items) != 1 {
		t.Fatalf("second page returned %d items, want the remaining 1", len(second.Items))
	}
	if second.Pagination.HasMore {
		t.Fatal("has_more must be false on the last page")
	}

	if first.Items[0].ID == second.Items[0].ID {
		t.Fatal("pages overlap; the same manifest appeared on both")
	}
}

func TestListEndpointsHaveABoundedDefaultLimit(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	for _, path := range []string{"/api/manifests", "/api/decisions"} {
		t.Run(path, func(t *testing.T) {
			var got struct {
				Pagination PaginationView `json:"pagination"`
			}
			getJSON(t, s, path, &got)
			if got.Pagination.Limit != database.DefaultPageLimit {
				t.Fatalf("default limit = %d, want %d", got.Pagination.Limit, database.DefaultPageLimit)
			}
		})
	}
}

func TestListEndpointsClampAnOversizedLimit(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got manifestListResponse
	getJSON(t, s, "/api/manifests?limit=100000", &got)
	if got.Pagination.Limit != database.MaxPageLimit {
		t.Fatalf("limit = %d, want it clamped to %d", got.Pagination.Limit, database.MaxPageLimit)
	}
}

func TestListEndpointsRejectNonsensePagination(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	for _, path := range []string{
		"/api/manifests?limit=lots",
		"/api/manifests?limit=-1",
		"/api/manifests?offset=-1",
		"/api/manifests?offset=yesterday",
		"/api/decisions?limit=lots",
	} {
		t.Run(path, func(t *testing.T) {
			status, body := getStatus(t, s, path)
			if status != http.StatusBadRequest || body.Code != CodeInvalidRequest {
				t.Fatalf("got %d/%q, want 400/%s", status, body.Code, CodeInvalidRequest)
			}
		})
	}
}

// TestDecisionFeedIsTheAuditTrail covers the endpoint that did not exist at
// all: who approved or rejected what, when, and why, across every server.
func TestDecisionFeedIsTheAuditTrail(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got decisionListResponse
	getJSON(t, s, "/api/decisions", &got)

	if len(got.Items) != 2 {
		t.Fatalf("got %d decisions, want the approval and the rejection: %+v", len(got.Items), got.Items)
	}
	newest := got.Items[0]
	if newest.Decision != database.DecisionRejected {
		t.Fatalf("newest decision = %q, want REJECTED (newest first)", newest.Decision)
	}
	if newest.Username != "sam" {
		t.Fatalf("username = %q, want sam", newest.Username)
	}
	if newest.Reason != "execute_command is not acceptable" {
		t.Fatalf("reason = %q", newest.Reason)
	}
	if newest.Server != "calendar" {
		t.Fatalf("server = %q, want calendar", newest.Server)
	}
	if newest.ManifestHash == "" {
		t.Fatal("manifest_hash is empty; a decision has to say which capability set it was about")
	}
	if newest.DecidedAt.IsZero() {
		t.Fatal("decided_at is zero")
	}
}

func TestDecisionFeedFiltersByServer(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	var got decisionListResponse
	getJSON(t, s, "/api/decisions?server=calendar", &got)
	if len(got.Items) != 2 {
		t.Fatalf("got %d decisions for calendar, want 2", len(got.Items))
	}

	status, body := getStatus(t, s, "/api/decisions?server=no-such-server")
	if status != http.StatusNotFound || body.Code != CodeNotFound {
		t.Fatalf("got %d/%q, want 404/%s", status, body.Code, CodeNotFound)
	}
}

// TestPerManifestDecisionHistory is the other half of the audit trail:
// Store.ListApprovalsForManifest existed and was reachable from no endpoint.
func TestPerManifestDecisionHistory(t *testing.T) {
	s, _, _, approvedID := seedAPIHistory(t)

	var got decisionListResponse
	getJSON(t, s, "/api/manifests/"+itoa(approvedID)+"/decisions", &got)

	if len(got.Items) != 1 {
		t.Fatalf("got %d decisions for manifest %d, want 1: %+v", len(got.Items), approvedID, got.Items)
	}
	only := got.Items[0]
	if only.Decision != database.DecisionApproved || only.Username != "eric" || only.Reason != "baseline" {
		t.Fatalf("decision = %+v, want eric's baseline approval", only)
	}
	if only.ManifestID != approvedID {
		t.Fatalf("manifest_id = %d, want %d", only.ManifestID, approvedID)
	}
}

func TestDecisionHistoryOfAManifestWithNoDecisionsIsAnEmptyList(t *testing.T) {
	s, store, serverID, _ := seedAPIHistory(t)
	ctx := context.Background()

	pending, err := store.ListManifests(ctx, database.ManifestFilter{
		ServerID: &serverID, States: []string{database.StatePending},
	})
	if err != nil || len(pending) != 1 {
		t.Fatalf("locate the pending manifest: %v, %v", pending, err)
	}

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/"+itoa(pending[0].ID)+"/decisions", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// An empty JSON array, never null: a client iterating the result must
	// not have to special-case "no decisions yet".
	var raw struct {
		Items []DecisionView `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.Items == nil {
		t.Fatalf("items must be [] rather than null: %s", rr.Body.String())
	}
	if len(raw.Items) != 0 {
		t.Fatalf("items = %+v, want empty", raw.Items)
	}
}

func TestDecisionHistoryOfAnUnknownManifestIsNotFound(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	status, body := getStatus(t, s, "/api/manifests/9999/decisions")
	if status != http.StatusNotFound || body.Code != CodeNotFound {
		t.Fatalf("got %d/%q, want 404/%s", status, body.Code, CodeNotFound)
	}
}

// TestReleasedListEndpointsKeepTheirBareArrayShape is the compatibility
// guard. /api/servers and /api/manifests/pending shipped in v0.1.0 as bare
// JSON arrays and the in-tree CLI still decodes them that way; the new
// envelope is for the new endpoints only.
func TestReleasedListEndpointsKeepTheirBareArrayShape(t *testing.T) {
	s, _, _, _ := seedAPIHistory(t)

	for _, path := range []string{"/api/servers", "/api/manifests/pending"} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			var asArray []json.RawMessage
			if err := json.Unmarshal(rr.Body.Bytes(), &asArray); err != nil {
				t.Fatalf("%s no longer decodes as a bare array: %v (%s)", path, err, rr.Body.String())
			}
		})
	}
}

// newTestServerWithoutSeed is used where a bare, empty gateway is wanted.
func TestEmptyListsReturnAnEmptyItemsArray(t *testing.T) {
	store, err := database.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := NewServer(store, approval.New(store, approval.FailModeBlock), t.TempDir())

	for _, path := range []string{"/api/manifests", "/api/decisions"} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			var raw struct {
				Items []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if raw.Items == nil {
				t.Fatalf("items must be [] rather than null: %s", rr.Body.String())
			}
		})
	}
}
