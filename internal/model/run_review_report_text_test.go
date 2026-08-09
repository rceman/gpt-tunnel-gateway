package model

import (
	"strings"
	"testing"
)

func TestRunReviewReportTextBoundsUseUnicodeCodePoints(t *testing.T) {
	tests := []struct {
		name string
		max  int
		set  func(*RunReviewReport, string)
	}{
		{"finding title", 512, func(v *RunReviewReport, text string) { v.Findings[0].Title = text }},
		{"finding detail", 20000, func(v *RunReviewReport, text string) { v.Findings[0].Detail = text }},
		{"scope surface", 512, func(v *RunReviewReport, text string) { v.ScopeCoverage[0].Surface = text }},
		{"scope detail", 20000, func(v *RunReviewReport, text string) { v.ScopeCoverage[0].Detail = text }},
		{"unexpected surfaces", 20000, func(v *RunReviewReport, text string) { v.UnexpectedSurfaces = []string{text} }},
		{"historical compatibility", 20000, func(v *RunReviewReport, text string) { v.HistoricalCompatibility = []string{text} }},
		{"prohibited actions", 20000, func(v *RunReviewReport, text string) { v.ProhibitedActions = []string{text} }},
		{"next action", 20000, func(v *RunReviewReport, text string) { v.NextAction = text }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := reviewReportParityFixture()
			test.set(&report, strings.Repeat("🙂", test.max))
			if err := ValidateRunReviewReport(report); err != nil {
				t.Fatalf("exact Unicode code-point maximum rejected: %v", err)
			}
			test.set(&report, strings.Repeat("🙂", test.max+1))
			if err := ValidateRunReviewReport(report); err == nil {
				t.Fatal("Unicode code-point maximum+1 accepted")
			}
			test.set(&report, "\x80")
			if err := ValidateRunReviewReport(report); err == nil {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
}
