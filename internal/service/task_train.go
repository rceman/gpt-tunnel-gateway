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
	train := model.TaskTrain{SchemaVersion: model.TaskTrainSchemaVersion, ID: "current", ProjectID: in.ProjectID, TaskIDs: append([]string{}, in.TaskIDs...), CurrentTaskID: in.TaskIDs[0], Status: model.TaskTrainActive, UpdatedAt: time.Now().UTC()}
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
	plan.ActiveTaskID = train.CurrentTaskID
	plan.UpdatedBy = in.CreatedBy
	plan.UpdatedAt = train.UpdatedAt
	plan.Revision++
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
	return train, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: model.TaskTrainActive}, nil
}

func (s *Service) TaskTrainStatus(ctx context.Context, project string) (TaskTrainStatus, error) {
	train, err := s.TaskTrainRead(ctx, project)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	return s.taskTrainStatus(ctx, train)
}

func (s *Service) taskTrainStatus(ctx context.Context, train model.TaskTrain) (TaskTrainStatus, error) {
	result := TaskTrainStatus{ProjectID: train.ProjectID, TrainID: train.ID, Status: train.Status, CurrentIndex: train.CurrentIndex, TaskCount: len(train.TaskIDs), CurrentTaskID: train.CurrentTaskID, CurrentRunID: train.CurrentRunID, WaitReason: train.WaitReason}
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

func (s *Service) TaskTrainPoll(ctx context.Context, in TaskTrainPollInput) (TaskTrainStatus, error) {
	train, err := s.TaskTrainRead(ctx, in.ProjectID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	status, err := s.taskTrainStatus(ctx, train)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	if train.Status == model.TaskTrainCompleted || train.Status == model.TaskTrainBlocked {
		return status, nil
	}
	task, err := s.findTask(ctx, train.CurrentTaskID)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskTrainStatus{}, err
	}
	plan, planErr := s.PlanRead(ctx, train.ProjectID)
	if planErr != nil {
		return TaskTrainStatus{}, planErr
	}
	// Recover the durable relationship if a process exited after dispatch
	// committed but before the train record was bound to the new run.
	if status.CurrentRunID == "" && plan.ActiveTaskID == task.ID && plan.ActiveRunID != "" {
		run, runErr := s.RunRead(ctx, plan.ActiveRunID)
		if runErr != nil {
			return TaskTrainStatus{}, runErr
		}
		if run.TaskID != task.ID || !operationalActiveRun(run) {
			return TaskTrainStatus{}, fmt.Errorf("plan active run is not an operational run for task %s", task.ID)
		}
		train.CurrentRunID = run.ID
		if err := s.bindTaskTrainRun(ctx, train, run.ID, ""); err != nil {
			return TaskTrainStatus{}, err
		}
		status.CurrentRunID, status.CurrentRunStatus = run.ID, run.Status
		return s.taskTrainTail(ctx, status, in.Cursor)
	}
	if status.CurrentRunID != "" && operationalRunStatus(status.CurrentRunStatus) {
		return s.taskTrainTail(ctx, status, in.Cursor)
	}
	switch state.Status {
	case "created", "ready":
		if plan.ActiveTaskID != task.ID || plan.ActiveRunID != "" {
			return status, nil
		}
		run, operation, dispatchErr := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID})
		if dispatchErr != nil {
			return status, dispatchErr
		}
		train.CurrentRunID = run.ID
		if err := s.bindTaskTrainRun(ctx, train, run.ID, operation.Hub.After); err != nil {
			return TaskTrainStatus{}, err
		}
		status.CurrentRunID, status.CurrentRunStatus = run.ID, run.Status
		return s.taskTrainTail(ctx, status, "")
	case "completed", "merge_ready":
		if train.Status != model.TaskTrainWaitingDelivery {
			train.Status, train.WaitReason = model.TaskTrainWaitingDelivery, "delivery_review_or_merge_required"
			if err := s.updateTaskTrain(ctx, train, ""); err != nil {
				return TaskTrainStatus{}, err
			}
		}
		status.Status, status.WaitReason = model.TaskTrainWaitingDelivery, "delivery_review_or_merge_required"
		return status, nil
	case "merged":
		return s.advanceTaskTrain(ctx, train, status)
	case "failed", "cancelled", "superseded", "deferred":
		if train.Status != model.TaskTrainBlocked {
			train.Status, train.WaitReason = model.TaskTrainBlocked, "current_task_"+state.Status
			if err := s.updateTaskTrain(ctx, train, ""); err != nil {
				return TaskTrainStatus{}, err
			}
		}
		status.Status, status.WaitReason = model.TaskTrainBlocked, train.WaitReason
		return status, nil
	default:
		return status, fmt.Errorf("task train encountered unsupported task state %q", state.Status)
	}
}

func operationalRunStatus(status string) bool {
	return status == "created" || status == "dispatching" || status == "dispatched" || status == "awaiting_result" || status == "cancel_requested"
}

func (s *Service) taskTrainTail(ctx context.Context, status TaskTrainStatus, cursor string) (TaskTrainStatus, error) {
	if status.CurrentRunID == "" {
		return status, nil
	}
	run, err := s.RunRead(ctx, status.CurrentRunID)
	if err != nil {
		return status, err
	}
	local, err := s.projectConfig(status.ProjectID)
	if err != nil {
		return status, err
	}
	if session, sessionErr := s.Airelay.Status(ctx, local.AirelaySessionKey); sessionErr == nil {
		status.AgentState = session.State
	}
	tail, err := s.RunAgentTailPage(ctx, run.ID, AgentTailInput{Lines: 10, Cursor: cursor})
	if err != nil {
		status.WaitReason = "active_run_tail_unavailable"
		return status, nil
	}
	status.Tail, status.NextCursor, status.HasMore = tail.Text, tail.NextCursor, tail.HasMore
	return status, nil
}

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
		if current.CurrentIndex != next.CurrentIndex || current.CurrentTaskID != next.CurrentTaskID || current.CurrentRunID != next.CurrentRunID && next.CurrentRunID != "" && current.CurrentRunID != "" || current.Status != next.Status && next.Status != model.TaskTrainActive {
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
	run, _, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: tx.After}})
	if err != nil {
		return status, err
	}
	next.CurrentRunID = run.ID
	if err := s.bindTaskTrainRun(ctx, next, run.ID, ""); err != nil {
		return status, err
	}
	return s.taskTrainTail(ctx, TaskTrainStatus{ProjectID: next.ProjectID, TrainID: next.ID, Status: next.Status, CurrentIndex: next.CurrentIndex, TaskCount: len(next.TaskIDs), CurrentTaskID: next.CurrentTaskID, CurrentRunID: run.ID, CurrentRunStatus: run.Status, NextTaskID: nextTaskID(next)}, "")
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
