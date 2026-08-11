package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskDispatchOnce(ctx context.Context, in DispatchInput) (model.Run, OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if in.TrainID != "" {
		if err := model.ValidateObjectIdentifier(in.TrainID); err != nil {
			return model.Run{}, OperationResult{}, err
		}
	}
	if in.LaneBranch != "" {
		if err := model.ValidateBranch(in.LaneBranch); err != nil {
			return model.Run{}, OperationResult{}, err
		}
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	revision, err := s.currentTaskRevision(ctx, task)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	revisionAware := revision.TaskRevision > 1
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if state.Status != "created" && state.Status != "ready" {
		return model.Run{}, OperationResult{}, fmt.Errorf("task is not dispatchable: %s", state.Status)
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	resolved, err := s.ResolveAgent(ctx, AgentResolveInput{
		ProjectID:            task.ProjectID,
		Role:                 model.AgentRoleCoding,
		AgentID:              in.AgentID,
		RecommendedReasoning: in.RecommendedReasoning,
		RequireUsable:        true,
	})
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	resolvedReasoning := resolved.ResolvedReasoning
	fallback := resolved.Fallback
	fallbackReason := resolved.FallbackReason
	if in.ResolvedReasoning != "" {
		resolvedReasoning = in.ResolvedReasoning
	}
	if in.AgentFallback {
		fallback = true
		fallbackReason = in.AgentFallbackReason
	}
	sessionLock, err := s.acquireSessionSendLock(resolved.SessionKey)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	defer sessionLock.Release()
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+task.ProjectID)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	defer projectLock.Release()
	if err := s.checkSessionAvailableForRun(ctx, resolved.SessionKey, in.TrainID, in.LaneBranch); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	executionBase, err := s.dispatchExecutionBase(ctx, task, revision, local)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if in.ExpectedHubRevision == "" {
		in.ExpectedHubRevision, err = s.hubRevision(ctx)
		if err != nil {
			return model.Run{}, OperationResult{}, err
		}
	}
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if !wt.Clean {
		return model.Run{}, OperationResult{}, fmt.Errorf("project worktree is dirty")
	}
	resolvedBase, err := s.Git.Resolve(ctx, local.Root, executionBase)
	if err != nil || resolvedBase != executionBase {
		return model.Run{}, OperationResult{}, fmt.Errorf("execution base unavailable or mismatched")
	}
	var counter model.TaskRunCounter
	if err := s.Hub.ReadJSON(ctx, s.taskRunCounterPath(task.ProjectID, task.ID), &counter); err != nil {
		return model.Run{}, OperationResult{}, fmt.Errorf("read task run counter: %w", err)
	}
	if err := model.ValidateTaskRunCounter(counter); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := validateTaskRunCounterIdentity(counter, task); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	id, err := model.FormatRunID(task.ID, counter.NextRunNumber)
	if revisionAware {
		revisionID, revisionErr := model.FormatTaskRevisionID(task.ID, revision.TaskRevision)
		if revisionErr != nil {
			return model.Run{}, OperationResult{}, revisionErr
		}
		id, err = model.FormatTaskRevisionRunID(revisionID, counter.NextRunNumber)
	}
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if counter.NextRunNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.runPath(task.ProjectID, id)); readErr == nil {
			return model.Run{}, OperationResult{}, fmt.Errorf("run allocator exhausted for task %q", task.ID)
		} else if !IsNotFound(readErr) {
			return model.Run{}, OperationResult{}, readErr
		}
	}
	completionPath, err := canonicalCompletionDestination(s.Config.StateDir, id)
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run := model.Run{SchemaVersion: model.SchemaVersion, ID: id, TaskID: task.ID, TaskSHA256: task.SHA256, ProjectID: task.ProjectID, GatewayID: s.Config.GatewayID, SessionKey: resolved.SessionKey, AgentID: resolved.AgentID, RequestedReasoning: resolved.RequestedReasoning, ResolvedReasoning: resolvedReasoning, AgentFallback: fallback, AgentFallbackReason: fallbackReason, Branch: revision.Branch, TrainID: in.TrainID, LaneBranch: in.LaneBranch, BaseRevision: executionBase, Status: "created", CompletionPath: completionPath, CreatedAt: now}
	if revisionAware {
		run.TaskRevision, run.TaskRevisionSHA256, run.TaskRunNumber = revision.TaskRevision, revision.RevisionSHA256, counter.NextRunNumber
	}
	if err := model.ValidateRun(run); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if (revisionAware && model.ValidateTaskRevisionRunID(run.ID) != nil) || (!revisionAware && model.ValidateCanonicalRunID(run.ID) != nil) {
		return model.Run{}, OperationResult{}, fmt.Errorf("invalid run identity")
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create run "+run.ID, func(w string) ([]string, error) {
		var currentPlan model.Plan
		if err := readWorktreeJSON(w, s.planPath(task.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		var currentState model.TaskState
		if err := readWorktreeJSON(w, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, task); err != nil {
			return nil, err
		}
		if currentState.Status != "created" && currentState.Status != "ready" {
			return nil, fmt.Errorf("task state changed before dispatch: %s", currentState.Status)
		}
		focusPlan := currentPlan.ActiveRunID == "" && (currentPlan.ActiveTaskID == "" || currentPlan.ActiveTaskID == task.ID)
		paths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID)}
		if focusPlan {
			paths = append(paths, s.planPath(task.ProjectID))
		}
		var currentCounter model.TaskRunCounter
		if err := readWorktreeJSON(w, s.taskRunCounterPath(task.ProjectID, task.ID), &currentCounter); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskRunCounter(currentCounter); err != nil {
			return nil, err
		}
		if err := validateTaskRunCounterIdentity(currentCounter, task); err != nil {
			return nil, err
		}
		if currentCounter.NextRunNumber != counter.NextRunNumber {
			return nil, fmt.Errorf("task run counter changed before dispatch")
		}
		if currentCounter.NextRunNumber < model.MaxSafeInteger {
			currentCounter.NextRunNumber++
		}
		if err := model.ValidateTaskRunCounter(currentCounter); err != nil {
			return nil, err
		}
		paths = append(paths, s.taskRunCounterPath(task.ProjectID, task.ID))
		counter = currentCounter
		if err := ensureSessionAvailableInWorktreeForRun(w, run.SessionKey, run.TrainID, run.LaneBranch, s.Config.MaxReadBytes); err != nil {
			return nil, err
		}
		if focusPlan {
			currentPlan.Revision++
			currentPlan.ActiveTaskID = task.ID
			currentPlan.ActiveRunID = run.ID
			currentPlan.UpdatedBy = s.Config.GatewayID
			currentPlan.UpdatedAt = now
		}
		currentState.Status = "dispatched"
		currentState.UpdatedAt = now
		vals := []any{run, currentState}
		basePaths := []string{s.runPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID)}
		if focusPlan {
			vals = append(vals, currentPlan)
			basePaths = append(basePaths, s.planPath(task.ProjectID))
		}
		for i, path := range basePaths {
			if err := hub.WriteJSON(w, path, vals[i]); err != nil {
				return nil, err
			}
		}
		if err := hub.WriteJSON(w, s.taskRunCounterPath(task.ProjectID, task.ID), counter); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return model.Run{}, OperationResult{}, err
	}
	run.HubRevision = tx.After
	if err := s.writeLocalRun(run, task); err != nil {
		return model.Run{}, OperationResult{}, err
	}
	if err := s.Git.PrepareBranch(ctx, local, revision.Branch, executionBase); err != nil {
		_, _ = s.failRun(ctx, run, task, "failed", "repository preparation failed: "+err.Error(), tx.After)
		return run, OperationResult{}, err
	}
	message := "Read task and execute it. Run: gpt-tunnel task read " + task.ID + ". Do not stop at reading or summarizing: implement the task, run its required gates, write completion, and finalize until TASK_FINALIZED; if execution is blocked, report the explicit blocker."
	run.Status = "dispatching"
	run.DispatchMessage = message
	dispatch, err := s.Airelay.Prompt(ctx, run.SessionKey, message)
	code := dispatch.ExitCode
	run.DispatchExitCode = &code
	run.DispatchStdout = dispatch.Stdout
	run.DispatchStderr = dispatch.Stderr
	dispatchedAt := dispatch.FinishedAt
	run.DispatchedAt = &dispatchedAt
	if err != nil {
		tx2, e := s.failRun(ctx, run, task, "failed", "Airelay dispatch failed: "+err.Error(), tx.After)
		if e != nil {
			return run, OperationResult{}, fmt.Errorf("dispatch failed (%v), recording failed (%v)", err, e)
		}
		run.Status = "failed"
		return run, OperationResult{
			Hub:       tx2,
			ProjectID: run.ProjectID,
			TaskID:    run.TaskID,
			RunID:     run.ID,
			Status:    run.Status,
		}, err
	}
	run.Status = "awaiting_result"
	tx2, err := s.updateRun(ctx, run, tx.After, "gateway: dispatch run "+run.ID)
	if err != nil {
		return run, OperationResult{}, err
	}
	_ = s.writeLocalRun(run, task)
	return run, OperationResult{
		Hub:       tx2,
		ProjectID: run.ProjectID,
		TaskID:    run.TaskID,
		RunID:     run.ID,
		Status:    run.Status,
	}, nil
}
