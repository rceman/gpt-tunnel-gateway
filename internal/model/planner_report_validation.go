package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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
