package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	if err := validateAuthoringInput(in.Title, in.Objective, in.AcceptanceCriteria, in.Constraints, in.Priority, in.Dependencies, in.PreparationReferences, in.Metadata, in.ADRRelation, in.ADRReferences); err != nil {
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
	task := model.TaskAuthoring{
		SchemaVersion: model.TaskAuthoringSchemaVersion, ID: id, ProjectID: in.ProjectID, Revision: 1,
		Title: in.Title, Objective: in.Objective, AcceptanceCriteria: append([]string{}, in.AcceptanceCriteria...),
		Constraints: append([]string{}, in.Constraints...), Priority: in.Priority, Dependencies: append([]string{}, in.Dependencies...),
		PreparationReferences: append([]string{}, in.PreparationReferences...), Metadata: cloneStringMap(in.Metadata), ADRRelation: in.ADRRelation,
		ADRReferences: append([]string{}, in.ADRReferences...), Status: model.TaskAuthoringPlanned, CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	task.RevisionSHA256, err = model.HashTaskAuthoring(task)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
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
	if err := checkAuthoringRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	updated := current
	changed := false
	if in.Title != nil && *in.Title != updated.Title {
		updated.Title, changed = *in.Title, true
	}
	if in.Objective != nil && *in.Objective != updated.Objective {
		updated.Objective, changed = *in.Objective, true
	}
	if in.AcceptanceCriteria != nil && !reflect.DeepEqual(*in.AcceptanceCriteria, updated.AcceptanceCriteria) {
		updated.AcceptanceCriteria, changed = append([]string{}, (*in.AcceptanceCriteria)...), true
	}
	if in.Constraints != nil && !reflect.DeepEqual(*in.Constraints, updated.Constraints) {
		updated.Constraints, changed = append([]string{}, (*in.Constraints)...), true
	}
	if in.Priority != nil && *in.Priority != updated.Priority {
		updated.Priority, changed = *in.Priority, true
	}
	if in.Dependencies != nil && !reflect.DeepEqual(*in.Dependencies, updated.Dependencies) {
		updated.Dependencies, changed = append([]string{}, (*in.Dependencies)...), true
	}
	if in.PreparationReferences != nil && !reflect.DeepEqual(*in.PreparationReferences, updated.PreparationReferences) {
		updated.PreparationReferences, changed = append([]string{}, (*in.PreparationReferences)...), true
	}
	if in.Metadata != nil && !reflect.DeepEqual(*in.Metadata, updated.Metadata) {
		updated.Metadata, changed = cloneStringMap(*in.Metadata), true
	}
	if in.ADRRelation != nil && *in.ADRRelation != updated.ADRRelation {
		updated.ADRRelation, changed = *in.ADRRelation, true
	}
	if in.ADRReferences != nil && !reflect.DeepEqual(*in.ADRReferences, updated.ADRReferences) {
		updated.ADRReferences, changed = append([]string{}, (*in.ADRReferences)...), true
	}
	if !changed {
		return current, OperationResult{ProjectID: current.ProjectID, TaskID: current.ID, Status: current.Status}, nil
	}
	if err := validateAuthoringInput(updated.Title, updated.Objective, updated.AcceptanceCriteria, updated.Constraints, updated.Priority, updated.Dependencies, updated.PreparationReferences, updated.Metadata, updated.ADRRelation, updated.ADRReferences); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	updated.Revision++
	updated.RevisionSHA256 = ""
	updated.Status = model.TaskAuthoringPlanned
	updated.ReadySeal = nil
	updated.UpdatedAt = s.durableNow()
	updated.RevisionSHA256, err = model.HashTaskAuthoring(updated)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if err := model.ValidateTaskAuthoring(updated); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update train_v2 task "+current.ID, func(w string) ([]string, error) {
		var latest model.TaskAuthoring
		if err := readWorktreeJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), &latest); err != nil {
			return nil, err
		}
		if err := checkAuthoringRevision(latest, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), updated); err != nil {
			return nil, err
		}
		return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return updated, OperationResult{Hub: tx, ProjectID: updated.ProjectID, TaskID: updated.ID, Status: updated.Status}, nil
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
	if err := checkAuthoringRevision(current, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	if current.Status == model.TaskAuthoringReady {
		return current, OperationResult{ProjectID: current.ProjectID, TaskID: current.ID, Status: current.Status}, nil
	}
	if err := s.validateAuthoringADRReferences(ctx, current); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	ready := current
	ready.Status = model.TaskAuthoringReady
	ready.ReadySeal = &model.TaskReadySeal{Revision: ready.Revision, RevisionSHA256: ready.RevisionSHA256, ReadyBy: in.ReadyBy, ReadyAt: s.durableNow()}
	ready.UpdatedAt = ready.ReadySeal.ReadyAt
	if err := model.ValidateTaskAuthoring(ready); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: ready train_v2 task "+current.ID, func(w string) ([]string, error) {
		var latest model.TaskAuthoring
		if err := readWorktreeJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), &latest); err != nil {
			return nil, err
		}
		if err := checkAuthoringRevision(latest, in.ExpectedRevision, in.ExpectedRevisionSHA256); err != nil {
			return nil, err
		}
		if latest.Status == model.TaskAuthoringReady {
			return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
		}
		if err := hub.WriteJSON(w, s.taskAuthoringPath(in.ProjectID, in.TaskID), ready); err != nil {
			return nil, err
		}
		return []string{s.taskAuthoringPath(in.ProjectID, in.TaskID)}, nil
	})
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return ready, OperationResult{Hub: tx, ProjectID: ready.ProjectID, TaskID: ready.ID, Status: ready.Status}, nil
}

func (s *Service) TaskAuthoringList(ctx context.Context, in TaskAuthoringListInput) (TaskAuthoringListResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskAuthoringListResult{}, err
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
		if task.ProjectID != in.ProjectID || (in.Status != "" && task.Status != in.Status) {
			continue
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return TaskAuthoringListResult{}, err
		}
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

func validateAuthoringInput(title, objective string, criteria, constraints []string, priority string, dependencies, preparation []string, metadata map[string]string, relation string, references []string) error {
	now := time.Unix(1, 0).UTC()
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "AAA-TSK1", ProjectID: "example", Revision: 1, RevisionSHA256: strings.Repeat("a", 64), Title: title, Objective: objective, AcceptanceCriteria: criteria, Constraints: constraints, Priority: priority, Dependencies: dependencies, PreparationReferences: preparation, Metadata: metadata, ADRRelation: relation, ADRReferences: references, Status: model.TaskAuthoringPlanned, CreatedBy: "validator", CreatedAt: now, UpdatedAt: now}
	// Validate content through the canonical model validator without making the
	// caller's input depend on a generated task identity or current clock.
	if err := model.ValidateTaskAuthoring(task); err != nil {
		if strings.Contains(err.Error(), "task authoring revision hash mismatch") {
			return nil
		}
		return err
	}
	return nil
}

func checkAuthoringRevision(task model.TaskAuthoring, revision int, hash string) error {
	if task.Revision != revision {
		return fmt.Errorf("task authoring revision conflict: expected %d, current %d", revision, task.Revision)
	}
	if hash != "" && task.RevisionSHA256 != hash {
		return fmt.Errorf("task authoring revision hash conflict")
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
