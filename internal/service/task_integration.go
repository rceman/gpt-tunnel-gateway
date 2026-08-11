package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validateTaskIntegrationReceipt(receipt TaskIntegrationReceipt) error {
	if err := model.ValidateCanonicalTaskID(receipt.TaskID); err != nil {
		return err
	}
	for name, value := range map[string]string{"reviewed_head": receipt.ReviewedHead, "integration_head": receipt.IntegrationHead, "runtime_source_head": receipt.RuntimeSourceHead} {
		if err := model.ValidateCommitSHA(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	for name, value := range map[string]string{"pre_activation": receipt.PreActivation, "pre_smoke": receipt.PreSmoke, "post_activation": receipt.PostActivation, "post_smoke": receipt.PostSmoke} {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if strings.TrimSpace(receipt.NextAction) == "" {
		return fmt.Errorf("next_action is required")
	}
	return nil
}

func (s *Service) readTaskIntegrationReceipt(ctx context.Context, project, taskID string) (TaskIntegrationReceipt, error) {
	var receipt TaskIntegrationReceipt
	if err := s.Hub.ReadJSON(ctx, s.taskIntegrationReceiptPath(project, taskID), &receipt); err != nil {
		return TaskIntegrationReceipt{}, err
	}
	if err := validateTaskIntegrationReceipt(receipt); err != nil {
		return TaskIntegrationReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) recordTaskIntegrationReceipt(ctx context.Context, project, taskID string, receipt TaskIntegrationReceipt) error {
	if err := validateTaskIntegrationReceipt(receipt); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: record task integration receipt "+taskID, func(worktree string) ([]string, error) {
		path := s.taskIntegrationReceiptPath(project, taskID)
		if err := hub.WriteJSON(worktree, path, receipt); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) latestAcceptedDelivery(ctx context.Context, task model.Task, reviewedHead string) (model.Run, model.RunReviewReport, error) {
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return model.Run{}, model.RunReviewReport{}, err
	}
	revision := 0
	var revisionSHA string
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		current, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return model.Run{}, model.RunReviewReport{}, revisionErr
		}
		revision = current.TaskRevision
		revisionSHA = current.RevisionSHA256
	}
	run, ok := latestApplicableRunForRevision(runs, task.ID, revision, revisionSHA)
	if !ok || run.Status != "succeeded" {
		return model.Run{}, model.RunReviewReport{}, fmt.Errorf("no canonical successful run for task %s", task.ID)
	}
	report, err := s.readFinalReviewReport(ctx, task, run)
	if err != nil {
		return model.Run{}, model.RunReviewReport{}, err
	}
	if report.Outcome != model.ReviewOutcomeAccepted || report.ReviewedHead != reviewedHead {
		return model.Run{}, model.RunReviewReport{}, fmt.Errorf("latest Delivery review does not match reviewed head")
	}
	return run, report, nil
}

func (s *Service) TaskIntegrate(ctx context.Context, in TaskIntegrationInput) (TaskIntegrationReceipt, OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	if state.Status == "merged" {
		receipt, readErr := s.readTaskIntegrationReceipt(ctx, task.ProjectID, task.ID)
		if readErr != nil {
			return TaskIntegrationReceipt{}, OperationResult{}, readErr
		}
		return receipt, OperationResult{
			ProjectID: task.ProjectID,
			TaskID:    task.ID,
			Status:    "merged",
		}, nil
	}
	if state.Status != "merge_ready" {
		return TaskIntegrationReceipt{}, OperationResult{}, fmt.Errorf("task must be merge_ready before integrate: %s", state.Status)
	}
	_, _, err = s.latestAcceptedDelivery(ctx, task, state.ReviewedHead)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	project, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.Git.Refresh(ctx, project); err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	taskHead, exists, err := s.Git.MirrorBranchHead(ctx, project, task.Branch)
	if err != nil || !exists || taskHead != state.ReviewedHead {
		return TaskIntegrationReceipt{}, OperationResult{}, fmt.Errorf("remote task branch does not point at reviewed head")
	}
	integrationHead, exists, err := s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
	if err != nil || !exists {
		return TaskIntegrationReceipt{}, OperationResult{}, fmt.Errorf("remote integration branch is unavailable")
	}
	receipt := TaskIntegrationReceipt{
		TaskID:            task.ID,
		ReviewedHead:      state.ReviewedHead,
		IntegrationHead:   integrationHead,
		RuntimeSourceHead: state.ReviewedHead,
		PreActivation:     "reused",
		PreSmoke:          "reused",
		PostActivation:    "pending",
		PostSmoke:         "pending",
		NextAction:        "activate_post_merge",
	}
	if integrationHead != state.ReviewedHead {
		ancestor, ancestorErr := s.Git.MirrorAncestor(ctx, project, integrationHead, state.ReviewedHead)
		if ancestorErr != nil {
			return TaskIntegrationReceipt{}, OperationResult{}, ancestorErr
		}
		if !ancestor {
			receipt.IntegrationConflict = "integration branch is not an ancestor of reviewed head"
			receipt.NextAction = "resolve_integration_conflict"
			_ = s.recordTaskIntegrationReceipt(ctx, task.ProjectID, task.ID, receipt)
			return receipt, OperationResult{
				ProjectID: task.ProjectID,
				TaskID:    task.ID,
				Status:    "integration_conflict",
			}, fmt.Errorf("integration branch diverged from reviewed head")
		}
		if s.taskActivator == nil {
			return receipt, OperationResult{}, fmt.Errorf("task activation is not configured")
		}
		pre, activateErr := s.taskActivator(ctx, project, state.ReviewedHead)
		if activateErr != nil {
			receipt.PreActivation = "failed"
			receipt.PreSmoke = "failed"
			receipt.ActivationBlocker = boundedTaskOutput([]byte(activateErr.Error()))
			receipt.NextAction = "retry_pre_merge_activation"
			_ = s.recordTaskIntegrationReceipt(ctx, task.ProjectID, task.ID, receipt)
			return receipt, OperationResult{
				ProjectID: task.ProjectID,
				TaskID:    task.ID,
				Status:    "pre_merge_activation_blocked",
			}, activateErr
		}
		receipt.PreActivation, receipt.PreSmoke, receipt.RuntimeSourceHead = pre.Activation, pre.Smoke, pre.SourceHead
		if err := s.Git.PushFastForward(ctx, project, policy.IntegrationBranch, integrationHead, state.ReviewedHead); err != nil {
			return receipt, OperationResult{}, fmt.Errorf("fast-forward integration push failed: %w", err)
		}
		if err := s.Git.Refresh(ctx, project); err != nil {
			return receipt, OperationResult{}, err
		}
		integrationHead, exists, err = s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
		if err != nil || !exists || integrationHead != state.ReviewedHead {
			return receipt, OperationResult{}, fmt.Errorf("integration branch did not reach reviewed head")
		}
	}
	if s.taskActivator == nil {
		return receipt, OperationResult{}, fmt.Errorf("task activation is not configured")
	}
	post, activateErr := s.taskActivator(ctx, project, state.ReviewedHead)
	if activateErr != nil {
		receipt.IntegrationHead = integrationHead
		receipt.PostActivation = "failed"
		receipt.PostSmoke = "failed"
		receipt.ActivationBlocker = boundedTaskOutput([]byte(activateErr.Error()))
		receipt.NextAction = "retry_post_merge_activation"
		_ = s.recordTaskIntegrationReceipt(ctx, task.ProjectID, task.ID, receipt)
		return receipt, OperationResult{
			ProjectID: task.ProjectID,
			TaskID:    task.ID,
			Status:    "integrated_pending_activation",
		}, activateErr
	}
	receipt.IntegrationHead = integrationHead
	receipt.RuntimeSourceHead = post.SourceHead
	receipt.PostActivation, receipt.PostSmoke = post.Activation, post.Smoke
	receipt.Merged = true
	receipt.NextAction = "complete"
	if err := validateTaskIntegrationReceipt(receipt); err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: integrate task "+task.ID, func(worktree string) ([]string, error) {
		var currentTask model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &currentTask); err != nil {
			return nil, err
		}
		var currentState model.TaskState
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, currentTask); err != nil || currentState.Status != "merge_ready" || currentState.ReviewedHead != state.ReviewedHead {
			return nil, fmt.Errorf("task changed before integration receipt")
		}
		currentState.Status = "merged"
		currentState.IntegrationBranch = policy.IntegrationBranch
		currentState.IntegrationHead = integrationHead
		currentState.UpdatedAt = time.Now().UTC()
		if err := model.ValidateTaskState(currentState, currentTask); err != nil {
			return nil, err
		}
		receiptPath := s.taskIntegrationReceiptPath(task.ProjectID, task.ID)
		statePath := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, statePath, currentState); err != nil {
			return nil, err
		}
		return []string{receiptPath, statePath}, nil
	})
	if err != nil {
		return TaskIntegrationReceipt{}, OperationResult{}, err
	}
	return receipt, OperationResult{
		Hub:       tx,
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Status:    "merged",
	}, nil
}
