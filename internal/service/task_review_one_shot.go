package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func reviewNextAction(outcome string) string {
	switch outcome {
	case model.ReviewOutcomeAccepted:
		return "reviewed_merge_ready"
	case model.ReviewOutcomeRejected:
		return "create_bounded_correction"
	case model.ReviewOutcomeBlocked:
		return "planner_decision_required"
	default:
		return "continue_delivery_review"
	}
}

// TaskReview publishes a complete Delivery report and, for the accepted
// outcome, advances the task state in the same Hub transaction. It is the
// normal closeout path; draft APIs remain available for bounded recovery.
func (s *Service) TaskReview(ctx context.Context, in TaskReviewInput) (model.RunReviewReport, OperationResult, error) {
	if err := validateTaskReviewSemanticInput(in); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	lock, err := s.reviewReportLock(in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	defer lock.Release()
	if err := model.ValidateReviewOutcome(in.Outcome); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	if run, err := s.findRun(ctx, in.RunID); err == nil && run.TrainID != "" {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("Train-v2 review requires the exact Train item Attempt; Run review is not a canonical action")
	}
	review, err := s.loadReviewContext(ctx, in.TaskID, in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	if review.run.Status != "succeeded" && in.Outcome == model.ReviewOutcomeAccepted {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("accepted delivery review requires a succeeded Agent run")
	}
	if exists, err := s.reviewReportExists(ctx, review.task.ProjectID, review.run.ID); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	} else if exists {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("delivery review report already finalized")
	}

	report := model.RunReviewReport{
		SchemaVersion:           model.RunReviewReportSchemaVersion,
		ID:                      model.NewRunReviewReportID(review.run.ID),
		TaskID:                  review.task.ID,
		RunID:                   review.run.ID,
		ProjectID:               review.task.ProjectID,
		TaskSHA256:              review.task.SHA256,
		TaskRevision:            review.run.TaskRevision,
		TaskRevisionSHA256:      review.run.TaskRevisionSHA256,
		TaskRunNumber:           review.run.TaskRunNumber,
		Branch:                  review.branch,
		BaseRevision:            review.run.BaseRevision,
		ReviewedHead:            review.head,
		Outcome:                 in.Outcome,
		RepositoryState:         review.repository,
		Gates:                   append([]model.CompletionGateResult{}, review.gates...),
		ServerGateResults:       append([]model.CompletionGateResult{}, review.serverGates...),
		Findings:                append([]model.ReviewFinding{}, in.Findings...),
		ScopeCoverage:           append([]model.ReviewScopeCoverage{}, in.ScopeCoverage...),
		ChangedFiles:            append([]string{}, review.changed...),
		UnexpectedSurfaces:      append([]string{}, in.UnexpectedSurfaces...),
		HistoricalCompatibility: append([]string{}, in.HistoricalCompatibility...),
		ProhibitedActions:       append([]string{}, in.ProhibitedActions...),
		NextAction:              reviewNextAction(in.Outcome),
		FinishedAt:              time.Now().UTC(),
	}
	if err := model.ValidateRunReviewReport(report); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	reportPath := s.reviewReportPath(review.task.ProjectID, review.run.ID)
	accepted := in.Outcome == model.ReviewOutcomeAccepted
	tx, err := s.Hub.Transact(ctx, expected, "gateway: one-shot delivery review "+report.ID, func(worktree string) ([]string, error) {
		state, err := s.validateReviewPublicationInWorktree(worktree, review)
		if err != nil {
			return nil, err
		}
		if state.Status != "completed" {
			return nil, fmt.Errorf("task must be completed before delivery review: %s", state.Status)
		}
		paths := []string{reportPath}
		if err := hub.WriteJSON(worktree, reportPath, report); err != nil {
			return nil, err
		}
		if accepted {
			state.Status = "merge_ready"
			state.ReviewedHead = review.head
			state.UpdatedAt = time.Now().UTC()
			if err := model.ValidateTaskState(state, review.task); err != nil {
				return nil, err
			}
			statePath := s.taskStatePath(review.task.ProjectID, review.task.ID)
			if err := hub.WriteJSON(worktree, statePath, state); err != nil {
				return nil, err
			}
			paths = append(paths, statePath)
		}
		return paths, nil
	})
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	_ = os.Remove(s.reviewReportDraftPath(in.RunID))
	status := "review_report_finalized"
	if accepted {
		status = "merge_ready"
	}
	return report, OperationResult{
		Hub:       tx,
		ProjectID: review.task.ProjectID,
		TaskID:    review.task.ID,
		RunID:     review.run.ID,
		Status:    status,
	}, nil
}

func validateTaskReviewSemanticInput(in TaskReviewInput) error {
	if strings.TrimSpace(in.TaskID) == "" || strings.TrimSpace(in.RunID) == "" {
		return fmt.Errorf("task_id and run_id are required")
	}
	if len(in.Findings) > model.MaxReviewFindings || len(in.ScopeCoverage) > model.MaxReviewScopeCoverage {
		return fmt.Errorf("review semantic section exceeds bounds")
	}
	return nil
}
