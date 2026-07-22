package mcp

import (
	"context"
	"encoding/json"
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

// Gate is implemented by the approval workflow. It is defined here (in
// terms of this package's own Tool/Prompt/Resource types) rather than in
// terms of a manifest.Manifest, so that internal/mcp never has to import
// internal/manifest — manifest already imports mcp for its Tool/Prompt/
// Resource types, and a reverse import would create a cycle. The adapter
// that bridges this interface to approval.Workflow lives in internal/app,
// which is free to import both.
type Gate interface {
	CheckAndRecord(ctx context.Context, serverName string, tools []Tool, prompts []Prompt, resources []Resource) (allowed, warn bool, manifestID int64, state string, err error)
}

// serverSession holds the lazily-started upstream connection for one
// configured server.
type serverSession struct {
	cfg ServerConfig

	mu     sync.Mutex
	client *UpstreamClient
	inited bool
}

func (s *serverSession) ensureStarted(ctx context.Context) (*UpstreamClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		// The subprocess must outlive this one HTTP request — it's a
		// persistent per-server session reused across many requests —
		// so it's started against context.Background(), not the
		// request's context, which would otherwise kill the process the
		// moment this request's context is done.
		c, err := NewStdioUpstreamClient(context.Background(), s.cfg.Command, s.cfg.Args, s.cfg.Env)
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
		h.servers[c.Name] = &serverSession{cfg: c}
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

	allowed, _, _, state, err := h.gate.CheckAndRecord(ctx, name, tools, prompts, resources)
	if err != nil {
		writeError(w, req.ID, CodeUpstreamError, "gate check failed: "+err.Error())
		return
	}
	if !allowed {
		code := CodeManifestPending
		msg := "manifest pending approval"
		if state == "REJECTED" {
			code = CodeManifestRejected
			msg = "manifest rejected"
		}
		writeError(w, req.ID, code, msg)
		return
	}

	switch req.Method {
	case MethodInitialize:
		writeResult(w, req.ID, InitializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo:      ServerInfo{Name: name, Version: "proxied"},
		})
	case MethodToolsList:
		writeResult(w, req.ID, ToolsListResult{Tools: tools})
	case MethodPromptsList:
		writeResult(w, req.ID, PromptsListResult{Prompts: prompts})
	case MethodResourcesList:
		writeResult(w, req.ID, ResourcesListResult{Resources: resources})
	case MethodToolsCall:
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeError(w, req.ID, CodeUpstreamError, "invalid tools/call params: "+err.Error())
			return
		}
		result, err := client.CallTool(ctx, params.Name, params.Arguments)
		if err != nil {
			writeError(w, req.ID, CodeUpstreamError, "upstream tools/call failed: "+err.Error())
			return
		}
		writeResult(w, req.ID, result)
	default:
		resp, err := client.Call(ctx, req.Method, json.RawMessage(req.Params))
		if err != nil {
			writeError(w, req.ID, CodeUpstreamError, "upstream call failed: "+err.Error())
			return
		}
		writeJSON(w, resp)
	}
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
