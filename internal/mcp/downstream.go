package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

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

	// UpstreamTimeout bounds how long one downstream request may spend
	// waiting on the upstream server. Without it a wedged upstream holds
	// the client's connection open indefinitely.
	UpstreamTimeout time.Duration
}

// DefaultUpstreamTimeout bounds a single downstream request's upstream work.
const DefaultUpstreamTimeout = 30 * time.Second

func NewDownstreamHandler(cfg []ServerConfig, gate Gate) (*DownstreamHandler, error) {
	h := &DownstreamHandler{
		servers:         make(map[string]*serverSession, len(cfg)),
		gate:            gate,
		UpstreamTimeout: DefaultUpstreamTimeout,
	}
	for _, c := range cfg {
		h.servers[c.Name] = &serverSession{cfg: c, newClient: stdioUpstreamFactory}
	}
	return h, nil
}

// upstreamTimeout falls back to the default rather than trusting a zero
// value: UpstreamTimeout is exported, and a zero duration would expire every
// request's context immediately, taking the whole gateway down.
func (h *DownstreamHandler) upstreamTimeout() time.Duration {
	if h.UpstreamTimeout <= 0 {
		return DefaultUpstreamTimeout
	}
	return h.UpstreamTimeout
}

func (h *DownstreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := serverName(r.URL.Path)
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

	ctx, cancel := context.WithTimeout(r.Context(), h.upstreamTimeout())
	defer cancel()

	client, snap, err := h.connectAndGate(ctx, name, session)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, err.Error())
		return
	}

	h.dispatch(ctx, w, client, &req, name, snap)
}

func serverName(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "/mcp/"), "/")
}

// gatedSnapshot bundles one freshly fetched capability set with the gate's
// decision about it — the unit every method handler works from.
type gatedSnapshot struct {
	tools     []Tool
	prompts   []Prompt
	resources []Resource
	decision  *GateDecision
}

// connectAndGate starts the upstream if needed, re-fetches its full
// capability set, and re-runs the gate over it. This happens on every single
// request: there is no cached "already approved this session" shortcut, so a
// server that mutates its capabilities mid-session cannot slip a new tool
// past approval by piggybacking on an earlier allow decision.
//
// Every failure here is returned, never swallowed, so the caller can only
// fail the request closed.
func (h *DownstreamHandler) connectAndGate(ctx context.Context, name string, session *serverSession) (upstream, *gatedSnapshot, error) {
	client, err := session.ensureStarted(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("upstream connect failed: %w", err)
	}

	snap := &gatedSnapshot{}
	if snap.tools, err = client.ListTools(ctx); err != nil {
		return nil, nil, fmt.Errorf("upstream tools/list failed: %w", err)
	}
	if snap.prompts, err = client.ListPrompts(ctx); err != nil {
		return nil, nil, fmt.Errorf("upstream prompts/list failed: %w", err)
	}
	if snap.resources, err = client.ListResources(ctx); err != nil {
		return nil, nil, fmt.Errorf("upstream resources/list failed: %w", err)
	}

	if snap.decision, err = h.gate.CheckAndRecord(ctx, name, snap.tools, snap.prompts, snap.resources); err != nil {
		return nil, nil, fmt.Errorf("gate check failed: %w", err)
	}
	return client, snap, nil
}

func (h *DownstreamHandler) dispatch(ctx context.Context, w http.ResponseWriter, client upstream, req *Request, name string, snap *gatedSnapshot) {
	switch req.Method {
	case MethodInitialize:
		handleInitialize(w, req, name)
	case MethodToolsList:
		writeResult(w, req.ID, ToolsListResult{Tools: filterTools(snap.tools, snap.decision.SafeTools)})
	case MethodPromptsList:
		writeResult(w, req.ID, PromptsListResult{Prompts: filterPrompts(snap.prompts, snap.decision.SafePrompts)})
	case MethodResourcesList:
		writeResult(w, req.ID, ResourcesListResult{Resources: filterResources(snap.resources, snap.decision.SafeResources)})
	case MethodToolsCall:
		handleToolsCall(ctx, w, client, req, snap)
	default:
		handlePassthrough(ctx, w, client, req, snap)
	}
}

// handleInitialize answers the handshake without consulting the gate. The
// handshake reveals no capability data, so it is never gated — a client must
// be able to connect even to a brand new, fully unapproved server; it just
// won't see any tools yet.
func handleInitialize(w http.ResponseWriter, req *Request, name string) {
	writeResult(w, req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: name, Version: "proxied"},
	})
}

func handleToolsCall(ctx context.Context, w http.ResponseWriter, client upstream, req *Request, snap *gatedSnapshot) {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, CodeUpstreamError, "invalid tools/call params: "+err.Error())
		return
	}
	if !snap.decision.SafeTools[params.Name] {
		writeError(w, req.ID, blockedCode(snap.decision.State),
			fmt.Sprintf("tool %q is part of an unapproved manifest change (%s)", params.Name, snap.decision.State))
		return
	}
	result, err := client.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream tools/call failed: "+err.Error())
		return
	}
	writeResult(w, req.ID, result)
}

// handlePassthrough forwards methods the gateway does not specifically
// understand (resources/read, prompts/get, notifications, ...). They cannot
// be filtered item by item without parsing method-specific params, so they
// get the coarse treatment: only forwarded once this server has *some*
// approved baseline to stand on.
func handlePassthrough(ctx context.Context, w http.ResponseWriter, client upstream, req *Request, snap *gatedSnapshot) {
	d := snap.decision
	if len(d.SafeTools) == 0 && len(d.SafePrompts) == 0 && len(d.SafeResources) == 0 {
		writeError(w, req.ID, blockedCode(d.State), "server has no approved manifest yet")
		return
	}
	resp, err := client.Call(ctx, req.Method, req.Params)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "upstream call failed: "+err.Error())
		return
	}
	writeJSON(w, resp)
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
