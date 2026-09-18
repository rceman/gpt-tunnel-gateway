package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	RuleStatusProposed = "proposed"
	RuleStatusAccepted = "accepted"
	RuleStatusArchived = "archived"
)

const (
	RuleTitleMaxRunes       = 128
	RuleSummaryMaxRunes     = 256
	RuleDescriptionMaxRunes = 16384
	RuleValueMaxBytes       = 8192
	RuleNameMaxRunes        = 128
)

// ruleNameRE is the closed machine-identity grammar: lowercase [a-z0-9_]
// segments optionally separated by single dots. Bare names are valid; leading,
// trailing, or doubled dots are not.
var ruleNameRE = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)

type Rule struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Revision      int             `json:"revision"`
	Title         string          `json:"title"`
	Summary       string          `json:"summary,omitempty"`
	Status        string          `json:"status"`
	Name          string          `json:"name,omitempty"`
	Value         json.RawMessage `json:"value,omitempty"`
	Description   string          `json:"description,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedBy     string          `json:"updated_by,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
	LastReason    string          `json:"last_reason,omitempty"`
	ArchivedAt    *time.Time      `json:"archived_at,omitempty"`
	ArchivedBy    string          `json:"archived_by,omitempty"`
	ArchiveReason string          `json:"archive_reason,omitempty"`
}

type RuleHistoryEntry struct {
	SchemaVersion int       `json:"schema_version"`
	Key           string    `json:"key"`
	ProjectID     string    `json:"project_id"`
	Revision      int       `json:"revision"`
	MutationKind  string    `json:"mutation_kind"`
	Actor         string    `json:"actor"`
	Reason        string    `json:"reason"`
	ChangedFields []string  `json:"changed_fields,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
}

func ValidateRuleName(name string) error {
	if name == "" || len([]rune(name)) > RuleNameMaxRunes || !ruleNameRE.MatchString(name) {
		return fmt.Errorf("invalid rule name")
	}
	return nil
}

// ValidateRuleValue enforces the bounded typed-JSON rule value contract:
// string, number, boolean, object, or array content only; null is not a valid
// rule value.
func ValidateRuleValue(value json.RawMessage) error {
	if len(value) == 0 {
		return fmt.Errorf("named rule requires a value")
	}
	if len(value) > RuleValueMaxBytes {
		return fmt.Errorf("rule value exceeds the serialized bound")
	}
	if !json.Valid(value) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return fmt.Errorf("invalid rule value")
	}
	return nil
}

// ValidateRule enforces the two valid Rule forms: a machine rule carries an
// immutable name plus a typed value (optionally a description), and a
// narrative rule carries only a description.
func ValidateRule(v Rule) error {
	if v.SchemaVersion != SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateRuleID(v.ID) != nil {
		return fmt.Errorf("invalid rule identity")
	}
	if v.Revision < 1 {
		return fmt.Errorf("invalid rule revision")
	}
	if title := len([]rune(v.Title)); title == 0 || title > RuleTitleMaxRunes || strings.TrimSpace(v.Title) != v.Title {
		return fmt.Errorf("invalid rule title")
	}
	if len([]rune(v.Summary)) > RuleSummaryMaxRunes {
		return fmt.Errorf("invalid rule summary")
	}
	if v.Status != RuleStatusProposed && v.Status != RuleStatusAccepted && v.Status != RuleStatusArchived {
		return fmt.Errorf("invalid rule status")
	}
	if v.Name != "" {
		if err := ValidateRuleName(v.Name); err != nil {
			return err
		}
		if err := ValidateRuleValue(v.Value); err != nil {
			return err
		}
	} else {
		if len(v.Value) != 0 {
			return fmt.Errorf("unnamed rule cannot carry a value")
		}
		if strings.TrimSpace(v.Description) == "" {
			return fmt.Errorf("narrative rule requires a description")
		}
	}
	if len([]rune(v.Description)) > RuleDescriptionMaxRunes || strings.ContainsRune(v.Description, 0) {
		return fmt.Errorf("invalid rule description")
	}
	if v.CreatedAt.IsZero() {
		return fmt.Errorf("invalid rule timestamps")
	}
	if v.Status == RuleStatusArchived {
		if v.ArchivedAt == nil || v.ArchivedAt.IsZero() || v.ArchivedBy == "" || strings.ContainsAny(v.ArchivedBy, "\r\n\x00") {
			return fmt.Errorf("invalid archived rule metadata")
		}
		if len(v.ArchiveReason) == 0 || len(v.ArchiveReason) > MaxDeferredReasonBytes || strings.ContainsAny(v.ArchiveReason, "\r\n\x00") {
			return fmt.Errorf("invalid archived rule reason")
		}
	}
	return nil
}
