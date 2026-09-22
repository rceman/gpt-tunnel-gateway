package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type MilestoneMembershipInput struct {
	ProjectID string
	Key       string
	Tasks     []string
	Actor     string
	Reason    string
}

func (s *Service) MilestoneLifecycleAppendTasks(ctx context.Context, in MilestoneMembershipInput) (model.Milestone, OperationResult, error) {
	return s.mutateMilestoneMembership(ctx, in, true)
}

func (s *Service) MilestoneLifecycleRemoveTasks(ctx context.Context, in MilestoneMembershipInput) (model.Milestone, OperationResult, error) {
	return s.mutateMilestoneMembership(ctx, in, false)
}

func (s *Service) mutateMilestoneMembership(ctx context.Context, in MilestoneMembershipInput, appendTasks bool) (model.Milestone, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if in.Actor == "" || in.Reason == "" || len(in.Tasks) == 0 {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone membership mutation requires tasks, actor, and reason")
	}
	current, err := s.MilestoneLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if current.Status == model.MilestoneCompleted || current.Status == model.MilestoneArchived {
		return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone %q cannot change membership from status %q", in.Key, current.Status)
	}
	code, err := s.sharedTaskProjectCode(ctx, in.ProjectID)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if err := s.validateMilestoneTasks(ctx, in.ProjectID, code, in.Tasks); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	existing := make(map[string]struct{}, len(current.Tasks))
	for _, taskID := range current.Tasks {
		existing[taskID] = struct{}{}
	}
	changed := false
	if appendTasks {
		for _, taskID := range in.Tasks {
			if _, found := existing[taskID]; found {
				continue
			}
			existing[taskID] = struct{}{}
			changed = true
		}
	} else {
		for _, taskID := range in.Tasks {
			if _, found := existing[taskID]; !found {
				continue
			}
			if track, found, trackErr := s.nonterminalTrackForTask(ctx, in.ProjectID, taskID); trackErr != nil {
				return model.Milestone{}, OperationResult{}, trackErr
			} else if found {
				return model.Milestone{}, OperationResult{}, fmt.Errorf("milestone task %q belongs to nonterminal Track %q", taskID, track.ID)
			}
			delete(existing, taskID)
			changed = true
		}
	}
	if !changed {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			EntityKey: current.ID,
			Revision:  current.Revision,
			Status:    current.Status,
		}, nil
	}
	membership := make([]string, 0, len(existing))
	for taskID := range existing {
		membership = append(membership, taskID)
	}
	updated := current
	updated.Tasks = model.CanonicalMilestoneTasks(membership)
	updated.Revision++
	updated.UpdatedBy = in.Actor
	updated.UpdatedAt = s.durableNow().UTC()
	if !updated.UpdatedAt.After(current.UpdatedAt) {
		updated.UpdatedAt = current.UpdatedAt.Add(time.Nanosecond)
	}
	if err := model.ValidateMilestone(updated); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	kind := "milestone-append-task"
	if !appendTasks {
		kind = "milestone-remove-task"
	}
	operationID := kind + "-" + in.Key + "-r" + strconv.Itoa(updated.Revision)
	entity, err := s.Durability.ReadSharedEntity(ctx, "milestone", in.Key)
	if err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	if _, err := s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: operationID, EntityType: "milestone", ProjectID: in.ProjectID, EntityID: in.Key, ExpectedRevision: int64(current.Revision), ExpectedStoreRevision: entity.Revision, Revision: int64(updated.Revision), Kind: kind, HistoryMutationKind: "update", Payload: payload, Actor: in.Actor, Reason: in.Reason, ChangedFields: []string{"tasks"}, CreatedAt: updated.UpdatedAt}); err != nil {
		return model.Milestone{}, OperationResult{}, err
	}
	return updated, OperationResult{
		OperationID: operationID,
		ProjectID:   updated.ProjectID,
		EntityKey:   updated.ID,
		Revision:    updated.Revision,
		Status:      updated.Status,
	}, nil
}
