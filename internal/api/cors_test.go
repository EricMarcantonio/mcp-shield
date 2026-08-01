package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// newCORSTestServer builds an API server whose CORS allowlist is exactly
// origins. Passing none exercises the default posture: no allowlist at all.
func newCORSTestServer(t *testing.T, origins ...string) *Server {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	opts := []Option{}
	if len(origins) > 0 {
		policy, err := NewCORSPolicy(origins)
		if err != nil {
			t.Fatalf("new cors policy for %v: %v", origins, err)
		}
		opts = append(opts, WithCORS(policy))
	}
	wf := approval.New(store, approval.FailModeBlock)
	return NewServer(store, wf, filepath.Join(t.TempDir(), "no-such-templates"), opts...)
}

// TestCORSIsAbsentUntilConfigured is the default posture, and it is the
// security-relevant one: approve/reject are unauthenticated by design, so an
// unconfigured gateway must give a cross-origin page nothing to work with.
func TestCORSIsAbsentUntilConfigured(t *testing.T) {
	s := newCORSTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("an unconfigured gateway sent Access-Control-Allow-Origin: %q", got)
	}
}

func TestCORSAllowsAConfiguredOrigin(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if !strings.Contains(rr.Header().Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q, want it to include Origin so caches never share a CORS response across origins", rr.Header().Get("Vary"))
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected the request itself to still be served, got %d", rr.Code)
	}
}

// TestCORSNeverReflectsAnUnlistedOrigin is the anti-requirement: reflecting
// whatever Origin arrives is indistinguishable from "*" for an attacker, who
// controls that header.
func TestCORSNeverReflectsAnUnlistedOrigin(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q for an unlisted origin", got)
	}
}

func TestCORSPreflightFromAnAllowedOriginSucceeds(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodOptions, "/api/manifests/1/approve", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("preflight Access-Control-Allow-Methods = %q, want it to include POST", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
		t.Fatalf("preflight Access-Control-Allow-Headers = %q, want it to include Content-Type", got)
	}
}

func TestCORSPreflightFromAnUnlistedOriginIsRefused(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodOptions, "/api/manifests/1/approve", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("preflight from an unlisted origin returned %d, want 403", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("refused preflight still sent Access-Control-Allow-Origin: %q", got)
	}
}

// TestCORSPreflightIsRefusedWhenNothingIsConfigured pins the default: a
// browser gets a straight refusal rather than a route that silently 405s.
func TestCORSPreflightIsRefusedWhenNothingIsConfigured(t *testing.T) {
	s := newCORSTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/manifests/1/approve", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("preflight against an unconfigured gateway returned %d, want 403", rr.Code)
	}
}

// TestCORSNeverAllowsCredentials: this API has no cookies or sessions to
// carry, and Allow-Credentials with an allowlisted origin is exactly the
// combination that would let a third-party page act as a logged-in operator
// if one were ever added.
func TestCORSNeverAllowsCredentials(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want it never sent", got)
	}
}

func TestNewCORSPolicyRejectsWildcard(t *testing.T) {
	for _, raw := range []string{"*", "http://localhost:5173,*"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewCORSPolicy(ParseOriginList(raw)); err == nil {
				t.Fatalf("NewCORSPolicy(%q) succeeded; a wildcard must never be accepted", raw)
			}
		})
	}
}

func TestNewCORSPolicyRejectsMalformedOrigins(t *testing.T) {
	malformed := map[string]string{
		"no scheme":          "localhost:5173",
		"trailing path":      "http://localhost:5173/admin",
		"trailing slash":     "http://localhost:5173/",
		"unsupported scheme": "ftp://localhost:5173",
		"no host":            "http://",
	}
	for name, origin := range malformed {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCORSPolicy([]string{origin}); err == nil {
				t.Fatalf("NewCORSPolicy(%q) succeeded, want a configuration error", origin)
			}
		})
	}
}

func TestParseOriginListTrimsAndDropsEmptyEntries(t *testing.T) {
	got := ParseOriginList(" http://localhost:5173 , , http://127.0.0.1:5173 ")
	want := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	if len(got) != len(want) {
		t.Fatalf("ParseOriginList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseOriginList = %v, want %v", got, want)
		}
	}
}

func TestParseOriginListOfNothingIsEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", ",", " , "} {
		if got := ParseOriginList(raw); len(got) != 0 {
			t.Fatalf("ParseOriginList(%q) = %v, want no origins", raw, got)
		}
	}
}

// TestCORSOriginMatchIsExact guards against prefix/suffix matching, the
// classic allowlist bug: "http://localhost:5173.evil.example" must not match
// "http://localhost:5173".
func TestCORSOriginMatchIsExact(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	for _, origin := range []string{
		"http://localhost:5173.evil.example",
		"http://evil.example?http://localhost:5173",
		"https://localhost:5173",
		"http://localhost:51730",
	} {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
			req.Header.Set("Origin", origin)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("origin %q was allowed as %q", origin, got)
			}
		})
	}
}

// TestCrossOriginFormPostIsRefused closes the hole CORS itself does not: a
// form POST is a "simple" request, so the browser never preflights it and the
// dashboard's approve/reject forms are reachable from any page on the
// internet. The Origin header is still sent, and an unlisted one is refused.
func TestCrossOriginFormPostIsRefused(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	body := strings.NewReader("username=eric&reason=drive-by")
	req := httptest.NewRequest(http.MethodPost, "/manifests/1/approve", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin form post returned %d, want 403", rr.Code)
	}
}

// A request with no Origin header at all is not a browser request: the CLI
// and curl must keep working exactly as before.
func TestRequestWithoutAnOriginIsUnaffected(t *testing.T) {
	s := newCORSTestServer(t, "http://localhost:5173")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/servers", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("a request with no Origin returned %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a request with no Origin got Access-Control-Allow-Origin: %q", got)
	}
}
