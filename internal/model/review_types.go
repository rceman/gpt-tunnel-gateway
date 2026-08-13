package model

import "fmt"

const (
	ReviewOutcomeAccepted           = "accepted"
	ReviewOutcomeRejected           = "rejected"
	ReviewOutcomeBlocked            = "blocked"
	ReviewOutcomeInconclusive       = "inconclusive"
	ReviewOutcomeAcceptedActionable = "accepted_with_actionable_finding"
	ReviewOutcomeRejectedCorrection = "rejected_needs_correction"
)

type ReviewFinding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

type ReviewScopeCoverage struct {
	Surface string `json:"surface"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
}

func ValidateReviewOutcome(value string) error {
	switch value {
	case "accepted", "rejected", "blocked", "inconclusive", "accepted_with_actionable_finding", "rejected_needs_correction":
		return nil
	default:
		return fmt.Errorf("invalid review outcome")
	}
}
