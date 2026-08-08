package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const GateEvidenceSchemaVersion = 1

// GateEvidenceArtifact is the Gateway-owned rich projection of immutable gate
// execution. It is deliberately separate from the compact v1 Report wire.
type GateEvidenceArtifact struct {
	SchemaVersion int                    `json:"schema_version"`
	ProjectID     string                 `json:"project_id"`
	TaskID        string                 `json:"task_id"`
	RunID         string                 `json:"run_id"`
	GateResults   []CompletionGateResult `json:"gate_results"`
}

func CompactReport(report Report) Report {
	original := report.GateResults
	report.GateResults = make([]CompletionGateResult, len(original))
	for i, gate := range original {
		report.GateResults[i] = CompletionGateResult{ID: gate.ID, ExitCode: gate.ExitCode}
	}
	return report
}

func NewGateEvidenceArtifact(report Report) GateEvidenceArtifact {
	return GateEvidenceArtifact{
		SchemaVersion: GateEvidenceSchemaVersion,
		ProjectID:     report.ProjectID,
		TaskID:        report.TaskID,
		RunID:         report.RunID,
		GateResults:   append([]CompletionGateResult{}, report.GateResults...),
	}
}

func validateRichGateResult(gate CompletionGateResult) error {
	switch gate.Kind {
	case "executable", "manual", "unsupported":
	default:
		return fmt.Errorf("invalid gate evidence kind")
	}
	switch gate.Outcome {
	case "passed", "failed", "timeout", "manual", "unsupported":
	default:
		return fmt.Errorf("invalid gate evidence outcome")
	}
	if gate.TimedOut && (gate.Kind != "executable" || gate.Outcome != "timeout") {
		return fmt.Errorf("timed-out gate evidence is inconsistent")
	}
	if gate.Outcome == "timeout" && !gate.TimedOut {
		return fmt.Errorf("timeout gate evidence is missing timed_out")
	}
	if gate.OutputTruncated && gate.Outcome == "passed" {
		return fmt.Errorf("truncated gate evidence cannot pass")
	}
	if err := utf8Bounded(gate.Command, 1024, "gate command"); err != nil {
		return err
	}
	if err := utf8Bounded(gate.Evidence, 4096, "gate evidence"); err != nil {
		return err
	}
	if err := utf8Bounded(gate.Stdout, 1<<20, "gate stdout"); err != nil {
		return err
	}
	if err := utf8Bounded(gate.Stderr, 1<<20, "gate stderr"); err != nil {
		return err
	}
	if gate.Kind == "executable" && (gate.StartedAt == nil || gate.FinishedAt == nil || strings.TrimSpace(gate.Command) == "") {
		return fmt.Errorf("executable gate evidence is incomplete")
	}
	return nil
}

func ValidateGateEvidenceArtifact(value GateEvidenceArtifact, task Task, run Run, limits ...int) error {
	limit := 128
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	if value.SchemaVersion != GateEvidenceSchemaVersion || value.ProjectID != run.ProjectID || value.TaskID != task.ID || value.RunID != run.ID {
		return fmt.Errorf("gate evidence identity mismatch")
	}
	if len(value.GateResults) > limit || len(value.GateResults) != len(task.RequiredGates) {
		return fmt.Errorf("gate evidence count mismatch")
	}
	for i, gate := range value.GateResults {
		if gate.ID != fmt.Sprintf("G%d", i+1) || gate.ExitCode < -1 {
			return fmt.Errorf("gate evidence is not ordered")
		}
		if err := validateRichGateResult(gate); err != nil {
			return fmt.Errorf("gate evidence %s: %w", gate.ID, err)
		}
	}
	return nil
}

func ValidateGateEvidenceParity(report Report, evidence GateEvidenceArtifact) error {
	if report.ProjectID != evidence.ProjectID || report.TaskID != evidence.TaskID || report.RunID != evidence.RunID || len(report.GateResults) != len(evidence.GateResults) {
		return fmt.Errorf("gate evidence does not match compact report identity or count")
	}
	for i, compact := range report.GateResults {
		rich := evidence.GateResults[i]
		if compact.ID != rich.ID || compact.ExitCode != rich.ExitCode {
			return fmt.Errorf("gate evidence does not match compact report at %s", rich.ID)
		}
	}
	return nil
}

func gateEvidenceObjectKeys(obj map[string]any) error {
	allowed := map[string]bool{
		"id": true, "exit_code": true, "kind": true, "outcome": true, "command": true,
		"evidence": true, "stdout": true, "stderr": true, "started_at": true,
		"finished_at": true, "timed_out": true, "output_truncated": true,
	}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown gate evidence field %q", key)
		}
	}
	for _, key := range []string{"id", "exit_code", "kind", "outcome"} {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing gate evidence field %q", key)
		}
	}
	return nil
}

func ParseGateEvidence(data []byte, task Task, run Run, limits ...int) (GateEvidenceArtifact, error) {
	var out GateEvidenceArtifact
	obj, err := strictJSONObject(data)
	if err != nil {
		return out, err
	}
	allowed := map[string]bool{"schema_version": true, "project_id": true, "task_id": true, "run_id": true, "gate_results": true}
	for key := range obj {
		if !allowed[key] {
			return out, fmt.Errorf("unknown gate evidence artifact field %q", key)
		}
	}
	for _, key := range []string{"schema_version", "project_id", "task_id", "run_id", "gate_results"} {
		if _, ok := obj[key]; !ok {
			return out, fmt.Errorf("missing gate evidence artifact field %q", key)
		}
	}
	items, ok := obj["gate_results"].([]any)
	if !ok {
		return out, fmt.Errorf("gate evidence gate_results must be an array")
	}
	for i, item := range items {
		gate, ok := item.(map[string]any)
		if !ok {
			return out, fmt.Errorf("gate evidence gate_results[%d] must be an object", i)
		}
		if err := gateEvidenceObjectKeys(gate); err != nil {
			return out, err
		}
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return out, fmt.Errorf("encode gate evidence artifact")
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return out, fmt.Errorf("trailing gate evidence JSON")
		}
		return out, err
	}
	if err := ValidateGateEvidenceArtifact(out, task, run, limits...); err != nil {
		return out, err
	}
	return out, nil
}
