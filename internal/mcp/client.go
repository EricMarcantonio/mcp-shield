package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// UpstreamClient is an MCP JSON-RPC client that talks to a single upstream
// MCP server over a Transport (stdio subprocess for MVP).
type UpstreamClient struct {
	transport Transport

	nextID int64

	mu      sync.Mutex
	pending map[int64]chan *Response
	closed  bool

	dispatchOnce sync.Once
}

// NewStdioUpstreamClient starts cmdPath as a subprocess and returns a client
// bound to it over stdio.
func NewStdioUpstreamClient(ctx context.Context, cmdPath string, args, env []string) (*UpstreamClient, error) {
	t := NewStdioTransport(cmdPath, args, env)
	if err := t.Start(ctx); err != nil {
		return nil, err
	}
	c := &UpstreamClient{
		transport: t,
		pending:   make(map[int64]chan *Response),
	}
	c.dispatchOnce.Do(func() { go c.dispatchLoop() })
	return c, nil
}

func (c *UpstreamClient) dispatchLoop() {
	frames, errs := c.transport.Recv()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			var resp Response
			if err := json.Unmarshal(frame, &resp); err != nil {
				continue
			}
			c.deliver(&resp)
		case err, ok := <-errs:
			if !ok {
				errs = nil
			}
			_ = err
			if frames == nil && errs == nil {
				c.failAllPending(fmt.Errorf("upstream transport closed"))
				return
			}
		}
		if frames == nil && errs == nil {
			c.failAllPending(fmt.Errorf("upstream transport closed"))
			return
		}
	}
}

func (c *UpstreamClient) deliver(resp *Response) {
	id, ok := idAsInt64(resp.ID)
	if !ok {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (c *UpstreamClient) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		ch <- &Response{JSONRPC: JSONRPCVersion, Error: &RPCError{Code: CodeUpstreamError, Message: err.Error()}}
		delete(c.pending, id)
	}
}

func idAsInt64(id any) (int64, bool) {
	switch v := id.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

// Call issues a JSON-RPC request and waits for the matching response.
func (c *UpstreamClient) Call(ctx context.Context, method string, params any) (*Response, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("upstream client closed")
	}
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan *Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		raw = b
	}

	req := Request{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: raw}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if err := c.transport.Send(b); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *UpstreamClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	resp, err := c.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      ClientInfo{Name: "mcp-shield", Version: "0.1.0"},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("initialize: %s", resp.Error.Message)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("initialize: decode result: %w", err)
	}
	return &result, nil
}

func (c *UpstreamClient) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.Call(ctx, MethodToolsList, nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("tools/list: decode result: %w", err)
	}
	return result.Tools, nil
}

func (c *UpstreamClient) ListPrompts(ctx context.Context) ([]Prompt, error) {
	resp, err := c.Call(ctx, MethodPromptsList, nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/list: %s", resp.Error.Message)
	}
	var result PromptsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("prompts/list: decode result: %w", err)
	}
	return result.Prompts, nil
}

func (c *UpstreamClient) ListResources(ctx context.Context) ([]Resource, error) {
	resp, err := c.Call(ctx, MethodResourcesList, nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list: %s", resp.Error.Message)
	}
	var result ResourcesListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("resources/list: decode result: %w", err)
	}
	return result.Resources, nil
}

func (c *UpstreamClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	resp, err := c.Call(ctx, MethodToolsCall, CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call: %s", resp.Error.Message)
	}
	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("tools/call: decode result: %w", err)
	}
	return &result, nil
}

func (c *UpstreamClient) Close() error {
	return c.transport.Close()
}
