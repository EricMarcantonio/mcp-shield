package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeErrorCode reads the machine-readable code out of an error response,
// failing the test if the body is not the uniform error shape. Every error
// this package emits goes through it, so a handler that hand-rolls its own
// body is caught by whichever test covers that handler.
func decodeErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	if body.Error == "" {
		t.Fatalf("expected a human-readable error message, got %q", rr.Body.String())
	}
	if body.Code == "" {
		t.Fatalf("expected a machine-readable code, got %q", rr.Body.String())
	}
	return body.Code
}

func TestErrorCodesAreUniformAcrossEndpoints(t *testing.T) {
	s, _, _, _ := newTestServer(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"unparseable manifest id", http.MethodGet, "/api/manifests/not-a-number", "", http.StatusBadRequest, CodeInvalidRequest},
		{"missing manifest", http.MethodGet, "/api/manifests/9999", "", http.StatusNotFound, CodeNotFound},
		{"missing manifest diff", http.MethodGet, "/api/manifests/9999/diff", "", http.StatusNotFound, CodeNotFound},
		{"missing manifest approvals", http.MethodGet, "/api/manifests/9999/approvals", "", http.StatusNotFound, CodeNotFound},
		{"missing server", http.MethodGet, "/api/servers/no-such-server", "", http.StatusNotFound, CodeNotFound},
		{"malformed decision body", http.MethodPost, "/api/manifests/1/approve", "{", http.StatusBadRequest, CodeInvalidRequest},
		{"decision without username", http.MethodPost, "/api/manifests/1/approve", `{"reason":"ok"}`, http.StatusBadRequest, CodeUsernameRequired},
		{"notifications not configured", http.MethodGet, "/api/notifications/failed", "", http.StatusNotFound, CodeNotConfigured},
		{"unknown manifest state filter", http.MethodGet, "/api/manifests?state=BOGUS", "", http.StatusBadRequest, CodeInvalidRequest},
		{"non-numeric limit", http.MethodGet, "/api/manifests?limit=lots", "", http.StatusBadRequest, CodeInvalidRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reader *strings.Reader
			if tc.body != "" {
				reader = strings.NewReader(tc.body)
			} else {
				reader = strings.NewReader("")
			}
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, reader))

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if got := decodeErrorCode(t, rr); got != tc.wantCode {
				t.Fatalf("expected code %q, got %q", tc.wantCode, got)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("expected application/json, got %q", ct)
			}
		})
	}
}

// TestErrorKeepsLegacyMessageField pins the pre-1.0 field that shipped in
// v0.1.x. The code is additive; removing "error" would break every existing
// caller including this repo's own CLI.
func TestErrorKeepsLegacyMessageField(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/manifests/9999", nil))

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("expected a string \"error\" field, got %v", body)
	}
}
