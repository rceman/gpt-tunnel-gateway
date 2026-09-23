package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskExecutionIntegrateSHA(desc string) map[string]any {
	schema := str(desc + " Public commit fingerprints are exactly 8 lowercase hexadecimal characters.")
	schema["minLength"], schema["maxLength"] = 8, 8
	schema["pattern"] = `^[0-9a-f]{8}$`
	return schema
}

func taskExecutionIntegrateSchema() map[string]any {
	comment := str("Optional bounded integration comment.")
	comment["maxLength"] = 1024
	key := str("Canonical Task identifier.")
	evidence := str("Canonical Journal evidence identifier.")
	evidence["pattern"] = model.JournalIDPattern
	evidence["minLength"] = 8
	evidence["maxLength"] = 23
	historical := map[string]any{"oneOf": []any{
		obj(map[string]any{
			"integration_head": taskExecutionIntegrateSHA("Already-landed canonical integration commit."),
			"profile":          map[string]any{"type": "string", "description": "Historical proof profile.", "enum": []any{"legacy"}},
			"evidence":         evidence,
		}, "integration_head", "profile", "evidence"),
		obj(map[string]any{
			"integration_head": taskExecutionIntegrateSHA("Already-landed canonical integration commit."),
			"profile":          map[string]any{"type": "string", "description": "Historical proof profile.", "enum": []any{"bootstrap_full"}},
			"evidence":         evidence,
			"candidate_head":   taskExecutionIntegrateSHA("Reviewed candidate head."),
			"main_base":        taskExecutionIntegrateSHA("Verified canonical base."),
		}, "integration_head", "profile", "evidence", "candidate_head", "main_base"),
	}}
	return map[string]any{"type": "object", "oneOf": []any{
		obj(map[string]any{
			"key":     key,
			"comment": comment,
			"mode":    map[string]any{"type": "string", "description": "Integration mode; omitted defaults to verified.", "enum": []any{"verified"}, "default": "verified"},
		}, "key"),
		obj(map[string]any{
			"key":        key,
			"comment":    comment,
			"mode":       map[string]any{"type": "string", "description": "Integration mode.", "enum": []any{"historical"}},
			"historical": historical,
		}, "key", "mode", "historical"),
	}}
}

// taskExecutionIntegrateExecutionSchema keeps the public oneOf branches closed
// and adds the session-derived project_id to each top-level branch.
func taskExecutionIntegrateExecutionSchema(public map[string]any) map[string]any {
	projectID := str("Session-derived project identity.")
	branches, _ := public["oneOf"].([]any)
	out := make([]any, 0, len(branches))
	for _, raw := range branches {
		branch, _ := raw.(map[string]any)
		props := map[string]any{"project_id": projectID}
		if p, ok := branch["properties"].(map[string]any); ok {
			for k, v := range p {
				props[k] = v
			}
		}
		required := []string{"project_id"}
		if r, ok := branch["required"].([]string); ok {
			required = append(required, r...)
		}
		out = append(out, obj(props, required...))
	}
	return map[string]any{"type": "object", "oneOf": out}
}

func taskExecutionIntegrateOutputSchema() map[string]any {
	errorValue := outputString()
	errorValue["maxLength"] = 2048
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputEnum("accepted", "running", "completed", "failed", "outcome_unknown"), "result": taskExecutionLifecycleOutputSchema(), "error": errorValue, "created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func (s *Server) registerTaskExecutionIntegrateAction() error {
	return s.RegisterGenericAction(GenericAction{
		Path:                 "task/integrate",
		Description:          "Integrate one fully reviewed Task lane into canonical main, or recognize an already-landed integration from canonical Journal evidence.",
		AuthorityRole:        "lead",
		SessionBound:         true,
		LocalReceiptOnly:     true,
		InputSchema:          taskExecutionIntegrateSchema(),
		ExecutionInputSchema: taskExecutionIntegrateExecutionSchema(taskExecutionIntegrateSchema()),
		OutputSchema:         taskExecutionIntegrateOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskExecutionIntegrateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionIntegrateAsync(ctx, in)
		},
	})
}
