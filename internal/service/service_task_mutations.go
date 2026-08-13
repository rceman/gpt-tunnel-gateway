package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskSupersedeOnce(ctx context.Context, oldID string, in TaskCreateInput) (model.Task, OperationResult, error) {
	if err := requireCanonicalTaskID(oldID); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	old, err := s.findTask(ctx, oldID)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if in.ProjectID == "" {
		in.ProjectID = old.ProjectID
	}
	if in.ProjectID != old.ProjectID {
		return model.Task{}, OperationResult{}, fmt.Errorf("superseding task must remain in project")
	}
	if in.Slug == "" {
		return model.Task{}, OperationResult{}, fmt.Errorf("slug is required")
	}
	if err := model.ValidateTaskSlug(in.Slug); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, old.ProjectID)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	_, effectivePolicy, err := s.deriveTaskWorkflowPolicy(ctx, old.ProjectID, in.OperationClass, in.RequiredGates)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	id, err := model.FormatTaskID(identifiers.ProjectCode, identifiers.NextTaskNumber)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if identifiers.NextTaskNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.taskPath(old.ProjectID, id)); readErr == nil {
			return model.Task{}, OperationResult{}, fmt.Errorf("task allocator exhausted for project %q", old.ProjectID)
		} else if !IsNotFound(readErr) {
			return model.Task{}, OperationResult{}, readErr
		}
	}
	branch := "task/" + id + "-" + in.Slug
	now := time.Now().UTC()
	newTask := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Title: in.Title, Objective: in.Objective, Branch: branch, AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), WorkflowPolicyRevision: effectivePolicy.WorkflowPolicyRevision, OperationClass: effectivePolicy.OperationClass, EffectiveCIField: effectivePolicy.EffectiveCIField, EffectiveCIMode: effectivePolicy.EffectiveCIMode, WaitForCI: effectivePolicy.WaitForCI, CIBlocking: effectivePolicy.CIBlocking, AgentMayWait: effectivePolicy.AgentMayWait, Status: "created", Supersedes: old.ID, CreatedBy: in.CreatedBy, CreatedAt: now}
	hash, err := model.HashTask(newTask)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	newTask.SHA256 = hash
	if err := model.ValidateTask(newTask); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	original := old
	oldState, err := s.taskState(ctx, original)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if oldState.Status == "completed" || oldState.Status == "cancelled" || oldState.Status == "superseded" {
		return model.Task{}, OperationResult{}, fmt.Errorf("cannot supersede terminal task")
	}
	oldState.Status = "superseded"
	oldState.SupersededBy = newTask.ID
	oldState.UpdatedAt = now
	newState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: newTask.ID, TaskSHA256: newTask.SHA256, Status: "created", UpdatedAt: now}
	updatedIdentifiers := identifiers
	if updatedIdentifiers.NextTaskNumber < model.MaxSafeInteger {
		updatedIdentifiers.NextTaskNumber++
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: supersede task "+old.ID, func(w string) ([]string, error) {
		targetPaths := []string{s.taskPath(newTask.ProjectID, newTask.ID), s.taskStatePath(newTask.ProjectID, newTask.ID)}
		for _, path := range targetPaths {
			if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
				return nil, fmt.Errorf("task supersede target already exists: %s", path)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		paths := []string{s.taskPath(newTask.ProjectID, newTask.ID), s.taskStatePath(newTask.ProjectID, newTask.ID), s.taskStatePath(old.ProjectID, old.ID)}
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(old.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextTaskNumber != identifiers.NextTaskNumber {
			return nil, fmt.Errorf("project identifiers changed before task allocation")
		}
		paths = append(paths, s.projectIdentifiersPath(old.ProjectID))
		vals := []any{newTask, newState, oldState}
		for i, p := range paths {
			if i >= len(vals) {
				break
			}
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		if err := hub.WriteJSON(w, s.projectIdentifiersPath(old.ProjectID), updatedIdentifiers); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	return newTask, OperationResult{
		Hub:       tx,
		ProjectID: newTask.ProjectID,
		TaskID:    newTask.ID,
		Status:    "created",
	}, nil
}

func (s *Service) TaskCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	return OperationResult{}, errRunAuthorityRetired
}
