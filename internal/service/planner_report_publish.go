package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validateCompletedDeliveryProofInWorktree(worktree string, s *Service, handoff model.DeliveryHandoff, expectedTask model.Task, expectedRun model.Run, expectedAgent model.Report, expectedDelivery model.RunReviewReport) error {
	var task model.Task
	if err := readWorktreeJSON(worktree, s.taskPath(handoff.ProjectID, handoff.TaskID), &task); err != nil {
		return fmt.Errorf("task changed before completed report: %w", err)
	}
	if err := model.ValidateTask(task); err != nil || task.ID != expectedTask.ID || task.SHA256 != expectedTask.SHA256 || task.ProjectID != expectedTask.ProjectID || task.Branch != expectedTask.Branch || task.BaseRevision != expectedTask.BaseRevision {
		return fmt.Errorf("task changed before completed report")
	}
	if err := model.ValidateTaskHash(task); err != nil {
		return fmt.Errorf("task hash changed before completed report")
	}
	var run model.Run
	if err := readWorktreeJSON(worktree, s.runPath(handoff.ProjectID, handoff.RunID), &run); err != nil {
		return fmt.Errorf("run changed before completed report: %w", err)
	}
	if err := model.ValidateRun(run); err != nil || run.ID != expectedRun.ID || run.TaskID != expectedRun.TaskID || run.ProjectID != expectedRun.ProjectID || run.TaskSHA256 != expectedRun.TaskSHA256 || run.Branch != expectedRun.Branch || run.BaseRevision != expectedRun.BaseRevision || run.Status != "succeeded" || operationalActiveRun(run) {
		return fmt.Errorf("run changed before completed report")
	}
	var agent model.Report
	if err := readWorktreeJSON(worktree, s.reportPath(handoff.ProjectID, handoff.RunID), &agent); err != nil {
		return fmt.Errorf("Agent report changed before completed report: %w", err)
	}
	if err := model.ValidateReport(agent, task, run, s.Config.MaxListItems); err != nil || agent.Status != "succeeded" || !sameAgentAuthority(agent, expectedAgent) {
		return fmt.Errorf("Agent report changed before completed report")
	}
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.reviewReportPath(handoff.ProjectID, handoff.RunID))))
	if err != nil {
		return fmt.Errorf("Delivery report changed before completed report: %w", err)
	}
	delivery, err := model.ParseRunReviewReport(data)
	if err != nil || model.ValidateRunReviewReport(delivery) != nil {
		return fmt.Errorf("Delivery report changed before completed report")
	}
	if delivery.ID != expectedDelivery.ID || delivery.TaskID != task.ID || delivery.RunID != run.ID || delivery.ProjectID != task.ProjectID || delivery.TaskSHA256 != task.SHA256 || delivery.Branch != run.Branch || delivery.BaseRevision != run.BaseRevision || delivery.Outcome != model.ReviewOutcomeAccepted || delivery.ReviewedHead != agent.Repository.Head {
		return fmt.Errorf("Delivery report proof changed before completed report")
	}
	var state model.TaskState
	if err := readWorktreeJSON(worktree, s.taskStatePath(handoff.ProjectID, handoff.TaskID), &state); err != nil {
		return fmt.Errorf("task state changed before completed report: %w", err)
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return fmt.Errorf("task state changed before completed report")
	}
	switch state.Status {
	case "completed", "merge_ready", "merged":
	default:
		return fmt.Errorf("task state changed before completed report")
	}
	return nil
}
