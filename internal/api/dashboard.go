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

// diffView is the manifest_detail.html view of a stored diff: every field is
// a flat list of names the template renders as its own section. Each field of
// diff.Diff must appear here and in the template, or the dashboard silently
// hides that class of change from the person about to approve it.
type diffView struct {
	AddedTools       []string
	RemovedTools     []string
	ChangedTools     []string
	AddedPrompts     []string
	RemovedPrompts   []string
	ChangedPrompts   []string
	AddedResources   []string
	RemovedResources []string
	ChangedResources []string
}

func buildDiffView(diffJSON string) *diffView {
	if diffJSON == "" {
		return nil
	}
	var d diff.Diff
	if err := json.Unmarshal([]byte(diffJSON), &d); err != nil {
		return nil
	}
	changedTools := make([]string, 0, len(d.ChangedTools))
	for _, tc := range d.ChangedTools {
		changedTools = append(changedTools, tc.Name)
	}
	changedPrompts := make([]string, 0, len(d.ChangedPrompts))
	for _, pc := range d.ChangedPrompts {
		changedPrompts = append(changedPrompts, pc.Name)
	}
	changedResources := make([]string, 0, len(d.ChangedResources))
	for _, rc := range d.ChangedResources {
		changedResources = append(changedResources, rc.URI)
	}
	return &diffView{
		AddedTools: d.AddedTools, RemovedTools: d.RemovedTools, ChangedTools: changedTools,
		AddedPrompts: d.AddedPrompts, RemovedPrompts: d.RemovedPrompts, ChangedPrompts: changedPrompts,
		AddedResources: d.AddedResources, RemovedResources: d.RemovedResources, ChangedResources: changedResources,
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Same rule as the JSON API: a decision the gateway cannot attribute is
	// refused rather than recorded under an invented identity. Both dashboard
	// templates post an explicit username, so a form without one was
	// hand-crafted.
	username, err := attributableUsername(r.FormValue("username"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := decide(r.Context(), id, username, r.FormValue("reason")); err != nil {
		http.Error(w, err.Error(), statusForStoreError(err))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
