package service

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCanonicalReportUsesArraysInsteadOfNull(t *testing.T) {
	report := canonicalReport(model.Report{})
	if report.GateResults == nil || report.AcceptanceCoverage == nil || report.Deviations == nil || report.RemainingRisks == nil || report.Repository.Commits == nil || report.Repository.ChangedFiles == nil {
		t.Fatalf("report contains nil collection: %#v", report)
	}
}
