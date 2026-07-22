// Package manifest builds a canonical, hashable snapshot of an MCP server's
// advertised tools, prompts, and resources.
package manifest

import (
	"sort"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// Manifest is the canonical capability snapshot of one upstream MCP server.
// It intentionally holds only capability content (tools/prompts/resources),
// not storage identifiers like a database server ID — the manifest hash
// must depend solely on what the upstream server advertises, not on how
// mcp-shield happens to track it internally.
type Manifest struct {
	Tools     []mcp.Tool
	Prompts   []mcp.Prompt
	Resources []mcp.Resource
}

// Build assembles a Manifest from live upstream lists, sorting each
// collection by name so equivalent capability sets always canonicalize
// identically regardless of the order the upstream server returned them in.
func Build(tools []mcp.Tool, prompts []mcp.Prompt, resources []mcp.Resource) *Manifest {
	m := &Manifest{
		Tools:     append([]mcp.Tool(nil), tools...),
		Prompts:   append([]mcp.Prompt(nil), prompts...),
		Resources: append([]mcp.Resource(nil), resources...),
	}
	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Name < m.Tools[j].Name })
	sort.Slice(m.Prompts, func(i, j int) bool { return m.Prompts[i].Name < m.Prompts[j].Name })
	sort.Slice(m.Resources, func(i, j int) bool { return m.Resources[i].URI < m.Resources[j].URI })
	return m
}

// ToolByName returns the tool with the given name, or nil.
func (m *Manifest) ToolByName(name string) *mcp.Tool {
	for i := range m.Tools {
		if m.Tools[i].Name == name {
			return &m.Tools[i]
		}
	}
	return nil
}
