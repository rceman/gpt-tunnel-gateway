package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

func utf8Bounded(value string, max int, field string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s exceeds %d characters", field, max)
	}
	return nil
}

func strictJSONObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value map[string]any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("trailing JSON content")
	}
	if value == nil {
		return nil, fmt.Errorf("JSON object is required")
	}
	return value, nil
}
