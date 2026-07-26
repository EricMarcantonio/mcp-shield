// Package diff compares two manifests and classifies the risk of the
// difference so a human approver can quickly judge whether a capability
// change is safe.
package diff

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
)

// riskKeywords are substrings (case-insensitive) that mark a newly added
// tool as HIGH risk. This is a deliberately blunt heuristic: it will flag
// legitimate tools like "filesystem_status" (contains "file") as HIGH too
// — that's the literal rule as specified, not a bug to be silently
// patched. A human still makes the final call in the approval workflow.
var riskKeywords = []string{
	"delete", "upload", "execute", "shell", "file", "write", "admin", "credential",
}

const (
	RiskLow    = "LOW"
	RiskMedium = "MEDIUM"
	RiskHigh   = "HIGH"
)

type ToolChange struct {
	Name               string `json:"name"`
	DescriptionChanged bool   `json:"description_changed"`
	SchemaChanged      bool   `json:"schema_changed"`
}

type PromptChange struct {
	Name               string `json:"name"`
	DescriptionChanged bool   `json:"description_changed"`
	ArgumentsChanged   bool   `json:"arguments_changed"`
}

type ResourceChange struct {
	URI                string `json:"uri"`
	DescriptionChanged bool   `json:"description_changed"`
	MimeTypeChanged    bool   `json:"mime_type_changed"`
}

// Diff is the set of differences between two manifest versions.
type Diff struct {
	AddedTools   []string     `json:"added_tools"`
	RemovedTools []string     `json:"removed_tools"`
	ChangedTools []ToolChange `json:"changed_tools"`

	AddedPrompts   []string       `json:"added_prompts"`
	RemovedPrompts []string       `json:"removed_prompts"`
	ChangedPrompts []PromptChange `json:"changed_prompts"`

	AddedResources   []string         `json:"added_resources"`
	RemovedResources []string         `json:"removed_resources"`
	ChangedResources []ResourceChange `json:"changed_resources"`
}

// Compare returns the diff from old to new. old may be nil, meaning there
// is no prior approved baseline — every tool/prompt/resource in new is
// then reported as added.
func Compare(old, new *manifest.Manifest) *Diff {
	d := &Diff{
		AddedTools: []string{}, RemovedTools: []string{}, ChangedTools: []ToolChange{},
		AddedPrompts: []string{}, RemovedPrompts: []string{}, ChangedPrompts: []PromptChange{},
		AddedResources: []string{}, RemovedResources: []string{}, ChangedResources: []ResourceChange{},
	}
	if new == nil {
		return d
	}

	var oldTools []mcpTool
	var oldPrompts []mcpPrompt
	var oldResources []mcpResource
	if old != nil {
		oldTools = toolsOf(old)
		oldPrompts = promptsOf(old)
		oldResources = resourcesOf(old)
	}

	diffTools(d, oldTools, toolsOf(new))
	diffPrompts(d, oldPrompts, promptsOf(new))
	diffResources(d, oldResources, resourcesOf(new))

	return d
}

// ClassifyRisk applies the spec's precedence rule:
//  1. HIGH  — any added tool's name substring-matches a risk keyword.
//  2. MEDIUM — else, any changed tool's input schema differs.
//  3. LOW    — else, any changed tool's description differs (or nothing
//     risk-relevant changed, e.g. only prompts/resources touched).
func ClassifyRisk(d *Diff) (risk string, reasons []string) {
	for _, name := range d.AddedTools {
		lower := strings.ToLower(name)
		for _, kw := range riskKeywords {
			if strings.Contains(lower, kw) {
				reasons = append(reasons, "added tool \""+name+"\" matches risk keyword \""+kw+"\"")
			}
		}
	}
	if len(reasons) > 0 {
		return RiskHigh, reasons
	}

	for _, tc := range d.ChangedTools {
		if tc.SchemaChanged {
			reasons = append(reasons, "tool \""+tc.Name+"\" input schema changed")
		}
	}
	if len(reasons) > 0 {
		return RiskMedium, reasons
	}

	for _, tc := range d.ChangedTools {
		if tc.DescriptionChanged {
			reasons = append(reasons, "tool \""+tc.Name+"\" description changed")
		}
	}
	if len(reasons) > 0 {
		return RiskLow, reasons
	}

	if len(d.AddedTools) > 0 || len(d.RemovedTools) > 0 {
		// Added tools with no keyword match, or removed tools: still a
		// capability change worth a human look, default to LOW.
		return RiskLow, []string{"tool set changed"}
	}

	return RiskLow, []string{"no tool-level change"}
}

// Summarize renders a Diff as short human-readable lines, e.g. "Added
// tool: upload_attachment" — used anywhere a diff needs to be shown to a
// person or included in a notification (dashboard, API, webhook) without
// duplicating this formatting in each of those callers.
func Summarize(d *Diff) []string {
	var out []string
	for _, name := range d.AddedTools {
		out = append(out, "Added tool: "+name)
	}
	for _, name := range d.RemovedTools {
		out = append(out, "Removed tool: "+name)
	}
	for _, tc := range d.ChangedTools {
		switch {
		case tc.SchemaChanged:
			out = append(out, "Schema changed: "+tc.Name)
		case tc.DescriptionChanged:
			out = append(out, "Description changed: "+tc.Name)
		}
	}
	for _, name := range d.AddedPrompts {
		out = append(out, "Added prompt: "+name)
	}
	for _, name := range d.RemovedPrompts {
		out = append(out, "Removed prompt: "+name)
	}
	for _, uri := range d.AddedResources {
		out = append(out, "Added resource: "+uri)
	}
	for _, uri := range d.RemovedResources {
		out = append(out, "Removed resource: "+uri)
	}
	return out
}

// --- internal comparison plumbing -----------------------------------------

type mcpTool struct {
	Name, Description string
	Schema            string // canonical JSON string for equality comparison
}
type mcpPrompt struct {
	Name, Description string
	ArgsJSON          string
}
type mcpResource struct {
	URI, Name, Description, MimeType string
}

func toolsOf(m *manifest.Manifest) []mcpTool {
	out := make([]mcpTool, len(m.Tools))
	for i, t := range m.Tools {
		out[i] = mcpTool{Name: t.Name, Description: t.Description, Schema: normalizeJSON(t.InputSchema)}
	}
	return out
}

func promptsOf(m *manifest.Manifest) []mcpPrompt {
	out := make([]mcpPrompt, len(m.Prompts))
	for i, p := range m.Prompts {
		b, _ := json.Marshal(p.Arguments)
		out[i] = mcpPrompt{Name: p.Name, Description: p.Description, ArgsJSON: string(b)}
	}
	return out
}

func resourcesOf(m *manifest.Manifest) []mcpResource {
	out := make([]mcpResource, len(m.Resources))
	for i, r := range m.Resources {
		out[i] = mcpResource{URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MimeType}
	}
	return out
}

func normalizeJSON(raw json.RawMessage) string {
	s, err := manifest.CanonicalizeValue(raw)
	if err != nil {
		return string(raw)
	}
	return s
}

func diffTools(d *Diff, oldTools, newTools []mcpTool) {
	oldByName := indexTools(oldTools)
	newByName := indexTools(newTools)

	for name := range newByName {
		if _, ok := oldByName[name]; !ok {
			d.AddedTools = append(d.AddedTools, name)
		}
	}
	for name := range oldByName {
		if _, ok := newByName[name]; !ok {
			d.RemovedTools = append(d.RemovedTools, name)
		}
	}
	for name, nt := range newByName {
		if ot, ok := oldByName[name]; ok {
			descChanged := ot.Description != nt.Description
			schemaChanged := ot.Schema != nt.Schema
			if descChanged || schemaChanged {
				d.ChangedTools = append(d.ChangedTools, ToolChange{
					Name: name, DescriptionChanged: descChanged, SchemaChanged: schemaChanged,
				})
			}
		}
	}

	sort.Strings(d.AddedTools)
	sort.Strings(d.RemovedTools)
	sort.Slice(d.ChangedTools, func(i, j int) bool { return d.ChangedTools[i].Name < d.ChangedTools[j].Name })
}

func indexTools(tools []mcpTool) map[string]mcpTool {
	m := make(map[string]mcpTool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}

func diffPrompts(d *Diff, oldPrompts, newPrompts []mcpPrompt) {
	oldByName := make(map[string]mcpPrompt, len(oldPrompts))
	for _, p := range oldPrompts {
		oldByName[p.Name] = p
	}
	newByName := make(map[string]mcpPrompt, len(newPrompts))
	for _, p := range newPrompts {
		newByName[p.Name] = p
	}

	for name := range newByName {
		if _, ok := oldByName[name]; !ok {
			d.AddedPrompts = append(d.AddedPrompts, name)
		}
	}
	for name := range oldByName {
		if _, ok := newByName[name]; !ok {
			d.RemovedPrompts = append(d.RemovedPrompts, name)
		}
	}
	for name, np := range newByName {
		if op, ok := oldByName[name]; ok {
			descChanged := op.Description != np.Description
			argsChanged := op.ArgsJSON != np.ArgsJSON
			if descChanged || argsChanged {
				d.ChangedPrompts = append(d.ChangedPrompts, PromptChange{
					Name: name, DescriptionChanged: descChanged, ArgumentsChanged: argsChanged,
				})
			}
		}
	}

	sort.Strings(d.AddedPrompts)
	sort.Strings(d.RemovedPrompts)
	sort.Slice(d.ChangedPrompts, func(i, j int) bool { return d.ChangedPrompts[i].Name < d.ChangedPrompts[j].Name })
}

func diffResources(d *Diff, oldResources, newResources []mcpResource) {
	oldByURI := make(map[string]mcpResource, len(oldResources))
	for _, r := range oldResources {
		oldByURI[r.URI] = r
	}
	newByURI := make(map[string]mcpResource, len(newResources))
	for _, r := range newResources {
		newByURI[r.URI] = r
	}

	for uri := range newByURI {
		if _, ok := oldByURI[uri]; !ok {
			d.AddedResources = append(d.AddedResources, uri)
		}
	}
	for uri := range oldByURI {
		if _, ok := newByURI[uri]; !ok {
			d.RemovedResources = append(d.RemovedResources, uri)
		}
	}
	for uri, nr := range newByURI {
		if or, ok := oldByURI[uri]; ok {
			descChanged := or.Description != nr.Description
			mimeChanged := or.MimeType != nr.MimeType
			if descChanged || mimeChanged {
				d.ChangedResources = append(d.ChangedResources, ResourceChange{
					URI: uri, DescriptionChanged: descChanged, MimeTypeChanged: mimeChanged,
				})
			}
		}
	}

	sort.Strings(d.AddedResources)
	sort.Strings(d.RemovedResources)
	sort.Slice(d.ChangedResources, func(i, j int) bool { return d.ChangedResources[i].URI < d.ChangedResources[j].URI })
}
