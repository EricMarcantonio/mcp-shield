package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// respondToNextRequest reads the one request the client is expected to have
// just sent and writes back a matching response (by id) built from result.
// If result is nil, an RPCError with rpcErrMsg is sent instead. It runs in
// the caller's goroutine's background via a new goroutine so the caller can
// immediately block in the client call it's testing.
func respondToNextRequest(t *testing.T, ft *fakeTransport, result json.RawMessage, rpcErrMsg string) {
	t.Helper()
	go func() {
		req := <-ft.sent
		var r Request
		if err := json.Unmarshal(req, &r); err != nil {
			t.Errorf("decode sent request: %v", err)
			return
		}
		resp := Response{JSONRPC: JSONRPCVersion, ID: r.ID}
		if rpcErrMsg != "" {
			resp.Error = &RPCError{Code: CodeUpstreamError, Message: rpcErrMsg}
		} else {
			resp.Result = result
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		ft.frames <- out
	}()
}

// TestInitializeAdvertisesTargetProtocolVersion pins decision D7: the
// gateway speaks exactly one MCP revision, 2025-11-25, and it must be the
// same one on both sides of the proxy — an upstream handshake at one
// revision and a downstream handshake at another would let a client and a
// server disagree about the protocol while the gateway told each what it
// wanted to hear.
func TestInitializeAdvertisesTargetProtocolVersion(t *testing.T) {
	if ProtocolVersion != "2025-11-25" {
		t.Fatalf("expected the gateway to target MCP 2025-11-25 (decision D7), got %q", ProtocolVersion)
	}

	c, ft := newClientWithFake(t)
	handshake := make(chan []byte, 1)
	go func() {
		raw := <-ft.sent
		handshake <- raw
		var r Request
		_ = json.Unmarshal(raw, &r)
		out, _ := json.Marshal(Response{JSONRPC: JSONRPCVersion, ID: r.ID, Result: json.RawMessage(`{"protocolVersion":"2025-11-25"}`)})
		ft.frames <- out
	}()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var sent Request
	if err := json.Unmarshal(<-handshake, &sent); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	var params InitializeParams
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("decode initialize params: %v", err)
	}
	if params.ProtocolVersion != ProtocolVersion {
		t.Fatalf("upstream handshake advertised %q, want %q", params.ProtocolVersion, ProtocolVersion)
	}
}

func TestInitializeDecodesProtocolVersion(t *testing.T) {
	c, ft := newClientWithFake(t)
	respondToNextRequest(t, ft, json.RawMessage(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"cal","version":"1"}}`), "")

	result, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if result.ProtocolVersion != "2024-11-05" || result.ServerInfo.Name != "cal" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListToolsDecodesTools(t *testing.T) {
	c, ft := newClientWithFake(t)
	respondToNextRequest(t, ft, json.RawMessage(`{"tools":[{"name":"calendar_read"}]}`), "")

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "calendar_read" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestListPromptsDecodesPrompts(t *testing.T) {
	c, ft := newClientWithFake(t)
	respondToNextRequest(t, ft, json.RawMessage(`{"prompts":[{"name":"greeting"}]}`), "")

	prompts, err := c.ListPrompts(context.Background())
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Name != "greeting" {
		t.Fatalf("unexpected prompts: %+v", prompts)
	}
}

func TestListResourcesDecodesResources(t *testing.T) {
	c, ft := newClientWithFake(t)
	respondToNextRequest(t, ft, json.RawMessage(`{"resources":[{"uri":"file:///a"}]}`), "")

	resources, err := c.ListResources(context.Background())
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "file:///a" {
		t.Fatalf("unexpected resources: %+v", resources)
	}
}

func TestCallToolDecodesResult(t *testing.T) {
	c, ft := newClientWithFake(t)
	respondToNextRequest(t, ft, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`), "")

	result, err := c.CallTool(context.Background(), "calendar_read", nil)
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "done" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCloseClosesUnderlyingTransport(t *testing.T) {
	c, ft := newClientWithFake(t)
	closed := make(chan struct{})
	ft.onClose = func() { close(closed) }

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not close the underlying transport")
	}
}

// TestUpstreamMethodsPropagateRPCError covers the "resp.Error != nil" branch
// each of UpstreamClient's typed methods has, table-driven since the shape
// (call the method, expect an error mentioning the method name and the
// upstream's message) is identical across all of them.
func TestUpstreamMethodsPropagateRPCError(t *testing.T) {
	cases := []struct {
		name string
		call func(c *UpstreamClient) error
	}{
		{"initialize", func(c *UpstreamClient) error { _, err := c.Initialize(context.Background()); return err }},
		{"tools/list", func(c *UpstreamClient) error { _, err := c.ListTools(context.Background()); return err }},
		{"prompts/list", func(c *UpstreamClient) error { _, err := c.ListPrompts(context.Background()); return err }},
		{"resources/list", func(c *UpstreamClient) error { _, err := c.ListResources(context.Background()); return err }},
		{"tools/call", func(c *UpstreamClient) error { _, err := c.CallTool(context.Background(), "x", nil); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ft := newClientWithFake(t)
			respondToNextRequest(t, ft, nil, "upstream boom")

			err := tc.call(c)
			if err == nil || !strings.Contains(err.Error(), "upstream boom") {
				t.Fatalf("expected an error containing the upstream message, got %v", err)
			}
		})
	}
}
