package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskExecutionReviewSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase")}, "key", "stage")
}

func taskExecutionReviewDecisionSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase"), "decision": outputEnum("accept", "reject"), "comment": str("Optional bounded review comment.")}, "key", "stage", "decision")
}

func taskExecutionReworkSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "stage": outputEnum("code", "tests", "rebase"), "comment": str("Required bounded rework comment.")}, "key", "stage", "comment")
}

func taskExecutionReviewOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "stage": outputEnum("code", "tests", "rebase"), "status": outputString(), "worktree": outputString(), "head": taskExecutionPublicHeadSchema(), "agent": outputString(), "execution_revision": outputInteger(), "submitted_at": outputDateTime()}, "key", "stage", "status", "worktree", "head", "agent", "execution_revision", "submitted_at")
}

func (s *Server) registerTaskExecutionReviewActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = "planner"
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "task/review",
		Description:          "Read the latest immutable Task execution submission for review.",
		InputSchema:          taskExecutionReviewSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReviewSchema()),
		OutputSchema:         taskExecutionReviewOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionReview(ctx, service.TaskExecutionReviewInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage})
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/review_decide",
		Description:          "Accept or reject one immutable Task execution review.",
		InputSchema:          taskExecutionReviewDecisionSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReviewDecisionSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
				Decision  string `json:"decision"`
				Comment   string `json:"comment,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionReviewDecide(ctx, service.TaskExecutionReviewDecisionInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage, Decision: in.Decision, Comment: in.Comment})
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:                 "task/rework",
		Description:          "Reopen one Task execution stage with a bounded Planner comment.",
		InputSchema:          taskExecutionReworkSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionReworkSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Stage     string `json:"stage"`
				Comment   string `json:"comment"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionRework(ctx, service.TaskExecutionReworkInput{ProjectID: in.ProjectID, Key: in.Key, Stage: in.Stage, Comment: in.Comment})
		},
	})
}
