package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func operationIDFromRaw(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var operationID string
	_ = json.Unmarshal(fields["operation_id"], &operationID)
	return operationID
}
func genericActionError(_ string, message any) map[string]any {
	if err, ok := message.(error); ok {
		var structured interface {
			StructuredActionError() map[string]any
		}
		if errors.As(err, &structured) {
			return map[string]any{"result": map[string]any{"error": structured.StructuredActionError()}, "is_error": true}
		}
	}
	return map[string]any{"result": map[string]any{"error": fmt.Sprint(message)}, "is_error": true}
}
func genericActionSuccess(result map[string]any) map[string]any {
	return map[string]any{"result": result, "is_error": false}
}
func genericBatchResult(action string, result map[string]any) map[string]any {
	item := map[string]any{"action": action}
	for key, value := range result {
		item[key] = value
	}
	return item
}
func validateGenericActionInput(schema map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("input must be an object")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("input must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("input has trailing JSON content")
	}
	// Preserve the established generic-action diagnostics for ordinary
	// contracts. Mode-dispatched contracts such as task/create use oneOf and
	// must go through the recursive schema validator so each branch is checked
	// truthfully.
	if _, hasOneOf := schema["oneOf"]; !hasOneOf && schema["additionalProperties"] == false {
		if err := validateToolArguments(schema, raw); err != nil {
			return err
		}
	}
	if err := validateSchemaValue(schema, value, "input"); err != nil {
		return err
	}
	return nil
}
