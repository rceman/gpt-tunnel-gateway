package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

var completionHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
var completionGateRE = regexp.MustCompile(`^G[1-9][0-9]*$`)
var completionACRE = regexp.MustCompile(`^AC[1-9][0-9]*$`)

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
	allowed := map[string]bool{"schema_version": true, "run_id": true, "task_sha256": true, "status": true, "summary": true, "gate_results": true, "acceptance_coverage": true, "deviations": true, "remaining_risks": true}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown completion field %q", key)
		}
	}
	for key := range allowed {
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
		out = append(out, CompletionGateResult{ID: id, ExitCode: int(code64)})
	}
	return out, nil
}

func ParseCompletion(data []byte, task Task) (Completion, error) {
	if !utf8.Valid(data) {
		return Completion{}, fmt.Errorf("completion is not valid UTF-8")
	}
	obj, err := strictJSONObject(data)
	if err != nil {
		return Completion{}, err
	}
	if err := requiredCompletionFields(obj); err != nil {
		return Completion{}, err
	}
	version, ok := obj["schema_version"].(json.Number)
	if !ok || version.String() != "1" {
		return Completion{}, fmt.Errorf("schema_version must be integer 1")
	}
	runID, err := completionString(obj, "run_id")
	if err != nil {
		return Completion{}, err
	}
	taskHash, err := completionString(obj, "task_sha256")
	if err != nil {
		return Completion{}, err
	}
	status, err := completionString(obj, "status")
	if err != nil {
		return Completion{}, err
	}
	summary, err := completionString(obj, "summary")
	if err != nil {
		return Completion{}, err
	}
	acceptance, err := completionStringArray(obj["acceptance_coverage"], "acceptance_coverage")
	if err != nil {
		return Completion{}, err
	}
	deviations, err := completionStringArray(obj["deviations"], "deviations")
	if err != nil {
		return Completion{}, err
	}
	risks, err := completionStringArray(obj["remaining_risks"], "remaining_risks")
	if err != nil {
		return Completion{}, err
	}
	gates, err := completionGates(obj["gate_results"])
	if err != nil {
		return Completion{}, err
	}
	c := Completion{SchemaVersion: 1, RunID: runID, TaskSHA256: taskHash, Status: status, Summary: summary, GateResults: gates, AcceptanceCoverage: acceptance, Deviations: deviations, RemainingRisks: risks}
	if err := ValidateCompletion(c, task); err != nil {
		return Completion{}, err
	}
	return c, nil
}

func ValidateCompletion(c Completion, task Task) error {
	if c.SchemaVersion != 1 || ValidateCanonicalRunID(c.RunID) != nil {
		return fmt.Errorf("invalid completion identity")
	}
	if !completionHashRE.MatchString(c.TaskSHA256) || strings.ToLower(c.TaskSHA256) != c.TaskSHA256 || c.TaskSHA256 != task.SHA256 {
		return fmt.Errorf("completion task hash mismatch")
	}
	switch c.Status {
	case "succeeded", "failed", "needs_gpt_revision":
	default:
		return fmt.Errorf("invalid completion status")
	}
	if err := utf8Bounded(c.Summary, 4096, "summary"); err != nil {
		return err
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("summary must be non-empty")
	}
	if len(c.GateResults) > 128 || len(c.AcceptanceCoverage) > 128 || len(c.Deviations) > 64 || len(c.RemainingRisks) > 64 {
		return fmt.Errorf("completion bounds exceeded")
	}
	for i, gate := range c.GateResults {
		want := fmt.Sprintf("G%d", i+1)
		if gate.ID != want || !completionGateRE.MatchString(gate.ID) {
			return fmt.Errorf("gate results must be the ordered positional sequence")
		}
	}
	seen := map[string]bool{}
	for _, id := range c.AcceptanceCoverage {
		if !completionACRE.MatchString(id) || seen[id] {
			return fmt.Errorf("acceptance coverage must be ordered and unique")
		}
		seen[id] = true
	}
	if c.Status == "succeeded" {
		if len(c.GateResults) != len(task.RequiredGates) || len(c.AcceptanceCoverage) != len(task.AcceptanceCriteria) {
			return fmt.Errorf("successful completion must report every gate and criterion")
		}
		for _, gate := range c.GateResults {
			if gate.ExitCode != 0 {
				return fmt.Errorf("successful completion contains failed gate %s", gate.ID)
			}
		}
		for i := range c.AcceptanceCoverage {
			if c.AcceptanceCoverage[i] != fmt.Sprintf("AC%d", i+1) {
				return fmt.Errorf("successful acceptance coverage must be positional")
			}
		}
	} else {
		if len(c.GateResults) > len(task.RequiredGates) || len(c.AcceptanceCoverage) > len(task.AcceptanceCriteria) {
			return fmt.Errorf("completion exceeds task bounds")
		}
		for i, gate := range c.GateResults {
			if gate.ID != fmt.Sprintf("G%d", i+1) {
				return fmt.Errorf("gate prefix is not positional")
			}
		}
		last := 0
		for _, id := range c.AcceptanceCoverage {
			var n int
			_, _ = fmt.Sscanf(id, "AC%d", &n)
			if n <= last || n < 1 || n > len(task.AcceptanceCriteria) {
				return fmt.Errorf("acceptance coverage is not an ordered bounded subset")
			}
			last = n
		}
	}
	for _, v := range append(append([]string{}, c.Deviations...), c.RemainingRisks...) {
		if err := utf8Bounded(v, 2048, "completion entry"); err != nil {
			return err
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("completion entry must be non-empty")
		}
	}
	return nil
}

func CanonicalCompletion(c Completion) Completion {
	if c.GateResults == nil {
		c.GateResults = []CompletionGateResult{}
	}
	if c.AcceptanceCoverage == nil {
		c.AcceptanceCoverage = []string{}
	}
	if c.Deviations == nil {
		c.Deviations = []string{}
	}
	if c.RemainingRisks == nil {
		c.RemainingRisks = []string{}
	}
	return c
}

func CompletionJSON(c Completion) ([]byte, error) { return json.Marshal(CanonicalCompletion(c)) }
