package model

import (
	"fmt"
	"strings"
	"time"
)

const MaxTrainV2ReviewResolutionFindings = 128

// TrainV2ReviewCorrection identifies the exact accepted correction evidence
// that resolves one rejected review. It is deliberately fully qualified so a
// task or review ID cannot be reused across a Train or project.
type TrainV2ReviewCorrection struct {
	ProjectID     string   `json:"project_id"`
	TrainID       string   `json:"train_id"`
	TaskID        string   `json:"task_id"`
	ItemPosition  int      `json:"item_position"`
	AttemptNumber uint64   `json:"attempt_number"`
	ReviewID      string   `json:"review_id"`
	ReviewedHead  string   `json:"reviewed_head"`
	ProofHead     string   `json:"proof_head"`
	FindingIDs    []string `json:"finding_ids"`
}

// TrainV2ReviewResolution is immutable server-owned evidence linking a
// rejected review to accepted correction evidence. The original review is
// never rewritten; the resolution is appended to the Train revision.
type TrainV2ReviewResolution struct {
	SchemaVersion         int                       `json:"schema_version"`
	ID                    string                    `json:"id"`
	ProjectID             string                    `json:"project_id"`
	TrainID               string                    `json:"train_id"`
	RejectedTaskID        string                    `json:"rejected_task_id"`
	RejectedItemPosition  int                       `json:"rejected_item_position"`
	RejectedAttemptNumber uint64                    `json:"rejected_attempt_number"`
	RejectedReviewID      string                    `json:"rejected_review_id"`
	RejectedReviewedHead  string                    `json:"rejected_reviewed_head"`
	FindingIDs            []string                  `json:"finding_ids"`
	Corrections           []TrainV2ReviewCorrection `json:"corrections"`
	ResolvingHead         string                    `json:"resolving_head"`
	ReviewerEvidence      string                    `json:"reviewer_evidence"`
	RecordedAt            time.Time                 `json:"recorded_at"`
}

func ValidateTrainV2ReviewResolution(v TrainV2ReviewResolution) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateObjectIdentifier(v.ID) != nil || ValidateProjectIdentifier(v.ProjectID) != nil || ParseTrainV2IDMust(v.TrainID) != nil || ValidateCanonicalTaskID(v.RejectedTaskID) != nil || v.RejectedItemPosition < 0 || v.RejectedAttemptNumber == 0 || ValidateObjectIdentifier(v.RejectedReviewID) != nil || !shaRE.MatchString(v.RejectedReviewedHead) || !shaRE.MatchString(v.ResolvingHead) || strings.TrimSpace(v.ReviewerEvidence) == "" || v.RecordedAt.IsZero() {
		return fmt.Errorf("invalid Train-v2 review resolution identity")
	}
	if len(v.FindingIDs) == 0 || len(v.FindingIDs) > MaxTrainV2ReviewResolutionFindings || len(v.Corrections) == 0 || len(v.Corrections) > MaxTrainV2ReviewResolutionFindings {
		return fmt.Errorf("invalid Train-v2 review resolution coverage")
	}
	if err := validateFindingIDs(v.FindingIDs); err != nil {
		return err
	}
	for _, correction := range v.Corrections {
		if ValidateProjectIdentifier(correction.ProjectID) != nil || ParseTrainV2IDMust(correction.TrainID) != nil || ValidateCanonicalTaskID(correction.TaskID) != nil || correction.ItemPosition < 0 || correction.AttemptNumber == 0 || ValidateObjectIdentifier(correction.ReviewID) != nil || !shaRE.MatchString(correction.ReviewedHead) || !shaRE.MatchString(correction.ProofHead) {
			return fmt.Errorf("invalid Train-v2 correction identity")
		}
		if err := validateFindingIDs(correction.FindingIDs); err != nil {
			return err
		}
	}
	return nil
}

func validateFindingIDs(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if ValidateObjectIdentifier(id) != nil {
			return fmt.Errorf("invalid Train-v2 review finding ID")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate Train-v2 review finding ID")
		}
		seen[id] = struct{}{}
	}
	return nil
}

// ParseTrainV2IDMust keeps validation expressions readable without changing
// the existing parser contract.
func ParseTrainV2IDMust(id string) error {
	_, _, err := ParseTrainV2ID(id)
	return err
}
