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

func (s *Service) TaskReviewReportFinalize(ctx context.Context, in TaskReviewReportFinalizeInput) (model.RunReviewReport, OperationResult, error) {
	lock, err := s.reviewReportLock(in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, in.TaskID, in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	draft, err := s.readReviewDraft(in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	if in.ExpectedDraftRevision != draft.DraftRevision {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("DRAFT_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedDraftRevision, draft.DraftRevision)
	}
	s.reviewMachineDraft(context, &draft)
	if missing := reviewDraftMissingSections(draft); len(missing) > 0 {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("review draft incomplete: %s", strings.Join(missing, ", "))
	}
	report := model.RunReviewReport{
		SchemaVersion:           model.RunReviewReportSchemaVersion,
		ID:                      model.NewRunReviewReportID(context.run.ID),
		TaskID:                  context.task.ID,
		RunID:                   context.run.ID,
		ProjectID:               context.task.ProjectID,
		TaskSHA256:              context.task.SHA256,
		TaskRevision:            context.run.TaskRevision,
		TaskRevisionSHA256:      context.run.TaskRevisionSHA256,
		TaskRunNumber:           context.run.TaskRunNumber,
		Branch:                  context.branch,
		BaseRevision:            context.run.BaseRevision,
		ReviewedHead:            context.head,
		Outcome:                 draft.Outcome,
		RepositoryState:         context.repository,
		Gates:                   append([]model.CompletionGateResult{}, context.gates...),
		ServerGateResults:       append([]model.CompletionGateResult{}, context.serverGates...),
		Findings:                append([]model.ReviewFinding{}, draft.Findings...),
		ScopeCoverage:           append([]model.ReviewScopeCoverage{}, draft.ScopeCoverage...),
		ChangedFiles:            append([]string{}, context.changed...),
		UnexpectedSurfaces:      append([]string{}, draft.UnexpectedSurfaces...),
		HistoricalCompatibility: append([]string{}, draft.HistoricalCompatibility...),
		ProhibitedActions:       append([]string{}, draft.ProhibitedActions...),
		NextAction:              draft.NextAction,
		FinishedAt:              time.Now().UTC(),
	}
	if err := model.ValidateRunReviewReport(report); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.RunReviewReport{}, OperationResult{}, err
		}
	}
	path := s.reviewReportPath(context.task.ProjectID, context.run.ID)
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize delivery review "+report.ID, func(worktree string) ([]string, error) {
		if _, err := s.validateReviewPublicationInWorktree(worktree, context); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, report); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	_ = os.Remove(s.reviewReportDraftPath(in.RunID))
	return report, OperationResult{
		Hub:       tx,
		ProjectID: context.task.ProjectID,
		TaskID:    context.task.ID,
		RunID:     context.run.ID,
		Status:    "review_report_finalized",
	}, nil
}
