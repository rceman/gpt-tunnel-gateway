package model

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// CanonicalDeliveryHandoffDigest returns the digest of the immutable
// publication payload. Mutable lifecycle state is deliberately excluded.
func CanonicalDeliveryHandoffDigest(v DeliveryHandoff) (string, error) {
	payload := struct {
		SchemaVersion        int             `json:"schema_version"`
		ID                   string          `json:"id"`
		ProjectID            string          `json:"project_id"`
		TaskID               string          `json:"task_id"`
		RunID                string          `json:"run_id"`
		TaskSHA256           string          `json:"task_sha256"`
		OwnerSummary         OwnerSummary    `json:"owner_summary"`
		TechnicalEvidence    json.RawMessage `json:"technical_evidence"`
		SupersedesHandoffID  string          `json:"supersedes_handoff_id,omitempty"`
		PlanRevision         int             `json:"plan_revision"`
		HubRevision          string          `json:"hub_revision"`
		TaskRefs             []TaskRef       `json:"task_refs"`
		TrainRefs            []string        `json:"train_refs"`
		PlanSectionRefs      []string        `json:"plan_section_refs"`
		OperatorEventRefs    []string        `json:"operator_event_refs"`
		ExpectedRepoBase     string          `json:"expected_repo_base"`
		ExpectedRepoHead     string          `json:"expected_repo_head"`
		FirstAction          string          `json:"first_action"`
		StopBoundary         string          `json:"stop_boundary"`
		ProhibitedOperations []string        `json:"prohibited_operations"`
		InstructionBody      string          `json:"instruction_body"`
		RoleRefs             []string        `json:"role_refs"`
		DelegationRefs       []string        `json:"delegation_refs"`
		AuthorRole           string          `json:"author_role"`
		ConsumerRole         string          `json:"consumer_role"`
		CreatedBy            string          `json:"created_by"`
		CreatedAt            time.Time       `json:"created_at"`
	}{
		SchemaVersion:        v.SchemaVersion,
		ID:                   v.ID,
		ProjectID:            v.ProjectID,
		TaskID:               v.TaskID,
		RunID:                v.RunID,
		TaskSHA256:           v.TaskSHA256,
		OwnerSummary:         v.OwnerSummary,
		TechnicalEvidence:    v.TechnicalEvidence,
		SupersedesHandoffID:  v.SupersedesHandoffID,
		PlanRevision:         v.PlanRevision,
		HubRevision:          v.HubRevision,
		TaskRefs:             v.TaskRefs,
		TrainRefs:            v.TrainRefs,
		PlanSectionRefs:      v.PlanSectionRefs,
		OperatorEventRefs:    v.OperatorEventRefs,
		ExpectedRepoBase:     v.ExpectedRepoBase,
		ExpectedRepoHead:     v.ExpectedRepoHead,
		FirstAction:          v.FirstAction,
		StopBoundary:         v.StopBoundary,
		ProhibitedOperations: v.ProhibitedOperations,
		InstructionBody:      v.InstructionBody,
		RoleRefs:             v.RoleRefs,
		DelegationRefs:       v.DelegationRefs,
		AuthorRole:           v.AuthorRole,
		ConsumerRole:         v.ConsumerRole,
		CreatedBy:            v.CreatedBy,
		CreatedAt:            v.CreatedAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:]), nil
}

func CanonicalPlannerReportDigest(v PlannerReport) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:]), nil
}
