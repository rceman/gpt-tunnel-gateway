package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskExecutionTestSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier.")}, "key")
}

func taskExecutionVerificationOutputSchema() map[string]any {
	revision := outputInteger()
	revision["minimum"] = 1
	return closedOutput(map[string]any{
		"operation_id":   outputString(),
		"task_revision":  revision,
		"task_digest":    taskExecutionPublicHeadSchema(),
		"candidate_head": taskExecutionPublicHeadSchema(),
		"candidate_tree": taskExecutionPublicHeadSchema(),
		"main_base":      taskExecutionPublicHeadSchema(),
		"gate_profile":   taskExecutionPublicHeadSchema(),
		"verified_at":    outputDateTime(),
	}, "operation_id", "task_revision", "task_digest", "candidate_head", "candidate_tree", "main_base", "gate_profile", "verified_at")
}

func taskExecutionTestOutputSchema() map[string]any {
	errorValue := outputString()
	errorValue["maxLength"] = 2048
	return closedOutput(map[string]any{
		"operation_id": outputString(),
		"status":       outputEnum("accepted", "running", "completed", "failed", "outcome_unknown"),
		"result":       taskExecutionLifecycleOutputSchema(),
		"error":        errorValue,
		"created_at":   outputDateTime(),
		"updated_at":   outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func (s *Server) registerTaskExecutionTestAction() error {
	return s.RegisterGenericAction(GenericAction{
		Path:                 "task/test",
		Description:          "Run the full repository suite and every integration-class required gate on the exact reviewed Task candidate, recording durable verification proof.",
		AuthorityRole:        "lead",
		SessionBound:         true,
		LocalReceiptOnly:     true,
		InputSchema:          taskExecutionTestSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionTestSchema()),
		OutputSchema:         taskExecutionTestOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionTestAsync(ctx, service.TaskExecutionTestInput{ProjectID: in.ProjectID, Key: in.Key})
		},
	})
}
