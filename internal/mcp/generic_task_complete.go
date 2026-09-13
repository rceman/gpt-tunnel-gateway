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
	acceptanceItem := obj(map[string]any{
		"criterion": map[string]any{"type": "integer", "description": "1-based Task acceptance criterion position.", "minimum": float64(1), "maximum": float64(128)},
		"evidence":  map[string]any{"type": "array", "description": "Planner task-review Journal evidence for this criterion.", "minItems": 1, "maxItems": 8, "uniqueItems": true, "items": jrn},
	}, "criterion", "evidence")
	return obj(map[string]any{
		"key":    key,
		"mode":   map[string]any{"type": "string", "description": "Completion mode.", "enum": []any{"integrated", "non_code", "historical"}},
		"reason": map[string]any{"type": "string", "description": "Completion reason.", "minLength": 1, "maxLength": 1024},
		"acceptance": map[string]any{"type": "array", "description": "Acceptance evidence for every Task criterion.",
			"minItems": 1, "maxItems": 128, "items": acceptanceItem},
	}, "key", "mode", "reason", "acceptance")
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
		Description:          "Complete one canonical Task with acceptance-backed Planner Journal evidence.",
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
