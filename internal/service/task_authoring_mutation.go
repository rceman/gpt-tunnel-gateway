package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) TaskAuthoringUpdate(ctx context.Context, in TaskAuthoringUpdateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || in.UpdatedBy == "" || strings.ContainsAny(in.UpdatedBy, "\x00\r\n") {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("expected_revision and updated_by are required")
	}
	current, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.TaskID)
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
	updated, changed, err := trainv2.UpdateTask(current, trainv2.AuthoringPatch{Type: in.Type, Scope: in.Scope, Title: in.Title, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria, Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies, PreparationReferences: in.PreparationReferences, Metadata: in.Metadata, ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences}, in.UpdatedBy, s.durableNow())
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if !changed {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			TaskID:    current.ID,
			Status:    current.Status,
		}, nil
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update train_v2 task "+current.ID, func(w string) ([]string, error) {
		var latest model.TaskAuthoring
		if err := readWorktreeJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), &latest); err != nil {
			return nil, err
		}
		if err := trainv2.CheckRevision(latest, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
			return nil, err
		}
		admitted, err := taskAdmittedToNonterminalTrainInWorktree(w, s.trainV2Root(in.ProjectID), in.TaskID)
		if err != nil {
			return nil, err
		}
		if admitted {
			return nil, fmt.Errorf("ready Task %q is admitted to a nonterminal Train and cannot be edited", in.TaskID)
		}
		if err := hub.WriteJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), updated); err != nil {
			return nil, err
		}
		return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return updated, OperationResult{
		Hub:       tx,
		ProjectID: updated.ProjectID,
		TaskID:    updated.ID,
		Status:    updated.Status,
	}, nil
}

func (s *Service) TaskAuthoringReady(ctx context.Context, in TaskAuthoringReadyInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.ReadyBy) == "" || strings.ContainsAny(in.ReadyBy, "\x00\r\n") {
		return model.TaskAuthoring{}, OperationResult{}, fmt.Errorf("expected_revision and ready_by are required")
	}
	current, err := s.TaskAuthoringRead(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := trainv2.CheckRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if current.Status == model.TaskAuthoringReady {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			TaskID:    current.ID,
			Status:    current.Status,
		}, nil
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
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: ready train_v2 task "+current.ID, func(w string) ([]string, error) {
		var latest model.TaskAuthoring
		if err := readWorktreeJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), &latest); err != nil {
			return nil, err
		}
		if err := trainv2.CheckRevision(latest, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
			return nil, err
		}
		if latest.Status == model.TaskAuthoringReady {
			return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
		}
		if err := s.validateTaskDependenciesInWorktree(w, in.ProjectID, []model.TaskAuthoring{latest}); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), ready); err != nil {
			return nil, err
		}
		return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return ready, OperationResult{
		Hub:       tx,
		ProjectID: ready.ProjectID,
		TaskID:    ready.ID,
		Status:    ready.Status,
	}, nil
}

func (s *Service) TaskAuthoringList(ctx context.Context, in TaskAuthoringListInput) (TaskAuthoringListResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskAuthoringListResult{}, err
	}
	if len(in.Query) > 256 {
		return TaskAuthoringListResult{}, fmt.Errorf("task query exceeds 256 characters")
	}
	if in.Type != "" {
		if _, err := model.NormalizeTaskType(in.Type); err != nil {
			return TaskAuthoringListResult{}, err
		}
	}
	if in.Execution != "" {
		if _, err := model.NormalizeTaskExecution(in.Execution); err != nil {
			return TaskAuthoringListResult{}, err
		}
	}
	if s.Durability != nil {
		tasks, err := s.sharedTaskAuthoringAll(ctx, in.ProjectID)
		if err != nil {
			return TaskAuthoringListResult{}, err
		}
		filtered := tasks[:0]
		for _, task := range tasks {
			if (in.Status == "" || task.Status == in.Status) && taskAuthoringMatches(task, in.Query, in.Type, in.Execution) {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
		limit := in.Limit
		if limit == 0 {
			limit = DefaultTaskListLimit
		}
		if limit < 1 || limit > MaxTaskListLimit {
			return TaskAuthoringListResult{}, fmt.Errorf("task authoring list limit must be between 1 and %d", MaxTaskListLimit)
		}
		if len(tasks) > limit {
			tasks = tasks[:limit]
		}
		return TaskAuthoringListResult{Tasks: tasks}, nil
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(in.ProjectID)+"/tasks-v2", ".json")
	if err != nil {
		if IsNotFound(err) {
			return TaskAuthoringListResult{Tasks: []model.TaskAuthoring{}}, nil
		}
		return TaskAuthoringListResult{}, err
	}
	tasks := make([]model.TaskAuthoring, 0, len(paths))
	for _, path := range paths {
		var task model.TaskAuthoring
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return TaskAuthoringListResult{}, err
		}
		if task.ProjectID != in.ProjectID || (in.Status != "" && task.Status != in.Status) || !taskAuthoringMatches(task, in.Query, in.Type, in.Execution) {
			continue
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return TaskAuthoringListResult{}, err
		}
		task.Type = model.DefaultTaskType(task.Type)
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	limit := in.Limit
	if limit == 0 {
		limit = DefaultTaskListLimit
	}
	if limit < 1 || limit > MaxTaskListLimit {
		return TaskAuthoringListResult{}, fmt.Errorf("task authoring list limit must be between 1 and %d", MaxTaskListLimit)
	}
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return TaskAuthoringListResult{Tasks: tasks}, nil
}

func taskAuthoringMatches(task model.TaskAuthoring, query string, typ model.TaskType, execution model.TaskExecution) bool {
	actualType := model.DefaultTaskType(task.Type)
	if typ != "" && actualType != typ {
		return false
	}
	actualExecution := task.Execution
	if execution != "" && actualExecution != execution {
		return false
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	text := strings.ToLower(strings.Join([]string{task.ID, string(actualType), task.Title, task.Objective, task.Status, task.CreatedBy, task.Priority, task.ADRRelation}, "\n"))
	for _, value := range append(append(append([]string{}, task.AcceptanceCriteria...), task.Constraints...), append(task.Dependencies, task.PreparationReferences...)...) {
		text += "\n" + strings.ToLower(value)
	}
	return strings.Contains(text, query)
}

func (s *Service) taskAuthoringAll(ctx context.Context, projectID string) ([]model.TaskAuthoring, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if s.Durability != nil {
		return s.sharedTaskAuthoringAll(ctx, projectID)
	}
	paths, err := s.Hub.List(ctx, s.projectPrefix(projectID)+"/tasks-v2", ".json")
	if err != nil {
		if IsNotFound(err) {
			return []model.TaskAuthoring{}, nil
		}
		return nil, err
	}
	tasks := make([]model.TaskAuthoring, 0, len(paths))
	for _, path := range paths {
		var task model.TaskAuthoring
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return nil, err
		}
		if task.ProjectID != projectID {
			continue
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return nil, err
		}
		task.Type = model.DefaultTaskType(task.Type)
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	return tasks, nil
}

func (s *Service) validateAuthoringADRReferences(ctx context.Context, task model.TaskAuthoring) error {
	if task.ADRRelation == model.TaskADRNoRequired {
		return nil
	}
	for _, id := range task.ADRReferences {
		adr, err := s.ADRRead(ctx, task.ProjectID, id)
		if err != nil {
			return fmt.Errorf("ADR %q is unavailable: %w", id, err)
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
