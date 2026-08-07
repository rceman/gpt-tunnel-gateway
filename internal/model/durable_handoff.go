package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DurableHandoffSchemaVersion = 1
	MaxOwnerSummaryFieldBytes   = 512
	MaxTechnicalEvidenceBytes   = 64 * 1024
)

var durableSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// OwnerSummary is the concise owner-facing part of a durable handoff or
// planner report. Technical identifiers and exact proof belong in
// TechnicalEvidence instead.
type OwnerSummary struct {
	Status              string `json:"status"`
	Goal                string `json:"goal"`
	CurrentlyDoing      string `json:"currently_doing"`
	WhyItMatters        string `json:"why_it_matters"`
	CompletedSoFar      string `json:"completed_so_far"`
	NextStep            string `json:"next_step"`
	OwnerActionRequired string `json:"owner_action_required"`
}

// DeliveryHandoff is the mutable lifecycle record routed from Planner to
// Delivery. The evidence payload is intentionally separate from the concise
// owner summary and is not part of bounded default projections.
type DeliveryHandoff struct {
	SchemaVersion         int             `json:"schema_version"`
	ID                    string          `json:"id"`
	ProjectID             string          `json:"project_id"`
	TaskID                string          `json:"task_id"`
	RunID                 string          `json:"run_id"`
	TaskSHA256            string          `json:"task_sha256"`
	Status                string          `json:"status"`
	OwnerSummary          OwnerSummary    `json:"owner_summary"`
	TechnicalEvidence     json.RawMessage `json:"technical_evidence"`
	CurrentReportID       string          `json:"current_report_id,omitempty"`
	SupersedesHandoffID   string          `json:"supersedes_handoff_id,omitempty"`
	SupersededByHandoffID string          `json:"superseded_by_handoff_id,omitempty"`
	CreatedBy             string          `json:"created_by"`
	AcknowledgedBy        string          `json:"acknowledged_by,omitempty"`
	CancelledBy           string          `json:"cancelled_by,omitempty"`
	CancelReason          string          `json:"cancel_reason,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	AcknowledgedAt        *time.Time      `json:"acknowledged_at,omitempty"`
	CancelledAt           *time.Time      `json:"cancelled_at,omitempty"`
}

// PlannerReport is immutable. Corrections are new records linked with
// SupersedesReportID; there is deliberately no update operation.
type PlannerReport struct {
	SchemaVersion      int             `json:"schema_version"`
	ID                 string          `json:"id"`
	ProjectID          string          `json:"project_id"`
	HandoffID          string          `json:"handoff_id"`
	TaskID             string          `json:"task_id"`
	RunID              string          `json:"run_id"`
	TaskSHA256         string          `json:"task_sha256"`
	ReportType         string          `json:"report_type"`
	OwnerSummary       OwnerSummary    `json:"owner_summary"`
	TechnicalEvidence  json.RawMessage `json:"technical_evidence"`
	SupersedesReportID string          `json:"supersedes_report_id,omitempty"`
	PublishedBy        string          `json:"published_by"`
	PublishedAt        time.Time       `json:"published_at"`
}

type DeliveryHandoffStatus struct {
	SchemaVersion         int          `json:"schema_version"`
	ID                    string       `json:"id"`
	ProjectID             string       `json:"project_id"`
	TaskID                string       `json:"task_id"`
	RunID                 string       `json:"run_id"`
	Status                string       `json:"status"`
	OwnerSummary          OwnerSummary `json:"owner_summary"`
	CurrentReportID       string       `json:"current_report_id,omitempty"`
	SupersedesHandoffID   string       `json:"supersedes_handoff_id,omitempty"`
	SupersededByHandoffID string       `json:"superseded_by_handoff_id,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

type PlannerReportStatus struct {
	SchemaVersion      int          `json:"schema_version"`
	ID                 string       `json:"id"`
	ProjectID          string       `json:"project_id"`
	HandoffID          string       `json:"handoff_id"`
	TaskID             string       `json:"task_id"`
	RunID              string       `json:"run_id"`
	ReportType         string       `json:"report_type"`
	OwnerSummary       OwnerSummary `json:"owner_summary"`
	SupersedesReportID string       `json:"supersedes_report_id,omitempty"`
	PublishedBy        string       `json:"published_by"`
	PublishedAt        time.Time    `json:"published_at"`
}

const (
	DeliveryHandoffPending          = "pending"
	DeliveryHandoffAcknowledged     = "acknowledged"
	DeliveryHandoffCompleted        = "completed"
	DeliveryHandoffBlocked          = "blocked"
	DeliveryHandoffAwaitingDecision = "awaiting_decision"
	DeliveryHandoffCancelled        = "cancelled"
	DeliveryHandoffSuperseded       = "superseded"
)

const (
	PlannerReportCompleted        = "completed"
	PlannerReportBlocked          = "blocked"
	PlannerReportDecisionRequired = "decision_required"
)

func ValidateOwnerSummary(v OwnerSummary) error {
	fields := map[string]string{
		"status":                v.Status,
		"goal":                  v.Goal,
		"currently_doing":       v.CurrentlyDoing,
		"why_it_matters":        v.WhyItMatters,
		"completed_so_far":      v.CompletedSoFar,
		"next_step":             v.NextStep,
		"owner_action_required": v.OwnerActionRequired,
	}
	for name, value := range fields {
		if !utf8.ValidString(value) || len([]byte(value)) == 0 || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("owner_summary.%s is empty, invalid, or oversized", name)
		}
	}
	return nil
}

func ValidateTechnicalEvidence(value json.RawMessage) error {
	if len(value) == 0 || len(value) > MaxTechnicalEvidenceBytes || !json.Valid(value) {
		return fmt.Errorf("technical_evidence is invalid or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return fmt.Errorf("technical_evidence must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("technical_evidence has trailing content")
	}
	return nil
}

func validateDurableHandoffIdentity(schemaVersion int, id, projectID, taskID, runID, taskSHA256 string) error {
	if schemaVersion != DurableHandoffSchemaVersion {
		return fmt.Errorf("invalid durable handoff schema_version")
	}
	for name, value := range map[string]string{"id": id, "project_id": projectID, "task_id": taskID, "run_id": runID} {
		if err := ValidateObjectIdentifier(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if !durableSHA256RE.MatchString(taskSHA256) {
		return fmt.Errorf("invalid task_sha256")
	}
	return nil
}

func ValidateDeliveryHandoff(v DeliveryHandoff) error {
	if err := validateDurableHandoffIdentity(v.SchemaVersion, v.ID, v.ProjectID, v.TaskID, v.RunID, v.TaskSHA256); err != nil {
		return err
	}
	switch v.Status {
	case DeliveryHandoffPending, DeliveryHandoffAcknowledged, DeliveryHandoffCompleted, DeliveryHandoffBlocked, DeliveryHandoffAwaitingDecision, DeliveryHandoffCancelled, DeliveryHandoffSuperseded:
	default:
		return fmt.Errorf("invalid delivery handoff status")
	}
	if err := ValidateOwnerSummary(v.OwnerSummary); err != nil {
		return err
	}
	if err := ValidateTechnicalEvidence(v.TechnicalEvidence); err != nil {
		return err
	}
	if v.CreatedBy == "" || strings.ContainsAny(v.CreatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("invalid delivery handoff metadata")
	}
	if v.SupersedesHandoffID != "" {
		if err := ValidateObjectIdentifier(v.SupersedesHandoffID); err != nil {
			return fmt.Errorf("supersedes_handoff_id: %w", err)
		}
	}
	if v.CurrentReportID != "" {
		if err := ValidateObjectIdentifier(v.CurrentReportID); err != nil {
			return fmt.Errorf("current_report_id: %w", err)
		}
	}
	if v.Status == DeliveryHandoffAcknowledged && (v.AcknowledgedBy == "" || v.AcknowledgedAt == nil) {
		return fmt.Errorf("acknowledged handoff requires acknowledgement metadata")
	}
	if v.Status == DeliveryHandoffCancelled && (v.CancelReason == "" || v.CancelledAt == nil) {
		return fmt.Errorf("cancelled handoff requires cancellation metadata")
	}
	return nil
}

func ValidatePlannerReport(v PlannerReport) error {
	if v.SchemaVersion != DurableHandoffSchemaVersion {
		return fmt.Errorf("invalid planner report schema_version")
	}
	for name, value := range map[string]string{"id": v.ID, "project_id": v.ProjectID, "handoff_id": v.HandoffID, "task_id": v.TaskID, "run_id": v.RunID} {
		if err := ValidateObjectIdentifier(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if !durableSHA256RE.MatchString(v.TaskSHA256) {
		return fmt.Errorf("invalid task_sha256")
	}
	switch v.ReportType {
	case PlannerReportCompleted, PlannerReportBlocked, PlannerReportDecisionRequired:
	default:
		return fmt.Errorf("invalid planner report_type")
	}
	if err := ValidateOwnerSummary(v.OwnerSummary); err != nil {
		return err
	}
	if err := ValidateTechnicalEvidence(v.TechnicalEvidence); err != nil {
		return err
	}
	if v.SupersedesReportID != "" {
		if err := ValidateObjectIdentifier(v.SupersedesReportID); err != nil {
			return fmt.Errorf("supersedes_report_id: %w", err)
		}
	}
	if v.PublishedBy == "" || strings.ContainsAny(v.PublishedBy, "\x00\r\n") || v.PublishedAt.IsZero() {
		return fmt.Errorf("invalid planner report metadata")
	}
	return nil
}

func PlannerReportRequiresTerminalEvidence(v PlannerReport) error {
	if v.ReportType != PlannerReportCompleted {
		return nil
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(v.TechnicalEvidence, &evidence); err != nil {
		return fmt.Errorf("terminal technical evidence is invalid")
	}
	for _, key := range []string{"terminal", "reviewed"} {
		var value bool
		if err := json.Unmarshal(evidence[key], &value); err != nil || !value {
			return fmt.Errorf("completed planner report requires %s terminal technical evidence", key)
		}
	}
	return nil
}
