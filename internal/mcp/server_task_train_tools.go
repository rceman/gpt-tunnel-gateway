package mcp

import (
	"context"
	"encoding/json"
)

func (s *Server) addTaskTrainTools(add toolAdder) {
	add("task_train_status", "Read the bounded server-owned ordered Task Train status without discovering backlog work.", obj(map[string]any{
		"project_id": str("Project identifier"),
	}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		return s.Service.TaskTrainStatus(ctx, id)
	})
}
