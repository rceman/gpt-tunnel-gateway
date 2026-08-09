package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) PlannerReportPublish(ctx context.Context, in PlannerReportPublishInput) (model.PlannerReport, OperationResult, error) {
	if err := authority.RequireDelivery(ctx); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	handoff, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, handoff.ProjectID, handoff.TaskID, handoff.TaskSHA256, handoff.TaskRefs); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if handoff.Status != model.DeliveryHandoffInProgress && handoff.Status != model.DeliveryHandoffBlocked && handoff.Status != model.DeliveryHandoffAwaitingDecision {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("handoff cannot receive a report from %q", handoff.Status)
	}
	report := in.Report
	if report.ID == "" {
		report.ID, err = newDurableRecordID()
		if err != nil {
			return model.PlannerReport{}, OperationResult{}, err
		}
	}
	report.SchemaVersion = model.DurableHandoffSchemaVersion
	report.ProjectID = handoff.ProjectID
	report.HandoffID = handoff.ID
	report.TaskID = handoff.TaskID
	report.RunID = handoff.RunID
	report.TaskSHA256 = handoff.TaskSHA256
	if strings.TrimSpace(report.PublishedBy) == "" {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("published_by is required")
	}
	if report.PublishedAt.IsZero() {
		report.PublishedAt = time.Now().UTC()
	}
	if err := model.ValidatePlannerReport(report); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	if err := model.PlannerReportRequiresTerminalEvidence(report); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	var expectedTask model.Task
	var expectedRun model.Run
	var expectedAgent model.Report
	var expectedDelivery model.RunReviewReport
	if report.ReportType == model.PlannerReportCompleted {
		expectedTask, expectedRun, expectedAgent, expectedDelivery, err = s.validateCompletedDeliveryProof(ctx, handoff, report.TechnicalEvidence)
		if err != nil {
			return model.PlannerReport{}, OperationResult{}, err
		}
	}
	if handoff.CurrentReportID == "" && report.SupersedesReportID != "" {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("report supersedes no current report")
	}
	if handoff.CurrentReportID != "" && report.SupersedesReportID != handoff.CurrentReportID {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("report supersession does not match current report")
	}
	if report.ReportType == model.PlannerReportCompleted && handoff.Status != model.DeliveryHandoffInProgress {
		return model.PlannerReport{}, OperationResult{}, fmt.Errorf("completed report requires an in-progress handoff")
	}
	reportDigest, err := model.CanonicalPlannerReportDigest(report)
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	reportState := model.PlannerReportState{SchemaVersion: model.DurableHandoffSchemaVersion, ReportID: report.ID, ReportSHA256: reportDigest, Status: model.PlannerReportPublished, UpdatedAt: report.PublishedAt}
	reportPath := s.plannerReportPath(handoff.ProjectID, report.ID)
	reportStatePath := s.plannerReportStatePath(handoff.ProjectID, report.ID)
	handoffPath := s.deliveryHandoffPath(handoff.ProjectID, handoff.ID)
	nextHandoff := handoff
	nextHandoff.CurrentReportID = report.ID
	nextHandoff.UpdatedAt = report.PublishedAt
	switch report.ReportType {
	case model.PlannerReportCompleted:
		nextHandoff.Status = model.DeliveryHandoffCompleted
	case model.PlannerReportBlocked:
		nextHandoff.Status = model.DeliveryHandoffBlocked
	case model.PlannerReportDecisionRequired:
		nextHandoff.Status = model.DeliveryHandoffAwaitingDecision
	}
	if err := model.ValidateDeliveryHandoff(nextHandoff); err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "delivery: publish planner report "+report.ID, func(worktree string) ([]string, error) {
		changed := make([]string, 0, 4)
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, handoffPath, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.ID != handoff.ID || stored.Status != handoff.Status || stored.CurrentReportID != handoff.CurrentReportID || stored.UpdatedAt != handoff.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before report publication")
		}
		if report.ReportType == model.PlannerReportCompleted {
			if err := validateCompletedDeliveryProofInWorktree(worktree, s, stored, expectedTask, expectedRun, expectedAgent, expectedDelivery); err != nil {
				return nil, err
			}
		}
		if stored.CurrentReportID != "" {
			oldReport, oldState, err := validatePlannerReportStateInWorktree(worktree, s, stored.ProjectID, stored.CurrentReportID)
			if err != nil {
				return nil, err
			}
			oldStatePath := s.plannerReportStatePath(stored.ProjectID, stored.CurrentReportID)
			if oldReport.ID != stored.CurrentReportID {
				return nil, fmt.Errorf("current planner report identity mismatch")
			}
			if oldState.Status == model.PlannerReportResolved || oldState.Status == model.PlannerReportSuperseded {
				return nil, fmt.Errorf("planner report cannot supersede state %q", oldState.Status)
			}
			oldState.Status = model.PlannerReportSuperseded
			oldState.UpdatedAt = report.PublishedAt
			if err := hub.WriteJSON(worktree, oldStatePath, oldState); err != nil {
				return nil, err
			}
			changed = append(changed, oldStatePath)
		}
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(reportPath))); statErr == nil {
			return nil, fmt.Errorf("planner report already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, reportPath, report); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, reportStatePath, reportState); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, handoffPath, nextHandoff); err != nil {
			return nil, err
		}
		extraReportIDs := []string{}
		if handoff.CurrentReportID != "" {
			extraReportIDs = append(extraReportIDs, handoff.CurrentReportID)
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(nextHandoff, report.ID, extraReportIDs, "planner report published and handoff advanced", "delivery"))
		if err != nil {
			return nil, err
		}
		changed = append(changed, reportPath, reportStatePath, handoffPath)
		changed = append(changed, journalPaths...)
		return changed, nil
	})
	if err != nil {
		return model.PlannerReport{}, OperationResult{}, err
	}
	return report, OperationResult{
		Hub:       tx,
		ProjectID: report.ProjectID,
		Status:    nextHandoff.Status,
	}, nil
}
