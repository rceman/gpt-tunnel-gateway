package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) DeliveryHandoffSupersede(ctx context.Context, in DeliveryHandoffSupersedeInput) (model.DeliveryHandoff, OperationResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := validateHandoffSummaryAndEvidence(in.OwnerSummary, in.TechnicalEvidence); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	old, err := s.findDeliveryHandoff(ctx, in.HandoffID)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateCandidatePlanAuthority(ctx, old.ProjectID, in.PlanRevision, in.HubRevision, in.ExpectedHubRevision); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateTaskRefsAgainstDurable(ctx, old.ProjectID, old.TaskID, old.TaskSHA256, in.TaskRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := s.validateHandoffPlanAndJournalRefs(ctx, old.ProjectID, in.PlanSectionRefs, in.OperatorEventRefs); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if old.Status == model.DeliveryHandoffCompleted || old.Status == model.DeliveryHandoffCancelled || old.Status == model.DeliveryHandoffSuperseded {
		return model.DeliveryHandoff{}, OperationResult{}, fmt.Errorf("handoff cannot be superseded from %q", old.Status)
	}
	id, err := newDurableRecordID()
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	now := monotonicTimestamp(s.durableNow(), old.CreatedAt, old.UpdatedAt)
	next := model.DeliveryHandoff{SchemaVersion: model.DurableHandoffSchemaVersion, ID: id, ProjectID: old.ProjectID, TaskID: old.TaskID, RunID: old.RunID, TaskSHA256: old.TaskSHA256, Status: model.DeliveryHandoffPending, OwnerSummary: in.OwnerSummary, TechnicalEvidence: append(json.RawMessage(nil), in.TechnicalEvidence...), SupersedesHandoffID: old.ID, PlanRevision: in.PlanRevision, HubRevision: in.HubRevision, TaskRefs: append([]model.TaskRef(nil), in.TaskRefs...), TrainRefs: append([]string(nil), in.TrainRefs...), PlanSectionRefs: append([]string(nil), in.PlanSectionRefs...), OperatorEventRefs: append([]string(nil), in.OperatorEventRefs...), ExpectedRepoBase: in.ExpectedRepoBase, ExpectedRepoHead: in.ExpectedRepoHead, FirstAction: in.FirstAction, StopBoundary: in.StopBoundary, ProhibitedOperations: append([]string(nil), in.ProhibitedOperations...), InstructionBody: in.InstructionBody, RoleRefs: append([]string(nil), in.RoleRefs...), DelegationRefs: append([]string(nil), in.DelegationRefs...), AuthorRole: "planner", ConsumerRole: "delivery", CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now}
	next.CanonicalDigest, err = model.CanonicalDeliveryHandoffDigest(next)
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	if err := model.ValidateDeliveryHandoff(next); err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	oldPath, nextPath := s.deliveryHandoffPath(old.ProjectID, old.ID), s.deliveryHandoffPath(old.ProjectID, id)
	oldNext := old
	oldNext.Status = model.DeliveryHandoffSuperseded
	oldNext.SupersededByHandoffID = id
	oldNext.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "planner: supersede delivery handoff "+old.ID, func(worktree string) ([]string, error) {
		var stored model.DeliveryHandoff
		if err := readWorktreeJSON(worktree, oldPath, &stored); err != nil {
			return nil, err
		}
		if err := model.ValidateDeliveryHandoff(stored); err != nil {
			return nil, err
		}
		if stored.Status != old.Status || stored.UpdatedAt != old.UpdatedAt {
			return nil, fmt.Errorf("handoff changed before supersession")
		}
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(nextPath))); statErr == nil {
			return nil, fmt.Errorf("replacement handoff already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, oldPath, oldNext); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, nextPath, next); err != nil {
			return nil, err
		}
		_, journalPaths, err := s.appendOperatorEventInWorktree(worktree, handoffJournalInput(next, "", []string{old.ID}, "delivery handoff superseded", "planner"))
		if err != nil {
			return nil, err
		}
		return append([]string{oldPath, nextPath}, journalPaths...), nil
	})
	if err != nil {
		return model.DeliveryHandoff{}, OperationResult{}, err
	}
	return next, OperationResult{
		Hub:       tx,
		ProjectID: next.ProjectID,
		Status:    next.Status,
	}, nil
}

func evidenceString(evidence map[string]json.RawMessage, key string) (string, error) {
	var value string
	if err := json.Unmarshal(evidence[key], &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("technical_evidence.%s is required", key)
	}
	return value, nil
}
