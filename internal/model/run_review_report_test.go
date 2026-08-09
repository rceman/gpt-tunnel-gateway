package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReviewReportDraftSchemaUsesLocalClosedDefinitions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "run-review-report-draft.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"repository_state", "gates", "findings", "scope_coverage"} {
		property := properties[name].(map[string]any)
		ref, ok := property["$ref"].(string)
		if !ok || ref != "#/$defs/"+name {
			t.Fatalf("%s does not use a local definition: %#v", name, property)
		}
	}
	defs := schema["$defs"].(map[string]any)
	for _, name := range []string{"findings", "scope_coverage"} {
		definition := defs[name].(map[string]any)
		items := definition["items"].(map[string]any)
		if items["additionalProperties"] != false {
			t.Fatalf("%s item schema is not closed", name)
		}
	}
}

func reviewReportParityFixture() RunReviewReport {
	return RunReviewReport{
		SchemaVersion: RunReviewReportSchemaVersion,
		ID:            NewRunReviewReportID("EXM-TSK1-RUN1"),
		TaskID:        "EXM-TSK1",
		RunID:         "EXM-TSK1-RUN1",
		ProjectID:     "example",
		TaskSHA256:    strings.Repeat("a", 64),
		Branch:        "task/EXM-TSK1-review",
		BaseRevision:  strings.Repeat("b", 40),
		ReviewedHead:  strings.Repeat("c", 40),
		Outcome:       ReviewOutcomeAccepted,
		RepositoryState: ReviewRepositoryState{
			Branch:        "task/EXM-TSK1-review",
			BaseRevision:  strings.Repeat("b", 40),
			ReviewedHead:  strings.Repeat("c", 40),
			WorktreeClean: true,
			BaseAncestor:  true,
		},
		Gates:                   []CompletionGateResult{},
		Findings:                []ReviewFinding{{ID: "F1", Severity: "info", Title: "title", Detail: "detail"}},
		ScopeCoverage:           []ReviewScopeCoverage{{Surface: "surface", Status: "covered", Detail: "detail"}},
		ChangedFiles:            []string{},
		UnexpectedSurfaces:      []string{},
		HistoricalCompatibility: []string{},
		ProhibitedActions:       []string{},
		NextAction:              "next",
		FinishedAt:              time.Now().UTC(),
	}
}
