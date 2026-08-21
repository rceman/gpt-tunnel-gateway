package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) sharedTaskProjectCode(ctx context.Context, projectID string) (string, error) {
	if project, ok := s.Config.Projects[projectID]; ok && project.ProjectCode != "" {
		return project.ProjectCode, nil
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("read project identifiers: %w", err)
	}
	return identifiers.ProjectCode, nil
}

func (s *Service) sharedTaskSequenceStart(ctx context.Context, projectID, projectCode string) (int64, error) {
	_, next, found, err := s.Durability.ReadSharedTaskSequence(ctx, projectID)
	if err != nil {
		return 0, err
	}
	if found {
		return next, nil
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("read project identifiers for shared allocation: %w", err)
	}
	if identifiers.ProjectCode != projectCode {
		return 0, fmt.Errorf("project identifier code does not match local project code")
	}
	return int64(identifiers.NextTaskNumber), nil
}

func (s *Service) readOrSeedSharedTask(ctx context.Context, projectID, taskID string) (model.TaskAuthoring, error) {
	shared, err := s.Durability.ReadSharedTask(ctx, taskID)
	if err == nil {
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
	if !errors.Is(err, os.ErrNotExist) {
		return model.TaskAuthoring{}, err
	}
	// Compatibility bootstrap is worker-only. The public receipt has already
	// been persisted; a pre-cutover Hub record is imported into Shared before
	// the local CAS mutation and is never written back synchronously.
	task, err := s.TaskAuthoringRead(ctx, projectID, taskID)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := s.Durability.SeedSharedTask(ctx, sqlitestore.SharedTask{ID: task.ID, Revision: int64(task.Revision), Payload: payload, UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		return model.TaskAuthoring{}, err
	}
	return task, nil
}

func (s *Service) taskAuthoringCreateShared(ctx context.Context, operationID string, in TaskAuthoringCreateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if in.CreatedBy == "" {
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
	initialNumber, err := s.sharedTaskSequenceStart(ctx, in.ProjectID, code)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	var created model.TaskAuthoring
	_, _, payload, err := s.Durability.CommitSharedTaskCreate(ctx, sqlitestore.SharedTaskCreate{
		OperationID:           operationID,
		ProjectID:             in.ProjectID,
		ProjectCode:           code,
		InitialNextTaskNumber: initialNumber,
		Kind:                  "task-authoring-create",
		CreatedAt:             s.durableNow(),
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
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	current, err := s.readOrSeedSharedTask(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := trainv2.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if admitted, err := s.taskAdmittedToNonterminalTrain(ctx, in.ProjectID, in.TaskID); err != nil {
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
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	current, err := s.readOrSeedSharedTask(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := trainv2.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if current.Status == model.TaskAuthoringReady {
		return current, OperationResult{OperationID: operationID, ProjectID: current.ProjectID, TaskID: current.ID, Status: current.Status}, nil
	}
	if err := s.validateAuthoringADRReferences(ctx, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := s.validateTaskDependencies(ctx, in.ProjectID, current); err != nil {
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
