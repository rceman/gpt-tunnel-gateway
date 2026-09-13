package model

import (
	"fmt"
	"strings"
	"time"
)

type TaskExecutionVerification struct {
	ProjectID          string                 `json:"project_id"`
	TaskID             string                 `json:"task_id"`
	OperationID        string                 `json:"operation_id"`
	TaskRevisionSHA256 string                 `json:"task_revision_sha256"`
	BaseHead           string                 `json:"base_head"`
	CandidateHead      string                 `json:"candidate_head"`
	CandidateTree      string                 `json:"candidate_tree"`
	Branch             string                 `json:"branch"`
	GateProfileSHA256  string                 `json:"gate_profile_sha256"`
	Outcome            string                 `json:"outcome"`
	Error              string                 `json:"error,omitempty"`
	TaskRevision       int                    `json:"task_revision"`
	AttemptRevision    int                    `json:"attempt_revision"`
	CodeReviewID       int64                  `json:"code_review_id"`
	TestsReviewID      int64                  `json:"tests_review_id"`
	RebaseReviewID     int64                  `json:"rebase_review_id,omitempty"`
	Gates              []CompletionGateResult `json:"gates"`
	StartedAt          time.Time              `json:"started_at"`
	CompletedAt        time.Time              `json:"completed_at"`
}

const (
	TaskExecutionVerificationSucceeded   = "succeeded"
	TaskExecutionVerificationFailed      = "failed"
	TaskExecutionVerificationInterrupted = "interrupted"
)

func ValidateTaskExecutionVerification(v TaskExecutionVerification) error {
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return fmt.Errorf("invalid Task verification project: %w", err)
	}
	if err := ValidateCanonicalTaskID(v.TaskID); err != nil {
		return fmt.Errorf("invalid Task verification Task: %w", err)
	}
	if err := ValidateObjectIdentifier(v.OperationID); err != nil {
		return fmt.Errorf("invalid Task verification operation: %w", err)
	}
	if ValidateSHA256(v.TaskRevisionSHA256) != nil || ValidateSHA256(v.GateProfileSHA256) != nil {
		return fmt.Errorf("invalid Task verification digest authority")
	}
	if ValidateCommitSHA(v.BaseHead) != nil || ValidateCommitSHA(v.CandidateHead) != nil || ValidateCommitSHA(v.CandidateTree) != nil {
		return fmt.Errorf("invalid Task verification head authority")
	}
	if err := ValidateBranch(v.Branch); err != nil || !strings.HasPrefix(v.Branch, "task/"+v.TaskID+"-") {
		return fmt.Errorf("invalid Task verification branch")
	}
	if v.TaskRevision < 1 || v.AttemptRevision < 1 || v.CodeReviewID < 1 || v.TestsReviewID < 1 || v.RebaseReviewID < 0 {
		return fmt.Errorf("incomplete Task verification revision authority")
	}
	if v.StartedAt.IsZero() || v.CompletedAt.IsZero() || v.StartedAt.Location() != time.UTC || v.CompletedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Task verification timing")
	}
	switch v.Outcome {
	case TaskExecutionVerificationSucceeded:
		if !v.CompletedAt.After(v.StartedAt) {
			return fmt.Errorf("successful Task verification requires ordered timing")
		}
		if v.Error != "" {
			return fmt.Errorf("successful Task verification must not record an error")
		}
		if len(v.Gates) == 0 {
			return fmt.Errorf("successful Task verification requires gate evidence")
		}
		seen := make(map[string]struct{}, len(v.Gates))
		for _, gate := range v.Gates {
			if gate.ID == "" || gate.ExitCode != 0 {
				return fmt.Errorf("successful Task verification requires all-passing gate evidence")
			}
			if _, exists := seen[gate.ID]; exists {
				return fmt.Errorf("duplicate Task verification gate evidence")
			}
			seen[gate.ID] = struct{}{}
			if gate.TreeID != "" && gate.TreeID != v.CandidateTree {
				return fmt.Errorf("Task verification gate evidence does not match candidate tree")
			}
		}
	case TaskExecutionVerificationFailed, TaskExecutionVerificationInterrupted:
		if v.Error == "" {
			return fmt.Errorf("unsuccessful Task verification must record an error")
		}
	default:
		return fmt.Errorf("invalid Task verification outcome")
	}
	return nil
}
