package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) bindTaskTrainRun(ctx context.Context, train model.TaskTrain, runID, expected string) error {
	train.CurrentRunID = runID
	return s.updateTaskTrain(ctx, train, expected)
}

func (s *Service) updateTaskTrain(ctx context.Context, next model.TaskTrain, expected string) error {
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return err
		}
	}
	next.UpdatedAt = time.Now().UTC()
	if err := model.ValidateTaskTrain(next); err != nil {
		return err
	}
	_, err := s.Hub.Transact(ctx, expected, "watcher: update task train", func(worktree string) ([]string, error) {
		var current model.TaskTrain
		if err := readWorktreeJSON(worktree, s.taskTrainPath(next.ProjectID), &current); err != nil {
			return nil, err
		}
		statusChanged := current.Status != next.Status
		allowedStatusChange := current.Status == model.TaskTrainActive && (next.Status == model.TaskTrainWaitingDelivery || next.Status == model.TaskTrainBlocked || next.Status == model.TaskTrainCompleted)
		allowedStatusChange = allowedStatusChange || current.Status == model.TaskTrainWaitingDelivery && next.Status == model.TaskTrainActive
		if current.CurrentIndex != next.CurrentIndex || current.CurrentTaskID != next.CurrentTaskID || current.CurrentRunID != next.CurrentRunID && next.CurrentRunID != "" && current.CurrentRunID != "" || statusChanged && !allowedStatusChange {
			return nil, fmt.Errorf("task train changed concurrently")
		}
		if err := hub.WriteJSON(worktree, s.taskTrainPath(next.ProjectID), next); err != nil {
			return nil, err
		}
		return []string{s.taskTrainPath(next.ProjectID)}, nil
	})
	return err
}

func (s *Service) advanceTaskTrain(ctx context.Context, train model.TaskTrain, status TaskTrainStatus) (TaskTrainStatus, error) {
	if train.CurrentIndex+1 >= len(train.TaskIDs) {
		train.CurrentIndex = len(train.TaskIDs)
		train.Status, train.WaitReason = model.TaskTrainCompleted, ""
		if err := s.updateTaskTrain(ctx, train, ""); err != nil {
			return TaskTrainStatus{}, err
		}
		status.Status = model.TaskTrainCompleted
		status.CurrentIndex = train.CurrentIndex
		status.WaitReason = ""
		return status, nil
	}
	active, err := s.activeOperationalRuns(ctx, train.ProjectID)
	if err != nil {
		return status, err
	}
	if active > 0 {
		status.WaitReason = "another_active_run_exists"
		return status, nil
	}
	next := train
	next.CurrentIndex++
	next.CurrentTaskID = next.TaskIDs[next.CurrentIndex]
	next.CurrentRunID = ""
	next.Status, next.WaitReason = model.TaskTrainActive, ""
	plan, err := s.PlanRead(ctx, train.ProjectID)
	if err != nil {
		return status, err
	}
	if plan.ActiveRunID != "" || (plan.ActiveTaskID != "" && plan.ActiveTaskID != train.CurrentTaskID) {
		return status, fmt.Errorf("plan is not ready for next task train step")
	}
	plan.ActiveTaskID, plan.ActiveRunID = next.CurrentTaskID, ""
	plan.Revision++
	plan.UpdatedBy, plan.UpdatedAt = s.Config.GatewayID, time.Now().UTC()
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return status, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "watcher: advance task train", func(worktree string) ([]string, error) {
		var current model.TaskTrain
		if err := readWorktreeJSON(worktree, s.taskTrainPath(train.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.CurrentIndex != train.CurrentIndex || current.CurrentTaskID != train.CurrentTaskID || current.Status != train.Status {
			return nil, fmt.Errorf("task train changed concurrently")
		}
		var currentPlan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(train.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if currentPlan.ActiveRunID != "" || (currentPlan.ActiveTaskID != "" && currentPlan.ActiveTaskID != train.CurrentTaskID) {
			return nil, fmt.Errorf("plan changed before task train advance")
		}
		if err := model.ValidatePlan(plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.taskTrainPath(train.ProjectID), next); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.planPath(train.ProjectID), plan); err != nil {
			return nil, err
		}
		return []string{s.taskTrainPath(train.ProjectID), s.planPath(train.ProjectID)}, nil
	})
	if err != nil {
		return status, err
	}
	task, err := s.findTask(ctx, next.CurrentTaskID)
	if err != nil {
		return status, err
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil {
		return status, err
	}
	next.CurrentRunID = run.ID
	if err := s.bindTaskTrainRun(ctx, next, run.ID, ""); err != nil {
		return status, err
	}
	return s.taskTrainTail(ctx, TaskTrainStatus{
		ProjectID:        next.ProjectID,
		TrainID:          next.ID,
		Status:           next.Status,
		CurrentIndex:     next.CurrentIndex,
		TaskCount:        len(next.TaskIDs),
		CurrentTaskID:    next.CurrentTaskID,
		CurrentRunID:     run.ID,
		CurrentRunStatus: run.Status,
		NextTaskID:       nextTaskID(next),
	}, "")
}

func nextTaskID(train model.TaskTrain) string {
	if train.CurrentIndex+1 < len(train.TaskIDs) {
		return train.TaskIDs[train.CurrentIndex+1]
	}
	return ""
}

func (s *Service) activeOperationalRuns(ctx context.Context, project string) (int, error) {
	runs, err := s.RunList(ctx, project)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, run := range runs {
		if operationalActiveRun(run) {
			count++
		}
	}
	return count, nil
}
