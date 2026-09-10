package model

import (
	"fmt"
	"strings"
)

func ValidateProject(v Project) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported project schema_version")
	}
	if !idRE.MatchString(v.ID) {
		return fmt.Errorf("invalid project id")
	}
	if v.RepositoryURL == "" || len(v.RepositoryURL) > 2048 {
		return fmt.Errorf("invalid repository_url")
	}
	if err := ValidateBranch(v.DefaultBranch); err != nil {
		return err
	}
	return nil
}

func ValidatePlan(v Plan) error {
	if v.SchemaVersion != PlanSchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid plan identity")
	}
	if v.Revision < 1 || len(v.Title) < 1 || len(v.Title) > 300 || len(v.Summary) < 1 || len(v.Summary) > 500 || len(v.CurrentObjective) > 20000 {
		return fmt.Errorf("invalid plan content")
	}
	if len(v.Queue) > 200 || len(v.Sections) > 200 {
		return fmt.Errorf("plan bounds exceeded")
	}
	seen := map[string]bool{}
	for _, id := range v.Queue {
		if err := ValidateObjectIdentifier(id); err != nil {
			return fmt.Errorf("invalid plan queue item: %w", err)
		}
	}
	for _, section := range v.Sections {
		if err := ValidatePlanSectionIndex(section); err != nil {
			return err
		}
		if seen[section.ID] {
			return fmt.Errorf("duplicate plan section %q", section.ID)
		}
		seen[section.ID] = true
	}
	if v.ActiveTaskID != "" {
		if err := ValidateObjectIdentifier(v.ActiveTaskID); err != nil {
			return fmt.Errorf("invalid active task: %w", err)
		}
	}
	if v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\r\n\x00") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid plan update metadata")
	}
	return nil
}

func ValidatePlanSectionIndex(v PlanSectionIndex) error {
	if err := ValidateObjectIdentifier(v.ID); err != nil {
		return fmt.Errorf("invalid plan section identity: %w", err)
	}
	if len(v.Title) < 1 || len(v.Title) > 300 || len(v.ShortDescription) < 1 || len(v.ShortDescription) > 500 || strings.ContainsAny(v.ShortDescription, "\r\n\x00") || v.Revision < 1 {
		return fmt.Errorf("invalid plan section index")
	}
	return nil
}

func ValidatePlanSection(v PlanSection) error {
	if v.SchemaVersion != PlanSchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid plan section identity")
	}
	if err := ValidatePlanSectionIndex(PlanSectionIndex{
		ID:               v.ID,
		Title:            v.Title,
		ShortDescription: v.ShortDescription,
		Revision:         v.Revision,
	}); err != nil {
		return err
	}
	if len(v.Description) > 200000 || strings.ContainsRune(v.Description, 0) || v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\r\n\x00") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid plan section content")
	}
	return nil
}

func ValidateADR(v ADR) error {
	if v.SchemaVersion != SchemaVersion || !idRE.MatchString(v.ProjectID) {
		return fmt.Errorf("invalid ADR identity")
	}
	if err := validateAnyADRIdentifier(v.ID); err != nil {
		return err
	}
	if v.Supersedes != "" {
		if err := validateAnyADRIdentifier(v.Supersedes); err != nil {
			return fmt.Errorf("invalid supersedes: %w", err)
		}
	}
	if v.ReplacedBy != "" {
		if err := validateAnyADRIdentifier(v.ReplacedBy); err != nil {
			return fmt.Errorf("invalid replaced_by: %w", err)
		}
	}
	if len(v.Title) < 3 || len(v.Title) > 300 || len(v.Context) > 100000 || len(v.Decision) > 100000 || len(v.Consequences) > 100000 {
		return fmt.Errorf("invalid ADR content")
	}
	if v.Status != ADRStatusProposed && v.Status != ADRStatusAccepted && v.Status != ADRStatusSuperseded && v.Status != ADRStatusArchived {
		return fmt.Errorf("invalid ADR status")
	}
	if v.Status == ADRStatusArchived {
		if v.ArchivedAt == nil || v.ArchivedAt.IsZero() || v.ArchivedBy == "" || strings.ContainsAny(v.ArchivedBy, "\r\n\x00") {
			return fmt.Errorf("invalid archived ADR metadata")
		}
		if len([]byte(v.ArchiveReason)) == 0 || len([]byte(v.ArchiveReason)) > MaxDeferredReasonBytes || strings.ContainsAny(v.ArchiveReason, "\r\n\x00") {
			return fmt.Errorf("invalid archived ADR reason")
		}
		if v.ReplacedBy == v.ID {
			return fmt.Errorf("archived ADR cannot replace itself")
		}
	}
	return nil
}
