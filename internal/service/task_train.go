package service

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskTrainPath(project string) string {
	return s.projectPrefix(project) + "/train/current.json"
}

func (s *Service) taskTrainPathFor(project, trainID string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateObjectIdentifier(trainID) != nil {
		return "../invalid-task-train"
	}
	return s.projectPrefix(project) + "/train/" + trainID + ".json"
}

func normalizeTaskTrain(train *model.TaskTrain) {
	if train.TrainID == "" {
		train.TrainID = train.ID
	}
	train.ID = ""
	if len(train.ExecutionGroups) == 0 {
		train.ExecutionGroups = model.DefaultExecutionGroups(train.TaskIDs, "")
	}
}

func (s *Service) TaskTrainRead(ctx context.Context, project string) (model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return model.TaskTrain{}, err
	}
	var legacy model.TaskTrain
	if err := s.Hub.ReadJSON(ctx, s.taskTrainPath(project), &legacy); err == nil {
		normalizeTaskTrain(&legacy)
		if err := model.ValidateTaskTrain(legacy); err != nil || legacy.ProjectID != project {
			return model.TaskTrain{}, fmt.Errorf("invalid task train")
		}
		return legacy, nil
	} else if !IsNotFound(err) {
		return model.TaskTrain{}, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/train", ".json")
	if err != nil {
		return model.TaskTrain{}, err
	}
	if len(paths) == 0 {
		return model.TaskTrain{}, os.ErrNotExist
	}
	if len(paths) > 1 {
		return model.TaskTrain{}, fmt.Errorf("multiple task trains exist; train_id is required")
	}
	return s.TaskTrainReadByID(ctx, project, strings.TrimSuffix(filepathBase(paths[0]), ".json"))
}

func (s *Service) TaskTrainReadByID(ctx context.Context, project, trainID string) (model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return model.TaskTrain{}, err
	}
	if err := model.ValidateObjectIdentifier(trainID); err != nil {
		return model.TaskTrain{}, err
	}
	var train model.TaskTrain
	if err := s.Hub.ReadJSON(ctx, s.taskTrainPathFor(project, trainID), &train); err != nil {
		return model.TaskTrain{}, err
	}
	normalizeTaskTrain(&train)
	if err := model.ValidateTaskTrain(train); err != nil || train.ProjectID != project {
		return model.TaskTrain{}, fmt.Errorf("invalid task train")
	}
	if train.TrainID != trainID {
		return model.TaskTrain{}, fmt.Errorf("task train id mismatch")
	}
	return train, nil
}

func filepathBase(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func (s *Service) TaskTrainList(ctx context.Context, project string) ([]model.TaskTrain, error) {
	if err := model.ValidateProjectIdentifier(project); err != nil {
		return nil, err
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/train", ".json")
	if err != nil {
		return nil, err
	}
	trains := make([]model.TaskTrain, 0, len(paths))
	for _, path := range paths {
		id := strings.TrimSuffix(filepathBase(path), ".json")
		train, err := s.TaskTrainReadByID(ctx, project, id)
		if err != nil {
			return nil, err
		}
		trains = append(trains, train)
	}
	sort.Slice(trains, func(i, j int) bool { return trains[i].TrainID < trains[j].TrainID })
	return trains, nil
}
