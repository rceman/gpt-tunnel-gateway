package service

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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

func correctionTrainProjection(train model.TrainV2, position int) *TrainV2StaleTrain {
	return &TrainV2StaleTrain{
		TrainID:               train.ID,
		Status:                train.Status,
		Classification:        "correction_pending",
		Blocker:               "TRAIN_CORRECTION_PENDING",
		Detail:                fmt.Sprintf("item %d has an immutable rejected review and a queued correction tail", position),
		RecommendedNextAction: "start the exact queued correction with train/correction-start",
	}
}
