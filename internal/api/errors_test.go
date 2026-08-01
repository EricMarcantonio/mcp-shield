package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// decodeErrorBody reads the uniform error envelope every failing endpoint
// returns.
func decodeErrorBody(t *testing.T, rr *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	return body
}

// TestErrorsCarryAMachineReadableCode is what makes the error surface usable
// from a UI: branching on a 400 tells a client nothing about whether to
// highlight the username field or the request body.
func TestErrorsCarryAMachineReadableCode(t *testing.T) {
	cases := []struct {
		name     string
		request  func(t *testing.T) *http.Request
		wantCode string
		wantHTTP int
	}{
		{
			name: "non-numeric manifest id",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/manifests/not-a-number", nil)
			},
			wantCode: CodeInvalidRequest,
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "unknown manifest",
			request:  func(*testing.T) *http.Request { return httptest.NewRequest(http.MethodGet, "/api/manifests/9999", nil) },
			wantCode: CodeNotFound,
			wantHTTP: http.StatusNotFound,
		},
		{
			name: "malformed decision body",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/manifests/1/approve", strings.NewReader(`{not json`))
			},
			wantCode: CodeInvalidJSON,
			wantHTTP: http.StatusBadRequest,
		},
		{
			name: "decision with no username",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/manifests/1/approve", strings.NewReader(`{"reason":"looks fine"}`))
			},
			wantCode: CodeUsernameRequired,
			wantHTTP: http.StatusBadRequest,
		},
		{
			name: "notifications not configured",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/notifications/failed", nil)
			},
			wantCode: CodeNotConfigured,
			wantHTTP: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := newTestServer(t)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, tc.request(t))

			if rr.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.wantHTTP, rr.Body.String())
			}
			body := decodeErrorBody(t, rr)
			if body.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q (body %s)", body.Code, tc.wantCode, rr.Body.String())
			}
			if strings.TrimSpace(body.Error) == "" {
				t.Fatal("error message is empty; an error a client cannot show a human is not an error message")
			}
		})
	}
}

// TestConflictingDecisionReportsAConflictCode covers the one code that needs
// a manifest in a specific state to reach.
func TestConflictingDecisionReportsAConflictCode(t *testing.T) {
	s, _, wf, serverID := newTestServer(t)
	ctx := context.Background()

	res, err := wf.CheckAndRecord(ctx, serverID, mustBuild(t, []mcp.Tool{{Name: "calendar_read"}}))
	if err != nil {
		t.Fatalf("check and record: %v", err)
	}
	if err := wf.Reject(ctx, res.ManifestID, "eric", "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodPost,
		"/api/manifests/"+itoa(res.ManifestID)+"/approve", strings.NewReader(`{"username":"eric"}`)))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if code := decodeErrorBody(t, rr).Code; code != CodeConflict {
		t.Fatalf("code = %q, want %q", code, CodeConflict)
	}
}

// TestErrorEnvelopeKeepsTheReleasedErrorField pins the compatibility
// promise: `code` was added beside the existing `error` string, not in place
// of it, so a client written against v0.1.x still reads the same field.
func TestErrorEnvelopeKeepsTheReleasedErrorField(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/9999", nil))

	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	message, ok := raw["error"].(string)
	if !ok {
		t.Fatalf(`"error" must still be a plain string, got %#v`, raw["error"])
	}
	if message == "" {
		t.Fatal(`"error" must still carry the human-readable message`)
	}
}

// TestNotFoundMessagesNameWhatWasMissing: "database: not found" tells an
// integrator nothing — not which parameter was wrong, not what to fix. Every
// 404 must name the thing that was not there.
func TestNotFoundMessagesNameWhatWasMissing(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	cases := map[string]string{
		"/api/manifests/9999":           "9999",
		"/api/manifests/9999/diff":      "9999",
		"/api/manifests/9999/decisions": "9999",
		"/api/servers/no-such-server":   "no-such-server",
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rr.Code)
			}
			if msg := decodeErrorBody(t, rr).Error; !strings.Contains(msg, want) {
				t.Fatalf("message %q does not name the missing %q", msg, want)
			}
		})
	}
}
