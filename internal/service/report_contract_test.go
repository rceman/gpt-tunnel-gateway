package service

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCanonicalReportUsesArraysInsteadOfNull(t *testing.T) {
	report := canonicalReport(model.Report{})
	if report.Commits == nil || report.ChangedFiles == nil || report.Commands == nil || report.Deviations == nil || report.RemainingRisks == nil {
		t.Fatalf("report contains nil collection: %#v", report)
	}
}
