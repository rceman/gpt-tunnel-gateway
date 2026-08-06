package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunReviewReportIdentityIsDeterministic(t *testing.T) {
	if got, want := NewRunReviewReportID("run-123"), "run-123-REPORT"; got != want {
		t.Fatalf("report identity = %q, want %q", got, want)
	}
	if got, want := NewRunReviewReportID("run-123"), NewRunReviewReportID("run-123"); got != want {
		t.Fatalf("report identity is not deterministic: %q != %q", got, want)
	}
}

func TestRunReviewReportParserRejectsUnknownOutcomeAndFields(t *testing.T) {
	if _, err := ParseRunReviewReport([]byte(`{"schema_version":1,"id":"run-123-REPORT","outcome":"not-a-valid-outcome"}`)); err == nil {
		t.Fatal("unknown review outcome accepted")
	}
	if _, err := ParseRunReviewReport([]byte(`{"schema_version":1,"id":"run-123-REPORT","unknown":true}`)); err == nil {
		t.Fatal("unknown review-report field accepted")
	}
}

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
