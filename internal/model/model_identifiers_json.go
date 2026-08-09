package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func (p *ProjectIdentifiers) UnmarshalJSON(data []byte) error {
	fields, err := decodeProjectIdentifiersObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number":
		default:
			return fmt.Errorf("unknown project identifiers field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "project_id", "project_code", "next_task_number", "next_adr_number"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("project identifiers field %q is required", key)
		}
	}

	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	if schemaVersion > uint64(^uint(0)>>1) {
		return fmt.Errorf("schema_version overflows int")
	}
	var projectID, projectCode string
	if err := json.Unmarshal(fields["project_id"], &projectID); err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	if err := json.Unmarshal(fields["project_code"], &projectCode); err != nil {
		return fmt.Errorf("project_code: %w", err)
	}
	nextTaskNumber, err := parseJSONInteger(fields["next_task_number"])
	if err != nil {
		return fmt.Errorf("next_task_number: %w", err)
	}
	nextADRNumber, err := parseJSONInteger(fields["next_adr_number"])
	if err != nil {
		return fmt.Errorf("next_adr_number: %w", err)
	}
	*p = ProjectIdentifiers{
		SchemaVersion:  int(schemaVersion),
		ProjectID:      projectID,
		ProjectCode:    projectCode,
		NextTaskNumber: nextTaskNumber,
		NextADRNumber:  nextADRNumber,
	}
	return nil
}

func decodeProjectIdentifiersObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("project identifiers must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("project identifiers object key must be a string")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate project identifiers field %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("project identifiers object is not closed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("project identifiers JSON has trailing content")
	}
	return fields, nil
}

func parseJSONInteger(data []byte) (uint64, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("must be a JSON number")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, fmt.Errorf("must contain one JSON value")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("must be a JSON number")
	}
	return parseJSONNumberInteger(number.String())
}

func parseJSONNumberInteger(value string) (uint64, error) {
	if strings.HasPrefix(value, "-") {
		return 0, fmt.Errorf("must be non-negative")
	}
	if strings.HasPrefix(value, "+") {
		return 0, fmt.Errorf("must be a JSON number")
	}
	mantissa := value
	var exponent int64
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		exponentText := mantissa[index+1:]
		parsed, err := strconv.ParseInt(exponentText, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("number exponent overflows")
		}
		exponent = parsed
		mantissa = mantissa[:index]
	}
	parts := strings.SplitN(mantissa, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	digits := whole + frac
	if strings.Trim(digits, "0") == "" {
		return 0, nil
	}
	scale := int64(len(frac)) - exponent
	if scale > int64(len(digits)) {
		return 0, fmt.Errorf("must be an integer")
	}
	integerDigits := digits
	if scale > 0 {
		cut := len(digits) - int(scale)
		if !strings.HasSuffix(digits, strings.Repeat("0", int(scale))) {
			return 0, fmt.Errorf("must be an integer")
		}
		integerDigits = digits[:cut]
	} else if scale < 0 {
		zeros := -scale
		if zeros > int64(len(strconv.FormatUint(MaxSafeInteger, 10))) {
			return 0, fmt.Errorf("overflows maximum safe integer")
		}
		integerDigits += strings.Repeat("0", int(zeros))
	}
	integerDigits = strings.TrimLeft(integerDigits, "0")
	if integerDigits == "" {
		return 0, nil
	}
	maxText := strconv.FormatUint(MaxSafeInteger, 10)
	if len(integerDigits) > len(maxText) || (len(integerDigits) == len(maxText) && integerDigits > maxText) {
		return 0, fmt.Errorf("overflows maximum safe integer")
	}
	number, err := strconv.ParseUint(integerDigits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid JSON integer")
	}
	return number, nil
}
