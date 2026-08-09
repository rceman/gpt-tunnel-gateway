package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// decodeJSONValue rejects duplicate object members at every nesting level.
func decodeJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			out := map[string]any{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				if _, exists := out[name]; exists {
					return nil, fmt.Errorf("duplicate object key %q", name)
				}
				value, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				out[name] = value
			}
			_, err := dec.Token()
			return out, err
		case '[':
			out := []any{}
			for dec.More() {
				value, err := decodeJSONValue(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			_, err := dec.Token()
			return out, err
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter")
		}
	default:
		return tok, nil
	}
}

func strictJSONObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeJSONValue(dec)
	if err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON content")
		}
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("completion must be a JSON object")
	}
	return obj, nil
}

func requiredCompletionFields(obj map[string]any) error {
	allowed := map[string]bool{"schema_version": true, "run_id": true, "task_sha256": true, "task_revision": true, "task_revision_sha256": true, "task_run_number": true, "status": true, "summary": true, "gate_results": true, "acceptance_coverage": true, "deviations": true, "remaining_risks": true, "agent_feedback": true}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown completion field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "run_id", "task_sha256", "status", "summary", "gate_results", "acceptance_coverage", "deviations", "remaining_risks"} {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing completion field %q", key)
		}
	}
	return nil
}

func utf8Bounded(s string, max int, field string) error {
	if !utf8.ValidString(s) || len([]byte(s)) > max {
		return fmt.Errorf("invalid or oversized %s", field)
	}
	return nil
}

func decodeCompletionField(obj map[string]any, name string, out any) error {
	b, err := json.Marshal(obj[name])
	if err != nil {
		return fmt.Errorf("invalid %s", name)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func completionString(obj map[string]any, name string) (string, error) {
	v, ok := obj[name].(string)
	if !ok {
		return "", fmt.Errorf("completion field %q must be a string", name)
	}
	return v, nil
}

func completionStringArray(v any, name string) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("completion field %q must be an array", name)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("completion field %q must contain strings", name)
		}
		out = append(out, value)
	}
	return out, nil
}

func optionalCompletionUint(obj map[string]any, name string) (uint64, bool, error) {
	value, ok := obj[name]
	if !ok {
		return 0, false, nil
	}
	var number uint64
	if err := decodeCompletionField(obj, name, &number); err != nil || number == 0 || number > MaxSafeInteger {
		return 0, true, fmt.Errorf("completion field %q must be a positive safe integer", name)
	}
	_ = value
	return number, true, nil
}

func completionGates(v any) ([]CompletionGateResult, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("gate_results must be an array")
	}
	out := make([]CompletionGateResult, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("gate result must be an object")
		}
		if len(obj) != 2 {
			return nil, fmt.Errorf("gate result has unknown or missing fields")
		}
		id, ok := obj["id"].(string)
		if !ok {
			return nil, fmt.Errorf("gate id must be a string")
		}
		n, ok := obj["exit_code"].(json.Number)
		if !ok {
			return nil, fmt.Errorf("gate exit_code must be an integer")
		}
		code64, err := n.Int64()
		if err != nil {
			return nil, fmt.Errorf("gate exit_code must be an integer")
		}
		if int64(int(code64)) != code64 {
			return nil, fmt.Errorf("gate exit_code is out of range")
		}
		out = append(out, CompletionGateResult{
			ID:       id,
			ExitCode: int(code64),
		})
	}
	return out, nil
}
