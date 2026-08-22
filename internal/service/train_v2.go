package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) trainV2Root(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-trains-v2"
	}
	return s.projectPrefix(projectID) + "/trains-v2"
}

func (s *Service) trainV2Path(projectID, trainID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-train-v2"
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return "../invalid-train-v2"
	}
	return s.trainV2Root(projectID) + "/" + trainID + ".json"
}

func (s *Service) TrainV2Read(ctx context.Context, projectID, trainID string) (model.TrainV2, error) {
	if err := requireTrainV2Authoring(ctx, s, projectID); err != nil {
		return model.TrainV2{}, err
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.TrainV2{}, err
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return model.TrainV2{}, err
	}
	var train model.TrainV2
	if s.Durability != nil {
		shared, err := s.Durability.ReadSharedEntity(ctx, "train", trainID)
		if err != nil {
			return model.TrainV2{}, err
		}
		if err := json.Unmarshal(shared.Payload, &train); err != nil {
			return model.TrainV2{}, err
		}
	} else if _, err := s.entityRegistry(projectID).ReadInto(ctx, entity.TrainFamily, trainID, &train); err != nil {
		return model.TrainV2{}, err
	}
	if train.ProjectID != projectID || train.ID != trainID {
		return model.TrainV2{}, fmt.Errorf("train v2 identity mismatch")
	}
	if err := model.ValidateTrainV2(train); err != nil {
		return model.TrainV2{}, err
	}
	return train, nil
}

func (s *Service) TrainV2List(ctx context.Context, in TrainV2ListInput) (TrainV2ListResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ListResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2ListResult{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = model.MaxTrainV2Items
	}
	if limit < 1 || limit > model.MaxTrainV2Items {
		return TrainV2ListResult{}, fmt.Errorf("invalid train v2 list limit")
	}
	var trains []model.TrainV2
	if s.Durability != nil {
		var err error
		trains, err = s.sharedTrains(ctx, in.ProjectID)
		if err != nil {
			return TrainV2ListResult{}, err
		}
	} else {
		records, err := s.entityRegistry(in.ProjectID).ListRecords(ctx, entity.Query{Family: entity.TrainFamily})
		if err != nil {
			return TrainV2ListResult{}, err
		}
		trains = make([]model.TrainV2, 0, len(records))
		for _, record := range records {
			var train model.TrainV2
			if err := decodeStrict(record.Bytes, &train); err != nil {
				return TrainV2ListResult{}, err
			}
			if train.ProjectID != in.ProjectID || model.ValidateTrainV2(train) != nil {
				return TrainV2ListResult{}, fmt.Errorf("invalid train v2 record %q", record.Path)
			}
			trains = append(trains, train)
		}
	}
	sort.Slice(trains, func(i, j int) bool { return trains[i].UpdatedAt.After(trains[j].UpdatedAt) })
	if len(trains) > limit {
		trains = trains[:limit]
	}
	return TrainV2ListResult{Trains: trains}, nil
}
