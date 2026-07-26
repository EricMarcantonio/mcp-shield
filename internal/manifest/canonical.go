package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// Canonicalize renders a Manifest as deterministic, whitespace-free JSON:
// object keys are sorted recursively, and arrays whose elements are all
// JSON primitives (string/number/bool/null) are sorted too. Arrays that
// contain any object or nested array are canonicalized element-wise but
// keep their original order, since reordering something like a JSON
// Schema `anyOf`/`oneOf` array has no well-defined canonical order and can
// even change validation semantics.
//
// The same canonical bytes are always produced for the same capability
// set regardless of the order the upstream server returned tools/prompts/
// resources in, or the key order of nested schema objects — this is what
// makes Hash(Canonicalize(m)) a stable identity for a manifest.
func Canonicalize(m *Manifest) ([]byte, error) {
	raw, err := json.Marshal(struct {
		Tools     []toolDoc     `json:"tools"`
		Prompts   []promptDoc   `json:"prompts"`
		Resources []resourceDoc `json:"resources"`
	}{
		Tools:     toToolDocs(m.Tools),
		Prompts:   toPromptDocs(m.Prompts),
		Resources: toResourceDocs(m.Resources),
	})
	if err != nil {
		return nil, fmt.Errorf("canonicalize: marshal: %w", err)
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("canonicalize: unmarshal: %w", err)
	}

	canonical := canonicalizeValue(v)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonical); err != nil {
		return nil, fmt.Errorf("canonicalize: encode: %w", err)
	}
	// json.Encoder.Encode appends a trailing newline; strip it so the
	// output is truly whitespace-free.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// canonicalizeValue recursively normalizes a decoded JSON value:
//   - objects (map[string]any) get their keys sorted, by encoding/json's
//     map marshaling.
//   - arrays of only-primitive elements are sorted.
//   - arrays containing any object/array element are canonicalized
//     element-wise but keep their original order.
func canonicalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			out[k] = canonicalizeValue(child)
		}
		// encoding/json marshals map[string]any keys in sorted order;
		// TestCanonicalizeValueSortsObjectKeys pins that invariant, which the
		// manifest hash depends on.
		return out
	case []any:
		canon := make([]any, len(val))
		allPrimitive := true
		for i, elem := range val {
			canon[i] = canonicalizeValue(elem)
			if !isPrimitive(elem) {
				allPrimitive = false
			}
		}
		if allPrimitive {
			sort.Slice(canon, func(i, j int) bool {
				return fmt.Sprint(canon[i]) < fmt.Sprint(canon[j])
			})
		}
		return canon
	default:
		return val
	}
}

// CanonicalizeValue normalizes an arbitrary JSON blob (e.g. a tool's
// input_schema) using the same key/array canonicalization rules as
// Canonicalize, returning a compact deterministic string. This is exported
// so callers that need to compare two JSON values for semantic equality
// (the diff engine comparing a stored, already-canonical baseline schema
// against a freshly fetched live one) normalize both sides identically —
// otherwise a schema whose primitive arrays got sorted once at storage
// time would spuriously appear "changed" against an unsorted live fetch of
// the exact same schema.
func CanonicalizeValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("canonicalize value: %w", err)
	}
	b, err := json.Marshal(canonicalizeValue(v))
	if err != nil {
		return "", fmt.Errorf("canonicalize value: %w", err)
	}
	return string(b), nil
}

func isPrimitive(v any) bool {
	switch v.(type) {
	case string, float64, bool, nil:
		return true
	default:
		return false
	}
}

type toolDoc struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

type promptDoc struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Arguments   any    `json:"arguments,omitempty"`
}

type resourceDoc struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

func toToolDocs(tools []mcp.Tool) []toolDoc {
	docs := make([]toolDoc, len(tools))
	for i, t := range tools {
		var schema any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		docs[i] = toolDoc{Name: t.Name, Description: t.Description, InputSchema: schema}
	}
	return docs
}

func toPromptDocs(prompts []mcp.Prompt) []promptDoc {
	docs := make([]promptDoc, len(prompts))
	for i, p := range prompts {
		docs[i] = promptDoc{Name: p.Name, Description: p.Description, Arguments: p.Arguments}
	}
	return docs
}

func toResourceDocs(resources []mcp.Resource) []resourceDoc {
	docs := make([]resourceDoc, len(resources))
	for i, r := range resources {
		docs[i] = resourceDoc{URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MimeType}
	}
	return docs
}
