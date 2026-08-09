package model

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

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
	c := Completion{
		SchemaVersion:      1,
		RunID:              runID,
		TaskSHA256:         taskHash,
		Status:             status,
		Summary:            summary,
		GateResults:        gates,
		AcceptanceCoverage: acceptance,
		Deviations:         deviations,
		RemainingRisks:     risks,
	}
	if _, ok := obj["agent_feedback"]; ok {
		var feedback AgentFeedback
		if err := decodeCompletionField(obj, "agent_feedback", &feedback); err != nil {
			return Completion{}, err
		}
		c.AgentFeedback = &feedback
	}
	if raw, ok := obj["task_revision_sha256"]; ok {
		value, valid := raw.(string)
		if !valid {
			return Completion{}, fmt.Errorf("completion field %q must be a string", "task_revision_sha256")
		}
		c.TaskRevisionSHA256 = value
	}
	if revisionID, runNumber, revisionErr := ParseTaskRevisionRunID(runID); revisionErr == nil {
		taskID, revision, parseErr := ParseTaskRevisionID(revisionID)
		if parseErr != nil || taskID != task.ID {
			return Completion{}, fmt.Errorf("completion revision identity does not match task")
		}
		c.TaskRevision, c.TaskRunNumber = revision, runNumber
	}
	if value, present, err := optionalCompletionUint(obj, "task_revision"); err != nil {
		return Completion{}, err
	} else if present {
		c.TaskRevision = int(value)
	}
	if value, present, err := optionalCompletionUint(obj, "task_run_number"); err != nil {
		return Completion{}, err
	} else if present {
		c.TaskRunNumber = value
	}
	if err := ValidateCompletion(c, task); err != nil {
		return Completion{}, err
	}
	return c, nil
}
