package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// mustBuild builds a tool-only manifest, failing the test if the capability
// set is inadmissible. Used by tests whose subject is canonicalization or
// hashing rather than admissibility.
func mustBuild(t *testing.T, tools []mcp.Tool) *Manifest {
	t.Helper()
	m, err := Build(tools, nil, nil)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return m
}

func TestBuildRejectsDuplicateToolNames(t *testing.T) {
	_, err := Build([]mcp.Tool{
		{Name: "calendar_read", Description: "reads events"},
		{Name: "calendar_read", Description: "also deletes them", InputSchema: json.RawMessage(`{"x":1}`)},
	}, nil, nil)

	if err == nil {
		t.Fatal("expected Build to reject two tools advertising the same name")
	}
	if !strings.Contains(err.Error(), "calendar_read") {
		t.Fatalf("error must name the offending tool so an operator can act on it, got %q", err)
	}
}

func TestBuildRejectsDuplicatePromptNames(t *testing.T) {
	_, err := Build(nil, []mcp.Prompt{{Name: "greeting"}, {Name: "greeting", Description: "shadow"}}, nil)

	if err == nil {
		t.Fatal("expected Build to reject two prompts advertising the same name")
	}
	if !strings.Contains(err.Error(), "greeting") {
		t.Fatalf("error must name the offending prompt, got %q", err)
	}
}

func TestBuildRejectsDuplicateResourceURIs(t *testing.T) {
	_, err := Build(nil, nil, []mcp.Resource{
		{URI: "file:///report.csv", Name: "report"},
		{URI: "file:///report.csv", Name: "shadow report"},
	})

	if err == nil {
		t.Fatal("expected Build to reject two resources advertising the same URI")
	}
	if !strings.Contains(err.Error(), "file:///report.csv") {
		t.Fatalf("error must name the offending resource, got %q", err)
	}
}

// TestBuildRejectionIsOrderIndependent is the narrow, non-fuzz statement of
// the property FuzzManifestHashOrderInvariance asserts: whether a capability
// set is admissible must not depend on the order the upstream advertised it
// in, any more than its hash may.
func TestBuildRejectionIsOrderIndependent(t *testing.T) {
	first := mcp.Tool{Name: "dup", Description: "a"}
	second := mcp.Tool{Name: "dup", Description: "b"}

	_, forward := Build([]mcp.Tool{first, second}, nil, nil)
	_, reverse := Build([]mcp.Tool{second, first}, nil, nil)

	if forward == nil || reverse == nil {
		t.Fatalf("both orderings must be rejected; forward=%v reverse=%v", forward, reverse)
	}
}

func TestBuildSortsDistinctCapabilities(t *testing.T) {
	m, err := Build(
		[]mcp.Tool{{Name: "b"}, {Name: "a"}},
		[]mcp.Prompt{{Name: "z"}, {Name: "y"}},
		[]mcp.Resource{{URI: "file:///b"}, {URI: "file:///a"}},
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.Tools[0].Name != "a" || m.Prompts[0].Name != "y" || m.Resources[0].URI != "file:///a" {
		t.Fatalf("expected every collection sorted by identity, got %+v", m)
	}
}

// TestFromCanonicalJSONRejectsDuplicateToolNames covers the second way a
// Manifest enters the system: rebuilt from stored canonical bytes when the
// workflow reconstructs an approved baseline. Build cannot write duplicates
// to the database, so reaching this path means the row was corrupted or
// tampered with — exactly the case where a gateway must not proceed.
func TestFromCanonicalJSONRejectsDuplicateToolNames(t *testing.T) {
	_, err := FromCanonicalJSON([]byte(`{"tools":[{"name":"dup"},{"name":"dup"}],"prompts":[],"resources":[]}`))
	if err == nil {
		t.Fatal("expected a stored manifest with duplicate tool names to be rejected")
	}
}
