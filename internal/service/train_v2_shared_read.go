package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Shared Train reads are deliberately private to execution paths that have
// already opted into Shared authority. Generic TrainV2Read/List remain
// Hub-backed compatibility reads until the lifecycle writes migrate together.
func (s *Service) trainV2ReadShared(ctx context.Context, projectID, trainID string) (model.TrainV2, error) {
	if err := requireTrainV2Authoring(ctx, s, projectID); err != nil {
		return model.TrainV2{}, err
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.TrainV2{}, err
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return model.TrainV2{}, err
	}
	shared, err := s.Durability.ReadSharedEntity(ctx, "train", trainID)
	if err != nil {
		return model.TrainV2{}, err
	}
	var train model.TrainV2
	if err := json.Unmarshal(shared.Payload, &train); err != nil {
		return model.TrainV2{}, err
	}
	if train.ProjectID != projectID || train.ID != trainID {
		return model.TrainV2{}, fmt.Errorf("shared train v2 identity mismatch")
	}
	if err := model.ValidateTrainV2(train); err != nil {
		return model.TrainV2{}, err
	}
	return train, nil
}

func (s *Service) trainV2ListShared(ctx context.Context, projectID string, limit int) ([]model.TrainV2, error) {
	if err := requireTrainV2Authoring(ctx, s, projectID); err != nil {
		return nil, err
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = model.MaxTrainV2Items
	}
	if limit < 1 || limit > model.MaxTrainV2Items {
		return nil, fmt.Errorf("invalid train v2 list limit")
	}
	trains, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return nil, err
	}
	sort.Slice(trains, func(i, j int) bool { return trains[i].UpdatedAt.After(trains[j].UpdatedAt) })
	if len(trains) > limit {
		trains = trains[:limit]
	}
	return trains, nil
}
