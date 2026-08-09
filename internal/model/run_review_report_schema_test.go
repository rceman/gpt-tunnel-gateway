package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReviewReportFindingAndScopeBoundsAndSeverity(t *testing.T) {
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		report := reviewReportParityFixture()
		report.Findings[0].Severity = severity
		if err := ValidateRunReviewReport(report); err != nil {
			t.Fatalf("severity %q rejected: %v", severity, err)
		}
	}
	invalid := reviewReportParityFixture()
	invalid.Findings[0].Severity = "warning"
	if err := ValidateRunReviewReport(invalid); err == nil {
		t.Fatal("invalid finding severity accepted")
	}
	for _, status := range []string{"covered", "inspected_no_change", "blocked"} {
		report := reviewReportParityFixture()
		report.ScopeCoverage[0].Status = status
		if err := ValidateRunReviewReport(report); err != nil {
			t.Fatalf("scope status %q rejected: %v", status, err)
		}
	}
	invalidScope := reviewReportParityFixture()
	invalidScope.ScopeCoverage[0].Status = "partial"
	if err := ValidateRunReviewReport(invalidScope); err == nil {
		t.Fatal("invalid scope status accepted")
	}
	for name, value := range map[string]string{
		"finding title at max":  strings.Repeat("t", 512),
		"finding detail at max": strings.Repeat("d", 20000),
		"scope surface at max":  strings.Repeat("s", 512),
		"scope detail at max":   strings.Repeat("d", 20000),
		"string entry at max":   strings.Repeat("e", 20000),
	} {
		report := reviewReportParityFixture()
		switch name {
		case "finding title at max":
			report.Findings[0].Title = value
		case "finding detail at max":
			report.Findings[0].Detail = value
		case "scope surface at max":
			report.ScopeCoverage[0].Surface = value
		case "scope detail at max":
			report.ScopeCoverage[0].Detail = value
		case "string entry at max":
			report.UnexpectedSurfaces = []string{value}
		}
		if err := ValidateRunReviewReport(report); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
		report = reviewReportParityFixture()
		switch name {
		case "finding title at max":
			report.Findings[0].Title = value + "x"
		case "finding detail at max":
			report.Findings[0].Detail = value + "x"
		case "scope surface at max":
			report.ScopeCoverage[0].Surface = value + "x"
		case "scope detail at max":
			report.ScopeCoverage[0].Detail = value + "x"
		case "string entry at max":
			report.UnexpectedSurfaces = []string{value + "x"}
		}
		if err := ValidateRunReviewReport(report); err == nil {
			t.Fatalf("%s over-limit value accepted", name)
		}
	}
	for _, field := range []string{"unexpected_surfaces", "historical_compatibility", "prohibited_actions"} {
		report := reviewReportParityFixture()
		value := strings.Repeat("x", MaxReviewStringEntryCodePoints)
		switch field {
		case "unexpected_surfaces":
			report.UnexpectedSurfaces = []string{value}
		case "historical_compatibility":
			report.HistoricalCompatibility = []string{value}
		case "prohibited_actions":
			report.ProhibitedActions = []string{value}
		}
		if err := ValidateRunReviewReport(report); err != nil {
			t.Fatalf("%s max entry rejected: %v", field, err)
		}
	}
}

func TestRunReviewReportSchemasMatchBoundedModelContract(t *testing.T) {
	for _, filename := range []string{"run-review-report.schema.json", "run-review-report-draft.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", filename))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := strictJSONObject(data); err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		defs := schema["$defs"].(map[string]any)
		resolve := func(value any) map[string]any {
			object := value.(map[string]any)
			if ref, ok := object["$ref"].(string); ok {
				return defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
			}
			return object
		}
		properties := schema["properties"].(map[string]any)
		findings := resolve(properties["findings"])
		finding := resolve(findings["items"])
		severity := finding["properties"].(map[string]any)["severity"].(map[string]any)
		got, ok := severity["enum"].([]any)
		if !ok || len(got) != 5 {
			t.Fatalf("%s severity enum = %#v", filename, severity["enum"])
		}
		for i, want := range []any{"critical", "high", "medium", "low", "info"} {
			if got[i] != want {
				t.Fatalf("%s severity enum = %#v", filename, got)
			}
		}
		for field, max := range map[string]float64{"id": 64, "title": 512, "detail": 20000} {
			if got := finding["properties"].(map[string]any)[field].(map[string]any)["maxLength"].(float64); got != max {
				t.Fatalf("%s finding %s maxLength = %v, want %v", filename, field, got, max)
			}
		}
		coverage := resolve(properties["scope_coverage"])
		coverageItem := resolve(coverage["items"])
		coverageProperties := coverageItem["properties"].(map[string]any)
		for i, want := range []any{"covered", "inspected_no_change", "blocked"} {
			statuses := coverageProperties["status"].(map[string]any)["enum"].([]any)
			if statuses[i] != want {
				t.Fatalf("%s scope status enum = %#v", filename, statuses)
			}
		}
		if got := coverageProperties["surface"].(map[string]any)["maxLength"].(float64); got != 512 {
			t.Fatalf("%s scope surface maxLength = %v", filename, got)
		}
		if got := coverageProperties["detail"].(map[string]any)["maxLength"].(float64); got != 20000 {
			t.Fatalf("%s scope detail maxLength = %v", filename, got)
		}
		for _, field := range []string{"unexpected_surfaces", "historical_compatibility", "prohibited_actions"} {
			item := resolve(properties[field])["items"].(map[string]any)
			item = resolve(item)
			if item["minLength"].(float64) != 1 || item["maxLength"].(float64) != 20000 {
				t.Fatalf("%s %s item bounds are not closed to the model contract", filename, field)
			}
		}
	}
}
