package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// validateReviewPublicationInWorktree rechecks every mutable input that the
// Delivery report depends on inside the Hub transaction. The same helper is
// used by draft finalization and the one-shot task/review action so the two
// entry points cannot drift apart.
func (s *Service) validateReviewPublicationInWorktree(worktree string, review reviewContext) (model.TaskState, error) {
	var currentTask model.Task
	if err := readWorktreeJSON(worktree, s.taskPath(review.task.ProjectID, review.task.ID), &currentTask); err != nil {
		return model.TaskState{}, err
	}
	if err := model.ValidateTask(currentTask); err != nil || currentTask.ID != review.task.ID || currentTask.SHA256 != review.task.SHA256 {
		return model.TaskState{}, fmt.Errorf("task changed before delivery review publication")
	}
	if err := model.ValidateTaskHash(currentTask); err != nil || currentTask.SHA256 != review.task.SHA256 {
		return model.TaskState{}, fmt.Errorf("task hash changed before delivery review publication")
	}

	var currentRun model.Run
	if err := readWorktreeJSON(worktree, s.runPath(review.task.ProjectID, review.run.ID), &currentRun); err != nil {
		return model.TaskState{}, err
	}
	if err := model.ValidateRun(currentRun); err != nil || currentRun.Historical || currentRun.ID != review.run.ID || currentRun.TaskID != review.task.ID || currentRun.TaskSHA256 != review.task.SHA256 || currentRun.TaskRevision != review.run.TaskRevision || currentRun.TaskRevisionSHA256 != review.run.TaskRevisionSHA256 || currentRun.TaskRunNumber != review.run.TaskRunNumber || currentRun.ProjectID != review.task.ProjectID || currentRun.Branch != review.run.Branch || currentRun.BaseRevision != review.run.BaseRevision || operationalActiveRun(currentRun) {
		return model.TaskState{}, fmt.Errorf("run changed or is still operational before delivery review publication")
	}

	var currentAgent model.Report
	if err := readWorktreeJSON(worktree, s.reportPath(review.task.ProjectID, review.run.ID), &currentAgent); err != nil {
		return model.TaskState{}, err
	}
	if err := model.ValidateReport(currentAgent, currentTask, currentRun, s.Config.MaxListItems); err != nil {
		return model.TaskState{}, fmt.Errorf("Agent report changed before delivery review publication: %w", err)
	}
	if currentAgent.TaskID != review.task.ID || currentAgent.RunID != review.run.ID || currentAgent.TaskRevision != review.run.TaskRevision || currentAgent.TaskRevisionSHA256 != review.run.TaskRevisionSHA256 || currentAgent.TaskRunNumber != review.run.TaskRunNumber || currentAgent.ProjectID != review.task.ProjectID || currentAgent.Repository.Head != review.head || currentAgent.Repository.Branch != review.branch || currentAgent.Repository.DiffScope != review.run.BaseRevision+".."+review.head || currentAgent.Status != currentRun.Status || !sameAgentAuthority(currentAgent, review.agent) {
		return model.TaskState{}, fmt.Errorf("Agent report changed before delivery review publication")
	}

	var state model.TaskState
	if err := readWorktreeJSON(worktree, s.taskStatePath(review.task.ProjectID, review.task.ID), &state); err != nil {
		return model.TaskState{}, err
	}
	if err := model.ValidateTaskState(state, currentTask); err != nil {
		return model.TaskState{}, err
	}
	reportPath := filepath.Join(worktree, filepath.FromSlash(s.reviewReportPath(review.task.ProjectID, review.run.ID)))
	if _, err := os.Lstat(reportPath); err == nil {
		return model.TaskState{}, fmt.Errorf("delivery review report already finalized")
	} else if !os.IsNotExist(err) {
		return model.TaskState{}, err
	}
	return state, nil
}
