package mcp

import (
	"context"
	"encoding/json"
)

func (s *Server) addTaskTrainTools(add toolAdder) {
	add("task_train_status", "Read the bounded server-owned ordered Task Train status without discovering backlog work.", obj(map[string]any{
		"project_id": str("Project identifier"),
		"train_id":   str("Optional stable task train identifier"),
	}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		if trainID := optionalString(raw, "train_id"); trainID != "" {
			return s.Service.TaskTrainStatusByID(ctx, id, trainID)
		}
		return s.Service.TaskTrainStatus(ctx, id)
	})
}
