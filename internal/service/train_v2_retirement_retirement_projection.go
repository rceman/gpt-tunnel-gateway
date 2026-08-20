package service

import (
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func staticTrainV2SafeToRetire(train model.TrainV2) bool {
	if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked && train.Status != model.TrainV2RecoveryQuarantined {
		return false
	}
	for _, item := range train.Items {
		if item.Status == model.TrainV2ItemQueued && len(item.Attempts) == 0 {
			return false
		}
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				return false
			}
		}
	}
	return true
}
func staleTrainProjection(classification trainV2LifecycleClassification, train model.TrainV2) *TrainV2StaleTrain {
	if classification.Class != trainV2ClassStale && classification.Class != trainV2ClassAmbiguous {
		return nil
	}
	return &TrainV2StaleTrain{
		TrainID:               train.ID,
		Status:                train.Status,
		Classification:        classification.Class,
		Blocker:               classification.Blocker,
		Detail:                classification.Detail,
		RecommendedNextAction: classification.Recommended,
	}
}
func correctionTrainProjection(classification trainV2LifecycleClassification, train model.TrainV2) *TrainV2StaleTrain {
	if classification.Class != trainV2ClassCorrection {
		return nil
	}
	return &TrainV2StaleTrain{
		TrainID:               train.ID,
		Status:                train.Status,
		Classification:        classification.Class,
		Blocker:               classification.Blocker,
		Detail:                classification.Detail,
		RecommendedNextAction: classification.Recommended,
	}
}
func sortTrainV2StaleProjection(values []*TrainV2StaleTrain) {
	sort.Slice(values, func(i, j int) bool { return values[i].TrainID < values[j].TrainID })
}
