package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) taskAuthoringReadyShared(ctx context.Context, operationID string, in TaskAuthoringReadyInput) (model.TaskAuthoring, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	current, err := s.readSharedTask(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := model.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if current.Status == model.TaskAuthoringReady {
		return current, OperationResult{
			OperationID: operationID,
			ProjectID:   current.ProjectID,
			TaskID:      current.ID,
			Status:      current.Status,
		}, nil
	}
	if err := s.validateAuthoringADRReferencesShared(ctx, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := s.validateTaskDependenciesShared(ctx, in.ProjectID, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	ready, err := model.ReadyTask(current, in.ReadyBy, s.durableNow())
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	payload, err := json.Marshal(ready)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if _, err := s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "task", EntityID: ready.ID, ExpectedRevision: int64(in.ExpectedRevision), Revision: int64(ready.Revision), Kind: "task-authoring-ready", Payload: payload, CreatedAt: s.durableNow(), AllowSameRevision: true}); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return ready, OperationResult{
		OperationID: operationID,
		ProjectID:   ready.ProjectID,
		TaskID:      ready.ID,
		Status:      ready.Status,
	}, nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			return true
		}
	}
	return false
}

// taskHasNonterminalExecutionShared reports whether the Task owns a live
// canonical execution — the Task-execution state is the only current-task
// admission authority. Without Shared durability no canonical execution can
// exist, so the answer is always false.
func (s *Service) taskHasNonterminalExecutionShared(ctx context.Context, projectID, taskID string) (bool, error) {
	if s.Durability == nil {
		return false, nil
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, taskID)
	if err != nil {
		return false, err
	}
	return found && model.IsTaskExecutionNonTerminal(state.Status), nil
}

func (s *Service) validateAuthoringADRReferencesShared(ctx context.Context, task model.TaskAuthoring) error {
	if task.ADRRelation == model.TaskADRNoRequired {
		return nil
	}
	for _, id := range task.ADRReferences {
		entity, err := s.Durability.ReadSharedEntity(ctx, "adr", id)
		if err != nil {
			return fmt.Errorf("ADR %q is unavailable in Shared: %w", id, err)
		}
		var adr model.ADR
		if err := json.Unmarshal(entity.Payload, &adr); err != nil {
			return fmt.Errorf("decode shared ADR %q: %w", id, err)
		}
		if adr.ProjectID != task.ProjectID || adr.Status != "accepted" {
			return fmt.Errorf("ADR %q is not an accepted ADR for project %q", id, task.ProjectID)
		}
		if err := model.ValidateADR(adr); err != nil {
			return fmt.Errorf("ADR %q is invalid: %w", id, err)
		}
	}
	return nil
}

// validateTaskDependenciesShared requires every declared dependency to carry
// canonical integrated evidence: a Task-execution state that reached
// integrated/done or the task completion lifecycle event.
func (s *Service) validateTaskDependenciesShared(ctx context.Context, projectID string, task model.TaskAuthoring) error {
	for _, dependencyID := range task.Dependencies {
		integrated := false
		if state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, dependencyID); err != nil {
			return err
		} else if found && (state.Status == model.TaskExecutionIntegrated || state.Status == model.TaskExecutionDone) {
			integrated = true
		}
		if !integrated {
			if _, found, err := s.Durability.ReadTaskCompletionEvent(ctx, projectID, dependencyID); err != nil {
				return err
			} else if found {
				integrated = true
			}
		}
		if !integrated {
			return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
		}
	}
	return nil
}
