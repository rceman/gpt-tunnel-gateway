package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func requireTrainV2Authoring(ctx context.Context, s *Service, projectID string) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	enabled, err := s.trainV2Enabled(ctx, projectID)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("train_v2 task authoring is not active for project %q", projectID)
	}
	return nil
}

func (s *Service) TaskAuthoringRead(ctx context.Context, projectID, taskID string) (model.TaskAuthoring, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return model.TaskAuthoring{}, err
	}
	var task model.TaskAuthoring
	if err := s.Hub.ReadJSON(ctx, s.taskAuthoringPath(projectID, taskID), &task); err != nil {
		return model.TaskAuthoring{}, err
	}
	if task.ProjectID != projectID {
		return model.TaskAuthoring{}, fmt.Errorf("task authoring project_id mismatch")
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return model.TaskAuthoring{}, err
	}
	return task, nil
}

func (s *Service) TaskAuthoringFind(ctx context.Context, taskID string) (model.TaskAuthoring, error) {
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return model.TaskAuthoring{}, err
	}
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	for _, project := range projects {
		task, readErr := s.TaskAuthoringRead(ctx, project.ID, taskID)
		if readErr == nil {
			return task, nil
		}
		if !IsNotFound(readErr) {
			return model.TaskAuthoring{}, readErr
		}
	}
	return model.TaskAuthoring{}, fmt.Errorf("task authoring not found: %s", taskID)
}

func (s *Service) TaskAuthoringCreate(ctx context.Context, in TaskAuthoringCreateInput) (model.TaskAuthoring, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		task, result, err := s.taskAuthoringCreateOnce(ctx, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return task, result, err
		}
	}
}

func (s *Service) taskAuthoringCreateOnce(ctx context.Context, in TaskAuthoringCreateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if in.CreatedBy == "" || strings.ContainsAny(in.CreatedBy, "\x00\r\n") {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	if in.ADRRelation == "" {
		in.ADRRelation = model.TaskADRNoRequired
	}
	draft := trainv2.AuthoringDraft{Title: in.Title, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria, Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies, PreparationReferences: in.PreparationReferences, Metadata: in.Metadata, ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences}
	if err := trainv2.ValidateDraft(draft); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	id, err := model.FormatTaskID(identifiers.ProjectCode, identifiers.NextTaskNumber)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	now := s.durableNow()
	task, err := trainv2.NewTask(in.ProjectID, id, draft, in.CreatedBy, now)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	updatedIdentifiers := identifiers
	if updatedIdentifiers.NextTaskNumber < model.MaxSafeInteger {
		updatedIdentifiers.NextTaskNumber++
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create train_v2 task "+task.ID, func(w string) ([]string, error) {
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(in.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextTaskNumber != identifiers.NextTaskNumber {
			return nil, fmt.Errorf("project identifiers changed before task authoring allocation")
		}
		for _, path := range []string{s.taskAuthoringPath(in.ProjectID, task.ID), s.taskPath(in.ProjectID, task.ID)} {
			if _, statErr := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); statErr == nil {
				return nil, fmt.Errorf("task already exists")
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
		}
		path := s.taskAuthoringPath(in.ProjectID, task.ID)
		if err := hub.WriteJSON(w, path, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.projectIdentifiersPath(in.ProjectID), updatedIdentifiers); err != nil {
			return nil, err
		}
		return []string{path, s.projectIdentifiersPath(in.ProjectID)}, nil
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return task, OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: task.Status}, nil
}
