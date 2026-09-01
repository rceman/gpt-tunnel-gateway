package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskCreateOnce(ctx context.Context, in TaskCreateInput) (model.Task, OperationResult, error) {
	if in.Slug == "" {
		return model.Task{}, OperationResult{}, fmt.Errorf("slug is required")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	operationClass := in.OperationClass
	if operationClass == "" {
		operationClass = "implementation"
	}
	_, effectivePolicy, err := s.deriveTaskWorkflowPolicy(ctx, in.ProjectID, operationClass, in.RequiredGates)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.Task{}, OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	var id, branch string
	if err := model.ValidateTaskSlug(in.Slug); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	id, err = model.FormatTaskID(identifiers.ProjectCode, identifiers.NextTaskNumber)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	if identifiers.NextTaskNumber == model.MaxSafeInteger {
		if _, readErr := s.Hub.ReadFile(ctx, s.taskPath(in.ProjectID, id)); readErr == nil {
			return model.Task{}, OperationResult{}, fmt.Errorf("task allocator exhausted for project %q", in.ProjectID)
		} else if !IsNotFound(readErr) {
			return model.Task{}, OperationResult{}, readErr
		}
	}
	branch = "task/" + id + "-" + in.Slug
	typ, err := model.NormalizeTaskType(in.Type)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	task := model.Task{SchemaVersion: model.SchemaVersion, ID: id, ProjectID: in.ProjectID, Type: typ, Title: in.Title, Objective: in.Objective, Branch: branch, AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...), Constraints: append([]string{}, in.Constraints...), RequiredGates: append([]string{}, in.RequiredGates...), WorkflowPolicyRevision: effectivePolicy.WorkflowPolicyRevision, OperationClass: effectivePolicy.OperationClass, EffectiveCIField: effectivePolicy.EffectiveCIField, EffectiveCIMode: effectivePolicy.EffectiveCIMode, WaitForCI: effectivePolicy.WaitForCI, CIBlocking: effectivePolicy.CIBlocking, AgentMayWait: effectivePolicy.AgentMayWait, Status: "created", Supersedes: in.Supersedes, CreatedBy: in.CreatedBy, CreatedAt: s.durableNow()}
	hash, err := model.HashTask(task)
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	task.SHA256 = hash
	if err := model.ValidateTask(task); err != nil {
		return model.Task{}, OperationResult{}, err
	}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: s.durableNow()}
	updatedIdentifiers := identifiers
	if updatedIdentifiers.NextTaskNumber < model.MaxSafeInteger {
		updatedIdentifiers.NextTaskNumber++
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create task "+task.ID, func(w string) ([]string, error) {
		path := s.taskPath(task.ProjectID, task.ID)
		statePath := s.taskStatePath(task.ProjectID, task.ID)
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("task already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(w, filepath.FromSlash(statePath))); err == nil {
			return nil, fmt.Errorf("task state already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		paths := []string{path, statePath}
		var current model.ProjectIdentifiers
		if err := readWorktreeJSON(w, s.projectIdentifiersPath(task.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.ProjectCode != identifiers.ProjectCode || current.NextTaskNumber != identifiers.NextTaskNumber {
			return nil, fmt.Errorf("project identifiers changed before task allocation")
		}
		paths = append(paths, s.projectIdentifiersPath(task.ProjectID))
		if err := hub.WriteJSON(w, path, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, statePath, state); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.projectIdentifiersPath(task.ProjectID), updatedIdentifiers); err != nil {
			return nil, err
		}
		return paths, nil
	})
	if err != nil {
		return model.Task{}, OperationResult{}, err
	}
	return task, OperationResult{
		Hub:       tx,
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Status:    "created",
	}, nil
}

func (s *Service) taskState(ctx context.Context, task model.Task) (model.TaskState, error) {
	var state model.TaskState
	err := s.Hub.ReadJSON(ctx, s.taskStatePath(task.ProjectID, task.ID), &state)
	if err != nil {
		if IsNotFound(err) {
			return model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: task.CreatedAt}, nil
		}
		return model.TaskState{}, err
	}
	if err := model.ValidateTaskState(state, task); err != nil {
		return model.TaskState{}, err
	}
	return state, nil
}

func (s *Service) updateTaskState(ctx context.Context, task model.Task, state model.TaskState, expected, subject string) (hub.TransactionResult, error) {
	if err := model.ValidateTaskState(state, task); err != nil {
		return hub.TransactionResult{}, err
	}
	return s.Hub.Transact(ctx, expected, subject, func(w string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(w, path, state); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}
