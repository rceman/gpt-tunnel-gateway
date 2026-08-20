package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	trainV2ClassTerminal      = "terminal"
	trainV2ClassPlanned       = "planned_idle"
	trainV2ClassLiveAttempt   = "live_attempt"
	trainV2ClassLiveOperation = "live_operation"
	trainV2ClassIntegration   = "integration_pending"
	trainV2ClassCorrection    = "correction_pending"
	trainV2ClassStale         = "stale"
	trainV2ClassAmbiguous     = "ambiguous"
	trainV2ClassRetired       = "retired"
)

type trainV2LifecycleClassification struct {
	Class        string
	SafeToRetire bool
	Blocker      string
	Detail       string
	Recommended  string
}

func (s *Service) classifyTrainV2Lifecycle(projectID string, train model.TrainV2) (trainV2LifecycleClassification, error) {
	return s.classifyTrainV2LifecycleWithContext(context.Background(), projectID, train)
}
func (s *Service) classifyTrainV2LifecycleWithContext(ctx context.Context, projectID string, train model.TrainV2) (trainV2LifecycleClassification, error) {
	if train.Status == model.TrainV2Retired {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassRetired,
			Recommended: "retain retired Train history",
		}, nil
	}
	if train.Status == model.TrainV2Completed {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassTerminal,
			Recommended: "retain completed Train history",
		}, nil
	}
	if train.Status == model.TrainV2ReadyForIntegration {
		stale, err := s.trainV2StaleIntegrationHistory(ctx, projectID, train.ID)
		if err != nil {
			return trainV2LifecycleClassification{}, err
		}
		if stale {
			return trainV2LifecycleClassification{
				Class:       trainV2ClassStale,
				Blocker:     "TRAIN_INTEGRATION_RECONCILIATION_REQUIRED",
				Detail:      "failed durable integration mutation left a pre_pending prefix requiring reconciliation",
				Recommended: "reconcile the stale integration prefix before retrying integration",
			}, nil
		}
		return trainV2LifecycleClassification{
			Class:       trainV2ClassIntegration,
			Blocker:     "TRAIN_INTEGRATION_PENDING",
			Detail:      "Train has completed execution and still requires integration",
			Recommended: "integrate or explicitly recover the Train",
		}, nil
	}
	if train.Status == model.TrainV2Planned {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassPlanned,
			Recommended: "start the planned Train",
		}, nil
	}

	for _, item := range train.Items {
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				return trainV2LifecycleClassification{
					Class:       trainV2ClassLiveAttempt,
					Blocker:     "TRAIN_ATTEMPT_LIVE",
					Detail:      fmt.Sprintf("item %d attempt %d is still running", item.Position, attempt.Number),
					Recommended: "reconcile the live Attempt before retirement",
				}, nil
			}
		}
	}
	if live, err := s.trainV2HasLiveOperationWithContext(ctx, projectID, train.ID); err != nil {
		return trainV2LifecycleClassification{}, err
	} else if live {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassLiveOperation,
			Blocker:     "TRAIN_OPERATION_LIVE",
			Detail:      "a durable Train mutation or integration operation is still active",
			Recommended: "let the operation reach a terminal state before retirement",
		}, nil
	}
	if rejected, ok := correctionPendingTrain(train); ok {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassCorrection,
			Blocker:     "TRAIN_CORRECTION_PENDING",
			Detail:      fmt.Sprintf("item %d has an immutable rejected review and a queued correction tail", rejected),
			Recommended: "start the exact queued correction with train/correction-start",
		}, nil
	}

	allTerminal := true
	for _, item := range train.Items {
		if item.Status == model.TrainV2ItemQueued && len(item.Attempts) == 0 {
			allTerminal = false
			break
		}
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				allTerminal = false
				break
			}
		}
	}
	if allTerminal && (train.Status == model.TrainV2Blocked || train.Status == model.TrainV2Paused || train.Status == model.TrainV2RecoveryQuarantined || train.Status == model.TrainV2Running) {
		return trainV2LifecycleClassification{
			Class:        trainV2ClassStale,
			SafeToRetire: true,
			Blocker:      "TRAIN_STALE",
			Detail:       "no live Attempt or durable operation remains for this non-terminal Train",
			Recommended:  "retire the stale Train with server-owned evidence",
		}, nil
	}
	return trainV2LifecycleClassification{
		Class:       trainV2ClassAmbiguous,
		Blocker:     "TRAIN_LIFECYCLE_AMBIGUOUS",
		Detail:      "durable state is non-terminal but cannot be proven inactive",
		Recommended: "inspect and reconcile the Train before retirement",
	}, nil
}
func correctionPendingTrain(train model.TrainV2) (int, bool) {
	eligible := -1
	for position, item := range train.Items {
		if item.Status != model.TrainV2ItemReviewed || item.Review == nil || item.Review.Outcome != model.ReviewOutcomeRejectedCorrection {
			continue
		}
		if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return -1, false
		}
		attempt := item.Attempts[item.SuccessfulAttemptNumber-1]
		if attempt.Status != model.TrainV2AttemptSucceeded || attempt.ReviewID != item.Review.ReportID {
			return -1, false
		}
		tailQueued := position < len(train.Items)-1
		for tailPosition := position + 1; tailPosition < len(train.Items); tailPosition++ {
			tail := train.Items[tailPosition]
			if tail.Status != model.TrainV2ItemQueued || len(tail.Attempts) != 0 || tail.Review != nil || tail.Proof != nil {
				tailQueued = false
				break
			}
		}
		if tailQueued {
			if eligible != -1 {
				return -1, false
			}
			eligible = position
		}
	}
	return eligible, eligible >= 0
}
