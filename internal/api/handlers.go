// Package api exposes the HTTP approval API (and the server-rendered
// dashboard that sits on top of it) on the gateway's :8081 listener.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// DefaultTemplatesDir is where the dashboard looks for its templates when
// none is configured, relative to the process's working directory (the
// repo root when running `go run ./cmd/gateway` or the Docker image's
// WORKDIR, both of which COPY web/ alongside the binary).
const DefaultTemplatesDir = "web/dashboard/templates"

type Server struct {
	store    database.Store
	workflow *approval.Workflow
	mux      *http.ServeMux
	tmpl     *template.Template // nil if templates failed to load; dashboard routes 503 in that case
}

// NewServer builds the API+dashboard handler. templatesDir may be empty,
// in which case DefaultTemplatesDir is used; if templates fail to parse
// the JSON API still works, only the HTML dashboard routes degrade.
func NewServer(store database.Store, workflow *approval.Workflow, templatesDir string) *Server {
	if templatesDir == "" {
		templatesDir = DefaultTemplatesDir
	}

	s := &Server{store: store, workflow: workflow, mux: http.NewServeMux()}

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
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) // empty/absent body is fine, fields default to ""
	}
	if body.Username == "" {
		body.Username = "unknown"
	}
	if err := decide(r.Context(), id, body.Username, body.Reason); err != nil {
		writeJSONNotFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "ok": true})
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
	switch {
	case errors.Is(err, database.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err)
	case errors.Is(err, approval.ErrNotPending):
		writeJSONError(w, http.StatusConflict, err)
	default:
		writeJSONError(w, http.StatusInternalServerError, err)
	}
}
