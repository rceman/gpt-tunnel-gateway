package hub

import "fmt"

// TrainAttemptReportPath identifies the historical Hub Attempt report.
func TrainAttemptReportPath(projectID, trainID string, position int, attempt uint64) string {
	return fmt.Sprintf("%s/projects/%s/train-attempts/%s/item-%d/attempt-%d/report.json", ProtocolRoot, projectID, trainID, position, attempt)
}

// TrainAttemptReviewPath identifies the historical Hub review record. Local
// Shared evidence paths are owned by internal/persistence instead.
func TrainAttemptReviewPath(projectID, trainID string, position int, attempt uint64) string {
	return fmt.Sprintf("%s/projects/%s/train-attempts/%s/item-%d/attempt-%d/review.json", ProtocolRoot, projectID, trainID, position, attempt)
}
