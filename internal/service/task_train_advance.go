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
	trainID := model.CanonicalTaskTrainID(next)
	_, err := s.Hub.Transact(ctx, expected, "watcher: update task train", func(worktree string) ([]string, error) {
		var current model.TaskTrain
		path := s.taskTrainPathFor(next.ProjectID, trainID)
		if err := readWorktreeJSON(worktree, path, &current); err != nil {
			return nil, err
		}
		normalizeTaskTrain(&current)
		statusChanged := current.Status != next.Status
		allowedStatusChange := current.Status == model.TaskTrainActive && (next.Status == model.TaskTrainWaitingDelivery || next.Status == model.TaskTrainBlocked || next.Status == model.TaskTrainCompleted)
		allowedStatusChange = allowedStatusChange || current.Status == model.TaskTrainWaitingDelivery && next.Status == model.TaskTrainActive
		completionAdvance := next.Status == model.TaskTrainCompleted && current.Status == model.TaskTrainActive && next.CurrentIndex == current.CurrentIndex+1 && next.CurrentIndex == len(next.TaskIDs) && current.CurrentTaskID == next.CurrentTaskID
		if (current.CurrentIndex != next.CurrentIndex || current.CurrentTaskID != next.CurrentTaskID) && !completionAdvance || current.CurrentRunID != next.CurrentRunID && next.CurrentRunID != "" && current.CurrentRunID != "" || statusChanged && !allowedStatusChange {
			return nil, fmt.Errorf("task train changed concurrently")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		return []string{path}, nil
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
	active, err := s.activeLaneRuns(ctx, train)
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
	focusPlan := plan.ActiveTaskID == "" && plan.ActiveRunID == ""
	if focusPlan {
		plan.ActiveTaskID, plan.ActiveRunID = next.CurrentTaskID, ""
		plan.Revision++
		plan.UpdatedBy, plan.UpdatedAt = s.Config.GatewayID, time.Now().UTC()
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return status, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "watcher: advance task train", func(worktree string) ([]string, error) {
		var current model.TaskTrain
		trainPath := s.taskTrainPathFor(train.ProjectID, model.CanonicalTaskTrainID(train))
		if err := readWorktreeJSON(worktree, trainPath, &current); err != nil {
			return nil, err
		}
		normalizeTaskTrain(&current)
		if current.CurrentIndex != train.CurrentIndex || current.CurrentTaskID != train.CurrentTaskID || current.Status != train.Status {
			return nil, fmt.Errorf("task train changed concurrently")
		}
		var currentPlan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(train.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if focusPlan {
			if currentPlan.ActiveRunID != "" || (currentPlan.ActiveTaskID != "" && currentPlan.ActiveTaskID != train.CurrentTaskID) {
				return nil, fmt.Errorf("plan changed before task train focus update")
			}
			if err := model.ValidatePlan(plan); err != nil {
				return nil, err
			}
		}
		if err := hub.WriteJSON(worktree, trainPath, next); err != nil {
			return nil, err
		}
		if focusPlan {
			if err := hub.WriteJSON(worktree, s.planPath(train.ProjectID), plan); err != nil {
				return nil, err
			}
		}
		paths := []string{trainPath}
		if focusPlan {
			paths = append(paths, s.planPath(train.ProjectID))
		}
		return paths, nil
	})
	if err != nil {
		return status, err
	}
	task, err := s.findTask(ctx, next.CurrentTaskID)
	if err != nil {
		return status, err
	}
	groupIndex, group, _ := taskTrainExecutionGroup(next, task.ID)
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID:               task.ID,
		TrainID:              model.CanonicalTaskTrainID(next),
		LaneBranch:           next.LaneBranch,
		AgentID:              group.AgentID,
		RecommendedReasoning: group.RecommendedReasoning,
		ResolvedReasoning:    group.ResolvedReasoning,
		AgentFallback:        group.Fallback,
		AgentFallbackReason:  group.FallbackReason,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil {
		return status, err
	}
	if groupIndex >= 0 && next.ExecutionGroups[groupIndex].AgentID == "" {
		next.ExecutionGroups = append([]model.ExecutionGroup{}, next.ExecutionGroups...)
		next.ExecutionGroups[groupIndex].AgentID = run.AgentID
		next.ExecutionGroups[groupIndex].ResolvedReasoning = run.ResolvedReasoning
		next.ExecutionGroups[groupIndex].Fallback = run.AgentFallback
		next.ExecutionGroups[groupIndex].FallbackReason = run.AgentFallbackReason
	}
	next.CurrentRunID = run.ID
	if err := s.bindTaskTrainRun(ctx, next, run.ID, ""); err != nil {
		return status, err
	}
	return s.taskTrainTail(ctx, TaskTrainStatus{
		ProjectID:        next.ProjectID,
		TrainID:          model.CanonicalTaskTrainID(next),
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

func (s *Service) activeLaneRuns(ctx context.Context, train model.TaskTrain) (int, error) {
	runs, err := s.RunList(ctx, train.ProjectID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, run := range runs {
		if !operationalActiveRun(run) {
			continue
		}
		if run.TrainID == model.CanonicalTaskTrainID(train) || (run.LaneBranch != "" && run.LaneBranch == train.LaneBranch) || run.ID == train.CurrentRunID {
			count++
		}
	}
	return count, nil
}
