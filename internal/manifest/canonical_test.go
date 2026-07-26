package manifest

import (
	"encoding/json"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

func TestCanonicalizeDeterministicUnderReorder(t *testing.T) {
	a := mustBuild(t, []mcp.Tool{
		{Name: "b_tool", Description: "second", InputSchema: json.RawMessage(`{"z":1,"a":2}`)},
		{Name: "a_tool", Description: "first", InputSchema: json.RawMessage(`{"required":["y","x"]}`)},
	})
	b := mustBuild(t, []mcp.Tool{
		{Name: "a_tool", Description: "first", InputSchema: json.RawMessage(`{"required":["x","y"]}`)},
		{Name: "b_tool", Description: "second", InputSchema: json.RawMessage(`{"a":2,"z":1}`)},
	})

	ca, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("canonicalize a: %v", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("canonicalize b: %v", err)
	}

	if string(ca) != string(cb) {
		t.Fatalf("expected identical canonical output regardless of input order:\na=%s\nb=%s", ca, cb)
	}
	if Hash(ca) != Hash(cb) {
		t.Fatalf("expected identical hash regardless of input order")
	}
}

func TestCanonicalizeObjectArrayPreservesOrder(t *testing.T) {
	// anyOf-style array of objects must not be reordered, since there is
	// no well-defined canonical order and reordering could change schema
	// semantics for some validators.
	m := mustBuild(t, []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{"anyOf":[{"type":"string"},{"type":"number"}]}`)},
	})
	out, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tools := decoded["tools"].([]any)
	tool := tools[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	anyOf := schema["anyOf"].([]any)
	first := anyOf[0].(map[string]any)
	if first["type"] != "string" {
		t.Fatalf("expected anyOf order preserved (string first), got %v", anyOf)
	}
}

func TestCanonicalizePrimitiveArraySorted(t *testing.T) {
	m := mustBuild(t, []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{"required":["z","a","m"]}`)}})
	out, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	tools := decoded["tools"].([]any)
	tool := tools[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	required := schema["required"].([]any)
	if required[0] != "a" || required[1] != "m" || required[2] != "z" {
		t.Fatalf("expected sorted required array, got %v", required)
	}
}

func TestCanonicalizeNoWhitespace(t *testing.T) {
	m := mustBuild(t, []mcp.Tool{{Name: "t"}})
	out, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	for _, b := range out {
		if b == ' ' || b == '\n' || b == '\t' {
			t.Fatalf("expected whitespace-free output, got: %s", out)
		}
	}
}

func TestHashFromCanonicalJSONRoundTrip(t *testing.T) {
	m := mustBuild(t, []mcp.Tool{{Name: "t", Description: "d"}})
	canonical, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	restored, err := FromCanonicalJSON(canonical)
	if err != nil {
		t.Fatalf("from canonical json: %v", err)
	}
	canonical2, err := Canonicalize(restored)
	if err != nil {
		t.Fatalf("canonicalize restored: %v", err)
	}
	if Hash(canonical) != Hash(canonical2) {
		t.Fatalf("expected round-tripped manifest to hash identically")
	}
}
