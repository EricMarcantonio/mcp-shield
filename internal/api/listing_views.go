package api

import (
	"context"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/database"
)

// On identifying a manifest: id addresses, hash describes
//
// A manifest's real identity is its hash — that is the whole premise of the
// project, and the hash is what the gate compares. It is nonetheless not the
// public address of a manifest here, and that is deliberate rather than
// inherited.
//
// A hash is unique only within one server: the schema's unique index is
// (server_id, hash), and two servers advertising identical capabilities
// legitimately produce the same hash. So "the manifest with hash abc123" is
// not a question with one answer, and an endpoint addressed that way would
// need the server name alongside it to mean anything — at which point it is
// a two-part key that happens to look like an identifier.
//
// The id, by contrast, addresses one row: one snapshot of one server, with
// one state and one decision history. That is what approve, reject, diff and
// the notification payloads all refer to, and it shipped in v0.1.0.
//
// So: id addresses (path parameters, references between objects), hash
// describes (present on every manifest response, and available as a filter
// via GET /api/manifests?server=…&hash=… for a client that holds only a
// hash). Neither is hidden, and neither is asked to do the other's job.

// PaginationView reports the page a client actually received.
//
// There is no total. Counting every matching row means a second full scan
// that is stale the moment it is returned, and no UI needs it to page
// correctly — HasMore answers the only question a "next" button asks.
type PaginationView struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	Count   int  `json:"count"`
	HasMore bool `json:"has_more"`
}

// ManifestSummaryView is one row of GET /api/manifests.
type ManifestSummaryView struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	State     string    `json:"state"`
	Changes   []string  `json:"changes"`
	CreatedAt time.Time `json:"created_at"`
}

// DecisionView is one entry in the audit trail: a human's approval or
// rejection of one manifest.
//
// Username is what the caller said it was. The gateway does not
// authenticate it and does not promise it is true — see the attestation
// model in docs/api.md — but it is always something a caller supplied,
// never something the gateway invented.
type DecisionView struct {
	ID           int64     `json:"id"`
	ManifestID   int64     `json:"manifest_id"`
	ManifestHash string    `json:"manifest_hash"`
	Server       string    `json:"server"`
	Decision     string    `json:"decision"`
	Username     string    `json:"username"`
	Reason       string    `json:"reason"`
	DecidedAt    time.Time `json:"decided_at"`
}

// ServerSummaryView is one row of GET /api/servers.
//
// database.Server carries no JSON tags, so serialising it directly emitted Go
// field names (ID, Name, CreatedAt) from this one route while every other
// route in the API is snake_case -- including ServerDetailView immediately
// below, describing the same entity. A client had to special-case a single
// endpoint for no reason a caller could infer.
type ServerSummaryView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	CreatedAt time.Time `json:"created_at"`
}

func toServerSummaryViews(servers []database.Server) []ServerSummaryView {
	out := make([]ServerSummaryView, 0, len(servers))
	for _, srv := range servers {
		out = append(out, ServerSummaryView{
			ID:        srv.ID,
			Name:      srv.Name,
			Endpoint:  srv.Endpoint,
			CreatedAt: srv.CreatedAt,
		})
	}
	return out
}

// ServerDetailView is GET /api/servers/{name}.
type ServerDetailView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	CreatedAt time.Time `json:"created_at"`

	// ApprovedManifest is the baseline the gate currently enforces, or null
	// for a server that has never had one approved. Null is a real answer
	// here — it is the fail-closed starting state, not a missing value — so
	// the field is always present.
	ApprovedManifest *ManifestSummaryView `json:"approved_manifest"`
}

func toDecisionView(d database.Decision) DecisionView {
	return DecisionView{
		ID:           d.ID,
		ManifestID:   d.ManifestID,
		ManifestHash: d.ManifestHash,
		Server:       d.ServerName,
		Decision:     d.Decision,
		Username:     d.Username,
		Reason:       d.Reason,
		DecidedAt:    d.CreatedAt,
	}
}

func toDecisionViews(decisions []database.Decision) []DecisionView {
	views := make([]DecisionView, 0, len(decisions))
	for _, d := range decisions {
		views = append(views, toDecisionView(d))
	}
	return views
}

// serverNames resolves server ids to names once per request. A page of
// manifests usually spans a handful of servers, so looking each row's server
// up independently turns one listing into up to MaxPageLimit extra queries.
type serverNames struct {
	store database.Store
	known map[int64]string
}

func newServerNames(store database.Store) *serverNames {
	return &serverNames{store: store, known: map[int64]string{}}
}

func (n *serverNames) of(ctx context.Context, serverID int64) (string, error) {
	if name, ok := n.known[serverID]; ok {
		return name, nil
	}
	name, err := serverNameFor(ctx, n.store, serverID)
	if err != nil {
		return "", err
	}
	n.known[serverID] = name
	return name, nil
}

func toManifestSummaryViews(ctx context.Context, store database.Store, records []database.ManifestRecord) ([]ManifestSummaryView, error) {
	names := newServerNames(store)
	views := make([]ManifestSummaryView, 0, len(records))
	for _, m := range records {
		name, err := names.of(ctx, m.ServerID)
		if err != nil {
			return nil, err
		}
		views = append(views, ManifestSummaryView{
			ID: m.ID, Server: name, Hash: m.Hash, State: m.State,
			Changes: summarizeDiff(m.DiffJSON), CreatedAt: m.CreatedAt,
		})
	}
	return views, nil
}
