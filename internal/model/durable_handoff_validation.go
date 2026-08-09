package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func containsOwnerTechnical(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"refs/heads/", "refs/remotes/", "feature/", "task/", "fix/", "release/", "ops/", "go test", "gofmt", "sha256sum", "git diff", "required gates", "prohibited operations"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return ownerIdentifierRE.MatchString(value)
}

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
