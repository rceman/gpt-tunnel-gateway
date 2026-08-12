package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
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
	if err := s.Hub.ReadJSON(ctx, s.trainV2Path(projectID, trainID), &train); err != nil {
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
	paths, err := s.Hub.List(ctx, s.trainV2Root(in.ProjectID), ".json")
	if err != nil {
		return TrainV2ListResult{}, err
	}
	trains := make([]model.TrainV2, 0, len(paths))
	for _, path := range paths {
		var train model.TrainV2
		if err := s.Hub.ReadJSON(ctx, path, &train); err != nil {
			return TrainV2ListResult{}, err
		}
		if train.ProjectID != in.ProjectID || model.ValidateTrainV2(train) != nil {
			return TrainV2ListResult{}, fmt.Errorf("invalid train v2 record %q", path)
		}
		trains = append(trains, train)
	}
	sort.Slice(trains, func(i, j int) bool { return trains[i].UpdatedAt.After(trains[j].UpdatedAt) })
	if len(trains) > limit {
		trains = trains[:limit]
	}
	return TrainV2ListResult{Trains: trains}, nil
}

func (s *Service) TrainV2Create(ctx context.Context, in TrainV2CreateInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.CreatedBy == "" || strings.ContainsAny(in.CreatedBy, "\x00\r\n") {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TrainV2{}, OperationResult{}, err
		}
	}
	now := s.durableNow()
	var created model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: create train v2", func(worktree string) ([]string, error) {
		trainID, err := nextTrainV2ID(worktree, s.trainV2Root(in.ProjectID), identifiers.ProjectCode)
		if err != nil {
			return nil, err
		}
		tasks, err := s.trainV2AdmissionTasks(worktree, in.ProjectID, in.TaskIDs)
		if err != nil {
			return nil, err
		}
		created, err = trainv2.New(in.ProjectID, trainID, in.CreatedBy, tasks, now)
		if err != nil {
			return nil, err
		}
		path := s.trainV2Path(in.ProjectID, trainID)
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); statErr == nil {
			return nil, fmt.Errorf("train v2 already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, path, created); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return created, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: created.Status}, nil
}

func (s *Service) TrainV2Add(ctx context.Context, in TrainV2AddInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.AddedBy == "" || strings.ContainsAny(in.AddedBy, "\x00\r\n") || in.ExpectedRevision < 1 {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("expected_revision and added_by are required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision || current.Status != model.TrainV2Planned {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("train v2 revision or status conflict")
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TrainV2{}, OperationResult{}, err
		}
	}
	now := s.durableNow()
	var updated model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: add tasks to train v2 "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != in.ExpectedRevision || latest.Status != model.TrainV2Planned {
			return nil, fmt.Errorf("train v2 revision or status conflict")
		}
		if len(latest.Items)+len(in.TaskIDs) > model.MaxTrainV2Items {
			return nil, fmt.Errorf("train v2 item limit exceeded")
		}
		tasks, err := s.trainV2AdmissionTasks(worktree, in.ProjectID, in.TaskIDs)
		if err != nil {
			return nil, err
		}
		updated, err = trainv2.Append(latest, tasks, now)
		if err != nil {
			return nil, err
		}
		path := s.trainV2Path(in.ProjectID, in.TrainID)
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return updated, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: updated.Status}, nil
}

func (s *Service) trainV2AdmissionTasks(worktree, projectID string, taskIDs []string) ([]model.TaskAuthoring, error) {
	if err := trainv2.ValidateTaskIDs(taskIDs); err != nil {
		return nil, err
	}
	root := filepath.Join(worktree, filepath.FromSlash(s.trainV2Root(projectID)))
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	existing := make([]model.TrainV2, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var existingTrain model.TrainV2
		if err := readWorktreeJSON(worktree, filepath.ToSlash(filepath.Join(s.trainV2Root(projectID), entry.Name())), &existingTrain); err != nil {
			return nil, err
		}
		if err := model.ValidateTrainV2(existingTrain); err != nil {
			return nil, err
		}
		existing = append(existing, existingTrain)
	}
	if err := trainv2.ValidateUnadmitted(existing, taskIDs); err != nil {
		return nil, err
	}
	tasks := make([]model.TaskAuthoring, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		var task model.TaskAuthoring
		if err := readWorktreeJSON(worktree, s.taskAuthoringPath(projectID, taskID), &task); err != nil {
			return nil, fmt.Errorf("read ready task %q: %w", taskID, err)
		}
		if task.ProjectID != projectID || model.ValidateTaskAuthoring(task) != nil || task.Status != model.TaskAuthoringReady || task.ReadySeal == nil || task.ReadySeal.Revision != task.Revision || task.ReadySeal.RevisionSHA256 != task.RevisionSHA256 {
			return nil, fmt.Errorf("task %q is not an exact ready train_v2 Task", taskID)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func nextTrainV2ID(worktree, root, projectCode string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(root)))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var next uint64 = 1
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code, number, err := model.ParseTrainV2ID(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || code != projectCode {
			return "", fmt.Errorf("invalid train v2 member %q", entry.Name())
		}
		if number >= next {
			next = number + 1
		}
	}
	return model.FormatTrainV2ID(projectCode, next)
}
