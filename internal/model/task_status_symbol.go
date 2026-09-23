package model

import "fmt"

func TaskStatusSymbol(status string) (string, error) {
	switch status {
	case TaskAuthoringPlanned, TaskAuthoringReady:
		return "○", nil
	case TaskExecutionDispatched, TaskExecutionInProgress:
		return "▶", nil
	case TaskExecutionAwaitingReview:
		return "◐", nil
	case TaskExecutionReadyForVerification, TaskExecutionVerifying, TaskExecutionVerified, TaskExecutionIntegrating, TaskExecutionIntegrated:
		return "◇", nil
	case TaskExecutionBlocked:
		return "⊘", nil
	case TaskAuthoringDone:
		return "●", nil
	case TaskExecutionChangesRequested, TaskExecutionFailed, "rejected":
		return "×", nil
	case "deferred", "paused":
		return "◌", nil
	default:
		return "", fmt.Errorf("invalid Task status %q", status)
	}
}
