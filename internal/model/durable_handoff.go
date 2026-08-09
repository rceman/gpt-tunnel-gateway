package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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
var durableCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var ownerIdentifierRE = regexp.MustCompile(`(?i)([a-z0-9]+-(tsk|run|adr|opr)[a-z0-9-]*|report-[a-z0-9-]+|[0-9a-f]{40,64})`)

func containsOwnerTechnical(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"refs/heads/", "refs/remotes/", "feature/", "task/", "fix/", "release/", "ops/", "go test", "gofmt", "sha256sum", "git diff", "required gates", "prohibited operations"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return ownerIdentifierRE.MatchString(value)
}

// OwnerSummary is the concise owner-facing part of a durable handoff or
// planner report. Technical identifiers and exact proof belong in
// TechnicalEvidence instead.
type OwnerSummary struct {
	Status              string   `json:"status"`
	Goal                string   `json:"goal"`
	CurrentlyDoing      string   `json:"currently_doing"`
	WhyItMatters        string   `json:"why_it_matters"`
	CompletedSoFar      []string `json:"completed_so_far"`
	NextStep            string   `json:"next_step"`
	OwnerActionRequired *string  `json:"owner_action_required"`
}

type TaskRef struct {
	TaskID     string `json:"task_id"`
	TaskSHA256 string `json:"task_sha256"`
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
	PlanRevision          int             `json:"plan_revision"`
	HubRevision           string          `json:"hub_revision"`
	TaskRefs              []TaskRef       `json:"task_refs"`
	TrainRefs             []string        `json:"train_refs"`
	PlanSectionRefs       []string        `json:"plan_section_refs"`
	OperatorEventRefs     []string        `json:"operator_event_refs"`
	ExpectedRepoBase      string          `json:"expected_repo_base"`
	ExpectedRepoHead      string          `json:"expected_repo_head"`
	FirstAction           string          `json:"first_action"`
	StopBoundary          string          `json:"stop_boundary"`
	ProhibitedOperations  []string        `json:"prohibited_operations"`
	InstructionBody       string          `json:"instruction_body"`
	RoleRefs              []string        `json:"role_refs"`
	DelegationRefs        []string        `json:"delegation_refs"`
	AuthorRole            string          `json:"author_role"`
	ConsumerRole          string          `json:"consumer_role"`
	CanonicalDigest       string          `json:"canonical_digest"`
	CreatedBy             string          `json:"created_by"`
	AcknowledgedBy        string          `json:"acknowledged_by,omitempty"`
	StartedBy             string          `json:"started_by,omitempty"`
	CancelledBy           string          `json:"cancelled_by,omitempty"`
	CancelReason          string          `json:"cancel_reason,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	AcknowledgedAt        *time.Time      `json:"acknowledged_at,omitempty"`
	StartedAt             *time.Time      `json:"started_at,omitempty"`
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
	Status             string       `json:"status"`
}

// PlannerReportState is mutable lifecycle state for an immutable PlannerReport.
type PlannerReportState struct {
	SchemaVersion  int        `json:"schema_version"`
	ReportID       string     `json:"report_id"`
	ReportSHA256   string     `json:"report_sha256"`
	Status         string     `json:"status"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

const (
	DeliveryHandoffPending          = "pending"
	DeliveryHandoffAcknowledged     = "acknowledged"
	DeliveryHandoffInProgress       = "in_progress"
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
	PlannerReportPublished        = "published"
	PlannerReportAcknowledged     = "acknowledged_by_planner"
	PlannerReportResolved         = "resolved"
	PlannerReportSuperseded       = "superseded"
)

func ValidateOwnerSummary(v OwnerSummary) error {
	switch v.Status {
	case "working", "completed", "blocked", "decision_required":
	default:
		return fmt.Errorf("owner_summary.status is invalid")
	}
	fields := map[string]string{
		"goal":            v.Goal,
		"currently_doing": v.CurrentlyDoing,
		"why_it_matters":  v.WhyItMatters,
		"next_step":       v.NextStep,
	}
	for name, value := range fields {
		if !utf8.ValidString(value) || len([]byte(value)) == 0 || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("owner_summary.%s is empty, invalid, or oversized", name)
		}
		if containsOwnerTechnical(value) {
			return fmt.Errorf("owner_summary.%s contains technical detail", name)
		}
	}
	if len(v.CompletedSoFar) > 3 {
		return fmt.Errorf("owner_summary.completed_so_far has more than three items")
	}
	for i, value := range v.CompletedSoFar {
		if !utf8.ValidString(value) || len([]byte(value)) == 0 || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("owner_summary.completed_so_far[%d] is empty, invalid, or oversized", i)
		}
		if containsOwnerTechnical(value) {
			return fmt.Errorf("owner_summary.completed_so_far[%d] contains technical detail", i)
		}
	}
	if v.OwnerActionRequired != nil {
		value := *v.OwnerActionRequired
		if !utf8.ValidString(value) || len([]byte(value)) == 0 || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("owner_summary.owner_action_required is empty, invalid, or oversized")
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

func validateBoundedText(name, value string) error {
	if !utf8.ValidString(value) || len([]byte(value)) == 0 || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s is empty, invalid, or oversized", name)
	}
	return nil
}

func validateBoundedRefs(name string, refs []string) error {
	if len(refs) > 16 {
		return fmt.Errorf("%s has too many entries", name)
	}
	for i, ref := range refs {
		if err := validateBoundedText(fmt.Sprintf("%s[%d]", name, i), ref); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			return fmt.Errorf("%s contains duplicate reference %q", name, ref)
		}
		seen[ref] = true
	}
	return nil
}

func validateTaskRefs(refs []TaskRef) error {
	if len(refs) > 16 {
		return fmt.Errorf("task_refs has too many entries")
	}
	for i, ref := range refs {
		if err := ValidateObjectIdentifier(ref.TaskID); err != nil {
			return fmt.Errorf("task_refs[%d].task_id: %w", i, err)
		}
		if !durableSHA256RE.MatchString(ref.TaskSHA256) {
			return fmt.Errorf("task_refs[%d].task_sha256 is invalid", i)
		}
		for j := 0; j < i; j++ {
			if refs[j].TaskID == ref.TaskID {
				return fmt.Errorf("task_refs contains duplicate task_id %q", ref.TaskID)
			}
		}
	}
	return nil
}

func validateDeliveryPlan(v DeliveryHandoff) error {
	if v.PlanRevision < 1 {
		return fmt.Errorf("invalid plan_revision")
	}
	if !durableCommitRE.MatchString(v.HubRevision) {
		return fmt.Errorf("invalid hub_revision")
	}
	for name, value := range map[string]string{"first_action": v.FirstAction, "stop_boundary": v.StopBoundary, "instruction_body": v.InstructionBody} {
		if err := validateBoundedText(name, value); err != nil {
			return err
		}
	}
	if err := validateTaskRefs(v.TaskRefs); err != nil {
		return err
	}
	for name, refs := range map[string][]string{"train_refs": v.TrainRefs, "plan_section_refs": v.PlanSectionRefs, "operator_event_refs": v.OperatorEventRefs, "prohibited_operations": v.ProhibitedOperations, "role_refs": v.RoleRefs, "delegation_refs": v.DelegationRefs} {
		if err := validateBoundedRefs(name, refs); err != nil {
			return err
		}
	}
	if (v.ExpectedRepoBase != "" && !durableCommitRE.MatchString(v.ExpectedRepoBase)) || (v.ExpectedRepoHead != "" && !durableCommitRE.MatchString(v.ExpectedRepoHead)) {
		return fmt.Errorf("expected repository base/head are invalid")
	}
	if !durableSHA256RE.MatchString(v.CanonicalDigest) {
		return fmt.Errorf("invalid canonical_digest")
	}
	if v.AuthorRole != "planner" || v.ConsumerRole != "delivery" {
		return fmt.Errorf("invalid handoff role binding")
	}
	return nil
}

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
	case DeliveryHandoffPending, DeliveryHandoffAcknowledged, DeliveryHandoffInProgress, DeliveryHandoffCompleted, DeliveryHandoffBlocked, DeliveryHandoffAwaitingDecision, DeliveryHandoffCancelled, DeliveryHandoffSuperseded:
	default:
		return fmt.Errorf("invalid delivery handoff status")
	}
	if err := ValidateOwnerSummary(v.OwnerSummary); err != nil {
		return err
	}
	if err := ValidateTechnicalEvidence(v.TechnicalEvidence); err != nil {
		return err
	}
	if err := validateDeliveryPlan(v); err != nil {
		return err
	}
	expectedDigest, err := CanonicalDeliveryHandoffDigest(v)
	if err != nil || v.CanonicalDigest != expectedDigest {
		return fmt.Errorf("canonical_digest does not match immutable handoff payload")
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
	if v.Status == DeliveryHandoffInProgress && (v.StartedBy == "" || v.StartedAt == nil) {
		return fmt.Errorf("in-progress handoff requires start metadata")
	}
	switch v.Status {
	case DeliveryHandoffCompleted, DeliveryHandoffBlocked, DeliveryHandoffAwaitingDecision:
		if v.AcknowledgedBy == "" || v.AcknowledgedAt == nil || v.StartedBy == "" || v.StartedAt == nil {
			return fmt.Errorf("terminal handoff state requires acknowledgement and start metadata")
		}
	}
	if v.Status == DeliveryHandoffCancelled && (v.CancelReason == "" || v.CancelledAt == nil) {
		return fmt.Errorf("cancelled handoff requires cancellation metadata")
	}
	return nil
}

func ValidatePlannerReportState(v PlannerReportState) error {
	if v.SchemaVersion != DurableHandoffSchemaVersion {
		return fmt.Errorf("invalid planner report state schema_version")
	}
	if err := ValidateObjectIdentifier(v.ReportID); err != nil {
		return fmt.Errorf("report_id: %w", err)
	}
	if !durableSHA256RE.MatchString(v.ReportSHA256) {
		return fmt.Errorf("invalid report_sha256")
	}
	switch v.Status {
	case PlannerReportPublished, PlannerReportAcknowledged, PlannerReportResolved, PlannerReportSuperseded:
	default:
		return fmt.Errorf("invalid planner report state")
	}
	if v.UpdatedAt.IsZero() {
		return fmt.Errorf("planner report state updated_at is required")
	}
	if v.Status == PlannerReportAcknowledged && (v.AcknowledgedBy == "" || v.AcknowledgedAt == nil) {
		return fmt.Errorf("acknowledged planner report requires metadata")
	}
	if v.Status == PlannerReportResolved && (v.ResolvedBy == "" || v.ResolvedAt == nil) {
		return fmt.Errorf("resolved planner report requires metadata")
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
	if v.OwnerSummary.Status != v.ReportType {
		return fmt.Errorf("planner report_type must match owner_summary.status")
	}
	if err := ValidateOwnerSummary(v.OwnerSummary); err != nil {
		return err
	}
	if err := ValidateTechnicalEvidence(v.TechnicalEvidence); err != nil {
		return err
	}
	if err := validateTypedPlannerReportEvidence(v); err != nil {
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

func CanonicalPlannerReportDigest(v PlannerReport) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:]), nil
}

func evidenceText(fields map[string]json.RawMessage, name string) error {
	var value string
	if err := json.Unmarshal(fields[name], &value); err != nil || strings.TrimSpace(value) == "" || len([]byte(value)) > MaxOwnerSummaryFieldBytes || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("technical_evidence.%s is required and bounded", name)
	}
	return nil
}

func evidenceBoolFalse(fields map[string]json.RawMessage, name string) error {
	var value bool
	if err := json.Unmarshal(fields[name], &value); err != nil || value {
		return fmt.Errorf("technical_evidence.%s must be false", name)
	}
	return nil
}

func evidenceFacts(fields map[string]json.RawMessage, name string) error {
	var values []string
	if err := json.Unmarshal(fields[name], &values); err != nil || len(values) == 0 || len(values) > 8 {
		return fmt.Errorf("technical_evidence.%s must be a bounded string array", name)
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("technical_evidence.%s is invalid", name)
		}
		if err := evidenceText(map[string]json.RawMessage{name: encoded}, name); err != nil {
			return err
		}
	}
	return nil
}

type blockedPlannerReportEvidence struct {
	BlockerClass               *string  `json:"blocker_class"`
	Severity                   *string  `json:"severity"`
	FailedPrecondition         *string  `json:"failed_precondition"`
	VerifiedFacts              []string `json:"verified_facts"`
	PreservationResume         *string  `json:"preservation_resume"`
	SameRunCorrectionAvailable *bool    `json:"same_run_correction_available"`
}

type decisionPlannerReportEvidence struct {
	DecisionQuestion              *string  `json:"decision_question"`
	Options                       []string `json:"options"`
	Tradeoffs                     *string  `json:"tradeoffs"`
	Recommendation                *string  `json:"recommendation"`
	DeferralConsequence           *string  `json:"deferral_consequence"`
	PreservedState                *string  `json:"preserved_state"`
	UnauthorizedChoiceImplemented *bool    `json:"unauthorized_choice_implemented"`
}

type completedPlannerReportEvidence struct {
	Terminal         *bool   `json:"terminal"`
	Reviewed         *bool   `json:"reviewed"`
	TaskSHA256       *string `json:"task_sha256"`
	RunID            *string `json:"run_id"`
	DeliveryReportID *string `json:"delivery_report_id"`
	ReviewedHead     *string `json:"reviewed_head"`
}

func decodeClosedPlannerEvidence(data json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("technical_evidence has trailing content")
	}
	return nil
}

func requireEvidencePointer(name string, value *string) error {
	if value == nil {
		return fmt.Errorf("technical_evidence.%s is required", name)
	}
	return validateBoundedText("technical_evidence."+name, *value)
}

func validateTypedPlannerReportEvidence(v PlannerReport) error {
	switch v.ReportType {
	case PlannerReportBlocked:
		var evidence blockedPlannerReportEvidence
		if err := decodeClosedPlannerEvidence(v.TechnicalEvidence, &evidence); err != nil {
			return fmt.Errorf("blocked technical_evidence is closed and invalid: %w", err)
		}
		for name, value := range map[string]*string{"blocker_class": evidence.BlockerClass, "severity": evidence.Severity, "failed_precondition": evidence.FailedPrecondition, "preservation_resume": evidence.PreservationResume} {
			if err := requireEvidencePointer(name, value); err != nil {
				return err
			}
		}
		if evidence.Severity != nil {
			switch *evidence.Severity {
			case "low", "medium", "high", "critical":
			default:
				return fmt.Errorf("technical_evidence.severity is invalid")
			}
		}
		if len(evidence.VerifiedFacts) == 0 || len(evidence.VerifiedFacts) > 8 {
			return fmt.Errorf("technical_evidence.verified_facts must be a bounded string array")
		}
		for _, fact := range evidence.VerifiedFacts {
			if err := validateBoundedText("technical_evidence.verified_facts", fact); err != nil {
				return err
			}
		}
		if evidence.SameRunCorrectionAvailable == nil || *evidence.SameRunCorrectionAvailable {
			return fmt.Errorf("technical_evidence.same_run_correction_available must be false")
		}
	case PlannerReportDecisionRequired:
		var evidence decisionPlannerReportEvidence
		if err := decodeClosedPlannerEvidence(v.TechnicalEvidence, &evidence); err != nil {
			return fmt.Errorf("decision technical_evidence is closed and invalid: %w", err)
		}
		for name, value := range map[string]*string{"decision_question": evidence.DecisionQuestion, "tradeoffs": evidence.Tradeoffs, "recommendation": evidence.Recommendation, "deferral_consequence": evidence.DeferralConsequence, "preserved_state": evidence.PreservedState} {
			if err := requireEvidencePointer(name, value); err != nil {
				return err
			}
		}
		if len(evidence.Options) == 0 || len(evidence.Options) > 8 {
			return fmt.Errorf("technical_evidence.options must be a bounded string array")
		}
		for _, option := range evidence.Options {
			if err := validateBoundedText("technical_evidence.options", option); err != nil {
				return err
			}
		}
		if evidence.UnauthorizedChoiceImplemented == nil || *evidence.UnauthorizedChoiceImplemented {
			return fmt.Errorf("technical_evidence.unauthorized_choice_implemented must be false")
		}
	case PlannerReportCompleted:
		var evidence completedPlannerReportEvidence
		if err := decodeClosedPlannerEvidence(v.TechnicalEvidence, &evidence); err != nil {
			return fmt.Errorf("completed technical_evidence is closed and invalid: %w", err)
		}
		if evidence.Terminal == nil || !*evidence.Terminal || evidence.Reviewed == nil || !*evidence.Reviewed {
			return fmt.Errorf("completed technical_evidence requires terminal and reviewed true")
		}
		for name, value := range map[string]*string{"task_sha256": evidence.TaskSHA256, "run_id": evidence.RunID, "delivery_report_id": evidence.DeliveryReportID, "reviewed_head": evidence.ReviewedHead} {
			if err := requireEvidencePointer(name, value); err != nil {
				return err
			}
		}
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
