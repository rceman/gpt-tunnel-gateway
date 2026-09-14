package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskCompleteSchema() map[string]any {
	key := str("Canonical Task key.")
	key["pattern"] = `^[A-Z]{3}-TSK[1-9][0-9]*$`
	jrn := str("Canonical same-project Journal evidence identifier.")
	jrn["pattern"] = model.JournalIDPattern
	jrn["minLength"] = 8
	jrn["maxLength"] = 23
	return obj(map[string]any{
		"key":    key,
		"mode":   map[string]any{"type": "string", "description": "Completion mode.", "enum": []any{"integrated", "non_code", "historical"}},
		"reason": map[string]any{"type": "string", "description": "Planner's concise completion rationale.", "minLength": 1, "maxLength": 1024},
		"review": jrn,
	}, "key", "mode", "reason", "review")
}

func taskCompleteOutputSchema() map[string]any {
	status := outputString()
	status["const"] = model.TaskAuthoringDone
	revision := outputInteger()
	revision["minimum"] = float64(1)
	return closedOutput(map[string]any{
		"key":      outputString(),
		"status":   status,
		"revision": revision,
	}, "key", "status", "revision")
}

func (s *Server) registerTaskCompleteAction() error {
	return s.RegisterGenericAction(GenericAction{
		Path:                 "task/complete",
		Description:          "Complete one canonical Task with one final Planner task-review Journal reference.",
		AuthorityRole:        "planner",
		SessionBound:         true,
		LocalReceiptOnly:     true,
		InputSchema:          taskCompleteSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskCompleteSchema()),
		OutputSchema:         taskCompleteOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskCompleteInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("task/complete requires a bound session actor")
			}
			return s.Service.TaskComplete(ctx, in, actor)
		},
	})
}
