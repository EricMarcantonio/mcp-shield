package api

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/diff"
)

type dashboardPendingRow struct {
	PendingManifestView
}

type dashboardHomeData struct {
	Pending []dashboardPendingRow
}

type dashboardServersRow struct {
	database.Server
	ApprovedHash string
}

type dashboardServersData struct {
	Servers []dashboardServersRow
}

type dashboardManifestDetailData struct {
	Manifest ManifestView
	Diff     *diffView
	History  []database.Approval
}

type diffView struct {
	AddedTools       []string
	RemovedTools     []string
	ChangedTools     []string
	AddedPrompts     []string
	RemovedPrompts   []string
	AddedResources   []string
	RemovedResources []string
}

func buildDiffView(diffJSON string) *diffView {
	if diffJSON == "" {
		return nil
	}
	var d diff.Diff
	if err := json.Unmarshal([]byte(diffJSON), &d); err != nil {
		return nil
	}
	changed := make([]string, 0, len(d.ChangedTools))
	for _, tc := range d.ChangedTools {
		changed = append(changed, tc.Name)
	}
	return &diffView{
		AddedTools: d.AddedTools, RemovedTools: d.RemovedTools, ChangedTools: changed,
		AddedPrompts: d.AddedPrompts, RemovedPrompts: d.RemovedPrompts,
		AddedResources: d.AddedResources, RemovedResources: d.RemovedResources,
	}
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render template failed", "template", name, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) requireTemplates(w http.ResponseWriter) bool {
	if s.tmpl == nil {
		http.Error(w, "dashboard templates not available", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) handleDashboardHome(w http.ResponseWriter, r *http.Request) {
	if !s.requireTemplates(w) {
		return
	}
	ctx := r.Context()
	pending, err := s.store.ListPendingManifests(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]dashboardPendingRow, 0, len(pending))
	for _, m := range pending {
		v, err := toPendingView(ctx, s.store, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows = append(rows, dashboardPendingRow{v})
	}
	renderTemplate(w, s.tmpl, "pending.html", dashboardHomeData{Pending: rows})
}

func (s *Server) handleDashboardServers(w http.ResponseWriter, r *http.Request) {
	if !s.requireTemplates(w) {
		return
	}
	ctx := r.Context()
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]dashboardServersRow, 0, len(servers))
	for _, srv := range servers {
		hash := "(none approved)"
		if approved, err := s.store.GetApprovedManifest(ctx, srv.ID); err == nil && approved != nil {
			hash = approved.Hash
		}
		rows = append(rows, dashboardServersRow{Server: srv, ApprovedHash: hash})
	}
	renderTemplate(w, s.tmpl, "servers.html", dashboardServersData{Servers: rows})
}

func (s *Server) handleDashboardManifestDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireTemplates(w) {
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	m, err := s.store.GetManifestByID(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	view, err := toManifestView(ctx, s.store, m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	history, err := s.store.ListApprovalsForManifest(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, s.tmpl, "manifest_detail.html", dashboardManifestDetailData{
		Manifest: view,
		Diff:     buildDiffView(m.DiffJSON),
		History:  history,
	})
}

func (s *Server) handleDashboardApprove(w http.ResponseWriter, r *http.Request) {
	s.handleDashboardDecision(w, r, s.workflow.Approve)
}

func (s *Server) handleDashboardReject(w http.ResponseWriter, r *http.Request) {
	s.handleDashboardDecision(w, r, s.workflow.Reject)
}

func (s *Server) handleDashboardDecision(w http.ResponseWriter, r *http.Request, decide func(ctx context.Context, manifestID int64, username, reason string) error) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	username := r.FormValue("username")
	if username == "" {
		username = "dashboard"
	}
	reason := r.FormValue("reason")
	if err := decide(r.Context(), id, username, reason); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
