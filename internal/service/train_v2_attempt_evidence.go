package service

import (
	"errors"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func loadOrReuseAttemptReport(evidence trainv2.EvidenceStore, candidate model.TrainV2AttemptReport) (model.TrainV2AttemptReport, error) {
	stored, err := evidence.ReadAttemptReport(candidate.TrainID, candidate.TaskID, candidate.AttemptNumber)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		return model.TrainV2AttemptReport{}, fmt.Errorf("read existing Attempt report: %w", err)
	}
	if stored.TrainID != candidate.TrainID || stored.TaskID != candidate.TaskID || stored.ItemPosition != candidate.ItemPosition || stored.AttemptNumber != candidate.AttemptNumber || stored.ProjectID != candidate.ProjectID {
		return model.TrainV2AttemptReport{}, fmt.Errorf("existing Attempt report identity mismatch")
	}
	if err := model.ValidateTrainV2AttemptReport(stored); err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("existing Attempt report is invalid: %w", err)
	}
	return stored, nil
}

func loadOrReuseAttemptReview(evidence trainv2.EvidenceStore, candidate model.TrainV2AttemptReview) (model.TrainV2AttemptReview, error) {
	stored, err := evidence.ReadAttemptReview(candidate.TrainID, candidate.TaskID, candidate.AttemptNumber)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		return model.TrainV2AttemptReview{}, fmt.Errorf("read existing Attempt review: %w", err)
	}
	if stored.ID != candidate.ID || stored.TrainID != candidate.TrainID || stored.TaskID != candidate.TaskID || stored.ItemPosition != candidate.ItemPosition || stored.AttemptNumber != candidate.AttemptNumber {
		return model.TrainV2AttemptReview{}, fmt.Errorf("existing Attempt review identity mismatch")
	}
	if err := model.ValidateTrainV2AttemptReview(stored); err != nil {
		return model.TrainV2AttemptReview{}, fmt.Errorf("existing Attempt review is invalid: %w", err)
	}
	return stored, nil
}
