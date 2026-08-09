package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		var currentTask model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(context.task.ProjectID, context.task.ID), &currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(currentTask); err != nil || currentTask.ID != context.task.ID || currentTask.SHA256 != context.task.SHA256 {
			return nil, fmt.Errorf("task changed before delivery review publication")
		}
		if err := model.ValidateTaskHash(currentTask); err != nil || currentTask.SHA256 != context.task.SHA256 {
			return nil, fmt.Errorf("task hash changed before delivery review publication")
		}
		var currentRun model.Run
		if err := readWorktreeJSON(worktree, s.runPath(context.task.ProjectID, context.run.ID), &currentRun); err != nil {
			return nil, err
		}
		if err := model.ValidateRun(currentRun); err != nil || currentRun.Historical || currentRun.ID != context.run.ID || currentRun.TaskID != context.task.ID || currentRun.TaskSHA256 != context.task.SHA256 || currentRun.TaskRevision != context.run.TaskRevision || currentRun.TaskRevisionSHA256 != context.run.TaskRevisionSHA256 || currentRun.TaskRunNumber != context.run.TaskRunNumber || currentRun.ProjectID != context.task.ProjectID || currentRun.Branch != context.run.Branch || currentRun.BaseRevision != context.run.BaseRevision || operationalActiveRun(currentRun) {
			return nil, fmt.Errorf("run changed or is still operational before delivery review publication")
		}
		var currentAgent model.Report
		if err := readWorktreeJSON(worktree, s.reportPath(context.task.ProjectID, context.run.ID), &currentAgent); err != nil {
			return nil, err
		}
		if err := model.ValidateReport(currentAgent, currentTask, currentRun, s.Config.MaxListItems); err != nil {
			return nil, fmt.Errorf("Agent report changed before delivery review publication: %w", err)
		}
		if currentAgent.TaskID != context.task.ID || currentAgent.RunID != context.run.ID || currentAgent.TaskRevision != context.run.TaskRevision || currentAgent.TaskRevisionSHA256 != context.run.TaskRevisionSHA256 || currentAgent.TaskRunNumber != context.run.TaskRunNumber || currentAgent.ProjectID != context.task.ProjectID || currentAgent.Repository.Head != context.head || currentAgent.Repository.Branch != context.branch || currentAgent.Repository.DiffScope != context.run.BaseRevision+".."+context.head || currentAgent.Status != currentRun.Status || !sameAgentAuthority(currentAgent, context.agent) {
			return nil, fmt.Errorf("Agent report changed before delivery review publication")
		}
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("delivery review report already finalized")
		} else if !os.IsNotExist(err) {
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
