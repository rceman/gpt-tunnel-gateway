package model

import (
	"encoding/json"
	"fmt"
)

func (c *TaskRunCounter) UnmarshalJSON(data []byte) error {
	fields, err := decodeProjectIdentifiersObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "project_id", "task_id", "next_run_number":
		default:
			return fmt.Errorf("unknown task run counter field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "project_id", "task_id", "next_run_number"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("task run counter field %q is required", key)
		}
	}
	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil || schemaVersion > uint64(^uint(0)>>1) {
		if err == nil {
			err = fmt.Errorf("overflows int")
		}
		return fmt.Errorf("schema_version: %w", err)
	}
	var projectID, taskID string
	if err := json.Unmarshal(fields["project_id"], &projectID); err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	if err := json.Unmarshal(fields["task_id"], &taskID); err != nil {
		return fmt.Errorf("task_id: %w", err)
	}
	next, err := parseJSONInteger(fields["next_run_number"])
	if err != nil {
		return fmt.Errorf("next_run_number: %w", err)
	}
	*c = TaskRunCounter{
		SchemaVersion: int(schemaVersion),
		ProjectID:     projectID,
		TaskID:        taskID,
		NextRunNumber: next,
	}
	return nil
}
