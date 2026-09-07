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

func genericActionSuccessWithPagination(result map[string]any, pagination map[string]any) map[string]any {
	success := genericActionSuccess(result)
	if pagination != nil {
		success["pagination"] = pagination
	}
	return success
}

func detachPrivateTransportMetadata(result map[string]any) (map[string]any, map[string]any, error) {
	public := make(map[string]any, len(result))
	for key, value := range result {
		if key != "_pagination" && key != "_metrics" {
			public[key] = value
		}
	}
	private, ok := result["_pagination"]
	if !ok {
		return public, nil, nil
	}
	pagination, ok := private.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("invalid private pagination metadata")
	}
	cursor, ok := pagination["next_cursor"].(string)
	if !ok || strings.TrimSpace(cursor) == "" {
		return nil, nil, fmt.Errorf("invalid private pagination cursor")
	}
	return public, map[string]any{"next_cursor": cursor}, nil
}

func sanitizeTransportOutputSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	sanitized := make(map[string]any, len(schema))
	for key, value := range schema {
		sanitized[key] = value
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		cleanProperties := make(map[string]any, len(properties))
		for property, propertySchema := range properties {
			if property != "_pagination" && property != "_metrics" {
				cleanProperties[property] = propertySchema
			}
		}
		sanitized["properties"] = cleanProperties
	}
	if required, ok := schema["required"].([]string); ok {
		cleanRequired := make([]string, 0, len(required))
		for _, field := range required {
			if field != "_pagination" && field != "_metrics" {
				cleanRequired = append(cleanRequired, field)
			}
		}
		sanitized["required"] = cleanRequired
	}
	return sanitized
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
	// Preserve the established generic-action diagnostics for ordinary closed
	// contracts while allowing recursive validation for composed contracts.
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
