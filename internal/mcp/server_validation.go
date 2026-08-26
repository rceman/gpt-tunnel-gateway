package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func requireToolAuthority(ctx context.Context, toolName string) error {
	return requireActionAuthority(ctx, actionAuthorityContractFor(toolName))
}

func validateToolArguments(schema map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var args map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return fmt.Errorf("arguments must be an object: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	if _, hasOneOf := schema["oneOf"]; hasOneOf {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("arguments must be an object: %w", err)
		}
		options, _ := schema["oneOf"].([]any)
		matches := 0
		for _, candidate := range options {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && validateSchemaValue(candidateSchema, value, "arguments") == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("arguments: expected exactly one matching input shape, got %d", matches)
		}
		return nil
	}
	properties, _ := schema["properties"].(map[string]any)
	for key := range args {
		if _, ok := properties[key]; !ok {
			return fmt.Errorf("unknown argument %q", key)
		}
	}
	if required, ok := schema["required"].([]string); ok {
		for _, key := range required {
			if _, exists := args[key]; !exists {
				return fmt.Errorf("missing required argument %q", key)
			}
		}
	} else if requiredAny, ok := schema["required"].([]any); ok {
		for _, value := range requiredAny {
			key, _ := value.(string)
			if key != "" {
				if _, exists := args[key]; !exists {
					return fmt.Errorf("missing required argument %q", key)
				}
			}
		}
	}
	if _, hasSessionEnvelope := properties["session"]; hasSessionEnvelope {
		return nil
	}
	if _, exists := args["session_id"]; !exists {
		if sessionless, _ := schema["x-sessionless"].(bool); sessionless {
			return nil
		}
		bootstrapAction, _ := schema["x-allow-missing-session-action"].(string)
		var action string
		_ = json.Unmarshal(args["action"], &action)
		if action != bootstrapAction {
			return fmt.Errorf("missing required argument %q", "session_id")
		}
	}
	return nil
}

const maxToolCallMetaBytes = 64 << 10

func validateToolCallMeta(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxToolCallMetaBytes {
		return fmt.Errorf("_meta exceeds %d bytes", maxToolCallMetaBytes)
	}
	var value map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("_meta must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("_meta has trailing JSON content")
	}
	return nil
}

func toolResult(tool Tool, value any, isError bool) map[string]any {
	if !isError && tool.OutputSchema == nil {
		text, ok := value.(string)
		if !ok {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "tool output contract violation: expected plain text"}}, "isError": true}
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}
	}
	obj := normalizeObject(value)
	text, _ := json.MarshalIndent(obj, "", "  ")
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": isError}
	if isError {
		return result
	}
	if err := validateOutputValue(tool.OutputSchema, obj); err != nil {
		failure := map[string]any{"error": "tool output contract violation: " + err.Error()}
		failureText, _ := json.MarshalIndent(failure, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(failureText)}}, "isError": true}
	}
	result["structuredContent"] = obj
	return result
}

func normalizeObject(v any) map[string]any {
	if v == nil {
		return map[string]any{"ok": true}
	}
	data, _ := json.Marshal(v)
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil && obj != nil {
		return obj
	}
	var arr []any
	if json.Unmarshal(data, &arr) == nil {
		return map[string]any{"items": arr}
	}
	return map[string]any{"value": v}
}

func obj(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func integer(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}

func array(items any) map[string]any { return map[string]any{"type": "array", "items": items} }

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func getString(raw json.RawMessage, key string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	v, ok := m[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

func optionalString(raw json.RawMessage, key string) string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	v, _ := m[key].(string)
	return v
}

func intArg(raw json.RawMessage, key string, def int) int {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}

func optionalInteger(raw json.RawMessage, key string) (int, bool, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return 0, false, err
	}
	value, ok := values[key]
	if !ok {
		return 0, false, nil
	}
	if string(value) == "null" {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}
	var integerValue int
	if err := json.Unmarshal(value, &integerValue); err != nil {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}
	return integerValue, true, nil
}

type toolAdder func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error))
