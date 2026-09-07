package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) taskAuthoringReadyShared(ctx context.Context, operationID string, in TaskAuthoringReadyInput) (model.TaskAuthoring, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
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

func (s *Service) sharedTrains(ctx context.Context, projectID string) ([]model.TrainV2, error) {
	entities, err := s.sharedProjectEntities(ctx, "train", projectID)
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
				if item.TaskID != dependencyID || item.Proof == nil || item.Proof.ImplementationSHA != train.FullProof.CandidateHead {
					continue
				}
				receipt, err := s.Durability.ReadSharedEntity(ctx, "integration_receipt", sqlitestore.SharedIntegrationReceiptID(projectID, train.ID))
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						continue
					}
					return fmt.Errorf("read local integration receipt for Train %q: %w", train.ID, err)
				}
				var integration trainv2.IntegrationReceipt
				if err := json.Unmarshal(receipt.Payload, &integration); err != nil {
					return fmt.Errorf("decode local integration receipt for Train %q: %w", train.ID, err)
				}
				if err := trainv2.ValidateIntegrationReceipt(integration); err != nil {
					return fmt.Errorf("invalid local integration receipt for Train %q: %w", train.ID, err)
				}
				if integration.ProjectID == projectID && integration.TrainID == train.ID && integration.Status == "completed" && integration.IntegrationHead == train.FullProof.CandidateHead {
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
