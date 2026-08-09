package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) DeliveryHandoffCreate(ctx context.Context, in DeliveryHandoffCreateInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.SupersedesID) != "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("delivery handoff create does not accept supersedes_handoff_id; use delivery handoff supersede")
	}
	if err := validateHandoffSummaryAndEvidence(in.OwnerSummary, in.TechnicalEvidence); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	task, run, err := s.validateHandoffReferences(ctx, in.ProjectID, in.TaskID, in.RunID, in.TaskSHA256)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateCandidatePlanAuthority(ctx, in.ProjectID, in.PlanRevision, in.HubRevision, in.ExpectedHubRevision); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, in.ProjectID, task.ID, task.SHA256, in.TaskRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateHandoffPlanAndJournalRefs(ctx, in.ProjectID, in.PlanSectionRefs, in.OperatorEventRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	id, err := newDurableRecordID()
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	handoff := model.DeliveryHandoff{SchemaVersion: model.DurableHandoffSchemaVersion, ID: id, ProjectID: in.ProjectID, TaskID: task.ID, RunID: run.ID, TaskSHA256: task.SHA256, Status: model.DeliveryHandoffPending, OwnerSummary: in.OwnerSummary, TechnicalEvidence: append(json.RawMessage(nil), in.TechnicalEvidence...), PlanRevision: in.PlanRevision, HubRevision: in.HubRevision, TaskRefs: append([]model.TaskRef(nil), in.TaskRefs...), TrainRefs: append([]string(nil), in.TrainRefs...), PlanSectionRefs: append([]string(nil), in.PlanSectionRefs...), OperatorEventRefs: append([]string(nil), in.OperatorEventRefs...), ExpectedRepoBase: in.ExpectedRepoBase, ExpectedRepoHead: in.ExpectedRepoHead, FirstAction: in.FirstAction, StopBoundary: in.StopBoundary, ProhibitedOperations: append([]string(nil), in.ProhibitedOperations...), InstructionBody: in.InstructionBody, RoleRefs: append([]string(nil), in.RoleRefs...), DelegationRefs: append([]string(nil), in.DelegationRefs...), AuthorRole: "planner", ConsumerRole: "delivery", CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now}
	handoff.CanonicalDigest, err = model.CanonicalDeliveryHandoffDigest(handoff)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := model.ValidateDeliveryHandoff(handoff); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	path := s.deliveryHandoffPath(in.ProjectID, id)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: create delivery handoff "+id, func(worktree string) ([]string, error) {
		changed := make([]string, 0, 3)
		if err := s.rejectDuplicateActiveHandoffInWorktree(worktree, handoff.ProjectID, handoff.TaskID, handoff.RunID); err != nil {
			return nil, err
		}
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); statErr == nil {
			return nil, fmt.Errorf("delivery handoff already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, path, handoff); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(handoff, "", nil, "delivery handoff created", "planner"))
		if err != nil {
			return nil, err
		}
		changed = append(changed, path)
		changed = append(changed, journalPaths...)
		return changed, nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return handoff, OperationResult{
		Hub:       tx,
		ProjectID: handoff.ProjectID,
		Status:    handoff.Status,
	}, nil
}

func (s *Service) deliveryHandoffReadInProject(ctx context.Context, projectID, id string) (model.DeliveryHandoff, error) {
	var handoff model.DeliveryHandoff
	if err := s.Hub.ReadJSON(ctx, s.deliveryHandoffPath(projectID, id), &handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	if err := model.ValidateDeliveryHandoff(handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	return handoff, nil
}

func (s *Service) deliveryHandoffReadInWorktree(worktree, projectID, id string) (model.DeliveryHandoff, error) {
	var handoff model.DeliveryHandoff
	if err := readWorktreeJSON(worktree, s.deliveryHandoffPath(projectID, id), &handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	if err := model.ValidateDeliveryHandoff(handoff); err != nil {
		return model.DeliveryHandoff{}, err
	}
	return handoff, nil
}

func (s *Service) findDeliveryHandoff(ctx context.Context, id string) (model.DeliveryHandoff, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.DeliveryHandoff{}, err
	}
	for _, project := range projects {
		handoff, readErr := s.deliveryHandoffReadInProject(ctx, project.ID, id)
		if readErr == nil {
			return handoff, nil
		}
		if !IsNotFound(readErr) {
			return model.DeliveryHandoff{}, readErr
		}
	}
	return model.DeliveryHandoff{}, fmt.Errorf("delivery handoff not found: %s", id)
}

func (s *Service) DeliveryHandoffRead(ctx context.Context, id string) (model.DeliveryHandoff, error) {
	return s.findDeliveryHandoff(ctx, id)
}

func (s *Service) deliveryHandoffStatusSummary(ctx context.Context, item model.DeliveryHandoff) (model.OwnerSummary, error) {
	if item.CurrentReportID == "" {
		return item.OwnerSummary, nil
	}
	report, state, err := s.plannerReportStateReadInProjectWithReport(ctx, item.ProjectID, item.CurrentReportID)
	if err != nil {
		return model.OwnerSummary{}, err
	}
	if item.Status == model.DeliveryHandoffBlocked || item.Status == model.DeliveryHandoffAwaitingDecision {
		if state.Status == model.PlannerReportResolved || state.Status == model.PlannerReportSuperseded {
			return item.OwnerSummary, nil
		}
		return report.OwnerSummary, nil
	}
	if item.Status == model.DeliveryHandoffCompleted && state.Status != model.PlannerReportSuperseded {
		return report.OwnerSummary, nil
	}
	return item.OwnerSummary, nil
}
