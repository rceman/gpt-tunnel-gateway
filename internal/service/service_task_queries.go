package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// taskStatusList reads only the task and mutable state fields needed by the
// project status and workflow-policy projections.  Full record enrichment
// also loads revisions, run history and review summaries; that work belongs
// to the canonical task list/read surfaces, not the bounded status path.

func (s *Service) taskStatusList(ctx context.Context, project string) ([]TaskRecord, error) {
	records, err := s.entityRegistry(project).ListRecords(ctx, entity.Query{Family: entity.TaskFamily})
	if err != nil {
		return nil, err
	}
	tasks := make([]model.Task, 0, len(records))
	for _, record := range records {
		var task model.Task
		if err := decodeStrict(record.Bytes, &task); err != nil {
			return nil, err
		}
		task.Type = model.DefaultTaskType(task.Type)
		tasks = append(tasks, task)
	}
	states, err := s.taskStatesBatch(ctx, tasks)
	if err != nil {
		return nil, err
	}
	items := []TaskRecord{}
	for _, task := range tasks {
		if err := model.ValidateTask(task); err != nil {
			return nil, err
		}
		if task.ProjectID != project {
			return nil, fmt.Errorf("task project_id mismatch: %s", task.ID)
		}
		state := states[task.ID]
		items = append(items, TaskRecord{
			Task:  task,
			State: state,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Task.CreatedAt.After(items[j].Task.CreatedAt) })
	return items, nil
}

func (s *Service) taskStatesBatch(ctx context.Context, tasks []model.Task) (map[string]model.TaskState, error) {
	states := make(map[string]model.TaskState, len(tasks))
	if len(tasks) == 0 {
		return states, nil
	}
	paths := make([]string, 0, len(tasks))
	for _, task := range tasks {
		paths = append(paths, s.taskStatePath(task.ProjectID, task.ID))
	}
	dataByPath, err := s.Hub.ReadFiles(ctx, paths)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		statePath := s.taskStatePath(task.ProjectID, task.ID)
		data, ok := dataByPath[statePath]
		if !ok {
			states[task.ID] = model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "created", UpdatedAt: task.CreatedAt}
			continue
		}
		var state model.TaskState
		if err := decodeStrict(data, &state); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(state, task); err != nil {
			return nil, err
		}
		states[task.ID] = state
	}
	return states, nil
}

func (s *Service) findTask(ctx context.Context, id string) (model.Task, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Task{}, err
	}
	for _, p := range projects {
		var t model.Task
		_, err := s.entityRegistry(p.ID).ReadInto(ctx, entity.TaskFamily, id, &t)
		if err == nil {
			t.Type = model.DefaultTaskType(t.Type)
			return t, nil
		}
		if !IsNotFound(err) {
			return model.Task{}, err
		}
	}
	return model.Task{}, notFoundf("task %s", id)
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
