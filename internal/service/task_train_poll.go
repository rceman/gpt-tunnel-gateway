package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskTrainPoll(ctx context.Context, in TaskTrainPollInput) (TaskTrainStatus, error) {
	var train model.TaskTrain
	var err error
	if in.TrainID != "" {
		train, err = s.TaskTrainReadByID(ctx, in.ProjectID, in.TrainID)
	} else {
		train, err = s.TaskTrainRead(ctx, in.ProjectID)
	}
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
		run, operation, dispatchErr := s.TaskDispatch(ctx, DispatchInput{
			TaskID:     task.ID,
			TrainID:    train.TrainID,
			LaneBranch: train.LaneBranch,
		})
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
	return s.superviseTaskTrainRun(ctx, status, run)
}

const taskTrainStallReprompt = "Execution stall detected. Re-read the durable task packet and run state, then continue the existing scope toward verification and finalization."

func (s *Service) superviseTaskTrainRun(ctx context.Context, status TaskTrainStatus, run model.Run) (TaskTrainStatus, error) {
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		return status, err
	}
	observation := inspectCompaction(run, status.Tail, events)
	if observation.Detected && latestEvent(events, model.EventResumeSent, observation.EventID) == nil && !hasExplicitQuestion(status.Tail) {
		if _, resumeErr := s.RunResume(ctx, run.ID); resumeErr != nil {
			status.WaitReason = "compaction_resume_failed"
			return status, nil
		}
		status.AgentState = model.AgentStateCompactedResuming
		status.WaitReason = "compaction_resume_sent"
		return status, nil
	}
	if !taskTrainExplicitStall(status.AgentState, status.Tail, observation, events) || hasExplicitQuestion(status.Tail) {
		return status, nil
	}
	if run.RepromptCount > 0 {
		status.WaitReason = "execution_stall_reprompt_already_sent"
		return status, nil
	}
	if len(taskTrainStallReprompt) > s.Airelay.MaxMessageBytes {
		return status, fmt.Errorf("task train stall reprompt exceeds bound")
	}
	now := time.Now().UTC()
	run.RepromptCount = 1
	run.LastRepromptAt = &now
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return status, err
	}
	if _, err := s.updateRun(ctx, run, expected, "watcher: reserve task train stall reprompt "+run.ID); err != nil {
		return status, err
	}
	receipt, sendErr := s.AgentSend(ctx, run.ProjectID, taskTrainStallReprompt)
	if sendErr != nil || !receipt.Delivered {
		status.WaitReason = "execution_stall_reprompt_failed"
		return status, nil
	}
	status.WaitReason = "execution_stall_reprompt_sent"
	return status, nil
}

func taskTrainExplicitStall(state, tail string, observation compactionObservation, events []model.RunOperationalEvent) bool {
	if state == model.AgentStateStalled || strings.Contains(strings.ToLower(tail), "execution stalled") || strings.Contains(strings.ToLower(tail), "execution stall") {
		return true
	}
	if !observation.Detected {
		return false
	}
	return latestEvent(events, model.EventStalledAfterCompaction, observation.EventID) != nil
}
