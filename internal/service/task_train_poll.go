package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
	tail, err := s.RunAgentTailPage(ctx, run.ID, AgentTailInput{
		Lines:  10,
		Cursor: cursor,
	})
	if err != nil {
		status.WaitReason = "active_run_tail_unavailable"
		return status, nil
	}
	status.Tail, status.NextCursor, status.HasMore = tail.Text, tail.NextCursor, tail.HasMore
	return status, nil
}
