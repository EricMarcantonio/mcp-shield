package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeUpstream is a scriptable stand-in for *UpstreamClient, substituted via
// serverSession.newClient so these tests never spawn a real subprocess.
type fakeUpstream struct {
	tools     []Tool
	prompts   []Prompt
	resources []Resource
}

func (f *fakeUpstream) Initialize(_ context.Context) (*InitializeResult, error) {
	return &InitializeResult{ProtocolVersion: "2024-11-05"}, nil
}
func (f *fakeUpstream) ListTools(_ context.Context) ([]Tool, error)     { return f.tools, nil }
func (f *fakeUpstream) ListPrompts(_ context.Context) ([]Prompt, error) { return f.prompts, nil }
func (f *fakeUpstream) ListResources(_ context.Context) ([]Resource, error) {
	return f.resources, nil
}

func (f *fakeUpstream) CallTool(_ context.Context, name string, _ json.RawMessage) (*CallToolResult, error) {
	return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: "ok: " + name}}}, nil
}

func (f *fakeUpstream) Call(_ context.Context, _ string, _ any) (*Response, error) {
	return &Response{JSONRPC: JSONRPCVersion, Result: json.RawMessage(`{}`)}, nil
}
func (f *fakeUpstream) Close() error { return nil }

// fakeGate lets each test dictate the gate's decision (or force an error)
// without exercising the real approval workflow.
type fakeGate struct {
	decision *GateDecision
	err      error
}

func (g *fakeGate) CheckAndRecord(_ context.Context, _ string, _ []Tool, _ []Prompt, _ []Resource) (*GateDecision, error) {
	return g.decision, g.err
}

func newTestHandler(t *testing.T, up upstream, gate Gate) *DownstreamHandler {
	t.Helper()
	h, err := NewDownstreamHandler([]ServerConfig{{Name: "cal"}}, gate)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	h.servers["cal"].newClient = func(_ context.Context, _ ServerConfig) (upstream, error) { return up, nil }
	return h
}

func rpc(t *testing.T, h *DownstreamHandler, path, body string) *Response {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	h.ServeHTTP(rr, req)
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rr.Body.String())
	}
	return &resp
}

func TestToolsListFiltersUnsafeTools(t *testing.T) {
	up := &fakeUpstream{tools: []Tool{{Name: "calendar_read"}, {Name: "upload_receipt"}}}
	gate := &fakeGate{decision: &GateDecision{State: "PENDING", SafeTools: map[string]bool{"calendar_read": true}}}

	resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "calendar_read" {
		t.Fatalf("expected only calendar_read, got %+v", result.Tools)
	}
}

func TestPromptsListFiltersUnsafePrompts(t *testing.T) {
	up := &fakeUpstream{prompts: []Prompt{{Name: "safe_prompt"}, {Name: "unsafe_prompt"}}}
	gate := &fakeGate{decision: &GateDecision{State: "PENDING", SafePrompts: map[string]bool{"safe_prompt": true}}}

	resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)

	var result PromptsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Prompts) != 1 || result.Prompts[0].Name != "safe_prompt" {
		t.Fatalf("expected only safe_prompt, got %+v", result.Prompts)
	}
}

func TestResourcesListFiltersUnsafeResources(t *testing.T) {
	up := &fakeUpstream{resources: []Resource{{URI: "file:///safe"}, {URI: "file:///unsafe"}}}
	gate := &fakeGate{decision: &GateDecision{State: "PENDING", SafeResources: map[string]bool{"file:///safe": true}}}

	resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)

	var result ResourcesListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Resources) != 1 || result.Resources[0].URI != "file:///safe" {
		t.Fatalf("expected only file:///safe, got %+v", result.Resources)
	}
}

func TestToolsCallBlockedReturnsManifestStateCode(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		wantCode int
	}{
		{"pending manifest blocks with pending code", "PENDING", CodeManifestPending},
		{"rejected manifest blocks with rejected code", "REJECTED", CodeManifestRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeUpstream{tools: []Tool{{Name: "upload_receipt"}}}
			gate := &fakeGate{decision: &GateDecision{State: tc.state, SafeTools: map[string]bool{}}}

			resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal",
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_receipt"}}`)

			if resp.Error == nil {
				t.Fatalf("expected error response, got result %s", resp.Result)
			}
			if resp.Error.Code != tc.wantCode {
				t.Fatalf("expected code %d, got %d", tc.wantCode, resp.Error.Code)
			}
			if !strings.Contains(resp.Error.Message, "upload_receipt") {
				t.Fatalf("expected error message to name the blocked tool, got %q", resp.Error.Message)
			}
		})
	}
}

func TestToolsCallSafeToolForwarded(t *testing.T) {
	up := &fakeUpstream{tools: []Tool{{Name: "calendar_read"}}}
	gate := &fakeGate{decision: &GateDecision{State: "APPROVED", SafeTools: map[string]bool{"calendar_read": true}}}

	resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"calendar_read"}}`)

	if resp.Error != nil {
		t.Fatalf("expected no error, got %+v", resp.Error)
	}
	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok: calendar_read" {
		t.Fatalf("expected forwarded call result, got %+v", result.Content)
	}
}

func TestInitializeNeverGated(t *testing.T) {
	up := &fakeUpstream{}
	gate := &fakeGate{decision: &GateDecision{State: "PENDING", SafeTools: map[string]bool{}}}

	resp := rpc(t, newTestHandler(t, up, gate), "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

	if resp.Error != nil {
		t.Fatalf("expected initialize to succeed even with no approved baseline, got %+v", resp.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ProtocolVersion == "" {
		t.Fatal("expected a protocol version in the initialize result")
	}
}

func TestUnknownServer404Code(t *testing.T) {
	gate := &fakeGate{decision: &GateDecision{}}
	h := newTestHandler(t, &fakeUpstream{}, gate)

	resp := rpc(t, h, "/mcp/nope", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	if resp.Error == nil || resp.Error.Code != CodeUnknownServer {
		t.Fatalf("expected CodeUnknownServer, got %+v", resp.Error)
	}
}

func TestInvalidBodyRejected(t *testing.T) {
	gate := &fakeGate{decision: &GateDecision{}}
	h := newTestHandler(t, &fakeUpstream{}, gate)

	resp := rpc(t, h, "/mcp/cal", `{not json`)

	if resp.Error == nil || resp.Error.Code != CodeUpstreamError {
		t.Fatalf("expected CodeUpstreamError for malformed body, got %+v", resp.Error)
	}
}

func TestPassthroughBlockedWithoutBaseline(t *testing.T) {
	cases := []struct {
		name        string
		decision    *GateDecision
		wantBlocked bool
	}{
		{
			name:        "no approved baseline blocks passthrough methods",
			decision:    &GateDecision{State: "PENDING"},
			wantBlocked: true,
		},
		{
			name:        "any approved baseline forwards passthrough methods",
			decision:    &GateDecision{State: "APPROVED", SafeTools: map[string]bool{"calendar_read": true}},
			wantBlocked: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := &fakeGate{decision: tc.decision}
			h := newTestHandler(t, &fakeUpstream{}, gate)

			resp := rpc(t, h, "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{}}`)

			if tc.wantBlocked {
				if resp.Error == nil {
					t.Fatal("expected passthrough to be blocked without an approved baseline")
				}
			} else if resp.Error != nil {
				t.Fatalf("expected passthrough to be forwarded, got error %+v", resp.Error)
			}
		})
	}
}

// TestEnsureStartedNeverRecoversDeadUpstream pins a known bug (design doc
// finding C2, internal/mcp/server.go:57-79): ensureStarted only calls
// newClient when s.client is nil, and nothing in this codebase ever sets
// s.client back to nil once it's been assigned. So once an upstream
// subprocess dies, ensureStarted keeps handing back that same dead client
// forever — even if a fresh call to newClient would succeed — until the
// whole gateway process is restarted. Fail-closed on a dead upstream is
// correct; fail-closed-forever is an availability bug. Not fixed here —
// Phase 5 owns exposing UpstreamClient.Closed() and having ensureStarted
// discard and respawn a dead client.
func TestEnsureStartedNeverRecoversDeadUpstream(t *testing.T) {
	first := &fakeUpstream{}
	session := &serverSession{
		cfg:       ServerConfig{Name: "cal"},
		newClient: func(_ context.Context, _ ServerConfig) (upstream, error) { return first, nil },
	}

	got, err := session.ensureStarted(context.Background())
	if err != nil {
		t.Fatalf("ensureStarted: %v", err)
	}
	if got != upstream(first) {
		t.Fatalf("expected the first client back on the first call")
	}

	// Simulate the upstream subprocess dying and a fresh connection now
	// being available: swap in a factory that would return a different,
	// healthy client.
	second := &fakeUpstream{}
	session.newClient = func(_ context.Context, _ ServerConfig) (upstream, error) { return second, nil }

	got, err = session.ensureStarted(context.Background())
	if err != nil {
		t.Fatalf("ensureStarted: %v", err)
	}
	if got != upstream(first) {
		t.Fatalf("known-bug pin broke: ensureStarted returned a new client after the factory "+
			"changed — if it now recovers a dead upstream, update this test to assert recovery "+
			"instead of the stale-forever behavior; got %+v, want the original first client", got)
	}
}

func TestGateErrorFailsClosed(t *testing.T) {
	gate := &fakeGate{err: errors.New("db down")}
	h := newTestHandler(t, &fakeUpstream{tools: []Tool{{Name: "calendar_read"}}}, gate)

	resp := rpc(t, h, "/mcp/cal", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	if resp.Error == nil {
		t.Fatal("expected an error response when the gate itself fails, never a result")
	}
	if resp.Result != nil {
		t.Fatalf("expected no result when the gate fails, got %s", resp.Result)
	}
}
