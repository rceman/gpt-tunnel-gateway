package model

import (
	"fmt"
	"strings"
)

func ValidateOperatorJournalCounter(v OperatorJournalCounter) error {
	if v.SchemaVersion != OperatorJournalSchemaVersion {
		return fmt.Errorf("invalid operator journal counter schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateCompactIDNumber(v.NextEventNumber); err != nil {
		return fmt.Errorf("next_event_number: %w", err)
	}
	return nil
}

func ValidateOperatorJournalKind(v OperatorJournalKind) error {
	if !operatorJournalKinds[v] {
		return fmt.Errorf("invalid operator journal kind %q", v)
	}
	return nil
}

func ValidateOperatorJournalContent(v OperatorJournalContent) error {
	values := map[string][]string{"decisions": v.Decisions, "commitments": v.Commitments, "facts": v.Facts, "assumptions": v.Assumptions, "blockers": v.Blockers, "unresolved": v.Unresolved, "next_actions": v.NextActions}
	for name, items := range values {
		if len(items) > MaxOperatorContentItems {
			return fmt.Errorf("%s contains too many entries", name)
		}
		for _, item := range items {
			if err := validateOperatorText(item, MaxOperatorContentItemBytes, name, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v OperatorJournalContent) HasMaterial() bool {
	return len(v.Decisions)+len(v.Commitments)+len(v.Facts)+len(v.Assumptions)+len(v.Blockers)+len(v.Unresolved)+len(v.NextActions) > 0
}

func ValidateOperatorJournalReferences(v OperatorJournalReferences) error {
	return ValidateOperatorJournalReferencesForProject(v, "")
}

// ValidateOperatorJournalReferencesForProject validates journal references and,
// when a project code is supplied, binds compact ADR references to that
// project's adopted code. Legacy ADR-* references remain globally valid.
func ValidateOperatorJournalReferencesForProject(v OperatorJournalReferences, projectCode string) error {
	if projectCode != "" {
		if err := ValidateProjectCode(projectCode); err != nil {
			return err
		}
	}
	values := map[string][]string{"plan_sections": v.PlanSections, "adrs": v.ADRs, "tasks": v.Tasks, "runs": v.Runs, "commits": v.Commits, "identities": v.Identities}
	for name, items := range values {
		if len(items) > MaxOperatorReferenceItems {
			return fmt.Errorf("%s contains too many references", name)
		}
		seen := map[string]bool{}
		for _, item := range items {
			if err := validateOperatorText(item, MaxOperatorContentItemBytes, name, true); err != nil {
				return err
			}
			if seen[item] {
				return fmt.Errorf("duplicate %s reference %q", name, item)
			}
			seen[item] = true
			switch name {
			case "plan_sections":
				if err := ValidateObjectIdentifier(item); err != nil {
					return fmt.Errorf("plan_sections: %w", err)
				}
			case "adrs":
				if strings.HasPrefix(item, "ADR-") {
					if err := ValidateADRIdentifier(item); err != nil {
						return fmt.Errorf("adrs: %w", err)
					}
					continue
				}
				code, _, err := ParseADRID(item)
				if err != nil {
					code, _, err = ParseHistoricalADRID(item)
					if err != nil {
						return fmt.Errorf("adrs: %w", err)
					}
				}
				if projectCode != "" && code != projectCode {
					return fmt.Errorf("adrs: compact ADR project code %q does not match expected project code %q", code, projectCode)
				}
			case "tasks", "runs":
				if err := ValidateObjectIdentifier(item); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			case "commits":
				if err := ValidateCommitSHA(item); err != nil {
					return fmt.Errorf("commits: %w", err)
				}
			}
		}
	}
	return nil
}

func ValidateOperatorJournalEvent(v OperatorJournalEvent) error {
	if v.SchemaVersion != OperatorJournalSchemaVersion {
		return fmt.Errorf("invalid operator journal event schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateAnyOperatorEventID(v.ID); err != nil {
		return err
	}
	if err := ValidateOperatorJournalKind(v.Kind); err != nil {
		return err
	}
	if err := validateOperatorText(v.Summary, MaxOperatorSummaryBytes, "summary", true); err != nil {
		return err
	}
	if v.SessionID != nil {
		if err := validateOperatorText(*v.SessionID, MaxOperatorSessionIDBytes, "session_id", true); err != nil {
			return err
		}
	}
	if err := ValidateOperatorJournalContent(v.Content); err != nil {
		return err
	}
	if err := ValidateOperatorJournalReferences(v.References); err != nil {
		return err
	}
	if !v.Content.HasMaterial() && !operatorReferencesHaveMaterial(v.References) {
		return fmt.Errorf("operator journal event must contain material content or references")
	}
	if err := validateOperatorText(v.Actor, MaxOperatorActorBytes, "actor", true); err != nil {
		return err
	}
	if v.OccurredAt.IsZero() || v.RecordedAt.IsZero() {
		return fmt.Errorf("operator journal timestamps are required")
	}
	if v.OccurredAt.After(v.RecordedAt) {
		return fmt.Errorf("occurred_at cannot be after recorded_at")
	}
	if v.SupersedesEventID != "" {
		if err := ValidateAnyOperatorEventID(v.SupersedesEventID); err != nil {
			return fmt.Errorf("supersedes_event_id: %w", err)
		}
		if v.Kind != OperatorCorrection {
			return fmt.Errorf("supersedes_event_id requires correction kind")
		}
	} else if v.Kind == OperatorCorrection {
		return fmt.Errorf("correction kind requires supersedes_event_id")
	}
	return nil
}

func FormatOperatorEventID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-OPR%d", projectCode, number), nil
}

func ParseOperatorEventID(value string) (string, uint64, error) {
	matches := operatorEventIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid canonical operator event ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ParseHistoricalOperatorEventID(value string) (string, uint64, error) {
	matches := historicalOperatorEventIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical operator event ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ValidateAnyOperatorEventID(value string) error {
	if _, _, err := ParseOperatorEventID(value); err == nil {
		return nil
	}
	_, _, err := ParseHistoricalOperatorEventID(value)
	return err
}

func ValidateOperatorEventID(value string) error {
	_, _, err := ParseOperatorEventID(value)
	return err
}

func ValidateOperatorEventIDForProject(value, projectCode string) error {
	if err := ValidateProjectCode(projectCode); err != nil {
		return err
	}
	code, _, err := ParseOperatorEventID(value)
	if err != nil {
		return err
	}
	if code != projectCode {
		return fmt.Errorf("operator event ID project code %q does not match expected project code %q", code, projectCode)
	}
	return nil
}
