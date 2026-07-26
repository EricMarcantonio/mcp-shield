package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ServerConfig describes one upstream MCP server the gateway proxies to.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// GateDecision reports which tools/prompts/resources are currently safe
// to expose and call for one server. It's deliberately not a single
// allow/deny bool: a manifest that isn't fully approved still lets
// through whatever is unchanged from the last approved baseline, so a
// rejected or pending capability change only withholds the specific new
// or changed items, not the entire server.
type GateDecision struct {
	ManifestID int64
	State      string // PENDING, APPROVED, REJECTED, or SUPERSEDED
	Warn       bool

	SafeTools     map[string]bool
	SafePrompts   map[string]bool
	SafeResources map[string]bool
}

// Gate is implemented by the approval workflow. It is defined here (in
// terms of this package's own Tool/Prompt/Resource types) rather than in
// terms of a manifest.Manifest, so that internal/mcp never has to import
// internal/manifest — manifest already imports mcp for its Tool/Prompt/
// Resource types, and a reverse import would create a cycle. The adapter
// that bridges this interface to approval.Workflow lives in internal/app,
// which is free to import both.
type Gate interface {
	CheckAndRecord(ctx context.Context, serverName string, tools []Tool, prompts []Prompt, resources []Resource) (*GateDecision, error)
}

// upstream is the subset of *UpstreamClient that serverSession depends on.
// It exists so tests can substitute a fake upstream without spawning a real
// subprocess; *UpstreamClient satisfies it unmodified.
type upstream interface {
	Initialize(ctx context.Context) (*InitializeResult, error)
	ListTools(ctx context.Context) ([]Tool, error)
	ListPrompts(ctx context.Context) ([]Prompt, error)
	ListResources(ctx context.Context) ([]Resource, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error)
	Call(ctx context.Context, method string, params any) (*Response, error)
	Close() error
}

// upstreamFactory constructs the upstream connection for a serverSession.
// Production code always uses stdioUpstreamFactory; tests substitute a
// factory that returns a fake.
type upstreamFactory func(ctx context.Context, cfg ServerConfig) (upstream, error)

func stdioUpstreamFactory(ctx context.Context, cfg ServerConfig) (upstream, error) {
	return NewStdioUpstreamClient(ctx, cfg.Command, cfg.Args, cfg.Env)
}

// serverSession holds the lazily-started upstream connection for one
// configured server.
type serverSession struct {
	cfg       ServerConfig
	newClient upstreamFactory

	mu     sync.Mutex
	client upstream
	inited bool
}

func (s *serverSession) ensureStarted(ctx context.Context) (upstream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		// The subprocess must outlive this one HTTP request — it's a
		// persistent per-server session reused across many requests —
		// so it's started against context.Background(), not the
		// request's context, which would otherwise kill the process the
		// moment this request's context is done.
		c, err := s.newClient(context.Background(), s.cfg)
		if err != nil {
			return nil, err
		}
		s.client = c
	}
	if !s.inited {
		if _, err := s.client.Initialize(ctx); err != nil {
			return nil, err
		}
		s.inited = true
	}
	return s.client, nil
}

// DownstreamHandler is the client-facing side of the gateway: an HTTP
// handler at POST /mcp/{server} that speaks one JSON-RPC request/response
// per HTTP call. Every intercepted method re-fetches the upstream server's
// current tools/prompts/resources and re-runs the gate before any
// capability data or tool-call result is written back to the client —
// there is no cached "already approved this session" shortcut, so a
// server that mutates its capabilities mid-session cannot slip a new tool
// past approval by piggybacking on an earlier allow decision.
type DownstreamHandler struct {
	servers map[string]*serverSession
	gate    Gate
}

func NewDownstreamHandler(cfg []ServerConfig, gate Gate) (*DownstreamHandler, error) {
	h := &DownstreamHandler{servers: make(map[string]*serverSession, len(cfg)), gate: gate}
	for _, c := range cfg {
		h.servers[c.Name] = &serverSession{cfg: c, newClient: stdioUpstreamFactory}
	}
	return h, nil
}

func (h *DownstreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/mcp/")
	name = strings.Trim(name, "/")

	session, ok := h.servers[name]
	if !ok {
		writeError(w, nil, CodeUnknownServer, "unknown mcp server: "+name)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, CodeUpstreamError, "invalid request body: "+err.Error())
		return
	}

	ctx := r.Context()
	client, err := session.ensureStarted(ctx)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream connect failed: "+err.Error())
		return
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream tools/list failed: "+err.Error())
		return
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream prompts/list failed: "+err.Error())
		return
	}
	resources, err := client.ListResources(ctx)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream resources/list failed: "+err.Error())
		return
	}

	decision, err := h.gate.CheckAndRecord(ctx, name, tools, prompts, resources)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "gate check failed: "+err.Error())
		return
	}

	switch req.Method {
	case MethodInitialize:
		// The handshake itself reveals no capability data, so it's never
		// gated — a client must be able to connect even to a brand new,
		// fully unapproved server; it just won't see any tools yet.
		writeResult(w, req.ID, InitializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo:      ServerInfo{Name: name, Version: "proxied"},
		})
	case MethodToolsList:
		writeResult(w, req.ID, ToolsListResult{Tools: filterTools(tools, decision.SafeTools)})
	case MethodPromptsList:
		writeResult(w, req.ID, PromptsListResult{Prompts: filterPrompts(prompts, decision.SafePrompts)})
	case MethodResourcesList:
		writeResult(w, req.ID, ResourcesListResult{Resources: filterResources(resources, decision.SafeResources)})
	case MethodToolsCall:
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeError(w, req.ID, CodeUpstreamError, "invalid tools/call params: "+err.Error())
			return
		}
		if !decision.SafeTools[params.Name] {
			writeError(w, req.ID, blockedCode(decision.State), fmt.Sprintf("tool %q is part of an unapproved manifest change (%s)", params.Name, decision.State))
			return
		}
		result, err := client.CallTool(ctx, params.Name, params.Arguments)
		if err != nil {
			writeError(w, req.ID, CodeUpstreamError, "upstream tools/call failed: "+err.Error())
			return
		}
		writeResult(w, req.ID, result)
	default:
		// Methods we don't specifically understand (resources/read,
		// prompts/get, notifications, ...) can't be filtered item by
		// item without parsing method-specific params, so they get the
		// coarse treatment: only forwarded once this server has *some*
		// approved baseline to stand on.
		if len(decision.SafeTools) == 0 && len(decision.SafePrompts) == 0 && len(decision.SafeResources) == 0 {
			writeError(w, req.ID, blockedCode(decision.State), "server has no approved manifest yet")
			return
		}
		resp, err := client.Call(ctx, req.Method, req.Params)
		if err != nil {
			writeError(w, req.ID, CodeUpstreamError, "upstream call failed: "+err.Error())
			return
		}
		writeJSON(w, resp)
	}
}

func blockedCode(state string) int {
	if state == "REJECTED" {
		return CodeManifestRejected
	}
	return CodeManifestPending
}

func filterTools(tools []Tool, safe map[string]bool) []Tool {
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if safe[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

func filterPrompts(prompts []Prompt, safe map[string]bool) []Prompt {
	out := make([]Prompt, 0, len(prompts))
	for _, p := range prompts {
		if safe[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

func filterResources(resources []Resource, safe map[string]bool) []Resource {
	out := make([]Resource, 0, len(resources))
	for _, r := range resources {
		if safe[r.URI] {
			out = append(out, r)
		}
	}
	return out
}

func writeResult(w http.ResponseWriter, id any, result any) {
	b, err := json.Marshal(result)
	if err != nil {
		writeError(w, id, CodeUpstreamError, "marshal result: "+err.Error())
		return
	}
	writeJSON(w, Response{JSONRPC: JSONRPCVersion, ID: id, Result: b})
}

func writeError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, Response{JSONRPC: JSONRPCVersion, ID: id, Error: &RPCError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
