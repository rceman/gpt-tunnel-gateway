package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
