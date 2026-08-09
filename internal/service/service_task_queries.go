package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskList(ctx context.Context, project string) ([]TaskRecord, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	runs, err := s.RunList(ctx, project)
	if err != nil {
		return nil, err
	}
	items := []TaskRecord{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return nil, err
		}
		state, err := s.taskState(ctx, task)
		if err != nil {
			return nil, err
		}
		var currentRevision *model.TaskRevision
		if model.ValidateCanonicalTaskID(task.ID) == nil {
			if revision, revisionErr := s.currentTaskRevision(ctx, task); revisionErr != nil {
				return nil, revisionErr
			} else {
				currentRevision = &revision
			}
		}
		summaries, err := s.taskReviewSummaries(ctx, task, runs)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskRecord{
			Task:            task,
			State:           state,
			CurrentRevision: currentRevision,
			RunSummaries:    summaries,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })
	return items, nil
}

// taskStatusList reads only the task and mutable state fields needed by the
// project status and workflow-policy projections.  Full TaskList enrichment
// also loads revisions, run history and review summaries; that work belongs
// to the explicit task-list/read surfaces, not the bounded status path.

func (s *Service) taskStatusList(ctx context.Context, project string) ([]TaskRecord, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/tasks", ".json")
	if err != nil {
		return nil, err
	}
	items := []TaskRecord{}
	for _, path := range paths {
		if strings.HasSuffix(path, ".state.json") || strings.HasSuffix(path, ".run-counter.json") || strings.Contains(path, "/revisions/") {
			continue
		}
		var task model.Task
		if err := s.Hub.ReadJSON(ctx, path, &task); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(task); err != nil {
			return nil, err
		}
		if task.ProjectID != project {
			return nil, fmt.Errorf("task project_id mismatch: %s", task.ID)
		}
		state, err := s.taskState(ctx, task)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskRecord{
			Task:  task,
			State: state,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })
	return items, nil
}

func (s *Service) findTask(ctx context.Context, id string) (model.Task, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Task{}, err
	}
	for _, p := range projects {
		var t model.Task
		err := s.Hub.ReadJSON(ctx, s.taskPath(p.ID, id), &t)
		if err == nil {
			return t, nil
		}
		if !IsNotFound(err) {
			return model.Task{}, err
		}
	}
	return model.Task{}, fmt.Errorf("task not found: %s", id)
}

func (s *Service) TaskReadRecord(ctx context.Context, id string) (TaskRecord, error) {
	task, err := s.findTask(ctx, id)
	if err != nil {
		return TaskRecord{}, err
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return TaskRecord{}, err
	}
	var currentRevision *model.TaskRevision
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		revision, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return TaskRecord{}, revisionErr
		}
		currentRevision = &revision
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return TaskRecord{}, err
	}
	summaries, err := s.taskReviewSummaries(ctx, task, runs)
	if err != nil {
		return TaskRecord{}, err
	}
	var policy *model.ProjectWorkflowPolicy
	if current, policyErr := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID); policyErr == nil {
		policy = &current
	} else if !IsNotFound(policyErr) {
		return TaskRecord{}, policyErr
	}
	return TaskRecord{
		Task:            task,
		State:           state,
		CurrentRevision: currentRevision,
		RunSummaries:    summaries,
		WorkflowPolicy:  policy,
	}, nil
}

func (s *Service) TaskSupersede(ctx context.Context, oldID string, in TaskCreateInput) (model.Task, OperationResult, error) {
	for attempt := 0; ; attempt++ {
		task, result, err := s.taskSupersedeOnce(ctx, oldID, in)
		if in.ExpectedHubRevision != "" || err == nil || !allocatorConflict(err) || attempt+1 >= allocatorRetryLimit {
			return task, result, err
		}
	}
}
