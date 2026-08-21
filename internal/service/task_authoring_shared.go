package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) requireLocalTaskAuthoring(projectID string) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	_, ok := s.Config.Projects[projectID]
	if !ok {
		return fmt.Errorf("project %q is not configured locally", projectID)
	}
	return nil
}

func (s *Service) sharedTaskProjectCode(ctx context.Context, projectID string) (string, error) {
	if err := s.requireLocalTaskAuthoring(projectID); err != nil {
		return "", err
	}
	if code := s.Config.Projects[projectID].ProjectCode; model.ValidateProjectCode(code) == nil {
		return code, nil
	}
	code, _, found, err := s.Durability.ReadSharedTaskSequence(ctx, projectID)
	if err != nil {
		return "", err
	}
	if found && model.ValidateProjectCode(code) == nil {
		return code, nil
	}
	return "", fmt.Errorf("project %q has no local project code", projectID)
}

func (s *Service) readSharedTask(ctx context.Context, projectID, taskID string) (model.TaskAuthoring, error) {
	shared, err := s.Durability.ReadSharedTask(ctx, taskID)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(shared.Payload, &task); err != nil {
		return model.TaskAuthoring{}, fmt.Errorf("decode shared task %s: %w", taskID, err)
	}
	if task.ProjectID != projectID || task.ID != taskID {
		return model.TaskAuthoring{}, fmt.Errorf("shared task ownership mismatch")
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return model.TaskAuthoring{}, err
	}
	return task, nil
}

func (s *Service) taskAuthoringCreateShared(ctx context.Context, operationID string, in TaskAuthoringCreateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if in.CreatedBy == "" || containsControl(in.CreatedBy) {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	if in.ADRRelation == "" {
		in.ADRRelation = model.TaskADRNoRequired
	}
	draft := trainv2.AuthoringDraft{Title: in.Title, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria, Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies, PreparationReferences: in.PreparationReferences, Metadata: in.Metadata, ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences}
	if err := trainv2.ValidateDraft(draft); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	code, err := s.sharedTaskProjectCode(ctx, in.ProjectID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	var created model.TaskAuthoring
	_, _, payload, err := s.Durability.CommitSharedTaskCreate(ctx, sqlitestore.SharedTaskCreate{
		OperationID: operationID,
		ProjectID:   in.ProjectID,
		ProjectCode: code,
		Kind:        "task-authoring-create",
		CreatedAt:   s.durableNow(),
		BuildPayload: func(taskID string) ([]byte, error) {
			var err error
			created, err = trainv2.NewTask(in.ProjectID, taskID, draft, in.CreatedBy, s.durableNow())
			if err != nil {
				return nil, err
			}
			return json.Marshal(created)
		},
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return created, OperationResult{OperationID: operationID, ProjectID: created.ProjectID, TaskID: created.ID, Status: created.Status}, nil
}

func (s *Service) taskAuthoringUpdateShared(ctx context.Context, operationID string, in TaskAuthoringUpdateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	current, err := s.readSharedTask(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := trainv2.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if admitted, err := s.taskAdmittedToNonterminalTrainShared(ctx, in.ProjectID, in.TaskID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	} else if admitted {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("ready Task %q is admitted to a nonterminal Train and cannot be edited", in.TaskID)
	}
	updated, changed, err := trainv2.UpdateTask(current, trainv2.AuthoringPatch{Title: in.Title, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria, Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies, PreparationReferences: in.PreparationReferences, Metadata: in.Metadata, ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences}, in.UpdatedBy, s.durableNow())
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if !changed {
		return current, OperationResult{OperationID: operationID, ProjectID: current.ProjectID, TaskID: current.ID, Status: current.Status}, nil
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if _, err := s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "task", EntityID: updated.ID, ExpectedRevision: int64(in.ExpectedRevision), Revision: int64(updated.Revision), Kind: "task-authoring-update", Payload: payload, CreatedAt: s.durableNow()}); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return updated, OperationResult{OperationID: operationID, ProjectID: updated.ProjectID, TaskID: updated.ID, Status: updated.Status}, nil
}

func (s *Service) taskAuthoringReadyShared(ctx context.Context, operationID string, in TaskAuthoringReadyInput) (model.TaskAuthoring, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	current, err := s.readSharedTask(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := trainv2.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if current.Status == model.TaskAuthoringReady {
		return current, OperationResult{OperationID: operationID, ProjectID: current.ProjectID, TaskID: current.ID, Status: current.Status}, nil
	}
	if err := s.validateAuthoringADRReferencesShared(ctx, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := s.validateTaskDependenciesShared(ctx, in.ProjectID, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	ready, err := trainv2.ReadyTask(current, in.ReadyBy, s.durableNow())
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
	return ready, OperationResult{OperationID: operationID, ProjectID: ready.ProjectID, TaskID: ready.ID, Status: ready.Status}, nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			return true
		}
	}
	return false
}

func (s *Service) sharedTrains(ctx context.Context, projectID string) ([]model.TrainV2, error) {
	entities, err := s.Durability.ListSharedEntities(ctx, "train", 1000)
	if err != nil {
		return nil, err
	}
	trains := make([]model.TrainV2, 0, len(entities))
	for _, entity := range entities {
		var train model.TrainV2
		if err := json.Unmarshal(entity.Payload, &train); err != nil {
			return nil, fmt.Errorf("decode shared Train %s: %w", entity.ID, err)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return nil, err
		}
		if train.ProjectID == projectID {
			trains = append(trains, train)
		}
	}
	return trains, nil
}

func (s *Service) taskAdmittedToNonterminalTrainShared(ctx context.Context, projectID, taskID string) (bool, error) {
	trains, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return false, err
	}
	return trainv2.TaskAdmittedToNonterminal(trains, taskID), nil
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

func (s *Service) validateTaskDependenciesShared(ctx context.Context, projectID string, task model.TaskAuthoring) error {
	if len(task.Dependencies) == 0 {
		return nil
	}
	trains, err := s.sharedTrains(ctx, projectID)
	if err != nil {
		return err
	}
	for _, dependencyID := range task.Dependencies {
		integrated := false
		for _, train := range trains {
			if train.Status != model.TrainV2Completed || train.FullProof == nil {
				continue
			}
			for _, item := range train.Items {
				if item.TaskID == dependencyID && item.Proof != nil && item.Proof.ImplementationSHA == train.FullProof.CandidateHead {
					integrated = true
				}
			}
		}
		if !integrated {
			return fmt.Errorf("dependency-not-integrated: Task %q depends on %q without canonical integrated implementation", task.ID, dependencyID)
		}
	}
	return nil
}
