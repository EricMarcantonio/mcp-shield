package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

const (
	allowedOrigin    = "https://console.example.com"
	disallowedOrigin = "https://evil.example.com"
)

func newCORSServer(t *testing.T, origins ...string) *Server {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "cors.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	wf := approval.New(store, approval.FailModeBlock)
	return NewServer(store, wf, filepath.Join(t.TempDir(), "no-such-templates"), WithAllowedOrigins(origins))
}

func preflight(t *testing.T, s *Server, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodOptions, "/api/servers", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}

// TestCORSDisabledByDefault is the posture that matters most: the approval
// endpoints are unauthenticated by design, so an operator who configured
// nothing must not have a browser-drivable API.
func TestCORSDisabledByDefault(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", allowedOrigin)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header with nothing configured, got %q", got)
	}
}

func TestCORSEchoesConfiguredOrigin(t *testing.T) {
	s := newCORSServer(t, allowedOrigin)
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", allowedOrigin)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("expected the configured origin echoed, got %q", got)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary: Origin so caches cannot serve one origin's response to another, got %q", got)
	}
}

func TestCORSNeverReflectsUnconfiguredOrigin(t *testing.T) {
	s := newCORSServer(t, allowedOrigin)
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", disallowedOrigin)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header for an unconfigured origin, got %q", got)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("a non-browser client sending Origin must still be served, got %d", rr.Code)
	}
}

func TestCORSPreflightFromAllowedOrigin(t *testing.T) {
	s := newCORSServer(t, allowedOrigin)
	rr := preflight(t, s, allowedOrigin)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for a preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("expected the configured origin echoed, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected Access-Control-Allow-Methods on a preflight response")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected Access-Control-Allow-Headers on a preflight response")
	}
}

func TestCORSPreflightFromDisallowedOriginIsRefused(t *testing.T) {
	s := newCORSServer(t, allowedOrigin)
	rr := preflight(t, s, disallowedOrigin)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a preflight from an unconfigured origin, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header, got %q", got)
	}
	if code := decodeErrorCode(t, rr); code != CodeOriginNotAllowed {
		t.Fatalf("expected code %q, got %q", CodeOriginNotAllowed, code)
	}
}

// TestCORSNeverCredentialed pins that the gateway does not invite a browser
// to attach the user's cookies to an unauthenticated approval API.
func TestCORSNeverCredentialed(t *testing.T) {
	s := newCORSServer(t, allowedOrigin)
	rr := preflight(t, s, allowedOrigin)
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("expected no Access-Control-Allow-Credentials, got %q", got)
	}
}

// TestCORSWildcardIsNotAnOrigin: "*" as a configured value must not become a
// blanket allow. A wildcard on an unauthenticated approval API lets any page
// on the internet drive a user's browser into approving capability changes.
func TestCORSWildcardIsNotAnOrigin(t *testing.T) {
	s := newCORSServer(t, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", disallowedOrigin)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected \"*\" to allow nothing, got %q", got)
	}
}

func TestCORSAllowsEachConfiguredOrigin(t *testing.T) {
	second := "http://localhost:5173"
	s := newCORSServer(t, allowedOrigin, second)
	for _, origin := range []string{allowedOrigin, second} {
		rr := preflight(t, s, origin)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin %s: expected it echoed, got %q", origin, got)
		}
	}
}
