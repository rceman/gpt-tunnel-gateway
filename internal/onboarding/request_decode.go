package onboarding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

func DecodeRequest(data []byte) (Request, error) {
	var request Request
	if err := decodeStrictObject(data, &request); err != nil {
		return Request{}, err
	}
	if err := ValidateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func decodeStrictObject(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("request is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, true); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("trailing JSON content: %w", err)
		}
		return fmt.Errorf("trailing JSON content after %v", token)
	}
	if err := requireRequestFields(data); err != nil {
		return err
	}

	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	return nil
}

func requireRequestFields(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("decode request shape: %w", err)
	}
	if top == nil {
		return fmt.Errorf("request must be a JSON object")
	}
	if err := requireFields(top, "request", "schema_version", "project_id", "root", "remote", "repository_url", "default_branch", "airelay", "project_code", "gateway_state_dir", "initial_plan", "expected_hub_revision"); err != nil {
		return err
	}

	airelay, err := objectFields(top["airelay"], "request.airelay")
	if err != nil {
		return err
	}
	if err := requireFields(airelay, "request.airelay", "session_required"); err != nil {
		return err
	}
	if raw, ok := top["workflow"]; ok {
		workflow, err := objectFields(raw, "request.workflow")
		if err != nil {
			return err
		}
		if err := requireFields(workflow, "request.workflow", "repository", "commit"); err != nil {
			return err
		}
	}

	plan, err := objectFields(top["initial_plan"], "request.initial_plan")
	if err != nil {
		return err
	}
	if err := requireFields(plan, "request.initial_plan", "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at"); err != nil {
		return err
	}
	var sections []json.RawMessage
	if err := json.Unmarshal(plan["sections"], &sections); err != nil {
		return fmt.Errorf("request.initial_plan.sections must be an array: %w", err)
	}
	for index, raw := range sections {
		section, err := objectFields(raw, fmt.Sprintf("request.initial_plan.sections[%d]", index))
		if err != nil {
			return err
		}
		if err := requireFields(section, fmt.Sprintf("request.initial_plan.sections[%d]", index), "id", "title", "short_description", "revision"); err != nil {
			return err
		}
	}
	return nil
}

func requireFields(object map[string]json.RawMessage, name string, fields ...string) error {
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("%s is missing required field %q", name, field)
		}
	}
	return nil
}

func objectFields(raw json.RawMessage, name string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err != nil {
			return nil, fmt.Errorf("%s must be an object: %w", name, err)
		}
		return nil, fmt.Errorf("%s must be an object", name)
	}
	return object, nil
}

func scanJSONValue(decoder *json.Decoder, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	switch value := token.(type) {
	case nil:
		return fmt.Errorf("null JSON values are not allowed")
	case json.Delim:
		switch value {
		case '{':
			if err := scanJSONObject(decoder); err != nil {
				return err
			}
		case '[':
			if root {
				return fmt.Errorf("request must be a JSON object")
			}
			if err := scanJSONArray(decoder); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	default:
		if root {
			return fmt.Errorf("request must be a JSON object")
		}
	}
	return nil
}

func scanJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder, false); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode object end: %w", err)
	}
	if end != json.Delim('}') {
		return fmt.Errorf("invalid JSON object termination")
	}
	return nil
}

func scanJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := scanJSONValue(decoder, false); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode array end: %w", err)
	}
	if end != json.Delim(']') {
		return fmt.Errorf("invalid JSON array termination")
	}
	return nil
}

func parsePositiveInteger(data []byte) (uint64, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0, fmt.Errorf("positive integer is empty")
	}
	if text[0] == '-' {
		return 0, fmt.Errorf("positive integer cannot be negative")
	}
	index := 0
	integerStart := index
	if text[index] == '0' {
		index++
		if index < len(text) && text[index] >= '0' && text[index] <= '9' {
			return 0, fmt.Errorf("invalid JSON number")
		}
	} else if text[index] >= '1' && text[index] <= '9' {
		index++
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
	} else {
		return 0, fmt.Errorf("positive integer must be a JSON number")
	}
	integerEnd := index
	fracLen := 0
	if index < len(text) && text[index] == '.' {
		index++
		fractionStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		fracLen = index - fractionStart
		if fracLen == 0 {
			return 0, fmt.Errorf("invalid JSON number fraction")
		}
	}
	exponentSign := 1
	exponent := uint64(0)
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		index++
		if index < len(text) && (text[index] == '+' || text[index] == '-') {
			if text[index] == '-' {
				exponentSign = -1
			}
			index++
		}
		exponentStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			digit := uint64(text[index] - '0')
			if exponent > 1000000 || exponent > (1000000-digit)/10 {
				return 0, fmt.Errorf("JSON number exponent is outside the bounded range")
			}
			exponent = exponent*10 + digit
			index++
		}
		if index == exponentStart {
			return 0, fmt.Errorf("invalid JSON number exponent")
		}
	}
	if index != len(text) {
		return 0, fmt.Errorf("invalid JSON number")
	}

	digits := text[integerStart:integerEnd]
	if indexOfDot := strings.IndexByte(text[integerEnd:], '.'); indexOfDot >= 0 {
		fractionStart := integerEnd + indexOfDot + 1
		fractionEnd := fractionStart + fracLen
		digits += text[fractionStart:fractionEnd]
	}
	if strings.Trim(digits, "0") == "" {
		return 0, fmt.Errorf("positive integer must be greater than zero")
	}

	if exponentSign < 0 || exponent < uint64(fracLen) {
		decimalPlaces := uint64(fracLen)
		if exponentSign < 0 {
			decimalPlaces += exponent
		} else {
			decimalPlaces -= exponent
		}
		if decimalPlaces >= uint64(len(digits)) {
			return 0, fmt.Errorf("JSON number is fractional")
		}
		split := len(digits) - int(decimalPlaces)
		if strings.Trim(digits[split:], "0") != "" {
			return 0, fmt.Errorf("JSON number is fractional")
		}
		digits = digits[:split]
	} else {
		zeroes := exponent - uint64(fracLen)
		trimmed := strings.TrimLeft(digits, "0")
		if uint64(len(trimmed))+zeroes > uint64(len(strconv.FormatUint(MaxSafeInteger, 10))) {
			return 0, fmt.Errorf("positive integer exceeds %d", MaxSafeInteger)
		}
		digits = trimmed + strings.Repeat("0", int(zeroes))
	}

	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, fmt.Errorf("positive integer must be greater than zero")
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || value > MaxSafeInteger {
		return 0, fmt.Errorf("positive integer exceeds %d", MaxSafeInteger)
	}
	return value, nil
}
