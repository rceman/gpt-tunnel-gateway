package model

import (
	"fmt"
	"strings"
	"time"
)

type TrainV2AttemptCompletion struct {
	SchemaVersion      int                    `json:"schema_version"`
	TrainID            string                 `json:"train_id"`
	TaskID             string                 `json:"task_id"`
	ItemPosition       int                    `json:"item_position"`
	AttemptNumber      uint64                 `json:"attempt_number"`
	TaskSHA256         string                 `json:"task_sha256"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	FinishedAt         time.Time              `json:"finished_at,omitempty"`
}

type TrainV2AttemptReport struct {
	SchemaVersion      int                    `json:"schema_version"`
	TrainID            string                 `json:"train_id"`
	TaskID             string                 `json:"task_id"`
	ItemPosition       int                    `json:"item_position"`
	AttemptNumber      uint64                 `json:"attempt_number"`
	ProjectID          string                 `json:"project_id"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	ServerGateResults  []CompletionGateResult `json:"server_gate_results,omitempty"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	Repository         RepositoryProof        `json:"repository"`
	HubCommit          string                 `json:"hub_commit,omitempty"`
	FinishedAt         time.Time              `json:"finished_at"`
}

func ValidateTrainV2AttemptCompletion(v TrainV2AttemptCompletion, task TaskAuthoring, trainID string, position int, attempt uint64) error {
	if v.SchemaVersion != 1 || v.TrainID != trainID || v.TaskID != task.ID || v.ItemPosition != position || v.AttemptNumber != attempt || v.AttemptNumber == 0 || v.ItemPosition < 0 || v.TaskSHA256 != task.RevisionSHA256 {
		return fmt.Errorf("invalid Train-v2 Attempt completion identity")
	}
	switch v.Status {
	case "succeeded", "failed", "needs_gpt_revision":
	default:
		return fmt.Errorf("invalid Attempt completion status")
	}
	if strings.TrimSpace(v.Summary) == "" || len([]byte(v.Summary)) > 4096 {
		return fmt.Errorf("Attempt completion summary is invalid")
	}
	if len(v.GateResults) > 128 || len(v.AcceptanceCoverage) > len(task.AcceptanceCriteria) {
		return fmt.Errorf("Attempt completion exceeds task bounds")
	}
	for i, gate := range v.GateResults {
		if gate.ID != fmt.Sprintf("G%d", i+1) {
			return fmt.Errorf("Attempt gate results are not positional")
		}
	}
	for i, criterion := range v.AcceptanceCoverage {
		if criterion != fmt.Sprintf("AC%d", i+1) {
			return fmt.Errorf("Attempt acceptance coverage is not positional")
		}
	}
	return nil
}

func ValidateTrainV2AttemptReport(v TrainV2AttemptReport) error {
	if v.SchemaVersion != 1 || ValidateProjectIdentifier(v.ProjectID) != nil || v.TrainID == "" || ValidateCanonicalTaskID(v.TaskID) != nil || v.ItemPosition < 0 || v.AttemptNumber == 0 || v.FinishedAt.IsZero() {
		return fmt.Errorf("invalid Train-v2 Attempt report")
	}
	if strings.TrimSpace(v.Summary) == "" {
		return fmt.Errorf("Attempt report summary is required")
	}
	if v.HubCommit != "" && ValidateCommitSHA(v.HubCommit) != nil {
		return fmt.Errorf("invalid Attempt report hub commit")
	}
	return nil
}
