package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) PlannerReportRead(ctx context.Context, id string) (model.PlannerReport, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.PlannerReport{}, err
	}
	for _, project := range projects {
		var report model.PlannerReport
		if readErr := s.Hub.ReadJSON(ctx, s.plannerReportPath(project.ID, id), &report); readErr == nil {
			if err := model.ValidatePlannerReport(report); err != nil {
				return model.PlannerReport{}, err
			}
			return report, nil
		} else if !IsNotFound(readErr) {
			return model.PlannerReport{}, readErr
		}
	}
	return model.PlannerReport{}, fmt.Errorf("planner report not found: %s", id)
}

func (s *Service) PlannerReportStatus(ctx context.Context, id string) (model.PlannerReportStatus, error) {
	report, err := s.PlannerReportRead(ctx, id)
	if err != nil {
		return model.PlannerReportStatus{}, err
	}
	_, state, err := s.plannerReportStateReadInProjectWithReport(ctx, report.ProjectID, report.ID)
	if err != nil {
		return model.PlannerReportStatus{}, err
	}
	return plannerReportStatusProjection(report, state), nil
}

func (s *Service) plannerReportStateReadInProject(ctx context.Context, projectID, id string) (model.PlannerReportState, error) {
	_, state, err := s.plannerReportStateReadInProjectWithReport(ctx, projectID, id)
	return state, err
}

func (s *Service) plannerReportStateReadInProjectWithReport(ctx context.Context, projectID, id string) (model.PlannerReport, model.PlannerReportState, error) {
	var report model.PlannerReport
	if err := s.Hub.ReadJSON(ctx, s.plannerReportPath(projectID, id), &report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	var state model.PlannerReportState
	if err := s.Hub.ReadJSON(ctx, s.plannerReportStatePath(projectID, id), &state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReportState(state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	digest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil || state.ReportSHA256 != digest || state.ReportID != report.ID {
		return model.PlannerReport{}, model.PlannerReportState{}, fmt.Errorf("planner report state does not match immutable report")
	}
	return report, state, nil
}

func validatePlannerReportStateInWorktree(worktree string, s *Service, projectID, id string) (model.PlannerReport, model.PlannerReportState, error) {
	var report model.PlannerReport
	if err := readWorktreeJSON(worktree, s.plannerReportPath(projectID, id), &report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	var state model.PlannerReportState
	if err := readWorktreeJSON(worktree, s.plannerReportStatePath(projectID, id), &state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	if err := model.ValidatePlannerReportState(state); err != nil {
		return model.PlannerReport{}, model.PlannerReportState{}, err
	}
	digest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil || state.ReportID != report.ID || state.ReportSHA256 != digest {
		return model.PlannerReport{}, model.PlannerReportState{}, fmt.Errorf("planner report state does not match immutable report")
	}
	return report, state, nil
}

func (s *Service) PlannerReportAcknowledge(ctx context.Context, in PlannerReportAcknowledgeInput) (model.PlannerReportState, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.AcknowledgedBy) == "" {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("acknowledged_by is required")
	}
	report, err := s.PlannerReportRead(ctx, in.ReportID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	state, err := s.plannerReportStateReadInProject(ctx, report.ProjectID, report.ID)
	if err != nil {
		return model.PlannerReportState{}, OperationResult{}, err
	}
	if state.Status != model.PlannerReportPublished {
		return model.PlannerReportState{}, OperationResult{}, fmt.Errorf("planner report cannot be acknowledged from %q", state.Status)
	}
	now := time.Now().UTC()
	next := state
	next.Status = model.PlannerReportAcknowledged
	next.AcknowledgedBy = in.AcknowledgedBy
	next.AcknowledgedAt = &now
	next.UpdatedAt = now
	path := s.plannerReportStatePath(report.ProjectID, report.ID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: acknowledge report "+report.ID, func(worktree string) ([]string, error) {
		storedReport, stored, err := validatePlannerReportStateInWorktree(worktree, s, report.ProjectID, report.ID)
		if err != nil {
			return nil, err
		}
		if storedReport.ID != report.ID {
			return nil, fmt.Errorf("planner report identity mismatch")
		}
		if stored.ReportID != state.ReportID || stored.ReportSHA256 != state.ReportSHA256 || stored.Status != model.PlannerReportPublished || !stored.UpdatedAt.Equal(state.UpdatedAt) {
			return nil, fmt.Errorf("planner report changed before acknowledgement")
		}
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		journalHandoff := model.DeliveryHandoff{ID: report.HandoffID, ProjectID: report.ProjectID, TaskID: report.TaskID, RunID: report.RunID}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(journalHandoff, report.ID, nil, "planner report acknowledged", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{path}, journalPaths...), nil
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
