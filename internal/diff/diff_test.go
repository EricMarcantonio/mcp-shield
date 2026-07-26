package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

func tool(name, desc string) mcp.Tool { return mcp.Tool{Name: name, Description: desc} }

// mustBuild builds a tool-only manifest, failing the test if the capability
// set is inadmissible. Admissibility is manifest.Build's concern and is
// tested there; these tests are about diffing valid manifests.
func mustBuild(t *testing.T, tools []mcp.Tool) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Build(tools, nil, nil)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	return m
}

func TestCompareAddedRemovedChanged(t *testing.T) {
	old := mustBuild(t, []mcp.Tool{tool("a", "desc a"), tool("b", "desc b")})
	newM := mustBuild(t, []mcp.Tool{tool("a", "desc a v2"), tool("c", "desc c")})

	d := Compare(old, newM)

	if len(d.AddedTools) != 1 || d.AddedTools[0] != "c" {
		t.Fatalf("expected added=[c], got %v", d.AddedTools)
	}
	if len(d.RemovedTools) != 1 || d.RemovedTools[0] != "b" {
		t.Fatalf("expected removed=[b], got %v", d.RemovedTools)
	}
	if len(d.ChangedTools) != 1 || d.ChangedTools[0].Name != "a" || !d.ChangedTools[0].DescriptionChanged {
		t.Fatalf("expected changed=[a] (description), got %v", d.ChangedTools)
	}
}

func TestCompareNilBaselineTreatsAllAsAdded(t *testing.T) {
	newM := mustBuild(t, []mcp.Tool{tool("calendar_read", ""), tool("calendar_create", "")})
	d := Compare(nil, newM)
	if len(d.AddedTools) != 2 {
		t.Fatalf("expected 2 added tools on nil baseline, got %v", d.AddedTools)
	}
}

func TestCompareIgnoresCanonicalizationArtifacts(t *testing.T) {
	// Regression test: a manifest's stored baseline has been through
	// Canonicalize (which sorts primitive arrays like a schema's
	// "required" list), then FromCanonicalJSON to rebuild it for
	// diffing. A freshly fetched live manifest has its schema in
	// whatever order the upstream server returned it in. The same
	// logical schema must not be reported as "changed" just because one
	// side went through canonicalization and the other didn't.
	schemaSortedOrder := `{"type":"object","required":["data","eventId"]}`
	schemaNaturalOrder := `{"type":"object","required":["eventId","data"]}`

	baselineRaw := mustBuild(t, []mcp.Tool{{Name: "upload_attachment", InputSchema: []byte(schemaSortedOrder)}})
	canonical, err := manifest.Canonicalize(baselineRaw)
	if err != nil {
		t.Fatalf("canonicalize baseline: %v", err)
	}
	baseline, err := manifest.FromCanonicalJSON(canonical)
	if err != nil {
		t.Fatalf("from canonical json: %v", err)
	}

	live := mustBuild(t, []mcp.Tool{{Name: "upload_attachment", InputSchema: []byte(schemaNaturalOrder)}})

	d := Compare(baseline, live)
	if len(d.ChangedTools) != 0 {
		t.Fatalf("expected no changed tools for a schema differing only in array order, got %+v", d.ChangedTools)
	}
}

// TestSummarizeCoversChangedPromptsAndResources guards the fix for design
// doc finding S3. Summarize rendered lines for added/removed tools, changed
// tools, and added/removed prompts/resources, but never iterated
// d.ChangedPrompts or d.ChangedResources. A prompt-argument change — a real
// injection vector, since prompt arguments feed straight into the model —
// produced an empty change list everywhere Summarize's output is shown, so a
// human could approve a diff they could not see.
func TestSummarizeCoversChangedPromptsAndResources(t *testing.T) {
	d := &Diff{
		ChangedPrompts: []PromptChange{
			{Name: "greeting", ArgumentsChanged: true},
			{Name: "farewell", DescriptionChanged: true},
		},
		ChangedResources: []ResourceChange{
			{URI: "file:///report.csv", MimeTypeChanged: true},
			{URI: "file:///notes.txt", DescriptionChanged: true},
		},
	}

	lines := Summarize(d)

	want := []string{
		"Arguments changed: prompt greeting",
		"Description changed: prompt farewell",
		"MIME type changed: resource file:///report.csv",
		"Description changed: resource file:///notes.txt",
	}
	if len(lines) != len(want) {
		t.Fatalf("expected %d summary lines, got %d: %v", len(want), len(lines), lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d: got %q, want %q (all: %v)", i, lines[i], w, lines)
		}
	}
}

// TestSummarizeCoversEveryDiffField is the structural guard: any field added
// to Diff in future must show up in Summarize, or an approver silently stops
// seeing that class of change.
func TestSummarizeCoversEveryDiffField(t *testing.T) {
	d := &Diff{
		AddedTools:       []string{"t_added"},
		RemovedTools:     []string{"t_removed"},
		ChangedTools:     []ToolChange{{Name: "t_changed", SchemaChanged: true}},
		AddedPrompts:     []string{"p_added"},
		RemovedPrompts:   []string{"p_removed"},
		ChangedPrompts:   []PromptChange{{Name: "p_changed", ArgumentsChanged: true}},
		AddedResources:   []string{"r_added"},
		RemovedResources: []string{"r_removed"},
		ChangedResources: []ResourceChange{{URI: "r_changed", MimeTypeChanged: true}},
	}

	lines := Summarize(d)

	joined := strings.Join(lines, "\n")
	for _, marker := range []string{
		"t_added", "t_removed", "t_changed",
		"p_added", "p_removed", "p_changed",
		"r_added", "r_removed", "r_changed",
	} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("Summarize dropped %q; an approver would never see that change:\n%s", marker, joined)
		}
	}
	if got := reflect.TypeOf(Diff{}).NumField(); got != 9 {
		t.Fatalf("Diff has %d fields but this test only covers 9 — add the new field to Summarize and here", got)
	}
}
