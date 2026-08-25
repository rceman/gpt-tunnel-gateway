package hub

import "fmt"

// TrainAttemptReviewPath identifies the historical Hub review record. Local
// Shared evidence paths are owned by internal/persistence instead.
func TrainAttemptReviewPath(projectID, trainID string, position int, attempt uint64) string {
	return fmt.Sprintf("%s/projects/%s/train-attempts/%s/item-%d/attempt-%d/review.json", ProtocolRoot, projectID, trainID, position, attempt)
}
