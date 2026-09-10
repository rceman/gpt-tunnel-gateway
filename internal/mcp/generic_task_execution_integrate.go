package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskExecutionIntegrateSchema() map[string]any {
	return obj(map[string]any{"key": str("Canonical Task identifier."), "comment": str("Optional bounded integration comment.")}, "key")
}

func (s *Server) registerTaskExecutionIntegrateAction() error {
	return s.RegisterGenericAction(GenericAction{
		Path:                 "task/integrate",
		Description:          "Integrate one fully reviewed Task lane into the exact canonical main branch.",
		AuthorityRole:        "planner",
		SessionBound:         true,
		LocalReceiptOnly:     true,
		InputSchema:          taskExecutionIntegrateSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskExecutionIntegrateSchema()),
		OutputSchema:         taskExecutionLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Comment   string `json:"comment,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TaskExecutionIntegrate(ctx, service.TaskExecutionIntegrateInput{ProjectID: in.ProjectID, Key: in.Key, Comment: in.Comment})
		},
	})
}
