package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func trainV2ReviewNextAction(outcome string) string {
	switch outcome {
	case model.ReviewOutcomeAccepted:
		return "train_ready_for_integration"
	case model.ReviewOutcomeRejected:
		return "create_bounded_correction_train_item"
	case model.ReviewOutcomeBlocked:
		return "planner_decision_required_for_train"
	default:
		return "continue_train_delivery_review"
	}
}

func (s *Service) trainV2TaskReview(ctx context.Context, in TaskReviewInput, run model.Run) (model.RunReviewReport, OperationResult, error) {
	authority, err := s.loadTrainV2HistoricalAuthority(ctx, run)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	if authority.item.Status != model.TrainV2ItemFinalized || authority.item.Review != nil {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("Train v2 item is not awaiting review")
	}
	if run.Status != "succeeded" && in.Outcome == model.ReviewOutcomeAccepted {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("accepted Train v2 review requires a succeeded Run")
	}
	if exists, err := s.reviewReportExists(ctx, run.ProjectID, run.ID); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	} else if exists {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("Train v2 review report already finalized")
	}
	agent, err := s.RunReport(ctx, run.ID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	report := model.RunReviewReport{
		SchemaVersion: model.RunReviewReportSchemaVersion,
		ID:            model.NewRunReviewReportID(run.ID), TaskID: run.TaskID, RunID: run.ID, ProjectID: run.ProjectID,
		TaskSHA256: run.TaskSHA256, TaskRevision: run.TaskRevision, TaskRevisionSHA256: run.TaskRevisionSHA256,
		TaskRunNumber: run.TaskRunNumber, Branch: run.Branch, BaseRevision: run.BaseRevision,
		ReviewedHead: agent.Repository.Head, Outcome: in.Outcome, RepositoryState: model.ReviewRepositoryState{
			Branch: run.Branch, BaseRevision: run.BaseRevision, ReviewedHead: agent.Repository.Head,
			WorktreeClean: agent.Repository.WorktreeClean, BaseAncestor: agent.Repository.BaseAncestor,
		}, Gates: append([]model.CompletionGateResult{}, agent.GateResults...),
		ServerGateResults: append([]model.CompletionGateResult{}, agent.ServerGateResults...),
		Findings:          append([]model.ReviewFinding{}, in.Findings...), ScopeCoverage: append([]model.ReviewScopeCoverage{}, in.ScopeCoverage...),
		ChangedFiles: append([]string{}, agent.Repository.ChangedFiles...), UnexpectedSurfaces: append([]string{}, in.UnexpectedSurfaces...),
		HistoricalCompatibility: append([]string{}, in.HistoricalCompatibility...), ProhibitedActions: append([]string{}, in.ProhibitedActions...),
		NextAction: trainV2ReviewNextAction(in.Outcome), FinishedAt: time.Now().UTC(),
	}
	if err := model.ValidateRunReviewReport(report); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	var updated model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: review Train v2 item "+run.TaskID, func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(run.ProjectID, run.TrainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != authority.train.Revision {
			return nil, fmt.Errorf("Train v2 changed before review")
		}
		updated, err = trainv2.RecordReview(current, run.TaskID, in.Outcome, report.ID, report.FinishedAt)
		if err != nil {
			return nil, err
		}
		path := s.trainV2Path(run.ProjectID, run.TrainID)
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		reviewPath := s.reviewReportPath(run.ProjectID, run.ID)
		if err := hub.WriteJSON(worktree, reviewPath, report); err != nil {
			return nil, err
		}
		return []string{path, reviewPath}, nil
	})
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	status := "train_item_reviewed"
	if in.Outcome != model.ReviewOutcomeAccepted {
		status = "train_item_blocked"
	}
	return report, OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: status}, nil
}
