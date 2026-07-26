package diff

import (
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

func TestClassifyRiskKeywordMatrix(t *testing.T) {
	cases := []struct {
		name      string
		addedTool string
		want      string
	}{
		{"delete", "delete_calendar", RiskHigh},
		{"upload", "upload_attachment", RiskHigh},
		{"execute", "execute_command", RiskHigh},
		{"shell", "run_shell", RiskHigh},
		{"file", "read_file", RiskHigh},
		{"write", "write_data", RiskHigh},
		{"admin", "admin_reset", RiskHigh},
		{"credential", "get_credential", RiskHigh},
		{"benign", "calendar_read", RiskLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Diff{AddedTools: []string{tc.addedTool}}
			risk, _ := ClassifyRisk(d)
			if risk != tc.want {
				t.Fatalf("tool %q: expected risk %s, got %s", tc.addedTool, tc.want, risk)
			}
		})
	}
}

func TestClassifyRiskPrecedenceHighBeatsSchemaAndDescription(t *testing.T) {
	d := &Diff{
		AddedTools:   []string{"upload_x"},
		ChangedTools: []ToolChange{{Name: "y", SchemaChanged: true}},
	}
	risk, _ := ClassifyRisk(d)
	if risk != RiskHigh {
		t.Fatalf("expected HIGH to win over MEDIUM, got %s", risk)
	}
}

func TestClassifyRiskSchemaChangeIsMedium(t *testing.T) {
	d := &Diff{ChangedTools: []ToolChange{{Name: "a", SchemaChanged: true}}}
	risk, _ := ClassifyRisk(d)
	if risk != RiskMedium {
		t.Fatalf("expected MEDIUM, got %s", risk)
	}
}

func TestClassifyRiskDescriptionOnlyIsLow(t *testing.T) {
	d := &Diff{ChangedTools: []ToolChange{{Name: "a", DescriptionChanged: true}}}
	risk, _ := ClassifyRisk(d)
	if risk != RiskLow {
		t.Fatalf("expected LOW, got %s", risk)
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

// TestSummarizeOmitsChangedPromptsAndResources pins a known bug (design doc
// finding S3, internal/diff/diff.go:140-169): Summarize renders lines for
// added/removed tools, changed tools, and added/removed prompts/resources,
// but never iterates d.ChangedPrompts or d.ChangedResources at all. A
// prompt-argument change — a real injection vector, since prompt arguments
// feed straight into the model — currently produces an empty "changes" list
// anywhere Summarize's output is shown: the dashboard's pending-manifest
// table, the JSON pending-manifests API, and any future notification that
// reuses this seam (see design doc Option A1). Not fixed here — Phase 5
// owns completing Summarize before that seam is reused for notifications.
func TestSummarizeOmitsChangedPromptsAndResources(t *testing.T) {
	d := &Diff{
		ChangedPrompts:   []PromptChange{{Name: "greeting", ArgumentsChanged: true}},
		ChangedResources: []ResourceChange{{URI: "file:///report.csv", MimeTypeChanged: true}},
	}

	lines := Summarize(d)

	if len(lines) != 0 {
		t.Fatalf("known-bug pin broke: expected Summarize to still silently drop changed "+
			"prompts/resources (0 lines), got %v — if Summarize was fixed to include them, "+
			"update this test to assert the new (correct) lines instead", lines)
	}
}

func TestClassifyRiskKnownFalsePositiveSubstring(t *testing.T) {
	// Documented, intentional edge case: "filesystem_status" contains
	// "file" and trips HIGH under the literal substring rule, even
	// though the tool itself isn't inherently dangerous. Not silently
	// patched — see package doc comment on riskKeywords.
	d := &Diff{AddedTools: []string{"filesystem_status"}}
	risk, _ := ClassifyRisk(d)
	if risk != RiskHigh {
		t.Fatalf("expected documented false positive to still classify HIGH, got %s", risk)
	}
}
