package train

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RecordImplementationProof closes one running TrainItem without changing
// its authoring Task. The immutable proof is anchored to the lane start and
// candidate checkpoint supplied by the caller after independent Git checks.
func RecordImplementationProof(current model.TrainV2, taskID string, attemptNumber uint64, checkpointHead, implementationSHA, reportID string, gates []model.CompletionGateResult, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if current.Status != model.TrainV2Running || now.IsZero() || attemptNumber < 1 || model.ValidateCommitSHA(checkpointHead) != nil || model.ValidateCommitSHA(implementationSHA) != nil || reportID == "" || len(gates) == 0 {
		return model.TrainV2{}, fmt.Errorf("invalid train implementation proof input")
	}
	position := -1
	for i := range current.Items {
		if current.Items[i].TaskID == taskID {
			position = i
			break
		}
	}
	if position < 0 {
		return model.TrainV2{}, fmt.Errorf("train item %q not found", taskID)
	}
	item := current.Items[position]
	if item.Status != model.TrainV2ItemRunning || attemptNumber > uint64(len(item.Attempts)) || item.Proof != nil {
		return model.TrainV2{}, fmt.Errorf("train item is not the exact running item")
	}
	attempt := &item.Attempts[attemptNumber-1]
	if attempt.Status != model.TrainV2AttemptRunning {
		return model.TrainV2{}, fmt.Errorf("Train item Attempt is not running")
	}
	if err := model.ValidateServerGateEvidence(gates); err != nil {
		return model.TrainV2{}, err
	}
	updated := current
	updated.Items[position].Status = model.TrainV2ItemFinalized
	updated.Items[position].Attempts[attemptNumber-1].Status = model.TrainV2AttemptSucceeded
	updated.Items[position].Attempts[attemptNumber-1].FinishedAt = &now
	updated.Items[position].SuccessfulAttemptNumber = attemptNumber
	updated.Items[position].ActiveAttemptNumber = 0
	updated.Items[position].Proof = &model.TrainV2ImplementationProof{CheckpointHead: checkpointHead, ImplementationSHA: implementationSHA, ReportID: reportID, GateResults: append([]model.CompletionGateResult{}, gates...), RecordedAt: now}
	updated.Revision++
	updated.UpdatedAt = now
	hasQueued := false
	for _, next := range updated.Items {
		if next.Status == model.TrainV2ItemQueued {
			hasQueued = true
			break
		}
	}
	if !hasQueued {
		updated.Status = model.TrainV2ReadyForIntegration
	}
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}

// RecordReview attaches a one-shot Delivery outcome to immutable item proof.
// A rejected or blocked item blocks the Train; accepted review never changes
// the authoring Task or requires the lane to be at the reviewed head.
func RecordReview(current model.TrainV2, taskID, outcome, reportID string, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if now.IsZero() || reportID == "" || (outcome != model.ReviewOutcomeAccepted && outcome != model.ReviewOutcomeRejected && outcome != model.ReviewOutcomeBlocked) {
		return model.TrainV2{}, fmt.Errorf("invalid train item review")
	}
	updated := current
	for i := range updated.Items {
		if updated.Items[i].TaskID != taskID {
			continue
		}
		if updated.Items[i].Proof == nil || updated.Items[i].Review != nil {
			return model.TrainV2{}, fmt.Errorf("train item is not reviewable or was already reviewed")
		}
		updated.Items[i].Review = &model.TrainV2ItemReview{Outcome: outcome, ReportID: reportID, ReviewedAt: now}
		if outcome == model.ReviewOutcomeAccepted {
			updated.Items[i].Status = model.TrainV2ItemReviewed
		} else {
			updated.Items[i].Status = model.TrainV2ItemBlocked
			updated.Status = model.TrainV2Blocked
		}
		updated.Revision++
		updated.UpdatedAt = now
		if err := model.ValidateTrainV2(updated); err != nil {
			return model.TrainV2{}, err
		}
		return updated, nil
	}
	return model.TrainV2{}, fmt.Errorf("train item %q not found", taskID)
}
