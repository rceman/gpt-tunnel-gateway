package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

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
	return created, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    created.Status,
	}, nil
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
	if current.Revision != in.ExpectedRevision || (current.Status != model.TrainV2Planned && current.Status != model.TrainV2Running) {
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
		if latest.Revision != in.ExpectedRevision || (latest.Status != model.TrainV2Planned && latest.Status != model.TrainV2Running) {
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
	return updated, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    updated.Status,
	}, nil
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
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
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

func (s *Service) taskAdmittedToNonterminalTrain(ctx context.Context, projectID, taskID string) (bool, error) {
	trains, err := s.readTrainV2Records(ctx, projectID)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return trainv2.TaskAdmittedToNonterminal(trains, taskID), nil
}

func taskAdmittedToNonterminalTrainInWorktree(worktree, root, taskID string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(root)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	trains := make([]model.TrainV2, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		var train model.TrainV2
		if err := readWorktreeJSON(worktree, filepath.ToSlash(filepath.Join(root, entry.Name())), &train); err != nil {
			return false, err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return false, err
		}
		trains = append(trains, train)
	}
	return trainv2.TaskAdmittedToNonterminal(trains, taskID), nil
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
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
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

func canonicalTrainV2RecordName(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	_, _, err := model.ParseTrainV2ID(strings.TrimSuffix(name, ".json"))
	return err == nil
}
