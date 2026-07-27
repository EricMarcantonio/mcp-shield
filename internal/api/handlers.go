// Package api exposes the HTTP approval API (and the server-rendered
// dashboard that sits on top of it) on the gateway's :8081 listener.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// DefaultTemplatesDir is where the dashboard looks for its templates when
// none is configured, relative to the process's working directory (the
// repo root when running `go run ./cmd/mcp-shield` or the Docker image's
// WORKDIR, both of which COPY web/ alongside the binary).
const DefaultTemplatesDir = "web/dashboard/templates"

type Server struct {
	store    database.Store
	workflow *approval.Workflow
	mux      *http.ServeMux
	tmpl     *template.Template // nil if templates failed to load; dashboard routes 503 in that case

	// notifyMaxAttempts is the attempt count at which the dispatcher gives
	// up, and therefore the threshold for "permanently failed". Zero means
	// notifications are disabled and the failed-notifications route 404s.
	notifyMaxAttempts int
}

// Option adjusts optional server behaviour.
type Option func(*Server)

// WithFailedNotifications enables GET /api/notifications/failed, reporting
// events that reached maxAttempts without being delivered. Without it the
// route 404s: an operator who configured no targets should be told the
// surface does not exist, rather than shown an empty list that reads as
// "everything was delivered".
func WithFailedNotifications(maxAttempts int) Option {
	return func(s *Server) { s.notifyMaxAttempts = maxAttempts }
}

// NewServer builds the API+dashboard handler. templatesDir may be empty,
// in which case DefaultTemplatesDir is used; if templates fail to parse
// the JSON API still works, only the HTML dashboard routes degrade.
func NewServer(store database.Store, workflow *approval.Workflow, templatesDir string, opts ...Option) *Server {
	if templatesDir == "" {
		templatesDir = DefaultTemplatesDir
	}

	s := &Server{store: store, workflow: workflow, mux: http.NewServeMux()}
	for _, opt := range opts {
		opt(s)
	}

	tmpl, err := template.ParseGlob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		slog.Warn("dashboard templates not loaded; dashboard routes will 503", "dir", templatesDir, "error", err)
	} else {
		s.tmpl = tmpl
	}

	staticDir := filepath.Join(filepath.Dir(templatesDir), "static")
	s.routes(staticDir)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes(staticDir string) {
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.mux.HandleFunc("GET /api/servers", s.handleAPIListServers)
	s.mux.HandleFunc("GET /api/manifests/pending", s.handleAPIListPending)
	s.mux.HandleFunc("GET /api/manifests/{id}", s.handleAPIGetManifest)
	s.mux.HandleFunc("GET /api/manifests/{id}/diff", s.handleAPIGetManifestDiff)
	s.mux.HandleFunc("POST /api/manifests/{id}/approve", s.handleAPIApprove)
	s.mux.HandleFunc("POST /api/manifests/{id}/reject", s.handleAPIReject)
	s.mux.HandleFunc("GET /api/notifications/failed", s.handleAPIFailedNotifications)

	s.mux.HandleFunc("GET /", s.handleDashboardHome)
	s.mux.HandleFunc("GET /servers", s.handleDashboardServers)
	s.mux.HandleFunc("GET /manifests/{id}", s.handleDashboardManifestDetail)
	s.mux.HandleFunc("POST /manifests/{id}/approve", s.handleDashboardApprove)
	s.mux.HandleFunc("POST /manifests/{id}/reject", s.handleDashboardReject)
}

// --- JSON API ---------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleAPIListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

func (s *Server) handleAPIListPending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, err := s.store.ListPendingManifests(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]PendingManifestView, 0, len(pending))
	for _, m := range pending {
		v, err := toPendingView(ctx, s.store, m)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleAPIGetManifest(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetManifestByID(r.Context(), id)
	if err != nil {
		writeJSONNotFoundOr500(w, err)
		return
	}
	v, err := toManifestView(r.Context(), s.store, m)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleAPIGetManifestDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	m, err := s.store.GetManifestByID(r.Context(), id)
	if err != nil {
		writeJSONNotFoundOr500(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if m.DiffJSON == "" {
		_, _ = w.Write([]byte(`null`))
		return
	}
	_, _ = w.Write([]byte(m.DiffJSON)) //nolint:gosec // G705: Content-Type is set to application/json above; DiffJSON is internally-generated, already-validated JSON, not rendered as HTML
}

// handleAPIFailedNotifications reports events the dispatcher gave up on.
// Silent notification death is the exact failure the notification feature
// exists to remove, so a target that stopped working stays queryable here
// instead of disappearing.
func (s *Server) handleAPIFailedNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notifyMaxAttempts == 0 {
		writeJSONError(w, http.StatusNotFound, errors.New("notifications are not configured"))
		return
	}
	ctx := r.Context()
	rows, err := s.store.ListUndeliveredNotifications(ctx, s.notifyMaxAttempts)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]FailedNotificationView, 0, len(rows))
	for _, row := range rows {
		v, err := toFailedNotificationView(ctx, s.store, row)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, views)
}

type decisionRequest struct {
	Username string `json:"username"`
	Reason   string `json:"reason"`
}

func (s *Server) handleAPIApprove(w http.ResponseWriter, r *http.Request) {
	s.handleDecision(w, r, s.workflow.Approve)
}

func (s *Server) handleAPIReject(w http.ResponseWriter, r *http.Request) {
	s.handleDecision(w, r, s.workflow.Reject)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request, decide func(ctx context.Context, manifestID int64, username, reason string) error) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	username, err := attributableUsername(body.Username)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if err := decide(r.Context(), id, username, body.Reason); err != nil {
		writeJSONNotFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "ok": true})
}

// attributableUsername rejects a decision the gateway cannot attribute.
//
// The approvals table is the record of who authorized a capability change,
// and it is the product here. An absent username used to be stored as
// "unknown", which is indistinguishable in the audit trail from a real user
// of that name and asserts something nobody supplied. The API is
// unauthenticated by design (it is meant to be bound to localhost), so the
// username is a caller-supplied attestation rather than a verified identity
// — which is exactly why it must be supplied rather than invented: an
// unverified claim on the record is still a claim someone made, whereas a
// default is a claim the gateway made up.
//
// Every in-tree caller already sends one: the CLI defaults its -username
// flag to "cli", the dashboard templates post a hidden username field, and
// the documented curl examples pass one.
func attributableUsername(raw string) (string, error) {
	username := strings.TrimSpace(raw)
	if username == "" {
		return "", errors.New("username is required: an approval decision must be attributable to whoever made it")
	}
	return username, nil
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSONNotFoundOr500(w http.ResponseWriter, err error) {
	writeJSONError(w, statusForStoreError(err), err)
}

// statusForStoreError maps the errors the store and approval workflow return
// onto HTTP statuses. It is shared by the JSON API and the dashboard so the
// same failure never reports differently depending on which surface asked.
func statusForStoreError(err error) int {
	switch {
	case errors.Is(err, database.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, approval.ErrNotPending):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
