package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskMarkMergeReady(ctx context.Context, in TaskMarkMergeReadyInput) (OperationResult, error) {
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
	if state.Status != "completed" {
		return OperationResult{}, fmt.Errorf("task must be completed before merge_ready: %s", state.Status)
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return OperationResult{}, err
	}
	revision := 0
	var revisionSHA string
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		current, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return OperationResult{}, revisionErr
		}
		revision = current.TaskRevision
		revisionSHA = current.RevisionSHA256
	}
	latest, ok := latestApplicableRunForRevision(runs, task.ID, revision, revisionSHA)
	if !ok {
		return OperationResult{}, fmt.Errorf("no canonical successful report for task %s", task.ID)
	}
	if latest.Status != "succeeded" {
		return OperationResult{}, fmt.Errorf("latest applicable run %s is not succeeded: %s", latest.ID, latest.Status)
	}
	report, err := s.RunReport(ctx, latest.ID)
	if err != nil {
		return OperationResult{}, fmt.Errorf("latest successful report is invalid: %w", err)
	}
	if err := model.ValidateCommitSHA(report.Repository.Head); err != nil {
		return OperationResult{}, fmt.Errorf("successful report repository head: %w", err)
	}
	delivery, err := s.readFinalReviewReport(ctx, task, latest)
	if err != nil {
		return OperationResult{}, fmt.Errorf("latest run %s requires a finalized Delivery review: %w", latest.ID, err)
	}
	if delivery.RunID != latest.ID || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != latest.Branch || delivery.BaseRevision != latest.BaseRevision || delivery.Outcome != model.ReviewOutcomeAccepted {
		return OperationResult{}, fmt.Errorf("Delivery review outcome %q does not permit merge-ready", delivery.Outcome)
	}
	if delivery.ReviewedHead != report.Repository.Head {
		return OperationResult{}, fmt.Errorf("Delivery review head does not match successful Agent report")
	}
	tx, err := s.transitionTaskStateWithWorktree(ctx, task, in.ExpectedHubRevision, "gateway: mark task merge-ready "+task.ID, func(worktree string, current model.TaskState) (model.TaskState, error) {
		if current.Status != "completed" {
			return model.TaskState{}, fmt.Errorf("task changed before merge_ready: %s", current.Status)
		}
		currentLatest, found, err := s.latestApplicableRunInWorktree(worktree, task.ProjectID, task.ID)
		if err != nil {
			return model.TaskState{}, err
		}
		if !found || currentLatest.ID != latest.ID || currentLatest.Status != "succeeded" || currentLatest.TaskSHA256 != task.SHA256 || currentLatest.Branch != latest.Branch || currentLatest.BaseRevision != latest.BaseRevision {
			return model.TaskState{}, fmt.Errorf("latest applicable run changed before merge_ready")
		}
		var currentAgent model.Report
		if err := readWorktreeJSON(worktree, s.reportPath(task.ProjectID, currentLatest.ID), &currentAgent); err != nil {
			return model.TaskState{}, fmt.Errorf("Agent report changed before merge_ready: %w", err)
		}
		if err := model.ValidateReport(currentAgent, task, currentLatest, s.Config.MaxListItems); err != nil || currentAgent.Status != "succeeded" || !sameAgentAuthority(currentAgent, report) {
			return model.TaskState{}, fmt.Errorf("Agent report changed before merge_ready")
		}
		deliveryData, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.reviewReportPath(task.ProjectID, currentLatest.ID))))
		if err != nil {
			return model.TaskState{}, fmt.Errorf("Delivery review changed before merge_ready: %w", err)
		}
		currentDelivery, err := model.ParseRunReviewReport(deliveryData)
		if err != nil {
			return model.TaskState{}, fmt.Errorf("Delivery review changed before merge_ready: %w", err)
		}
		if err := model.ValidateRunReviewReport(currentDelivery); err != nil || currentDelivery.TaskID != task.ID || currentDelivery.RunID != currentLatest.ID || currentDelivery.ProjectID != task.ProjectID || currentDelivery.TaskSHA256 != task.SHA256 || currentDelivery.Branch != currentLatest.Branch || currentDelivery.BaseRevision != currentLatest.BaseRevision || currentDelivery.Outcome != model.ReviewOutcomeAccepted || currentDelivery.ReviewedHead != currentAgent.Repository.Head {
			return model.TaskState{}, fmt.Errorf("Delivery review no longer permits merge-ready")
		}
		current.Status = "merge_ready"
		current.ReviewedHead = currentAgent.Repository.Head
		return current, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Status:    "merge_ready",
	}, nil
}
