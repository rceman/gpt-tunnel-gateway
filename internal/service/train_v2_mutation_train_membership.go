package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func trainV2AddableStatus(status string) bool {
	return status == model.TrainV2Planned || status == model.TrainV2Running || status == model.TrainV2ReadyForIntegration
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
	if err := s.validateTaskDependenciesInWorktree(worktree, projectID, tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Service) trainV2AdmissionTasksShared(ctx context.Context, projectID string, taskIDs []string) ([]model.TaskAuthoring, error) {
	if err := trainv2.ValidateTaskIDs(taskIDs); err != nil {
		return nil, err
	}
	existing, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := trainv2.ValidateUnadmitted(existing, taskIDs); err != nil {
		return nil, err
	}
	tasks := make([]model.TaskAuthoring, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, err := s.readSharedTask(ctx, projectID, taskID)
		if err != nil {
			return nil, fmt.Errorf("read ready task %q: %w", taskID, err)
		}
		if task.Status != model.TaskAuthoringReady || task.ReadySeal == nil || task.ReadySeal.Revision != task.Revision || task.ReadySeal.RevisionSHA256 != task.RevisionSHA256 {
			return nil, fmt.Errorf("task %q is not an exact ready train_v2 Task", taskID)
		}
		if err := s.validateTaskDependenciesShared(ctx, projectID, task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// validateTrainV2TaskMembershipInWorktree is called inside the start
// transaction so a pre-existing duplicate Task cannot execute through either
// Train, even when another admission transaction raced the initial read.
func (s *Service) validateTrainV2TaskMembershipInWorktree(worktree, projectID, targetTrainID string) error {
	root := filepath.Join(worktree, filepath.FromSlash(s.trainV2Root(projectID)))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	owners := make(map[string]string)
	seenTrain := false
	for _, entry := range entries {
		if entry.IsDir() || !canonicalTrainV2RecordName(entry.Name()) {
			continue
		}
		var train model.TrainV2
		path := filepath.ToSlash(filepath.Join(s.trainV2Root(projectID), entry.Name()))
		if err := readWorktreeJSON(worktree, path, &train); err != nil {
			return err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		if train.Historical != nil {
			if train.ID == targetTrainID {
				return fmt.Errorf("historical Train %q cannot start", targetTrainID)
			}
			continue
		}
		if train.ID == targetTrainID {
			seenTrain = true
		}
		for _, item := range train.Items {
			owner, exists := owners[item.TaskID]
			if exists {
				return fmt.Errorf("task %q belongs to multiple trains (%s, %s)", item.TaskID, owner, train.ID)
			}
			owners[item.TaskID] = train.ID
		}
	}
	if !seenTrain {
		return fmt.Errorf("Train %q has no canonical Task membership", targetTrainID)
	}
	return nil
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
