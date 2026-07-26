// Package manifest builds a canonical, hashable snapshot of an MCP server's
// advertised tools, prompts, and resources.
package manifest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// Manifest is the canonical capability snapshot of one upstream MCP server.
// It intentionally holds only capability content (tools/prompts/resources),
// not storage identifiers like a database server ID — the manifest hash
// must depend solely on what the upstream server advertises, not on how
// mcp-shield happens to track it internally.
//
// Every constructor in this package guarantees the same invariant: within a
// Manifest, no two tools share a name, no two prompts share a name, and no
// two resources share a URI. See validateUniqueIdentities for why that is
// enforced rather than tolerated.
type Manifest struct {
	Tools     []mcp.Tool
	Prompts   []mcp.Prompt
	Resources []mcp.Resource
}

// Build assembles a Manifest from live upstream lists, sorting each
// collection by identity so equivalent capability sets always canonicalize
// identically regardless of the order the upstream server returned them in.
//
// It fails closed on a capability set that advertises the same identity
// twice; see validateUniqueIdentities.
func Build(tools []mcp.Tool, prompts []mcp.Prompt, resources []mcp.Resource) (*Manifest, error) {
	m := &Manifest{
		Tools:     slices.Clone(tools),
		Prompts:   slices.Clone(prompts),
		Resources: slices.Clone(resources),
	}
	slices.SortFunc(m.Tools, func(a, b mcp.Tool) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(m.Prompts, func(a, b mcp.Prompt) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(m.Resources, func(a, b mcp.Resource) int { return strings.Compare(a.URI, b.URI) })

	if err := validateUniqueIdentities(m); err != nil {
		return nil, err
	}
	return m, nil
}

// validateUniqueIdentities rejects a capability set in which two tools share
// a name, two prompts share a name, or two resources share a URI.
//
// The MCP specification (revision 2025-11-25, "Tools") states that each tool
// is "uniquely identified by a name" and that tool names "SHOULD be unique
// within a server"; prompts and resources are identified the same way, by
// name and by URI respectively. A duplicate is therefore an upstream that is
// violating the protocol.
//
// mcp-shield fails closed on it rather than canonicalizing it away, for two
// reasons:
//
//   - The gate is keyed by identity end to end (GateDecision.SafeTools,
//     filterTools, Manifest.ToolByName, and the diff engine all map a name to
//     exactly one capability). Approving a manifest containing two different
//     tools called "x" would authorize both under one decision, and a
//     subsequent tools/call for "x" is resolved by the upstream, not by us —
//     so the operator could not know which of the two they had approved.
//     Silently deduplicating or ordering the pair would produce a stable
//     fingerprint of a capability set the gate still cannot enforce.
//   - Accepting it would let the upstream influence its own fingerprint.
//     Sorting on name alone is not a total order over a set with duplicates,
//     so the surviving order — and therefore the hash — would depend on the
//     order the server chose to advertise in.
//
// A gateway that quietly normalizes malformed capability lists is masking the
// upstream misbehavior it exists to catch. The error surfaces to the caller,
// which fails the request closed and names the offending identity.
func validateUniqueIdentities(m *Manifest) error {
	if dup, found := firstDuplicate(m.Tools, func(t mcp.Tool) string { return t.Name }); found {
		return fmt.Errorf("manifest: upstream advertised tool %q more than once; tool names must be unique within a server", dup)
	}
	if dup, found := firstDuplicate(m.Prompts, func(p mcp.Prompt) string { return p.Name }); found {
		return fmt.Errorf("manifest: upstream advertised prompt %q more than once; prompt names must be unique within a server", dup)
	}
	if dup, found := firstDuplicate(m.Resources, func(r mcp.Resource) string { return r.URI }); found {
		return fmt.Errorf("manifest: upstream advertised resource %q more than once; resource URIs must be unique within a server", dup)
	}
	return nil
}

// firstDuplicate reports the first identity that appears more than once in
// items. It makes no assumption about ordering: FromCanonicalJSON validates
// manifests that may have been tampered with in storage, where the sorted
// order Build produces can no longer be trusted.
func firstDuplicate[T any](items []T, identityOf func(T) string) (string, bool) {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		identity := identityOf(item)
		if _, duplicate := seen[identity]; duplicate {
			return identity, true
		}
		seen[identity] = struct{}{}
	}
	return "", false
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
