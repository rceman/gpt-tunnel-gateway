package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) PlannerReportNext(ctx context.Context, in PlannerReportNextInput) (model.PlannerReportState, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.ResolvedBy) == "" {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("resolved_by is required")
	}
	report, err := s.PlannerReportRead(ctx, in.ReportID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	state, err := s.plannerReportStateReadInProject(ctx, report.ProjectID, report.ID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if state.Status != model.PlannerReportAcknowledged {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("planner report cannot be resolved from %q", state.Status)
	}
	handoff, err := s.deliveryHandoffReadInProject(ctx, report.ProjectID, report.HandoffID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if handoff.CurrentReportID != report.ID {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("planner report is not the current handoff report")
	}
	resumeHandoff := false
	switch report.ReportType {
	case model.PlannerReportBlocked:
		if handoff.Status != model.DeliveryHandoffBlocked {
			return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("blocked planner report requires a blocked handoff")
		}
		resumeHandoff = true
	case model.PlannerReportDecisionRequired:
		if handoff.Status != model.DeliveryHandoffAwaitingDecision {
			return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("decision planner report requires an awaiting-decision handoff")
		}
		resumeHandoff = true
	case model.PlannerReportCompleted:
		if handoff.Status != model.DeliveryHandoffCompleted {
			return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("completed planner report requires a completed handoff")
		}
	}
	now := time.Now().UTC()
	next := state
	next.Status = model.PlannerReportResolved
	next.ResolvedBy = in.ResolvedBy
	next.ResolvedAt = &now
	next.UpdatedAt = now
	path := s.plannerReportStatePath(report.ProjectID, report.ID)
	nextHandoff := handoff
	if resumeHandoff {
		nextHandoff.Status = model.DeliveryHandoffInProgress
		nextHandoff.CurrentReportID = ""
		nextHandoff.UpdatedAt = now
		if err := model.ValidateDeliveryHandoff(nextHandoff); err != nil {
			return model.PlannerReportState{}, OperationResult{}, err
		}
	}
	handoffPath := s.deliveryHandoffPath(handoff.ProjectID, handoff.ID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: resolve report "+report.ID, func(worktree string) ([]string, error) {
		storedHandoff, err := s.deliveryHandoffReadInWorktree(worktree, handoff.ProjectID, handoff.ID)
		if err != nil {
			return nil, err
		}
		if storedHandoff.ID != handoff.ID || storedHandoff.CurrentReportID != handoff.CurrentReportID || storedHandoff.Status != handoff.Status || !storedHandoff.UpdatedAt.Equal(handoff.UpdatedAt) {
			return nil, fmt.Errorf("handoff changed before report resolution")
		}
		storedReport, stored, err := validatePlannerReportStateInWorktree(worktree, s, report.ProjectID, report.ID)
		if err != nil {
			return nil, err
		}
		if storedReport.ID != report.ID {
			return nil, fmt.Errorf("planner report identity mismatch")
		}
		if stored.ReportID != state.ReportID || stored.ReportSHA256 != state.ReportSHA256 || stored.Status != model.PlannerReportAcknowledged || !stored.UpdatedAt.Equal(state.UpdatedAt) {
			return nil, fmt.Errorf("planner report changed before resolution")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		changed := []string{path}
		journalHandoff := storedHandoff
		summary := "planner report resolved"
		if resumeHandoff {
			if err := hub.WriteJSON(worktree, handoffPath, nextHandoff); err != nil {
				return nil, err
			}
			changed = append(changed, handoffPath)
			journalHandoff = nextHandoff
			summary = "planner report resolved and handoff resumed"
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(journalHandoff, report.ID, nil, summary, "planner"))
		if err != nil {
			return nil, err
		}
		return append(changed, journalPaths...), nil
	})
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	return next, OperationResult{
		Hub:       tx,
		ProjectID: report.ProjectID,
		Status:    next.Status,
	}, nil
}

func plannerReportStatusProjection(item model.PlannerReport, state model.PlannerReportState) model.PlannerReportStatus {
	return model.PlannerReportStatus{SchemaVersion: item.SchemaVersion, ID: item.ID, ProjectID: item.ProjectID, HandoffID: item.HandoffID, TaskID: item.TaskID, RunID: item.RunID, ReportType: item.ReportType, OwnerSummary: item.OwnerSummary, SupersedesReportID: item.SupersedesReportID, PublishedBy: item.PublishedBy, PublishedAt: item.PublishedAt, Status: state.Status}
}

func (s *Service) PlannerReportList(ctx context.Context, in PlannerReportListInput) ([]model.PlannerReportStatus, error) {
	limit, err := boundedDurableListLimit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return nil, err
	}
	paths, err := s.Hub.List(ctx, s.plannerReportPrefix(in.ProjectID), ".json")
	if err != nil {
		return nil, err
	}
	items := make([]model.PlannerReportStatus, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") {
			continue
		}
		var report model.PlannerReport
		if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
			return nil, err
		}
		if err := model.ValidatePlannerReport(report); err != nil {
			return nil, err
		}
		state, err := s.plannerReportStateReadInProject(ctx, in.ProjectID, report.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, plannerReportStatusProjection(report, state))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
