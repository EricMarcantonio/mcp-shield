// Package mcp implements the wire types and transport for the Model
// Context Protocol's JSON-RPC 2.0 framing, plus a proxy client/server pair
// used by mcp-shield to sit between an AI client and an upstream MCP server.
package mcp

import "encoding/json"

const JSONRPCVersion = "2.0"

// ProtocolVersion is the single MCP specification revision this gateway
// speaks, both upstream (in the initialize request it sends) and downstream
// (in the initialize result it returns). 2025-11-25 is the current stable
// revision; the gateway deliberately does not negotiate versions yet. See
// decision D7 in docs/superpowers/specs/2026-07-25-oss-hardening-design.md.
const ProtocolVersion = "2025-11-25"

// Request is a JSON-RPC 2.0 request or notification (ID nil => notification).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error codes used by mcp-shield itself (application-reserved range).
const (
	CodeManifestPending  = -32001
	CodeManifestRejected = -32002
	CodeUnknownServer    = -32003
	CodeUpstreamError    = -32004
)

// Tool is an MCP tool descriptor as advertised by tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Prompt is an MCP prompt descriptor as advertised by prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Resource is an MCP resource descriptor as advertised by resources/list.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

type PromptsListResult struct {
	Prompts []Prompt `json:"prompts"`
}

type ResourcesListResult struct {
	Resources []Resource `json:"resources"`
}

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Method names intercepted by the gateway.
const (
	MethodInitialize    = "initialize"
	MethodToolsList     = "tools/list"
	MethodPromptsList   = "prompts/list"
	MethodResourcesList = "resources/list"
	MethodToolsCall     = "tools/call"
)
