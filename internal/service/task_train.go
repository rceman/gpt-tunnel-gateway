package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskTrainPath(project string) string {
	return s.projectPrefix(project) + "/train/current.json"
}

func (s *Service) TaskTrainRead(ctx context.Context, project string) (model.TaskTrain, error) {
	var train model.TaskTrain
	if err := s.Hub.ReadJSON(ctx, s.taskTrainPath(project), &train); err != nil {
		return model.TaskTrain{}, err
	}
	if err := model.ValidateTaskTrain(train); err != nil || train.ProjectID != project {
		return model.TaskTrain{}, fmt.Errorf("invalid task train")
	}
	return train, nil
}

func (s *Service) TaskTrainCreate(ctx context.Context, in TaskTrainCreateInput) (model.TaskTrain, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	if len(in.TaskIDs) < 1 || len(in.TaskIDs) > model.MaxTaskTrainTasks {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("task train must contain 1-%d tasks", model.MaxTaskTrainTasks)
	}
	if in.CreatedBy == "" {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	for _, id := range in.TaskIDs {
		if err := model.ValidateObjectIdentifier(id); err != nil {
			return model.TaskTrain{}, OperationResult{}, err
		}
	}
	train := model.TaskTrain{
		SchemaVersion: model.TaskTrainSchemaVersion,
		ID:            "current",
		ProjectID:     in.ProjectID,
		TaskIDs:       append([]string{}, in.TaskIDs...),
		CurrentTaskID: in.TaskIDs[0],
		Status:        model.TaskTrainActive,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := model.ValidateTaskTrain(train); err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TaskTrain{}, OperationResult{}, err
		}
	}
	plan, err := s.PlanRead(ctx, in.ProjectID)
	if err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	if plan.ActiveRunID != "" || (plan.ActiveTaskID != "" && plan.ActiveTaskID != train.CurrentTaskID) {
		return model.TaskTrain{}, OperationResult{}, fmt.Errorf("project plan is already bound to another active task")
	}
	tx, err := s.Hub.Transact(ctx, expected, "watcher: create task train", func(worktree string) ([]string, error) {
		var existing model.TaskTrain
		if err := readWorktreeJSON(worktree, s.taskTrainPath(in.ProjectID), &existing); err == nil {
			return nil, fmt.Errorf("task train already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		var currentPlan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(in.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if currentPlan.ActiveRunID != "" || (currentPlan.ActiveTaskID != "" && currentPlan.ActiveTaskID != train.CurrentTaskID) {
			return nil, fmt.Errorf("project plan changed before task train creation")
		}
		for i, id := range train.TaskIDs {
			var task model.Task
			if err := readWorktreeJSON(worktree, s.taskPath(in.ProjectID, id), &task); err != nil {
				return nil, err
			}
			if task.ProjectID != in.ProjectID || model.ValidateTask(task) != nil {
				return nil, fmt.Errorf("task train task %q is invalid or belongs to another project", id)
			}
			var state model.TaskState
			if err := readWorktreeJSON(worktree, s.taskStatePath(in.ProjectID, id), &state); err != nil {
				return nil, err
			}
			if err := model.ValidateTaskState(state, task); err != nil {
				return nil, err
			}
			if i == 0 && state.Status != "created" && state.Status != "ready" {
				return nil, fmt.Errorf("first task is not dispatchable: %s", state.Status)
			}
		}
		currentPlan.ActiveTaskID = train.CurrentTaskID
		currentPlan.UpdatedBy = in.CreatedBy
		currentPlan.UpdatedAt = train.UpdatedAt
		currentPlan.Revision++
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.taskTrainPath(in.ProjectID), train); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.planPath(in.ProjectID), currentPlan); err != nil {
			return nil, err
		}
		return []string{s.taskTrainPath(in.ProjectID), s.planPath(in.ProjectID)}, nil
	})
	if err != nil {
		return model.TaskTrain{}, OperationResult{}, err
	}
	return train, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    model.TaskTrainActive,
	}, nil
}

func (s *Service) TaskTrainStatus(ctx context.Context, project string) (TaskTrainStatus, error) {
	train, err := s.TaskTrainRead(ctx, project)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	return s.taskTrainStatus(ctx, train)
}

func (s *Service) taskTrainStatus(ctx context.Context, train model.TaskTrain) (TaskTrainStatus, error) {
	result := TaskTrainStatus{
		ProjectID:     train.ProjectID,
		TrainID:       train.ID,
		Status:        train.Status,
		CurrentIndex:  train.CurrentIndex,
		TaskCount:     len(train.TaskIDs),
		CurrentTaskID: train.CurrentTaskID,
		CurrentRunID:  train.CurrentRunID,
		WaitReason:    train.WaitReason,
	}
	if train.CurrentIndex+1 < len(train.TaskIDs) {
		result.NextTaskID = train.TaskIDs[train.CurrentIndex+1]
	}
	task, err := s.findTask(ctx, train.CurrentTaskID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	result.CurrentTaskState = state.Status
	runs, err := s.RunList(ctx, train.ProjectID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	for _, run := range runs {
		if run.TaskID != task.ID || run.Historical {
			continue
		}
		if train.CurrentRunID == run.ID || (result.CurrentRunID == "" && operationalActiveRun(run)) {
			result.CurrentRunID, result.CurrentRunStatus = run.ID, run.Status
			break
		}
	}
	return result, nil
}
