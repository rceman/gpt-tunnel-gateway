package service

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func (s *Service) durableMutationExecutionSet3(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case "agent-disable":
		var input AgentDisableInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		agent, result, err := s.AgentDisable(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"agent": agent, "operation": result})
	case "watcher-guide-update":
		var input WatcherGuideUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.WatcherGuideUpdate(ctx, input)
		if err != nil {
			return nil, err
		}
		guide, err := s.WatcherGuideRead(ctx, input.ProjectID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"guide": guide, "operation": result})
	case "watcher-nudge":
		var input WatcherNudgeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.WatcherNudge(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "project-configuration-update":
		var input ProjectConfigurationUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		configuration, result, err := s.ProjectConfigurationUpdate(projectConfigurationMutationContext(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"configuration": configuration, "operation": result})
	case "task-supersede":
		var input TaskSupersedeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		task, result, err := s.TaskSupersede(ctx, input.OldTaskID, input.Task)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"task": task, "operation": result})
	case "task-work":
		var input TaskWorkInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TaskWork(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-finalize":
		var input TaskFinalizeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TaskFinalize(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	return nil, nil
}
