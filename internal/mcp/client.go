package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// dispatchLoop routes upstream responses to their waiting callers until the
// transport shuts down, then fails every in-flight call with the transport's
// terminal error.
//
// Termination is expressed in the loop condition, not checked inside the
// body. An earlier version drained a channel, set it to nil and `continue`d
// past a terminal check placed at the bottom of the body, so whenever errs
// was drained before frames the next select blocked on two nil channels with
// no ready case — the goroutine leaked, failAllPending never ran, and every
// pending caller waited until its own context expired. That was the common
// case, not a rare race: StdioTransport.readLoop's LIFO defers close errs
// before frames on every clean shutdown.
func (c *UpstreamClient) dispatchLoop() {
	frames, errs := c.transport.Recv()
	var terminal error
	for frames != nil || errs != nil {
		select {
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			c.dispatchFrame(frame)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				terminal = err
			}
		}
	}

	// io.EOF is how a healthy upstream's stdout reports a clean exit; it
	// carries no more information than "closed" and reads as noise in a log.
	if terminal == nil || errors.Is(terminal, io.EOF) {
		terminal = errors.New("transport closed")
	}
	slog.Warn("upstream transport terminated", "error", terminal)
	c.failAllPending(fmt.Errorf("upstream: %w", terminal))
}

func (c *UpstreamClient) dispatchFrame(frame []byte) {
	var resp Response
	if err := json.Unmarshal(frame, &resp); err != nil {
		slog.Warn("upstream sent undecodable frame", "error", err)
		return
	}
	c.deliver(&resp)
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
		ProtocolVersion: ProtocolVersion,
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
