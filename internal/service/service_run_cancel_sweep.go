package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) RunCancelAcknowledgeNoMutation(ctx context.Context, id, expected string) (OperationResult, error) {
	if err := requireCanonicalRunID(id); err != nil {
		return OperationResult{}, err
	}
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	if run.Status != "cancel_requested" {
		return OperationResult{}, fmt.Errorf("run status must be cancel_requested")
	}
	if err := validateCancelDelivery(run); err != nil {
		return OperationResult{}, err
	}

	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateTask(task); err != nil {
		return OperationResult{}, err
	}
	if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
		return OperationResult{}, fmt.Errorf("cancelled run task identity does not match")
	}
	if run.Branch != task.Branch {
		return OperationResult{}, fmt.Errorf("cancelled run repository identity does not match task")
	}
	if hashErr := model.ValidateTaskHash(task); hashErr != nil {
		return OperationResult{}, fmt.Errorf("durable task hash mismatch")
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	defer projectLock.Release()
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return OperationResult{}, err
	}
	if state.Status != "dispatched" {
		return OperationResult{}, fmt.Errorf("task state must be dispatched")
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return OperationResult{}, err
	}
	if plan.ActiveTaskID != task.ID || plan.ActiveRunID != run.ID {
		return OperationResult{}, fmt.Errorf("plan does not own cancelled task and run")
	}
	completionExists, err := os.Lstat(run.CompletionPath)
	if err == nil {
		_ = completionExists
		return OperationResult{}, fmt.Errorf("completion file already exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return OperationResult{}, err
	}
	if err := s.validateCancelNoMutationWorktree(ctx, task, run.BaseRevision); err != nil {
		return OperationResult{}, err
	}
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return OperationResult{}, err
		}
	}
	deliveryExitCode := *run.DispatchExitCode
	deliveryStdout := run.DispatchStdout
	deliveryStderr := run.DispatchStderr

	now := time.Now().UTC()
	tx, err := s.Hub.Transact(ctx, expected, "gateway: acknowledge cancellation without mutation "+run.ID, func(worktree string) ([]string, error) {
		currentRun, err := readCurrentRun(worktree, s.runPath(run.ProjectID, run.ID), s.Config.MaxReadBytes)
		if err != nil {
			return nil, err
		}
		currentTask := model.Task{}
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &currentTask); err != nil {
			return nil, err
		}
		currentState := model.TaskState{}
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		currentPlan := model.Plan{}
		if err := readWorktreeJSON(worktree, s.planPath(task.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if err := model.ValidateRun(currentRun); err != nil {
			return nil, err
		}
		if currentRun.ID != run.ID || currentRun.TaskID != task.ID || currentRun.TaskSHA256 != task.SHA256 || currentRun.ProjectID != task.ProjectID || currentRun.CompletionPath != run.CompletionPath || currentRun.Branch != task.Branch || currentRun.BaseRevision != run.BaseRevision {
			return nil, fmt.Errorf("run changed before cancellation acknowledgement")
		}
		if err := requireCanonicalRun(currentRun); err != nil {
			return nil, err
		}
		if currentRun.Status != "cancel_requested" {
			return nil, fmt.Errorf("run changed before cancellation acknowledgement")
		}
		if err := validateCancelDelivery(currentRun); err != nil {
			return nil, err
		}
		if currentRun.DispatchExitCode == nil || *currentRun.DispatchExitCode != deliveryExitCode || currentRun.DispatchStdout != deliveryStdout || currentRun.DispatchStderr != deliveryStderr {
			return nil, fmt.Errorf("cancellation delivery evidence changed before acknowledgement")
		}
		if err := model.ValidateTask(currentTask); err != nil {
			return nil, err
		}
		if currentTask.ID != task.ID || currentTask.ProjectID != task.ProjectID || currentTask.SHA256 != task.SHA256 {
			return nil, fmt.Errorf("task changed before cancellation acknowledgement")
		}
		if hashErr := model.ValidateTaskHash(currentTask); hashErr != nil {
			return nil, fmt.Errorf("durable task hash mismatch")
		}
		if err := model.ValidateTaskState(currentState, currentTask); err != nil {
			return nil, err
		}
		if currentState.Status != "dispatched" {
			return nil, fmt.Errorf("task state changed before cancellation acknowledgement")
		}
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		if currentPlan.ProjectID != task.ProjectID || currentPlan.ActiveTaskID != task.ID || currentPlan.ActiveRunID != run.ID {
			return nil, fmt.Errorf("plan changed before cancellation acknowledgement")
		}

		currentRun.Status = "failed"
		currentRun.FinishedAt = &now
		currentState.Status = taskStateStatusForResult("failed")
		currentState.UpdatedAt = now
		currentPlan.Revision++
		currentPlan.ActiveTaskID = ""
		currentPlan.ActiveRunID = ""
		currentPlan.UpdatedBy = s.Config.GatewayID
		currentPlan.UpdatedAt = now
		if err := model.ValidateRun(currentRun); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidatePlan(currentPlan); err != nil {
			return nil, err
		}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		values := []any{currentRun, currentState, currentPlan}
		for i, path := range paths {
			if err := hub.WriteJSON(worktree, path, values[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: run.ProjectID,
		TaskID:    run.TaskID,
		RunID:     run.ID,
		Status:    "cancelled_no_mutation",
	}, nil
}

type SweepItem struct {
	RunID  string `json:"run_id"`
	Action string `json:"action"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type SweepResult struct {
	Checked int         `json:"checked"`
	Items   []SweepItem `json:"items"`
}
