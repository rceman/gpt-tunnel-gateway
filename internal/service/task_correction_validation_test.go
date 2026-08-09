package service

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCorrectionEligibleReportRejectsCleanAcceptedAndInvalidOutcomes(t *testing.T) {
	cleanAccepted := model.RunReviewReport{Outcome: model.ReviewOutcomeAccepted}
	if correctionEligibleReport(cleanAccepted) {
		t.Fatal("clean accepted report unexpectedly became correction-eligible")
	}
	for _, outcome := range []string{model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive, "unknown"} {
		if correctionEligibleReport(model.RunReviewReport{Outcome: outcome}) {
			t.Fatalf("outcome %q unexpectedly became correction-eligible", outcome)
		}
	}
}
