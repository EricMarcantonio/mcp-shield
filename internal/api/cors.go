package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Why this API's CORS allowlist has no wildcard, ever
//
// approve and reject are unauthenticated by design: the gateway is meant to
// be bound to localhost, and the `username` field is a caller-supplied
// attestation rather than a verified identity. That design is only safe
// while a request has to come from software the operator ran deliberately.
//
// "Access-Control-Allow-Origin: *" would break exactly that. Any page a
// visitor opened could then drive their browser into approving a capability
// change on their own local gateway — the browser reaches localhost, the
// gateway asks for no credentials, and the audit trail records whatever
// username the attacking page chose to send. Reflecting an arbitrary Origin
// back is the same hole with extra steps, since the attacker sets that
// header.
//
// So enabling CORS here is a deployment decision with consequences, not a
// convenience toggle: the operator names the exact origins they trust to act
// on their behalf, and nothing else is accepted.

// corsMaxAgeSeconds is how long a browser may cache one preflight result.
// Ten minutes: long enough that a UI does not preflight every click, short
// enough that removing an origin from the allowlist takes effect promptly.
const corsMaxAgeSeconds = "600"

// corsAllowedMethods and corsAllowedHeaders are fixed rather than echoed
// back from the request. A browser only ever needs these to drive this API,
// and echoing whatever was asked for turns the preflight into a mirror that
// approves anything.
var (
	corsAllowedMethods = strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodOptions}, ", ")
	corsAllowedHeaders = "Content-Type"
)

// CORSPolicy is the set of browser origins permitted to call this API. The
// zero value allows nothing, which is the default posture.
type CORSPolicy struct {
	allowed map[string]bool
}

// NewCORSPolicy compiles an allowlist. Every entry must be a bare origin —
// scheme://host[:port] with no path — and "*" is refused outright. An empty
// list yields a policy that allows nothing and adds no CORS headers, which
// is what a gateway that was never configured for a browser should do.
func NewCORSPolicy(origins []string) (*CORSPolicy, error) {
	allowed := make(map[string]bool, len(origins))
	for _, raw := range origins {
		origin, err := normalizeOrigin(raw)
		if err != nil {
			return nil, err
		}
		allowed[origin] = true
	}
	return &CORSPolicy{allowed: allowed}, nil
}

// ParseOriginList splits the comma-separated form used by the
// CORS_ALLOWED_ORIGINS environment variable. It only splits and trims;
// NewCORSPolicy is what validates.
func ParseOriginList(raw string) []string {
	var origins []string
	for _, field := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

// normalizeOrigin validates one allowlist entry and lowercases it for
// comparison. Origins are compared byte-for-byte after this, so a stray
// trailing slash or path would silently never match and leave an operator
// debugging a browser error against a config that looks right.
func normalizeOrigin(raw string) (string, error) {
	origin := strings.ToLower(strings.TrimSpace(raw))
	if origin == "*" {
		return "", fmt.Errorf("cors: %q is not an allowed origin: this API is unauthenticated, so a wildcard would let any page on the internet approve capability changes on this gateway", raw)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("cors: origin %q is not a URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("cors: origin %q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("cors: origin %q names no host", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("cors: origin %q must be scheme://host[:port] with no path, query, or credentials", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// allows reports whether origin is on the allowlist. Matching is exact: a
// prefix or suffix test here is the classic allowlist bug that lets
// "http://localhost:5173.evil.example" through.
func (p *CORSPolicy) allows(origin string) bool {
	if p == nil || origin == "" {
		return false
	}
	return p.allowed[strings.ToLower(origin)]
}

// wrap applies the policy to h: it answers preflights itself, annotates
// allowed cross-origin responses, and refuses cross-origin state-changing
// requests outright.
func (p *CORSPolicy) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Not a browser request. The CLI, curl and the integration
			// tests all land here and are unaffected by CORS entirely.
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")

		if isPreflight(r) {
			p.writePreflight(w, r, origin)
			return
		}
		if p.allows(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			h.ServeHTTP(w, r)
			return
		}
		if isStateChanging(r) && !isSameOrigin(origin, r.Host) {
			// A form POST is a "simple" request, so the browser never
			// preflights it: without this check the dashboard's
			// approve/reject forms would be submittable from any page on
			// the internet, with CORS none the wiser. Note that CORS alone
			// does not protect these — this does.
			writeAPIError(w, http.StatusForbidden, CodeOriginNotAllowed,
				fmt.Errorf("origin %q is not allowed to submit to this gateway", origin))
			return
		}
		// A cross-origin read from an unlisted origin is served without any
		// Allow-Origin header; the browser is what withholds the body.
		h.ServeHTTP(w, r)
	})
}

// isPreflight distinguishes a CORS preflight from an ordinary OPTIONS
// request: only the former carries Access-Control-Request-Method.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func isStateChanging(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
}

// isSameOrigin reports whether origin names the host the request was already
// addressed to, which is a request the browser would have sent regardless of
// any allowlist.
func isSameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host != "" && strings.EqualFold(u.Host, host)
}

// writePreflight answers a preflight. An unlisted origin gets an explicit
// 403 rather than a bare 405 from the router, so a developer wiring up a UI
// sees which decision was made and where to change it.
func (p *CORSPolicy) writePreflight(w http.ResponseWriter, r *http.Request, origin string) {
	w.Header().Add("Vary", "Access-Control-Request-Method")
	if !p.allows(origin) {
		writeAPIError(w, http.StatusForbidden, CodeOriginNotAllowed,
			fmt.Errorf("origin %q is not in CORS_ALLOWED_ORIGINS", origin))
		return
	}
	if !strings.Contains(corsAllowedMethods, r.Header.Get("Access-Control-Request-Method")) {
		writeAPIError(w, http.StatusForbidden, CodeOriginNotAllowed,
			fmt.Errorf("method %q is not allowed cross-origin", r.Header.Get("Access-Control-Request-Method")))
		return
	}
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Methods", corsAllowedMethods)
	header.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
	header.Set("Access-Control-Max-Age", corsMaxAgeSeconds)
	// Access-Control-Allow-Credentials is deliberately never sent: this API
	// has no cookie or session for a browser to attach, and allowing them
	// would make an allowlisted origin able to act as a signed-in operator
	// the moment one is introduced in front of the gateway.
	w.WriteHeader(http.StatusNoContent)
}
