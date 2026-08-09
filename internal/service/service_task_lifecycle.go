package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskDefer(ctx context.Context, in TaskDeferInput) (OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status != "completed" && state.Status != "merge_ready" {
		return OperationResult{}, fmt.Errorf("task cannot be deferred from state %s", state.Status)
	}
	reason := strings.TrimSpace(in.Reason)
	if strings.ContainsRune(reason, '\x00') {
		return OperationResult{}, fmt.Errorf("deferred reason must not contain NUL")
	}
	if reason == "" {
		return OperationResult{}, fmt.Errorf("deferred reason must be non-empty")
	}
	if len([]byte(reason)) > model.MaxDeferredReasonBytes {
		return OperationResult{}, fmt.Errorf("deferred reason exceeds %d bytes", model.MaxDeferredReasonBytes)
	}
	reviewedHead := state.ReviewedHead
	if state.Status == "completed" {
		report, reportErr := s.latestSuccessfulReport(ctx, task)
		if reportErr != nil {
			return OperationResult{}, reportErr
		}
		reviewedHead = report.Repository.Head
	}
	if err := model.ValidateCommitSHA(reviewedHead); err != nil {
		return OperationResult{}, fmt.Errorf("reviewed head: %w", err)
	}
	tx, err := s.transitionTaskState(ctx, task, in.ExpectedHubRevision, "gateway: defer task "+task.ID, func(current model.TaskState) (model.TaskState, error) {
		switch current.Status {
		case "completed":
			if current.ReviewedHead != "" {
				return model.TaskState{}, fmt.Errorf("task acquired a reviewed head concurrently")
			}
			current.ReviewedHead = reviewedHead
		case "merge_ready":
			if current.ReviewedHead != reviewedHead {
				return model.TaskState{}, fmt.Errorf("reviewed head changed concurrently")
			}
		default:
			return model.TaskState{}, fmt.Errorf("task changed before defer: %s", current.Status)
		}
		current.Status = "deferred"
		current.DeferredReason = reason
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Status:    "deferred",
	}, nil
}

func (s *Service) TaskMarkMerged(ctx context.Context, in TaskMarkMergedInput) (OperationResult, error) {
	if err := model.ValidateCommitSHA(in.IntegrationHead); err != nil {
		return OperationResult{}, fmt.Errorf("integration_head: %w", err)
	}
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return OperationResult{}, err
	}
	if state.Status != "merge_ready" {
		return OperationResult{}, fmt.Errorf("task must be merge_ready before merged: %s", state.Status)
	}
	if err := model.ValidateCommitSHA(state.ReviewedHead); err != nil {
		return OperationResult{}, fmt.Errorf("reviewed head: %w", err)
	}
	project, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("read project workflow policy: %w", err)
	}
	if err := s.Git.Refresh(ctx, project); err != nil {
		return OperationResult{}, err
	}
	taskBranchHead, taskBranchExists, err := s.mirrorRemoteBranchHead(ctx, project, task.Branch)
	if err != nil {
		return OperationResult{}, err
	}
	if !taskBranchExists || taskBranchHead != state.ReviewedHead {
		return OperationResult{}, fmt.Errorf("remote task branch %q does not point at reviewed head", task.Branch)
	}
	integrationHead, integrationExists, err := s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
	if err != nil {
		return OperationResult{}, err
	}
	if !integrationExists || integrationHead != in.IntegrationHead {
		return OperationResult{}, fmt.Errorf("remote %s does not point at integration head", policy.IntegrationBranch)
	}
	ancestor, err := s.Git.MirrorAncestor(ctx, project, state.ReviewedHead, in.IntegrationHead)
	if err != nil {
		return OperationResult{}, err
	}
	if !ancestor {
		return OperationResult{}, fmt.Errorf("reviewed head is not an ancestor of integration head")
	}
	tx, err := s.transitionTaskState(ctx, task, in.ExpectedHubRevision, "gateway: record merged task "+task.ID, func(current model.TaskState) (model.TaskState, error) {
		if current.Status != "merge_ready" {
			return model.TaskState{}, fmt.Errorf("task changed before merged: %s", current.Status)
		}
		if current.ReviewedHead != state.ReviewedHead {
			return model.TaskState{}, fmt.Errorf("reviewed head changed concurrently")
		}
		current.Status = "merged"
		current.DeferredReason = ""
		current.IntegrationBranch = policy.IntegrationBranch
		current.IntegrationHead = in.IntegrationHead
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Status:    "merged",
	}, nil
}

func (s *Service) latestSuccessfulReport(ctx context.Context, task model.Task) (model.Report, error) {
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return model.Report{}, err
	}
	for _, run := range runs {
		if run.TaskID != task.ID || run.Historical || run.Status != "succeeded" {
			continue
		}
		report, reportErr := s.RunReport(ctx, run.ID)
		if reportErr != nil {
			return model.Report{}, fmt.Errorf("latest successful report is invalid: %w", reportErr)
		}
		if report.Status != "succeeded" {
			return model.Report{}, fmt.Errorf("latest successful run has non-success report")
		}
		return report, nil
	}
	return model.Report{}, fmt.Errorf("no canonical successful report for task %s", task.ID)
}

func (s *Service) transitionTaskState(ctx context.Context, task model.Task, expected, subject string, mutate func(model.TaskState) (model.TaskState, error)) (hub.TransactionResult, error) {
	return s.transitionTaskStateWithWorktree(ctx, task, expected, subject, func(_ string, current model.TaskState) (model.TaskState, error) {
		return mutate(current)
	})
}
