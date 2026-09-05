package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func decodeManagedJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func decodeManagedSafeInteger(data []byte, name string) (uint64, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, fmt.Errorf("%s: trailing JSON content", name)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be a JSON number", name)
	}
	text := number.String()
	if strings.HasPrefix(text, "-") {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}

	mantissa := text
	exponent := 0
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		parsed, err := parseManagedExponent(mantissa[index+1:], len(data)+32)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		exponent = parsed
		mantissa = mantissa[:index]
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return 0, fmt.Errorf("%s must be a valid JSON number", name)
	}
	fracDigits := ""
	if len(parts) == 2 {
		fracDigits = parts[1]
	}
	digits := strings.TrimLeft(parts[0]+fracDigits, "0")
	if digits == "" {
		return 0, nil
	}
	scale := exponent - len(fracDigits)
	if scale >= 0 {
		if len(digits)+scale > 16 {
			return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
		}
		digits += strings.Repeat("0", scale)
	} else {
		cut := -scale
		if cut >= len(digits) {
			return 0, fmt.Errorf("%s must be integral", name)
		}
		fraction := digits[len(digits)-cut:]
		if strings.Trim(fraction, "0") != "" {
			return 0, fmt.Errorf("%s must be integral", name)
		}
		digits = digits[:len(digits)-cut]
	}
	if len(digits) > 16 {
		return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
	}
	parsed, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || parsed > MaxManagedProjectRegistryRevision {
		return 0, fmt.Errorf("%s exceeds safe integer maximum", name)
	}
	return parsed, nil
}

func parseManagedExponent(text string, limit int) (int, error) {
	if text == "" {
		return 0, fmt.Errorf("exponent is empty")
	}
	sign := 1
	if text[0] == '+' || text[0] == '-' {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if text == "" {
		return 0, fmt.Errorf("exponent is empty")
	}
	value := 0
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("exponent is invalid")
		}
		digit := int(character - '0')
		if value > (limit-digit)/10 {
			return 0, fmt.Errorf("exponent exceeds bounded range")
		}
		value = value*10 + digit
	}
	return sign * value, nil
}

func decodeManagedObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("object key must be a string")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate JSON field %q", key)
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
		return nil, fmt.Errorf("unterminated JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return fields, nil
}

func (r *ManagedProjectRegistry) UnmarshalJSON(data []byte) error {
	fields, err := decodeManagedObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "schema_version", "revision", "projects":
		default:
			return fmt.Errorf("unknown managed project registry field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "revision", "projects"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("managed project registry field %q is required", key)
		}
	}
	var schemaVersion int
	if err := json.Unmarshal(fields["schema_version"], &schemaVersion); err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	revision, err := decodeManagedSafeInteger(fields["revision"], "revision")
	if err != nil {
		return fmt.Errorf("revision: %w", err)
	}
	projectFields, err := decodeManagedObject(fields["projects"])
	if err != nil {
		return fmt.Errorf("projects: %w", err)
	}
	projects := make(map[string]ManagedProjectEntry, len(projectFields))
	for id, raw := range projectFields {
		var entry ManagedProjectEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("projects.%s: %w", id, err)
		}
		projects[id] = entry
	}
	*r = ManagedProjectRegistry{
		SchemaVersion: schemaVersion,
		Revision:      revision,
		Projects:      projects,
	}
	return nil
}

func (e *ManagedProjectEntry) UnmarshalJSON(data []byte) error {
	fields, err := decodeManagedObject(data)
	if err != nil {
		return err
	}
	for key := range fields {
		switch key {
		case "root", "repository_url", "remote", "default_branch", "airelay_session_key":
		default:
			return fmt.Errorf("unknown managed project field %q", key)
		}
	}
	for _, key := range []string{"root", "repository_url", "remote", "default_branch", "airelay_session_key"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("managed project field %q is required", key)
		}
	}
	var entry ManagedProjectEntry
	for key, target := range map[string]*string{"root": &entry.Root, "repository_url": &entry.RepositoryURL, "remote": &entry.Remote, "default_branch": &entry.DefaultBranch, "airelay_session_key": &entry.AirelaySessionKey} {
		if err := json.Unmarshal(fields[key], target); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	*e = entry
	return nil
}

func (e ManagedProjectEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Root              string `json:"root"`
		RepositoryURL     string `json:"repository_url"`
		Remote            string `json:"remote"`
		DefaultBranch     string `json:"default_branch"`
		AirelaySessionKey string `json:"airelay_session_key"`
	}{
		Root:              e.Root,
		RepositoryURL:     e.RepositoryURL,
		Remote:            e.Remote,
		DefaultBranch:     e.DefaultBranch,
		AirelaySessionKey: e.AirelaySessionKey,
	})
}
