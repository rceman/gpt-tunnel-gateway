package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func taskDispatchSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "agent": str("Optional logical coding Agent identity, such as GTW-CODER.")}, "key")
}

func taskExecutionStatusSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier.")}, "key")
}

func taskExecutionPublicHeadSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[a-f0-9]{8}$"}
}

func taskExecutionLifecycleOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "status": outputString(), "stage": outputString(), "worktree": outputString(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "updated_at": outputDateTime()}, "key", "status", "stage", "worktree", "head", "agent", "execution_revision")
}

func taskExecutionStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "status": outputString(), "stage": outputString(), "worktree": outputString(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "updated_at": outputDateTime()}, "key", "status")
}

func taskExecutionOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func (s *Server) registerTaskExecutionActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RolePlanner
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "task/dispatch",
		Description:          "Dispatch one canonical Task to exactly one eligible logical coding Agent.",
		InputSchema:          taskDispatchSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskDispatchSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Agent     string `json:"agent,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			result, err := s.Service.TaskExecutionDispatch(ctx, service.TaskExecutionDispatchInput{ProjectID: in.ProjectID, Key: in.Key, Agent: in.Agent})
			return result, err
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/status",
		Description:          "Read the durable execution state for one canonical Task.",
		InputSchema:          taskExecutionStatusSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionStatusSchema()),
		OutputSchema:         taskExecutionStatusOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionStatus(ctx, in.ProjectID, in.Key)
		},
	}); err != nil {
		return err
	}
	if err := s.registerTaskExecutionReviewActions(); err != nil {
		return err
	}
	return s.registerTaskExecutionIntegrateAction()
}
