package manifest

import (
	"encoding/json"
	"fmt"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

type canonicalDoc struct {
	Tools     []canonicalTool     `json:"tools"`
	Prompts   []canonicalPrompt   `json:"prompts"`
	Resources []canonicalResource `json:"resources"`
}

type canonicalTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type canonicalPrompt struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Arguments   []mcp.PromptArgument `json:"arguments"`
}

type canonicalResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mime_type"`
}

// FromCanonicalJSON reconstructs a Manifest from bytes previously produced
// by Canonicalize (as stored in manifests.canonical_json). Used to rebuild
// the approved baseline manifest for diffing against a newly observed one.
func FromCanonicalJSON(b []byte) (*Manifest, error) {
	var doc canonicalDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("manifest: decode canonical json: %w", err)
	}

	m := &Manifest{
		Tools:     make([]mcp.Tool, len(doc.Tools)),
		Prompts:   make([]mcp.Prompt, len(doc.Prompts)),
		Resources: make([]mcp.Resource, len(doc.Resources)),
	}
	for i, t := range doc.Tools {
		m.Tools[i] = mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	for i, p := range doc.Prompts {
		m.Prompts[i] = mcp.Prompt{Name: p.Name, Description: p.Description, Arguments: p.Arguments}
	}
	for i, r := range doc.Resources {
		m.Resources[i] = mcp.Resource{URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MimeType}
	}
	return m, nil
}
