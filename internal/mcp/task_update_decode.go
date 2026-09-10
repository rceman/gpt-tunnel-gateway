package mcp

import (
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func decodeTaskUpdateInput(raw json.RawMessage, out *service.TaskAuthoringUpdateInput) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if key, ok := fields["key"]; ok {
		fields["task_id"] = key
		delete(fields, "key")
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return decode(normalized, out)
}
