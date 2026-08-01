package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// listResponse is the envelope every paginated endpoint returns. Items is
// always an array, never null, so a client can iterate the result without
// special-casing "nothing yet".
type listResponse struct {
	Items      any            `json:"items"`
	Pagination PaginationView `json:"pagination"`
}

// handleAPIGetServer reports one server and the baseline the gate currently
// enforces for it. Store.GetServerByName existed with no endpoint in front
// of it, so a UI could list servers but never open one.
func (s *Server) handleAPIGetServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, err := s.store.GetServerByName(ctx, r.PathValue("name"))
	if err != nil {
		writeStoreError(w, namedServerError(r.PathValue("name"), err))
		return
	}

	view := ServerDetailView{ID: srv.ID, Name: srv.Name, Endpoint: srv.Endpoint, CreatedAt: srv.CreatedAt}

	approved, err := s.store.GetApprovedManifest(ctx, srv.ID)
	switch {
	case errors.Is(err, database.ErrNotFound):
		// No baseline yet is the fail-closed starting state, not a failure.
	case err != nil:
		writeStoreError(w, err)
		return
	default:
		summaries, err := toManifestSummaryViews(ctx, s.store, []database.ManifestRecord{*approved})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		view.ApprovedManifest = &summaries[0]
	}
	writeJSON(w, view)
}

// handleAPIListManifests is the general manifest listing: a server's whole
// history, filterable by state and hash. Before it, ListPendingManifests and
// GetApprovedManifest were the only ways in, so nothing that was neither
// pending nor the current baseline could be shown at all.
func (s *Server) handleAPIListManifests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, CodeInvalidRequest, err)
		return
	}
	states, err := parseStates(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, CodeInvalidRequest, err)
		return
	}
	serverID, ok := s.resolveServerFilter(w, r)
	if !ok {
		return
	}

	filter := database.ManifestFilter{
		ServerID: serverID,
		Hash:     r.URL.Query().Get("hash"),
		States:   states,
		Page:     overfetch(page),
	}
	records, err := s.store.ListManifests(ctx, filter)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	records, hasMore := trimOverfetch(records, page)

	views, err := toManifestSummaryViews(ctx, s.store, records)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, listResponse{Items: views, Pagination: paginationFor(page, len(views), hasMore)})
}

// handleAPIListDecisions is the audit trail across every server: who
// approved or rejected what, when, and why. It is the product of this
// project and had no endpoint at all.
func (s *Server) handleAPIListDecisions(w http.ResponseWriter, r *http.Request) {
	page, err := parsePage(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, CodeInvalidRequest, err)
		return
	}
	serverID, ok := s.resolveServerFilter(w, r)
	if !ok {
		return
	}
	s.writeDecisions(w, r, database.DecisionFilter{ServerID: serverID, Page: overfetch(page)}, page)
}

// handleAPIListManifestDecisions is the decision history of one manifest.
func (s *Server) handleAPIListManifestDecisions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	page, err := parsePage(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, CodeInvalidRequest, err)
		return
	}
	// Asked for explicitly so an unknown manifest is a 404 rather than an
	// empty history, which reads as "nobody has decided yet".
	if _, err := s.store.GetManifestByID(r.Context(), id); err != nil {
		writeStoreError(w, namedManifestError(id, err))
		return
	}
	s.writeDecisions(w, r, database.DecisionFilter{ManifestID: &id, Page: overfetch(page)}, page)
}

func (s *Server) writeDecisions(w http.ResponseWriter, r *http.Request, filter database.DecisionFilter, page database.Page) {
	decisions, err := s.store.ListDecisions(r.Context(), filter)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	decisions, hasMore := trimOverfetch(decisions, page)
	views := toDecisionViews(decisions)
	writeJSON(w, listResponse{Items: views, Pagination: paginationFor(page, len(views), hasMore)})
}

// resolveServerFilter turns a ?server=<name> parameter into an id. An
// unknown name is a 404 rather than an empty list: a typo and "this server
// has no manifests" are different answers, and silently returning the
// second for the first is how a UI ends up showing a reassuring empty page.
func (s *Server) resolveServerFilter(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	name := r.URL.Query().Get("server")
	if name == "" {
		return nil, true
	}
	srv, err := s.store.GetServerByName(r.Context(), name)
	if err != nil {
		writeStoreError(w, namedServerError(name, err))
		return nil, false
	}
	return &srv.ID, true
}

// namedServerError puts the name the caller used into a not-found message.
// "database: not found" tells an integrator nothing about which of their
// parameters was wrong.
func namedServerError(name string, err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("%w: no server named %q is registered", database.ErrNotFound, name)
	}
	return err
}

// namedManifestError does the same for a manifest id.
func namedManifestError(id int64, err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("%w: no manifest with id %d", database.ErrNotFound, id)
	}
	return err
}

// --- pagination -------------------------------------------------------------

// overfetch asks for one row beyond the page. Its presence is exactly what
// has_more reports, and it costs one row rather than the second full scan a
// total count would need.
func overfetch(page database.Page) database.Page {
	effective := page.Effective()
	return database.Page{Limit: effective.Limit + 1, Offset: effective.Offset}
}

// trimOverfetch drops the probe row and reports whether it was there.
func trimOverfetch[T any](rows []T, page database.Page) (trimmed []T, hasMore bool) {
	limit := page.Effective().Limit
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}

func paginationFor(page database.Page, count int, hasMore bool) PaginationView {
	effective := page.Effective()
	return PaginationView{Limit: effective.Limit, Offset: effective.Offset, Count: count, HasMore: hasMore}
}

// parsePage reads limit and offset. Both are optional; supplying either as
// anything but a sensible number is refused rather than silently ignored,
// because a client whose paging parameter was dropped sees a plausible
// first page and never learns it is missing rows.
func parsePage(r *http.Request) (database.Page, error) {
	limit, err := parseBoundedInt(r, "limit", 1)
	if err != nil {
		return database.Page{}, err
	}
	offset, err := parseBoundedInt(r, "offset", 0)
	if err != nil {
		return database.Page{}, err
	}
	return database.Page{Limit: limit, Offset: offset}, nil
}

// parseBoundedInt reads an optional non-negative query parameter. An absent
// parameter yields zero, which every caller treats as "unset".
func parseBoundedInt(r *http.Request, name string, minimum int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}
	if value < minimum {
		return 0, fmt.Errorf("%s must be at least %d, got %d", name, minimum, value)
	}
	return value, nil
}

// validManifestStates is the set ?state= accepts, keyed off the states the
// store actually writes so the two cannot drift.
var validManifestStates = map[string]bool{
	database.StatePending:    true,
	database.StateApproved:   true,
	database.StateRejected:   true,
	database.StateSuperseded: true,
}

// parseStates reads ?state=, which may repeat and may be comma-separated.
// An unrecognised state is refused: returning an empty list for "APROVED"
// is indistinguishable from "nothing is approved".
func parseStates(r *http.Request) ([]string, error) {
	var states []string
	for _, raw := range r.URL.Query()["state"] {
		for _, field := range strings.Split(raw, ",") {
			state := strings.ToUpper(strings.TrimSpace(field))
			if state == "" {
				continue
			}
			if !validManifestStates[state] {
				return nil, fmt.Errorf("unknown manifest state %q: expected one of PENDING, APPROVED, REJECTED, SUPERSEDED", field)
			}
			states = append(states, state)
		}
	}
	return states, nil
}
