package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// Machine-readable error codes. A client branches on these; the accompanying
// message is for a human and may be reworded at any time, so nothing should
// ever parse it.
//
// These are stable API surface: a code is never repurposed, and a new
// failure mode gets a new code rather than being folded into an existing
// one.
const (
	// CodeInvalidRequest: a path or query parameter was missing or
	// unparseable — a non-numeric manifest id, a negative limit.
	CodeInvalidRequest = "invalid_request"
	// CodeInvalidJSON: the request body is not the JSON this endpoint
	// expects. Nothing was recorded.
	CodeInvalidJSON = "invalid_json"
	// CodeUsernameRequired: a decision arrived with no username. The
	// gateway refuses to attribute a decision to an identity nobody
	// supplied; see the attestation model in docs/api.md.
	CodeUsernameRequired = "username_required"
	// CodeNotFound: the addressed manifest, server, or route does not exist.
	CodeNotFound = "not_found"
	// CodeConflict: the manifest is no longer PENDING, so the decision has
	// already been made. Re-read the manifest before retrying.
	CodeConflict = "conflict"
	// CodeNotConfigured: the feature behind this route is switched off in
	// this deployment (today: notifications).
	CodeNotConfigured = "not_configured"
	// CodeOriginNotAllowed: the browser's Origin is not in
	// CORS_ALLOWED_ORIGINS. Retrying will not help; the gateway's
	// configuration has to change.
	CodeOriginNotAllowed = "origin_not_allowed"
	// CodeInternal: the gateway failed for a reason that is not the
	// caller's fault. Safe to retry.
	CodeInternal = "internal"
)

// errorBody is the single error shape every JSON endpoint returns.
//
// `code` was added beside the released `error` string rather than replacing
// it. Nesting the message inside an object would have been tidier, but
// v0.1.x shipped `{"error":"..."}` and a client reading that field is
// entitled to keep working.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeAPIError(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: err.Error(), Code: code})
}

// writeStoreError maps the errors the store and approval workflow return
// onto a status and a code. It is the single place that mapping lives, so
// the same failure never reports differently depending on which endpoint
// asked.
func writeStoreError(w http.ResponseWriter, err error) {
	status, code := classifyStoreError(err)
	writeAPIError(w, status, code, err)
}

func classifyStoreError(err error) (status int, code string) {
	switch {
	case errors.Is(err, database.ErrNotFound):
		return http.StatusNotFound, CodeNotFound
	case errors.Is(err, approval.ErrNotPending):
		return http.StatusConflict, CodeConflict
	default:
		return http.StatusInternalServerError, CodeInternal
	}
}

// statusForStoreError is the dashboard's view of the same mapping: HTML
// error pages carry no code, only a status.
func statusForStoreError(err error) int {
	status, _ := classifyStoreError(err)
	return status
}
